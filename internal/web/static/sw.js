// The worker is what makes this an app rather than a page: it opens when the
// network does not.
//
// Only the shell is cached — the icons, and a document that says the app is
// offline. Pages are never cached, because every page here is rendered from
// data and a stale answer presented as a current one is worse than no answer.
// Serve data from the network, and fall back to something honest.
//
// The service names this cache after a digest of every asset it serves and the
// revision, so a changed file or deploy is a changed worker, and a changed
// worker is a rebuilt cache. There is no number here to remember to raise.

const SHELL = "peyk-__VERSION__";
const OFFLINE = "/offline";
// The Firebase modules are most of the app's weight; cached with the shell
// they download once per release instead of on every launch.
const ASSETS = [
  OFFLINE,
  "/static/icon.svg",
  "/static/app.css",
  "/static/app.js",
  "/static/prototype.js",
  "/static/release.js",
  "/static/board.js",
  "/static/markdown.js",
  "/static/images.js",
  "/static/passkey.js",
  "/static/share.js",
  "/static/logos/claude.svg",
  "/static/logos/antigravity.png",
  "/static/vendor/firebase-app.js",
  "/static/vendor/firebase-auth.js",
  "/static/vendor/firebase-firestore.js",
];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL).then((cache) => cache.addAll(ASSETS)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((key) => key !== SHELL).map((key) => caches.delete(key))),
    ),
  );
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;
  if (event.request.method === "POST" && url.pathname === SHARE) {
    event.respondWith(keepShare(event.request));
    return;
  }
  if (event.request.method !== "GET") return;

  // An installed icon has no browser chrome behind it, so a launch with no
  // signal has to reach the app rather than the browser's error page.
  if (event.request.mode === "navigate") {
    event.respondWith(fetch(event.request).catch(() => caches.match(OFFLINE)));
    return;
  }

  event.respondWith(
    caches.match(event.request).then((hit) => hit || fetch(event.request)),
  );
});

// The manifest's share target. Shared files never reach the server: they wait
// here, in the database share.js reads, until a signed-in page attaches them
// to a new task's draft. A newer share replaces one never opened.
const SHARE = "/app/share";

async function keepShare(request) {
  try {
    const form = await request.formData();
    const field = (k) => (typeof form.get(k) === "string" ? form.get(k) : "");
    const share = {
      title: field("title"), text: field("text"), url: field("url"),
      files: form.getAll("images").filter((f) => typeof f !== "string" && f.size),
    };
    const db = await new Promise((resolve, reject) => {
      const req = indexedDB.open("peyk-share", 1);
      req.onupgradeneeded = () => req.result.createObjectStore("shares");
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
    try {
      await new Promise((resolve, reject) => {
        const tx = db.transaction("shares", "readwrite");
        tx.objectStore("shares").put(share, "pending");
        tx.oncomplete = resolve;
        tx.onerror = () => reject(tx.error);
      });
    } finally {
      db.close();
    }
    await log("share", { files: share.files.length, text: Boolean(share.title || share.text || share.url) });
  } catch (err) {
    await log("share failed", { error: String(err) });
  }
  return Response.redirect("/app/#new", 303);
}

// Notifications from the laptop. The payload is encrypted to this browser's
// subscription key; only here is it readable.
self.addEventListener("push", (event) => {
  let note = { title: "peyk", body: "", url: "/app/", session: "" };
  try {
    note = { ...note, ...event.data.json() };
  } catch {
    note.body = event.data ? event.data.text() : "";
  }
  event.waitUntil(notify(note));
});

// Browsers require every push to show a notification: Safari revokes the
// subscription otherwise, and Chrome shows a generic one in its place. A note
// for the worker already on screen is shown silently and closed at once,
// except on Android: there a note closed as soon as it is shown stays in the
// tray, while Chrome forgets it, so nothing can close it later. Chrome on
// Android asks for no notification while the app is on screen.
let closeLeavesNote = /Android/.test(self.navigator.userAgent);

async function notify(note) {
  if (note.dismiss) return dismiss(note.session);
  const visible = await visibleWindows();
  const watched = Boolean(note.session) && (await watching(visible, note.session));
  await log("push", { session: note.session, watched, windows: describe(visible) });
  if (watched && closeLeavesNote) return;
  try {
    await self.registration.showNotification(note.title, {
      body: note.body,
      tag: note.session || undefined,
      renotify: Boolean(note.session) && !watched,
      silent: watched,
      badge: "/static/notification-badge.png",
      icon: note.icon || undefined,
      data: { url: note.url },
    });
  } catch (err) {
    await log("show failed", { session: note.session, error: String(err) });
    throw err;
  }
  if (watched) (await self.registration.getNotifications({ tag: note.session })).forEach((n) => n.close());
}

// A dismissal only closes. A placeholder shown and closed at once can stay on
// an installed Android app as an empty notification, and Chrome judges a
// push by what is open when it ends, so a closed placeholder counts as none.
async function dismiss(session) {
  const open = await self.registration.getNotifications({ tag: session });
  open.forEach((n) => n.close());
  await log("dismissed elsewhere", { session, closed: open.length });
}

async function visibleWindows() {
  return (await self.clients.matchAll({ type: "window" })).filter((w) => w.visibilityState === "visible");
}

// A window's url here does not follow its hash, so only the page knows which
// worker it shows. A frozen page never answers.
async function watching(wins, session) {
  return (await Promise.all(wins.map((w) => ask(w, { type: "showing", session }, 500)))).some((a) => a === true);
}

function ask(win, message, ms) {
  return new Promise((resolve) => {
    const { port1, port2 } = new MessageChannel();
    port1.onmessage = (e) => resolve(e.data);
    setTimeout(() => resolve(undefined), ms);
    win.postMessage(message, [port2]);
  });
}

const describe = (wins) => wins.map((w) => ({ url: w.url, visibility: w.visibilityState, focused: w.focused }));

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(open(event.notification.data?.url || "/app/", event.notification.tag));
});

// The page routes by its hash, so an open window is told where to go rather
// than reloaded. Focus comes first: browsers allow it only briefly after the
// click, and a navigation can outlast that or never settle for a hash-only
// change. A page that does not answer, frozen or from an older release, is
// navigated instead.
async function open(path, tag) {
  const url = new URL(path, self.location.origin).href;
  const wins = (await self.clients.matchAll({ type: "window", includeUncontrolled: true }))
    .filter((w) => w.url.startsWith(self.registration.scope));
  const win = wins.find((w) => w.focused) || wins.find((w) => w.visibilityState === "visible") || wins[0];
  const entry = { tag, url, windows: describe(wins) };
  try {
    if (!win) {
      entry.opened = Boolean(await self.clients.openWindow(url));
      return;
    }
    entry.focused = await win.focus().then(() => true, (err) => String(err));
    entry.answered = (await ask(win, { type: "open", url }, 1000)) === true;
    if (!entry.answered) entry.navigated = Boolean(await win.navigate(url));
  } catch (err) {
    entry.error = String(err);
  } finally {
    await log("click", entry);
  }
}

// What each push and click found, kept on the device because a phone's
// service worker has no console anyone can read. The page adds its own
// entries and reads them back through messages, since only the worker opens
// this database.
const LOG_LIMIT = 200;

function logDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open("peyk-log", 1);
    req.onupgradeneeded = () => req.result.createObjectStore("events", { autoIncrement: true });
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function logTx(mode, work) {
  const db = await logDB();
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction("events", mode);
      const result = work(tx.objectStore("events"));
      tx.oncomplete = () => resolve(result.result);
      tx.onerror = () => reject(tx.error);
    });
  } finally {
    db.close();
  }
}

async function log(event, detail) {
  try {
    await logTx("readwrite", (store) => {
      const add = store.add({ at: new Date().toISOString(), release: SHELL, event, ...detail });
      add.onsuccess = () => store.delete(IDBKeyRange.upperBound(add.result - LOG_LIMIT));
      return add;
    });
  } catch {
    // A log that cannot be written must not cost the user a notification.
  }
}

self.addEventListener("message", (event) => {
  const data = event.data || {};
  if (data.type === "log") event.waitUntil(log(data.event, data.detail));
  if (data.type === "read log") {
    event.waitUntil(logTx("readonly", (store) => store.getAll()).catch((err) => [{ event: "log unreadable", error: String(err) }])
      .then((entries) => event.ports[0]?.postMessage(entries)));
  }
});
