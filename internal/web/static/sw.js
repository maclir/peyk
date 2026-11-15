// The worker is what makes this an app rather than a page: it opens when the
// network does not.
//
// Only the shell is cached — the icons, and a document that says the app is
// offline. Pages are never cached, because every page here is rendered from
// data and a stale answer presented as a current one is worse than no answer.
// Serve data from the network, and fall back to something honest.
//
// The service names this cache after a digest of every asset it serves, so a
// changed file is a changed worker, and a changed worker is a rebuilt cache.
// There is no number here to remember to raise.

const SHELL = "peyk-__VERSION__";
const OFFLINE = "/offline";
// The Firebase modules are most of the app's weight; cached with the shell
// they download once per release instead of on every launch.
const ASSETS = [
  OFFLINE,
  "/static/icon.svg",
  "/static/app.css",
  "/static/app.js",
  "/static/board.js",
  "/static/markdown.js",
  "/static/images.js",
  "/static/passkey.js",
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
  if (event.request.method !== "GET" || url.origin !== self.location.origin) return;

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

// Notifications from the laptop. The payload is encrypted to this browser's
// subscription key; only here is it readable.
self.addEventListener("push", (event) => {
  let note = { title: "peyk", body: "", url: "/", session: "" };
  try {
    note = { ...note, ...event.data.json() };
  } catch {
    note.body = event.data ? event.data.text() : "";
  }
  event.waitUntil(
    self.registration.showNotification(note.title, {
      body: note.body,
      tag: note.session || undefined,
      renotify: Boolean(note.session),
      icon: "/static/icon-192.png",
      badge: "/static/notification-badge.png",
      data: { url: note.url },
    })
      // The page dismisses a worker's notes when it renders that worker, but
      // the push for a change can land after the snapshot that rendered it.
      // Safari revokes push for a subscription that receives a push without
      // showing a notification, so the note is shown first and the page closes it.
      .then(() => self.clients.matchAll({ type: "window" }))
      .then((wins) => wins.forEach((w) => w.postMessage({ type: "notified", session: note.session }))),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = new URL(event.notification.data?.url || "/", self.location.origin).href;
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((wins) => {
      const win = wins.find((w) => new URL(w.url).origin === self.location.origin);
      if (win) return win.navigate(url).then((w) => (w || win).focus());
      return self.clients.openWindow(url);
    }),
  );
});
