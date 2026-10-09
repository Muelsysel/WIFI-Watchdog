using System.Net.Http.Headers;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace WiFiWatchdog.Studio.Core;

public sealed record StudioUpdate(string Version, string Tag, string ReleaseUrl,
    string AssetName, string? Sha256, bool Newer, bool Prerelease);

public static class StudioUpdates
{
    public const string CurrentVersion = "0.3.0-preview";
    public const string ReleaseApi = "https://api.github.com/repos/Muelsysel/WIFI-Watchdog/releases?per_page=30";
    public const string OfficialReleases = "https://github.com/Muelsysel/WIFI-Watchdog/releases";
    public const string ExpectedAsset = "WiFiWatchdog-Studio-windows-amd64.zip";
    private static readonly Regex VersionPattern = new(
        @"^(?:studio-v)?(?<major>\d{1,6})\.(?<minor>\d{1,6})\.(?<patch>\d{1,6})(?<pre>-[A-Za-z0-9][A-Za-z0-9.\-]{0,80})?$",
        RegexOptions.CultureInvariant | RegexOptions.Compiled);
    private static readonly Regex DigestPattern = new(
        @"^sha256:(?<digest>[a-fA-F0-9]{64})$", RegexOptions.Compiled);

    private sealed record Parsed(int Major, int Minor, int Patch, string Prerelease);
    private static Parsed? Parse(string value)
    {
        var match = VersionPattern.Match(value);
        if (!match.Success ||
            !int.TryParse(match.Groups["major"].Value, out var major) ||
            !int.TryParse(match.Groups["minor"].Value, out var minor) ||
            !int.TryParse(match.Groups["patch"].Value, out var patch)) return null;
        return new Parsed(major, minor, patch, match.Groups["pre"].Value.TrimStart('-'));
    }

    public static int CompareVersions(string a, string b)
    {
        var x = Parse(a) ?? throw new ArgumentException("Invalid Studio version", nameof(a));
        var y = Parse(b) ?? throw new ArgumentException("Invalid Studio version", nameof(b));
        var result = x.Major.CompareTo(y.Major);
        if (result != 0) return result;
        result = x.Minor.CompareTo(y.Minor);
        if (result != 0) return result;
        result = x.Patch.CompareTo(y.Patch);
        if (result != 0) return result;
        if (x.Prerelease.Length == 0) return y.Prerelease.Length == 0 ? 0 : 1;
        if (y.Prerelease.Length == 0) return -1;
        return string.CompareOrdinal(x.Prerelease, y.Prerelease);
    }

    // No third-party update executable, no download or execution is initiated.
    // The UI only opens the exact official Github Release tag after user click.
    public static StudioUpdate? SelectLatest(string payload, string installedVersion = CurrentVersion)
    {
        if (payload.Length > 1_500_000)
            throw new InvalidDataException("GitHub API reply is unexpectedly large.");
        using var doc = JsonDocument.Parse(payload);
        if (doc.RootElement.ValueKind != JsonValueKind.Array)
            throw new InvalidDataException("Invalid GitHub release response.");
        StudioUpdate? best = null;
        foreach (var release in doc.RootElement.EnumerateArray())
        {
            if (release.ValueKind != JsonValueKind.Object ||
                release.TryGetProperty("draft", out var draft) && draft.ValueKind == JsonValueKind.True ||
                !release.TryGetProperty("tag_name", out var tagNode) ||
                tagNode.ValueKind != JsonValueKind.String) continue;
            var tag = tagNode.GetString()!;
            if (!tag.StartsWith("studio-v", StringComparison.Ordinal) || Parse(tag) is null) continue;
            // Explicit name AND GitHub-reported sha256 digest must both exist.
            // This makes the displayed asset integrity verifiable by the user.
            if (!release.TryGetProperty("assets", out var assets) ||
                assets.ValueKind != JsonValueKind.Array) continue;
            string? sha256 = null;
            foreach (var asset in assets.EnumerateArray())
            {
                if (asset.ValueKind != JsonValueKind.Object ||
                    !asset.TryGetProperty("name", out var name) ||
                    name.GetString() != ExpectedAsset ||
                    !asset.TryGetProperty("digest", out var digest) ||
                    digest.ValueKind != JsonValueKind.String) continue;
                var value = digest.GetString() ?? "";
                var valid = DigestPattern.Match(value);
                if (valid.Success) sha256 = valid.Groups["digest"].Value.ToLowerInvariant();
            }
            if (sha256 is null) continue;
            var version = tag["studio-v".Length..];
            if (best is not null && CompareVersions(version, best.Version) <= 0) continue;
            bool prerelease = release.TryGetProperty("prerelease", out var pre) &&
                              pre.ValueKind == JsonValueKind.True;
            best = new StudioUpdate(version, tag, OfficialReleases + "/tag/" + tag,
                ExpectedAsset, sha256, CompareVersions(version, installedVersion) > 0, prerelease);
        }
        return best;
    }

    public static async Task<StudioUpdate?> CheckAsync(HttpClient client,
        string installedVersion = CurrentVersion, CancellationToken cancellationToken = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, ReleaseApi);
        request.Headers.UserAgent.Add(new ProductInfoHeaderValue("WiFiWatchdog-Studio", "0.3"));
        request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/vnd.github+json"));
        using var response = await client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead,
            cancellationToken).ConfigureAwait(false);
        response.EnsureSuccessStatusCode();
        if (response.Content.Headers.ContentLength > 1_500_000)
            throw new InvalidDataException("GitHub API reply exceeds maximum allowed size.");
        await using var stream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        using var buffer = new MemoryStream();
        var chunk = new byte[8192];
        int read;
        while ((read = await stream.ReadAsync(chunk, cancellationToken).ConfigureAwait(false)) > 0)
        {
            if (buffer.Length + read > 1_500_000)
                throw new InvalidDataException("GitHub API reply exceeds maximum allowed size.");
            buffer.Write(chunk, 0, read);
        }
        return SelectLatest(System.Text.Encoding.UTF8.GetString(buffer.ToArray()), installedVersion);
    }
}
