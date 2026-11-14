package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log"
	"mime"
	"net/http"
	"time"
)

func init() {
	// Go does not know this extension, and the responses carry nosniff, so an
	// icon served as octet-stream is simply dropped.
	_ = mime.AddExtensionType(".ico", "image/x-icon")
}

// HealthPath is where readiness is reported.
//
// Deliberately not /healthz: Google's frontend reserves that path on
// *.run.app and answers it with its own 404, so the container never sees the
// request and every deploy smoke test fails against a service that is
// perfectly healthy.
const HealthPath = "/health"

// Firebase is the web app's public Firebase configuration. None of it is
// secret: it names the project, and Firestore rules and the owner claim decide
// what a signed-in browser may read.
type Firebase struct {
	APIKey     string `json:"apiKey"`
	AuthDomain string `json:"authDomain"`
	ProjectID  string `json:"projectId"`
	AppID      string `json:"appId"`
	// Emulators, for browser tests, points the page at local Auth and
	// Firestore emulators (host:port each) instead of Google.
	Emulators *Emulators `json:"emulators,omitempty"`
}

type Emulators struct {
	Auth      string `json:"auth"`
	Firestore string `json:"firestore"`
}

type App struct {
	firebase Firebase
	page     *template.Template
}

// New serves the phone app. An empty Firebase config serves the shell with a
// notice instead of a sign-in button, which is what local runs and tests get.
func New(fb Firebase) *App {
	return &App{firebase: fb, page: template.Must(template.New("page").Parse(pageHTML))}
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+HealthPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Handle("GET /static/", revalidated(http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /sw.js", serviceWorker)
	mux.HandleFunc("GET "+ManifestPath, manifest)
	mux.HandleFunc("GET "+OfflinePath, offline)
	mux.HandleFunc("GET /{$}", a.home)
	return securityHeaders(a.firebase, mux)
}

func (a *App) home(w http.ResponseWriter, _ *http.Request) {
	cfg := ""
	if a.firebase.APIKey != "" {
		b, _ := json.Marshal(a.firebase)
		cfg = string(b)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.page.Execute(w, cfg); err != nil {
		http.Error(w, "could not render page", http.StatusInternalServerError)
	}
}

// OfflinePath is what the worker shows when a launch cannot reach the service.
// It is a document of its own rather than a cached copy of a real page, because
// every page here is rendered from data, and a stale answer presented as a
// current one is worse than an honest empty-handed one.
const OfflinePath = "/offline"

func offline(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(offlineHTML)); err != nil {
		log.Printf("write offline page: %v", err)
	}
}

// revalidated lets a browser keep an asset but never trust it blindly. The
// files are embedded, so they have no modification time and http.FileServer
// offers a browser nothing to ask a question with; their content digest is that
// question. "no-cache" is not "do not store" — it stores, and asks first, which
// costs an empty 304 and cannot serve last week's stylesheet.
func revalidated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if etag, ok := staticETags[r.URL.Path]; ok {
			// ServeContent answers If-None-Match from this, so setting it here
			// is what turns a repeat request into a 304.
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// serviceWorkerBody is the worker with this build's version compiled into it.
// The substitution happens once, because the answer cannot change while the
// process is running.
var serviceWorkerBody = func() []byte {
	body, err := staticFS.ReadFile("static/sw.js")
	if err != nil {
		panic("read embedded service worker: " + err.Error())
	}
	return bytes.ReplaceAll(body, []byte("__VERSION__"), []byte(staticVersion))
}()

// serviceWorker is served from the root because a worker's scope cannot rise
// above its own path, and this one has to control the whole app.
//
// It is the update mechanism for everything else, so it is the one file that
// must never be answered from a cache without asking.
func serviceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("ETag", `"`+staticVersion+`"`)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "sw.js", time.Time{}, bytes.NewReader(serviceWorkerBody))
}

// securityHeaders allows exactly what Firebase sign-in and Firestore need:
// Google's APIs for data and tokens, apis.google.com for the sign-in helper,
// and the project's auth domain for the sign-in popup's frame.
func securityHeaders(fb Firebase, next http.Handler) http.Handler {
	connect, frame := "'self' https://*.googleapis.com", ""
	if fb.AuthDomain != "" {
		frame = "https://" + fb.AuthDomain
	}
	if e := fb.Emulators; e != nil {
		connect += " http://" + e.Auth + " http://" + e.Firestore
		frame = "http://" + e.Auth
	}
	csp := "default-src 'self'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; " +
		"script-src 'self' https://apis.google.com; connect-src " + connect + "; img-src 'self' data: blob:"
	if frame != "" {
		csp += "; frame-src " + frame
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// The page is installable from the first commit: a manifest, the icons a
// launcher needs, and the metas iOS reads for a home screen icon. The status
// bar is left "default" deliberately — it then follows the page's own
// background, where black-translucent would paint white text over a white page.
//
// The page has a dark half from the first commit too, and it is not decoration:
// an installed app's status bar follows the phone, so a product that stays
// white after dark is a white page under a black strip. The two theme-colour
// metas below paint that strip in a browser tab; the manifest names no colour
// at all, which is what makes the installed app's strip follow the phone. See
// manifest.go.
//
// The insets are what stop an installed app from drawing under a notch or a
// home indicator. In a browser tab they are all zero and nothing moves.
const pageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,minimum-scale=1,viewport-fit=cover,interactive-widget=resizes-content">
<meta name="theme-color" content="#f5f6f8" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#131516" media="(prefers-color-scheme: dark)">
<meta name="mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-title" content="peyk">
<meta name="apple-mobile-web-app-status-bar-style" content="default">
<title>peyk</title>
<link rel="icon" href="/static/icon.svg" type="image/svg+xml">
<link rel="icon" href="/static/favicon.ico" sizes="16x16 32x32 48x48">
<link rel="apple-touch-icon" href="/static/apple-touch-icon.png">
<link rel="manifest" href="/manifest.webmanifest">
<link rel="stylesheet" href="/static/app.css">
<style>` + pageCSS + `</style></head>
<body data-firebase="{{.}}">
<header class="bar"><h1><img src="/static/icon.svg" alt="" width="24" height="24">peyk</h1><span id="laptop" class="laptop">connecting…</span></header>
<main id="app"><p class="notice" id="notice">Loading…</p></main>
<script type="module" src="/static/app.js"></script></body></html>`

// color-scheme is what tells the browser to draw the surfaces this stylesheet
// does not own — the form controls, the scrollbar — for whichever mode the page
// is in. Without it they are drawn light over a dark page.
const pageCSS = `html{-webkit-text-size-adjust:100%;color-scheme:light dark}
body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:0 auto;color:#18212e;background:#f5f6f8;-webkit-tap-highlight-color:transparent;
padding:env(safe-area-inset-top,0px) calc(1rem + env(safe-area-inset-right,0px)) calc(1rem + env(safe-area-inset-bottom,0px)) calc(1rem + env(safe-area-inset-left,0px))}
@media(prefers-color-scheme:dark){body{color:#e4e9f1;background:#131516}}`

// offlineHTML is what an installed icon opens when there is no signal. It says
// what is wrong rather than showing the browser's error page, and it never
// pretends to show data.
const offlineHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,minimum-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#f5f6f8" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#131516" media="(prefers-color-scheme: dark)">
<title>Offline</title>
<link rel="icon" href="/static/icon.svg" type="image/svg+xml">
<style>` + pageCSS + `</style></head>
<body><main><h1>Offline</h1>
<p>This needs the network, and there is none right now. Everything you saved is
on the server, waiting.</p>
<p><a href="/">Try again</a></p></main></body></html>`
