// Passkeys sign commands; the laptop verifies them against keys it was given
// at pairing. The challenge is SHA-256 of the exact payload string stored with
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
