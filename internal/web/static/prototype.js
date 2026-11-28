// Scoped to /p/ so opening a prototype does not recreate a root / worker that
// would intercept Android WebAPK notification delegation for /app/.
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js", { scope: "/p/" }).catch(() => {});
}

const statusEl = document.getElementById("status");
const config = document.body.dataset.firebase;
const id = document.body.dataset.id;

function show(text, ...extra) {
  const p = document.createElement("p");
  p.textContent = text;
  statusEl.replaceChildren(p, ...extra);
}

function button(label, onclick) {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = label;
  b.addEventListener("click", onclick);
  return b;
}

if (!config) {
  show("This copy of peyk is not connected to Firebase.");
} else {
  start(JSON.parse(config)).catch((err) => show(`Could not start: ${err.message}`));
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

  const signIn = () =>
    auth.signInWithPopup(a, new auth.GoogleAuthProvider()).catch((err) => show(`Sign-in failed: ${err.message}`));

  auth.onAuthStateChanged(a, async (user) => {
    if (!user) {
      show("Sign in with the Google account that owns peyk.", button("Sign in with Google", signIn));
      return;
    }
    const token = await user.getIdTokenResult();
    if (!token.claims.owner) {
      show(`${user.email} is signed in but is not the owner.`, button("Sign out", () => auth.signOut(a)));
      return;
    }
    try {
      const snap = await fs.getDoc(fs.doc(db, "prototypes", id));
      if (!snap.exists()) {
        show("Prototype not found.");
        return;
      }
      const data = snap.data();
      const title = data.title || "Prototype";
      document.title = title;
      const frame = document.createElement("iframe");
      frame.src = "/sandbox";
      frame.setAttribute("sandbox", "allow-scripts");
      frame.title = title;
      const onMessage = (e) => {
        if (e.source !== frame.contentWindow || e.data?.type !== "peyk-sandbox-ready") return;
        window.removeEventListener("message", onMessage);
        frame.contentWindow.postMessage({ type: "peyk-prototype", html: data.html }, "*");
      };
      window.addEventListener("message", onMessage);
      document.body.replaceChildren(frame);
    } catch (err) {
      show(`Could not load prototype: ${err.message}`);
    }
  });
}
