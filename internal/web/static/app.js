import { renderMarkdown } from "./markdown.js";
import { STATES, ago, laptopOnline, pushLabel, sortSessions } from "./board.js";

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

  const state = { sessions: new Map(), laptop: null, user: null };
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
    notice(`Cannot read sessions: ${err.code === "permission-denied" ? "this account is not the owner." : err.message}`);
  }

  document.addEventListener("visibilitychange", () => {
    if (!state.user) return;
    if (document.hidden) stop();
    else listen();
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
    const id = location.hash.startsWith("#s-") ? decodeURIComponent(location.hash.slice(3)) : null;
    const s = id && state.sessions.get(id);
    if (s) app.replaceChildren(detail(s));
    else app.replaceChildren(board());
  }

  function board() {
    const now = Date.now();
    const list = sortSessions([...state.sessions.values()]);
    if (!list.length) return h("p", { class: "notice" }, "No sessions published yet. Start the agent on the laptop with make serve.");
    const out = [];
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
          h("div", { class: "top" }, h("span", { class: "name" }, s.title), chip(st)),
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
    if (g) {
      rows.push(["branch", g.upstream ? `${g.branch} → ${g.upstream}` : g.branch || "detached"]);
      if (g.last) rows.push(["commit", `${g.last.hash} ${g.last.subject} (${ago(g.last.at, now)})`]);
      rows.push(["push", pushLabel(g)]);
      rows.push(["tree", g.dirty ? `${g.dirty} changed files` : "clean"]);
    }
    const msg = h("div", { class: "reply" });
    msg.innerHTML = s.message ? renderMarkdown(s.message.text) : "<p>No reply yet.</p>";
    return h("article", { class: "detail" },
      h("a", { class: "back", href: "#" }, "← Sessions"),
      h("h2", {}, s.title),
      h("dl", {}, rows.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)])),
      h("h3", {}, "Last reply", s.message ? h("span", { class: "when" }, ` · ${ago(s.message.at, now)}`) : null),
      msg,
    );
  }
}

function chip(st) {
  return h("span", { class: `chip ${st.tone}` }, st.label);
}

function oneLine(text) {
  const s = String(text).replace(/[`*#]/g, "").replace(/\s+/g, " ").trim();
  return s.length > 160 ? `${s.slice(0, 159)}…` : s;
}

