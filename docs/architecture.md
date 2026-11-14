# Architecture

peyk has two processes and Firestore between them.

- **Laptop agent** (`cmd/peyk agent`, run by `make serve` in the `relayServer` aoe session). It reads aoe, git and Claude Code transcripts (`internal/laptop`), derives each session's state (`internal/session`), and writes `sessions/{id}` and `laptop/status` (`internal/store`). It writes a session only when its content changed. It does no polling: hooks run `peyk ping`, which touches a unix socket at `~/.peyk/agent.sock`; pings are debounced by 2 s, and a 10-minute heartbeat refreshes everything and marks the laptop seen. It authenticates by impersonating `peyk-agent@`, which holds only `roles/datastore.user`; no key file exists. Stopping it writes `online: false`.
- **Cloud Run service** (`cmd/server`). It serves the phone app: an installable page whose modules read Firestore directly with the Firebase SDK, vendored under `internal/web/static/vendor` (`make vendor`). It holds no state and needs no Firestore access yet.

Browsers only read, and only an account carrying the `owner` custom claim, set with `make owner EMAIL=…` after that account's first sign-in. Every other read and every browser write is denied; `tests/rules` proves both. The phone listens only while the app is visible.

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

## Testing pyramid

`make test` runs formatting/module verification, race-enabled Go tests, Firestore rules tests, Firestore adapter integration tests, and Playwright. Both Firestore layers use the local emulator. Playwright starts the real server without Firebase config and retains traces/screenshots on failure, which gives an agent inspectable evidence without production access.

