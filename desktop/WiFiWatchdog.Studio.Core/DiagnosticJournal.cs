using System.Text.Json;

namespace WiFiWatchdog.Studio.Core;

public sealed record DiagnosticRecord(
    DateTimeOffset Created, int Passed, int Total, string Conclusion,
    string Controller, string Tun, bool? MixedProxyValid,
    IReadOnlyList<EndpointRecord> Endpoints);
public sealed record EndpointRecord(string Name, bool Passed, int? HttpCode, long DurationMs);

public sealed class DiagnosticJournal(string directory)
{
    private const int MaxEntries = 60;
    private static readonly TimeSpan Retention = TimeSpan.FromDays(45);
    public static DiagnosticRecord Project(DiagnosticSnapshot report) => new(
        report.Created, report.Passed, report.Endpoints.Count, report.Diagnosis,
        report.Controller.Available ? report.Controller.AuthRequired ? "需要认证" : "API可访问" : "未知/无法连接",
        report.Controller.Tun,
        report.MixedProxy?.Valid,
        report.Endpoints.Take(8).Select(x => new EndpointRecord(
            x.Name, x.Valid, x.Status, x.Milliseconds)).ToArray());

    // Only store a limited summary. No SSID/IP/DNS/Secret, controller messages,
    // exception text or other potentially identifying diagnostics are persisted.
    public void Add(DiagnosticSnapshot report) => AddRecord(Project(report));

    public void AddRecord(DiagnosticRecord record)
    {
        Directory.CreateDirectory(directory);
        var name = "diag-" + record.Created.ToUniversalTime().ToString("yyyyMMdd-HHmmss") +
                   "-" + Guid.NewGuid().ToString("N")[..8] + ".json";
        var json = JsonSerializer.Serialize(record);
        if (json.Length > 32_768) throw new InvalidDataException("Diagnostic summary too large.");
        ConfigStore.WriteAtomic(Path.Combine(directory, name), json);
        Prune();
    }

    public IReadOnlyList<DiagnosticRecord> Read(int max = 40)
    {
        if (!Directory.Exists(directory)) return [];
        var result = new List<DiagnosticRecord>();
        foreach (var file in Directory.EnumerateFiles(directory, "diag-*.json")
                         .OrderByDescending(Path.GetFileName).Take(Math.Clamp(max, 1, MaxEntries)))
        {
            try
            {
                if (new FileInfo(file).Length > 32_768) continue;
                var doc = JsonSerializer.Deserialize<DiagnosticRecord>(File.ReadAllText(file));
                if (doc is not null && doc.Endpoints is { Count: <= 8 }) result.Add(doc);
            }
            catch (Exception ex) when (ex is IOException or JsonException or UnauthorizedAccessException)
            {
                // A corrupt, partial or inaccessible entry cannot break Studio.
            }
        }
        return result.OrderByDescending(x => x.Created).ToArray();
    }

    public void Prune()
    {
        if (!Directory.Exists(directory)) return;
        var files = Directory.EnumerateFiles(directory, "diag-*.json")
            .OrderByDescending(Path.GetFileName).ToArray();
        var threshold = DateTime.UtcNow - Retention;
        foreach (var (file, index) in files.Select((name, i) => (name, i)))
        {
            try
            {
                if (index >= MaxEntries || File.GetLastWriteTimeUtc(file) < threshold)
                    File.Delete(file);
            }
            catch (IOException) { }
            catch (UnauthorizedAccessException) { }
        }
    }
}

public sealed record GuidedRecommendation(string Title, string Detail, string Severity);

public static class HealthAdvisor
{
    public static IReadOnlyList<GuidedRecommendation> Evaluate(
        DiagnosticSnapshot? diagnostic, IReadOnlyList<AdapterSnapshot> adapters)
    {
        var list = new List<GuidedRecommendation>();
        var wifi = adapters.Where(a => a.Kind == "Wi-Fi").ToArray();
        if (wifi.Length == 0)
            list.Add(new("未发现 Wi-Fi 接口", "确认无线驱动、硬件开关和设备管理器状态。", "attention"));
        else if (wifi.All(a => !a.IsUp))
            list.Add(new("无线链路未连接", "先尝试正常连接已保存的无线网络；不要仅凭公网测试失败强制重置网卡。", "warning"));
        else if (wifi.Any(a => a.IsUp && string.IsNullOrWhiteSpace(a.IPv4)))
            list.Add(new("Wi-Fi 已关联但未发现 IPv4", "查看 DHCP、认证门户和 IPv6 配置；再决定是否使用便携引擎恢复。", "attention"));

        if (diagnostic is null)
        {
            list.Add(new("尚无本次会话诊断", "点击“完整诊断”后才能判断公网、代理及 TUN 的差异。", "info"));
            return list;
        }
        if (diagnostic.Passed > 0 && diagnostic.MixedProxy?.Valid == false)
            list.Add(new("系统正常，代理出口未验证", "优先检查 Mihomo 节点、规则及 Mixed-Port；不要重启正常的 Wi-Fi。", "warning"));
        if (diagnostic.Passed == 0 && diagnostic.MixedProxy?.Valid == true)
            list.Add(new("代理可用，系统探测均未通过", "优先检查系统 DNS、TUN 路由及规则策略；保护物理无线连接。", "warning"));
        if (diagnostic.Passed == 0 && diagnostic.MixedProxy?.Valid != true)
            list.Add(new("互联网尚未验证", "这并不等于 Wi-Fi 网卡损坏。结合网关、地址分配及校园网认证情况排查。", "attention"));
        if (diagnostic.Passed == diagnostic.Endpoints.Count && diagnostic.Endpoints.Count > 0)
            list.Add(new("公网探测全部通过", "无需进行侵入式网络恢复。", "success"));
        return list;
    }
}
