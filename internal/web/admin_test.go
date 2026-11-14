package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/analytics"
	"github.com/maclir/peyk/internal/idea"
	"github.com/maclir/peyk/internal/store"
)

var testNow = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

// newTestApp wires the app to an in-memory warehouse, the way the local server
// and the end-to-end suite run.
func newTestApp() *App {
	a, _ := newTestAppWithWarehouse()
	return a
}

func newTestAppWithWarehouse() (*App, *analytics.Memory) {
	m := analytics.NewMemory(func() time.Time { return testNow })
	a := New(store.NewMemory(), analytics.NewLogger(m), m)
	a.now = func() time.Time { return testNow }
	return a, m
}

func adminGet(t *testing.T, h http.Handler, query string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AdminPath+"?"+query, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestAdminNeedsTheKey(t *testing.T) {
	h := newTestApp().Handler()
	for _, q := range []string{"", "key=", "key=wrong", "key=" + store.MemoryAdminKey + "x"} {
		if code, _ := adminGet(t, h, q); code != http.StatusUnauthorized {
			t.Errorf("%q = %d, want 401", q, code)
		}
	}
	if code, _ := adminGet(t, h, "key="+store.MemoryAdminKey); code != http.StatusOK {
		t.Errorf("right key = %d, want 200", code)
	}
}

type lockedStore struct{ store.Store }

func (lockedStore) AdminKey(context.Context) (string, error) { return "", store.ErrNoAdminKey }

func TestAdminIsLockedWithoutAConfiguredKey(t *testing.T) {
	m := analytics.NewMemory(time.Now)
	h := New(lockedStore{store.NewMemory()}, analytics.NewLogger(m), m).Handler()
	if code, _ := adminGet(t, h, "key="); code != http.StatusUnauthorized {
		t.Fatalf("empty key against no key = %d, want 401", code)
	}
}

// The dashboard counts the events the handlers emit, through the same lines
// the sink carries, so a handler that stops emitting shows up here.
func TestAdminCountsIdeasCreated(t *testing.T) {
	a := newTestApp()
	h := a.Handler()
	for _, title := range []string{"one", "two", " "} {
		req := httptest.NewRequest(http.MethodPost, "/ideas", strings.NewReader(url.Values{"title": {title}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	_, body := adminGet(t, h, "key="+store.MemoryAdminKey)
	if !strings.Contains(body, `data-figure="ideas">2<`) {
		t.Fatalf("want 2 ideas (the blank one failed):\n%s", body)
	}
	if !strings.Contains(body, "Latest event 4 Mar, 12:00 UTC") {
		t.Fatalf("latest event missing:\n%s", body)
	}
}

type failingStore struct{ store.Store }

func (failingStore) Create(context.Context, idea.Idea) error { return errors.New("down") }

func TestAFailedSaveIsNotCounted(t *testing.T) {
	m := analytics.NewMemory(time.Now)
	a := New(failingStore{store.NewMemory()}, analytics.NewLogger(m), m)
	req := httptest.NewRequest(http.MethodPost, "/ideas", strings.NewReader("title=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.Handler().ServeHTTP(httptest.NewRecorder(), req)
	snap, _ := m.Read(context.Background(), time.Time{})
	if n := snap.Total(analytics.IdeaCreated{}); n != 0 {
		t.Fatalf("counted %d failed saves", n)
	}
}

func TestAdminDrawsEveryDayInTheWindow(t *testing.T) {
	_, body := adminGet(t, newTestApp().Handler(), "key="+store.MemoryAdminKey+"&range=7d")
	if n := strings.Count(body, `<time datetime=`); n != 8 {
		t.Fatalf("drew %d days for a 7 day window, want 8 (both ends)", n)
	}
	if !strings.Contains(body, "No events in this window") {
		t.Fatalf("an empty window should say so:\n%s", body)
	}
}

func TestAdminListsDaysNewestFirst(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	var page adminPage
	fillAdmin(&page, analytics.Snapshot{}, now.Add(-7*24*time.Hour), now)
	var got []string
	for _, d := range page.Days[:4] {
		got = append(got, d.Label)
	}
	if want := "Today,Yesterday,Sun 27 Sep,Sat 26 Sep"; strings.Join(got, ",") != want {
		t.Errorf("days = %v, want %s", got, want)
	}
	if last := page.Days[len(page.Days)-1].Date; last != "2026-09-22" {
		t.Errorf("oldest day = %s, want 2026-09-22", last)
	}
}

func TestAdminKeepsTheKeyInItsLinks(t *testing.T) {
	_, body := adminGet(t, newTestApp().Handler(), "key="+store.MemoryAdminKey)
	for _, r := range analytics.Ranges {
		if !strings.Contains(body, "?key="+store.MemoryAdminKey+"&amp;range="+r.Key) {
			t.Errorf("no link to %s carrying the key:\n%s", r.Key, body)
		}
	}
}

func TestAdminSaysWhenTheWarehouseIsUnreachable(t *testing.T) {
	a, m := newTestAppWithWarehouse()
	m.Fail(errors.New("down"))
	_, body := adminGet(t, a.Handler(), "key="+store.MemoryAdminKey)
	if !strings.Contains(body, "could not be read") || strings.Contains(body, `data-figure=`) {
		t.Fatalf("an unreadable warehouse must not draw figures:\n%s", body)
	}
}
