// Unsent message text (localStorage) and screenshots (IndexedDB) kept across
// navigation, reloads, and Android share navigations. Each composer gets its
// own key so open tabs never overwrite each other's drafts; blocked or full
// storage falls back to memory for the life of the page.

export const DRAFT_PREFIX = "peyk:draft:";
export const MAX_DRAFTS = 50;
export const MAX_IMAGE_DRAFTS = 20;
export const MAX_DRAFT_AGE_MS = 30 * 24 * 60 * 60 * 1000;

export function createDraftStore(storage = defaultStorage(), now = () => Date.now()) {
  const memory = new Map();

  function readEntry(fullKey) {
    if (!storage) return null;
    try {
      const raw = storage.getItem(fullKey);
      if (raw == null) return null;
      const parsed = JSON.parse(raw);
      if (!parsed || typeof parsed.text !== "string" || !Number.isFinite(parsed.at)) {
        storage.removeItem(fullKey);
        return null;
      }
      if (now() - parsed.at > MAX_DRAFT_AGE_MS) {
        storage.removeItem(fullKey);
        return null;
      }
      return parsed;
    } catch {
      try { storage.removeItem(fullKey); } catch {}
      return null;
    }
  }

  function draftKeys() {
    if (!storage) return [];
    const keys = [];
    try {
      for (let i = 0; i < storage.length; i++) {
        const k = storage.key(i);
        if (k && k.startsWith(DRAFT_PREFIX)) keys.push(k);
      }
    } catch {
      return [];
    }
    return keys;
  }

  function listEntries() {
    const entries = [];
    for (const k of draftKeys()) {
      const entry = readEntry(k);
      if (entry) entries.push({ key: k, at: entry.at });
    }
    return entries;
  }

  function prune(keepKey) {
    if (!storage || draftKeys().length <= MAX_DRAFTS) return;
    const entries = listEntries().filter((e) => e.key !== keepKey);
    entries.sort((a, b) => a.at - b.at);
    const excess = entries.length - Math.max(0, MAX_DRAFTS - 1);
    for (let i = 0; i < excess; i++) {
      try { storage.removeItem(entries[i].key); } catch {}
    }
  }

  function get(key) {
    if (memory.has(key)) return memory.get(key);
    return readEntry(DRAFT_PREFIX + key)?.text ?? null;
  }

  function del(key) {
    memory.delete(key);
    if (!storage) return;
    try { storage.removeItem(DRAFT_PREFIX + key); } catch {}
  }

  function set(key, text, defaultText = "") {
    if (typeof text !== "string" || !text.trim() || text === defaultText) {
      del(key);
      return;
    }
    const fullKey = DRAFT_PREFIX + key;
    if (!storage) {
      memory.set(key, text);
      return;
    }
    const payload = JSON.stringify({ text, at: now() });
    try {
      storage.setItem(fullKey, payload);
      memory.delete(key);
      prune(fullKey);
    } catch {
      // Storage may be full (QuotaExceededError); clear expired entries first,
      // then evict the oldest drafts until the write fits.
      const entries = listEntries().filter((e) => e.key !== fullKey).sort((a, b) => a.at - b.at);
      let stored = false;
      for (let i = 0; i <= entries.length; i++) {
        if (i > 0) {
          try { storage.removeItem(entries[i - 1].key); } catch {}
        }
        try {
          storage.setItem(fullKey, payload);
          memory.delete(key);
          stored = true;
          break;
        } catch {}
      }
      if (!stored) memory.set(key, text);
    }
  }

  return { get, set, delete: del };
}

function defaultStorage() {
  try {
    return typeof globalThis.localStorage !== "undefined" ? globalThis.localStorage : null;
  } catch {
    return null;
  }
}

// Draft screenshots live in IndexedDB because up to four 1 MB images per
// message will not fit in localStorage. Mutations run through one queue so a
// quick attach followed by send or remove can never commit out of order.
export function createImageDraftStore(backend = defaultImageBackend(), now = () => Date.now()) {
  let queue = Promise.resolve();
  const enqueue = (fn) => {
    const next = queue.then(fn, fn);
    queue = next.catch(() => {});
    return next;
  };

  function normalizeImage(img) {
    if (!img || typeof img !== "object") return null;
    if (img.bytes && typeof img.type === "string") {
      const bytes = img.bytes instanceof Uint8Array ? new Uint8Array(img.bytes) : new Uint8Array(img.bytes);
      return bytes.byteLength ? { bytes, type: img.type } : null;
    }
    if (typeof img.id === "string" && img.id) return { id: img.id };
    return null;
  }

  function validEntry(val) {
    if (!val || typeof val !== "object" || !Number.isFinite(val.at) || !Array.isArray(val.images)) return null;
    if (now() - val.at > MAX_DRAFT_AGE_MS) return null;
    const images = val.images.map(normalizeImage);
    if (images.some((x) => !x)) return null;
    return { at: val.at, images };
  }

  async function scanValid() {
    if (!backend) return [];
    let raw;
    try {
      raw = await backend.getAll();
    } catch {
      return [];
    }
    const kept = [];
    for (const [key, val] of raw) {
      const entry = validEntry(val);
      if (entry) kept.push({ key, ...entry });
      else await backend.delete(key).catch(() => {});
    }
    return kept;
  }

  function loadAll() {
    return enqueue(async () => {
      const valid = await scanValid();
      return new Map(valid.map((e) => [e.key, e.images]));
    });
  }

  function del(key) {
    return enqueue(async () => {
      if (!backend) return;
      await backend.delete(key).catch(() => {});
    });
  }

  function set(key, images, { keepEmpty = false } = {}) {
    const serialized = Array.isArray(images) ? images.map(normalizeImage).filter(Boolean) : [];
    if (!serialized.length && !keepEmpty) return del(key);
    return enqueue(async () => {
      if (!backend) return;
      const entry = { at: now(), images: serialized };
      try {
        await backend.put(key, entry);
      } catch {
        // Storage may be full; clear expired entries, then evict oldest drafts
        // until the write fits.
        const others = (await scanValid()).filter((e) => e.key !== key).sort((a, b) => a.at - b.at);
        for (let i = 0; i <= others.length; i++) {
          if (i > 0) await backend.delete(others[i - 1].key).catch(() => {});
          try {
            await backend.put(key, entry);
            break;
          } catch {}
        }
        return;
      }
      const allKeys = backend.keys ? await backend.keys().catch(() => []) : (await scanValid()).map((e) => e.key);
      if (allKeys.length <= MAX_IMAGE_DRAFTS) return;
      const others = (await scanValid()).filter((e) => e.key !== key);
      if (others.length <= MAX_IMAGE_DRAFTS - 1) return;
      others.sort((a, b) => a.at - b.at);
      const excess = others.length - Math.max(0, MAX_IMAGE_DRAFTS - 1);
      for (let i = 0; i < excess; i++) {
        await backend.delete(others[i].key).catch(() => {});
      }
    });
  }

  return { loadAll, set, delete: del };
}

const IMAGE_DB = "peyk-drafts";
const IMAGE_STORE = "images";

function defaultImageBackend() {
  try {
    if (typeof globalThis.indexedDB === "undefined") return null;
  } catch {
    return null;
  }
  const open = () => new Promise((resolve, reject) => {
    const req = globalThis.indexedDB.open(IMAGE_DB, 1);
    req.onupgradeneeded = () => {
      if (!req.result.objectStoreNames.contains(IMAGE_STORE)) req.result.createObjectStore(IMAGE_STORE);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  const withStore = async (mode, work) => {
    const db = await open();
    try {
      return await new Promise((resolve, reject) => {
        const tx = db.transaction(IMAGE_STORE, mode);
        const result = work(tx.objectStore(IMAGE_STORE));
        tx.oncomplete = () => resolve(result());
        tx.onerror = () => reject(tx.error);
        tx.onabort = () => reject(tx.error || new Error("transaction aborted"));
      });
    } finally {
      db.close();
    }
  };
  return {
    keys: () => withStore("readonly", (store) => {
      const req = store.getAllKeys();
      return () => req.result;
    }),
    getAll: () => withStore("readonly", (store) => {
      const keys = store.getAllKeys();
      const vals = store.getAll();
      return () => keys.result.map((k, i) => [k, vals.result[i]]);
    }),
    put: (key, value) => withStore("readwrite", (store) => {
      store.put(value, key);
      return () => {};
    }),
    delete: (key) => withStore("readwrite", (store) => {
      store.delete(key);
      return () => {};
    }),
  };
}
