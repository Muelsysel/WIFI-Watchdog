using System.Collections.ObjectModel;
using System.Diagnostics;
using System.Globalization;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;
using Microsoft.Win32;
using WiFiWatchdog.Studio.Core;

namespace WiFiWatchdog.Studio;

public sealed record ChartBar(string Label, int Count, double Height, Brush BarBrush);
public sealed record EndpointRow(string Name, string Verdict, string StatusText, string TimeText, string Detail);
public sealed record EventRow(string TimestampText, string Level, string Message, WatchdogEvent Source);

public partial class MainWindow : Window
{
    private readonly ConfigStore configStore = new(AppPaths.Config);
    private readonly PresetStore presetStore = new(AppPaths.Presets);
    private readonly DispatcherTimer uiTimer;
    private IReadOnlyList<WatchdogEvent> events = [];
    private DiagnosticSnapshot? lastDiagnostic;
    private CancellationTokenSource? diagnosticCancellation;
    private readonly Dictionary<TextBox, (string key, int lo, int hi)> numericFields = new();
    private JsonObject loadedConfig = ConfigStore.Defaults();
    private string enginePath = "";
    private bool refreshing;
    public ObservableCollection<ChartBar> ChartBars { get; } = new();

    public MainWindow()
    {
        InitializeComponent();
        DataContext = this;
        InitFields();
        LoadPreferences();
        PopulateConfig();
        RefreshPresetNames();
        Navigate("OverviewPage");
        uiTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(60) };
        uiTimer.Tick += async (_, _) => await RefreshAllAsync();
        uiTimer.Start();
        Loaded += async (_, _) => await RefreshAllAsync();
        Closing += (_, _) =>
        {
            uiTimer.Stop();
            diagnosticCancellation?.Cancel();
            diagnosticCancellation?.Dispose();
        };
    }

    private void InitFields()
    {
        Add(CfgNormal, "normalCheckIntervalMinutes", 1, 1440);
        Add(CfgFailureSeconds, "failureCheckIntervalSeconds", 1, 3600);
        Add(CfgFailureCount, "failureThreshold", 1, 100);
        Add(CfgCooldown, "repairRetryIntervalMinutes", 1, 1440);
        Add(CfgTimeout, "connectionTimeoutSeconds", 1, 30);
        Add(CfgRetention, "logRetentionDays", 1, 3650);
        Add(CfgDisableWait, "wifiDisableWaitSeconds", 1, 120);
        Add(CfgStartupWait, "wifiStartupWaitSeconds", 1, 600);
        Add(CfgRetry, "connectRetryCount", 1, 10);
        Add(CfgRetryDelay, "connectRetryDelaySeconds", 1, 60);
        Add(CfgDhcp, "dhcpRenewWaitSeconds", 1, 120);
        Add(CfgMixed, "vpnLocalPort", 0, 65535);
        Add(CfgController, "mihomoControllerPort", 0, 65535);
    }

    private void Add(TextBox box, string name, int lo, int hi) => numericFields.Add(box, (name, lo, hi));

    private void PopulateConfig(JsonObject? config = null)
    {
        try
        {
            loadedConfig = config ?? configStore.Read();
            var defaults = ConfigStore.Defaults();
            foreach (var (box, field) in numericFields)
            {
                var fallback = ConfigStore.GetInt(defaults, field.key, field.lo);
                box.Text = ConfigStore.GetInt(loadedConfig, field.key, fallback).ToString(CultureInfo.InvariantCulture);
            }
            CfgAutoReconnect.IsChecked = ConfigStore.GetBool(loadedConfig, "autoReconnectDisconnected", false);
            CfgWlanSvc.IsChecked = ConfigStore.GetBool(loadedConfig, "enableWlanServiceRestart", false);
            CfgVpnAware.IsChecked = ConfigStore.GetBool(loadedConfig, "enableVpnAware", true);
        }
        catch (Exception ex)
        {
            Status("配置读取失败，未覆盖原文件：" + ex.Message);
            MessageBox.Show(this, "无法读取便携版配置。原文件保持不变。\n" + ex.Message, "配置错误",
                MessageBoxButton.OK, MessageBoxImage.Warning);
        }
    }

    private JsonObject CaptureConfig()
    {
        // Preserve keys not understood by Studio so newer portable engines can
        // extend the schema without Studio silently erasing their settings.
        var config = (JsonObject)loadedConfig.DeepClone();
        foreach (var (box, field) in numericFields)
        {
            if (!int.TryParse(box.Text, NumberStyles.Integer, CultureInfo.InvariantCulture, out var number)
                || number < field.lo || number > field.hi)
                throw new InvalidDataException($"{field.key} 必须介于 {field.lo}～{field.hi}。");
            config[field.key] = number;
        }
        config["autoReconnectDisconnected"] = CfgAutoReconnect.IsChecked == true;
        config["enableWlanServiceRestart"] = CfgWlanSvc.IsChecked == true;
        config["enableVpnAware"] = CfgVpnAware.IsChecked == true;
        ConfigStore.Validate(config);
        return config;
    }

    private void Status(string message) => StatusText.Text = message;

    private void Navigate(string pageName)
    {
        var views = new Dictionary<string, StackPanel>
        {
            ["OverviewPage"] = OverviewPage, ["DiagnosticsPage"] = DiagnosticsPage,
            ["HistoryPage"] = HistoryPage, ["SettingsPage"] = SettingsPage, ["AboutPage"] = AboutPage
        };
        var titles = new Dictionary<string, (string title, string hint)>
        {
            ["OverviewPage"] = ("网络概览", "掌握当前网络健康和恢复引擎状态"),
            ["DiagnosticsPage"] = ("智能诊断", "完整的 HTTP、Mihomo 和混合代理检测"),
            ["HistoryPage"] = ("历史事件", "分析异常、恢复次数与运行趋势"),
            ["SettingsPage"] = ("监控策略", "安全配置恢复引擎并管理多套方案"),
            ["AboutPage"] = ("产品与引擎", "查看权限边界、路径与发布信息")
        };
        foreach (var pair in views) pair.Value.Visibility = pair.Key == pageName ? Visibility.Visible : Visibility.Collapsed;
        var navs = new[] { NavOverview, NavDiagnostics, NavHistory, NavSettings, NavAbout };
        foreach (var nav in navs)
            nav.Background = (string?)nav.Tag == pageName
                ? new SolidColorBrush(Color.FromRgb(43, 67, 110)) : Brushes.Transparent;
        PageHeadline.Text = titles[pageName].title;
        PageHint.Text = titles[pageName].hint;
    }

    private void Navigate_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: string name }) Navigate(name);
    }
    private void OpenSettings_Click(object sender, RoutedEventArgs e) => Navigate("SettingsPage");
    private void OpenHistory_Click(object sender, RoutedEventArgs e) => Navigate("HistoryPage");

    private async Task RefreshAllAsync()
    {
        if (refreshing) return;
        refreshing = true;
        Status("正在读取本机事件与最近 14 天历史…");
        try
        {
            var result = await Task.Run(() =>
            {
                var logs = LogHistory.Recent(AppPaths.Logs);
                var days = LogHistory.LastDays(AppPaths.Logs);
                return (logs, days);
            });
            events = result.logs;
            ApplyLogFilter();
            ChartBars.Clear();
            var max = Math.Max(1, result.days.Max(d => d.Warn + d.Error));
            foreach (var day in result.days)
            {
                var count = day.Warn + day.Error;
                ChartBars.Add(new(day.Date[^5..], count, count == 0 ? 4 : 7 + 91.0 * count / max,
                    count == 0 ? new SolidColorBrush(Color.FromRgb(221, 231, 243))
                    : new SolidColorBrush(Color.FromRgb(83, 115, 220))));
            }
            var today = result.days.Last();
            WarningsValue.Text = (today.Warn + today.Error).ToString(CultureInfo.InvariantCulture);
            RepairsValue.Text = today.Recoveries.ToString(CultureInfo.InvariantCulture);
            if (events.Count == 0)
            {
                HealthValue.Text = "等待数据";
                HealthDetail.Text = "尚无引擎日志，可能未启动或使用了其他数据目录";
            }
            else
            {
                var recent = events[0];
                var old = DateTimeOffset.Now - recent.Timestamp > TimeSpan.FromMinutes(25);
                HealthValue.Text = old ? "数据待更新" : recent.Level == "ERROR" ? "需要关注" : "运行记录正常";
                HealthDetail.Text = $"{recent.Timestamp:HH:mm} · {recent.Message}";
            }
            EngineIndicator.Text = EngineIsRunning() ? "● 便携引擎运行中" : "○ 便携引擎未运行";
            Status($"刷新完成 · 读取 {events.Count} 条近期事件");
        }
        catch (Exception ex) { Status("读取日志失败：" + ex.Message); }
        finally { refreshing = false; }
    }

    private void RefreshAll_Click(object sender, RoutedEventArgs e) => _ = RefreshAllAsync();
    private void RefreshHistory_Click(object sender, RoutedEventArgs e) => _ = RefreshAllAsync();

    private void ApplyLogFilter()
    {
        if (EventList is null || LogLevelFilter is null || LogSearch is null) return;
        var level = (LogLevelFilter.SelectedItem as ComboBoxItem)?.Content?.ToString() ?? "全部级别";
        var query = LogSearch.Text?.Trim() ?? "";
        var filtered = events.Where(x => (level == "全部级别" || x.Level == level) &&
            (query.Length == 0 || x.Message.Contains(query, StringComparison.CurrentCultureIgnoreCase)))
            .Take(500).Select(x => new EventRow(x.Timestamp.ToString("yyyy-MM-dd HH:mm:ss"),
                x.Level, x.Message, x)).ToArray();
        EventList.ItemsSource = filtered;
        LogCount.Text = $"当前显示 {filtered.Length} 条 / 已读取 {events.Count} 条（最近 3 天，最多 500 条）";
    }
    private void LogFilter_Changed(object sender, RoutedEventArgs e) => ApplyLogFilter();

    private async void RunDiagnostics_Click(object sender, RoutedEventArgs e)
    {
        if (diagnosticCancellation is not null) return;
        Navigate("DiagnosticsPage");
        diagnosticCancellation = new CancellationTokenSource();
        RunDiagButton.IsEnabled = false;
        var token = diagnosticCancellation.Token;
        DiagnosticSummary.Text = "正在逐项检查 HTTP / Mihomo / 混合代理…";
        Status("网络诊断运行中：不会重启网卡，也不会修改 Mihomo");
        try
        {
            var config = configStore.Read();
            var ctl = ConfigStore.GetInt(config, "mihomoControllerPort", 9097);
            var mixed = ConfigStore.GetInt(config, "vpnLocalPort", 0);
            lastDiagnostic = await NetworkDiagnostics.RunAsync(ctl, mixed, SecretBox.Password, token);
            DiagnosticTime.Text = $"检测完成：{lastDiagnostic.Created:yyyy-MM-dd HH:mm:ss}";
            DiagnosticSummary.Text = lastDiagnostic.Diagnosis;
            ControllerDetail.Text = $"Mihomo：{lastDiagnostic.Controller.Description}；模式={lastDiagnostic.Controller.Mode}；TUN={lastDiagnostic.Controller.Tun}";
            MixedDetail.Text = lastDiagnostic.MixedProxy is null ? "Mixed-Port 未设置且无法发现，未执行代理出口检测"
                : $"Mixed-Port HTTPS：{(lastDiagnostic.MixedProxy.Valid ? "验证成功" : "未验证")}；{lastDiagnostic.MixedProxy.Milliseconds} ms";
            EndpointList.ItemsSource = lastDiagnostic.Endpoints.Select(x =>
                new EndpointRow(x.Name, x.Valid ? "通过" : "未通过", x.Status?.ToString() ?? "—",
                    x.Milliseconds + " ms", x.Detail)).ToArray();
            Status($"诊断完成：四个端点通过 {lastDiagnostic.Passed} 个；不自动执行修复");
        }
        catch (OperationCanceledException) { Status("用户已取消诊断"); }
        catch (Exception ex) { Status("诊断异常：" + ex.Message); DiagnosticSummary.Text = "诊断未完成：" + ex.Message; }
        finally
        {
            diagnosticCancellation?.Dispose();
            diagnosticCancellation = null;
            RunDiagButton.IsEnabled = true;
            SecretBox.Clear(); // Secret is never persisted.
        }
    }

    private void ExportDiagnostic_Click(object sender, RoutedEventArgs e)
    {
        if (lastDiagnostic is null) { Status("请先运行一次完整诊断"); return; }
        var dlg = new SaveFileDialog { Filter = "JSON 诊断报告 (*.json)|*.json", FileName = "watchdog-studio-diagnostic.json" };
        if (dlg.ShowDialog(this) != true) return;
        try { ConfigStore.WriteAtomic(dlg.FileName, ReportExporter.ToJson(lastDiagnostic)); Status("已导出不含 Secret/SSID 的诊断摘要"); }
        catch (Exception ex) { Error("导出报告失败", ex); }
    }

    private void ExportEvents_Click(object sender, RoutedEventArgs e)
    {
        var dlg = new SaveFileDialog { Filter = "CSV (*.csv)|*.csv", FileName = "watchdog-events.csv" };
        if (dlg.ShowDialog(this) != true) return;
        try
        {
            var rows = (EventList.ItemsSource as IEnumerable<EventRow>)?.Select(x => x.Source) ?? [];
            ConfigStore.WriteAtomic(dlg.FileName, ReportExporter.EventsCsv(rows));
            Status("事件 CSV 已导出；其中可能包含 SSID/接口信息，请勿直接公开分享");
        }
        catch (Exception ex) { Error("导出事件失败", ex); }
    }

    private void SaveSettings_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            var updated = CaptureConfig();
            configStore.Save(updated);
            loadedConfig = updated;
            Status("配置已安全保存，并备份上一版本为 config.json.bak");
            MessageBox.Show(this, EngineIsRunning()
                ? "已写入 config.json。当前便携引擎仍使用旧的内存配置；请从托盘正常退出并重新启动，才能应用新参数。"
                : "配置已保存。下次启动便携引擎时自动生效。", "保存成功",
                MessageBoxButton.OK, MessageBoxImage.Information);
        }
        catch (Exception ex) { Error("保存配置失败", ex); }
    }

    private void RefreshPresetNames()
    {
        try { PresetList.ItemsSource = presetStore.List(); }
        catch (Exception ex) { Status("读取方案失败：" + ex.Message); }
    }

    private void SavePreset_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            var name = PresetName.Text.Trim();
            if (string.IsNullOrWhiteSpace(name)) { Status("请先输入方案名称"); return; }
            presetStore.Save(name, CaptureConfig());
            RefreshPresetNames();
            PresetList.SelectedItem = name;
            Status("已保存方案：" + name);
        }
        catch (Exception ex) { Error("保存方案失败", ex); }
    }
    private void LoadPreset_Click(object sender, RoutedEventArgs e)
    {
        if (PresetList.SelectedItem is not string name) return;
        try { PopulateConfig(presetStore.Load(name)); Status("已将方案载入编辑器；点击“保存到引擎配置”才会应用"); }
        catch (Exception ex) { Error("载入方案失败", ex); }
    }
    private void DeletePreset_Click(object sender, RoutedEventArgs e)
    {
        if (PresetList.SelectedItem is not string name) return;
        if (MessageBox.Show(this, $"确认删除方案“{name}”？此操作不修改引擎配置。", "删除方案",
            MessageBoxButton.YesNo, MessageBoxImage.Question) != MessageBoxResult.Yes) return;
        try { presetStore.Remove(name); RefreshPresetNames(); Status("已删除方案：" + name); }
        catch (Exception ex) { Error("删除方案失败", ex); }
    }

    private static bool EngineIsRunning()
    {
        try
        {
            var all = Process.GetProcessesByName("WiFiWatchdog");
            foreach (var item in all) item.Dispose();
            return all.Length > 0;
        }
        catch { return false; }
    }

    private string PreferencesPath => Path.Combine(AppPaths.Studio, "preferences.json");
    private void LoadPreferences()
    {
        enginePath = Path.Combine(AppContext.BaseDirectory, "Engine", "WiFiWatchdog.exe");
        try
        {
            if (File.Exists(PreferencesPath))
            {
                using var doc = JsonDocument.Parse(File.ReadAllText(PreferencesPath));
                if (doc.RootElement.TryGetProperty("enginePath", out var value) &&
                    value.ValueKind == JsonValueKind.String && !string.IsNullOrEmpty(value.GetString()))
                    enginePath = value.GetString()!;
            }
        }
        catch (Exception ex) { Status("读取 Studio 偏好设置失败：" + ex.Message); }
        EnginePathText.Text = enginePath;
    }
    private void BrowseEngine_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new OpenFileDialog { Filter = "Watchdog executable (*.exe)|*.exe", Title = "选择便携版 WiFiWatchdog.exe" };
        if (dialog.ShowDialog(this) != true) return;
        enginePath = dialog.FileName;
        try
        {
            ConfigStore.WriteAtomic(PreferencesPath, JsonSerializer.Serialize(new { enginePath }));
            EnginePathText.Text = enginePath;
            Status("已记录本机恢复引擎路径");
        }
        catch (Exception ex) { Error("保存引擎位置失败", ex); }
    }

    private void StartEngine_Click(object sender, RoutedEventArgs e)
    {
        if (EngineIsRunning()) { Status("便携引擎已在运行，请从系统托盘管理"); return; }
        if (!File.Exists(enginePath))
        {
            Navigate("AboutPage");
            Status("未找到便携引擎，请点击“选择引擎 EXE”");
            return;
        }
        if (MessageBox.Show(this, "便携引擎需要管理员权限以执行网卡恢复。是否启动并允许 Windows 显示 UAC 确认？",
            "启动恢复引擎", MessageBoxButton.YesNo, MessageBoxImage.Question) != MessageBoxResult.Yes) return;
        try
        {
            Process.Start(new ProcessStartInfo(enginePath) { UseShellExecute = true, Verb = "runas",
                WorkingDirectory = Path.GetDirectoryName(enginePath)! });
            Status("已请求启动引擎；如出现 Windows UAC，请确认授权");
        }
        catch (System.ComponentModel.Win32Exception) { Status("已取消管理员权限请求，未启动引擎"); }
        catch (Exception ex) { Error("启动引擎失败", ex); }
    }

    private static void OpenFolder(string directory)
    {
        Directory.CreateDirectory(directory);
        var start = new ProcessStartInfo("explorer.exe") { UseShellExecute = true };
        start.ArgumentList.Add(directory);
        Process.Start(start);
    }
    private void OpenLogFolder_Click(object sender, RoutedEventArgs e) => OpenFolder(AppPaths.Logs);
    private void OpenDataFolder_Click(object sender, RoutedEventArgs e) => OpenFolder(AppPaths.Root);
    private void OpenGithub_Click(object sender, RoutedEventArgs e)
        => Process.Start(new ProcessStartInfo("https://github.com/Muelsysel/WIFI-Watchdog") { UseShellExecute = true });

    private void Error(string title, Exception ex)
    {
        Status($"{title}：{ex.Message}");
        MessageBox.Show(this, ex.Message, title, MessageBoxButton.OK, MessageBoxImage.Error);
    }
}
