# Architecture

peyk has two processes and Firestore between them.

- **Laptop agent** (`cmd/peyk agent`, run by `make serve` in the `relayServer` aoe session). It reads aoe, git and Claude Code transcripts (`internal/laptop`), derives each session's state (`internal/session`), and writes `sessions/{id}` and `laptop/status` (`internal/store`). It writes a session only when its content changed, and only when told something changed: Claude Code hooks (in the dotfiles' `settings.json`) run `peyk ping` on prompt submit, notification, stop, stop failure, and session start and end, which touches a unix socket at `~/.peyk/agent.sock`; a filesystem watcher on each repository's HEAD reflog and `refs` tree catches commits and pushes made in a terminal, and one FSEvents stream over the working trees of sessions that are not working catches file edits (macOS only; changes under `.git` are ignored, so the agent's own git reads do not wake it). A second FSEvents stream over the transcript directories of working sessions catches replies written mid-turn, which no hook reports; it rereads only those sessions' last reply and last prompt, not git. Pings are debounced by 2 s. There is no timed refresh: a change nothing reported is a bug to fix, not one to paper over. The one exception is CI, below, which no event on the laptop reports. A failed refresh or watcher error stops the agent. Every 10 minutes it rewrites `seen_at` only, so the phone can tell a sleeping laptop (not seen for 15 minutes) from a running one. It authenticates by impersonating `peyk-agent@`, which holds only `roles/datastore.user`; no key file exists. Stopping it writes `online: false`.
- **Cloud Run service** (`cmd/server`). It serves the phone app: an installable page whose modules read Firestore directly with the Firebase SDK, vendored under `internal/web/static/vendor` (`make vendor`). It holds no state and needs no Firestore access yet.

The agent also publishes `projects/{name}`: every session plus every git repository under peyk's parent directory, each marked free or busy, with the repository's own mark as a data URI when it has one (the first of its app icon, logo or favicon files within four directories, outside dependency and build folders, shrunk to 96 px; a directory whose `icon.svg` is the template's placeholder is skipped; searched again every ten minutes), for the phone's project picker, inbox and worker cards. A project without a mark is drawn as its initial. The phone adds tasks to `tasks/{id}` directly; rules accept only a queued task for a published project. The phone may edit a queued task's text, project or `rank` (its place in the inbox; a task without one sorts by when it was made, and a move writes only the moved task, between its new neighbours), and delete a task while it is queued or once done. A dispatch records the session it went to; after each refresh the agent marks a dispatched task `done` once that session has replied since dispatch and stopped working with nothing needing you, noting the commit made for it if there is one (`session.Finished`). The phone shows a session's current task on its card. A task may carry up to four images (`attachments/{sha256}`, created in the same batch as the task; larger images are re-encoded on the phone to fit one document). The dispatch payload names them by hash, so the signature covers them; the agent checks each image against its hash, saves it under `~/.peyk/attachments`, and appends the paths to the prompt. Deleting a task deletes images no other task uses. `peyk import-tasks` adds tasks from JSON (used once to move the Google Keep checklists over); it writes nothing if any task names an unknown project.

Each session's git state carries `ci`: the GitHub Actions runs for the commit its upstream branch points at, when the repository is on github.com and has `.github/workflows` (`laptop.GitHub`, authenticated as the account `gh` is logged in to; without `gh` the agent runs without it). One run stands for them all: a failed one, else one still going, else a passing one; its `url` is the run's page, which the worker's page links to. GitHub cannot tell the laptop that a run moved on, so this is the one thing the agent asks for on a timer: a pushed commit it has not seen is looked up during the refresh that saw it, and after that it asks every 20 s, only while a run is queued or running, or for two minutes after the push while none has appeared. A finished run is never asked about again. A lookup that fails is logged and keeps the last result rather than stopping the agent, because the rest of the board is still right. A run that fails sends a notification.

The phone's header shows how much of the Claude subscription's 5-hour and weekly limits is left, linked to claude.ai's usage page. Claude Code reports the limits only to a status line command, so the dotfiles' `statusLine` runs `peyk usage`, which saves them to `~/.peyk/usage/claude.json` when they changed. The agent watches that directory and publishes `laptop/usage` without a refresh, since the limits change with every reply. The figures are as of the last reply in any session; a window past its reset time is shown whole.

The phone calls an aoe session a worker. Each session lists the pages its own transcript published with Claude Code's Artifact tool (`prototypes`, newest first); the agent remembers how far it read each transcript and reads only what was appended. Its card is flagged `prototype` and its detail page links to them. A session is `cleared` while its own transcript has neither a prompt nor a reply. Its card marks it `cleared`, and `clean` when its repository has nothing uncommitted or unpushed. The phone sends commands by creating `commands/{id}`: `dispatch` (a queued task to its project, creating an aoe session for a repository that has none; with `clear`, a worker that is not cleared first gets `/clear`, and the task is sent once aoe reports the new conversation), `reply` and `stop` (a session), `clear` (`/clear` to a session that is neither working nor waiting on you, unless it is already cleared), and `push` (a session's branch). The agent listens for pending commands over the same Firestore connection and records `done` or `failed` with the reason. Every type except `push` must be signed by a paired passkey, directly or through a grant. The signed string is the exact `payload`, which names the command's own document id and expires within two minutes. Directly, the payload is the WebAuthn challenge (its SHA-256). A grant spares a passkey prompt per command: the phone generates a non-extractable P-256 key in WebCrypto, keeps it in IndexedDB, has the passkey sign a statement naming its public half and an expiry at most 12 hours away, and signs each payload with that key; the command carries the statement, the passkey's assertion over it, and the key's signature. The agent verifies the assertion against public keys in `~/.peyk/keys.json` and, for a grant, the session key's signature (`internal/command`), so the agent stays stateless and unpairing a passkey voids its grants. A grant the agent refuses fails the command with a `session approval:` error, and the phone then discards it and asks the passkey again next time. Script running on the app's origin could sign commands with a live grant without a prompt; that is the cost of not prompting per command. Keys get there only through `make pair LABEL=…`, which prints a one-time link, waits for the phone to write the new passkey's public half to `pairings/{code}`, and asks you to compare the fingerprint both screens show before trusting it. Neither Firestore nor the Google account can add a key, so neither can forge a command. Passkeys are bound to `PEYK_ORIGIN`'s host; open the app only there. A command that fails is that command's result; failing to record the result stops the agent.

Notifications use standard Web Push with the laptop's own VAPID key pair (`~/.peyk/vapid.json`, created on first run; the public half is published at `laptop/push`). A phone that turns notifications on writes its push subscription to `subscriptions/{hash of endpoint}`. After each refresh the agent compares every session's state with the previous one and sends a note when a session starts needing input, hits an error, or stops working (`internal/notify`). Payloads are encrypted to the phone's subscription key, so the push service cannot read them. A subscription the push service reports gone is deleted; any other push failure stops the agent.

Browsers only read, and only an account carrying the `owner` custom claim, set with `make owner EMAIL=…` after that account's first sign-in. Every other read and every other browser write is denied; `tests/rules` proves both. The phone listens only while the app is visible, and opens a new Firestore connection each time it comes back: a phone that froze the app leaves the old connection dead without an offline event, and the SDK would serve cached data until its next retry.

Work that outlives a response does not run. CPU is allocated only while a request is being handled, so a goroutine still going after the handler returns is throttled the moment it is on its own — it finishes late, or never, and nothing reports an error. Either complete the work before writing the response, or let the client trigger a second request and turn background work into ordinary request-driven work.

Cloud Run uses request-based billing, `min=0`, bounded maximum instances, 256 MiB, and concurrency. Cold starts are an accepted tradeoff. Do not add an always-on worker, VPC connector, load balancer, static IP, or minimum instance without revisiting the cost model.

## It installs on a phone

The page ships as an installable app from the first commit: a manifest, the PNG
icons a launcher needs (`cmd/icon` draws them, so no rasteriser is needed to
build: the mark is a chat bubble holding پ's three dots, white on the accent
blue for launchers, and alone in white on transparent for the notification
badge, because Android draws a badge from its alpha channel only), the metas iOS reads for a home screen icon, and safe-area insets so an
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

`make test` runs formatting/module verification, race-enabled Go tests, Firestore rules tests, Firestore adapter integration tests, and Playwright. Both Firestore layers use the local emulator. Playwright runs inside the Auth and Firestore emulators: it signs in through the Auth emulator's Google sign-in page, is refused until the owner claim is set, then exercises the board, a push command, and inbox add, edit and delete against data seeded as the agent would publish it. Passkey prompts cannot be driven in a test browser; their verification is covered by Go tests that sign real assertions and retains traces/screenshots on failure, which gives an agent inspectable evidence without production access.

