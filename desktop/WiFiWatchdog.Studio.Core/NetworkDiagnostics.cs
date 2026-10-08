using System.Diagnostics;
using System.Net;
using System.Net.Http.Headers;
using System.Text.Json;

namespace WiFiWatchdog.Studio.Core;

public sealed record EndpointTarget(string Name, string Url, Func<HttpStatusCode, string, bool> Validate);
public sealed record EndpointResult(string Name, string Url, int? Status, bool Valid, long Milliseconds, string Detail);
public sealed record ControllerResult(bool Available, bool AuthRequired, string Mode, string Tun, int MixedPort, string Description);
public sealed record DiagnosticSnapshot(DateTimeOffset Created, IReadOnlyList<EndpointResult> Endpoints,
    ControllerResult Controller, EndpointResult? MixedProxy)
{
    public int Passed => Endpoints.Count(x => x.Valid);
    public string Diagnosis
    {
        get
        {
            if (MixedProxy?.Valid == true && Passed == 0)
                return "代理出口正常，但系统联网探测失败：检查 TUN 路由、DNS 与代理规则，避免重启 Wi-Fi。";
            if (Passed > 0 && MixedProxy?.Valid == false)
                return "系统联网正常，但混合代理出口不可用：优先检查 Mihomo 节点和代理规则。";
            if (Passed > 0) return "互联网已验证可访问；无需执行 Wi-Fi 恢复。";
            return "全部目标未能验证联网。应结合 Wi-Fi 关联、认证门户和 VPN/TUN 状态，不能据此直接重启网卡。";
        }
    }
}

public static class NetworkDiagnostics
{
    public static IReadOnlyList<EndpointTarget> Targets { get; } =
    [
        new("Microsoft NCSI", "http://www.msftconnecttest.com/connecttest.txt",
            (code, body) => code == HttpStatusCode.OK && body.Contains("Microsoft Connect Test", StringComparison.Ordinal)),
        new("Google 204", "http://www.gstatic.com/generate_204",
            (code, _) => code == HttpStatusCode.NoContent),
        new("Microsoft HTTPS", "https://www.microsoft.com/",
            (code, _) => (int)code is >= 200 and < 400),
        new("Baidu HTTPS", "https://www.baidu.com/",
            (code, _) => (int)code is >= 200 and < 400)
    ];

    public static HttpClient CreateClient(int seconds = 5, IWebProxy? proxy = null)
    {
        var handler = new HttpClientHandler
        {
            AllowAutoRedirect = false,
            UseProxy = proxy is not null,
            Proxy = proxy
        };
        return new HttpClient(handler) { Timeout = TimeSpan.FromSeconds(Math.Clamp(seconds, 1, 20)) };
    }

    public static async Task<EndpointResult> TestAsync(HttpClient client, EndpointTarget target, CancellationToken cancellationToken)
    {
        var timer = Stopwatch.StartNew();
        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, target.Url);
            request.Headers.UserAgent.ParseAdd("WiFiWatchdog-Studio/0.1");
            using var response = await client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken).ConfigureAwait(false);
            await using var stream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
            var buffer = new byte[1024];
            var length = await stream.ReadAsync(buffer, cancellationToken).ConfigureAwait(false);
            var body = System.Text.Encoding.UTF8.GetString(buffer, 0, length);
            var valid = target.Validate(response.StatusCode, body);
            return new(target.Name, target.Url, (int)response.StatusCode, valid,
                timer.ElapsedMilliseconds, valid ? "有效响应" : $"HTTP {(int)response.StatusCode} 不符合该端点的验证条件");
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            return new(target.Name, target.Url, null, false, timer.ElapsedMilliseconds, "检测已取消");
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or IOException)
        {
            return new(target.Name, target.Url, null, false, timer.ElapsedMilliseconds,
                ex is TaskCanceledException ? "请求超时" : ex.Message.Length > 150 ? ex.Message[..150] : ex.Message);
        }
    }

    public static async Task<IReadOnlyList<EndpointResult>> TestAllAsync(HttpClient client,
        IEnumerable<EndpointTarget> targets, CancellationToken token)
        => await Task.WhenAll(targets.Select(t => TestAsync(client, t, token))).ConfigureAwait(false);

    public static async Task<ControllerResult> ReadControllerAsync(int port, string? secret, CancellationToken token)
    {
        if (port is < 1 or > 65535) return new(false, false, "", "未知", 0, "控制接口检测已关闭");
        using var client = CreateClient(3);
        try
        {
            var version = await LocalGetAsync(client, port, "/version", secret, token).ConfigureAwait(false);
            if (version.status == HttpStatusCode.Unauthorized || version.status == HttpStatusCode.Forbidden)
                return new(true, true, "", "未知", 0, "控制接口可达，但需要 Secret；未读取任何敏感配置");
            using var versionJson = JsonDocument.Parse(version.json);
            if (version.status != HttpStatusCode.OK ||
                !versionJson.RootElement.TryGetProperty("version", out var v) ||
                v.ValueKind != JsonValueKind.String)
                return new(false, false, "", "未知", 0, "端口有响应，但无法验证为 Mihomo API");
            var current = await LocalGetAsync(client, port, "/configs", secret, token).ConfigureAwait(false);
            if (current.status == HttpStatusCode.Unauthorized || current.status == HttpStatusCode.Forbidden)
                return new(true, true, "", "未知", 0, "Mihomo 运行中，但运行配置需要鉴权");
            if (current.status != HttpStatusCode.OK)
                return new(true, false, "", "未知", 0, $"运行配置请求返回 HTTP {(int)current.status}");
            using var configJson = JsonDocument.Parse(current.json);
            var root = configJson.RootElement;
            var mode = root.TryGetProperty("mode", out var m) ? m.ToString() : "未知";
            var tun = root.TryGetProperty("tun", out var t) && t.TryGetProperty("enable", out var e)
                ? e.ToString().ToLowerInvariant() : "未知";
            var mixed = root.TryGetProperty("mixed-port", out var p) && p.TryGetInt32(out var number) ? number : 0;
            return new(true, false, mode, tun, mixed, "Mihomo 本地只读 API 正常");
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or IOException or JsonException)
        {
            // Verge may use a named pipe rather than listening on TCP 9097.
            return new(false, false, "", "未知", 0, "TCP 控制接口不可读取：不代表 Mihomo 已停止");
        }
    }

    private static async Task<(HttpStatusCode status, string json)> LocalGetAsync(
        HttpClient client, int port, string path, string? secret, CancellationToken token)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, $"http://127.0.0.1:{port}{path}");
        if (!string.IsNullOrWhiteSpace(secret))
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", secret);
        using var response = await client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, token).ConfigureAwait(false);
        var json = await response.Content.ReadAsStringAsync(token).ConfigureAwait(false);
        if (json.Length > 32_768) throw new InvalidDataException("Unexpectedly large controller response");
        return (response.StatusCode, json);
    }

    public static async Task<DiagnosticSnapshot> RunAsync(int controllerPort, int mixedPort, string? secret,
        CancellationToken token)
    {
        using var client = CreateClient();
        var system = TestAllAsync(client, Targets, token);
        var controller = ReadControllerAsync(controllerPort, secret, token);
        await Task.WhenAll(system, controller).ConfigureAwait(false);
        var actualPort = mixedPort > 0 ? mixedPort : controller.Result.MixedPort;
        EndpointResult? proxied = null;
        if (actualPort is > 0 and <= 65535)
        {
            using var proxyClient = CreateClient(5, new WebProxy($"http://127.0.0.1:{actualPort}"));
            proxied = await TestAsync(proxyClient, Targets[1], token).ConfigureAwait(false);
            if (!proxied.Valid)
                proxied = await TestAsync(proxyClient, Targets[2], token).ConfigureAwait(false);
        }
        return new(DateTimeOffset.Now, system.Result, controller.Result, proxied);
    }
}
