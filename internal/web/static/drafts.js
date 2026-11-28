// Unsent message text kept in localStorage across navigation and reloads.
// Each input gets its own key so open tabs never overwrite each other's
// drafts; a blocked or full storage falls back to memory for the life of the
// page.

export const DRAFT_PREFIX = "peyk:draft:";
export const MAX_DRAFTS = 50;
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
