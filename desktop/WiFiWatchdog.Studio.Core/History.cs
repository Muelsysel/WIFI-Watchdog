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
        fs.ReadExactly(data);
        var lines = Encoding.UTF8.GetString(data).Split('\n');
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
            // Stream through each day's bounded engine log. The previous
            // version materialized up to 16 MiB * 14 days every refresh,
            // wasting both allocations and memory on idle dashboards.
            var info = 0;
            var warn = 0;
            var error = 0;
            var recovery = 0;
            if (File.Exists(file))
            {
                using var fs = new FileStream(file, FileMode.Open, FileAccess.Read,
                    FileShare.ReadWrite | FileShare.Delete);
                using var reader = new StreamReader(fs, Encoding.UTF8,
                    detectEncodingFromByteOrderMarks: true, bufferSize: 16 * 1024);
                string? line;
                while ((line = reader.ReadLine()) is not null)
                {
                    // Avoid creating a WatchdogEvent object for each line.
                    var parsed = ParseLine(line);
                    if (parsed is null) continue;
                    switch (parsed.Level)
                    {
                        case "INFO": info++; break;
                        case "WARN": warn++; break;
                        case "ERROR": error++; break;
                    }
                    if (parsed.Message.Contains("恢复层", StringComparison.Ordinal) ||
                        parsed.Message.Contains("恢复成功", StringComparison.Ordinal))
                        recovery++;
                }
            }
            summaries.Add(new DaySummary(date, info, warn, error, recovery));
        }
        return summaries;
    }
}
