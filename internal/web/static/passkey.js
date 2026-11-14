// Passkeys sign commands, directly or by approving a session key; the laptop
// verifies them against keys it was given at pairing. The challenge is SHA-256 of the exact payload string stored with
// the command, so the signature covers what the laptop will act on.

export const b64url = (buf) =>
  btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
export const b64 = (buf) => btoa(String.fromCharCode(...new Uint8Array(buf)));
const fromB64 = (s) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));

export async function sha256(text) {
  return crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
}

// Matches command.Fingerprint on the laptop: the first six bytes of the
// public key's SHA-256, as three groups of four hex digits.
export async function fingerprint(publicKeyB64) {
  const sum = new Uint8Array(await crypto.subtle.digest("SHA-256", fromB64(publicKeyB64)));
  const hex = [...sum.slice(0, 6)].map((b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 4)} ${hex.slice(4, 8)} ${hex.slice(8, 12)}`;
}

export async function createPasskey(user) {
  const cred = await navigator.credentials.create({
    publicKey: {
      rp: { id: location.hostname, name: "peyk" },
      user: { id: new TextEncoder().encode(user.uid), name: user.email, displayName: user.email },
      challenge: crypto.getRandomValues(new Uint8Array(32)),
      pubKeyCredParams: [{ type: "public-key", alg: -7 }, { type: "public-key", alg: -257 }],
      authenticatorSelection: { residentKey: "required", userVerification: "required" },
      attestation: "none",
    },
  });
  const res = cred.response;
  if (typeof res.getPublicKey !== "function") throw new Error("This browser cannot export the passkey's public key.");
  return { credential_id: b64url(cred.rawId), public_key: b64(res.getPublicKey()), alg: res.getPublicKeyAlgorithm() };
}

// One passkey approval lasts this long; the laptop refuses longer (MaxGrant).
const GRANT_MS = 12 * 60 * 60 * 1000;
// A grant this close to expiry is renewed first, so a command signed with it
// cannot reach the laptop after it ran out.
const GRANT_MARGIN_MS = 5 * 60 * 1000;

let grant = null;
let granting = null;

// signCommand signs a payload with this browser's session key, asking the
// passkey to approve a new session key when there is none or it is about to
// run out. The private key is generated non-extractable and kept in
// IndexedDB, so a reload keeps the session without the key leaving WebCrypto.
// onApprove runs before the passkey prompt, when there is one. Concurrent
// commands share one prompt.
export async function signCommand(payload, onApprove = () => {}) {
  grant ??= await loadGrant();
  if (!grant || grant.expires - Date.now() < GRANT_MARGIN_MS) {
    onApprove();
    if (!granting) {
      granting = newGrant().finally(() => { granting = null; });
    }
    grant = await granting;
  }
  const sig = await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, grant.privateKey, new TextEncoder().encode(payload));
  return { statement: grant.statement, assertion: grant.assertion, signature: b64url(sig) };
}

// forgetGrant drops a grant the laptop refused, so the next command asks the
// passkey again.
export async function forgetGrant() {
  grant = null;
  await withStore("readwrite", (s) => s.delete("current")).catch(() => {});
}

async function newGrant() {
  const pair = await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, false, ["sign"]);
  const expires = Date.now() + GRANT_MS;
  const key = b64(await crypto.subtle.exportKey("spki", pair.publicKey));
  const statement = JSON.stringify({ kind: "peyk-session", key, expires });
  const g = { privateKey: pair.privateKey, statement, assertion: await sign(statement), expires };
  await withStore("readwrite", (s) => s.put(g, "current")).catch(() => {});
  return g;
}

async function loadGrant() {
  return (await withStore("readonly", (s) => s.get("current")).catch(() => null)) ?? null;
}

function withStore(mode, op) {
  return new Promise((resolve, reject) => {
    const open = indexedDB.open("peyk", 1);
    open.onupgradeneeded = () => open.result.createObjectStore("grant");
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const db = open.result;
      const req = op(db.transaction("grant", mode).objectStore("grant"));
      req.onsuccess = () => { db.close(); resolve(req.result); };
      req.onerror = () => { db.close(); reject(req.error); };
    };
  });
}

export async function sign(payload) {
  const cred = await navigator.credentials.get({
    publicKey: {
      challenge: await sha256(payload),
      rpId: location.hostname,
      userVerification: "required",
      allowCredentials: [],
    },
  });
  const r = cred.response;
  return {
    credential_id: b64url(cred.rawId),
    authenticator_data: b64url(r.authenticatorData),
    client_data_json: b64url(r.clientDataJSON),
    signature: b64url(r.signature),
  };
}

// urlB64ToBytes decodes the VAPID public key for pushManager.subscribe.
export function urlB64ToBytes(s) {
  const b = s.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (s.length % 4)) % 4);
  return Uint8Array.from(atob(b), (c) => c.charCodeAt(0));
}
