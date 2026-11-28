import { renderMarkdown } from "./markdown.js";
import { STATES, actionsLeft, activity, ago, usageLeft, antigravityLeft, tightest, quotaState, MODEL_POOLS, antigravityPools, modelVersions, sessionModel, answerText, awaitingReply, currentTask, filterQueue, laptopOnline, mergeReply, monogram, pendingLabel, pushLabel, queue, queueCounts, quickAction, rankAt, topRank, replyFields, resumeAction, doneError, searchProjects, sentState, sentTasks, sortProjects, sortSessions, tags, waitingReplies, withDelivered, PROTOTYPE_KINDS, prototypeKind, nextPrototype, LENSES, IDEA_LIMITS, DEFAULT_IDEATE, lensName, ideateText, toggleLens, openIdeas, ideaCount } from "./board.js";
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
function revBusy(busy) {
  revEl.disabled = busy;
  revEl.toggleAttribute("aria-busy", busy);
  revEl.classList.toggle("busy", busy);
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
const railEl = document.getElementById("rail");
const sideEl = document.getElementById("side");
// A laptop-wide window keeps the workers on the left and the shown worker's
// project on the right, so the inbox and repository need no sheet there.
const wideQuery = matchMedia("(min-width: 1180px)");
const wide = () => wideQuery.matches;
document.documentElement.classList.toggle("wide", wide());
const hstatEl = document.getElementById("hstat");
const laptopEl = document.getElementById("laptop");
const seenEl = document.getElementById("seen");
const updateEl = document.getElementById("update");
const usageEl = document.getElementById("usage");
const usagePopEl = document.getElementById("usagepop");
const barEl = document.querySelector("header.bar");
const homeEl = document.getElementById("home");
const hpageEl = document.getElementById("hpage");
const config = document.body.dataset.firebase;
// A wide header has room for the revision beside the laptop; a narrow one
// leaves it in the footer.
const footEl = document.querySelector("footer.foot");
function placeRevision() {
  if (wide()) laptopEl.after(revEl);
  else footEl.append(revEl);
}
placeRevision();
// On a wide window the shown worker's head stays at the top of its column, so
// the conversation bar sticks below it. A render replaces the head, so each
// render observes the new one.
const dheadSize = new ResizeObserver(([e]) => document.documentElement.style.setProperty("--dheadh", `${e.target.offsetHeight}px`));

// On a wide window #app is its own scroll container; on a narrow one the page scrolls.
function scrollHost() {
  return wide()
    ? { el: app, top: () => app.scrollTop, to: (o) => app.scrollTo(o), by: (o) => app.scrollBy(o) }
    : { el: document.documentElement, top: () => window.scrollY, to: (o) => window.scrollTo(o), by: (o) => window.scrollBy(o) };
}
// Checking for updates and updating start from the header, so their result
// shows under it.
const hpopEl = hstatEl.appendChild(Object.assign(document.createElement("p"), { className: "hpop", hidden: true }));
hpopEl.setAttribute("role", "status");
// A result drawn by a render is a new element each time, which screen readers
// do not announce; this one region, never shown, says it once.
const liveEl = document.body.appendChild(Object.assign(document.createElement("p"), { className: "sronly" }));
liveEl.setAttribute("role", "status");
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

function notice(text, ...extra) {
  hpageEl.replaceChildren();
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

  const state = { sessions: new Map(), projects: [], tasks: [], replies: [], ideas: [],
    // A composer's ideation ask by its key, and the last one made, which the next starts from.
    ideates: new Map(), lastIdeate: DEFAULT_IDEATE,
    // The idea whose dismissal note is being written.
    dismissing: null, modelFor: null, modelAsked: new Map(), modelMore: new Set(), moving: new Map(), models: null, laptop: null, usage: null, actions: null, version: null, user: null, picked: null, busy: new Set(),
    // The last word on a command, by its busy key, shown beside the control that sent it: { text, busy, tone }.
    outcomes: new Map(), added: null, settledIdeas: new Map(), loaded: new Set(), push: "unknown", editing: null, sends: new Map(), drafts: new Map(), protos: new Map(), images: new Map(),
    filter: "ready", only: null, lastPicked: null, showDone: false, sideDone: false, factsOpen: false, menu: null, picking: false, pickFor: null, pickQuery: "",
    // The picker's edit mode, what it has ticked, and the last hide it can undo.
    hiding: false, hideSel: new Set(), showHidden: false, pickStatus: "", pickTone: "", undoHide: null,
    // The task sent from a worker's inbox; the inbox closes once it leaves the queue.
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
      fs.onSnapshot(fs.query(fs.collection(db, "ideas"), fs.where("status", "==", "open")), (snap) => {
        state.ideas = snap.docs.map((d) => ({ id: d.id, ...d.data() }));
        state.loaded.add("ideas");
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
      fs.onSnapshot(fs.doc(db, "laptop", "models"), (snap) => {
        state.models = snap.exists() ? snap.data() : null;
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
  // A long press on a control opens the browser's link or text menu, which
  // covers the board; a link inside a reply keeps it so it can be copied.
  document.addEventListener("contextmenu", (e) => {
    const control = e.target.closest?.("a, button, [role=button]");
    if (control && !control.closest(".reply")) e.preventDefault();
  });
  document.addEventListener("click", (e) => {
    if (state.menu && !e.target.closest(".wmenu")) { state.menu = null; render(); }
  });
  usageEl.addEventListener("click", () => { state.menu = state.menu === "usage" ? null : "usage"; render(); });
  usageEl.parentElement.addEventListener("keydown", (e) => { if (e.key === "Escape" && state.menu === "usage") { state.menu = null; render(); usageEl.focus(); } });
  window.addEventListener("hashchange", () => {
    document.documentElement.classList.remove("tuck");
    if (wide()) { app.scrollTop = 0; sideEl.scrollTop = 0; }
    syncLayers();
    render();
  });
  window.addEventListener("popstate", syncLayers);
  wideQuery.addEventListener("change", () => {
    document.documentElement.classList.toggle("wide", wide());
    if (wide()) window.scrollTo(0, 0);
    placeRevision();
    if (wide() && inboxSheet?.open) closeLayer("inbox");
    render();
  });
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
    receiveShare().catch((err) => {
      openCompose(null, null);
      composeAlert(`Could not attach what was shared: ${err.message}`);
    });
    refreshPush();
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

  // The header stacks each quota's bars in one column: Claude Code's two
  // windows, Antigravity's tightest pool for each window, then the month's
  // Actions minutes. The popover lays them out as a table and names a number
  // only where a quota runs low; each row opens that quota's own page.
  function renderUsage(now) {
    const claude = usageLeft(state.usage, now);
    const ag = antigravityLeft(state.usage, now);
    const gh = actionsLeft(state.actions, now);
    const any = Boolean(claude || ag || gh);
    const open = state.menu === "usage" && any;
    usageEl.hidden = !any;
    usagePopEl.hidden = !open;
    usageEl.setAttribute("aria-expanded", String(open));
    const agTight = ag && tightest(ag);
    const lefts = [claude?.[0].left, claude?.[1].left, agTight?.[0].left, agTight?.[1].left, gh?.pct];
    usageEl.querySelectorAll(".meter").forEach((m, i) => meter(m, lefts[i]));
    const minutes = (n) => n.toLocaleString("en-US");
    const windows = (ws) => ws.map((w) => `${w.label} ${w.left}% left`).join(", ");
    const said = [
      claude && `Claude ${windows(claude)}`,
      ...(ag ?? []).map((p) => `Antigravity ${p.name} ${windows(p.windows)}`),
      gh && `Actions ${minutes(gh.left)} of ${minutes(gh.included)} minutes left`,
    ].filter(Boolean);
    usageEl.setAttribute("aria-label", `Usage: ${said.join("; ")}`);
    usageEl.title = said.join("\n");
    if (!open) {
      usagePopEl.replaceChildren();
      return;
    }
    // Under each bar is how long until it resets, in its state's color; a
    // window with no reset ahead is whole and says nothing.
    const cell = (left, resetsAt, kind, wide) => {
      const s = quotaState(left);
      return h("span", { class: `ucell${wide ? " wide" : ""}` }, meter(h("span", { class: `meter ${kind}` }, h("i")), left),
        resetsAt ? h("span", { class: `unote ${s}`.trim() }, ago(now, resetsAt.getTime())) : null);
    };
    const windowCells = (ws, kind) => ws.map((w) => cell(w.left, w.resetsAt, kind));
    const row = (href, kind, name, label, cells) => h("a", {
      class: "urow", href, target: "_blank", rel: "noopener", "aria-label": label,
      onclick: () => { state.menu = null; render(); },
    }, h("span", { class: "uname" }, logo(kind), name), ...cells);
    const parts = [h("span", { class: "uhead", "aria-hidden": "true" }, h("span"), h("span", {}, "5 hours"), h("span", {}, "Week"))];
    if (claude) parts.push(row("https://claude.ai/settings/usage", "cc", "Claude Code", `Claude Code: ${windows(claude)}`, windowCells(claude, "cc")));
    for (const p of ag ?? []) {
      parts.push(row("https://antigravity.google/docs/cli/commands/usage", "ag", p.name, `Antigravity ${p.name}: ${windows(p.windows)}`, windowCells(p.windows, "ag")));
    }
    if (gh) {
      if (claude || ag) parts.push(h("hr"));
      parts.push(row("https://github.com/settings/billing", "gh", "Actions",
        `GitHub Actions: ${minutes(gh.left)} of ${minutes(gh.included)} minutes left`,
        [cell(gh.pct, gh.resetsAt, "gh", true)]));
    }
    usagePopEl.replaceChildren(...parts);
    // The bars sit beside the logo, so a popover hung from them can pass the
    // screen's right edge; shift it left to the page gutter instead.
    usagePopEl.style.left = "";
    const past = usagePopEl.getBoundingClientRect().right - (document.documentElement.clientWidth - 16);
    if (past > 0) usagePopEl.style.left = `${usagePopEl.offsetLeft - past}px`;
  }

  // meter fills a bar with what is used; an unreported quota hides its bar.
  function meter(m, left) {
    m.hidden = left == null;
    const s = quotaState(left ?? 100);
    m.classList.toggle("low", s === "low");
    m.classList.toggle("out", s === "out");
    m.firstElementChild.style.width = `${100 - (left ?? 100)}%`;
    return m;
  }

  // #inbox and #new open a sheet over the workers. The sheet then holds the
  // history entry, so a later render does not open it again.
  const ROUTES = ["#inbox", "#new"];

  function render() {
    if (!state.user) return;
    if (location.hash === "#inbox" && wide()) {
      history.replaceState(history.state, "", location.pathname + location.search);
      Object.assign(state, { only: null, filter: "ready" });
    } else if (ROUTES.includes(location.hash)) return location.hash === "#inbox" ? openInbox(null) : openCompose(null, null);
    ensureSheets();
    renderLaptop();
    // Snapshots re-render the whole view; keep what is being typed. The New
    // task sheet is never redrawn, so it needs no keeping.
    const typed = 'textarea, input[type="search"]';
    const fields = () => [app, railEl, sideEl, inboxSheet.body, pickerHost].flatMap((el) => [...el.querySelectorAll(typed)]);
    const drafts = new Map(fields().map((t) => [t.id, t.value]));
    const focused = document.activeElement && document.activeElement.matches(typed) ? document.activeElement.id : null;

    followMove(shownSession());
    const id = shownSession();
    const s = id && shownWorker(id);
    followHistory(s);
    renderHeader(s);
    const isWide = wide();
    const page = location.hash.startsWith("#pair-") || location.hash === "#log";
    if (location.hash.startsWith("#pair-")) app.replaceChildren(pairView(location.hash.slice(6)));
    else if (location.hash === "#log") app.replaceChildren(logView());
    else if (s) app.replaceChildren(detail(s, isWide));
    else app.replaceChildren(isWide ? inboxPane() : board());
    dheadSize.disconnect();
    const dhead = app.querySelector(".dhead");
    if (dhead) dheadSize.observe(dhead);
    railEl.hidden = !isWide || page;
    railEl.replaceChildren(...(railEl.hidden ? [] : [board(s?.id)]));
    sideEl.hidden = !isWide || !s;
    sideEl.replaceChildren(...(sideEl.hidden ? [] : projectPane(s)));

    renderInbox();
    renderCompose();
    const modelFor = state.modelFor && shownWorker(state.modelFor);
    if (state.modelFor && !modelFor) state.modelFor = null;
    pickerHost.replaceChildren(...(state.picking ? [picker(visibleProjects())] : modelFor ? [modelSheet(modelFor)] : []));
    for (const t of fields()) if (drafts.has(t.id)) t.value = drafts.get(t.id);
    document.documentElement.classList.toggle("locked", layers.length > 0 || state.picking || Boolean(modelFor));
    if (focused) document.getElementById(focused)?.focus();
    keepHistoryAnchor();
    if (s && !document.hidden) dismissNotes(s.id);
  }

  // The header's left side is the same on every page; its right side holds
  // what the page offers. Off the workers list the logo leads back to it.
  function renderHeader(s) {
    const onBoard = !s && !location.hash.startsWith("#pair-") && location.hash !== "#log";
    barEl.classList.toggle("sub", !onBoard);
    homeEl.setAttribute("aria-label", onBoard ? "peyk" : "Back to workers");
    const p = s && projectOf(s);
    const tools = onBoard ? [addButton(null, null), inboxButton(null)]
      : s && wide() ? [addButton(p?.name ?? null, p?.name ?? null), inboxButton(null)]
        : s ? [addButton(p?.name ?? null, p?.name ?? null), p ? inboxButton(p) : null]
          : [];
    hpageEl.replaceChildren(...tools.filter(Boolean));
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

  // A worker moved to another tool gets a new session under the same title;
  // the page follows it there.
  function followMove(id) {
    if (!id || state.sessions.has(id) || !state.moving.has(id)) return;
    const title = state.moving.get(id);
    const next = [...state.sessions.values()].find((x) => x.title === title);
    if (!next) return;
    state.moving.delete(id);
    const asked = state.modelAsked.get(id);
    if (asked) {
      state.modelAsked.delete(id);
      state.modelAsked.set(next.id, asked);
    }
    history.replaceState(history.state, "", `#s-${encodeURIComponent(next.id)}`);
  }

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
    return h("p", { class: o.busy ? "outcome busy" : "outcome", role: "status", "aria-busy": o.busy ? "true" : null, "data-tone": o.tone || null },
      h("span", { class: "grow" }, o.text),
      o.busy || o.tone === "ok" ? null
        : h("button", { type: "button", class: "icon dismiss", "aria-label": "Dismiss", onclick: () => setOutcome(key, "") }, icon("x")));
  }

  function announce(text) {
    liveEl.textContent = "";
    requestAnimationFrame(() => { liveEl.textContent = text; });
  }

  // A task added while its inbox is out of sight is shown arriving there:
  // the inbox button names the project until the result settles.
  function addedTo(project) {
    const a = { project };
    state.added = a;
    announce(`Added to ${project}`);
    setTimeout(() => {
      if (state.added !== a) return;
      state.added = null;
      render();
    }, SETTLED_MS);
    render();
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
    return h("button", { ...attrs, class: `${attrs.class ?? ""} busy`, disabled: "", "aria-busy": "true" }, ...children);
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
      button.replaceChildren("Creating passkey…");
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
      h("h2", {}, "Pair this device"),
      h("p", {}, "Commands to the laptop are signed with a passkey on this device. Create one, then confirm its fingerprint on the laptop."),
      button, out);
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

  // A sent or done task has had its turn, so it is one faded line that opens
  // to its whole text and the rest of its controls.
  function taskRow(t, kind, now, { inProject = false, stat, statBusy = false, action = null, detail }) {
    const open = state.openRows.has(t.id);
    return h("div", { class: `task row ${kind}${open ? " open" : ""}` },
      h("div", { class: "rhead" },
        h("button", { type: "button", class: "rline", "aria-expanded": String(open),
          onclick: () => { open ? state.openRows.delete(t.id) : state.openRows.add(t.id); render(); } },
          inProject ? null : face(projectNamed(t.project), t.project, "xs"),
          h("span", { class: "rtext" }, inProject ? null : [h("b", {}, t.project), " "], t.text),
          t.prototype ? prototypeTag(t.prototype) : null,
          t.ideate ? ideateTag(t.ideate) : null,
          h("span", { class: statBusy ? "rstat busy" : "rstat" }, stat)),
        action),
      outcomeLine(`task:${t.id}`),
      outcomeLine(`resume:${t.id}`),
      h("div", { class: "rmore", hidden: open ? null : "" },
        h("div", { class: "rfull" }, t.text),
        taskImages(t),
        h("div", { class: "rmeta" }, detail, h("span", { class: "grow" }),
          h("button", { type: "button", class: "icon", "aria-label": `Delete task: ${t.text}`, onclick: () => remove(t) }, icon("x")))),
    );
  }

  // A sent task stays in the inbox until it is done. A new worker is only
  // published once the laptop sees it start; a worker removed or replaced
  // since leaves the task behind for you to delete.
  function sentRow(t, now) {
    const s = state.sessions.get(t.session_id);
    const st = sentState(t, s, projectNamed(t.project), now, state.loaded.has("sessions"));
    return taskRow(t, "sent", now, {
      stat: [h("span", { class: `dot ${st.starting ? "run" : st.tone}` }), `${st.label} · ${ago(t.dispatched_at, now)}`],
      statBusy: Boolean(st.starting || st.tone === "run"),
      detail: s ? h("a", { href: `#s-${encodeURIComponent(s.id)}` }, `${s.title} · ${st.label}`)
        : h("span", {}, st.gone ? "Its worker is gone." : "Starting the worker…"),
    });
  }

  // A send's outcome stays on its task, which may have moved since, rather
  // than in a status line scrolled out of view. also gets each report too,
  // for a send started from another card.
  function send(t, clear, { also = null } = {}) {
    const report = (text, busy = false, tone = "") => {
      state.sends.set(t.id, { text, busy, tone });
      if (also) also(text, busy, tone);
      render();
    };
    state.menu = null;
    sendCommand("dispatch", { project: t.project, task: t.id, text: t.text, clear, ...(t.images?.length ? { images: t.images } : {}), ...(t.prototype ? { prototype: t.prototype } : {}), ...(t.ideate ? { ideate: t.ideate } : {}) },
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
  // order is the shown list and the task's place in it; the arrows move the
  // task among the tasks on screen, so a filter keeps them meaningful.
  function taskCard(t, now, { inProject = false, sendWith = send, order = null } = {}) {
    const clears = t.ready && hasConversation(t);
    const open = state.menu === t.id;
    const sent = state.sends.get(t.id);
    const status = sent
      ? h("span", { class: sent.busy && sent.text ? "tstatus busy" : "tstatus", role: "status", "data-tone": sent.tone || null }, sent.text)
      : h("span", { class: "tstatus" }, t.ready
        ? (projectNamed(t.project)?.session_id ? "Free" : "Starts a new worker")
        : `Waiting · ${t.why}`);
    return h("article", { class: `task${t.ready ? "" : " waiting"}` },
      h("div", { class: "thead" },
        inProject ? null : [face(projectNamed(t.project), t.project, "sm"), h("span", { class: "proj" }, t.project)],
        t.saving ? h("span", { class: "when saving" }, "saving") : h("span", { class: "when" }, ago(t.created_at, now)),
        t.prototype ? prototypeTag(t.prototype) : null,
        t.ideate ? ideateTag(t.ideate) : null,
        h("span", { class: "grow" }),
        order && order.list.length > 1 ? stepButtons(t, order) : null,
        h("div", { class: "wmenu" },
          h("button", { type: "button", class: "icon", "aria-label": `More actions: ${t.text}`, "aria-haspopup": "menu", "aria-expanded": String(open),
            onclick: () => { state.menu = open ? null : t.id; render(); } }, icon("more")),
          open ? taskMenu(t, clears, sendWith) : null)),
      h("div", { class: "ttext" }, t.text),
      taskImages(t),
      h("div", { class: "tfoot" },
        h("span", { class: `dot ${sent ? "sent" : t.ready ? "free" : "busy"}` }),
        status,
        t.ready ? sendButton(t, clears, () => sendWith(t, clears)) : null,
      ),
      outcomeLine(`task:${t.id}`),
    );
  }

  function sendButton(t, clears, onclick) {
    const label = clears ? "Clear and send" : "Send";
    return busyButton(`send:${t.id}`, { type: "button", class: "primary send", title: label, "aria-label": label, onclick },
      clears ? icon("broom") : null, icon("prompt"));
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

  function stepButtons(t, { list, i }) {
    return [
      h("button", { type: "button", class: "icon step", title: "Move up", "aria-label": `Move up: ${t.text}`, disabled: i === 0 ? "" : null, onclick: () => move(t, i - 1, list) }, icon("chevup")),
      h("button", { type: "button", class: "icon step", title: "Move down", "aria-label": `Move down: ${t.text}`, disabled: i === list.length - 1 ? "" : null, onclick: () => move(t, i + 1, list) }, icon("chevron")),
    ];
  }

  function move(t, to, list) {
    const i = list.findIndex((x) => x.id === t.id);
    state.menu = null;
    fs.updateDoc(fs.doc(db, "tasks", t.id), { rank: rankAt(list, i, to) })
      .catch((err) => setOutcome(`task:${t.id}`, `Could not move the task: ${err.message}`, false, "bad"));
  }

  // New tasks, edits and replies share one composer, so each takes text,
  // pasted screenshots and attached images the same way. key names both its
  // busy state and its draft images.
  // hold, when given, answers a long press on the submit button while
  // hold.armed() is true; otherwise the press submits as a tap would.
  function composerForm({ key, id, label, placeholder, rows = "2", cls, submitLabel, submitIcon, send, hold = null, lead = null, tools = null, tail = null }) {
    const imgs = draft(key);
    const text = h("textarea", { id, rows, placeholder, "aria-label": label, onpaste: (e) => pasteImages(key, e) });
    const files = h("input", { type: "file", accept: "image/*", multiple: "", hidden: "", "aria-label": "Attach images", onchange: (e) => { attachImages(key, [...e.target.files]); e.target.value = ""; } });
    return h("form", { class: `composer ${cls}`, onsubmit: (e) => { e.preventDefault(); send(text); } },
      imgs.length ? h("div", { class: "thumbs" }, imgs.map((img, i) => draftThumb(key, img, i))) : null,
      text,
      outcomeLine(`attach:${key}`),
      h("div", { class: "crow" },
        lead,
        h("button", { type: "button", class: "icon round", title: "Attach an image or screenshot", "aria-label": "Attach image", disabled: imgs.length >= MAX_IMAGES ? "" : null, onclick: () => files.click() }, icon("clip")),
        tools,
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

  // One button steps a composer's task through an ordinary task and each
  // kind of prototype; a long press turns it back to ordinary. It redraws
  // only itself and its note, so the textarea keeps its focus and caret.
  function prototypeToggle(key, note, onSet = null) {
    const button = h("button", { type: "button", class: "proto", ...longPress(() => Boolean(state.protos.get(key)), () => set("")) });
    const paint = () => {
      const kind = state.protos.get(key) ?? "";
      const k = PROTOTYPE_KINDS[kind];
      button.dataset.kind = kind || "off";
      button.setAttribute("aria-pressed", String(Boolean(kind)));
      button.setAttribute("aria-label", kind ? `Prototype: ${PROTOTYPE_NOTES[kind].label}` : "Prototype: off");
      button.title = kind ? `${PROTOTYPE_NOTES[kind].label}. Tap for the next kind; hold to turn off.` : "Ask for a prototype";
      button.replaceChildren(icon(k ? k.icon : "proto"), ...(k ? [h("span", {}, k.name)] : []));
      if (note) {
        note.textContent = kind ? PROTOTYPE_NOTES[kind].note : "";
        note.hidden = !kind;
      }
    };
    const set = (kind) => {
      kind ? state.protos.set(key, kind) : state.protos.delete(key);
      paint();
      onSet?.(kind);
    };
    // The long press's own click handler runs first and swallows the click
    // that ends a hold.
    button.addEventListener("click", (e) => e.defaultPrevented || set(nextPrototype(state.protos.get(key))));
    paint();
    return button;
  }

  // Ideate turns a composer's task into an ideation run: its text becomes an
  // optional focus, and a panel picks the lenses and how many ideas. A task
  // asks for a prototype or for ideas, not both. The toggle and panel redraw
  // only themselves, so the textarea keeps its focus and caret.
  function ideateToggle(key, onSet) {
    const button = h("button", { type: "button", class: "proto ideate", title: "Ask for improvement ideas" });
    const panel = h("div", { class: "ideatepanel", hidden: "" });
    const paint = () => {
      const ask = state.ideates.get(key);
      button.dataset.kind = ask ? "ideate" : "off";
      button.setAttribute("aria-pressed", String(Boolean(ask)));
      button.setAttribute("aria-label", ask ? "Ideate: on" : "Ideate: off");
      button.replaceChildren(icon("bulb"), ...(ask ? [h("span", {}, "Ideate")] : []));
      panel.hidden = !ask;
      if (!ask) return panel.replaceChildren();
      const change = (next) => { state.ideates.set(key, next); state.lastIdeate = next; paint(); };
      panel.replaceChildren(
        h("div", { class: "lenses", role: "group", "aria-label": "Lenses" }, LENSES.map(([id, name]) =>
          h("button", { type: "button", class: "fchip", "aria-pressed": String(ask.lenses.includes(id)), onclick: () => change(toggleLens(ask, id)) }, name))),
        h("div", { class: "limits", role: "group", "aria-label": "At most" }, h("span", {}, "At most"),
          IDEA_LIMITS.map((n) => h("button", { type: "button", class: "fchip", "aria-pressed": String(ask.max === n), onclick: () => change({ ...ask, max: n }) }, String(n))),
          h("span", {}, "ideas")),
      );
    };
    const set = (ask) => {
      ask ? state.ideates.set(key, ask) : state.ideates.delete(key);
      paint();
      onSet?.(ask);
    };
    button.addEventListener("click", () => set(state.ideates.has(key) ? null : state.lastIdeate));
    paint();
    return { button, panel, set };
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

  function visibleProjects() {
    return sortProjects(state.projects.filter((p) => !p.hidden));
  }

  // The New task sheet and a cleared worker's box are one composer, so a
  // change to how a task is written reaches both. sends makes a tap add the
  // task and send it, for a box whose worker is known to be free; otherwise
  // a tap only adds it and a long press also sends it when the worker is free.
  // A failed add puts the text, images and prototype kind back.
  function taskComposer({ key, id, cls, project, sends = false, lead = null, added, failed }) {
    const pnote = h("p", { class: "pnote", role: "status", hidden: "" });
    const c = { pnote };
    const placeholder = () => (state.ideates.has(key) ? "Focus, or leave empty for the whole repository…" : "Describe a task…");
    // Turning one kind on turns the other off.
    const onProto = (kind) => { if (kind && state.ideates.has(key)) c.ideate.set(null); };
    const onIdeate = (ask) => {
      if (ask && state.protos.has(key)) { state.protos.delete(key); repaint(); }
      const box = c.form?.querySelector("textarea");
      if (box) box.placeholder = placeholder();
    };
    c.proto = prototypeToggle(key, pnote, onProto);
    c.ideate = ideateToggle(key, onIdeate);
    const repaint = () => c.proto.replaceWith(c.proto = prototypeToggle(key, pnote, onProto));
    const add = (text, andSend) => {
      const value = text.value.trim();
      const name = project();
      const ask = state.ideates.get(key) ?? null;
      if ((!value && !ask) || !name || state.busy.has(key)) return;
      const attaching = draft(key);
      const kind = state.protos.get(key) ?? "";
      text.value = "";
      state.drafts.delete(key);
      state.protos.delete(key);
      repaint();
      if (ask) c.ideate.set(null);
      const ctx = added(name);
      addTask(name, ask ? ideateText(value) : value, attaching, kind, ask).then((t) => {
        forgetImages(attaching);
        if (andSend) send(t, hasConversation(t), { announce: !inboxSheet?.open });
      }, (err) => {
        const box = document.getElementById(id);
        if (box && !box.value) box.value = value;
        if (!draft(key).length) state.drafts.set(key, attaching);
        if (kind && !state.protos.has(key)) state.protos.set(key, kind);
        repaint();
        if (ask) c.ideate.set(ask);
        failed(err, name, ctx);
      });
    };
    const hold = sends ? null : {
      armed: (text) => Boolean((text.value.trim() || state.ideates.has(key)) && projectNamed(project())?.free),
      fire: (text) => add(text, true),
    };
    const submit = sends ? { submitLabel: "Add to inbox and send", submitIcon: "prompt" } : { submitLabel: "Add task", submitIcon: "addtask" };
    c.form = composerForm({ key, id, label: "Task description", placeholder: placeholder(), cls, ...submit,
      send: (text) => add(text, sends), hold, lead, tools: [c.proto, c.ideate.button] });
    c.form.append(c.ideate.panel, pnote);
    return c;
  }

  // The New task sheet takes the project it is opened with, or the one the
  // last task went to. It closes as the task is accepted; a failed add opens
  // it again with the text and images restored.
  function buildCompose() {
    const close = () => closeLayer("compose");
    const lead = h("span", { class: "pillslot" });
    const { form } = taskComposer({ key: "add", id: "task-text", cls: "add", project: () => state.picked, lead,
      added: (project) => {
        const from = compose.from;
        const shown = inboxSheet.open;
        state.lastPicked = project;
        if (shown && state.only && state.only !== project) state.only = project;
        close();
        if (!shown) addedTo(project);
        return from;
      },
      failed: (err, project, from) => {
        openCompose(project, from);
        composeAlert(`Could not add the task: ${err.message}`);
      } });
    const note = h("p", { class: "cnote", role: "status", hidden: "" });
    const alert = h("p", { class: "alert", role: "status" });
    const head = h("div", { class: "sheethead" }, h("h2", {}, "New task"),
      h("button", { type: "button", class: "icon", "aria-label": "Close", onclick: close }, icon("x")));
    const panel = h("section", { class: "sheet compose", role: "dialog", "aria-modal": "true", "aria-label": "New task", onkeydown: (e) => e.key === "Escape" && close() },
      sheetTop(head, close), alert, h("div", { class: "sheetfoot" }, form, note));
    const scrim = h("div", { class: "scrim", hidden: "", onclick: (e) => e.target === e.currentTarget && close() }, panel);
    document.body.append(scrim);
    return { scrim, form, lead, note, alert, text: form.querySelector("textarea"), open: false, from: null };
  }

  // from names the worker's project when the sheet opens on a worker's page,
  // so picking another project says where the task goes instead.
  function openCompose(project, from) {
    ensureSheets();
    state.picked = project ?? state.lastPicked ?? state.picked;
    compose.from = from;
    compose.alert.textContent = "";
    compose.open = true;
    compose.scrim.hidden = false;
    openLayer("compose");
    render();
    compose.text.focus();
  }

  function hideCompose() {
    compose.open = false;
    compose.scrim.hidden = true;
    compose.alert.textContent = "";
    if (!state.pickFor) state.picking = false;
    render();
  }

  // Only the project pill, the note and the thumbnails are redrawn; the
  // textarea stays where it is.
  function renderCompose() {
    syncThumbs(compose.form, "add");
    if (!compose.open) return;
    if (state.loaded.has("projects") && !state.projects.some((p) => p.name === state.picked)) state.picked = visibleProjects()[0]?.name ?? null;
    compose.lead.replaceChildren(projectPill(state.picked));
    compose.text.placeholder = state.picked ? "Describe a task…" : "No projects published yet";
    const elsewhere = Boolean(compose.from && state.picked && state.picked !== compose.from);
    compose.note.hidden = !elsewhere;
    compose.note.textContent = elsewhere ? `Goes to ${state.picked}, not ${compose.from}.` : "";
  }

  function syncThumbs(form, key) {
    const imgs = draft(key);
    form.querySelector(":scope > .thumbs")?.remove();
    if (imgs.length) form.prepend(h("div", { class: "thumbs" }, imgs.map((img, i) => draftThumb(key, img, i))));
    form.querySelector('[aria-label="Attach image"]').disabled = imgs.length >= MAX_IMAGES;
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
      setOutcome(`task:${t.id}`, `Could not delete: ${err.message}`, false, "bad");
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
      d.loading ? (d.tasks.length ? h("p", { class: "more pending", role: "status", "aria-label": "Loading more done tasks" }, "Loading…") : loading("Loading done tasks…"))
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
        onclick: () => sendCommand("resume", { session: p.session_id, conversation: t.conversation_id }, "Resume", `resume:${t.id}`, resumed(t), null, { done: null }),
      }, icon(r.clear ? "clearresume" : "resume")) : null,
      detail: [
        h("span", {}, `done ${ago(t.done_at, now)} ago${t.commit ? ` · ${t.commit}` : ""}${r?.current ? " · current conversation" : ""}`),
        t.ideate ? h("button", { type: "button", class: "link", onclick: () => openInbox(t.project) }, icon("bulb"), `${t.ideas ?? 0} idea${t.ideas === 1 ? "" : "s"}`) : null,
        t.prototype_url ? h("a", { class: "plink", href: t.prototype_url, target: "_blank", rel: "noopener noreferrer" },
          icon(PROTOTYPE_KINDS[t.prototype]?.icon ?? "proto"), "Open prototype") : null,
      ],
    });
  }

  // The inbox opens on ready tasks; one queued for a busy project would
  // vanish from that view as it is saved, so the view widens to show it.
  function reveal(project) {
    if (state.filter === "ready" && !projectNamed(project)?.free) state.filter = "all";
  }

  async function addTask(project, text, images, prototype = "", ideate = null) {
    reveal(project);
    const batch = fs.writeBatch(db);
    const ids = await storeImages(batch, images);
    const task = { project, text, created_at: fs.serverTimestamp(), status: "queued", rank: topRank(state.tasks, Date.now()) };
    if (ids.length) task.images = ids;
    if (prototype) task.prototype = prototype;
    if (ideate) task.ideate = { lenses: [...ideate.lenses], max: ideate.max };
    const ref = fs.doc(fs.collection(db, "tasks"));
    batch.set(ref, task);
    await batch.commit();
    return { ...task, id: ref.id };
  }

  // Images an edit drops go with it, unless another task shares one.
  async function saveTask(t, text, project, images, prototype) {
    const batch = fs.writeBatch(db);
    const ids = await storeImages(batch, images);
    batch.update(fs.doc(db, "tasks", t.id), { text, project, images: ids.length ? ids : fs.deleteField(), prototype: prototype || fs.deleteField() });
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
    if (files.length > room) attachNote(key, `At most ${MAX_IMAGES} images go with one message; the rest were left out.`, "warn");
    for (const file of files.slice(0, Math.max(0, room))) {
      try {
        const img = await prepareImage(file);
        imgs.push({ ...img, url: URL.createObjectURL(new Blob([img.bytes], { type: img.type })) });
      } catch (err) {
        attachNote(key, `Could not attach: ${err.message}`, "bad");
      }
    }
    render();
  }

  // A share from another app becomes the new task's draft. It waits for
  // sign-in, since only a signed-in page can store the task.
  async function receiveShare() {
    const shared = await takeShare();
    if (!shared) return;
    openCompose(null, null);
    const box = compose.text;
    const text = sharedText(shared);
    if (text) box.value = box.value ? `${box.value}\n${text}` : text;
    const all = shared.files || [];
    const files = all.filter((f) => f.type.startsWith("image/"));
    if (files.length) await attachImages("add", files);
    if (files.length < all.length) composeAlert("Only images can be attached; the other files were left out.", "warn");
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
    t.prototype ? state.protos.set(`edit:${t.id}`, t.prototype) : state.protos.delete(`edit:${t.id}`);
    render();
  }

  function editor(t, movable = true) {
    const key = `edit:${t.id}`;
    // Snapshots re-render the editor; the chosen project survives in state.
    const chosen = state.editProject ?? t.project;
    const close = () => {
      dropDraft(key);
      state.protos.delete(key);
      state.editing = null;
      state.editProject = undefined;
      render();
    };
    const save = async (text) => {
      const value = text.value.trim();
      if (!value) return;
      try {
        await track(key, () => saveTask(t, value, chosen, draft(key), state.protos.get(key) ?? ""));
        reveal(chosen);
        close();
      } catch (err) {
        setOutcome(`task:${t.id}`, `Could not save: ${err.code === "permission-denied" ? "the task was already sent" : err.message}`, false, "bad");
      }
    };
    const form = composerForm({ key, id: `edit-${t.id}`, label: "Task text", placeholder: "Describe a task…", rows: "3", cls: "edit",
      submitLabel: "Save", submitIcon: "check", send: save, lead: movable ? projectPill(chosen, t) : null, tools: t.ideate ? null : prototypeToggle(key, null),
      tail: h("button", { type: "button", class: "link", onclick: close }, "Cancel") });
    form.querySelector("textarea").value = t.text;
    const o = outcomeLine(`task:${t.id}`);
    if (o) form.append(o);
    return form;
  }

  // The New task sheet is never redrawn, so it reports in a line of its own.
  function composeAlert(text, tone = "bad") {
    compose.alert.textContent = text;
    toneOf(compose.alert, tone);
  }

  // Every other composer is redrawn, so it shows the outcome under its text.
  function attachNote(key, text, tone) {
    if (key === "add") return composeAlert(text, tone);
    setOutcome(`attach:${key}`, text, false, tone);
  }

  // A resume that went through closes the inbox sheet, as a sent task does;
  // the worker's new conversation is the result.
  function resumed(t) {
    return (text, busy, tone) => {
      setOutcome(`resume:${t.id}`, text, busy, tone);
      if (!busy && !text && inboxSheet?.open) closeLayer("inbox");
    };
  }

  // A failed check leaves the notifications row up with the error, so the
  // check can run again from there.
  function refreshPush() {
    return checkPush().then(() => setOutcome("notify", ""), (err) => {
      state.push = "failed";
      setOutcome("notify", `Cannot check notifications: ${err.message}`, false, "bad");
    });
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
      setOutcome("notify", "Notifications are on for this device.", false, "ok");
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
    return h("div", {}, h("h2", { class: "group" }, "Notification log"),
      h("p", { class: "hint" }, `Permission: ${"Notification" in window ? Notification.permission : "unsupported"} · subscribed here: ${state.push}`), list);
  }

  function board(selected = null) {
    const now = Date.now();
    const inboxCount = (s) => {
      const p = projectOf(s);
      return p ? queuedFor(p.name).length : 0;
    };
    const list = sortSessions([...state.sessions.keys()].map(shownWorker), inboxCount);
    // Turning notifications on keeps the row up until its result settles.
    const pushButton = state.push === "off" ? busyButton("notify", { type: "button", onclick: () => track("notify", turnOnPush) }, "Turn on notifications")
      : state.push === "failed" ? busyButton("notify", { type: "button", onclick: () => track("notify", refreshPush) }, "Check again")
        : null;
    const pushRow = pushButton || state.outcomes.has("notify")
      ? h("div", { class: "pushrow" }, pushButton ? h("span", {}, "Get notified when a worker finishes or needs you.") : null, pushButton, outcomeLine("notify"))
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
      const queued = inboxCount(s);
      const ideas = projectOf(s) ? ideaCount(state.ideas, projectOf(s).name) : 0;
      const line = s.question ? h("span", { class: "msg ask" }, `Asks: ${oneLine(s.question.items.map((q) => q.question).join(" · "))}`)
        : awaitingReply(s) ? h("span", { class: s.state === "working" ? "msg pending working" : "msg pending" }, pendingLabel(s))
        : s.cleared ? h("span", { class: "msg fresh" }, "New conversation")
        : s.message ? h("span", { class: "msg" }, oneLine(s.message.text)) : null;
      out.push(
        h("a", { class: s.notified ? "card notified" : "card", href: `#s-${encodeURIComponent(s.id)}`, "aria-current": s.id === selected ? "page" : null },
          h("span", { class: s.aoe_status === "stopped" ? "facewrap stopped" : "facewrap" }, face(projectOf(s), s.title, "md"), h("span", { class: `sdot ${st.tone}` })),
          h("span", { class: "cbody" },
            // The group heading names the state; only Needs you holds more than one.
            h("span", { class: "top" },
              h("span", { class: "name" }, s.title),
              st.group === "Needs you" ? chip(st) : null,
              h("span", { class: "tags" }, tagList(s, false), waitingLabel(s.id))),
            task ? h("span", { class: "curtask" }, `Task: ${oneLine(task.text)}`) : null,
            line,
          ),
          // Age and inbox sit on separate lines so a count never reads as part of the age.
          h("span", { class: "cmeta" },
            h("span", { class: "cage" },
              s.notified ? h("span", { class: "nbell", role: "img", "aria-label": "Notified" }, icon("bell")) : null,
              at ? h("span", { class: "when" }, ago(at, now)) : null),
            queued || ideas ? h("span", { class: "counts" },
              ideas ? h("span", { class: "qcount idea", role: "img", "aria-label": `${ideas} open idea${ideas === 1 ? "" : "s"}` }, icon("bulb"), String(ideas)) : null,
              queued ? h("span", { class: "qcount", role: "img", "aria-label": `Inbox, ${queued} queued` }, icon("tray"), String(queued)) : null) : null),
        ),
      );
    }
    return h("div", { class: "board" }, out);
  }

  // On a wide window the repository and the project's inbox sit beside the
  // conversation, in projectPane.
  function detail(s, isWide = false) {
    const now = Date.now();
    const st = STATES[s.state] || { label: s.state, tone: "idle" };
    const g = s.git;
    const busy = s.state === "working" || s.state === "needs_input";
    const task = currentTask(state.tasks, s.id);
    const p = projectOf(s);
    const next = p ? queuedFor(p.name).find((t) => t.ready) : null;
    const action = quickAction(s, Boolean(next));
    // Until the session's transcript records a sent reply, it shows as
    // pending, so the chat never presents it as part of the conversation.
    const unrecorded = s.prompt?.unrecorded;
    const sent = h("div", { class: !s.prompt ? "reply sent none" : unrecorded ? "reply sent queued" : "reply sent" });
    sent.innerHTML = s.prompt ? renderMarkdown(s.prompt.text) : "<p>Nothing sent yet.</p>";
    const you = () => h("span", { class: "who mine", ...(unrecorded ? { role: "status" } : {}) },
      unrecorded ? "Typed on the laptop · not in the conversation yet" : ["You", s.prompt ? ` · ${ago(s.prompt.at, now)}` : ""]);
    const pending = awaitingReply(s);
    const msg = h("div", { class: pending ? (s.state === "working" ? "reply pending working" : "reply pending") : "reply" });
    if (pending) msg.append(h("p", { role: "status" }, pendingLabel(s)));
    else msg.innerHTML = s.message ? renderMarkdown(s.message.text) : "<p>No reply yet.</p>";
    const whole = state.history?.id === s.id ? state.history : null;
    const convo = whole
      ? h("div", { class: "convo" }, historyView(s, whole, now),
        unrecorded ? [you(), sent] : null,
        pending ? msg : null)
      : h("div", { class: "convo" },
        s.prompt || s.message
          ? h("button", { type: "button", class: "link earlier", onclick: () => openHistory(s) }, icon("up"), "Whole conversation")
          : null,
        you(),
        sent,
        h("span", { class: "who" }, s.title, s.message && !pending ? ` · ${ago(s.message.at, now)}` : ""),
        msg,
      );
    return h("article", { class: "detail" },
      h("div", { class: "dhead" },
        h("div", { class: "drow" },
          h("span", { class: s.aoe_status === "stopped" ? "facewrap stopped" : "facewrap" }, face(projectOf(s), s.title, "lg"), h("span", { class: `sdot ${st.tone}` })),
          h("div", { class: "dtitle" },
            h("h2", {}, s.title, chip(st)),
            h("div", { class: "tags" }, modelChip(s), tagList(s, true))),
          quickButton(s, action),
          workerMenu(s, busy)),
        outcomeLine(`worker:${s.id}`),
      ),
      next ? nowAndNext(task, next, now)
        : task ? h("p", { class: "curtask" }, `Task: ${task.text}`, h("span", { class: "when" }, ` · sent ${ago(task.dispatched_at, now)} ago`)) : null,
      !isWide && g && g.unpushed > 0 ? pushCard(s, now) : null,
      s.prototypes && s.prototypes.length ? prototypes(s.prototypes, now) : null,
      convo,
      waitingReplyList(s),
      s.question ? questionCard(s) : null,
      isWide ? null : facts(s, now),
      replyBox(s),
    );
  }

  // A render rebuilds the details, so their open state outlives it here.
  function facts(s, now) {
    const g = s.git;
    const rows = [["aoe", s.aoe_status], ["path", s.path]];
    if (!g) rows.push(["repository", "none"]);
    if (g) {
      rows.push(["branch", g.upstream ? `${g.branch} → ${g.upstream}` : g.branch || "detached"]);
      if (g.last) rows.push(["commit", `${g.last.hash} ${g.last.subject} (${ago(g.last.at, now)})`]);
      rows.push(["push", pushLabel(g)]);
      rows.push(["tree", g.dirty ? `${g.dirty} changed files` : "clean"]);
    }
    return h("details", { class: "facts", open: state.factsOpen ? "" : null, ontoggle: (e) => { state.factsOpen = e.currentTarget.open; } }, h("summary", {}, "Details"),
      h("dl", {}, rows.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)])));
  }

  // The shown worker's repository and its project's tasks, ideas and sent
  // tasks, beside the conversation on a wide window.
  function projectPane(s) {
    const now = Date.now();
    const g = s.git;
    const p = projectOf(s);
    const out = [g && g.unpushed > 0 ? pushCard(s, now) : null];
    if (!p) return [...out, facts(s, now)].filter(Boolean);
    const list = queuedFor(p.name);
    const ideas = shownIdeas(p.name);
    const sent = sentTasks(state.tasks).filter((t) => t.project === p.name);
    out.push(
      h("div", { class: "group panehead" }, h("span", { class: "grow" }, "Inbox"), addButton(p.name, p.name)),
      h("div", { class: "tasks" }, !state.loaded.has("tasks") ? loading("Loading tasks…") : list.length
        ? list.map((t, i) => (state.editing === t.id ? editor(t) : taskCard(t, now, { inProject: true, order: { list, i } })))
        : h("p", { class: "empty" }, "Nothing queued.")),
      ideas.length ? [h("h2", { class: "group" }, "Ideas", h("span", { class: "n" }, ` ${ideas.length}`)),
        h("div", { class: "tasks" }, ideas.map((i) => ideaCard(i, now, true)))] : null,
      sent.length ? [h("h2", { class: "group" }, "Sent", h("span", { class: "n" }, ` ${sent.length}`)),
        h("div", { class: "rows" }, sent.map((t) => sentRow(t, now)))] : null,
      doneFold(state.sideDone, () => { state.sideDone = !state.sideDone; render(); }, p.name),
      state.sideDone ? doneList(p.name, now, true) : null,
      facts(s, now),
    );
    return out.flat().filter(Boolean);
  }

  // A worker's whole conversation is read from the laptop only while it is on
  // screen: opening it asks the laptop to publish it, each new prompt or reply
  // asks again, and leaving the worker or the app deletes it.
  function openHistory(s) {
    closeHistory();
    const sc = scrollHost();
    const whole = { id: s.id, latest: s.latest, data: null, nodes: null, unsub: null, fromBottom: sc.el.scrollHeight - sc.top() };
    state.history = whole;
    whole.unsub = fs.onSnapshot(fs.doc(db, "history", s.id), (snap) => {
      if (state.history !== whole) return;
      whole.data = snap.exists() ? snap.data() : null;
      whole.nodes = null;
      render();
    }, (err) => failHistory(whole, `Cannot read the conversation: ${err.message}`));
    requestHistory(whole);
    render();
  }

  // The earlier messages open above the latest ones, so until the laptop's
  // first copy is drawn the page keeps its distance from the bottom instead
  // of its distance from the top.
  function keepHistoryAnchor() {
    const whole = state.history;
    if (whole?.fromBottom == null) return;
    const sc = scrollHost();
    sc.to({ top: sc.el.scrollHeight - whole.fromBottom, behavior: "instant" });
    if (whole.data?.served_at) whole.fromBottom = null;
  }

  function requestHistory(whole) {
    fs.setDoc(fs.doc(db, "history", whole.id), { requested_at: fs.serverTimestamp() }, { merge: true })
      .catch((err) => failHistory(whole, `Cannot ask the laptop for the conversation: ${err.message}`));
  }

  function failHistory(whole, text) {
    if (state.history !== whole) return;
    whole.error = text;
    render();
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
    if (again) scrollHost().by({ top: again.getBoundingClientRect().top - top, behavior: "instant" });
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
    if (whole.error) {
      return [bar, h("p", { class: "outcome", role: "status", "data-tone": "bad" },
        h("span", { class: "grow" }, whole.error),
        h("button", { type: "button", class: "link", onclick: () => openHistory(s) }, "Try again"))];
    }
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

  // The model chip names the worker's model and opens the sheet of models to
  // switch to. Its dot is the tool: Claude Code or Antigravity.
  function modelChip(s) {
    const name = shownModel(s).name || (s.tool === "antigravity" ? "Antigravity" : "Model");
    const busy = state.busy.has(`model:${s.id}`);
    return h("button", { type: "button", class: `modelchip ${s.tool === "antigravity" ? "ag" : "cc"}${busy ? " busy" : ""}`, "aria-label": `Model: ${name}. Change model`, "aria-haspopup": "dialog", "aria-busy": busy ? "true" : null,
      onclick: () => { state.modelFor = s.id; render(); } },
    h("span", { class: "mdot", "aria-hidden": "true" }), h("span", { class: "mlabel" }, name));
  }

  // Every model a worker can run, as the sheet offers them: Claude Code's
  // aliases, then Antigravity's models by the pool their quota is in.
  function modelChoices() {
    const versions = modelVersions(state.sessions.values());
    const cc = MODEL_POOLS.map((p) => ({ ...p, tool: "", app: "Claude Code", title: "Claude Code",
      models: p.models.map((m) => ({ key: m.alias, name: versions[m.alias] ?? m.name, pips: m.pips, tool: "", claude: true })) }));
    const ag = antigravityPools(state.models?.antigravity).map((p) => ({ ...p, kind: "ag", tool: "antigravity", app: "Antigravity", title: p.name,
      models: p.models.map((m) => ({ key: m.id, name: m.name, short: m.short, pips: m.pips, more: m.more, tool: "antigravity", claude: /^claude-/.test(m.id) })) }));
    return [...cc, ...ag];
  }

  // A switch asked for shows as the model asked for until the worker reports
  // it; the worker's own model is the truth after that, or if the switch fails.
  function shownModel(s) {
    const asked = state.modelAsked.get(s.id);
    const actual = sessionModel(s, state.models?.antigravity, modelVersions(state.sessions.values()));
    if (asked && asked.key !== actual.key) return asked;
    if (asked) state.modelAsked.delete(s.id);
    return actual;
  }

  // The sheet lists each pool's models with what a task costs of its quota,
  // and the quota's windows beside the pool's name. A tap switches; a model
  // of the other tool moves the worker there.
  function modelSheet(s) {
    const close = () => { state.modelFor = null; render(); };
    const now = Date.now();
    const current = shownModel(s).key;
    const claude = usageLeft(state.usage, now);
    const ag = antigravityLeft(state.usage, now) ?? [];
    const pick = (m) => {
      state.modelFor = null;
      if (m.key === current) return render();
      const move = m.tool !== (s.tool ?? "");
      state.modelAsked.set(s.id, m);
      if (move) state.moving.set(s.id, s.title);
      sendCommand("model", { session: s.id, text: m.key }, `${move ? "Move" : "Switch"} to ${m.name}`, `model:${s.id}`, (text, busy, tone) => {
        if (tone === "bad") { state.modelAsked.delete(s.id); state.moving.delete(s.id); }
        setOutcome(`worker:${s.id}`, text, busy, tone);
      }, null, { done: `${move ? "Moved" : "Switched"} to ${m.name}` });
    };
    const quota = (pool) => {
      const windows = pool.tool === "" ? claude : ag.find((p) => p.name === pool.name)?.windows;
      return windows ? h("span", { class: "mquota", "aria-hidden": "true" },
        windows.map((w) => meter(h("span", { class: `meter ${pool.kind}` }, h("i")), w.left))) : null;
    };
    const row = (pool, m) => h("button", { type: "button", class: `mrow ${pool.kind}${m.claude ? " claude" : ""}`, "aria-pressed": String(m.key === current),
      "aria-label": `${m.name}${pool.tool ? " in Antigravity" : ""}${m.pips ? `, uses ${m.pips} of 5 of the quota per task` : ""}`, onclick: () => pick(m) },
    h("span", { class: "mdot", "aria-hidden": "true" }),
    h("span", { class: "mname" }, m.short ?? m.name),
    h("span", { class: "pips", "aria-hidden": "true" }, m.pips ? [1, 2, 3, 4, 5].map((i) => h("i", { class: i <= m.pips ? "on" : null })) : null),
    h("span", { class: "mcheck", "aria-hidden": "true" }, icon("check")));
    // A pool's models past its main ones fold under More, unless the worker
    // runs one of them.
    const body = modelChoices().map((pool) => {
      const shown = pool.models.filter((m) => !m.more || m.key === current);
      const more = pool.models.filter((m) => !shown.includes(m));
      const open = state.modelMore.has(pool.id);
      return h("div", { class: "mpool", role: "group", "aria-label": pool.tool ? `${pool.app} ${pool.name}` : pool.app },
        h("div", { class: "mhead" }, h("span", { class: "grow mtitle" }, logo(pool.kind), pool.title), quota(pool)),
        shown.map((m) => row(pool, m)),
        more.length ? h("button", { type: "button", class: "mmore", "aria-expanded": String(open),
          onclick: () => { if (open) state.modelMore.delete(pool.id); else state.modelMore.add(pool.id); render(); } },
        `More · ${more.length}`, icon("chevron")) : null,
        open ? more.map((m) => row(pool, m)) : null);
    });
    return h("div", { class: "scrim", onclick: (e) => e.target === e.currentTarget && close() },
      h("section", { class: "sheet msheet", role: "dialog", "aria-modal": "true", "aria-label": `Model for ${s.title}`, onkeydown: (e) => e.key === "Escape" && close() },
        sheetTop(h("div", { class: "sheethead" }, h("h2", {}, "Model")), close),
        h("div", { class: "sheetbody" }, body)));
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
      h("button", { type: "button", class: pending ? "icon tool busy" : "icon tool", "aria-label": "Worker actions", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-busy": pending ? "true" : null,
        onclick: () => { state.menu = open ? null : key; render(); } }, icon("more")),
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

  // Only open ideas are listened to, so an added one is kept here to hold
  // its place while its result is on screen.
  function shownIdeas(project) {
    const open = new Set(state.ideas.map((i) => i.id));
    const settled = [...state.settledIdeas.values()].filter((i) => !open.has(i.id) && state.outcomes.has(`idea:${i.id}`)).map((i) => ({ ...i, settled: true }));
    return openIdeas([...state.ideas, ...settled], project);
  }

  // An idea is added to its project's inbox the way a composer adds a task:
  // a tap adds it, and a long press also sends it when the worker is free.
  function ideaCard(i, now, inProject) {
    const key = `idea:${i.id}`;
    if (i.settled) {
      return h("article", { class: "task idea settled" },
        h("div", { class: "thead" },
          inProject ? null : [face(projectNamed(i.project), i.project, "sm"), h("span", { class: "proj" }, i.project)],
          h("span", { class: "tag ideas" }, lensName(i.lens))),
        h("div", { class: "ttext ititle" }, i.title),
        outcomeLine(key));
    }
    const open = state.menu === key;
    const dismissing = state.dismissing === i.id;
    const note = dismissing ? h("input", { type: "text", class: "dnote", maxlength: "500", "aria-label": "Why dismiss it (optional)", placeholder: "Why, for later runs (optional)" }) : null;
    const p = projectNamed(i.project);
    const hold = longPress(() => Boolean(projectNamed(i.project)?.free), () => addIdea(i, true));
    // The long press swallows the click that ends a hold; any other click adds.
    const onclick = (e) => { hold.onclick(e); if (!e.defaultPrevented) addIdea(i, false); };
    return h("article", { class: "task idea" },
      h("div", { class: "thead" },
        inProject ? null : [face(projectNamed(i.project), i.project, "sm"), h("span", { class: "proj" }, i.project)],
        h("span", { class: "tag ideas" }, lensName(i.lens)),
        h("span", { class: "when" }, ago(i.created_at, now)),
        h("span", { class: "grow" }),
        h("div", { class: "wmenu" },
          h("button", { type: "button", class: "icon", "aria-label": `More actions: ${i.title}`, "aria-haspopup": "menu", "aria-expanded": String(open),
            onclick: () => { state.menu = open ? null : key; render(); } }, icon("more")),
          open ? h("div", { class: "pop", role: "menu", onkeydown: (e) => { if (e.key === "Escape") { state.menu = null; render(); } } },
            h("button", { type: "button", role: "menuitem", class: "danger", onclick: () => { state.menu = null; state.dismissing = i.id; render(); document.querySelector(".dnote")?.focus(); } }, icon("x"), "Dismiss")) : null)),
      h("div", { class: "ttext ititle" }, i.title),
      i.why ? h("div", { class: "iwhy" }, i.why) : null,
      i.refs?.length ? h("div", { class: "irefs" }, i.refs.map((r) => h("code", {}, r))) : null,
      h("details", { class: "iprompt" }, h("summary", {}, "Task"), h("div", { class: "ttext" }, i.prompt)),
      dismissing ? h("form", { class: "dismiss", onsubmit: (e) => { e.preventDefault(); dismissIdea(i, note.value.trim()); } },
        note,
        h("button", { type: "button", class: "link", onclick: () => { state.dismissing = null; render(); } }, "Cancel"),
        busyButton(`dismiss:${i.id}`, { type: "submit" }, "Dismiss")) : null,
      h("div", { class: "tfoot" },
        h("span", { class: "tstatus" }, !p ? "" : p.free ? (p.session_id ? "Free" : "Starts a new worker") : `Waiting · ${p.busy}`),
        busyButton(key, { type: "button", class: "round go", "aria-label": "Add task", title: "Add task", ...hold, onclick },
          icon("addtask"), heldIcon())),
      outcomeLine(key),
    );
  }

  async function addIdea(i, andSend) {
    const key = `idea:${i.id}`;
    if (state.busy.has(key)) return;
    let t;
    try {
      t = await track(key, async () => {
        reveal(i.project);
        const batch = fs.writeBatch(db);
        const task = { project: i.project, text: i.prompt, created_at: fs.serverTimestamp(), status: "queued", rank: topRank(state.tasks, Date.now()) };
        const ref = fs.doc(fs.collection(db, "tasks"));
        batch.set(ref, task);
        batch.update(fs.doc(db, "ideas", i.id), { status: "added", added_task: ref.id });
        await batch.commit();
        return { ...task, id: ref.id };
      });
    } catch (err) {
      return setOutcome(key, `Could not add the idea: ${err.message}`, false, "bad");
    }
    state.settledIdeas.set(i.id, i);
    if (andSend) send(t, hasConversation(t), { also: (text, busy, tone) => setOutcome(key, text, busy, tone) });
    else setOutcome(key, `Added to ${i.project}'s inbox`, false, "ok");
    addedTo(i.project);
  }

  async function dismissIdea(i, note) {
    try {
      await track(`dismiss:${i.id}`, () => fs.updateDoc(fs.doc(db, "ideas", i.id), note ? { status: "dismissed", note } : { status: "dismissed" }));
      state.dismissing = null;
      render();
    } catch (err) {
      setOutcome(`idea:${i.id}`, `Could not dismiss the idea: ${err.message}`, false, "bad");
    }
  }

  function queuedFor(project) {
    return queue(state.tasks, state.projects).filter((t) => t.project === project);
  }

  // Sending the next task is what ends the current one, so the next task sits
  // above it with the inbox's send button, and its text opens the inbox. A
  // stopped worker is started by the send.
  function nowAndNext(task, next, now) {
    const sent = state.sends.get(next.id);
    const clears = hasConversation(next);
    return h("div", { class: "nownext" },
      h("div", { class: "nnrow" },
        h("button", { type: "button", class: "nntext", "aria-label": `Next task, open the inbox: ${next.text}`, onclick: () => openInbox(next.project) },
          sent ? h("span", { class: "nnlabel", role: "status", "data-tone": sent.tone || null }, sent.text) : h("span", { class: "nnlabel" }, "Next"),
          h("span", { class: "nnone" }, oneLine(next.text))),
        sendButton(next, clears, () => send(next, clears))),
      task ? h("div", { class: "nnrow" },
        h("div", { class: "nntext" }, h("span", { class: "nnlabel" }, `Task · sent ${ago(task.dispatched_at, now)} ago`), h("span", {}, task.text))) : null);
  }

  // The quick action shares the menu's busy keys, so either shows the other's
  // command as pending. Clearing for the next task is offered beside that task.
  function quickButton(s, action) {
    if (!action || action === "clearnext") return null;
    const [cmd, ico, label, fields] = {
      start: ["start", "play", "Start", {}],
      stop: ["stop", "stop", "Stop", {}],
      clearstop: ["stop", "clearstop", "Clear and stop", { clear: true }],
    }[action];
    return busyButton(`${cmd}:${s.id}`, { type: "button", class: "icon tool", title: label, "aria-label": label,
      onclick: () => workerCommand(s, cmd, label, fields) },
    icon(ico));
  }

  function addButton(project, from) {
    return h("button", { type: "button", class: "icon tool", title: "New task", "aria-label": "New task", "aria-haspopup": "dialog",
      onclick: () => openCompose(project, from) }, icon("plus"));
  }

  // The workers' tray counts the tasks ready to send; a worker's counts its
  // project's queue and open ideas, as the worker's card does.
  function inboxButton(p) {
    const n = p ? queuedFor(p.name).length : readyCount();
    const ideas = p ? ideaCount(state.ideas, p.name) : 0;
    const added = state.added && (!p || p.name === state.added.project) ? state.added.project : null;
    const label = ["Inbox", n ? (p ? `${n} queued` : `${n} ready`) : null, ideas ? `${ideas} idea${ideas === 1 ? "" : "s"}` : null,
      added ? `added to ${added}` : null].filter(Boolean).join(", ");
    return h("button", { type: "button", class: `icon tool inboxbtn${added ? " added" : ""}`, title: "Inbox", "aria-label": label, "aria-haspopup": "dialog",
      onclick: () => openInbox(p?.name ?? null) },
    icon("tray"), added ? h("span", { class: "addedto" }, p ? "+1" : `${added} +1`) : n ? h("span", { class: "count" }, String(n)) : null,
    ideas ? h("span", { class: "ideas" }, icon("bulb"), h("span", { class: "icount" }, String(ideas))) : null);
  }

  // The sheets live outside #app, stacked in the order they are made.
  let inboxSheet = null;
  let compose = null;
  let pickerHost = null;

  function ensureSheets() {
    if (inboxSheet) return;
    inboxSheet = buildInbox();
    compose = buildCompose();
    pickerHost = document.body.appendChild(h("div"));
  }

  // Each open sheet has a history entry of its own, so the phone's back
  // gesture closes the sheet rather than leaving the page.
  const layers = [];

  function openLayer(name) {
    if (ROUTES.includes(location.hash)) history.replaceState(history.state, "", location.pathname + location.search);
    if (layers.includes(name)) return;
    layers.push(name);
    history.pushState({ layers: layers.length }, "");
  }

  function closeLayer(name) {
    const i = layers.indexOf(name);
    if (i === -1) return;
    if (i === layers.length - 1 && history.state?.layers === layers.length) return history.back();
    layers.splice(i, 1);
    hideLayer(name);
  }

  // Going back, or following a link out of a sheet, leaves the sheets above
  // the entry the page is now on.
  // A route can open a sheet while one closes, so the depth is read again
  // after each.
  function syncLayers() {
    while (layers.length > (history.state?.layers ?? 0)) hideLayer(layers.pop());
  }

  function hideLayer(name) {
    if (name === "inbox") hideInbox();
    else hideCompose();
  }

  function buildInbox() {
    const close = () => closeLayer("inbox");
    const head = h("div", { class: "sheethead" });
    const body = h("div", { class: "sheetbody inboxbody" });
    const panel = h("section", { class: "sheet", role: "dialog", "aria-modal": "true", "aria-label": "Inbox", onkeydown: (e) => e.key === "Escape" && close() },
      sheetTop(head, close), body);
    const scrim = h("div", { class: "scrim", hidden: "", onclick: (e) => e.target === e.currentTarget && close() }, panel);
    document.body.append(scrim);
    return { scrim, panel, head, body, open: false, from: null };
  }

  // The workers' tray opens every project's tasks, a worker's its own
  // project's. A task sent from a worker's own inbox closes it, since the
  // worker's page shows the task from then on.
  function openInbox(project) {
    if (wide()) {
      Object.assign(state, { only: project, filter: project ? "all" : "ready", menu: null });
      if (location.hash && location.hash !== "#") location.hash = "";
      else render();
      return;
    }
    ensureSheets();
    Object.assign(state, { only: project, filter: project ? "all" : "ready", menu: null, sheetSent: null });
    inboxSheet.from = project && shownSession() ? project : null;
    inboxSheet.open = true;
    inboxSheet.scrim.hidden = false;
    openLayer("inbox");
    render();
  }

  function hideInbox() {
    inboxSheet.open = false;
    inboxSheet.scrim.hidden = true;
    Object.assign(state, { menu: null, sheetSent: null });
    if (state.editing && inboxSheet.body.contains(document.getElementById(`edit-${state.editing}`))) {
      dropDraft(`edit:${state.editing}`);
      state.editing = null;
    }
    render();
  }

  function sheetSend(t, clear) {
    state.sheetSent = t.id;
    send(t, clear);
  }

  function renderInbox() {
    const sh = inboxSheet;
    if (!sh.open) return;
    const all = queue(state.tasks, state.projects);
    if (state.sheetSent && !all.some((t) => t.id === state.sheetSent)) {
      state.sheetSent = null;
      return closeLayer("inbox");
    }
    sh.panel.setAttribute("aria-label", state.only ? `${state.only} inbox` : "Inbox");
    sh.head.replaceChildren(...[
      h("h2", { class: "grow" }, "Inbox"),
      addButton(state.only, sh.from),
      h("button", { type: "button", class: "icon", "aria-label": "Close", onclick: () => closeLayer("inbox") }, icon("x")),
    ].filter(Boolean));
    sh.body.replaceChildren(...inboxNodes(all, sh.from && sh.from === state.only ? sheetSend : send));
  }

  // A wide window's workers page is the inbox, since the workers are listed
  // beside it.
  function inboxPane() {
    const all = queue(state.tasks, state.projects);
    return h("section", { class: "inboxpane", "aria-label": "Inbox" },
      h("div", { class: "sheethead" }, h("h2", { class: "grow" }, "Inbox"), addButton(state.only, null)),
      inboxNodes(all, send));
  }

  function inboxNodes(all, sendWith) {
    const only = state.only;
    const inProject = Boolean(only);
    const counts = queueCounts(only ? all.filter((t) => t.project === only) : all);
    const list = filterQueue(all, state.filter, only);
    const now = Date.now();

    const chips = [["all", "All"], ["ready", "Ready"], ["waiting", "Waiting"]].map(([key, label]) =>
      h("button", { type: "button", class: "fchip", "aria-pressed": String(state.filter === key), onclick: () => { state.filter = key; render(); } },
        label, h("span", { class: "n" }, String(counts[key]))));
    const names = [...new Set([...all.map((t) => t.project), ...(only ? [only] : [])])];
    const strip = names.length > 1 || only
      ? h("div", { class: "strip", role: "group", "aria-label": "Filter by project" }, names.map((n) =>
        h("button", { type: "button", class: "sface", title: n, "aria-label": n, "aria-pressed": String(only === n),
          onclick: () => { state.only = only === n ? null : n; render(); } }, face(projectNamed(n), n, "md"))))
      : null;
    const items = !state.loaded.has("tasks") ? loading("Loading tasks…") : list.length
      ? list.map((t, i) => (state.editing === t.id ? editor(t)
        : taskCard(t, now, { inProject, sendWith, order: { list, i } })))
      : h("p", { class: "empty" }, only && !counts.all ? "Nothing queued." : all.length ? "Nothing here for this filter." : "No tasks queued.");
    const sent = sentTasks(state.tasks).filter((t) => !only || t.project === only);
    const rest = state.filter !== "waiting";
    const ideas = shownIdeas(only);
    return [
      h("div", { class: "filters" }, chips),
      strip,
      h("div", { class: "tasks" }, items),
      ...(ideas.length && rest ? [
        h("h2", { class: "group" }, "Ideas", h("span", { class: "n" }, ` ${ideas.length}`)),
        h("div", { class: "tasks" }, ideas.map((i) => ideaCard(i, now, inProject))),
      ] : []),
      ...(sent.length && rest ? [
        h("h2", { class: "group" }, "Sent", h("span", { class: "n" }, ` ${sent.length}`)),
        h("div", { class: "rows" }, sent.map((t) => sentRow(t, now))),
      ] : []),
      ...(rest ? [
        doneFold(state.showDone, () => { state.showDone = !state.showDone; render(); }, only ?? ""),
        ...(state.showDone ? doneList(only ?? "", now, inProject) : []),
      ] : []),
    ].filter(Boolean);
  }

  function pushCard(s, now) {
    const g = s.git;
    const more = g.unpushed - (g.pending?.length ?? 0);
    return h("section", { class: "pushcard" },
      h("div", { class: "phead" },
        h("h3", {}, `Unpushed commits · ${g.unpushed}`),
        h("span", { class: "grow" }),
        busyButton(`push:${s.id}`, { type: "button", class: "primary ok push", title: "Push", "aria-label": `Push ${g.unpushed} commit${g.unpushed === 1 ? "" : "s"}`,
          onclick: () => sendCommand("push", { session: s.id }, "Push", `push:${s.id}`, undefined, null, { done: "Pushed" }) },
        icon("push"), h("span", {}, String(g.unpushed)))),
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
    const cur = state.sessions.get(s.id);
    const after = cur?.prompt?.id ?? "";
    const conversation = cur?.conversation ?? "";
    return () => {
      state.delivered.set(s.id, { text, images: images.length, at: new Date(), after, conversation });
      render();
    };
  }

  function replyBox(s) {
    const p = projectOf(s);
    if (s.cleared && p?.free && p.session_id === s.id) return taskBox(s, p);
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
        const sc = scrollHost();
        sc.to({ top: sc.el.scrollHeight, behavior: "smooth" });
      } else {
        dropUnused([{ images: ids }]);
      }
    };
    const form = composerForm({ key, id: `reply-${s.id}`, label: "Reply", placeholder: `Reply to ${s.title}…`, rows: "1", cls: "respond", submitLabel: "Send reply", submitIcon: "prompt", send: reply });
    const o = outcomeLine(key);
    if (o) form.prepend(o);
    return form;
  }

  // A free worker with no conversation has nothing to reply to, so what is
  // written there becomes a task that is sent at once and recorded in the
  // inbox like any other task.
  function taskBox(s, p) {
    const key = `reply:${s.id}`;
    const { form } = taskComposer({ key, id: `reply-${s.id}`, cls: "respond", project: () => p.name, sends: true,
      added: () => render(),
      failed: (err) => setOutcome(key, `Not added: ${err.message}`, false, "bad") });
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

const PROTOTYPE_NOTES = {
  html: { label: "HTML page", note: "The worker publishes an HTML page and leaves the repository unchanged." },
  design: { label: "Design canvas", note: "The worker publishes a Design canvas and leaves the repository unchanged." },
  any: { label: "Claude picks the type", note: "The worker publishes an HTML page or a Design canvas, whichever fits, and leaves the repository unchanged." },
};

function prototypeTag(kind) {
  const k = PROTOTYPE_KINDS[kind];
  if (!k) return null;
  const label = `Prototype: ${PROTOTYPE_NOTES[kind].label}`;
  return h("span", { class: `tag ${k.icon} ${k.tone}`, title: label, "aria-label": label }, icon(k.icon), k.name);
}

function ideateTag(ask) {
  const label = `Ideas: ${ask.lenses.map(lensName).join(", ")}`;
  return h("span", { class: "tag ideas", title: label, "aria-label": label }, icon("bulb"), "Ideas");
}

function prototypes(list, now) {
  return h("ul", { class: "protos" },
    list.map((p) => h("li", {},
      h("a", { href: p.url, target: "_blank", rel: "noopener noreferrer", class: `k-${prototypeKind(p.kind)}` }, icon(PROTOTYPE_KINDS[prototypeKind(p.kind)].icon), h("span", { class: "grow" }, p.title || p.url)),
      h("span", { class: "when" }, ago(p.at, now)),
    )),
  );
}

function loading(text) {
  return h("p", { class: "notice loading", role: "status" }, text);
}

// A worker's tags, the same on its card and its page. On the card the whole
// card is the link, so only the page links a tag to what it describes.
function tagList(s, linked) {
  return tags(s).map((t) => {
    const attrs = { class: `tag ${t.kind} ${t.tone}`.trim(), "aria-label": t.label, title: t.label };
    const body = [t.tone === "run" ? null : icon(t.kind), t.text || null];
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
  plus: '<path d="M12 5v14M5 12h14"/>',
  addtask: '<path d="M4 6h11M4 12h11M4 18h7"/><path d="M18 14.5v7M14.5 18h7"/>',
  up: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  down: '<path d="M12 5v14M5 12l7 7 7-7"/>',
  chevron: '<path d="M6 9l6 6 6-6"/>',
  chevup: '<path d="M6 15l6-6 6 6"/>',
  clip: '<path d="M21 11.5l-8.6 8.6a5 5 0 01-7.1-7.1l8.6-8.6a3.4 3.4 0 014.8 4.8l-8.6 8.6a1.7 1.7 0 01-2.4-2.4l7.9-7.9"/>',
  tray: '<path d="M4 13l2.5-7h11L20 13v6H4z"/><path d="M4 13h4.5l1 2h5l1-2H20"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
  x: '<path d="M6 6l12 12M18 6L6 18"/>',
  bulb: '<path d="M9 18h6M10 21h4"/><path d="M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2V16h5v-.1c0-.8.4-1.5 1-2A6 6 0 0 0 12 3z"/>',
  bell: '<path d="M6 16v-5a6 6 0 0 1 12 0v5l1.5 2h-15z"/><path d="M10 20.5a2 2 0 0 0 4 0"/>',
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
  "ci-success": '<circle cx="12" cy="12" r="8.5"/><path d="M8.5 12.3l2.4 2.4 4.6-5"/>',
  "ci-failure": '<circle cx="12" cy="12" r="8.5"/><path d="M9.2 9.2l5.6 5.6M14.8 9.2l-5.6 5.6"/>',
  "ci-cancelled": '<circle cx="12" cy="12" r="8.5"/><path d="M8.5 12h7"/>',
  // A published page is a browser window; its kind is drawn inside.
  proto: '<g stroke-width="1.75"><rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="M3 8.5h18"/></g>',
  "proto-html": '<g stroke-width="1.75"><rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="M3 8.5h18"/><path d="M9.5 11.5l-2.5 2.75 2.5 2.75M14.5 11.5l2.5 2.75-2.5 2.75"/></g>',
  "proto-design": '<g stroke-width="1.75"><rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="M3 8.5h18"/><circle cx="9.25" cy="14.25" r="2"/><path d="M13.25 16.25l2.25-4 2.25 4z"/></g>',
  "proto-any": '<g stroke-width="1.75"><rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="M3 8.5h18"/><path d="M12 11.25c.36 1.74.9 2.28 3 3c-2.1.72-2.64 1.26-3 3c-.36-1.74-.9-2.28-3-3c2.1-.72 2.64-1.26 3-3z"/></g>',
};

// logo is a tool's mark beside its name, so it is decoration: the name says
// it. GitHub's mark takes the text color, which keeps it in dark mode.
const GITHUB_MARK = '<svg viewBox="0 0 98 96"><path fill="currentColor" fill-rule="evenodd" d="M48.854 0C21.839 0 0 22 0 49.217c0 21.756 13.993 40.172 33.405 46.69 2.427.49 3.316-1.059 3.316-2.362 0-1.141-.08-5.052-.08-9.127-13.59 2.934-16.42-5.867-16.42-5.867-2.184-5.704-5.42-7.17-5.42-7.17-4.448-3.015.324-3.015.324-3.015 4.934.326 7.523 5.052 7.523 5.052 4.367 7.496 11.404 5.378 14.235 4.074.404-3.178 1.699-5.378 3.074-6.6-10.839-1.141-22.243-5.378-22.243-24.283 0-5.378 1.94-9.778 5.014-13.2-.485-1.222-2.184-6.275.486-13.038 0 0 4.125-1.304 13.426 5.052a46.97 46.97 0 0 1 12.214-1.63c4.125 0 8.33.571 12.213 1.63 9.302-6.356 13.427-5.052 13.427-5.052 2.67 6.763.97 11.816.485 13.038 3.155 3.422 5.015 7.822 5.015 13.2 0 18.905-11.404 23.06-22.324 24.283 1.78 1.548 3.316 4.481 3.316 9.126 0 6.6-.08 11.897-.08 13.526 0 1.304.89 2.853 3.316 2.364 19.412-6.52 33.405-24.935 33.405-46.691C97.707 22 75.788 0 48.854 0z"/></svg>';

function logo(kind) {
  if (kind === "gh") {
    const span = h("span", { class: "logo gh", "aria-hidden": "true" });
    span.innerHTML = GITHUB_MARK;
    return span;
  }
  return h("img", { class: `logo ${kind}`, src: kind === "cc" ? "/static/logos/claude.svg" : "/static/logos/antigravity.png", alt: "", width: "20", height: "20" });
}

function icon(name) {
  const span = document.createElement("span");
  span.className = `ico ${name}`;
  span.setAttribute("aria-hidden", "true");
  span.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${ICONS[name]}</svg>`;
  return span;
}
