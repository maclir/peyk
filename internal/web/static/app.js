import { renderMarkdown } from "./markdown.js";
import { STATES, ago, answerText, currentTask, doneTasks, filterQueue, flags, laptopOnline, monogram, pushLabel, queue, queueCounts, rankAt, searchProjects, sortProjects, sortSessions } from "./board.js";
import { MAX_IMAGES, imageID, orphanedImages, prepareImage } from "./images.js";
import * as passkey from "./passkey.js";

// Registration is unconditional: the worker is what lets an installed app open
// with no signal at all.
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}

const app = document.getElementById("app");
const laptopEl = document.getElementById("laptop");
const config = document.body.dataset.firebase;

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

const spinner = () => h("span", { class: "spin", "aria-hidden": "true" });

function notice(text, ...extra) {
  app.replaceChildren(h("p", { class: "notice" }, text), ...extra);
}

if (!config) {
  laptopEl.textContent = "";
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

  const state = { sessions: new Map(), projects: [], tasks: [], laptop: null, user: null, picked: null, status: "", statusBusy: false, busy: new Set(), loaded: new Set(), push: "unknown", editing: null, sends: new Map(), attaching: [], images: new Map(),
    filter: "ready", only: null, arranging: false, menu: null, picking: false, pickQuery: "",
    // Picks for open questions by question id, kept across redraws.
    answers: new Map() };
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
        state.loaded.add("sessions");
        render();
      }, failed),
      fs.onSnapshot(fs.collection(db, "projects"), (snap) => {
        state.projects = snap.docs.map((d) => d.data());
        state.loaded.add("projects");
        render();
      }, failed),
      // Metadata changes report when the server confirms a write, which
      // ends a task's saving mark.
      fs.onSnapshot(fs.collection(db, "tasks"), { includeMetadataChanges: true }, (snap) => {
        state.tasks = snap.docs.map((d) => ({ id: d.id, ...d.data(), saving: d.metadata.hasPendingWrites }));
        state.loaded.add("tasks");
        render();
      }, failed),
      fs.onSnapshot(fs.doc(db, "laptop", "status"), (snap) => {
        state.laptop = snap.exists() ? snap.data() : null;
        render();
      }, failed),
    ];
  }
  function stop() {
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
  window.addEventListener("hashchange", render);
  setInterval(() => !document.hidden && state.user && renderLaptop(), 60_000);

  auth.onAuthStateChanged(a, async (user) => {
    stop();
    state.user = null;
    if (!user) {
      laptopEl.textContent = "";
      notice("Sign in with the Google account that owns peyk.", h("button", { class: "primary", onclick: signIn }, "Sign in with Google"));
      return;
    }
    const token = await user.getIdTokenResult();
    if (!token.claims.owner) {
      laptopEl.textContent = "";
      notice(
        `${user.email} is signed in but is not the owner.`,
        h("p", { class: "hint" }, "On the laptop, run ", h("code", {}, `make owner EMAIL=${user.email}`), ", then sign in again."),
        h("button", { onclick: () => auth.signOut(a) }, "Sign out"),
      );
      return;
    }
    state.user = user;
    listen();
    checkPush().then(render, (err) => setStatus(`Cannot check notifications: ${err.message}`));
  });

  function renderLaptop() {
    const now = Date.now();
    const online = laptopOnline(state.laptop, now);
    laptopEl.className = `laptop ${online ? "on" : "off"}`;
    const seen = state.laptop && state.laptop.seen_at ? ` · seen ${ago(state.laptop.seen_at, now)}` : "";
    laptopEl.textContent = (online ? "laptop online" : "laptop offline") + seen;
  }

  function render() {
    if (!state.user) return;
    renderLaptop();
    // Snapshots re-render the whole view; keep what is being typed.
    const typed = 'textarea, input[type="search"]';
    const drafts = new Map([...app.querySelectorAll(typed)].map((t) => [t.id, t.value]));
    const focused = document.activeElement && document.activeElement.matches(typed) ? document.activeElement.id : null;

    const id = location.hash.startsWith("#s-") ? decodeURIComponent(location.hash.slice(3)) : null;
    const s = id && state.sessions.get(id);
    const view = s ? "board" : location.hash === "#inbox" ? "inbox" : "board";
    const tabs = h("nav", { class: "tabs" },
      h("a", { href: "#", "aria-current": view === "board" ? "page" : null }, "Workers", h("span", { class: "n" }, String(state.sessions.size))),
      h("a", { href: "#inbox", "aria-current": view === "inbox" ? "page" : null }, "Inbox", queuedCount() ? h("span", { class: "n" }, String(queuedCount())) : null),
    );
    const status = h("p", { class: "status", id: "status", role: "status" }, state.statusBusy ? spinner() : null, state.status);
    if (location.hash.startsWith("#pair-")) app.replaceChildren(pairView(location.hash.slice(6)));
    else if (s) app.replaceChildren(tabs, status, detail(s));
    else if (view === "inbox") app.replaceChildren(tabs, status, inbox());
    else app.replaceChildren(tabs, status, board());

    for (const t of app.querySelectorAll(typed)) if (drafts.has(t.id)) t.value = drafts.get(t.id);
    document.documentElement.classList.toggle("locked", view === "inbox" && state.picking);
    if (focused) document.getElementById(focused)?.focus();
  }

  function setStatus(text, busy = false) {
    state.status = text;
    state.statusBusy = busy;
    document.getElementById("status")?.replaceChildren(...(busy ? [spinner()] : []), text);
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

  // sendCommand signs the payload with this device's session key (push needs
  // none), stores it, and reports each step and what the laptop made of it
  // through report. key stays busy until the laptop answers, so the same
  // action cannot be sent twice meanwhile. Resolves once the command is
  // stored, to whether it was.
  async function sendCommand(type, fields, label, key, report = setStatus) {
    if (state.busy.has(key)) return false;
    if (!laptopOnline(state.laptop, Date.now())) {
      report(`${label}: the laptop is offline, so nothing was sent.`);
      return false;
    }
    state.busy.add(key);
    const finish = (text) => {
      state.busy.delete(key);
      report(text, false);
      render();
    };
    const ref = fs.doc(fs.collection(db, "commands"));
    const payload = JSON.stringify({ id: ref.id, type, ...fields, expires: Date.now() + 60_000 });
    const data = { type, payload, status: "pending", created_at: fs.serverTimestamp() };
    report(`${label}: signing…`, true);
    render();
    try {
      if (type !== "push") data.grant = await passkey.signCommand(payload, () => report(`${label}: approve with your passkey…`, true));
      report(`${label}: sending…`, true);
      await fs.setDoc(ref, data);
    } catch (err) {
      finish(err.name === "NotAllowedError"
        ? `${label}: not sent. The passkey prompt was cancelled, or this device has no paired passkey (run make pair on the laptop).`
        : `${label}: not sent. ${err.message}`);
      return false;
    }
    report(`${label}: waiting for the laptop…`, true);
    // The payload expires after a minute, so a laptop that has not answered
    // by then will refuse it.
    const timer = setTimeout(() => {
      unsub();
      finish(`${label}: the laptop did not answer.`);
    }, 75_000);
    const unsub = fs.onSnapshot(ref, (snap) => {
      const c = snap.data();
      if (!c || c.status === "pending") return;
      clearTimeout(timer);
      unsub();
      if (c.status === "failed" && String(c.error).startsWith("session approval:")) passkey.forgetGrant();
      finish(c.status === "done" ? `${label}: done.` : `${label} failed: ${c.error}`);
    }, (err) => {
      clearTimeout(timer);
      finish(`${label}: cannot follow the result: ${err.message}`);
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
      h("a", { class: "back", href: "#" }, "← Workers"),
      h("h2", {}, "Pair this device"),
      h("p", {}, "Commands to the laptop are signed with a passkey on this device. Create one, then confirm its fingerprint on the laptop."),
      button, out);
  }

  function queuedCount() {
    return state.tasks.filter((t) => t.status === "queued").length;
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
    const projects = sortProjects(state.projects);
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
    const strip = names.length > 1
      ? h("div", { class: "strip", role: "group", "aria-label": "Filter by project" }, names.map((n) =>
        h("button", { type: "button", class: "sface", title: n, "aria-label": n, "aria-pressed": String(state.only === n),
          onclick: () => { state.only = state.only === n ? null : n; render(); } }, face(projectNamed(n), n, "md"))))
      : null;

    const items = !state.loaded.has("tasks") ? loading("Loading tasks…") : list.length
      ? list.map((t, i) => (state.editing === t.id ? editor(t, projects) : state.arranging ? arrangeRow(list, t, i) : taskCard(t, now)))
      : h("p", { class: "empty" }, all.length ? "Nothing here for this filter." : "No tasks queued.");
    const finished = doneTasks(state.tasks).filter((t) => !state.only || t.project === state.only);
    return h("div", { class: "inbox" },
      filters, strip,
      h("p", { class: "alert", id: "alert", role: "status" }),
      h("div", { class: "tasks" }, items),
      finished.length && state.filter !== "waiting" && !state.arranging ? [
        h("h2", { class: "group" }, "Done"),
        ...finished.map((t) =>
          h("div", { class: "task done" },
            h("div", { class: "thead" }, face(projectNamed(t.project), t.project, "sm"),
              h("span", { class: "proj" }, `${t.project} · done ${ago(t.done_at, now)} ago${t.commit ? ` · ${t.commit}` : ""}`),
              h("span", { class: "grow" }),
              h("button", { type: "button", class: "icon", "aria-label": `Delete task: ${t.text}`, onclick: () => remove(t) }, icon("x"))),
            h("div", { class: "ttext" }, t.text),
            taskImages(t),
          )),
      ] : null,
      composer(),
      state.picking ? picker(projects) : null,
    );
  }

  // A send's outcome stays on its task, which may have moved since, rather
  // than in a status line scrolled out of view.
  function send(t, clear) {
    const report = (text, busy = false) => {
      state.sends.set(t.id, { text, busy });
      render();
    };
    state.menu = null;
    sendCommand("dispatch", { project: t.project, task: t.id, text: t.text, clear, ...(t.images?.length ? { images: t.images } : {}) },
      clear ? `Clear ${t.project} and send` : `Send to ${t.project}`, `send:${t.id}`, report);
  }

  // A worker with a conversation is cleared first, so a task starts on its own
  // context; sending into the conversation is the exception, in the menu. A
  // new or just-cleared worker has nothing to clear.
  function hasConversation(t) {
    const p = projectNamed(t.project);
    const worker = p && p.session_id && state.sessions.get(p.session_id);
    return Boolean(worker && !worker.cleared);
  }

  function taskCard(t, now) {
    const clears = t.ready && hasConversation(t);
    const open = state.menu === t.id;
    const sent = state.sends.get(t.id);
    const status = sent
      ? h("span", { class: "tstatus", role: "status" }, sent.text)
      : h("span", { class: "tstatus" }, t.ready
        ? (projectNamed(t.project)?.session_id ? (clears ? "Free · clears the conversation first" : "Free") : "Starts a new worker")
        : `Waiting · ${t.why}`);
    return h("article", { class: `task${t.ready ? "" : " waiting"}` },
      h("div", { class: "thead" },
        face(projectNamed(t.project), t.project, "sm"),
        h("span", { class: "proj" }, t.project),
        t.saving ? h("span", { class: "when saving" }, spinner(), "saving") : h("span", { class: "when" }, ago(t.created_at, now)),
        h("span", { class: "grow" }),
        h("button", { type: "button", class: "icon", "aria-label": `More actions: ${t.text}`, "aria-expanded": String(open),
          onclick: () => { state.menu = open ? null : t.id; render(); } }, icon("more"))),
      h("div", { class: "ttext" }, t.text),
      taskImages(t),
      open ? h("div", { class: "menu" },
        clears && !state.busy.has(`send:${t.id}`) ? h("button", { type: "button", onclick: () => send(t, false) }, "Send without clearing") : null,
        h("button", { type: "button", onclick: () => move(t, 0) }, "Move to top"),
        h("button", { type: "button", "aria-label": `Edit task: ${t.text}`, onclick: () => { state.editing = t.id; state.editProject = undefined; state.menu = null; render(); } }, "Edit"),
        h("button", { type: "button", class: "danger", "aria-label": `Delete task: ${t.text}`, onclick: () => { state.menu = null; remove(t); } }, "Delete"),
      ) : null,
      h("div", { class: "tfoot" },
        sent?.busy ? spinner() : h("span", { class: `dot ${sent ? "sent" : t.ready ? "free" : "busy"}` }),
        status,
        t.ready ? busyButton(`send:${t.id}`, { type: "button", class: "primary send", onclick: () => send(t, clears) }, "Send", icon("arrow")) : null,
      ),
    );
  }

  function arrangeRow(list, t, i) {
    return h("div", { class: "task arrange" },
      icon("grip"),
      face(projectNamed(t.project), t.project, "sm"),
      h("span", { class: "ttext one" }, t.text),
      h("button", { type: "button", class: "icon step", "aria-label": `Move up: ${t.text}`, disabled: i === 0 ? "" : null, onclick: () => move(t, i - 1, list) }, icon("up")),
      h("button", { type: "button", class: "icon step", "aria-label": `Move down: ${t.text}`, disabled: i === list.length - 1 ? "" : null, onclick: () => move(t, i + 1, list) }, icon("down")),
    );
  }

  // Moves within the list on screen; to the top moves within the whole queue.
  function move(t, to, list = queue(state.tasks, state.projects)) {
    const i = list.findIndex((x) => x.id === t.id);
    state.menu = null;
    fs.updateDoc(fs.doc(db, "tasks", t.id), { rank: rankAt(list, i, to) })
      .catch((err) => alertLine(`Could not move the task: ${err.message}`));
  }

  function composer() {
    const p = projectNamed(state.picked);
    const text = h("textarea", { id: "task-text", rows: "2", placeholder: state.picked ? "Describe a task…" : "No projects published yet", "aria-label": "New task", onpaste: pasteImages });
    // The draft is cleared as soon as the task is accepted: a local write
    // re-renders the inbox before the server confirms it, and the re-render
    // would otherwise restore the text it found in the box.
    const add = async (e) => {
      e.preventDefault();
      const value = text.value.trim();
      if (!value || !state.picked) return;
      const attaching = state.attaching;
      text.value = "";
      state.attaching = [];
      try {
        await track("add", () => addTask(state.picked, value, attaching));
      } catch (err) {
        const box = document.getElementById("task-text");
        if (box && !box.value) box.value = value;
        if (!state.attaching.length) state.attaching = attaching;
        render();
        alertLine(`Could not add the task: ${err.message}`);
      }
    };
    const files = h("input", { type: "file", accept: "image/*", multiple: "", hidden: "", "aria-label": "Attach images", onchange: (e) => { attachImages([...e.target.files]); e.target.value = ""; } });
    return h("form", { class: "composer add", onsubmit: add },
      state.attaching.length ? h("div", { class: "thumbs" }, state.attaching.map((img, i) =>
        h("span", { class: "thumb" },
          h("img", { src: img.url, alt: `Attached image ${i + 1}` }),
          h("button", { type: "button", class: "icon", "aria-label": `Remove image ${i + 1}`, onclick: () => { URL.revokeObjectURL(img.url); state.attaching.splice(i, 1); render(); } }, icon("x")),
        ))) : null,
      text,
      h("div", { class: "crow" },
        h("button", { type: "button", class: "pill", "aria-label": `Project: ${state.picked ?? "none"}`, "aria-haspopup": "dialog",
          disabled: state.picked ? null : "", onclick: () => { state.picking = true; state.pickQuery = ""; render(); document.getElementById("pick-search")?.focus(); } },
          state.picked ? face(p, state.picked, "xs") : null, h("span", {}, state.picked ?? "No project"), icon("chevron")),
        h("button", { type: "button", class: "icon round", title: "Attach an image or screenshot", "aria-label": "Attach image", disabled: state.attaching.length >= MAX_IMAGES ? "" : null, onclick: () => files.click() }, icon("clip")),
        h("span", { class: "grow" }),
        busyButton("add", { type: "submit", class: "round go", "aria-label": "Add task" }, icon("up")),
      ),
      files,
    );
  }

  function picker(projects) {
    const close = () => { state.picking = false; render(); document.getElementById("task-text")?.focus(); };
    const pick = (name) => { state.picked = name; close(); };
    const queued = new Map();
    for (const t of state.tasks) if (t.status === "queued") queued.set(t.project, (queued.get(t.project) || 0) + 1);
    const q = state.pickQuery || "";
    const matches = searchProjects(projects, q);
    const recent = q ? [] : projects.filter((p) => p.active).slice(0, 4);
    const row = (p) => {
      const st = projectStatus(p);
      const on = p.name === state.picked;
      return h("button", { type: "button", role: "radio", class: "prow", "aria-checked": String(on), onclick: () => pick(p.name) },
        face(p, p.name, "lg"),
        h("span", { class: "pmain" },
          h("span", { class: "pname" }, p.name),
          h("span", { class: `pstat ${st.tone}` }, h("span", { class: `dot ${st.tone}` }), st.text)),
        queued.get(p.name) ? h("span", { class: "badge" }, `${queued.get(p.name)} queued`) : null,
        on ? icon("check") : null);
    };
    return h("div", { class: "scrim", onclick: (e) => e.target === e.currentTarget && close() },
      h("section", { class: "sheet", role: "dialog", "aria-modal": "true", "aria-label": "Choose a project", onkeydown: (e) => e.key === "Escape" && close() },
        h("div", { class: "grabber" }),
        h("div", { class: "sheethead" }, h("h2", {}, "Send to"), h("button", { type: "button", class: "link", onclick: close }, "Cancel")),
        h("label", { class: "search" }, icon("search"),
          h("input", { type: "search", id: "pick-search", placeholder: `Search ${projects.length} projects`, "aria-label": "Search projects", value: q,
            oninput: (e) => { state.pickQuery = e.target.value; render(); } })),
        h("div", { class: "sheetbody" },
          recent.length ? [
            h("h3", { class: "group" }, "Recent"),
            h("div", { class: "recent" }, recent.map((p) =>
              h("button", { type: "button", class: "tile", "aria-label": `${p.name} (recent)`, "aria-pressed": String(p.name === state.picked), onclick: () => pick(p.name) },
                face(p, p.name, "xl"), h("span", {}, p.name), h("span", { class: `dot ${projectStatus(p).tone}` })))),
          ] : null,
          h("h3", { class: "group" }, q ? `Matches · ${matches.length}` : "All projects"),
          matches.length
            ? h("div", { class: "plist", role: "radiogroup", "aria-label": "Project" }, matches.map(row))
            : h("p", { class: "empty" }, "No project matches."),
        ),
      ),
    );
  }

  // A task's images go with it, unless another task shares one.
  function remove(t) {
    const batch = fs.writeBatch(db);
    batch.delete(fs.doc(db, "tasks", t.id));
    for (const id of orphanedImages(state.tasks, [t])) batch.delete(fs.doc(db, "attachments", id));
    batch.commit().catch((err) => alertLine(`Could not delete: ${err.message}`));
  }

  // An image is named by its hash, so one already stored is the same image
  // and is not written again; writing it again would be refused.
  // The inbox opens on ready tasks; one queued for a busy project would
  // vanish from that view as it is saved, so the view widens to show it.
  function reveal(project) {
    if (state.filter === "ready" && !projectNamed(project)?.free) state.filter = "all";
  }

  async function addTask(project, text, images) {
    reveal(project);
    const batch = fs.writeBatch(db);
    const ids = [];
    for (const img of images) {
      const id = await imageID(img.bytes);
      if (ids.includes(id)) continue;
      ids.push(id);
      if (!(await fs.getDoc(fs.doc(db, "attachments", id))).exists()) {
        batch.set(fs.doc(db, "attachments", id), { data: fs.Bytes.fromUint8Array(img.bytes), type: img.type, created_at: fs.serverTimestamp() });
      }
    }
    // The rank is set here because created_at reads as null until the server
    // has confirmed it, which would sort the new task to the top meanwhile.
    const task = { project, text, created_at: fs.serverTimestamp(), status: "queued", rank: Date.now() };
    if (ids.length) task.images = ids;
    batch.set(fs.doc(fs.collection(db, "tasks")), task);
    await batch.commit();
    for (const img of images) URL.revokeObjectURL(img.url);
  }

  async function attachImages(files) {
    const room = MAX_IMAGES - state.attaching.length;
    if (files.length > room) alertLine(`A task carries at most ${MAX_IMAGES} images; the rest were left out.`);
    for (const file of files.slice(0, Math.max(0, room))) {
      try {
        const img = await prepareImage(file);
        state.attaching.push({ ...img, url: URL.createObjectURL(new Blob([img.bytes], { type: img.type })) });
      } catch (err) {
        alertLine(`Could not attach: ${err.message}`);
      }
    }
    render();
  }

  function pasteImages(e) {
    const files = [...(e.clipboardData?.files || [])].filter((f) => f.type.startsWith("image/"));
    if (!files.length) return;
    e.preventDefault();
    attachImages(files);
  }

  // Thumbnails load on first sight and are kept for the life of the page;
  // stored images never change.
  function taskImages(t) {
    if (!t.images?.length) return null;
    return h("div", { class: "thumbs" }, t.images.map((id, i) => {
      const url = state.images.get(id);
      if (url === undefined) loadImage(id);
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

  function editor(t, projects) {
    // Snapshots re-render the editor; the chosen project survives in state.
    const chosen = state.editProject ?? t.project;
    const select = h("select", { "aria-label": "Project", onchange: (e) => { state.editProject = e.target.value; } },
      projects.map((p) => {
        const o = h("option", { value: p.name }, p.free ? p.name : `${p.name} (${p.busy})`);
        if (p.name === chosen) o.selected = true;
        return o;
      }),
    );
    const text = h("textarea", { id: `edit-${t.id}`, rows: "3", "aria-label": "Task text" });
    text.value = t.text;
    const save = async (e) => {
      e.preventDefault();
      const value = text.value.trim();
      if (!value) return;
      try {
        await track(`edit:${t.id}`, () => fs.updateDoc(fs.doc(db, "tasks", t.id), { text: value, project: select.value }));
        state.editing = null;
        state.editProject = undefined;
        reveal(select.value);
        render();
      } catch (err) {
        alertLine(`Could not save: ${err.code === "permission-denied" ? "the task was already sent" : err.message}`);
      }
    };
    return h("form", { class: "task editing", onsubmit: save },
      h("div", { class: "grow" }, select, text),
      h("div", { class: "btnrow" },
        busyButton(`edit:${t.id}`, { type: "submit", class: "primary" }, "Save"),
        h("button", { type: "button", onclick: () => { state.editing = null; state.editProject = undefined; render(); } }, "Cancel"),
      ),
    );
  }

  function alertLine(text) {
    const el = document.getElementById("alert");
    if (el) el.textContent = text;
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
        setStatus("Notifications are blocked for this site in the browser settings.");
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
      setStatus("Notifications are on for this device.");
      render();
    } catch (err) {
      setStatus(`Could not turn on notifications: ${err.message}`);
    }
  }

  function board() {
    const now = Date.now();
    const list = sortSessions([...state.sessions.values()]);
    const pushRow = state.push === "off"
      ? h("div", { class: "pushrow" }, h("span", {}, "Get notified when a worker finishes or needs you."), busyButton("notify", { type: "button", onclick: () => track("notify", turnOnPush) }, "Turn on notifications"))
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
      const last = s.git && s.git.last;
      const task = currentTask(state.tasks, s.id);
      out.push(
        h("a", { class: "card", href: `#s-${encodeURIComponent(s.id)}` },
          h("span", { class: "facewrap" }, face(projectOf(s), s.title, "lg"), h("span", { class: `sdot ${st.tone}` })),
          h("span", { class: "cbody" },
            h("span", { class: "top" }, h("span", { class: "name" }, s.title), flagRow(s), h("span", { class: "grow" }), chip(st)),
            task ? h("span", { class: "curtask" }, `Task: ${oneLine(task.text)}`) : null,
            s.question ? h("span", { class: "msg ask" }, `Asks: ${oneLine(s.question.items.map((q) => q.question).join(" · "))}`)
              : s.message ? h("span", { class: "msg" }, oneLine(s.message.text)) : null,
            h("span", { class: "meta" },
              last ? h("span", {}, `${last.hash} · ${ago(last.at, now)}`) : null,
              h("span", {}, pushLabel(s.git)),
              s.git && s.git.dirty ? h("span", {}, `${s.git.dirty} changed`) : null,
            ),
          ),
        ),
      );
    }
    return h("div", { class: "board" }, out);
  }

  function detail(s) {
    const now = Date.now();
    const st = STATES[s.state] || { label: s.state, tone: "idle" };
    const g = s.git;
    const busy = s.state === "working" || s.state === "needs_input";
    const task = currentTask(state.tasks, s.id);
    const rows = [["aoe", s.aoe_status], ["path", s.path]];
    if (g) {
      if (g.last) rows.push(["commit", `${g.last.hash} ${g.last.subject} (${ago(g.last.at, now)})`]);
      rows.push(["push", pushLabel(g)]);
      rows.push(["tree", g.dirty ? `${g.dirty} changed files` : "clean"]);
    }
    const sent = h("div", { class: "reply sent" });
    sent.innerHTML = s.prompt ? renderMarkdown(s.prompt.text) : "<p>Nothing sent yet.</p>";
    const msg = h("div", { class: "reply" });
    msg.innerHTML = s.message ? renderMarkdown(s.message.text) : "<p>No reply yet.</p>";
    return h("article", { class: "detail" },
      h("div", { class: "dhead" },
        h("a", { class: "back", href: "#", "aria-label": "Back to workers" }, icon("back")),
        face(projectOf(s), s.title, "lg"),
        h("div", { class: "dtitle" },
          h("h2", {}, s.title, chip(st), flagRow(s)),
          h("span", { class: "branch" }, g ? (g.upstream ? `${g.branch} → ${g.upstream}` : g.branch || "detached") : "no repository")),
        s.cleared || busy ? null : busyButton(`clear:${s.id}`, { type: "button", class: "icon tool", "aria-label": "Clear", title: "Clear the conversation", onclick: () => sendCommand("clear", { session: s.id }, `Clear ${s.title}`, `clear:${s.id}`) }, icon("trash")),
        s.aoe_status !== "stopped" ? busyButton(`stop:${s.id}`, { type: "button", class: "icon tool", "aria-label": "Stop", title: "Stop the worker", onclick: () => sendCommand("stop", { session: s.id }, `Stop ${s.title}`, `stop:${s.id}`) }, icon("stop")) : null,
      ),
      task ? h("p", { class: "curtask" }, `Task: ${task.text}`, h("span", { class: "when" }, ` · sent ${ago(task.dispatched_at, now)} ago`)) : null,
      g && g.unpushed > 0 ? pushCard(s, now) : null,
      s.prototypes && s.prototypes.length ? prototypes(s.prototypes, now) : null,
      h("div", { class: "convo" },
        h("span", { class: "who mine" }, "You", s.prompt ? ` · ${ago(s.prompt.at, now)}` : ""),
        sent,
        h("span", { class: "who" }, s.title, s.message ? ` · ${ago(s.message.at, now)}` : ""),
        msg,
      ),
      s.question ? questionCard(s) : null,
      h("details", { class: "facts" }, h("summary", {}, "Details"),
        h("dl", {}, rows.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)]))),
      replyBox(s),
    );
  }

  function pushCard(s, now) {
    const g = s.git;
    const more = g.unpushed - (g.pending?.length ?? 0);
    return h("section", { class: "pushcard" },
      h("div", { class: "phead" },
        h("h3", {}, `Unpushed commits · ${g.unpushed}`),
        h("span", { class: "grow" }),
        busyButton(`push:${s.id}`, { type: "button", class: "primary ok", onclick: () => sendCommand("push", { session: s.id }, `Push ${s.title}`, `push:${s.id}`) }, `Push ${g.unpushed}`)),
      g.pending?.length ? h("ul", { class: "pending" },
        g.pending.map((c) => h("li", {}, h("code", {}, c.hash), ` ${c.subject}`, h("span", { class: "when" }, ago(c.at, now)))),
        more > 0 ? h("li", { class: "when" }, `and ${more} older`) : null,
      ) : null,
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
      if (text && (await sendCommand("answer", { session: s.id, question: q.id, text }, `Answer ${s.title}`, key))) {
        state.answers.delete(q.id);
      }
    };
    return h("form", { class: "qcard", onsubmit: submit },
      h("h3", {}, `${s.title} asks`),
      items,
      h("div", { class: "qfoot" }, h("span", { class: "hint" }, "Closes the dialog on the laptop and sends these answers."), send));
  }

  function replyBox(s) {
    const text = h("textarea", { id: `reply-${s.id}`, rows: "1", placeholder: `Reply to ${s.title}…`, "aria-label": "Reply" });
    const reply = async (e) => {
      e.preventDefault();
      const v = text.value.trim();
      // A redraw while sending replaces this textarea, so clear the one on screen.
      if (v && (await sendCommand("reply", { session: s.id, text: v }, `Reply to ${s.title}`, `reply:${s.id}`))) {
        const box = document.getElementById(`reply-${s.id}`);
        if (box) box.value = "";
      }
    };
    return h("form", { class: "composer inline", onsubmit: reply },
      text, busyButton(`reply:${s.id}`, { type: "submit", class: "round go", "aria-label": "Send reply" }, icon("up")));
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

function flagRow(s) {
  const f = flags(s);
  const proto = s.prototypes && s.prototypes.length
    ? h("span", { class: "flag proto", title: "Published a prototype; open it from the worker" }, s.prototypes.length > 1 ? `${s.prototypes.length} prototypes` : "prototype")
    : null;
  return f.length || proto ? h("span", { class: "flags" }, proto, f.map((x) => h("span", { class: `flag ${x}` }, x))) : null;
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
  arrow: '<path d="M5 12h14M13 6l6 6-6 6"/>',
  up: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  down: '<path d="M12 5v14M5 12l7 7 7-7"/>',
  chevron: '<path d="M6 9l6 6 6-6"/>',
  back: '<path d="M15 18l-6-6 6-6"/>',
  clip: '<path d="M21 11.5l-8.6 8.6a5 5 0 01-7.1-7.1l8.6-8.6a3.4 3.4 0 014.8 4.8l-8.6 8.6a1.7 1.7 0 01-2.4-2.4l7.9-7.9"/>',
  sort: '<path d="M7 4v16M3 8l4-4 4 4M17 20V4M13 16l4 4 4-4"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
  x: '<path d="M6 6l12 12M18 6L6 18"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="2"/>',
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
