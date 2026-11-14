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
const ASSETS = [OFFLINE, "/static/icon.svg"];

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
