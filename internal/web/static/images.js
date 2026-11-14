// Images attached to inbox tasks. Each is stored as one Firestore document,
// which holds at most 1 MiB, so larger images are re-encoded smaller.

export const MAX_IMAGES = 4;
export const MAX_BYTES = 1_000_000;
const KEPT = ["image/png", "image/jpeg", "image/webp", "image/gif"];
const MAX_SIDE = 2048;

// An image small enough is kept byte for byte, so a screenshot's text stays
// sharp; anything else becomes a JPEG that fits.
export async function prepareImage(file) {
  if (KEPT.includes(file.type) && file.size <= MAX_BYTES) {
    return { bytes: new Uint8Array(await file.arrayBuffer()), type: file.type };
  }
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error(`${file.name || "the image"} is not an image this browser can read`);
  });
  const longest = Math.max(bitmap.width, bitmap.height);
  let side = Math.min(MAX_SIDE, longest);
  let quality = 0.85;
  for (;;) {
    const scale = side / longest;
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    canvas.getContext("2d").drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/jpeg", quality));
    if (blob && blob.size <= MAX_BYTES) return { bytes: new Uint8Array(await blob.arrayBuffer()), type: "image/jpeg" };
    if (quality > 0.6) quality -= 0.125;
    else side = Math.round(side * 0.75);
    if (side < 256) throw new Error(`${file.name || "the image"} cannot be made small enough`);
  }
}

// The image's id: its SHA-256 in hex, which the laptop checks the bytes
// against before using them.
export async function imageID(bytes) {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return [...digest].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// Images no remaining task refers to, once the given tasks are deleted.
export function orphanedImages(tasks, deleted) {
  const gone = new Set(deleted.map((t) => t.id));
  const kept = new Set(tasks.filter((t) => !gone.has(t.id)).flatMap((t) => t.images || []));
  return [...new Set(deleted.flatMap((t) => t.images || []))].filter((id) => !kept.has(id));
}
