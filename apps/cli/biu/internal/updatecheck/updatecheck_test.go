package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCLITag(t *testing.T) {
	cases := []struct {
		tag  string
		want string
		ok   bool
	}{
		{"v0.3.0", "0.3.0", true},
		{"v1.20.3", "1.20.3", true},
		{"client-v2.0.0", "", false},
		{"service-v1.0.0", "", false},
		{"client-macos-v2.0.0", "", false},
		{"v0.3.0-rc1", "", false},
		{"0.3.0", "", false}, // bare, no v prefix
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseCLITag(tc.tag)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseCLITag(%q) = (%q, %v), want (%q, %v)",
				tc.tag, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.3.0", "0.2.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"0.2.1", "0.3.0", -1},
		// fail-closed: garbage compares equal, never "newer"
		{"garbage", "0.3.0", 0},
		{"0.3.0", "garbage", 0},
		{"0.3.0.1", "0.3.0", 0},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestShouldSkipVersion(t *testing.T) {
	skip := []string{"", "0.1.0-dev", "1.2.3+clientmanaged", "0.2.0-rc1", "garbage", "0.3"}
	keep := []string{"0.1.0", "1.20.3", "10.0.0"}
	for _, v := range skip {
		if !ShouldSkipVersion(v) {
			t.Errorf("ShouldSkipVersion(%q) = false, want true", v)
		}
	}
	for _, v := range keep {
		if ShouldSkipVersion(v) {
			t.Errorf("ShouldSkipVersion(%q) = true, want false", v)
		}
	}
}

// fakeGitHub returns an HTTPDo that serves the given release list JSON.
func fakeGitHub(t *testing.T, releases []map[string]any) func(*http.Request) (*http.Response, error) {
	t.Helper()
	body, err := json.Marshal(releases)
	if err != nil {
		t.Fatal(err)
	}
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Header:     http.Header{},
		}, nil
	}
}

func githubRelease(tag string, draft, prerelease bool, assets ...[2]string) map[string]any {
	rel := map[string]any{
		"tag_name":   tag,
		"html_url":   "https://github.com/biumind/biumind/releases/tag/" + tag,
		"draft":      draft,
		"prerelease": prerelease,
	}
	if len(assets) > 0 {
		list := make([]map[string]string, 0, len(assets))
		for _, a := range assets {
			list = append(list, map[string]string{
				"name":                 a[0],
				"browser_download_url": a[1],
			})
		}
		rel["assets"] = list
	}
	return rel
}

func testOpt(t *testing.T, current string, do func(*http.Request) (*http.Response, error)) Options {
	t.Helper()
	// Redirect HOME so Enabled()'s default-path read is hermetic —
	// a developer's real ~/.biu/update-check.json must not affect tests.
	t.Setenv("HOME", t.TempDir())
	return Options{
		CurrentVersion: current,
		Now:            func() time.Time { return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC) },
		HTTPDo:         do,
		StatePath:      filepath.Join(t.TempDir(), "update-check.json"),
	}
}

func TestMaybeCheckNewer(t *testing.T) {
	do := fakeGitHub(t, []map[string]any{
		githubRelease("client-v2.0.0", false, false), // wrong plane — skipped
		githubRelease("v0.3.0-rc1", false, false),    // bare tag but rc → prerelease=false? tag won't parse
		githubRelease("v0.3.0", false, false),
		githubRelease("v0.2.0", false, false),
	})
	res, err := MaybeCheck(context.Background(), testOpt(t, "0.2.0", do))
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected a result for 0.2.0 → 0.3.0")
	}
	if res.Latest != "0.3.0" || !res.Newer || res.Source != "github" {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.ReleaseURL == "" {
		t.Error("ReleaseURL should be set")
	}
}

func TestCheckSurfacesPlatformAssets(t *testing.T) {
	do := fakeGitHub(t, []map[string]any{
		githubRelease("v0.3.0", false, false,
			[2]string{PlatformFilename("0.3.0"), "https://dl.example/biu.tar.gz"},
			[2]string{"checksums.txt", "https://dl.example/checksums.txt"},
			[2]string{"biu_0.3.0_Windows_x86_64.zip", "https://dl.example/wrong.zip"},
		),
	})
	res, err := Check(context.Background(), testOpt(t, "0.2.0", do))
	if err != nil {
		t.Fatal(err)
	}
	if res.AssetFilename != PlatformFilename("0.3.0") {
		t.Errorf("AssetFilename = %q, want %q", res.AssetFilename, PlatformFilename("0.3.0"))
	}
	if res.AssetURL != "https://dl.example/biu.tar.gz" {
		t.Errorf("AssetURL = %q", res.AssetURL)
	}
	if res.ChecksumsURL != "https://dl.example/checksums.txt" {
		t.Errorf("ChecksumsURL = %q", res.ChecksumsURL)
	}
}

// fakeOSS returns an HTTPDo serving an OSS-style biu.json manifest.
func fakeOSS(t *testing.T, version, assetURL, sha string) func(*http.Request) (*http.Response, error) {
	t.Helper()
	manifest := map[string]any{
		"version": version,
		"assets": []map[string]any{{
			"platform": ossPlatform(),
			"url":      assetURL,
			"filename": "biu_" + version + "_fake.tar.gz",
			"sha256":   sha,
		}},
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/downloads/biu.json" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Header:     http.Header{},
		}, nil
	}
}

func TestCheckWithFallbackPrefersOSSDownload(t *testing.T) {
	gh := fakeGitHub(t, []map[string]any{
		githubRelease("v0.3.0", false, false,
			[2]string{PlatformFilename("0.3.0"), "https://github.com/dl/biu.tar.gz"},
			[2]string{"checksums.txt", "https://github.com/dl/checksums.txt"},
		),
	})
	oss := fakeOSS(t, "0.3.0", "https://oss.example/biu_0.3.0.tar.gz", strings.Repeat("ab", 32))

	// Router: api.github.com → gh, endpoint → oss.
	do := func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "api.github.com") {
			return gh(req)
		}
		return oss(req)
	}
	opt := testOpt(t, "0.2.0", do)
	opt.ManifestURL = "https://biumind.example.com/downloads/biu.json"

	res, err := CheckWithFallback(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "github" || res.Latest != "0.3.0" {
		t.Errorf("version info should come from github, got %+v", res)
	}
	if res.DownloadSource != "oss" || res.AssetURL != "https://oss.example/biu_0.3.0.tar.gz" {
		t.Errorf("download should prefer OSS, got %+v", res)
	}
	if res.AssetSha256 != strings.Repeat("ab", 32) {
		t.Errorf("AssetSha256 = %q", res.AssetSha256)
	}
}

func TestCheckWithFallbackGitHubDown(t *testing.T) {
	oss := fakeOSS(t, "0.3.0", "https://oss.example/biu_0.3.0.tar.gz", strings.Repeat("ab", 32))
	do := func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "api.github.com") {
			return nil, errors.New("github unreachable")
		}
		return oss(req)
	}
	opt := testOpt(t, "0.2.0", do)
	opt.ManifestURL = "https://biumind.example.com/downloads/biu.json"

	res, err := CheckWithFallback(context.Background(), opt)
	if err != nil {
		t.Fatalf("OSS fallback should cover GitHub outage: %v", err)
	}
	if res.Source != "oss" || !res.Newer || res.Latest != "0.3.0" {
		t.Errorf("unexpected fallback result: %+v", res)
	}
}

func TestCheckWithFallbackBothDown(t *testing.T) {
	do := func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }
	opt := testOpt(t, "0.2.0", do)
	opt.ManifestURL = "https://biumind.example.com/downloads/biu.json"

	_, err := CheckWithFallback(context.Background(), opt)
	if err == nil || !strings.Contains(err.Error(), "github") || !strings.Contains(err.Error(), "oss") {
		t.Errorf("both sources down should report both errors, got %v", err)
	}
}

func TestManifestURLForEndpoint(t *testing.T) {
	cases := []struct{ endpoint, want string }{
		{"https://biumind.xxlab.tech", "https://biumind.xxlab.tech/downloads/biu.json"},
		{"https://biumind.xxlab.tech/", "https://biumind.xxlab.tech/downloads/biu.json"},
		{"http://localhost:8088", "http://localhost:8088/downloads/biu.json"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := ManifestURLForEndpoint(tc.endpoint); got != tc.want {
			t.Errorf("ManifestURLForEndpoint(%q) = %q, want %q", tc.endpoint, got, tc.want)
		}
	}
}

func TestMaybeCheckUpToDate(t *testing.T) {
	do := fakeGitHub(t, []map[string]any{githubRelease("v0.3.0", false, false)})
	res, err := MaybeCheck(context.Background(), testOpt(t, "0.3.0", do))
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Errorf("same version should return nil result, got %+v", res)
	}
}

func TestMaybeCheckSkipsDevAndClientManaged(t *testing.T) {
	called := false
	do := func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("should not hit network")
	}
	for _, v := range []string{"0.1.0-dev", "1.2.3+clientmanaged"} {
		res, err := MaybeCheck(context.Background(), testOpt(t, v, do))
		if err != nil || res != nil {
			t.Errorf("version %q: got (%v, %v), want (nil, nil)", v, res, err)
		}
	}
	if called {
		t.Error("network was hit for a skipped build")
	}
}

func TestMaybeCheckThrottle(t *testing.T) {
	do := fakeGitHub(t, []map[string]any{githubRelease("v0.3.0", false, false)})
	opt := testOpt(t, "0.2.0", do)

	if _, err := MaybeCheck(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	// Second call within 24h: throttled, no result even for a new version.
	do2 := fakeGitHub(t, []map[string]any{githubRelease("v0.4.0", false, false)})
	opt2 := opt
	opt2.HTTPDo = do2
	res, err := MaybeCheck(context.Background(), opt2)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Errorf("throttled check should return nil, got %+v", res)
	}

	// After the interval: checks again, but 0.3.0 was already notified.
	later := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	opt3 := opt
	opt3.HTTPDo = do2
	opt3.Now = func() time.Time { return later }
	res, err = MaybeCheck(context.Background(), opt3)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Latest != "0.4.0" {
		t.Errorf("post-interval check should surface 0.4.0, got %+v (%v)", res, err)
	}
}

func TestMaybeCheckNotifyOnce(t *testing.T) {
	do := fakeGitHub(t, []map[string]any{githubRelease("v0.3.0", false, false)})
	opt := testOpt(t, "0.2.0", do)

	if _, err := MaybeCheck(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	// Wipe only the timestamp: same version, still not notified again.
	st, err := LoadState(opt.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	st.LastCheckedAt = time.Time{}
	if err := SaveState(opt.StatePath, st); err != nil {
		t.Fatal(err)
	}
	res, err := MaybeCheck(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Errorf("already-notified version should not notify again, got %+v", res)
	}
}

func TestMaybeCheckSkippedVersion(t *testing.T) {
	do := fakeGitHub(t, []map[string]any{githubRelease("v0.3.0", false, false)})
	opt := testOpt(t, "0.2.0", do)
	if err := SkipVersionTo(opt.StatePath, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	res, err := MaybeCheck(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Errorf("skipped version should not notify, got %+v", res)
	}
}

func TestMaybeCheckNetworkErrorStaysQuiet(t *testing.T) {
	opt := testOpt(t, "0.2.0", func(*http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})
	res, err := MaybeCheck(context.Background(), opt)
	if err != nil {
		t.Fatalf("network error should be swallowed, got %v", err)
	}
	if res != nil {
		t.Errorf("network error should yield nil result, got %+v", res)
	}
	// The failed attempt still counts as a check (throttle armed).
	st, err := LoadState(opt.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastCheckedAt.IsZero() {
		t.Error("failed check should still update lastCheckedAt")
	}
}

func TestMaybeCheckNoCLIReleaseFound(t *testing.T) {
	do := fakeGitHub(t, []map[string]any{
		githubRelease("client-v2.0.0", false, false),
		githubRelease("service-v1.0.0", false, false),
	})
	opt := testOpt(t, "0.2.0", do)
	res, err := MaybeCheck(context.Background(), opt)
	if err != nil || res != nil {
		t.Errorf("no CLI release → (nil, nil), got (%v, %v)", res, err)
	}
}

func TestEnabledToggle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIU_UPDATE_CHECK", "")
	// Missing file → enabled.
	if !Enabled() {
		t.Error("missing state file should default to enabled")
	}
	// Explicit off.
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Error("state file with enabled=false should disable")
	}
	// Back on.
	if err := Enable(); err != nil {
		t.Fatal(err)
	}
	if !Enabled() {
		t.Error("Enable() should re-enable")
	}
	// Env kill switch beats the file.
	t.Setenv("BIU_UPDATE_CHECK", "0")
	if Enabled() {
		t.Error("BIU_UPDATE_CHECK=0 should disable regardless of file")
	}
}

// SkipVersionTo writes a skip marker to an explicit path (test helper;
// the exported SkipVersion always writes the default path).
func SkipVersionTo(path, v string) error {
	s, err := LoadState(path)
	if err != nil {
		s = State{}
	}
	s.SkippedVersion = v
	return SaveState(path, s)
}
