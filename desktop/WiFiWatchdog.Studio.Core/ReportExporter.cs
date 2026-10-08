using System.Text;
using System.Text.Json;

namespace WiFiWatchdog.Studio.Core;

public static class ReportExporter
{
    // No raw config, local IPs, SSIDs, API token or full process arguments.
    // Diagnostics exported from Studio are intentionally much more private
    // than the low-level engine troubleshooting bundle.
    public static string ToJson(DiagnosticSnapshot report)
    {
        var sanitized = new
        {
            version = "studio-preview-0.2",
            at = report.Created,
            system = report.Endpoints.Select(x => new { x.Name, x.Status, x.Valid, x.Milliseconds, x.Detail }),
            controller = new { report.Controller.Available, report.Controller.AuthRequired,
                report.Controller.Mode, report.Controller.Tun, report.Controller.MixedPort },
            proxy = report.MixedProxy is null ? null : new { report.MixedProxy.Valid,
                report.MixedProxy.Status, report.MixedProxy.Milliseconds },
            diagnosis = report.Diagnosis
        };
        return JsonSerializer.Serialize(sanitized, new JsonSerializerOptions { WriteIndented = true });
    }

    public static string EventsCsv(IEnumerable<WatchdogEvent> events)
    {
        static string Escape(string value)
        {
            // CSV formula injection: spreadsheet apps can evaluate log text
            // starting with =, +, -, @, or tab. Encode as literal text.
            var cleaned = value.Replace("\r", " ").Replace("\n", " ");
            var start = cleaned.TrimStart(' ', '\t');
            if (start.Length > 0 && "=+-@".Contains(start[0]) || cleaned.StartsWith('\t'))
                cleaned = "'" + cleaned;
            return "\"" + cleaned.Replace("\"", "\"\"") + "\"";
        }
        var sb = new StringBuilder("\uFEFFTime,Level,Message\r\n");
        foreach (var item in events)
        {
            sb.Append(Escape(item.Timestamp.ToString("yyyy-MM-dd HH:mm:ss"))).Append(',')
              .Append(Escape(item.Level)).Append(',')
              .Append(Escape(item.Message)).Append("\r\n");
        }
        return sb.ToString();
    }
}
