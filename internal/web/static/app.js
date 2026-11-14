import { renderMarkdown } from "./markdown.js";
import { STATES, ago, currentTask, doneTasks, flags, inboxGroups, laptopOnline, pushLabel, sortProjects, sortSessions } from "./board.js";
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
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v);
  }
  for (const c of children.flat()) if (c != null) el.append(c);
  return el;
};

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

  const state = { sessions: new Map(), projects: [], tasks: [], laptop: null, user: null, picked: null, status: "", push: "unknown", editing: null, sends: new Map() };
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
        render();
      }, failed),
      fs.onSnapshot(fs.collection(db, "projects"), (snap) => {
        state.projects = snap.docs.map((d) => d.data());
        render();
      }, failed),
      fs.onSnapshot(fs.collection(db, "tasks"), (snap) => {
        state.tasks = snap.docs.map((d) => ({ id: d.id, ...d.data() }));
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
    const drafts = new Map([...app.querySelectorAll("textarea")].map((t) => [t.id, t.value]));
    const focused = document.activeElement && document.activeElement.tagName === "TEXTAREA" ? document.activeElement.id : null;

    const id = location.hash.startsWith("#s-") ? decodeURIComponent(location.hash.slice(3)) : null;
    const s = id && state.sessions.get(id);
    const view = s ? "board" : location.hash === "#inbox" ? "inbox" : "board";
    const tabs = h("nav", { class: "tabs" },
      h("a", { href: "#", class: view === "board" ? "on" : "" }, "Workers"),
      h("a", { href: "#inbox", class: view === "inbox" ? "on" : "" }, `Inbox${queuedCount() ? ` · ${queuedCount()}` : ""}`),
    );
    const status = h("p", { class: "status", id: "status", role: "status" }, state.status);
    if (location.hash.startsWith("#pair-")) app.replaceChildren(pairView(location.hash.slice(6)));
    else if (s) app.replaceChildren(tabs, status, detail(s));
    else if (view === "inbox") app.replaceChildren(tabs, status, inbox());
    else app.replaceChildren(tabs, status, board());

    for (const t of app.querySelectorAll("textarea")) if (drafts.has(t.id)) t.value = drafts.get(t.id);
    if (focused) document.getElementById(focused)?.focus();
  }

  function setStatus(text) {
    state.status = text;
    const el = document.getElementById("status");
    if (el) el.textContent = text;
  }

  // sendCommand signs the payload with the paired passkey (push needs none),
  // stores it, and reports what the laptop made of it through report.
  async function sendCommand(type, fields, label, report = setStatus) {
    if (!laptopOnline(state.laptop, Date.now())) {
      report(`${label}: the laptop is offline, so nothing was sent.`);
      return false;
    }
    const ref = fs.doc(fs.collection(db, "commands"));
    const payload = JSON.stringify({ id: ref.id, type, ...fields, expires: Date.now() + 60_000 });
    const data = { type, payload, status: "pending", created_at: fs.serverTimestamp() };
    try {
      if (type !== "push") data.assertion = await passkey.sign(payload);
      await fs.setDoc(ref, data);
    } catch (err) {
      report(err.name === "NotAllowedError"
        ? `${label}: not sent. The passkey prompt was cancelled, or this device has no paired passkey (run make pair on the laptop).`
        : `${label}: not sent. ${err.message}`);
      return false;
    }
    report(`${label}: waiting for the laptop…`);
    const unsub = fs.onSnapshot(ref, (snap) => {
      const c = snap.data();
      if (!c || c.status === "pending") return;
      unsub();
      report(c.status === "done" ? `${label}: done.` : `${label} failed: ${c.error}`);
    }, (err) => report(`${label}: cannot follow the result: ${err.message}`));
    return true;
  }

  function pairView(code) {
    const out = h("p", { class: "hint" });
    const button = h("button", { class: "primary", type: "button" }, "Create passkey");
    button.addEventListener("click", async () => {
      button.disabled = true;
      try {
        const key = await passkey.createPasskey(state.user);
        await fs.setDoc(fs.doc(db, "pairings", code), { ...key, created_at: fs.serverTimestamp() });
        out.replaceChildren("The laptop should now show this fingerprint. Confirm there only if it matches:",
          h("strong", { class: "fp" }, await passkey.fingerprint(key.public_key)));
      } catch (err) {
        button.disabled = false;
        out.textContent = `Pairing failed: ${err.message}`;
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

  function inbox() {
    const projects = sortProjects(state.projects);
    if (!state.picked || !projects.some((p) => p.name === state.picked)) state.picked = projects[0]?.name ?? null;
    const counts = new Map();
    for (const t of state.tasks) if (t.status === "queued") counts.set(t.project, (counts.get(t.project) || 0) + 1);

    const picker = h("div", { class: "picker", role: "radiogroup", "aria-label": "Project" },
      projects.map((p) =>
        h("button", {
          type: "button", role: "radio", class: "pchip", "aria-checked": String(p.name === state.picked),
          title: p.free ? "free" : p.busy,
          onclick: () => { state.picked = p.name; render(); },
        },
          h("span", { class: `dot ${p.free ? "free" : "busy"}` }),
          p.name,
          counts.get(p.name) ? h("span", { class: "q" }, String(counts.get(p.name))) : null,
        ),
      ),
    );
    const text = h("textarea", { id: "task-text", rows: "2", placeholder: state.picked ? `Task for ${state.picked}…` : "No projects published yet", "aria-label": "New task" });
    const add = async (e) => {
      e.preventDefault();
      const value = text.value.trim();
      if (!value || !state.picked) return;
      try {
        await fs.addDoc(fs.collection(db, "tasks"), { project: state.picked, text: value, created_at: fs.serverTimestamp(), status: "queued" });
        text.value = "";
      } catch (err) {
        alertLine(`Could not add the task: ${err.message}`);
      }
    };
    const form = h("form", { class: "add", onsubmit: add }, picker, h("div", { class: "addrow" }, text, h("button", { class: "primary", type: "submit" }, "Add")));

    const { ready, waiting } = inboxGroups(state.tasks, state.projects);
    const now = Date.now();
    // A send's outcome stays on its task, which may have moved between groups
    // since, rather than in a status line scrolled out of view.
    const send = (t, clear) => {
      const report = (text) => {
        state.sends.set(t.id, text);
        render();
      };
      sendCommand("dispatch", { project: t.project, task: t.id, text: t.text, clear }, clear ? `Clear ${t.project} and send` : `Send to ${t.project}`, report);
    };
    // A worker with a conversation is cleared first by default, so a task
    // starts on its own context; sending into the conversation is the
    // exception. A new or just-cleared worker has nothing to clear.
    const sendButtons = (t) => {
      const p = projects.find((x) => x.name === t.project);
      const worker = p && p.session_id && state.sessions.get(p.session_id);
      if (!worker || worker.cleared) return h("button", { type: "button", class: "primary", onclick: () => send(t, false) }, "Send");
      return [
        h("button", { type: "button", class: "primary", onclick: () => send(t, true) }, "Clear & send"),
        h("button", { type: "button", "aria-label": "Send without clearing", title: "Send into the current conversation", onclick: () => send(t, false) }, "Send"),
      ];
    };
    const item = (t, why) => {
      if (state.editing === t.id) return editor(t, projects);
      return h("div", { class: "task" },
        h("div", { class: "grow" },
          h("div", { class: "proj" }, `${t.project} · ${ago(t.created_at, now)}`),
          h("div", { class: "ttext" }, t.text),
          why ? h("div", { class: "meta" }, why) : null,
          state.sends.has(t.id) ? h("div", { class: "meta sent", role: "status" }, state.sends.get(t.id)) : null,
        ),
        why ? null : sendButtons(t),
        h("button", { type: "button", class: "icon", "aria-label": `Edit task: ${t.text}`, onclick: () => { state.editing = t.id; state.editProject = undefined; render(); } }, "✎"),
        h("button", { type: "button", class: "icon", "aria-label": `Delete task: ${t.text}`, onclick: () => remove(t) }, "×"),
      );
    };
    const finished = doneTasks(state.tasks);
    return h("div", { class: "inbox" },
      form,
      h("p", { class: "alert", id: "alert", role: "status" }),
      h("h2", { class: "group" }, "Ready to send"),
      ready.length ? ready.map((t) => item(t)) : h("p", { class: "empty" }, "Nothing queued for a free project."),
      h("h2", { class: "group" }, "Waiting on its worker"),
      waiting.length ? waiting.map((t) => item(t, t.why)) : h("p", { class: "empty" }, "Nothing waiting."),
      finished.length ? h("h2", { class: "group" }, "Done") : null,
      finished.map((t) =>
        h("div", { class: "task done" },
          h("div", { class: "grow" },
            h("div", { class: "proj" }, `${t.project} · done ${ago(t.done_at, now)} ago${t.commit ? ` · ${t.commit}` : ""}`),
            h("div", { class: "ttext" }, t.text),
          ),
          h("button", { type: "button", class: "icon", "aria-label": `Delete task: ${t.text}`, onclick: () => remove(t) }, "×"),
        ),
      ),
    );
  }

  function remove(t) {
    fs.deleteDoc(fs.doc(db, "tasks", t.id)).catch((err) => alertLine(`Could not delete: ${err.message}`));
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
        await fs.updateDoc(fs.doc(db, "tasks", t.id), { text: value, project: select.value });
        state.editing = null;
        state.editProject = undefined;
        render();
      } catch (err) {
        alertLine(`Could not save: ${err.code === "permission-denied" ? "the task was already sent" : err.message}`);
      }
    };
    return h("form", { class: "task editing", onsubmit: save },
      h("div", { class: "grow" }, select, text),
      h("div", { class: "btnrow" },
        h("button", { type: "submit", class: "primary" }, "Save"),
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
      ? h("div", { class: "pushrow" }, h("span", {}, "Get notified when a worker finishes or needs you."), h("button", { type: "button", onclick: turnOnPush }, "Turn on notifications"))
      : null;
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
      out.push(
        h("a", { class: "card", href: `#s-${encodeURIComponent(s.id)}` },
          h("div", { class: "top" }, h("span", { class: "name" }, s.title), flagRow(s), chip(st)),
          currentTask(state.tasks, s.id) ? h("p", { class: "curtask" }, `Task: ${oneLine(currentTask(state.tasks, s.id).text)}`) : null,
          s.message ? h("p", { class: "msg" }, oneLine(s.message.text)) : null,
          h("div", { class: "meta" },
            last ? h("span", {}, `${last.hash} · ${ago(last.at, now)}`) : null,
            h("span", {}, pushLabel(s.git)),
            s.git && s.git.dirty ? h("span", {}, `${s.git.dirty} changed`) : null,
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
    const rows = [["state", chip(st)], ["aoe", s.aoe_status], ["path", s.path]];
    const task = currentTask(state.tasks, s.id);
    if (task) rows.unshift(["task", `${task.text} (sent ${ago(task.dispatched_at, now)} ago)`]);
    if (g) {
      rows.push(["branch", g.upstream ? `${g.branch} → ${g.upstream}` : g.branch || "detached"]);
      if (g.last) rows.push(["commit", `${g.last.hash} ${g.last.subject} (${ago(g.last.at, now)})`]);
      rows.push(["push", pushLabel(g)]);
      rows.push(["tree", g.dirty ? `${g.dirty} changed files` : "clean"]);
    }
    const sent = h("div", { class: "reply sent" });
    sent.innerHTML = s.prompt ? renderMarkdown(s.prompt.text) : "<p>Nothing sent yet.</p>";
    const msg = h("div", { class: "reply" });
    msg.innerHTML = s.message ? renderMarkdown(s.message.text) : "<p>No reply yet.</p>";
    return h("article", { class: "detail" },
      h("a", { class: "back", href: "#" }, "← Workers"),
      h("h2", {}, s.title, flagRow(s)),
      h("dl", {}, rows.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)])),
      g && g.pending && g.pending.length ? pending(g, now) : null,
      h("h3", {}, "Last sent", s.prompt ? h("span", { class: "when" }, ` · ${ago(s.prompt.at, now)}`) : null),
      sent,
      h("h3", {}, "Last reply", s.message ? h("span", { class: "when" }, ` · ${ago(s.message.at, now)}`) : null),
      msg,
      actions(s),
    );
  }

  function actions(s) {
    const text = h("textarea", { id: `reply-${s.id}`, rows: "3", placeholder: "Reply to the agent…", "aria-label": "Reply" });
    const reply = async () => {
      const v = text.value.trim();
      if (v && (await sendCommand("reply", { session: s.id, text: v }, `Reply to ${s.title}`))) text.value = "";
    };
    const g = s.git;
    const busy = s.state === "working" || s.state === "needs_input";
    return h("div", { class: "actions" },
      text,
      h("div", { class: "btnrow" },
        h("button", { type: "button", class: "primary", onclick: reply }, "Send reply"),
        g && g.unpushed > 0 ? h("button", { type: "button", onclick: () => sendCommand("push", { session: s.id }, `Push ${s.title}`) }, `Push ${g.unpushed}`) : null,
        s.cleared || busy ? null : h("button", { type: "button", onclick: () => sendCommand("clear", { session: s.id }, `Clear ${s.title}`) }, "Clear"),
        s.aoe_status !== "stopped" ? h("button", { type: "button", onclick: () => sendCommand("stop", { session: s.id }, `Stop ${s.title}`) }, "Stop") : null,
      ),
    );
  }
}

function pending(g, now) {
  const more = g.unpushed - g.pending.length;
  return [
    h("h3", {}, `Unpushed commits · ${g.unpushed}`),
    h("ul", { class: "pending" },
      g.pending.map((c) => h("li", {}, h("code", {}, c.hash), ` ${c.subject}`, h("span", { class: "when" }, ago(c.at, now)))),
      more > 0 ? h("li", { class: "when" }, `and ${more} older`) : null,
    ),
  ];
}

function flagRow(s) {
  const f = flags(s);
  return f.length ? h("span", { class: "flags" }, f.map((x) => h("span", { class: `flag ${x}` }, x))) : null;
}

function chip(st) {
  return h("span", { class: `chip ${st.tone}` }, st.label);
}

function oneLine(text) {
  const s = String(text).replace(/[`*#]/g, "").replace(/\s+/g, " ").trim();
  return s.length > 160 ? `${s.slice(0, 159)}…` : s;
}

