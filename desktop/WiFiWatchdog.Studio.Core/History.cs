using System.Globalization;
using System.Text;
using System.Text.RegularExpressions;

namespace WiFiWatchdog.Studio.Core;

public sealed record WatchdogEvent(DateTimeOffset Timestamp, string Level, string Message);
public sealed record DaySummary(string Date, int Info, int Warn, int Error, int Recoveries)
{
    public int Total => Info + Warn + Error;
}
public static class LogHistory
{
    private static readonly Regex LinePattern = new(
        @"^\[(?<time>\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)\] \[(?<level>INFO|WARN|ERROR)\] (?<message>.*)$",
        RegexOptions.Compiled | RegexOptions.CultureInvariant);
    public static WatchdogEvent? ParseLine(string line)
    {
        var match = LinePattern.Match(line);
        if (!match.Success) return null;
        if (!DateTime.TryParseExact(match.Groups["time"].Value, "yyyy-MM-dd HH:mm:ss",
            CultureInfo.InvariantCulture, DateTimeStyles.AssumeLocal, out var time)) return null;
        return new WatchdogEvent(new DateTimeOffset(time), match.Groups["level"].Value, match.Groups["message"].Value);
    }

    // Capped byte tail avoids loading large log files or keeping file handles
    // open while the Go engine is still writing to them.
    public static IReadOnlyList<WatchdogEvent> ReadTail(string file, int maxEvents = 300, int maxBytes = 512 * 1024)
    {
        if (!File.Exists(file)) return [];
        using var fs = new FileStream(file, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
        var count = (int)Math.Min(fs.Length, Math.Max(1024, maxBytes));
        fs.Seek(-count, SeekOrigin.End);
        var data = new byte[count];
        var n = fs.Read(data, 0, count);
        var lines = Encoding.UTF8.GetString(data, 0, n).Split('\n');
        // The first line may be truncated if we started in the middle.
        var begin = fs.Length > count ? 1 : 0;
        return lines.Skip(begin).Select(x => ParseLine(x.TrimEnd('\r')))
            .OfType<WatchdogEvent>().TakeLast(maxEvents).ToArray();
    }

    public static IReadOnlyList<WatchdogEvent> Recent(string directory, int take = 300)
    {
        if (!Directory.Exists(directory)) return [];
        var paths = Directory.EnumerateFiles(directory, "watchdog-????-??-??.log")
            .OrderByDescending(Path.GetFileName).Take(3).ToArray();
        return paths.SelectMany(p => ReadTail(p, Math.Max(200, take)))
            .OrderByDescending(e => e.Timestamp).Take(take).ToArray();
    }

    public static IReadOnlyList<DaySummary> LastDays(string directory, int days = 14)
    {
        var summaries = new List<DaySummary>();
        for (var i = days - 1; i >= 0; i--)
        {
            var date = DateTime.Today.AddDays(-i).ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
            var file = Path.Combine(directory, "watchdog-" + date + ".log");
            var entries = ReadTail(file, 30000, 16 * 1024 * 1024);
            summaries.Add(new DaySummary(date, entries.Count(e => e.Level == "INFO"),
                entries.Count(e => e.Level == "WARN"), entries.Count(e => e.Level == "ERROR"),
                entries.Count(e => e.Message.Contains("恢复层", StringComparison.Ordinal) ||
                                   e.Message.Contains("恢复成功", StringComparison.Ordinal))));
        }
        return summaries;
    }
}
