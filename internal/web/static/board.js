// What the board shows for a session, independent of Firebase and the DOM.

export const STATES = {
  needs_input: { label: "needs input", tone: "bad", group: "Needs you" },
  error: { label: "error", tone: "bad", group: "Needs you" },
  dirty: { label: "uncommitted", tone: "bad", group: "Needs you" },
  unpushed: { label: "unpushed", tone: "warn", group: "Ready to push" },
  working: { label: "working", tone: "run", group: "Working" },
  idle: { label: "idle", tone: "idle", group: "Idle" },
  stopped: { label: "stopped", tone: "idle", group: "Stopped" },
};
const ORDER = Object.keys(STATES);

export function sortSessions(sessions) {
  return [...sessions].sort(
    (a, b) => rank(a.state) - rank(b.state) || String(a.title).localeCompare(String(b.title)),
  );
}

function rank(state) {
  const i = ORDER.indexOf(state);
  return i === -1 ? ORDER.length : i;
}

// The laptop is offline when the agent said so on exit, or when it has not
// been seen for longer than a missed heartbeat explains (sleep, crash).
export const STALE_AFTER_MS = 15 * 60 * 1000;

export function laptopOnline(status, now) {
  if (!status || !status.online || !status.seen_at) return false;
  return now - toDate(status.seen_at).getTime() < STALE_AFTER_MS;
}

export function toDate(v) {
  if (!v) return null;
  if (v instanceof Date) return v;
  if (typeof v.toDate === "function") return v.toDate();
  return new Date(v);
}

export function ago(date, now) {
  if (!date) return "";
  const s = Math.max(0, (now - toDate(date).getTime()) / 1000);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 48 * 3600) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export function pushLabel(git) {
  if (!git) return "no repository";
  if (git.unpushed > 0 && !git.upstream) return `${git.unpushed} unpushed · no remote branch`;
  if (git.unpushed > 0) return `${git.unpushed} unpushed`;
  if (git.upstream) return "pushed";
  return "no remote";
}
