import { renderMarkdown } from "./markdown.js";
import { STATES, actionsLeft, activity, ago, usageLeft, answerText, awaitingReply, currentTask, filterQueue, laptopOnline, mergeReply, monogram, pendingLabel, pushLabel, queue, queueCounts, quickAction, rankAt, replyFields, resumeAction, doneError, searchProjects, sentTasks, sortProjects, sortSessions, tags, waitingReplies, withDelivered } from "./board.js";
import { MAX_IMAGES, imageID, orphanedImages, prepareImage } from "./images.js";
import * as passkey from "./passkey.js";
import { sharedText, takeShare } from "./share.js";
import { revision } from "./release.js";

// Registration is unconditional: the worker is what lets an installed app open
// with no signal at all.
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}

// Adds to the service worker's notification log, which #log shows.
const noteLog = (event, detail) => navigator.serviceWorker?.controller?.postMessage({ type: "log", event, detail });

// A notification click asks the open window to show its worker. This answers
// before sign-in finishes, so a page still starting is not navigated away.
navigator.serviceWorker?.addEventListener("message", (e) => {
  if (e.data?.type !== "open") return;
  const url = new URL(e.data.url, location.href);
  noteLog("opened", { url: url.href, from: location.hash, hidden: document.hidden });
  e.ports[0]?.postMessage(true);
  if (url.pathname !== location.pathname) location.assign(url.href);
  else if (url.hash !== location.hash) location.hash = url.hash;
  else window.dispatchEvent(new HashChangeEvent("hashchange"));
});

// The footer names the revision this page's code came from, which the worker
// may have served from its cache. Tapping it asks the server which revision is
// deployed; when another one is, it turns yellow, and tapping it then drops
// the cached shell before reloading, since a plain reload would be answered
// from that same cache.
const revEl = document.getElementById("rev");
revEl.textContent = revision;
// The spinner sits outside the button's box, so the revision does not move.
function revBusy(busy) {
  revEl.disabled = busy;
  revEl.toggleAttribute("aria-busy", busy);
  if (busy) revEl.prepend(Object.assign(document.createElement("span"), { className: "spin", ariaHidden: "true" }));
  else revEl.querySelector(".spin")?.remove();
}
async function checkRevision() {
  revBusy(true);
  try {
    const res = await fetch("/version", { cache: "no-store" });
    if (res.ok) {
      const { revision: deployed } = await res.json();
      if (deployed !== revision) {
        revEl.classList.add("stale");
        revEl.title = `${deployed} is deployed; tap to load it`;
      }
    }
  } catch {
    // Offline: the deployed revision is unknown, so the footer claims nothing.
  }
  revBusy(false);
}
async function loadDeployed() {
  revBusy(true);
  try {
    await Promise.all((await caches.keys()).map((k) => caches.delete(k)));
    await (await navigator.serviceWorker?.getRegistration())?.update();
  } catch {
    // The reload still fetches whatever the deleted caches no longer hold.
  }
  location.reload();
}
revEl.addEventListener("click", () => (revEl.classList.contains("stale") ? loadDeployed() : checkRevision()));

const app = document.getElementById("app");
const hstatEl = document.getElementById("hstat");
const laptopEl = document.getElementById("laptop");
const seenEl = document.getElementById("seen");
const updateEl = document.getElementById("update");
const usageEl = document.getElementById("usage");
const usagePopEl = document.getElementById("usagepop");
const config = document.body.dataset.firebase;
// Checking for updates and updating start from the header, so their result
// shows under it; the toast carries results whose control is gone.
const hpopEl = hstatEl.appendChild(Object.assign(document.createElement("p"), { className: "hpop", hidden: true }));
hpopEl.setAttribute("role", "status");
const toastEl = document.body.appendChild(Object.assign(document.createElement("div"), { className: "toast", hidden: true }));
// How long a result that needs nothing more from you stays on screen.
const SETTLED_MS = 3000;
// The commands a worker's header sends.
const WORKER_COMMANDS = ["clear", "start", "restart", "stop"];
// A worker command's success, by the label its failure names.
const DONE = { Clear: "Cleared", Start: "Started", Restart: "Restarted", Stop: "Stopped", "Clear and stop": "Cleared and stopped" };

const h = (tag, attrs = {}, ...children) => {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v);
  }
  for (const c of children.flat()) if (c != null) el.append(c);
  return el;
};

// A sheet's top, its bar and head, follows a downward drag and lets the sheet
// go once the drag took it a quarter of the way down or left fast enough; a
// tap on the bar lets it go too. The body is left out because it scrolls.
function sheetTop(head, close) {
  const top = h("div", { class: "sheettop" }, h("div", { class: "grabber", "aria-hidden": "true" }), head);
  let drag = null;
  top.addEventListener("pointerdown", (e) => {
    if (e.button || e.target.closest("button, a, input, textarea, select, label")) return;
    const panel = top.closest(".sheet");
    drag = { id: e.pointerId, y: e.clientY, at: e.timeStamp, dy: 0, panel, onBar: Boolean(e.target.closest(".grabber")) };
    top.setPointerCapture(e.pointerId);
  });
  top.addEventListener("pointermove", (e) => {
    if (drag?.id !== e.pointerId) return;
    drag.dy = Math.max(0, e.clientY - drag.y);
    drag.panel.style.transform = drag.dy ? `translateY(${drag.dy}px)` : "";
  });
  const letGo = (e) => {
    if (drag?.id !== e.pointerId) return;
    const d = drag;
    drag = null;
    d.panel.style.transform = "";
    const flicked = d.dy > 24 && d.dy / Math.max(1, e.timeStamp - d.at) > 0.5;
    const tapped = d.onBar && d.dy <= 6 && e.type === "pointerup";
    if (d.dy > d.panel.offsetHeight / 4 || flicked || tapped) close();
  };
  top.addEventListener("pointerup", letGo);
  top.addEventListener("pointercancel", letGo);
  return top;
}

const spinner = () => h("span", { class: "spin", "aria-hidden": "true" });

function notice(text, ...extra) {
  app.replaceChildren(h("p", { class: "notice" }, text), ...extra);
}

if (!config) {
  hstatEl.hidden = true;
  notice("This copy of peyk is not connected to Firebase.");
} else {
  start(JSON.parse(config)).catch((err) => notice(`Could not start: ${err.message}`));
}

async function start(cfg) {
  const [{ initializeApp }, auth, fs] = await Promise.all([
    import("./vendor/firebase-app.js"),
    import("./vendor/firebase-auth.js"),
    import("./vendor/firebase-firestore.js"),
  ]);
  const fb = initializeApp(cfg);
  const a = auth.getAuth(fb);
  const db = fs.getFirestore(fb);
  if (cfg.emulators) {
    auth.connectAuthEmulator(a, `http://${cfg.emulators.auth}`, { disableWarnings: true });
    const [host, port] = cfg.emulators.firestore.split(":");
    fs.connectFirestoreEmulator(db, host, Number(port));
  }

  const state = { sessions: new Map(), projects: [], tasks: [], replies: [], laptop: null, usage: null, actions: null, version: null, user: null, picked: null, busy: new Set(),
    // The last word on a command, by its busy key, shown beside the control that sent it: { text, busy, tone }.
    outcomes: new Map(), loaded: new Set(), push: "unknown", editing: null, sends: new Map(), drafts: new Map(), images: new Map(),
    filter: "ready", only: null, arranging: false, showDone: false, menu: null, picking: false, pickFor: null, pickQuery: "",
    // The picker's edit mode, what it has ticked, and the last hide it can undo.
    hiding: false, hideSel: new Set(), showHidden: false, pickStatus: "", pickTone: "", undoHide: null,
    // The task sent from the inbox sheet; the sheet closes once it leaves the queue.
    sheetSent: null,
    // Sent and done rows the viewer has opened, by task id.
    openRows: new Set(),
    // Picks for open questions by question id, kept across redraws.
    answers: new Map(),
    // The worker whose whole conversation is open: { id, latest, data, nodes, unsub }.
    history: null,
    // Replies the laptop has typed into a session whose prompt it has not
    // published yet, by session id: { text, images, at, after }.
    delivered: new Map() };
  let unsubscribe = [];

  const signIn = () =>
    auth.signInWithPopup(a, new auth.GoogleAuthProvider()).catch((err) => notice(`Sign-in failed: ${err.message}`));

  // Listeners exist only while the app is on screen: a phone in a pocket keeps
  // no connection open.
  function listen() {
    stop();
    unsubscribe = [
      fs.onSnapshot(fs.collection(db, "sessions"), (snap) => {
        state.sessions = new Map(snap.docs.map((d) => [d.id, d.data()]));
        for (const [id, d] of state.delivered) {
          const s = state.sessions.get(id);
          if (!s || withDelivered(s, d) === s) state.delivered.delete(id);
        }
        state.loaded.add("sessions");
        render();
      }, failed),
      fs.onSnapshot(fs.collection(db, "projects"), (snap) => {
        state.projects = snap.docs.map((d) => d.data());
        state.loaded.add("projects");
        render();
      }, failed),
      // Metadata changes report when the server confirms a write, which
      // ends a task's saving mark. A task that leaves this query finished or
      // was deleted, so the Done lists read again.
      fs.onSnapshot(fs.query(fs.collection(db, "tasks"), fs.where("status", "in", ["queued", "dispatched"])), { includeMetadataChanges: true }, (snap) => {
        state.tasks = snap.docs.map((d) => ({ id: d.id, ...d.data(), saving: d.metadata.hasPendingWrites }));
        state.loaded.add("tasks");
        if (snap.docChanges().some((c) => c.type === "removed")) refreshDone();
        render();
      }, failed),
      // Replies the laptop has not finished. One that leaves this list was
      // sent, failed or withdrawn, so its images are no longer needed.
      fs.onSnapshot(fs.query(fs.collection(db, "commands"), fs.where("status", "in", ["pending", "held"])), (snap) => {
        const next = waitingReplies(snap.docs.map((d) => ({ id: d.id, ...d.data() })));
        const left = state.replies.filter((r) => !next.some((n) => n.id === r.id));
        state.replies = next;
        if (left.length) dropUnused(left);
        render();
      }, failed),
      fs.onSnapshot(fs.doc(db, "laptop", "status"), (snap) => {
        state.laptop = snap.exists() ? snap.data() : null;
        render();
      }, failed),
      fs.onSnapshot(fs.doc(db, "laptop", "usage"), (snap) => {
        state.usage = snap.exists() ? snap.data() : null;
        render();
      }, failed),
      fs.onSnapshot(fs.doc(db, "laptop", "actions"), (snap) => {
        state.actions = snap.exists() ? snap.data() : null;
        render();
      }, failed),
      fs.onSnapshot(fs.doc(db, "laptop", "version"), (snap) => {
        state.version = snap.exists() ? snap.data() : null;
        render();
      }, failed),
    ];
  }
  function stop() {
    closeHistory();
    unsubscribe.forEach((u) => u());
    unsubscribe = [];
  }
  function failed(err) {
    notice(`Cannot read workers: ${err.code === "permission-denied" ? "this account is not the owner." : err.message}`);
  }

  // A phone freezes a backgrounded app and its Firestore connection dies
  // without an offline event. The SDK notices only on a later retry, up to 90
  // seconds away, and serves cached data until then, so coming back opens a
  // new connection at once. Restarts are chained so two cannot interleave and
  // leave the network off. A restore from the back/forward cache may come
  // without a visibilitychange.
  let restarting = Promise.resolve();
  function reconnect() {
    restarting = restarting
      .then(async () => {
        try {
          await fs.disableNetwork(db);
        } finally {
          await fs.enableNetwork(db);
        }
      })
      .catch(() => {});
    return restarting;
  }
  document.addEventListener("visibilitychange", () => {
    if (!state.user) return;
    if (document.hidden) return stop();
    reconnect();
    listen();
  });
  window.addEventListener("pageshow", (e) => {
    if (e.persisted && state.user) reconnect();
  });
  // Going through the form's own submit button keeps a busy, disabled button
  // from sending twice.
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" || !(e.metaKey || e.ctrlKey) || e.isComposing) return;
    const form = e.target.closest?.("form");
    const submit = form?.querySelector('button[type="submit"]');
    if (!submit) return;
    e.preventDefault();
    if (!submit.disabled) form.requestSubmit(submit);
  });
  document.addEventListener("click", (e) => {
    if (state.menu && !e.target.closest(".wmenu")) { state.menu = null; render(); }
  });
  usageEl.addEventListener("click", () => { state.menu = state.menu === "usage" ? null : "usage"; render(); });
  usageEl.parentElement.addEventListener("keydown", (e) => { if (e.key === "Escape" && state.menu === "usage") { state.menu = null; render(); usageEl.focus(); } });
  window.addEventListener("hashchange", () => { document.documentElement.classList.remove("tuck"); render(); });
  tuckComposerOnScroll();
  setInterval(() => !document.hidden && state.user && renderLaptop(), 60_000);

  auth.onAuthStateChanged(a, async (user) => {
    stop();
    state.user = null;
    if (!user) {
      hstatEl.hidden = true;
      notice("Sign in with the Google account that owns peyk.", h("button", { class: "primary", onclick: signIn }, "Sign in with Google"));
      return;
    }
    const token = await user.getIdTokenResult();
    if (!token.claims.owner) {
      hstatEl.hidden = true;
      notice(
        `${user.email} is signed in but is not the owner.`,
        h("p", { class: "hint" }, "On the laptop, run ", h("code", {}, `make owner EMAIL=${user.email}`), ", then sign in again."),
        h("button", { onclick: () => auth.signOut(a) }, "Sign out"),
      );
      return;
    }
    state.user = user;
    listen();
    receiveShare().catch((err) => alertLine(`Could not attach what was shared: ${err.message}`));
    checkPush().then(render, (err) => toast(`Cannot check notifications: ${err.message}`, "bad"));
  });

  function renderLaptop() {
    const now = Date.now();
    const online = laptopOnline(state.laptop, now);
    hstatEl.hidden = false;
    laptopEl.className = `laptop ${online ? "on" : "off"}`;
    const since = !online && state.laptop && state.laptop.seen_at ? ago(state.laptop.seen_at, now) : "";
    const seen = !since ? "" : since === "now" ? "seen just now" : `seen ${since} ago`;
    seenEl.textContent = seen;
    const commit = state.version?.commit ? ` at ${state.version.commit}` : "";
    laptopEl.setAttribute("aria-label", (online ? `Laptop online${commit}` : `Laptop offline${seen ? `, ${seen}` : ""}`) + ". Check for updates");
    laptopEl.title = `Laptop ${online ? "online" : "offline"}${commit}. Tap to check for updates.`;
    laptopEl.classList.toggle("busy", state.busy.has("check"));
    const behind = online ? state.version?.behind ?? 0 : 0;
    updateEl.hidden = !behind;
    updateEl.disabled = state.busy.has("update");
    updateEl.textContent = state.busy.has("update") ? "Updating…" : "Update";
    updateEl.setAttribute("aria-label", `Update the laptop: ${behind} new commit${behind === 1 ? "" : "s"}`);
    const o = state.outcomes.get("update") ?? state.outcomes.get("check");
    hpopEl.hidden = !o?.text;
    hpopEl.textContent = o?.text ?? "";
    toneOf(hpopEl, o?.tone ?? "");

    renderUsage(now);
  }

  // The header shows Claude's windows and the month's Actions minutes as bars;
  // the popover names them, and each row opens that quota's own page.
  function renderUsage(now) {
    const claude = usageLeft(state.usage, now);
    const gh = actionsLeft(state.actions, now);
    const open = state.menu === "usage" && Boolean(claude || gh);
    usageEl.hidden = !claude && !gh;
    usagePopEl.hidden = !open;
    usageEl.setAttribute("aria-expanded", String(open));
    const meters = usageEl.querySelectorAll(".meter");
    meters.forEach((m, i) => {
      const left = i < 2 ? claude?.[i].left : gh?.pct;
      m.hidden = left == null;
      m.classList.toggle("low", left < 10);
      m.firstElementChild.style.width = `${100 - (left ?? 100)}%`;
    });
    const minutes = (n) => n.toLocaleString("en-US");
    const said = [
      claude && `Claude ${claude.map((w) => `${w.label} ${w.left}% left`).join(", ")}`,
      gh && `Actions ${minutes(gh.left)} of ${minutes(gh.included)} minutes left`,
    ].filter(Boolean);
    usageEl.setAttribute("aria-label", `Usage: ${said.join("; ")}`);
    usageEl.title = said.join("\n");
    if (!open) {
      usagePopEl.replaceChildren();
      return;
    }
    const row = (label, left, text) => h("span", { class: "uwin" }, h("span", {}, label),
      h("span", { class: `meter${left < 10 ? " low" : ""}` }, h("i", { style: `width: ${100 - left}%` })), h("span", {}, text));
    const link = (href, ...children) => h("a", { href, target: "_blank", rel: "noopener", onclick: () => { state.menu = null; render(); } }, ...children);
    const day = (d) => d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
    const parts = [];
    if (claude) {
      parts.push(link("https://claude.ai/settings/usage", h("strong", {}, "Claude"),
        claude.map((w) => row(w.label, w.left, `${w.left}%` + (w.resetsAt ? ` · ${ago(now, w.resetsAt.getTime())}` : "")))));
    }
    if (claude && gh) parts.push(h("hr"));
    if (gh) {
      const most = gh.repos.map((r) => `${r.repo.split("/").pop()} ${minutes(r.minutes)}`).join(" · ");
      parts.push(link("https://github.com/settings/billing", h("strong", {}, "GitHub Actions"),
        row("min", gh.pct, `${minutes(gh.left)} left` + (gh.resetsAt ? ` · ${day(gh.resetsAt)}` : "")),
        most ? h("span", { class: "hint" }, `Most used: ${most}`) : null));
    }
    usagePopEl.replaceChildren(...parts);
    // The bars sit mid-header, so a popover hung from them can pass the
    // screen's left edge; shift it right to the page gutter instead.
    usagePopEl.style.right = "";
    const past = usagePopEl.getBoundingClientRect().left - 16;
    if (past < 0) usagePopEl.style.right = `${past}px`;
  }

  function render() {
    if (!state.user) return;
    renderLaptop();
    // Snapshots re-render the whole view; keep what is being typed.
    const typed = 'textarea, input[type="search"]';
    const fields = () => [...app.querySelectorAll(typed), ...(sheet ? sheet.list.querySelectorAll(typed) : [])];
    const drafts = new Map(fields().map((t) => [t.id, t.value]));
    const focused = document.activeElement && document.activeElement.matches(typed) ? document.activeElement.id : null;

    const id = shownSession();
    const s = id && shownWorker(id);
    followHistory(s);
    const view = s ? "board" : location.hash === "#inbox" ? "inbox" : "board";
    const tabs = h("nav", { class: "tabs" },
      h("a", { href: "#", "aria-current": view === "board" ? "page" : null }, "Workers", h("span", { class: "n" }, String(activeCount()))),
      h("a", { href: "#inbox", "aria-current": view === "inbox" ? "page" : null }, "Inbox", readyCount() ? h("span", { class: "n" }, String(readyCount())) : null),
    );
    if (location.hash.startsWith("#pair-")) app.replaceChildren(pairView(location.hash.slice(6)));
    else if (location.hash === "#log") app.replaceChildren(logView());
    else if (s) app.replaceChildren(detail(s));
    else if (view === "inbox") app.replaceChildren(tabs, inbox());
    else app.replaceChildren(tabs, board());

    renderSheet(s);
    for (const t of fields()) if (drafts.has(t.id)) t.value = drafts.get(t.id);
    document.documentElement.classList.toggle("locked", (view === "inbox" && state.picking) || Boolean(sheet?.open));
    if (focused) document.getElementById(focused)?.focus();
    keepHistoryAnchor();
    if (s && !document.hidden) dismissNotes(s.id);
  }

  // Notifications are tagged with their worker's id; seeing the worker
  // answers them. Every render of a worker gets here, so the log records a
  // dismissal that found nothing once per visit, and every one that found or
  // failed something. The laptop is told once per visit, and has every other
  // device close the worker's notifications, whether or not this one has any.
  const dismissLogged = new Set();
  const seenSent = new Set();
  const newVisit = () => { dismissLogged.clear(); seenSent.clear(); };
  document.addEventListener("visibilitychange", newVisit);
  window.addEventListener("hashchange", newVisit);
  function dismissNotes(id) {
    if (!seenSent.has(id)) {
      seenSent.add(id);
      fs.setDoc(fs.doc(db, "seen", id), { seen_at: fs.serverTimestamp() })
        .catch((err) => noteLog("seen failed", { session: id, error: String(err) }));
    }
    if (!("serviceWorker" in navigator)) return;
    navigator.serviceWorker.ready
      .then((reg) => reg.getNotifications())
      .then((notes) => {
        const mine = notes.filter((n) => n.tag === id);
        mine.forEach((n) => n.close());
        if (mine.length || !dismissLogged.has(id)) noteLog("dismiss", { session: id, closed: mine.length, others: notes.filter((n) => n.tag !== id).map((n) => n.tag) });
        dismissLogged.add(id);
      })
      .catch((err) => noteLog("dismiss failed", { session: id, error: String(err) }));
  }
  // The service worker asks before showing a note whether its worker is on
  // screen; a window's url as the worker sees it lacks the hash.
  navigator.serviceWorker?.addEventListener("message", (e) => {
    if (e.data?.type !== "showing") return;
    e.ports[0]?.postMessage(!document.hidden && shownSession() === e.data.session);
  });

  function shownSession() {
    return location.hash.startsWith("#s-") ? decodeURIComponent(location.hash.slice(3)) : null;
  }

  // Scrolling down reads further into the list, so the composer gets out of
  // the way; scrolling up or reaching the end brings it back. The clamp ignores
  // overscroll bounce, which would otherwise read as scrolling up at either end.
  function tuckComposerOnScroll() {
    let last = window.scrollY;
    window.addEventListener("scroll", () => {
      const root = document.documentElement;
      const y = Math.min(Math.max(0, window.scrollY), root.scrollHeight - window.innerHeight);
      const typing = document.activeElement?.closest?.(".composer");
      const atEnd = window.innerHeight + y >= root.scrollHeight - 24;
      if (typing || atEnd || y < last - 4) root.classList.remove("tuck");
      else if (y > last + 4) root.classList.add("tuck");
      last = y;
    }, { passive: true });
  }

  // A tone is "ok", "warn", or "bad"; none leaves the text muted. A result
  // that needs nothing more from you clears itself; a failure or a warning
  // stays beside its control until dismissed or replaced.
  function setOutcome(key, text, busy = false, tone = "") {
    if (!text && !busy) state.outcomes.delete(key);
    else {
      const o = { text, busy, tone };
      state.outcomes.set(key, o);
      if (!busy && tone !== "bad" && tone !== "warn") {
        setTimeout(() => {
          if (state.outcomes.get(key) !== o) return;
          state.outcomes.delete(key);
          render();
        }, SETTLED_MS);
      }
    }
    render();
  }

  // outcomeLine draws what setOutcome holds for key, or nothing.
  function outcomeLine(key) {
    const o = state.outcomes.get(key);
    if (!o?.text) return null;
    return h("p", { class: "outcome", role: "status", "data-tone": o.tone || null },
      o.busy ? spinner() : null,
      h("span", { class: "grow" }, o.text),
      o.busy || o.tone === "ok" ? null
        : h("button", { type: "button", class: "icon dismiss", "aria-label": "Dismiss", onclick: () => setOutcome(key, "") }, icon("x")));
  }

  // A result with no control left on screen to show it beside.
  function toast(text, tone = "") {
    clearTimeout(toastEl.timer);
    toastEl.replaceChildren(h("span", { class: "grow" }, text),
      h("button", { type: "button", class: "icon dismiss", "aria-label": "Dismiss", onclick: () => { toastEl.hidden = true; } }, icon("x")));
    toneOf(toastEl, tone);
    toastEl.setAttribute("role", tone === "bad" ? "alert" : "status");
    toastEl.hidden = false;
    if (tone !== "bad") toastEl.timer = setTimeout(() => { toastEl.hidden = true; }, SETTLED_MS);
  }

  function toneOf(el, tone) {
    if (tone) el.dataset.tone = tone;
    else delete el.dataset.tone;
  }

  // track marks key busy while work runs; buttons drawn with busyButton show
  // it, since a snapshot may redraw them meanwhile.
  async function track(key, work) {
    state.busy.add(key);
    render();
    try {
      return await work();
    } finally {
      state.busy.delete(key);
      render();
    }
  }

  function busyButton(key, attrs, ...children) {
    if (!state.busy.has(key)) return h("button", attrs, ...children);
    return h("button", { ...attrs, class: `${attrs.class ?? ""} busy`, disabled: "", "aria-busy": "true" }, spinner(), ...children);
  }

  // Tapping the laptop asks it to fetch its checkout; the version it then
  // publishes says whether an update is waiting.
  laptopEl.addEventListener("click", () => sendCommand("check", {}, "Check for updates", "check", (text, busy, tone) => {
    if (tone === "ok") {
      const n = state.version?.behind ?? 0;
      text = n ? `${n} new commit${n === 1 ? "" : "s"} to update to` : "Up to date";
    }
    setOutcome("check", text, busy, tone);
  }, null, { done: "Checked" }));
  updateEl.addEventListener("click", () => sendCommand("update", {}, "Update", "update", undefined, null, { done: "Updated" }));

  // sendCommand signs the payload with this device's session key (push needs
  // none), stores it, and reports each step and what the laptop made of it
  // through report, with the tone setOutcome takes; by default beside the
  // control whose busy key is key. label names the action in a failure, and
  // done is the word for its success, or null to say nothing. key stays busy
  // until the laptop answers, so the same action cannot be sent twice
  // meanwhile. Resolves once the command is stored, to whether it was. With
  // queue, the command is stored while the laptop is offline too, for it to
  // pick up when it is back; the reply's own row then shows where it stands.
  // cancel names a waiting reply this one replaces, withdrawn in the same write.
  async function sendCommand(type, fields, label, key, report = (text, busy, tone) => setOutcome(key, text, busy, tone), onDone = null, { queue = false, cancel = null, done = "Done" } = {}) {
    if (state.busy.has(key)) return false;
    if (!queue && !laptopOnline(state.laptop, Date.now())) {
      report("Not sent: the laptop is offline.", false, "bad");
      return false;
    }
    state.busy.add(key);
    const finish = (text, tone) => {
      state.busy.delete(key);
      report(text, false, tone);
      render();
    };
    const ref = fs.doc(fs.collection(db, "commands"));
    const payload = JSON.stringify({ id: ref.id, type, ...fields, expires: Date.now() + 60_000 });
    const data = { type, payload, status: "pending", created_at: fs.serverTimestamp() };
    report("", true);
    try {
      if (type !== "push" && type !== "check") data.grant = await passkey.signCommand(payload, () => report("Approve with your passkey…", true));
      report("", true);
      if (cancel) {
        const batch = fs.writeBatch(db);
        batch.set(ref, data);
        batch.update(fs.doc(db, "commands", cancel), { status: "cancelled" });
        await batch.commit();
      } else {
        await fs.setDoc(ref, data);
      }
    } catch (err) {
      finish(err.name === "NotAllowedError"
        ? "Not sent: the passkey prompt was cancelled, or this device has no paired passkey (run make pair on the laptop)."
        : `Not sent: ${err.message}`, "bad");
      return false;
    }
    if (queue && !laptopOnline(state.laptop, Date.now())) {
      finish("", "");
      return true;
    }
    report(queue ? "" : "Waiting for the laptop…", true);
    // The payload expires after a minute, so a laptop that has not answered
    // by then will refuse it, unless it is a queued reply.
    const timer = setTimeout(() => {
      unsub();
      if (queue) finish("", "");
      else finish("The laptop did not answer.", "bad");
    }, 75_000);
    const unsub = fs.onSnapshot(ref, (snap) => {
      const c = snap.data();
      if (!c || c.status === "pending") return;
      clearTimeout(timer);
      unsub();
      if (c.status === "failed" && String(c.error).startsWith("session approval:")) passkey.forgetGrant();
      const [text, tone] = c.status === "done" ? [done ?? "", "ok"]
        : c.status === "failed" ? [`${label} failed: ${c.error}`, "bad"]
        : ["", ""];
      finish(text, tone);
      if (c.status === "done") onDone?.();
    }, (err) => {
      clearTimeout(timer);
      finish(`Cannot follow the result: ${err.message}`, "bad");
    });
    return true;
  }

  function pairView(code) {
    const out = h("p", { class: "hint" });
    const button = h("button", { class: "primary", type: "button" }, "Create passkey");
    button.addEventListener("click", async () => {
      button.disabled = true;
      button.classList.add("busy");
      button.replaceChildren(spinner(), "Creating passkey…");
      try {
        const key = await passkey.createPasskey(state.user);
        await fs.setDoc(fs.doc(db, "pairings", code), { ...key, created_at: fs.serverTimestamp() });
        out.replaceChildren("The laptop should now show this fingerprint. Confirm there only if it matches:",
          h("strong", { class: "fp" }, await passkey.fingerprint(key.public_key)));
      } catch (err) {
        button.disabled = false;
        out.textContent = `Pairing failed: ${err.message}`;
      } finally {
        button.classList.remove("busy");
        button.replaceChildren("Create passkey");
      }
    });
    return h("article", { class: "detail" },
      h("a", { class: "back", href: "#", "aria-label": "Back to workers" }, icon("back")),
      h("h2", {}, "Pair this device"),
      h("p", {}, "Commands to the laptop are signed with a passkey on this device. Create one, then confirm its fingerprint on the laptop."),
      button, out);
  }

  function activeCount() {
    return [...state.sessions.values()].filter((s) => s.aoe_status !== "stopped").length;
  }

  function readyCount() {
    return queueCounts(queue(state.tasks, state.projects)).ready;
  }

  function projectNamed(name) {
    return state.projects.find((p) => p.name === name);
  }

  // A worker's project is the one its session publishes as.
  function projectOf(s) {
    return state.projects.find((p) => p.session_id === s.id) || projectNamed(s.title);
  }

  function face(p, name, size) {
    if (p && p.logo) return h("img", { class: `face ${size}`, src: p.logo, alt: "" });
    const m = monogram(name);
    return h("span", { class: `face mono t${m.tint} ${size}`, "aria-hidden": "true" }, m.letter);
  }

  function projectStatus(p) {
    if (!p.session_id) return { text: "no worker · a task starts one", tone: "none" };
    return p.free ? { text: "free", tone: "ok" } : { text: p.busy, tone: "busy" };
  }

  function inbox() {
    const projects = sortProjects(state.projects.filter((p) => !p.hidden));
    if (!state.picked || !projects.some((p) => p.name === state.picked)) state.picked = projects[0]?.name ?? null;
    const all = queue(state.tasks, state.projects);
    if (state.only && !all.some((t) => t.project === state.only)) state.only = null;
    const counts = queueCounts(state.only ? all.filter((t) => t.project === state.only) : all);
    const list = filterQueue(all, state.filter, state.only);
    const now = Date.now();

    const chips = [["all", "All"], ["ready", "Ready"], ["waiting", "Waiting"]].map(([key, label]) =>
      h("button", { type: "button", class: "fchip", "aria-pressed": String(state.filter === key), onclick: () => { state.filter = key; render(); } },
        label, h("span", { class: "n" }, String(counts[key]))));
    const filters = h("div", { class: "filters" }, chips, h("span", { class: "grow" }),
      list.length > 1 || state.arranging
        ? h("button", { type: "button", class: "link", onclick: () => { state.arranging = !state.arranging; state.menu = null; render(); } },
          icon("sort"), state.arranging ? "Done" : "Reorder")
        : null);
    const names = [...new Set(all.map((t) => t.project))];
    const strip = names.length > 1 || state.only
      ? h("div", { class: "strip", role: "group", "aria-label": "Filter by project" }, names.map((n) =>
        h("button", { type: "button", class: "sface", title: n, "aria-label": n, "aria-pressed": String(state.only === n),
          onclick: () => { state.only = state.only === n ? null : n; render(); } }, face(projectNamed(n), n, "md"))))
      : null;

    const items = !state.loaded.has("tasks") ? loading("Loading tasks…") : list.length
      ? list.map((t, i) => (state.editing === t.id ? editor(t) : state.arranging ? arrangeRow(list, t, i) : taskCard(t, now)))
      : h("p", { class: "empty" }, all.length ? "Nothing here for this filter." : "No tasks queued.");
    const sent = sentTasks(state.tasks).filter((t) => !state.only || t.project === state.only);
    return h("div", { class: "inbox" },
      filters, strip,
      h("p", { class: "alert", id: "alert", role: "status" }),
      h("div", { class: "tasks" }, items),
      sent.length && state.filter !== "waiting" && !state.arranging ? [
        h("h2", { class: "group" }, "Sent", h("span", { class: "n" }, ` ${sent.length}`)),
        h("div", { class: "rows" }, sent.map((t) => sentRow(t, now))),
      ] : null,
      state.filter !== "waiting" && !state.arranging ? [
        doneFold(state.showDone, () => { state.showDone = !state.showDone; render(); }, state.only ?? ""),
        ...(state.showDone ? doneList(state.only ?? "", now) : []),
      ] : null,
      composer(),
      state.picking ? picker(projects) : null,
    );
  }

  // A sent or done task has had its turn, so it is one faded line that opens
  // to its whole text and the rest of its controls.
  function taskRow(t, kind, now, { inProject = false, stat, action = null, detail }) {
    const open = state.openRows.has(t.id);
    return h("div", { class: `task row ${kind}${open ? " open" : ""}` },
      h("div", { class: "rhead" },
        h("button", { type: "button", class: "rline", "aria-expanded": String(open),
          onclick: () => { open ? state.openRows.delete(t.id) : state.openRows.add(t.id); render(); } },
          inProject ? null : face(projectNamed(t.project), t.project, "xs"),
          h("span", { class: "rtext" }, inProject ? null : [h("b", {}, t.project), " "], t.text),
          h("span", { class: "rstat" }, stat)),
        action),
      h("div", { class: "rmore", hidden: open ? null : "" },
        h("div", { class: "rfull" }, t.text),
        taskImages(t),
        h("div", { class: "rmeta" }, detail, h("span", { class: "grow" }),
          h("button", { type: "button", class: "icon", "aria-label": `Delete task: ${t.text}`, onclick: () => remove(t) }, icon("x")))),
    );
  }

  // A sent task stays in the inbox until it is done. A new worker is only
  // published once the laptop sees it start, so until then the task has no
  // worker to link to.
  function sentRow(t, now) {
    const s = state.sessions.get(t.session_id);
    const st = s && (STATES[s.state] || { label: s.state, tone: "idle" });
    return taskRow(t, "sent", now, {
      stat: [s ? h("span", { class: `dot ${st.tone}` }) : spinner(), `${s ? st.label : "starting"} · ${ago(t.dispatched_at, now)}`],
      detail: s ? h("a", { href: `#s-${encodeURIComponent(s.id)}` }, `${s.title} · ${st.label}`)
        : h("span", {}, "Starting the worker…"),
    });
  }

  // A send's outcome stays on its task, which may have moved since, rather
  // than in a status line scrolled out of view.
  function send(t, clear) {
    const report = (text, busy = false, tone = "") => {
      state.sends.set(t.id, { text, busy, tone });
      render();
    };
    state.menu = null;
    sendCommand("dispatch", { project: t.project, task: t.id, text: t.text, clear, ...(t.images?.length ? { images: t.images } : {}) },
      clear ? `Clear ${t.project} and send` : `Send to ${t.project}`, `send:${t.id}`, report, null, { done: "Sent" });
  }

  // A worker with a conversation is cleared first, so a task starts on its own
  // context; sending into the conversation is the exception, in the menu. A
  // new or just-cleared worker has nothing to clear.
  function hasConversation(t) {
    const p = projectNamed(t.project);
    const worker = p && p.session_id && state.sessions.get(p.session_id);
    return Boolean(worker && !worker.cleared);
  }

  // In a project's own sheet the project goes without saying, and a send
  // goes through the sheet so that it can close once the task leaves.
  function taskCard(t, now, { inProject = false, sendWith = send } = {}) {
    const clears = t.ready && hasConversation(t);
    const open = state.menu === t.id;
    const sent = state.sends.get(t.id);
    const status = sent
      ? h("span", { class: "tstatus", role: "status", "data-tone": sent.tone || null }, sent.text)
      : h("span", { class: "tstatus" }, t.ready
        ? (projectNamed(t.project)?.session_id ? (clears ? "Free · clears the conversation first" : "Free") : "Starts a new worker")
        : `Waiting · ${t.why}`);
    return h("article", { class: `task${t.ready ? "" : " waiting"}` },
      h("div", { class: "thead" },
        inProject ? null : [face(projectNamed(t.project), t.project, "sm"), h("span", { class: "proj" }, t.project)],
        t.saving ? h("span", { class: "when saving" }, spinner(), "saving") : h("span", { class: "when" }, ago(t.created_at, now)),
        h("span", { class: "grow" }),
        h("div", { class: "wmenu" },
          h("button", { type: "button", class: "icon", "aria-label": `More actions: ${t.text}`, "aria-haspopup": "menu", "aria-expanded": String(open),
            onclick: () => { state.menu = open ? null : t.id; render(); } }, icon("more")),
          open ? taskMenu(t, clears, sendWith) : null)),
      h("div", { class: "ttext" }, t.text),
      taskImages(t),
      h("div", { class: "tfoot" },
        sent?.busy ? spinner() : h("span", { class: `dot ${sent ? "sent" : t.ready ? "free" : "busy"}` }),
        status,
        t.ready ? busyButton(`send:${t.id}`, { type: "button", class: "primary send", onclick: () => sendWith(t, clears) }, clears ? icon("broom") : null, "Send", icon("prompt")) : null,
      ),
    );
  }

  function taskMenu(t, clears, sendWith) {
    return h("div", { class: "pop", role: "menu", onkeydown: (e) => { if (e.key === "Escape") { state.menu = null; render(); } } },
      clears && !state.busy.has(`send:${t.id}`)
        ? h("button", { type: "button", role: "menuitem", onclick: () => { state.menu = null; sendWith(t, false); } }, icon("prompt"), "Send without clearing")
        : null,
      h("button", { type: "button", role: "menuitem", "aria-label": `Edit task: ${t.text}`, onclick: () => startEdit(t) }, icon("edit"), "Edit"),
      h("hr"),
      h("button", { type: "button", role: "menuitem", class: "danger", "aria-label": `Delete task: ${t.text}`, onclick: () => { state.menu = null; remove(t); } }, icon("trash"), "Delete"),
    );
  }

  function arrangeRow(list, t, i, { inProject = false } = {}) {
    return h("div", { class: "task arrange" },
      icon("grip"),
      inProject ? null : face(projectNamed(t.project), t.project, "sm"),
      h("span", { class: "ttext one" }, t.text),
      h("button", { type: "button", class: "icon step", "aria-label": `Move up: ${t.text}`, disabled: i === 0 ? "" : null, onclick: () => move(t, i - 1, list) }, icon("up")),
      h("button", { type: "button", class: "icon step", "aria-label": `Move down: ${t.text}`, disabled: i === list.length - 1 ? "" : null, onclick: () => move(t, i + 1, list) }, icon("down")),
    );
  }

  function move(t, to, list) {
    const i = list.findIndex((x) => x.id === t.id);
    state.menu = null;
    fs.updateDoc(fs.doc(db, "tasks", t.id), { rank: rankAt(list, i, to) })
      .catch((err) => alertLine(`Could not move the task: ${err.message}`));
  }

  // New tasks, edits and replies share one composer, so each takes text,
  // pasted screenshots and attached images the same way. key names both its
  // busy state and its draft images.
  // hold, when given, answers a long press on the submit button while
  // hold.armed() is true; otherwise the press submits as a tap would.
  function composerForm({ key, id, label, placeholder, rows = "2", cls, submitLabel, submitIcon, send, hold = null, lead = null, tail = null }) {
    const imgs = draft(key);
    const text = h("textarea", { id, rows, placeholder, "aria-label": label, onpaste: (e) => pasteImages(key, e) });
    const files = h("input", { type: "file", accept: "image/*", multiple: "", hidden: "", "aria-label": "Attach images", onchange: (e) => { attachImages(key, [...e.target.files]); e.target.value = ""; } });
    return h("form", { class: `composer ${cls}`, onsubmit: (e) => { e.preventDefault(); send(text); } },
      imgs.length ? h("div", { class: "thumbs" }, imgs.map((img, i) => draftThumb(key, img, i))) : null,
      text,
      h("div", { class: "crow" },
        lead,
        h("button", { type: "button", class: "icon round", title: "Attach an image or screenshot", "aria-label": "Attach image", disabled: imgs.length >= MAX_IMAGES ? "" : null, onclick: () => files.click() }, icon("clip")),
        h("span", { class: "grow" }),
        tail,
        busyButton(key, { type: "submit", class: "round go", "aria-label": submitLabel, ...(hold ? longPress(() => hold.armed(text), () => hold.fire(text)) : {}) },
          icon(submitIcon), hold ? heldIcon() : null),
      ),
      files,
    );
  }

  function heldIcon() {
    const el = icon("prompt");
    el.classList.add("held");
    return el;
  }

  // The click that ends a long press is swallowed, so the press does not
  // also submit. The button shows what the press will do only after a delay,
  // so a quick tap does not flash it.
  function longPress(armed, fire, { ms = 500, show = 150 } = {}) {
    let timers = [];
    let held = false;
    let button = null;
    const cancel = () => {
      timers.forEach(clearTimeout);
      timers = [];
      button?.classList.remove("holding");
    };
    return {
      onpointerdown: (e) => {
        if (e.button !== 0) return;
        held = false;
        cancel();
        if (!armed()) return;
        button = e.currentTarget;
        timers = [
          setTimeout(() => button.classList.add("holding"), show),
          setTimeout(() => {
            timers = [];
            if (!armed()) return cancel();
            held = true;
            navigator.vibrate?.(10);
            fire();
          }, ms),
        ];
      },
      onpointerup: cancel, onpointerleave: cancel, onpointercancel: cancel,
      oncontextmenu: (e) => e.preventDefault(),
      onclick: (e) => {
        if (!held) return;
        held = false;
        e.preventDefault();
      },
    };
  }

  function projectPill(name, task = null) {
    return h("button", { type: "button", class: "pill", "aria-label": `Project: ${name ?? "none"}`, "aria-haspopup": "dialog", disabled: name ? null : "",
      onclick: () => {
        Object.assign(state, { picking: true, pickFor: task, pickQuery: "", hiding: false, pickStatus: "", undoHide: null });
        state.hideSel.clear();
        render();
        document.getElementById("pick-search")?.focus();
      } },
      name ? face(projectNamed(name), name, "xs") : null, h("span", {}, name ?? "No project"), icon("chevron"));
  }

  function composer() {
    // The draft is cleared as soon as the task is accepted: a local write
    // re-renders the inbox before the server confirms it, and the re-render
    // would otherwise restore the text it found in the box.
    const add = async (text, andSend = false) => {
      const value = text.value.trim();
      if (!value || !state.picked) return;
      const attaching = draft("add");
      text.value = "";
      state.drafts.delete("add");
      try {
        const t = await track("add", () => addTask(state.picked, value, attaching));
        forgetImages(attaching);
        if (andSend) send(t, hasConversation(t));
      } catch (err) {
        const box = document.getElementById("task-text");
        if (box && !box.value) box.value = value;
        if (!draft("add").length) state.drafts.set("add", attaching);
        render();
        alertLine(`Could not add the task: ${err.message}`);
      }
    };
    // A long press sends the task straight away, as its Send button would,
    // when the project's worker is free to take it.
    const hold = {
      armed: (text) => Boolean(text.value.trim() && projectNamed(state.picked)?.free && !state.busy.has("add")),
      fire: (text) => add(text, true),
    };
    return composerForm({ key: "add", id: "task-text", label: "New task", placeholder: state.picked ? "Describe a task…" : "No projects published yet",
      cls: "add", submitLabel: "Add task", submitIcon: "addtask", send: add, hold, lead: projectPill(state.picked) });
  }

  // The picker lists the projects that are not hidden. Its edit mode hides
  // the ticked ones; hidden projects wait, folded, at the bottom, and search
  // still finds them so none is lost.
  function picker(projects) {
    const t = state.pickFor;
    const current = t ? state.editProject ?? t.project : state.picked;
    const hidden = state.projects.filter((p) => p.hidden).sort((a, b) => a.name.localeCompare(b.name));
    const close = () => {
      Object.assign(state, { picking: false, pickFor: null, hiding: false, undoHide: null });
      render();
      document.getElementById(t ? `edit-${t.id}` : "task-text")?.focus();
    };
    const pick = (name) => {
      if (t) state.editProject = name;
      else state.picked = name;
      close();
    };
    const report = (text, busy, tone = "") => {
      Object.assign(state, { pickStatus: text, pickTone: tone });
      const el = document.getElementById("pick-status");
      if (!el) return;
      el.textContent = text;
      toneOf(el, tone);
    };
    const hide = (names) => {
      const label = names.length === 1 ? `Hide ${names[0]}` : `Hide ${names.length} projects`;
      sendCommand("hide", { projects: names }, label, "hide", report, () => {
        state.undoHide = names;
        render();
      }, { done: "Hidden" }).then((sent) => {
        if (!sent) return;
        state.hiding = false;
        state.hideSel.clear();
        render();
      });
    };
    const show = (names, label) => {
      state.undoHide = null;
      sendCommand("show", { projects: names }, label, `show:${names.join(",")}`, report, null, { done: "Shown" });
    };
    const queued = new Map();
    for (const t of state.tasks) if (t.status === "queued") queued.set(t.project, (queued.get(t.project) || 0) + 1);
    const row = (p) => {
      const st = projectStatus(p);
      const main = [face(p, p.name, "lg"),
        h("span", { class: "pmain" },
          h("span", { class: "pname" }, p.name),
          h("span", { class: `pstat ${st.tone}` }, h("span", { class: `dot ${st.tone}` }), st.text)),
        queued.get(p.name) ? h("span", { class: "badge" }, `${queued.get(p.name)} queued`) : null];
      if (state.hiding) {
        const on = state.hideSel.has(p.name);
        return h("button", { type: "button", role: "checkbox", class: "prow", "aria-checked": String(on),
          onclick: () => { on ? state.hideSel.delete(p.name) : state.hideSel.add(p.name); render(); } },
          h("span", { class: "tick" }, icon("check")), main);
      }
      const on = p.name === current;
      return h("button", { type: "button", role: "radio", class: "prow", "aria-checked": String(on), onclick: () => pick(p.name) },
        main, on ? icon("check") : null);
    };
    const hiddenRow = (p) =>
      h("div", { class: "prow hidden" },
        face(p, p.name, "lg"),
        h("span", { class: "pmain" }, h("span", { class: "pname" }, p.name), h("span", { class: "pstat" }, "hidden")),
        h("button", { type: "button", class: "chipbtn", "aria-label": `Show ${p.name}`, disabled: state.busy.has(`show:${p.name}`) ? "" : null,
          onclick: () => show([p.name], `Show ${p.name}`) }, "Show"));
    const results = () => {
      const q = state.pickQuery || "";
      const matches = searchProjects(projects, q);
      const recent = q || state.hiding ? [] : projects.filter((p) => p.active).slice(0, 4);
      const hiddenMatches = q ? searchProjects(hidden, q) : [];
      return [
        recent.length ? [
          h("h3", { class: "group" }, "Recent"),
          h("div", { class: "recent" }, recent.map((p) =>
            h("button", { type: "button", class: "tile", "aria-label": `${p.name} (recent)`, "aria-pressed": String(p.name === current), onclick: () => pick(p.name) },
              face(p, p.name, "xl"), h("span", {}, p.name), h("span", { class: `dot ${projectStatus(p).tone}` })))),
        ] : null,
        h("h3", { class: "group" }, q ? `Matches · ${matches.length + hiddenMatches.length}` : "All projects"),
        matches.length
          ? h("div", { class: "plist", role: state.hiding ? "group" : "radiogroup", "aria-label": "Project" }, matches.map(row))
          : hiddenMatches.length ? null : h("p", { class: "empty" }, "No project matches."),
        hiddenMatches.length ? h("div", { class: "plist" }, hiddenMatches.map(hiddenRow)) : null,
        !q && hidden.length ? [
          h("h3", { class: "group" },
            h("button", { type: "button", class: "fold", "aria-expanded": String(state.showHidden), onclick: () => { state.showHidden = !state.showHidden; render(); } },
              "Hidden", h("span", { class: "n" }, String(hidden.length)), icon("chevron"))),
          state.showHidden ? h("div", { class: "plist" }, hidden.map(hiddenRow)) : null,
        ] : null,
      ].flat().filter(Boolean);
    };
    const n = state.hideSel.size;
    const head = state.hiding
      ? h("div", { class: "sheethead" }, h("h2", {}, "Hide projects"),
        h("button", { type: "button", class: "link", onclick: () => { state.hiding = false; state.hideSel.clear(); render(); } }, "Done"))
      : h("div", { class: "sheethead" }, h("h2", {}, "Send to"),
        h("span", { class: "heads" },
          h("button", { type: "button", class: "link", onclick: () => { Object.assign(state, { hiding: true, undoHide: null, pickStatus: "" }); render(); } }, "Edit"),
          h("button", { type: "button", class: "link", onclick: close }, "Cancel")));
    const foot = state.hiding
      ? h("div", { class: "sheetfoot pickfoot" },
        h("span", { class: "grow hint", id: "pick-status", role: "status", "data-tone": state.pickStatus && state.pickTone || null }, state.pickStatus || (n ? `${n} selected` : "Tick the projects to hide")),
        h("button", { type: "button", class: "danger", disabled: n && !state.busy.has("hide") ? null : "", onclick: () => hide([...state.hideSel]) },
          n ? `Hide ${n}` : "Hide"))
      : state.undoHide || state.pickStatus
        ? h("div", { class: "sheetfoot pickfoot" },
          h("span", { class: "grow hint", id: "pick-status", role: "status", "data-tone": !state.undoHide && state.pickTone || null }, state.undoHide
            ? `Hid ${state.undoHide.length === 1 ? state.undoHide[0] : `${state.undoHide.length} projects`}.`
            : state.pickStatus),
          state.undoHide ? h("button", { type: "button", class: "link", onclick: () => show(state.undoHide, "Undo") }, "Undo") : null)
        : null;
    // Typing redraws only the results: replacing the input under the caret
    // drops keystrokes and breaks the phone keyboard's composition.
    const body = h("div", { class: "sheetbody" }, results());
    return h("div", { class: "scrim", onclick: (e) => e.target === e.currentTarget && close() },
      h("section", { class: "sheet", role: "dialog", "aria-modal": "true", "aria-label": "Choose a project", onkeydown: (e) => e.key === "Escape" && close() },
        sheetTop(head, close),
        h("label", { class: "search" }, icon("search"),
          h("input", { type: "search", id: "pick-search", placeholder: `Search ${projects.length} projects`, "aria-label": "Search projects", value: state.pickQuery || "",
            oninput: (e) => { state.pickQuery = e.target.value; body.replaceChildren(...results()); } })),
        body,
        foot,
      ),
    );
  }

  // A task's images go with it, unless another task shares one. Done tasks
  // are not all on the phone, so the tasks sharing an image are asked for.
  async function remove(t) {
    try {
      const imgs = t.images ?? [];
      const sharing = imgs.length
        ? (await fs.getDocs(fs.query(fs.collection(db, "tasks"), fs.where("images", "array-contains-any", imgs)))).docs.map((d) => ({ id: d.id, ...d.data() }))
        : [];
      const batch = fs.writeBatch(db);
      batch.delete(fs.doc(db, "tasks", t.id));
      for (const id of orphanedImages([...state.tasks, ...sharing], [t])) batch.delete(fs.doc(db, "attachments", id));
      await batch.commit();
      for (const d of done.values()) d.tasks = d.tasks.filter((x) => x.id !== t.id);
      render();
    } catch (err) {
      alertLine(`Could not delete: ${err.message}`);
    }
  }

  // Done tasks only grow, so they are never listened to. A Done list reads a
  // page when it opens and again when a task leaves the active ones. Lists
  // are keyed by project, "" for all projects.
  const DONE_PAGE = 10;
  const done = new Map();

  async function loadDone(key) {
    let d = done.get(key);
    if (!d) done.set(key, (d = { tasks: [], cursor: null, more: true, loading: false, error: "" }));
    if (d.loading || !d.more) return;
    d.loading = true;
    render();
    const q = fs.query(fs.collection(db, "tasks"), fs.where("status", "==", "done"),
      ...(key ? [fs.where("project", "==", key)] : []), fs.orderBy("done_at", "desc"),
      ...(d.cursor ? [fs.startAfter(d.cursor)] : []), fs.limit(DONE_PAGE));
    try {
      const snap = await fs.getDocs(q);
      if (done.get(key) !== d) return;
      d.tasks.push(...snap.docs.map((x) => ({ id: x.id, ...x.data() })));
      d.cursor = snap.docs.at(-1) ?? d.cursor;
      d.more = snap.docs.length === DONE_PAGE;
    } catch (err) {
      d.error = doneError(err);
      d.more = false;
    } finally {
      d.loading = false;
      render();
    }
  }

  function refreshDone() {
    done.clear();
    render();
  }

  function doneFold(open, toggle, key) {
    return h("h2", { class: "group" },
      h("button", { type: "button", class: "fold", "aria-expanded": String(open), onclick: () => {
        if (done.get(key)?.error) done.delete(key);
        toggle();
      } }, "Done", icon("chevron")));
  }

  // An open list with nothing read yet starts reading as it is drawn.
  function doneList(key, now, inProject = false) {
    const d = done.get(key);
    if (!d) {
      queueMicrotask(() => loadDone(key));
      return [loading("Loading done tasks…")];
    }
    return [
      d.tasks.length ? h("div", { class: "rows" }, d.tasks.map((t) => doneRow(t, now, inProject))) : null,
      d.error ? h("p", { class: "empty" }, d.error) : null,
      d.error ? h("button", { type: "button", class: "link more", onclick: () => { done.delete(key); render(); } }, "Try again") : null,
      !d.tasks.length && !d.loading && !d.error ? h("p", { class: "empty" }, "Nothing done yet.") : null,
      d.loading ? (d.tasks.length ? h("p", { class: "more pending", role: "status", "aria-label": "Loading more done tasks" }, spinner()) : loading("Loading done tasks…"))
        : d.more ? h("button", { type: "button", class: "link more", onclick: () => loadDone(key) }, "Show more") : null,
    ].filter(Boolean);
  }

  function doneRow(t, now, inProject) {
    const p = projectNamed(t.project);
    const worker = p?.session_id ? state.sessions.get(p.session_id) : null;
    const r = resumeAction(t, p, worker);
    const label = r?.clear ? "Clear and resume" : "Resume conversation";
    return taskRow(t, "done", now, {
      inProject,
      stat: r?.current ? `current · ${ago(t.done_at, now)}` : ago(t.done_at, now),
      action: r && !r.current ? busyButton(`resume:${t.id}`, {
        type: "button", class: "icon", title: r.busy ? `${t.project} is ${r.busy}` : label, "aria-label": `${label}: ${t.text}`, disabled: r.busy ? "" : null,
        onclick: () => sendCommand("resume", { session: p.session_id, conversation: t.conversation_id }, "Resume", `resume:${t.id}`, resumed, null, { done: null }),
      }, icon(r.clear ? "clearresume" : "resume")) : null,
      detail: h("span", {}, `done ${ago(t.done_at, now)} ago${t.commit ? ` · ${t.commit}` : ""}${r?.current ? " · current conversation" : ""}`),
    });
  }

  // The inbox opens on ready tasks; one queued for a busy project would
  // vanish from that view as it is saved, so the view widens to show it.
  function reveal(project) {
    if (state.filter === "ready" && !projectNamed(project)?.free) state.filter = "all";
  }

  async function addTask(project, text, images) {
    reveal(project);
    const batch = fs.writeBatch(db);
    const ids = await storeImages(batch, images);
    // The rank is set here because created_at reads as null until the server
    // has confirmed it, which would sort the new task to the top meanwhile.
    const task = { project, text, created_at: fs.serverTimestamp(), status: "queued", rank: Date.now() };
    if (ids.length) task.images = ids;
    const ref = fs.doc(fs.collection(db, "tasks"));
    batch.set(ref, task);
    await batch.commit();
    return { ...task, id: ref.id };
  }

  // Images an edit drops go with it, unless another task shares one.
  async function saveTask(t, text, project, images) {
    const batch = fs.writeBatch(db);
    const ids = await storeImages(batch, images);
    batch.update(fs.doc(db, "tasks", t.id), { text, project, images: ids.length ? ids : fs.deleteField() });
    const dropped = (t.images || []).filter((id) => !ids.includes(id));
    for (const id of orphanedImages(state.tasks, [{ id: t.id, images: dropped }])) batch.delete(fs.doc(db, "attachments", id));
    await batch.commit();
  }

  // An image is named by its hash, so one already stored is the same image
  // and is not written again; writing it again would be refused.
  async function storeImages(batch, images) {
    const ids = [];
    for (const img of images) {
      const id = img.id ?? (await imageID(img.bytes));
      if (ids.includes(id)) continue;
      ids.push(id);
      if (img.bytes && !(await fs.getDoc(fs.doc(db, "attachments", id))).exists()) {
        batch.set(fs.doc(db, "attachments", id), { data: fs.Bytes.fromUint8Array(img.bytes), type: img.type, created_at: fs.serverTimestamp() });
      }
    }
    return ids;
  }

  // A composer's images: each is either still only in this page
  // ({bytes, type, url}) or already stored ({id}).
  function draft(key) {
    if (!state.drafts.has(key)) state.drafts.set(key, []);
    return state.drafts.get(key);
  }

  function dropDraft(key) {
    forgetImages(state.drafts.get(key) || []);
    state.drafts.delete(key);
  }

  function forgetImages(images) {
    for (const img of images) if (img.bytes) URL.revokeObjectURL(img.url);
  }

  async function attachImages(key, files) {
    const imgs = draft(key);
    const room = MAX_IMAGES - imgs.length;
    if (files.length > room) alertLine(`At most ${MAX_IMAGES} images go with one message; the rest were left out.`, "warn");
    for (const file of files.slice(0, Math.max(0, room))) {
      try {
        const img = await prepareImage(file);
        imgs.push({ ...img, url: URL.createObjectURL(new Blob([img.bytes], { type: img.type })) });
      } catch (err) {
        alertLine(`Could not attach: ${err.message}`);
      }
    }
    render();
  }

  // A share from another app becomes the new task's draft. It waits for
  // sign-in, since only a signed-in page can store the task.
  async function receiveShare() {
    const shared = await takeShare();
    if (!shared) return;
    if (location.hash !== "#inbox") history.replaceState(null, "", "#inbox");
    render();
    const box = document.getElementById("task-text");
    const text = sharedText(shared);
    if (box && text) box.value = box.value ? `${box.value}\n${text}` : text;
    const all = shared.files || [];
    const files = all.filter((f) => f.type.startsWith("image/"));
    if (files.length) await attachImages("add", files);
    if (files.length < all.length) alertLine("Only images can be attached; the other files were left out.", "warn");
  }

  function pasteImages(key, e) {
    const files = [...(e.clipboardData?.files || [])].filter((f) => f.type.startsWith("image/"));
    if (!files.length) return;
    e.preventDefault();
    attachImages(key, files);
  }

  function draftThumb(key, img, i) {
    const url = img.url ?? storedImage(img.id);
    return h("span", { class: `thumb${url ? "" : " empty"}${url === "" ? " loading" : ""}` },
      url ? h("img", { src: url, alt: `Attached image ${i + 1}` }) : null,
      h("button", { type: "button", class: "icon", "aria-label": `Remove image ${i + 1}`, onclick: () => {
        forgetImages([img]);
        draft(key).splice(i, 1);
        render();
      } }, icon("x")),
    );
  }

  // Thumbnails load on first sight and are kept for the life of the page;
  // stored images never change. "" is loading and null is gone.
  function storedImage(id) {
    const url = state.images.get(id);
    if (url !== undefined) return url;
    loadImage(id);
    return "";
  }

  function taskImages(t) {
    if (!t.images?.length) return null;
    return h("div", { class: "thumbs" }, t.images.map((id, i) => {
      const url = storedImage(id);
      if (!url) return h("span", { class: `thumb empty${url === null ? "" : " loading"}`, title: url === null ? "Image is gone" : "Loading…" });
      const img = h("img", { src: url, alt: `Image ${i + 1} of task` });
      return h("button", { type: "button", class: "thumb", "aria-label": `Enlarge image ${i + 1}`, onclick: (e) => e.currentTarget.classList.toggle("big") }, img);
    }));
  }

  function loadImage(id) {
    state.images.set(id, "");
    fs.getDoc(fs.doc(db, "attachments", id)).then((snap) => {
      const d = snap.data();
      state.images.set(id, d ? URL.createObjectURL(new Blob([d.data.toUint8Array()], { type: d.type })) : null);
      render();
    }, () => {
      state.images.set(id, null);
      render();
    });
  }

  function startEdit(t) {
    state.editing = t.id;
    state.editProject = undefined;
    state.menu = null;
    dropDraft(`edit:${t.id}`);
    state.drafts.set(`edit:${t.id}`, (t.images || []).map((id) => ({ id })));
    render();
  }

  function editor(t, movable = true) {
    const key = `edit:${t.id}`;
    // Snapshots re-render the editor; the chosen project survives in state.
    const chosen = state.editProject ?? t.project;
    const close = () => {
      dropDraft(key);
      state.editing = null;
      state.editProject = undefined;
      render();
    };
    const save = async (text) => {
      const value = text.value.trim();
      if (!value) return;
      try {
        await track(key, () => saveTask(t, value, chosen, draft(key)));
        reveal(chosen);
        close();
      } catch (err) {
        alertLine(`Could not save: ${err.code === "permission-denied" ? "the task was already sent" : err.message}`);
      }
    };
    const form = composerForm({ key, id: `edit-${t.id}`, label: "Task text", placeholder: "Describe a task…", rows: "3", cls: "edit",
      submitLabel: "Save", submitIcon: "check", send: save, lead: movable ? projectPill(chosen, t) : null,
      tail: h("button", { type: "button", class: "link", onclick: close }, "Cancel") });
    form.querySelector("textarea").value = t.text;
    return form;
  }

  // The closed sheet stays in the page, hidden, with its own alert line.
  function alertLine(text, tone = "bad") {
    const el = (sheet?.open ? sheet.scrim : app).querySelector(".alert");
    if (!el) return text && toast(text, tone);
    el.textContent = text;
    toneOf(el, tone);
  }

  // A resume that went through closes the inbox sheet, as a sent task does;
  // the worker's new conversation is the result.
  function resumed(text, busy, tone) {
    if (!busy && !text && sheet?.open) return closeSheet();
    alertLine(busy && !text ? "" : text, tone);
  }

  // Whether this browser already receives notifications, checked once per
  // load; the subscription itself lives in the browser and in Firestore.
  async function checkPush() {
    if (!("PushManager" in window) || !("serviceWorker" in navigator)) return (state.push = "unsupported");
    const reg = await navigator.serviceWorker.ready;
    const sub = await reg.pushManager.getSubscription();
    if (!sub || Notification.permission !== "granted") return (state.push = "off");
    const id = await subscriptionID(sub);
    const known = await fs.getDoc(fs.doc(db, "subscriptions", id));
    state.push = known.exists() ? "on" : "off";
  }

  async function subscriptionID(sub) {
    return passkey.b64url(await passkey.sha256(sub.endpoint));
  }

  async function turnOnPush() {
    try {
      if ((await Notification.requestPermission()) !== "granted") {
        setOutcome("notify", "Notifications are blocked for this site in the browser settings.", false, "bad");
        return;
      }
      const key = await fs.getDoc(fs.doc(db, "laptop", "push"));
      if (!key.exists()) throw new Error("the laptop has not published its push key; start make serve");
      const reg = await navigator.serviceWorker.ready;
      const appKey = passkey.urlB64ToBytes(key.data().vapid_public_key);
      let sub = await reg.pushManager.getSubscription();
      // A subscription made with another key cannot receive this laptop's pushes.
      const current = sub && sub.options.applicationServerKey && new Uint8Array(sub.options.applicationServerKey);
      if (sub && (!current || current.length !== appKey.length || current.some((b, i) => b !== appKey[i]))) {
        await sub.unsubscribe();
        sub = null;
      }
      if (!sub) sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: appKey });
      const json = sub.toJSON();
      await fs.setDoc(fs.doc(db, "subscriptions", await subscriptionID(sub)), {
        endpoint: json.endpoint, p256dh: json.keys.p256dh, auth: json.keys.auth, created_at: fs.serverTimestamp(),
      });
      state.push = "on";
      toast("Notifications are on for this device.", "ok");
      render();
    } catch (err) {
      setOutcome("notify", `Could not turn on notifications: ${err.message}`, false, "bad");
    }
  }

  // The notification log, newest first, read again on every render.
  function logView() {
    const list = h("ol", { class: "notelog" }, h("li", { class: "hint" }, "Loading…"));
    const ctl = navigator.serviceWorker?.controller;
    if (!ctl) list.replaceChildren(h("li", { class: "hint" }, "No service worker controls this page."));
    else {
      const { port1, port2 } = new MessageChannel();
      port1.onmessage = (e) => list.replaceChildren(...(e.data.length ? e.data.reverse().map((x) => h("li", {}, JSON.stringify(x))) : [h("li", { class: "hint" }, "Nothing logged yet.")]));
      ctl.postMessage({ type: "read log" }, [port2]);
    }
    return h("div", {}, h("p", {}, h("a", { href: "#" }, "Back")), h("h2", { class: "group" }, "Notification log"),
      h("p", { class: "hint" }, `Permission: ${"Notification" in window ? Notification.permission : "unsupported"} · subscribed here: ${state.push}`), list);
  }

  function board() {
    const now = Date.now();
    const list = sortSessions([...state.sessions.keys()].map(shownWorker));
    const pushRow = state.push === "off"
      ? h("div", { class: "pushrow" }, h("span", {}, "Get notified when a worker finishes or needs you."), busyButton("notify", { type: "button", onclick: () => track("notify", turnOnPush) }, "Turn on notifications"), outcomeLine("notify"))
      : null;
    if (!state.loaded.has("sessions")) return loading("Loading workers…");
    if (!list.length) return h("div", {}, pushRow, h("p", { class: "notice" }, "No workers published yet. Start the agent on the laptop with make serve."));
    const out = [pushRow];
    let group = "";
    for (const s of list) {
      const st = STATES[s.state] || { label: s.state, tone: "idle", group: "Other" };
      if (st.group !== group) {
        group = st.group;
        out.push(h("h2", { class: "group" }, group));
      }
      const task = currentTask(state.tasks, s.id);
      const at = activity(s);
      out.push(
        h("a", { class: "card", href: `#s-${encodeURIComponent(s.id)}` },
          h("span", { class: s.aoe_status === "stopped" ? "facewrap stopped" : "facewrap" }, face(projectOf(s), s.title, "md"), h("span", { class: `sdot ${st.tone}` })),
          h("span", { class: "cbody" },
            // The group heading names the state; only Needs you holds more than one.
            h("span", { class: "top" },
              h("span", { class: "name" }, s.title),
              h("span", { class: "tags" }, tagList(s, false), waitingLabel(s.id)),
              h("span", { class: "grow" }),
              st.group === "Needs you" ? chip(st) : null,
              at ? h("span", { class: "when" }, ago(at, now)) : null),
            task ? h("span", { class: "curtask" }, `Task: ${oneLine(task.text)}`) : null,
            s.question ? h("span", { class: "msg ask" }, `Asks: ${oneLine(s.question.items.map((q) => q.question).join(" · "))}`)
              : awaitingReply(s) ? h("span", { class: "msg pending" }, pendingLabel(s))
              : s.cleared ? h("span", { class: "msg fresh" }, "New conversation")
              : s.message ? h("span", { class: "msg" }, oneLine(s.message.text)) : null,
          ),
        ),
      );
    }
    if (state.push === "on") out.push(h("p", { class: "hint loglink" }, h("a", { href: "#log" }, "Notification log")));
    return h("div", { class: "board" }, out);
  }

  function detail(s) {
    const now = Date.now();
    const st = STATES[s.state] || { label: s.state, tone: "idle" };
    const g = s.git;
    const busy = s.state === "working" || s.state === "needs_input";
    const task = currentTask(state.tasks, s.id);
    const rows = [["aoe", s.aoe_status], ["path", s.path]];
    if (!g) rows.push(["repository", "none"]);
    if (g) {
      rows.push(["branch", g.upstream ? `${g.branch} → ${g.upstream}` : g.branch || "detached"]);
      if (g.last) rows.push(["commit", `${g.last.hash} ${g.last.subject} (${ago(g.last.at, now)})`]);
      rows.push(["push", pushLabel(g)]);
      rows.push(["tree", g.dirty ? `${g.dirty} changed files` : "clean"]);
    }
    const sent = h("div", { class: s.prompt ? "reply sent" : "reply sent none" });
    sent.innerHTML = s.prompt ? renderMarkdown(s.prompt.text) : "<p>Nothing sent yet.</p>";
    const pending = awaitingReply(s);
    const msg = h("div", { class: pending ? "reply pending" : "reply" });
    if (pending) msg.append(h("p", { role: "status" }, s.state === "working" ? spinner() : null, pendingLabel(s)));
    else msg.innerHTML = s.message ? renderMarkdown(s.message.text) : "<p>No reply yet.</p>";
    const whole = state.history?.id === s.id ? state.history : null;
    const convo = whole
      ? h("div", { class: "convo" }, historyView(s, whole, now),
        state.delivered.has(s.id) ? [h("span", { class: "who mine" }, "You", ` · ${ago(s.prompt.at, now)}`), sent] : null,
        pending ? msg : null)
      : h("div", { class: "convo" },
        s.prompt || s.message
          ? h("button", { type: "button", class: "link earlier", onclick: () => openHistory(s) }, icon("up"), "Whole conversation")
          : null,
        h("span", { class: "who mine" }, "You", s.prompt ? ` · ${ago(s.prompt.at, now)}` : ""),
        sent,
        h("span", { class: "who" }, s.title, s.message && !pending ? ` · ${ago(s.message.at, now)}` : ""),
        msg,
      );
    return h("article", { class: "detail" },
      h("div", { class: "dhead" },
        h("div", { class: "drow" },
          h("a", { class: "back", href: "#", "aria-label": "Back to workers" }, icon("back")),
          h("span", { class: s.aoe_status === "stopped" ? "facewrap stopped" : "facewrap" }, face(projectOf(s), s.title, "lg"), h("span", { class: `sdot ${st.tone}` })),
          h("div", { class: "dtitle" },
            h("h2", {}, s.title, chip(st)),
            h("div", { class: "tags" }, tagList(s, true))),
          quickButton(s),
          inboxButton(s),
          workerMenu(s, busy)),
        outcomeLine(`worker:${s.id}`),
      ),
      task ? h("p", { class: "curtask" }, `Task: ${task.text}`, h("span", { class: "when" }, ` · sent ${ago(task.dispatched_at, now)} ago`)) : null,
      g && g.unpushed > 0 ? pushCard(s, now) : null,
      s.prototypes && s.prototypes.length ? prototypes(s.prototypes, now) : null,
      convo,
      waitingReplyList(s),
      s.question ? questionCard(s) : null,
      h("details", { class: "facts" }, h("summary", {}, "Details"),
        h("dl", {}, rows.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)]))),
      replyBox(s),
    );
  }

  // A worker's whole conversation is read from the laptop only while it is on
  // screen: opening it asks the laptop to publish it, each new prompt or reply
  // asks again, and leaving the worker or the app deletes it.
  function openHistory(s) {
    closeHistory();
    const root = document.documentElement;
    const whole = { id: s.id, latest: s.latest, data: null, nodes: null, unsub: null, fromBottom: root.scrollHeight - window.scrollY };
    state.history = whole;
    whole.unsub = fs.onSnapshot(fs.doc(db, "history", s.id), (snap) => {
      if (state.history !== whole) return;
      whole.data = snap.exists() ? snap.data() : null;
      whole.nodes = null;
      render();
    }, (err) => toast(`Cannot read the conversation: ${err.message}`, "bad"));
    requestHistory(whole);
    render();
  }

  // The earlier messages open above the latest ones, so until the laptop's
  // first copy is drawn the page keeps its distance from the bottom instead
  // of its distance from the top.
  function keepHistoryAnchor() {
    const whole = state.history;
    if (whole?.fromBottom == null) return;
    window.scrollTo({ top: document.documentElement.scrollHeight - whole.fromBottom, behavior: "instant" });
    if (whole.data?.served_at) whole.fromBottom = null;
  }

  function requestHistory(whole) {
    fs.setDoc(fs.doc(db, "history", whole.id), { requested_at: fs.serverTimestamp() }, { merge: true })
      .catch((err) => toast(`Cannot ask the laptop for the conversation: ${err.message}`, "bad"));
  }

  function closeHistory() {
    const whole = state.history;
    if (!whole) return;
    state.history = null;
    whole.unsub?.();
    fs.deleteDoc(fs.doc(db, "history", whole.id)).catch(() => {});
  }

  // The bar stays pinned while the earlier messages scroll under it, so the
  // button that reopens them takes the bar's place on screen with the latest
  // messages below it, wherever in the conversation it was closed.
  function collapseHistory(bar) {
    const top = bar.getBoundingClientRect().top;
    closeHistory();
    render();
    const again = document.querySelector(".detail .convo .earlier");
    if (again) window.scrollBy({ top: again.getBoundingClientRect().top - top, behavior: "instant" });
  }

  function followHistory(s) {
    const whole = state.history;
    if (!whole) return;
    if (!s || s.id !== whole.id) return closeHistory();
    if (s.latest !== whole.latest) {
      whole.latest = s.latest;
      requestHistory(whole);
    }
  }

  function historyView(s, whole, now) {
    const bar = h("div", { class: "convobar" },
      h("span", {}, "Whole conversation"),
      h("button", { type: "button", class: "link", onclick: () => collapseHistory(bar) }, icon("down"), "Latest only"));
    const data = whole.data;
    if (!data?.served_at) {
      return [bar, laptopOnline(state.laptop, now)
        ? loading("Reading the conversation on the laptop…")
        : h("p", { class: "hint" }, "The laptop is offline. The conversation loads once it is back.")];
    }
    whole.nodes ??= historyEntries(s, data.entries || [], now);
    return [bar,
      data.truncated ? h("p", { class: "hint" }, "The oldest messages are left out: the whole conversation is too long to show.") : null,
      ...(whole.nodes.length ? whole.nodes : [h("p", { class: "hint" }, "Nothing in this conversation yet.")])];
  }

  // Successive tool calls collapse into one line that opens to list them.
  function historyEntries(s, entries, now) {
    const out = [];
    let tools = [];
    let last = null;
    const flush = () => {
      if (!tools.length) return;
      const lines = tools.map((e) => h("li", {}, h("span", { class: "tname" }, e.tool), e.text ? ` ${e.text}` : ""));
      out.push(h("details", { class: "tools" },
        h("summary", {}, tools.length === 1 ? `${tools[0].tool}${tools[0].text ? ` ${tools[0].text}` : ""}` : `${tools.length} tool calls`),
        h("ul", {}, lines)));
      tools = [];
    };
    for (const e of entries) {
      if (e.kind === "tool") { tools.push(e); continue; }
      flush();
      const mine = e.kind === "prompt";
      if (mine || last !== "reply") out.push(h("span", { class: mine ? "who mine" : "who" }, mine ? "You" : s.title, e.at ? ` · ${ago(e.at, now)}` : ""));
      const body = h("div", { class: mine ? "reply sent" : "reply" });
      body.innerHTML = renderMarkdown(e.text);
      out.push(body);
      last = e.kind;
    }
    flush();
    return out;
  }

  // Clear, start, restart and stop share a menu so the worker's name and
  // state keep the header's width on a phone.
  function workerMenu(s, busy) {
    const key = `worker:${s.id}`;
    const open = state.menu === key;
    const run = (cmd, label, fields = {}) => {
      state.menu = null;
      workerCommand(s, cmd, label, fields);
    };
    const item = (cmd, ico, label, cls) => busyButton(`${cmd}:${s.id}`, { type: "button", role: "menuitem", class: cls,
      onclick: () => run(cmd, `${cmd[0].toUpperCase()}${cmd.slice(1)}`) },
    icon(ico), label);
    const clearStop = h("button", { type: "button", role: "menuitem", class: "danger",
      onclick: () => run("stop", "Clear and stop", { clear: true }) },
    icon("clearstop"), "Clear and stop");
    const stopped = s.aoe_status === "stopped";
    const pending = WORKER_COMMANDS.some((c) => state.busy.has(`${c}:${s.id}`));
    return h("div", { class: "wmenu" },
      h("button", { type: "button", class: "icon tool", "aria-label": "Worker actions", "aria-haspopup": "menu", "aria-expanded": String(open),
        onclick: () => { state.menu = open ? null : key; render(); } }, pending ? spinner() : icon("more")),
      open ? h("div", { class: "pop", role: "menu", onkeydown: (e) => { if (e.key === "Escape") { state.menu = null; render(); } } },
        s.cleared || busy ? null : item("clear", "broom", "Clear conversation"),
        stopped ? item("start", "play", "Start worker") : item("restart", "restart", "Restart worker"),
        stopped ? null : h("hr"),
        stopped ? null : item("stop", "stop", "Stop worker"),
        stopped || s.cleared || busy ? null : clearStop,
      ) : null);
  }

  // The header's commands share one result line, which shows the latest.
  function workerCommand(s, cmd, label, fields) {
    sendCommand(cmd, { session: s.id, ...fields }, label, `${cmd}:${s.id}`,
      (text, busy, tone) => setOutcome(`worker:${s.id}`, text, busy, tone), null, { done: DONE[label] });
  }

  function queuedFor(project) {
    return queue(state.tasks, state.projects).filter((t) => t.project === project);
  }

  // The quick action shares the menu's busy keys, so either shows the other's
  // command as pending.
  function quickButton(s) {
    const p = projectOf(s);
    const action = quickAction(s, p ? queuedFor(p.name).length : 0);
    if (!action) return null;
    const [cmd, ico, label, fields] = {
      start: ["start", "play", "Start", {}],
      stop: ["stop", "stop", "Stop", {}],
      clear: ["clear", "broom", "Clear", {}],
      clearstop: ["stop", "clearstop", "Clear and stop", { clear: true }],
    }[action];
    const title = action === "clear" ? "Clear conversation" : label;
    return busyButton(`${cmd}:${s.id}`, { type: "button", class: "icon tool", title, "aria-label": title,
      onclick: () => workerCommand(s, cmd, label, fields) },
    icon(ico));
  }

  function inboxButton(s) {
    const p = projectOf(s);
    if (!p) return null;
    const n = queuedFor(p.name).length;
    return h("button", { type: "button", class: "icon tool inboxbtn", title: "Inbox", "aria-label": n ? `Inbox, ${n} queued` : "Inbox", "aria-haspopup": "dialog",
      onclick: () => openSheet(p.name) },
      icon("tray"), n ? h("span", { class: "count" }, String(n)) : null);
  }

  // The project's inbox as a sheet over its worker. It lives outside #app and
  // its composer is built once per project, never redrawn: a snapshot while
  // typing would otherwise replace the textarea under the caret, dropping
  // keystrokes and the phone keyboard's composition.
  let sheet = null;

  function openSheet(project) {
    if (sheet?.project !== project) buildSheet(project);
    sheet.open = true;
    sheet.arranging = false;
    sheet.scrim.hidden = false;
    render();
    if (!queuedFor(project).length) sheet.text.focus();
  }

  function closeSheet() {
    if (!sheet?.open) return;
    sheet.open = false;
    sheet.scrim.hidden = true;
    sheet.scrim.querySelector(".alert").textContent = "";
    state.menu = null;
    state.sheetSent = null;
    if (state.editing && sheet.list.contains(document.getElementById(`edit-${state.editing}`))) {
      dropDraft(`edit:${state.editing}`);
      state.editing = null;
    }
    render();
  }

  function buildSheet(project) {
    if (sheet) {
      dropDraft(sheet.key);
      sheet.scrim.remove();
    }
    const key = `sheet:${project}`;
    // The draft is cleared as soon as the task is accepted, as in the inbox.
    // A held add sends only if the worker is still free once the task is saved.
    const add = async (text, andSend = false) => {
      const value = text.value.trim();
      if (!value) return;
      const attaching = draft(key);
      text.value = "";
      state.drafts.delete(key);
      try {
        const t = await track(`add:${key}`, () => addTask(project, value, attaching));
        forgetImages(attaching);
        if (andSend && projectNamed(project)?.free) sheetSend(t, hasConversation(t));
      } catch (err) {
        if (!text.value) text.value = value;
        if (!draft(key).length) state.drafts.set(key, attaching);
        render();
        alertLine(`Could not add the task: ${err.message}`);
      }
    };
    const hold = {
      armed: (text) => Boolean(text.value.trim() && projectNamed(project)?.free),
      fire: (text) => add(text, true),
    };
    const form = composerForm({ key, id: "sheet-text", label: `New task for ${project}`, placeholder: "Add a task", cls: "add", submitLabel: "Add task", submitIcon: "addtask", send: add, hold });
    const head = h("div", { class: "sheethead" });
    const list = h("div", { class: "sheetbody tlist tasks" });
    const scrim = h("div", { class: "scrim", hidden: "", onclick: (e) => e.target === e.currentTarget && closeSheet() },
      h("section", { class: "sheet", role: "dialog", "aria-modal": "true", "aria-label": `${project} inbox`, onkeydown: (e) => e.key === "Escape" && closeSheet() },
        sheetTop(head, closeSheet),
        h("p", { class: "alert", id: "alert", role: "status" }),
        list,
        h("div", { class: "sheetfoot" }, form)));
    document.body.append(scrim);
    sheet = { project, key, scrim, head, list, form, text: form.querySelector("textarea"), open: false, arranging: false };
  }

  function sheetSend(t, clear) {
    state.sheetSent = t.id;
    send(t, clear);
  }

  function renderSheet(s) {
    if (!sheet?.open) return;
    if (!s || projectOf(s)?.name !== sheet.project) return closeSheet();
    const tasks = queuedFor(sheet.project);
    // A sent task closes the sheet, unless something is being written for the next one.
    if (state.sheetSent && !tasks.some((t) => t.id === state.sheetSent)) {
      state.sheetSent = null;
      if (!sheet.text.value.trim() && !draft(sheet.key).length) return closeSheet();
    }
    const p = projectNamed(sheet.project);
    const now = Date.now();
    sheet.head.replaceChildren(
      face(p, sheet.project, "sm"),
      h("h2", { class: "grow" }, "Inbox", h("span", { class: `dot ${p?.free ? "free" : "busy"}`, title: p?.free ? "Free" : p?.busy || "Busy" })),
      ...(tasks.length > 1 || sheet.arranging
        ? [h("button", { type: "button", class: "link", onclick: () => { sheet.arranging = !sheet.arranging; state.menu = null; render(); } },
          icon("sort"), sheet.arranging ? "Done" : "Reorder")]
        : []),
      h("button", { type: "button", class: "icon", "aria-label": "Close", onclick: closeSheet }, icon("x")));
    sheet.list.replaceChildren(...(tasks.length
      ? tasks.map((t, i) => (state.editing === t.id ? editor(t, false)
        : sheet.arranging ? arrangeRow(tasks, t, i, { inProject: true })
          : taskCard(t, now, { inProject: true, sendWith: sheetSend })))
      : [h("p", { class: "empty" }, "Nothing queued.")]),
    ...(sheet.arranging ? [] : [
      doneFold(sheet.showDone, () => { sheet.showDone = !sheet.showDone; render(); }, sheet.project),
      ...(sheet.showDone ? doneList(sheet.project, now, true) : []),
    ]));
    syncThumbs(sheet.form, sheet.key);
  }

  // Only the thumbnails are redrawn; the textarea stays where it is.
  function syncThumbs(form, key) {
    const imgs = draft(key);
    form.querySelector(":scope > .thumbs")?.remove();
    if (imgs.length) form.prepend(h("div", { class: "thumbs" }, imgs.map((img, i) => draftThumb(key, img, i))));
    form.querySelector('[aria-label="Attach image"]').disabled = imgs.length >= MAX_IMAGES;
  }

  function pushCard(s, now) {
    const g = s.git;
    const more = g.unpushed - (g.pending?.length ?? 0);
    return h("section", { class: "pushcard" },
      h("div", { class: "phead" },
        h("h3", {}, `Unpushed commits · ${g.unpushed}`),
        h("span", { class: "grow" }),
        busyButton(`push:${s.id}`, { type: "button", class: "primary ok", onclick: () => sendCommand("push", { session: s.id }, "Push", `push:${s.id}`, undefined, null, { done: "Pushed" }) }, `Push ${g.unpushed}`)),
      g.pending?.length ? h("ul", { class: "pending" },
        g.pending.map((c) => h("li", {}, h("code", {}, c.hash), ` ${c.subject}`, h("span", { class: "when" }, ago(c.at, now)))),
        more > 0 ? h("li", { class: "when" }, `and ${more} older`) : null,
      ) : null,
      outcomeLine(`push:${s.id}`),
    );
  }

  // questionCard answers the session's open multiple-choice dialog. The
  // laptop dismisses the dialog and sends the answers as a prompt, so the
  // terminal can still answer until then.
  function questionCard(s) {
    const q = s.question;
    if (!state.answers.has(q.id)) state.answers.set(q.id, q.items.map(() => ({ chosen: [], other: "" })));
    const picks = state.answers.get(q.id);
    const key = `answer:${s.id}`;
    const send = busyButton(key, { type: "submit", class: "primary send" }, "Send answers");
    const sync = () => { send.disabled = state.busy.has(key) || !answerText(q, picks); };
    const items = q.items.map((item, i) => {
      const p = picks[i];
      const name = `q-${q.id}-${i}`;
      const other = h("input", { type: "text", class: "qother", placeholder: "Or type your own answer…", "aria-label": `Own answer: ${item.question}` });
      other.value = p.other;
      const choices = item.options.map((o) => {
        const input = h("input", { type: item.multi_select ? "checkbox" : "radio", name, value: o.label });
        input.checked = p.chosen.includes(o.label);
        input.addEventListener("change", () => {
          if (item.multi_select) {
            p.chosen = input.checked ? [...p.chosen, o.label] : p.chosen.filter((x) => x !== o.label);
          } else {
            // The terminal dialog takes one option or typed text, not both.
            p.chosen = [o.label];
            p.other = other.value = "";
          }
          sync();
        });
        return h("label", { class: "qopt" }, input,
          h("span", {}, h("strong", {}, o.label), o.description ? h("span", { class: "qdesc" }, o.description) : null));
      });
      other.addEventListener("input", () => {
        p.other = other.value;
        if (!item.multi_select && other.value.trim()) {
          p.chosen = [];
          for (const c of choices) c.querySelector("input").checked = false;
        }
        sync();
      });
      return h("fieldset", { class: "qitem" },
        h("legend", {}, item.header ? h("span", { class: "qhead" }, item.header) : null, item.question),
        item.multi_select ? h("span", { class: "qdesc" }, "Pick any") : null,
        choices, other);
    });
    sync();
    const submit = async (e) => {
      e.preventDefault();
      const text = answerText(q, picks);
      if (text && (await sendCommand("answer", { session: s.id, question: q.id, text }, "Answer", key, undefined, null, { done: "Sent" }))) {
        state.answers.delete(q.id);
      }
    };
    return h("form", { class: "qcard", onsubmit: submit },
      h("h3", {}, `${s.title} asks`),
      items,
      h("div", { class: "qfoot" }, h("span", { class: "hint" }, "Closes the dialog on the laptop and sends these answers."), send),
      outcomeLine(key));
  }

  function shownWorker(id) {
    const s = state.sessions.get(id);
    return s && withDelivered(s, state.delivered.get(id));
  }

  // The reply stands in for the prompt from when the laptop has typed it.
  function delivered(s, text, images) {
    const after = state.sessions.get(s.id)?.prompt?.id ?? "";
    return () => {
      state.delivered.set(s.id, { text, images: images.length, at: new Date(), after });
      render();
    };
  }

  function replyBox(s) {
    const key = `reply:${s.id}`;
    const label = `Reply to ${s.title}`;
    const reply = async (text) => {
      const v = text.value.trim();
      const imgs = draft(key);
      if ((!v && !imgs.length) || state.busy.has(key)) return;
      let ids = [];
      if (imgs.length) {
        try {
          const batch = fs.writeBatch(db);
          ids = await storeImages(batch, imgs);
          await batch.commit();
        } catch (err) {
          setOutcome(key, `Not sent: ${err.message}`, false, "bad");
          return;
        }
      }
      const queued = state.replies.find((r) => r.session === s.id && r.status === "pending");
      const merged = queued ? mergeReply(queued, v, ids, MAX_IMAGES) : { text: v, images: ids };
      if (!merged) {
        setOutcome(key, `Not sent: with the reply already queued, that is more than ${MAX_IMAGES} images.`, false, "bad");
        dropUnused([{ images: ids }]);
        return;
      }
      if (await sendCommand("reply", replyFields(s, merged.text, merged.images), "Reply", key, undefined, delivered(s, merged.text, merged.images), { queue: true, cancel: queued?.id, done: null })) {
        // A redraw while sending replaces this textarea, so clear the one on screen.
        const box = document.getElementById(`reply-${s.id}`);
        if (box) box.value = "";
        dropDraft(key);
        render();
        // The sent reply lands at the end of the conversation, under the composer.
        window.scrollTo({ top: document.documentElement.scrollHeight, behavior: "smooth" });
      } else {
        dropUnused([{ images: ids }]);
      }
    };
    const form = composerForm({ key, id: `reply-${s.id}`, label: "Reply", placeholder: `Reply to ${s.title}…`, rows: "1", cls: "respond", submitLabel: "Send reply", submitIcon: "prompt", send: reply });
    const o = outcomeLine(key);
    if (o) form.prepend(o);
    return form;
  }

  function waitingLabel(id) {
    const mine = state.replies.filter((r) => r.session === id);
    if (!mine.length) return null;
    return h("span", { class: "waitmeta" }, mine.some((r) => r.status === "held") ? "reply held" : "reply queued");
  }

  // Replies waiting for the laptop: queued while it is away, or held because
  // the conversation moved on before it saw them, which needs your say.
  function waitingReplyList(s) {
    const mine = state.replies.filter((r) => r.session === s.id);
    if (!mine.length) return null;
    const online = laptopOnline(state.laptop, Date.now());
    const withdraw = (r, what) => track(`withdraw:${r.id}`, () =>
      fs.updateDoc(fs.doc(db, "commands", r.id), { status: "cancelled" })
        .catch((err) => setOutcome(`withdraw:${r.id}`, `Could not ${what} the reply: ${err.message}`, false, "bad")));
    return h("div", { class: "convo waiting" }, mine.map((r) => {
      const body = h("div", { class: "reply sent queued" });
      body.innerHTML = r.text ? renderMarkdown(r.text) : "";
      if (r.images.length) body.append(h("p", {}, `${r.images.length} image${r.images.length === 1 ? "" : "s"}`));
      const held = r.status === "held";
      const actions = held
        ? [busyButton(`resend:${r.id}`, { type: "button", class: "link", onclick: () =>
            sendCommand("reply", replyFields(s, r.text, r.images), "Reply", `resend:${r.id}`, undefined, delivered(s, r.text, r.images), { queue: true, cancel: r.id, done: null }) }, "Send anyway"),
          busyButton(`withdraw:${r.id}`, { type: "button", class: "link", onclick: () => withdraw(r, "discard") }, "Discard")]
        : [busyButton(`withdraw:${r.id}`, { type: "button", class: "link", onclick: () => withdraw(r, "cancel") }, "Cancel")];
      return h("div", { class: "waitrow" },
        h("span", { class: "who mine", role: "status" },
          held ? "Held: the conversation moved on since you wrote this" : online ? "Sending…" : "Queued until the laptop is back online"),
        body,
        outcomeLine(`resend:${r.id}`),
        outcomeLine(`withdraw:${r.id}`),
        h("div", { class: "waitacts" }, actions));
    }));
  }

  // Images no task or waiting reply names once gone are deleted.
  function dropUnused(gone) {
    const unused = orphanedImages([...state.tasks, ...state.replies], gone);
    if (!unused.length) return;
    const batch = fs.writeBatch(db);
    for (const id of unused) batch.delete(fs.doc(db, "attachments", id));
    batch.commit().catch(() => {});
  }
}

function prototypes(list, now) {
  return h("ul", { class: "protos" },
    list.map((p) => h("li", {},
      h("a", { href: p.url, target: "_blank", rel: "noopener noreferrer" }, icon("frame"), h("span", { class: "grow" }, p.title || p.url)),
      h("span", { class: "when" }, ago(p.at, now)),
    )),
  );
}

function loading(text) {
  return h("p", { class: "notice loading", role: "status" }, spinner(), text);
}

// A worker's tags, the same on its card and its page. On the card the whole
// card is the link, so only the page links a tag to what it describes.
function tagList(s, linked) {
  return tags(s).map((t) => {
    const attrs = { class: `tag ${t.kind} ${t.tone}`.trim(), "aria-label": t.label, title: t.label };
    const body = [icon(t.kind), t.text || null];
    return linked && t.url
      ? h("a", { ...attrs, class: `${attrs.class} link`, href: t.url, target: "_blank", rel: "noopener noreferrer" }, body)
      : h("span", attrs, body);
  });
}

function chip(st) {
  return h("span", { class: `chip ${st.tone}` }, st.label);
}

function oneLine(text) {
  const s = String(text).replace(/[`*#]/g, "").replace(/\s+/g, " ").trim();
  return s.length > 160 ? `${s.slice(0, 159)}…` : s;
}

// Stroke icons drawn in the text colour; static markup, never data.
const ICONS = {
  more: '<circle cx="5" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="19" cy="12" r="1.6" fill="currentColor" stroke="none"/>',
  prompt: '<path d="M5 7l5 5-5 5"/><path d="M12.5 17H19"/>',
  addtask: '<path d="M4 6h11M4 12h11M4 18h7"/><path d="M18 14.5v7M14.5 18h7"/>',
  up: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  down: '<path d="M12 5v14M5 12l7 7 7-7"/>',
  chevron: '<path d="M6 9l6 6 6-6"/>',
  back: '<path d="M15 18l-6-6 6-6"/>',
  clip: '<path d="M21 11.5l-8.6 8.6a5 5 0 01-7.1-7.1l8.6-8.6a3.4 3.4 0 014.8 4.8l-8.6 8.6a1.7 1.7 0 01-2.4-2.4l7.9-7.9"/>',
  tray: '<path d="M4 13l2.5-7h11L20 13v6H4z"/><path d="M4 13h4.5l1 2h5l1-2H20"/>',
  sort: '<path d="M7 4v16M3 8l4-4 4 4M17 20V4M13 16l4 4 4-4"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
  x: '<path d="M6 6l12 12M18 6L6 18"/>',
  broom: '<g transform="translate(2.2 -1.2) rotate(35 12 12)"><path d="M12 1.5V10"/><rect x="8.5" y="10" width="7" height="3" rx="1"/><path d="M8.5 13L6.5 21h11l-2-8"/><path d="M10.5 16.5l-.4 4.5M13.5 16.5l.4 4.5"/></g>',
  clearstop: '<g transform="translate(-0.3 -0.4) scale(.85) translate(2.2 -1.2) rotate(35 12 12)"><path d="M12 1.5V10"/><rect x="8.5" y="10" width="7" height="3" rx="1"/><path d="M8.5 13L6.5 21h11l-2-8"/><path d="M10.5 16.5l-.4 4.5M13.5 16.5l.4 4.5"/></g><rect x="15" y="15" width="6.5" height="6.5" rx="1.5"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="2"/>',
  play: '<path d="M8 5.5v13l10.5-6.5z"/>',
  restart: '<path d="M20 12a8 8 0 11-2.3-5.6"/><path d="M20 4v4.5h-4.5"/>',
  resume: '<path d="M3.5 6v12l8-6z"/><path d="M15.5 6v12M20 6v12"/>',
  clearresume: '<g transform="scale(.7) translate(2.2 -1.2) rotate(35 12 12)"><path d="M12 1.5V10"/><rect x="8.5" y="10" width="7" height="3" rx="1"/><path d="M8.5 13L6.5 21h11l-2-8"/><path d="M10.5 16.5l-.4 4.5M13.5 16.5l.4 4.5"/></g><path d="M13.5 14.5v7l4-3.5z"/><path d="M19.5 14.5v7M22.5 14.5v7"/>',
  edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13.5 6.5l4 4"/>',
  push: '<path d="M12 20V8"/><path d="M6.5 13.5 12 8l5.5 5.5"/><path d="M5 4h14"/>',
  noremote: '<path d="M7 18h10a4 4 0 0 0 .7-7.9A6 6 0 0 0 6.2 9 4.5 4.5 0 0 0 7 18z"/><path d="M4 4l16 16"/>',
  branch: '<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="7" r="2"/><path d="M6 7v10"/><path d="M18 9c0 5-12 3-12 8"/>',
  "ci-queued": '<circle cx="12" cy="12" r="8.5" stroke-dasharray="2.5 3.2"/>',
  "ci-running": '<circle cx="12" cy="12" r="8.5" opacity="0.3"/><path class="arc" d="M12 3.5a8.5 8.5 0 0 1 8.5 8.5"/>',
  "ci-success": '<circle cx="12" cy="12" r="8.5"/><path d="M8.5 12.3l2.4 2.4 4.6-5"/>',
  "ci-failure": '<circle cx="12" cy="12" r="8.5"/><path d="M9.2 9.2l5.6 5.6M14.8 9.2l-5.6 5.6"/>',
  "ci-cancelled": '<circle cx="12" cy="12" r="8.5"/><path d="M8.5 12h7"/>',
  frame: '<rect x="4" y="4" width="16" height="16" rx="3"/><path d="M9 4v16"/>',
  grip: '<circle cx="9" cy="6" r="1.4" fill="currentColor" stroke="none"/><circle cx="15" cy="6" r="1.4" fill="currentColor" stroke="none"/><circle cx="9" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="15" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="9" cy="18" r="1.4" fill="currentColor" stroke="none"/><circle cx="15" cy="18" r="1.4" fill="currentColor" stroke="none"/>',
};

function icon(name) {
  const span = document.createElement("span");
  span.className = `ico ${name}`;
  span.setAttribute("aria-hidden", "true");
  span.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${ICONS[name]}</svg>`;
  return span;
}
