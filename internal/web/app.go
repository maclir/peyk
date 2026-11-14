package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"mime"
	"net/http"
	"time"

	"github.com/maclir/peyk/internal/analytics"
	"github.com/maclir/peyk/internal/idea"
	"github.com/maclir/peyk/internal/store"
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

type App struct {
	store     store.Store
	events    *analytics.Logger
	reader    analytics.Reader
	now       func() time.Time
	ids       func() string
	page      *template.Template
	adminPage *template.Template
}

// New serves the app. events is where it reports what happened; reader is where
// the dashboard reads those reports back.
func New(s store.Store, events *analytics.Logger, reader analytics.Reader) *App {
	return &App{
		store: s, events: events, reader: reader, now: time.Now, ids: newID,
		page:      template.Must(template.New("page").Parse(pageHTML)),
		adminPage: adminTemplate,
	}
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+HealthPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Handle("GET /static/", revalidated(http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /sw.js", serviceWorker)
	mux.HandleFunc("GET "+ManifestPath, manifest)
	mux.HandleFunc("GET "+OfflinePath, offline)
	mux.HandleFunc("GET /", a.home)
	mux.HandleFunc("POST /ideas", a.create)
	mux.HandleFunc("GET "+AdminPath, a.admin)
	return securityHeaders(mux)
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	ideas, err := a.store.List(r.Context())
	if err != nil {
		http.Error(w, "could not load ideas", http.StatusInternalServerError)
		return
	}
	if err := a.page.Execute(w, ideas); err != nil {
		http.Error(w, "could not render page", http.StatusInternalServerError)
	}
}

func (a *App) create(w http.ResponseWriter, r *http.Request) {
	v, err := idea.New(a.ids(), r.FormValue("title"), a.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.store.Create(r.Context(), v); err != nil {
		http.Error(w, "could not save idea", http.StatusInternalServerError)
		return
	}
	a.events.Emit(analytics.IdeaCreated{})
	http.Redirect(w, r, "/", http.StatusSeeOther)
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

func newID() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
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
<meta name="viewport" content="width=device-width,initial-scale=1,minimum-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">
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
<style>` + pageCSS + `</style></head>
<body><main><h1>peyk</h1><p>A small place to start something.</p>
<form method="post" action="/ideas"><label for="title">New idea</label><input id="title" name="title" required><button>Add</button></form>
<ul>{{range .}}<li>{{.Title}}</li>{{else}}<li>No ideas yet.</li>{{end}}</ul></main>
<script src="/static/app.js"></script></body></html>`

// color-scheme is what tells the browser to draw the surfaces this stylesheet
// does not own — the form controls, the scrollbar — for whichever mode the page
// is in. Without it they are drawn light over a dark page.
const pageCSS = `html{-webkit-text-size-adjust:100%;color-scheme:light dark}
body{font:18px system-ui;max-width:42rem;margin:4rem auto;color:#202124;background:#ffffff;-webkit-tap-highlight-color:transparent;
padding:0 calc(1rem + env(safe-area-inset-right,0px)) calc(1rem + env(safe-area-inset-bottom,0px)) calc(1rem + env(safe-area-inset-left,0px))}
form{display:flex;gap:.5rem}input{flex:1;padding:.7rem}button{padding:.7rem 1rem}li{margin:.7rem 0}
@media(prefers-color-scheme:dark){body{color:#e8eaed;background:#131516}}`

// offlineHTML is what an installed icon opens when there is no signal. It says
// what is wrong rather than showing the browser's error page, and it never
// pretends to show data.
const offlineHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,minimum-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#131516" media="(prefers-color-scheme: dark)">
<title>Offline</title>
<link rel="icon" href="/static/icon.svg" type="image/svg+xml">
<style>` + pageCSS + `</style></head>
<body><main><h1>Offline</h1>
<p>This needs the network, and there is none right now. Everything you saved is
on the server, waiting.</p>
<p><a href="/">Try again</a></p></main></body></html>`
