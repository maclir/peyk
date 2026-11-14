package web

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/maclir/peyk/internal/analytics"
	"github.com/maclir/peyk/internal/store"
)

// AdminPath is the dashboard. It is reached with ?key=, checked against
// config/admin in the store.
const AdminPath = "/admin"

// adminPage is everything the dashboard template draws. Every figure is a count
// of events in the window; nothing here reads application state.
type adminPage struct {
	Key         string
	Range       analytics.Range
	Ranges      []analytics.Range
	Unavailable bool
	// Latest is when the newest event in the window was written, so a pipeline
	// that has stopped delivering looks stale instead of quiet.
	Latest time.Time

	Ideas int

	Days []adminDay
}

type adminDay struct {
	Date string
	// Label is how a person reads the day: "Today", "Yesterday", "Mon 28 Sep".
	Label string
	Ideas int
	// IdeasWidth is a percentage of the busiest day for that series, worked out
	// here because arithmetic in a template cannot be tested.
	IdeasWidth int
}

func (a *App) admin(w http.ResponseWriter, r *http.Request) {
	if !a.adminKeyOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rng := analytics.ParseRange(r.URL.Query().Get("range"))
	now := a.now().UTC()
	since := now.Add(-rng.Window)
	page := adminPage{Key: r.URL.Query().Get("key"), Range: rng, Ranges: analytics.Ranges}

	snap, err := a.reader.Read(r.Context(), since)
	if err != nil {
		// Zeroes would read as a quiet day. Saying the warehouse is unreachable
		// is the only honest page.
		log.Printf("admin: read analytics: %v", err)
		page.Unavailable = true
	} else {
		fillAdmin(&page, snap, since, now)
	}

	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.adminPage.Execute(w, page); err != nil {
		log.Printf("admin: render: %v", err)
	}
}

func fillAdmin(page *adminPage, snap analytics.Snapshot, since, now time.Time) {
	created := analytics.IdeaCreated{}
	page.Latest = snap.Latest
	page.Ideas = snap.Total(created)

	ideas := snap.PerDay(created)
	peak := 0
	for _, n := range ideas {
		peak = max(peak, n)
	}
	days := analytics.Days(since, now)
	slices.Reverse(days)
	for _, d := range days {
		page.Days = append(page.Days, adminDay{Date: d, Label: dayLabel(d, now), Ideas: ideas[d], IdeasWidth: percent(ideas[d], peak)})
	}
}

func percent(n, peak int) int {
	if n <= 0 || peak <= 0 {
		return 0
	}
	return n * 100 / peak
}

func dayLabel(day string, now time.Time) string {
	t, err := time.Parse(analytics.DayLayout, day)
	if err != nil {
		return day
	}
	switch day {
	case now.UTC().Format(analytics.DayLayout):
		return "Today"
	case now.UTC().AddDate(0, 0, -1).Format(analytics.DayLayout):
		return "Yesterday"
	}
	return t.Format("Mon 2 Jan")
}

// adminKeyOK compares in constant time, so the response time says nothing about
// how much of a guess was right. A missing or unreadable key locks the page.
func (a *App) adminKeyOK(r *http.Request) bool {
	supplied := r.URL.Query().Get("key")
	if supplied == "" {
		return false
	}
	want, err := a.store.AdminKey(r.Context())
	if err != nil {
		if !errors.Is(err, store.ErrNoAdminKey) {
			log.Printf("admin: read key: %v", err)
		}
		return false
	}
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(want)) == 1
}

var adminTemplate = template.Must(template.New("admin").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="robots" content="noindex">
<meta name="color-scheme" content="light dark">
<title>peyk dashboard</title>
<link rel="icon" href="/static/icon.svg" type="image/svg+xml">
<style>` + adminCSS + `</style></head>
<body><main><h1>Dashboard</h1>
<nav>{{$key := .Key}}{{$cur := .Range.Key}}{{range .Ranges}}<a href="?key={{$key}}&amp;range={{.Key}}"{{if eq .Key $cur}} aria-current="page"{{end}}>{{.Label}}</a>{{end}}</nav>
{{if .Unavailable}}<p role="alert">The analytics warehouse could not be read. These figures are unknown, not zero.</p>
{{else}}
<p class="window">Last {{.Range.Label}} · <span data-figure="latest">{{if .Latest.IsZero}}No events in this window{{else}}Latest event {{.Latest.Format "2 Jan, 15:04"}} UTC{{end}}</span></p>
<div class="tiles">
<div><h2>Ideas created</h2><p class="figure" data-figure="ideas">{{.Ideas}}</p></div>
</div>
<table data-table="days"><thead><tr><th>Day</th><th><i></i>Ideas</th></tr></thead>
<tbody>{{range .Days}}<tr{{if eq .Ideas 0}} class="quiet"{{end}}><th scope="row"><time datetime="{{.Date}}">{{.Label}}</time></th><td><div><span>{{.Ideas}}</span><b style="width:{{.IdeasWidth}}%"></b></div></td></tr>{{end}}</tbody></table>
{{end}}</main></body></html>`))

// adminCSS is the dashboard's whole stylesheet: a card on a ground, a row of
// range links, a grid of figures with what they break down into, and tables
// whose bars each scale to their own column. A second series in a table takes
// class "second" on its swatch and its bar. The table layout is fixed so that
// every table on the page has the same columns whatever its labels are.
const adminCSS = `:root{color-scheme:light dark;--ground:#faf9f7;--card:#fff;--ink:#171717;--strong:#262626;--on-strong:#fff;--text:#525252;--label:#737373;--faint:#a3a3a3;--quiet:#d4d4d4;--tile:#fafafa;--rule:#f5f5f5}
@media(prefers-color-scheme:dark){:root{--ground:#0a0a0a;--card:#171717;--ink:#f5f5f5;--strong:#e5e5e5;--on-strong:#171717;--text:#d4d4d4;--label:#a3a3a3;--faint:#737373;--quiet:#525252;--tile:#262626;--rule:#262626}}
*{box-sizing:border-box}html{-webkit-text-size-adjust:100%}
body{margin:0;min-height:100vh;display:flex;font:16px/1.5 Inter,ui-sans-serif,system-ui,sans-serif;color:var(--ink);background:var(--ground);
padding:1.5rem calc(1rem + env(safe-area-inset-right,0px)) calc(1.5rem + env(safe-area-inset-bottom,0px)) calc(1rem + env(safe-area-inset-left,0px))}
main{margin:auto;width:100%;max-width:32rem;padding:1rem;border-radius:1rem;background:var(--card)}
@media(min-width:640px){main{padding:1.5rem}}
h1{margin:0 0 .5rem;font-size:1.5rem;line-height:2rem;font-weight:700;letter-spacing:-.025em;text-align:center}
nav{display:flex;justify-content:center;gap:.5rem;margin-bottom:1rem}
nav a{padding:.25rem .75rem;border-radius:.5rem;font-size:.75rem;line-height:1rem;color:var(--label);text-decoration:none}
nav a[aria-current]{color:var(--on-strong);background:var(--strong)}
p{margin:0;font-size:.75rem;line-height:1rem;color:var(--faint)}
p[role=alert]{padding:2rem 0;font-size:1rem;line-height:1.5rem;text-align:center;color:var(--label)}
.window{margin-bottom:1.5rem;text-align:center}
.tiles{display:grid;grid-template-columns:1fr 1fr;gap:.75rem;margin-bottom:2rem}
.tiles div{padding:.75rem 1rem;border-radius:.5rem;background:var(--tile)}.tiles div:only-child{grid-column:1/-1}
h2{margin:0;font-size:.75rem;line-height:1rem;font-weight:400;color:var(--label)}
.figure{font-size:1.875rem;line-height:2.25rem;font-weight:600;color:var(--strong)}
.figure,table{font-variant-numeric:tabular-nums}
table{width:100%;table-layout:fixed;border-collapse:collapse;font-size:.75rem;line-height:1rem;color:var(--text)}
table+table{margin-top:2rem}
th,td{padding:0}th{text-align:left;font-weight:400}thead th,time{white-space:nowrap}
thead th{padding-bottom:.5rem;color:var(--faint)}thead th:first-child{width:26%}
tbody tr+tr{border-top:1px solid var(--rule)}
tbody th,td{padding:.375rem .75rem .375rem 0}tbody th{overflow-wrap:anywhere}td:last-child{padding-right:0}
td div{display:flex;align-items:center;gap:.5rem}td span{flex:none;width:1.5rem;text-align:right}
td b{display:block;height:.5rem;border-radius:2px;background:var(--strong)}
thead i{display:inline-block;width:.5rem;height:.5rem;margin-right:.25rem;border-radius:2px;background:var(--strong)}
.second{background:var(--faint)}
.quiet{color:var(--quiet)}`
