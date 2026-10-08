using System.Globalization;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace WiFiWatchdog.Studio.Core;

public static class AppPaths
{
    public static string Root => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "WiFiWatchdog");
    public static string Config => Path.Combine(Root, "config.json");
    public static string Logs => Path.Combine(Root, "logs");
    public static string Diagnostics => Path.Combine(Root, "diagnostics");
    public static string Studio => Path.Combine(Root, "Studio");
    public static string Presets => Path.Combine(Studio, "profiles");
}

public sealed class ConfigStore
{
    public string FileName { get; }
    public ConfigStore(string fileName) => FileName = fileName;

    public static JsonObject Defaults() => new()
    {
        ["normalCheckIntervalMinutes"] = 10, ["failureCheckIntervalSeconds"] = 10,
        ["failureThreshold"] = 5, ["repairRetryIntervalMinutes"] = 10,
        ["wifiDisableWaitSeconds"] = 3, ["wifiStartupWaitSeconds"] = 15,
        ["connectionTimeoutSeconds"] = 4, ["connectRetryCount"] = 3,
        ["connectRetryDelaySeconds"] = 6, ["dhcpRenewWaitSeconds"] = 8,
        ["autoReconnectDisconnected"] = false, ["enableWlanServiceRestart"] = false,
        ["enableVpnAware"] = true, ["vpnLocalPort"] = 0, ["mihomoControllerPort"] = 9097,
        ["logRetentionDays"] = 30, ["startWithWindows"] = false
    };

    public JsonObject Read()
    {
        if (!File.Exists(FileName)) return Defaults();
        try { return JsonNode.Parse(File.ReadAllText(FileName)) as JsonObject
                     ?? throw new InvalidDataException("Engine config root is not a JSON object."); }
        catch (JsonException ex) { throw new InvalidDataException("Config contains invalid JSON. Original left untouched.", ex); }
    }

    public static int GetInt(JsonObject obj, string name, int fallback)
        => int.TryParse(obj[name]?.ToString(), NumberStyles.Integer, CultureInfo.InvariantCulture, out var value) ? value : fallback;
    public static bool GetBool(JsonObject obj, string name, bool fallback)
        => bool.TryParse(obj[name]?.ToString(), out var value) ? value : fallback;

    public static void Validate(JsonObject c)
    {
        foreach (var (key, lo, hi) in new (string, int, int)[]
        {
            ("normalCheckIntervalMinutes",1,1440), ("failureCheckIntervalSeconds",1,3600),
            ("failureThreshold",1,100), ("repairRetryIntervalMinutes",1,1440),
            ("wifiDisableWaitSeconds",1,120), ("wifiStartupWaitSeconds",1,600),
            ("connectionTimeoutSeconds",1,30), ("connectRetryCount",1,10),
            ("connectRetryDelaySeconds",1,60), ("dhcpRenewWaitSeconds",1,120),
            ("vpnLocalPort",0,65535), ("mihomoControllerPort",0,65535),
            ("logRetentionDays",1,3650)
        })
        {
            if (c[key] is null) continue;
            if (!int.TryParse(c[key]!.ToString(), out var n) || n < lo || n > hi)
                throw new InvalidDataException($"Invalid {key}: expected {lo}..{hi}.");
        }
    }

    public void Save(JsonObject config)
    {
        Validate(config);
        var json = config.ToJsonString(new JsonSerializerOptions { WriteIndented = true });
        WriteAtomic(FileName, json);
    }

    public static void WriteAtomic(string path, string contents)
    {
        var directory = Path.GetDirectoryName(Path.GetFullPath(path))!;
        Directory.CreateDirectory(directory);
        var temp = Path.Combine(directory, "." + Path.GetFileName(path) + "-" + Guid.NewGuid().ToString("N") + ".tmp");
        try
        {
            File.WriteAllText(temp, contents);
            // Preserve a single known-good backup before applying changes.
            if (File.Exists(path)) File.Copy(path, path + ".bak", true);
            File.Move(temp, path, true);
        }
        finally { if (File.Exists(temp)) File.Delete(temp); }
    }
}

public sealed class PresetStore
{
    private readonly string directory;
    private static readonly Regex SafeName = new(@"^[\p{L}\p{N}_\- ]{1,40}$", RegexOptions.Compiled);
    public PresetStore(string directory) => this.directory = directory;

    public IReadOnlyList<string> List()
    {
        if (!Directory.Exists(directory)) return [];
        return Directory.EnumerateFiles(directory, "*.json")
            .Select(Path.GetFileNameWithoutExtension)
            .Where(x => x is not null && SafeName.IsMatch(x))
            .Select(x => x!)
            .OrderBy(x => x, StringComparer.CurrentCultureIgnoreCase).ToArray();
    }

    private string FilePath(string name)
    {
        if (!SafeName.IsMatch(name)) throw new ArgumentException("Profile names may use 1–40 letters, digits, spaces, _ or -.", nameof(name));
        return Path.Combine(directory, name + ".json");
    }

    public void Save(string name, JsonObject configuration) => new ConfigStore(FilePath(name)).Save(configuration);
    public JsonObject Load(string name) => new ConfigStore(FilePath(name)).Read();
    public void Remove(string name) => File.Delete(FilePath(name));
}
