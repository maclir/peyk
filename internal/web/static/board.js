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

// onDefaultBranch says the branch needs no mention: a worker is on main or
// master unless it is in a worktree of its own.
export function onDefaultBranch(git) {
  return git.branch === "main" || git.branch === "master";
}

// usageLeft is the percentage left in each Claude rate-limit window. A window
// past its reset is whole again, and so is one Claude Code no longer reports:
// it drops a window once that window resets.
export function usageLeft(usage, now) {
  if (!usage) return null;
  return [["5h", usage.five_hour], ["7d", usage.seven_day]].map(([label, w]) => {
    const resetsAt = w && toDate(w.resets_at);
    if (!resetsAt || resetsAt.getTime() <= now) return { label, left: 100, resetsAt: null };
    return { label, left: Math.max(0, Math.min(100, Math.round(100 - w.used))), resetsAt };
  });
}

export function pushLabel(git) {
  if (!git) return "no repository";
  if (git.unpushed > 0 && !git.upstream) return `${git.unpushed} unpushed · no remote branch`;
  if (git.unpushed > 0) return `${git.unpushed} unpushed`;
  if (git.upstream) return "pushed";
  return "no remote";
}

// A pushed commit's workflow runs, in the words and tone the phone shows.
const CI = {
  queued: { word: "queued", tone: "run" },
  running: { word: "running", tone: "run" },
  success: { word: "passed", tone: "ok" },
  failure: { word: "failed", tone: "bad" },
  cancelled: { word: "cancelled", tone: "idle" },
};

export function ciStatus(git) {
  const ci = git && git.ci;
  if (!ci) return null;
  const c = CI[ci.state] || { word: ci.state, tone: "idle" };
  return { short: `ci ${c.word}`, long: `${ci.workflow || "CI"} ${c.word}`, tone: c.tone, url: ci.url };
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
      if (!p) return { ...t, ready: false, why: "project no longer exists" };
      if (p.hidden) return { ...t, ready: false, why: "project hidden" };
      return p.free ? { ...t, ready: true } : { ...t, ready: false, why: p.busy };
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

// Tasks sent to a worker and not yet finished, newest first.
export function sentTasks(tasks) {
  return tasks
    .filter((t) => t.status === "dispatched")
    .sort((a, b) => toDate(b.dispatched_at) - toDate(a.dispatched_at));
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

// awaitingReply is true when the agent has not replied since the last prompt,
// so its latest reply answers an earlier one.
export function awaitingReply(s) {
  if (!s.prompt || s.cleared) return false;
  return !s.message || toDate(s.message.at) < toDate(s.prompt.at);
}

// pendingLabel says what the agent is doing with a prompt it has not answered.
export function pendingLabel(s) {
  return s.state === "working" ? "Working…" : s.state === "needs_input" ? "Waiting for you…" : "Not replied yet";
}
