# Project instructions

## Documentation

- Code, names, types, and tests carry the architecture and its reasons. Put a reason in a comment at the code it constrains, not in Markdown.
- Markdown holds only what code cannot: commands to run, manual console steps, and the invariants below. Do not add architecture docs, feature descriptions, or change history.

## Verification

- `make test` is the authoritative local suite. Rules, integration, and E2E tests use repository-local emulators and the ports in `firebase.json` and `Makefile`.
- Automated tests must never use cloud credentials or production data.

## Laptop agent

- The `com.peyk.agent` launchd job runs `make serve`. When a change affects the agent, run `make restart-agent` without asking. When it affects `menubar/`, run `make install-agent`.
- Claude Code hooks and `statusLine` in the dotfiles' `settings.json` call `peyk ping` and `peyk usage`; renaming either breaks the laptop outside this repository.

## Invariants

- The agent works only when an event says something changed. Fix a missed change at its event source; never add a timed refresh. CI status is the one exception.
- Every command that makes the laptop act is signed by a passkey paired through `make pair`. Firestore and the Google account must never be able to add a key.
- Keep production code independent of tests; inject clocks, IDs, and stores from tests.
- Prefer the Go standard library and one deployable service until a product requirement proves otherwise.
- Cloud Run stays at zero minimum instances. Work must finish within its request or be triggered by another request/event; never rely on a response-outliving goroutine.
- Persist state only in the `(default)` Firestore database. Browser access is deny-by-default, and every rules relaxation needs an emulator test.
- Nothing a third party serves may block the first paint. Keep the head's blocking CSS and scripts on our own origin; vendor or `defer` anything else. The `default-src 'self'` CSP enforces this, so relaxing it means re-checking every head resource.
- The service worker may cache the shell and offline page, never data-rendered pages. Cache names and asset ETags remain content-derived.
- Every page registers the service worker, and the worker answers a failed navigation with `/offline`. Never make registration conditional on a feature such as push.
- If browser storage holds identity or state, an installed PWA's `start_url` must restore that state rather than opening `/` as a fresh app.
- CI uses Workload Identity Federation; secrets belong in Secret Manager, never repository files or downloaded service-account keys.
