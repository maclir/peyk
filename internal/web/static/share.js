// What another app shared into the installed app. The service worker keeps
// it between the share's POST and the page it redirects to, because the share
// arrives before anyone is signed in to store it; sw.js writes this database
// with the same name, version and store.

const DB = "peyk-share";
const STORE = "shares";
const KEY = "pending";

function openDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

// Reads and forgets the waiting share, so a reload does not attach it twice.
export async function takeShare() {
  const db = await openDB();
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, "readwrite");
      const store = tx.objectStore(STORE);
      const get = store.get(KEY);
      store.delete(KEY);
      tx.oncomplete = () => resolve(get.result ?? null);
      tx.onerror = () => reject(tx.error);
    });
  } finally {
    db.close();
  }
}

// Apps fill title, text and url inconsistently, and often repeat the link in
// the text, so each distinct part is kept once.
export function sharedText({ title, text, url } = {}) {
  const parts = [];
  for (const part of [title, text, url]) {
    const v = typeof part === "string" ? part.trim() : "";
    if (v && !parts.some((p) => p.includes(v))) parts.push(v);
  }
  return parts.join("\n");
}
