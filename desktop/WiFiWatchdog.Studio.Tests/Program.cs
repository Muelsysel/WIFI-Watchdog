using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json.Nodes;
using WiFiWatchdog.Studio.Core;

var passed = 0;
void Check(bool condition, string name)
{
    if (!condition) throw new Exception("FAILED: " + name);
    Console.WriteLine("PASS " + name);
    passed++;
}
var parsed = LogHistory.ParseLine("[2026-10-08 10:47:00] [INFO] 系统网络正常；10 分钟后再次检测。");
Check(parsed is { Level: "INFO" } && parsed.Message.Contains("10 分钟"), "parse engine log");
Check(LogHistory.ParseLine("garbage without timestamp") is null, "reject malformed log");
Check(LogHistory.ParseLine("[2026-10-08 10:47:00] [WARN] delayed")?.Level == "WARN", "warn severity");
var root = Path.Combine(Path.GetTempPath(), "watchdog-studio-test-" + Guid.NewGuid().ToString("N"));
try
{
    Directory.CreateDirectory(root);
    var configPath = Path.Combine(root, "config.json");
    var config = new ConfigStore(configPath);
    var original = ConfigStore.Defaults();
    original["futureEngineExtension"] = "preserve me";
    config.Save(original);
    var modified = config.Read();
    modified["normalCheckIntervalMinutes"] = 15;
    config.Save(modified);
    var read = config.Read();
    Check(ConfigStore.GetInt(read, "normalCheckIntervalMinutes", 0) == 15, "save config int");
    Check(read["futureEngineExtension"]?.ToString() == "preserve me", "preserve unknown engine keys");
    Check(File.Exists(configPath + ".bak"), "backup previous config");
    var bad = (JsonObject)read.DeepClone();
    bad["normalCheckIntervalMinutes"] = 0;
    try { config.Save(bad); throw new Exception("bad config was accepted"); }
    catch (InvalidDataException) { Check(true, "reject unsafe polling interval"); }
    File.WriteAllText(configPath, "{BROKEN JSON");
    try { config.Read(); throw new Exception("corrupted config was accepted"); }
    catch (InvalidDataException) { Check(true, "fail closed on invalid JSON"); }
    Check(File.ReadAllText(configPath) == "{BROKEN JSON", "never silently overwrite corrupt user config");
    var presets = new PresetStore(Path.Combine(root, "presets"));
    presets.Save("Campus 8021x", read);
    Check(presets.List().Contains("Campus 8021x"), "named profile stored");
    Check(presets.Load("Campus 8021x")["futureEngineExtension"]?.ToString() == "preserve me", "profile keeps unknown keys");
    try { presets.Save("../escaped", read); throw new Exception("path traversal accepted"); }
    catch (ArgumentException) { Check(true, "reject profile path traversal"); }
    var logFile = Path.Combine(root, "watchdog-2026-10-08.log");
    File.WriteAllText(logFile, "incomplete\n[2026-10-08 10:47:00] [ERROR] network down\n");
    Check(LogHistory.ReadTail(logFile).Count == 1, "read bounded log tail");
    var csv = ReportExporter.EventsCsv(new[] { new WatchdogEvent(DateTimeOffset.Now, "WARN", "a,\"quoted\"\r\nvalue") });
    Check(csv.Contains("\"a,\"\"quoted\"\"  value\""), "escape CSV cells");
    var snapshot = new DiagnosticSnapshot(DateTimeOffset.Now,
        [new EndpointResult("Google 204", "https://test", 204, true, 15, "有效")],
        new ControllerResult(true, false, "rule", "true", 2026, "secret-not-included"), null);
    var report = ReportExporter.ToJson(snapshot);
    Check(report.Contains("Google 204") && !report.Contains("secret-not-included"), "sanitized report excludes sensitive raw controller state");

    using var fake = new HttpClient(new MockHandler(_ => new HttpResponseMessage(HttpStatusCode.NoContent)
    { Content = new StringContent(string.Empty, Encoding.UTF8) }));
    var four = await NetworkDiagnostics.TestAllAsync(fake,
        Enumerable.Repeat(new EndpointTarget("Mock 204", "https://127.0.0.1/", (code, _) => code == HttpStatusCode.NoContent), 4),
        CancellationToken.None);
    Check(four.Count == 4 && four.All(x => x.Valid && x.Status == 204), "complete four-target diagnosis");
}
finally
{
    if (Directory.Exists(root)) Directory.Delete(root, true);
}
Console.WriteLine($"Studio core checks passed: {passed}");

sealed class MockHandler(Func<HttpRequestMessage, HttpResponseMessage> responder) : HttpMessageHandler
{
    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        => Task.FromResult(responder(request));
}
