# Architecture

peyk starts as one stateless Go process. It serves HTTP, owns all writes, and uses a narrow `Store` interface. With no project configured it uses memory; on Cloud Run it uses Firestore. This makes local work and browser tests quick, deterministic, and credential-free while keeping production behavior explicit.

The initial Firestore rules deny all browser access. If instant client-side reads become valuable, open only the required documents and query shapes, then prove ownership and enumeration boundaries in `tests/rules`.

Work that outlives a response does not run. CPU is allocated only while a request is being handled, so a goroutine still going after the handler returns is throttled the moment it is on its own — it finishes late, or never, and nothing reports an error. Either complete the work before writing the response, or let the client trigger a second request and turn background work into ordinary request-driven work.

Cloud Run uses request-based billing, `min=0`, bounded maximum instances, 256 MiB, and concurrency. Cold starts are an accepted tradeoff. Do not add an always-on worker, VPC connector, load balancer, static IP, or minimum instance without revisiting the cost model.

## It installs on a phone

The page ships as an installable app from the first commit: a manifest, the PNG
icons a launcher needs (`cmd/icon` draws them, so no rasteriser is needed to
build), the metas iOS reads for a home screen icon, and safe-area insets so an
installed app does not draw under a notch. Retrofitting this later is
straightforward; discovering in month three that the product should have been
installable is not.

Three traps are pre-solved, because each is silent.

**An installed icon on iOS launches into its own storage container.** It is a
fresh browser: none of the cookies or local storage the same person has in
Safari. If what identifies a user's work lives in browser storage, a `start_url`
of `/` hands them an empty app on first launch, and it looks exactly like data
loss. Name the thing in `start_url` — which is why the manifest is generated
rather than served as a file — and give the app a way back in that does not
need an address bar it does not have.

**A cached asset can outlive the release that replaced it.** The worker's cache
is named after a digest of every embedded asset, so a changed file is a changed
worker and a changed worker rebuilds the cache; there is no version number to
remember to raise. Each asset also carries its own digest as an entity tag,
because the worker refills that cache with ordinary fetches and embedded files
have no modification time to revalidate against. Without the second half the
first is a trap: the rebuild would be answered from the browser's own cache and
would faithfully restore the file the release was meant to replace.

**A launch that cannot reach the server has nowhere to show an error.** In
standalone there is no browser chrome, and Android answers a failed navigation
with a blank white screen — which reads as a broken app rather than a missing
network. So the worker falls back to `/offline`, a document that fetches
nothing, and every page registers the worker rather than only the pages of
whatever feature happened to need one first. A worker registered behind a
feature flag is a fallback that exists for the people who opted in and for
nobody else.

The worker caches the shell and never a page. Every page here is rendered from
data, and a stale answer presented as a current one is worse than an honest
`/offline`.

## The dashboard counts events

`internal/analytics` is the whole of it. The app emits typed events as JSON
lines on stdout; a log sink copies them into BigQuery; `/admin` runs one query,
generated from the event catalog, that counts them per day and dimension. The
page reports what happened in a window and never application state, because a
document says what is true now and a dashboard figure has to say what happened.

Three things keep the figures true to the code:

- The catalog is Go types, pinned by `testdata/events.golden`. Renaming an
  event or changing a field's type fails the tests, because the sink infers
  each column's type once and silently drops rows that disagree.
- Locally and in tests the same lines go to `analytics.Memory`, which answers
  with the counts the query would return. The dashboard's own tests click
  through the app and read the number on the page, so a handler that stops
  emitting, or a reader that stops matching, fails there.
- Every sum names its event by type and its dimensions by field name, and
  panics on one the event does not declare; a typo cannot become a figure that
  reads zero forever.

Emit an event only after the work it reports has succeeded. The page shows when
the newest event arrived, and says so when BigQuery cannot be read, because a
stalled pipeline and a quiet day must not look alike.

The page has one layout, drawn by `internal/web/admin.go`, and a new figure
goes into it rather than beside it:

- A row of range links, then the window and when the newest event arrived.
- A grid of figures, one for each thing people do in the app. Under each figure
  a caption breaks it down: how often it was undone, or how it splits by a
  dimension.
- A table of days, newest first and labelled the way a person says them
  ("Today", "Yesterday", "Mon 28 Sep"), with one column per series. Each
  column's bars scale to that column's largest value, and a day with no events
  is dimmed rather than left out.
- Further tables in the same form for a split that has too many values for a
  caption.

An event is added to the page in the commit that starts emitting it, as a
figure, a caption or a column. Every number on the page carries a `data-figure`
name, which is what the tests read.

Every file in `internal/analytics` except `events.go` and the two filters is
meant to be identical across projects; change them here first.

## Testing pyramid

`make test` runs formatting/module verification, race-enabled Go tests, Firestore rules tests, Firestore adapter integration tests, and Playwright. Both Firestore layers use the local emulator. Playwright starts the real server with memory storage and retains traces/screenshots on failure, which gives an agent inspectable evidence without production access.

