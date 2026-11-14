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

// Flags say what a worker is ready for: cleared when its conversation is
// empty, clean when its repository has nothing uncommitted or unpushed.
export function flags(s) {
  const out = [];
  if (s.cleared) out.push("cleared");
  if (s.git && !s.git.dirty && !s.git.unpushed) out.push("clean");
  return out;
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

// A task's place in the inbox: the rank it was moved to, or when it was made.
export function taskRank(t) {
  return typeof t.rank === "number" ? t.rank : toDate(t.created_at)?.getTime() ?? 0;
}

// Queued tasks in the inbox's order, each marked ready when its project is
// free, or with why it waits.
export function queue(tasks, projects) {
  const byName = new Map(projects.map((p) => [p.name, p]));
  return tasks
    .filter((t) => t.status === "queued")
    .sort((a, b) => taskRank(a) - taskRank(b))
    .map((t) => {
      const p = byName.get(t.project);
      return p && p.free ? { ...t, ready: true } : { ...t, ready: false, why: p ? p.busy : "project no longer exists" };
    });
}

export function queueCounts(list) {
  const ready = list.filter((t) => t.ready).length;
  return { all: list.length, ready, waiting: list.length - ready };
}

export function filterQueue(list, status, project) {
  return list.filter((t) =>
    (!project || t.project === project) && (status === "ready" ? t.ready : status === "waiting" ? !t.ready : true));
}

// The rank that puts list[i] at position to, between the tasks that would
// then be its neighbours, so a move writes one task.
export function rankAt(list, i, to) {
  const rest = list.filter((_, k) => k !== i);
  const before = rest[to - 1];
  const after = rest[to];
  if (!before && !after) return taskRank(list[i]);
  if (!before) return taskRank(after) - 1000;
  if (!after) return taskRank(before) + 1000;
  return (taskRank(before) + taskRank(after)) / 2;
}

// A project with no logo is drawn as its initial on one of six tints, the
// same one every time.
export function monogram(name) {
  let hash = 0;
  for (const ch of String(name)) hash = (hash * 31 + ch.codePointAt(0)) >>> 0;
  return { letter: String(name).charAt(0).toUpperCase() || "?", tint: hash % 6 };
}

export function searchProjects(projects, query) {
  const q = query.trim().toLowerCase();
  return projects
    .filter((p) => !q || p.name.toLowerCase().includes(q))
    .sort((a, b) => a.name.localeCompare(b.name));
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

// answerText turns picks on the phone into the prompt the session gets after
// its dialog is dismissed. picks[i] is { chosen: [labels], other: "" } for
// question i. It returns "" until every question has an answer.
export function answerText(question, picks) {
  const lines = [];
  for (const [i, item] of question.items.entries()) {
    const p = picks[i] || {};
    const parts = [...(p.chosen || [])];
    const other = String(p.other || "").trim();
    if (other) parts.push(other);
    if (!parts.length) return "";
    lines.push(`- ${item.question} ${parts.join("; ")}`);
  }
  return `My answers to your questions:\n${lines.join("\n")}`;
}
