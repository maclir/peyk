# Project instructions

## Verification

- `make test` is the authoritative local suite: formatting/module/vet checks, race-enabled Go tests, Firestore rules tests, Firestore integration tests, and Chromium E2E tests.
- Rules, integration, and E2E tests use repository-local emulator state and the ports configured in `firebase.json` and `Makefile`.
- Automated tests must never use cloud credentials or production data.

## Laptop agent

- The `com.peyk.agent` launchd job runs `make serve`, which builds and runs the laptop agent. When a change affects the agent, restart it without asking: `make restart-agent`. When a change affects the menu bar app (`menubar/`), rebuild and relaunch both jobs: `make install-agent`.

## Invariants

- Keep production code independent of tests; inject clocks, IDs, and stores from tests.
- Prefer the Go standard library and one deployable service until a product requirement proves otherwise.
- Cloud Run stays at zero minimum instances. Work must finish within its request or be triggered by another request/event; never rely on a response-outliving goroutine.
- Persist state only in the `(default)` Firestore database. Browser access is deny-by-default, and every rules relaxation needs an emulator test.
- Nothing a third party serves may block the first paint. Keep the head's blocking CSS and scripts on our own origin; vendor or `defer` anything else. The `default-src 'self'` CSP enforces this, so relaxing it means re-checking every head resource.
- The service worker may cache the shell and offline page, never data-rendered pages. Cache names and asset ETags remain content-derived.
- Every page registers the service worker, and the worker answers a failed navigation with `/offline`. Standalone has no browser chrome to draw an error page in, so an installed app whose launch cannot reach the server opens to a blank white screen. Never make registration conditional on a feature such as push.
- If browser storage holds identity or state, an installed PWA's `start_url` must restore that state rather than opening `/` as a fresh app.
- CI uses Workload Identity Federation; secrets belong in Secret Manager, never repository files or downloaded service-account keys.
