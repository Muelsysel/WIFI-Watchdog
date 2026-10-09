using System.Text;
using System.Text.RegularExpressions;

namespace WiFiWatchdog.Studio.Core;

public static class CrashJournal
{
    // Store only exception type and a coarse component label. Exception
    // messages / full stack traces may contain SSIDs, file paths and secrets.
    private static readonly Regex SourcePattern = new(@"^[A-Za-z][A-Za-z0-9._-]{0,39}$",
        RegexOptions.CultureInvariant | RegexOptions.Compiled);
    public static string Render(string component, Type exceptionType, DateTimeOffset time)
    {
        var area = SourcePattern.IsMatch(component) ? component : "Application";
        var kind = exceptionType.FullName ?? exceptionType.Name;
        if (kind.Length > 180) kind = kind[..180];
        return $"{time.ToUniversalTime():yyyy-MM-ddTHH:mm:ssZ} | {area} | {kind}";
    }

    public static void Record(string directory, string component, Exception exception)
    {
        try
        {
            Directory.CreateDirectory(directory);
            var today = DateTimeOffset.UtcNow;
            var line = Render(component, exception.GetType(), today);
            var file = Path.Combine(directory, "studio-" + today.ToString("yyyy-MM-dd") + ".log");
            // Independent writer uses a short write-once handle; errors here
            // must never recurse into the crash dispatcher.
            using (var stream = new FileStream(file, FileMode.Append, FileAccess.Write, FileShare.Read,
                bufferSize: 4096))
            {
                var data = Encoding.UTF8.GetBytes(line + Environment.NewLine);
                stream.Write(data);
            }
            var keepSince = DateTime.UtcNow.AddDays(-14);
            foreach (var old in Directory.EnumerateFiles(directory, "studio-????-??-??.log"))
            {
                try { if (File.GetLastWriteTimeUtc(old) < keepSince) File.Delete(old); }
                catch (IOException) { }
                catch (UnauthorizedAccessException) { }
            }
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or ArgumentException)
        {
            // Telemetry is local-only and best effort, never critical-path.
        }
    }
}
