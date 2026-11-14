package web

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestShellCarriesFirebaseConfigOnlyWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(New(Firebase{APIKey: "k", AuthDomain: "p.firebaseapp.com", ProjectID: "p", AppID: "a"}).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `data-firebase="{&#34;apiKey&#34;:&#34;k&#34;`) {
		t.Fatalf("config not in page: %s", body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-src https://p.firebaseapp.com", "connect-src 'self' https://*.googleapis.com", "script-src 'self' https://apis.google.com"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}

	if page := get(t, httptest.NewServer(newTestApp().Handler()).URL+"/"); !strings.Contains(page, `data-firebase=""`) {
		t.Fatalf("unconfigured page should carry no config: %s", page)
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestApp().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ideas", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestVendoredFirebaseLoadsNothingFromGoogleCDN(t *testing.T) {
	for _, name := range []string{"firebase-app.js", "firebase-auth.js", "firebase-firestore.js"} {
		body, err := staticFS.ReadFile("static/vendor/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "gstatic.com/firebasejs") {
			t.Errorf("%s imports from gstatic; run make vendor", name)
		}
	}
}

func newTestApp() *App { return New(Firebase{}) }

// Google's frontend reserves /healthz on *.run.app and answers it itself, so a
// health check there reports a perfectly healthy service as broken. The path
// has to stay off that name.
func TestHealthEndpointAvoidsTheReservedPath(t *testing.T) {
	if HealthPath == "/healthz" {
		t.Fatal("/healthz never reaches the container on Cloud Run")
	}
	req := httptest.NewRequest(http.MethodGet, HealthPath, nil)
	rec := httptest.NewRecorder()
	newTestApp().Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("%s = %d, want 204", HealthPath, rec.Code)
	}
}

// A product is installed on a phone or it is a browser tab, and the difference
// is a manifest and the icons a launcher can actually use.
func TestPageIsInstallable(t *testing.T) {
	srv := httptest.NewServer(newTestApp().Handler())
	defer srv.Close()

	page := get(t, srv.URL+"/")
	for _, want := range []string{
		`rel="manifest" href="/manifest.webmanifest"`,
		`rel="apple-touch-icon"`,
		`name="apple-mobile-web-app-capable" content="yes"`,
		`name="apple-mobile-web-app-status-bar-style"`,
		"viewport-fit=cover",
		"safe-area-inset-bottom",
		// A page whose status bar follows the phone has to follow it too.
		`content="#f5f6f8" media="(prefers-color-scheme: light)"`,
		`content="#131516" media="(prefers-color-scheme: dark)"`,
		"color-scheme:light dark",
		"prefers-color-scheme:dark",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}

	var m webManifest
	if err := json.Unmarshal([]byte(get(t, srv.URL+ManifestPath)), &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.Display != "standalone" {
		t.Errorf("display = %q, want standalone", m.Display)
	}
	// A manifest can name one theme colour, an installed app's status bar is
	// painted from it in both light and dark mode, and so a named colour is the
	// wrong colour for half the day. Left out, the bar follows the phone. This
	// is a silence that is easy to fill back in by accident, so it is written
	// down here as well as in the manifest.
	if raw := get(t, srv.URL+ManifestPath); strings.Contains(raw, "theme_color") {
		t.Errorf("the manifest names a theme colour, which pins the status bar to one scheme:\n%s", raw)
	}
	// iOS will not take an SVG for a home screen icon, so the PNGs are the
	// difference between installing and not.
	png := 0
	for _, icon := range m.Icons {
		if icon.Type == "image/png" {
			png++
		}
		if code := head(t, srv.URL+icon.Src); code != http.StatusOK {
			t.Errorf("icon %s = %d, want 200", icon.Src, code)
		}
	}
	if png == 0 {
		t.Error("a manifest with no PNG icon does not install on a phone")
	}
}

// An installed icon has no browser chrome behind it, so a launch with no signal
// has to reach the app rather than the browser's error page. The document the
// worker falls back to is its own page, never a cached copy of one that shows
// data.
func TestOfflineDocumentIsServedAndCached(t *testing.T) {
	srv := httptest.NewServer(newTestApp().Handler())
	defer srv.Close()

	if body := get(t, srv.URL+OfflinePath); !strings.Contains(body, "Offline") {
		t.Errorf("offline page does not say so: %s", body)
	}
	if worker := get(t, srv.URL+"/sw.js"); !strings.Contains(worker, OfflinePath) {
		t.Error("the worker does not cache the offline page, so a cold launch has nothing to show")
	}
}

// A release only reaches an installed app if the worker notices it, and the
// worker only notices if its own bytes changed. They carry a digest of every
// asset, so nobody has to remember to raise a number.
func TestServiceWorkerCacheIsNamedAfterTheAssetsItHolds(t *testing.T) {
	srv := httptest.NewServer(newTestApp().Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sw.js")
	if err != nil {
		t.Fatalf("GET /sw.js: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "__VERSION__") {
		t.Error("the worker went out with its placeholder still in it")
	}
	if !strings.Contains(string(body), staticVersion) {
		t.Errorf("worker does not name this build's cache (%s)", staticVersion)
	}
	// A worker's scope cannot rise above its own path, so it is served from the
	// root; and being the carrier of every other update, it is never answered
	// from a cache without asking.
	if got := resp.Header.Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed = %q, want /", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

// The worker refills its cache with ordinary fetches. Without a validator those
// can be answered from the browser's own cache, which would faithfully preserve
// the very file the release was meant to replace — so embedded assets, which
// have no modification time to offer, carry their digest instead.
func TestAssetsRevalidateAgainstTheirOwnContent(t *testing.T) {
	srv := httptest.NewServer(newTestApp().Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/static/icon.svg")
	if err != nil {
		t.Fatalf("GET icon: %v", err)
	}
	defer resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("an embedded asset went out with nothing to revalidate against")
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/static/icon.svg", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("If-None-Match", etag)
	again, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	defer again.Body.Close()
	if again.StatusCode != http.StatusNotModified {
		t.Errorf("status = %d, want 304 for an unchanged asset", again.StatusCode)
	}
}

// The property the whole scheme rests on: edit anything and the version moves.
func TestFingerprintFollowsContent(t *testing.T) {
	tree := func(css string, extra ...string) fs.FS {
		files := fstest.MapFS{
			"static/app.js": &fstest.MapFile{Data: []byte(css)},
			"static/sw.js":  &fstest.MapFile{Data: []byte("register()")},
		}
		for _, name := range extra {
			files["static/"+name] = &fstest.MapFile{Data: []byte(name)}
		}
		return files
	}

	base, tags := fingerprint(tree("a"), "static")
	if same, _ := fingerprint(tree("a"), "static"); same != base {
		t.Error("the same assets produced two versions")
	}
	if changed, changedTags := fingerprint(tree("b"), "static"); changed == base {
		t.Error("a changed asset did not change the version")
	} else if changedTags["/static/app.js"] == tags["/static/app.js"] {
		t.Error("a changed asset kept its entity tag")
	}
	if added, _ := fingerprint(tree("a", "icon.svg"), "static"); added == base {
		t.Error("a new asset did not change the version")
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(body)
}

func head(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Head(url)
	if err != nil {
		t.Fatalf("HEAD %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
