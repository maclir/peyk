// What the board shows for a session, independent of Firebase and the DOM.

export const STATES = {
  needs_input: { label: "needs input", tone: "bad", group: "Needs you" },
  error: { label: "error", tone: "bad", group: "Needs you" },
  dirty: { label: "uncommitted", tone: "bad", group: "Needs you" },
  unpushed: { label: "unpushed", tone: "ok", group: "Ready to push" },
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

// The picker shows recently active projects first; never-active ones follow
// by name.
export function sortProjects(projects) {
  return [...projects].sort((a, b) => {
    const ta = a.active ? toDate(a.active).getTime() : 0;
    const tb = b.active ? toDate(b.active).getTime() : 0;
    return tb - ta || String(a.name).localeCompare(String(b.name));
  });
}

// Groups queued tasks into what can be sent now and what waits on a busy or
// unknown project, oldest first in each.
export function inboxGroups(tasks, projects) {
  const byName = new Map(projects.map((p) => [p.name, p]));
  const ready = [];
  const waiting = [];
  const queued = tasks
    .filter((t) => t.status === "queued")
    .sort((a, b) => toDate(a.created_at) - toDate(b.created_at));
  for (const t of queued) {
    const p = byName.get(t.project);
    if (p && p.free) ready.push(t);
    else waiting.push({ ...t, why: p ? p.busy : "project no longer exists" });
  }
  return { ready, waiting };
}

// The task a session is working on: the most recently dispatched one.
export function currentTask(tasks, sessionId) {
  return tasks
    .filter((t) => t.status === "dispatched" && t.session_id === sessionId)
    .sort((a, b) => toDate(b.dispatched_at) - toDate(a.dispatched_at))[0] || null;
}

// Finished tasks, newest first.
export function doneTasks(tasks, limit = 10) {
  return tasks
    .filter((t) => t.status === "done")
    .sort((a, b) => toDate(b.done_at) - toDate(a.done_at))
    .slice(0, limit);
}
