// The only script the scaffold ships. It exists to register the worker, which
// is what lets the app open when the network does not — and it is a file rather
// than an inline script because the page's own policy refuses inline scripts,
// which is a rule worth keeping.
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}
