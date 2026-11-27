// What the board shows for a session, independent of Firebase and the DOM.

export const STATES = {
  needs_input: { label: "needs input", tone: "bad", group: "Needs you" },
  error: { label: "error", tone: "bad", group: "Needs you" },
  dirty: { label: "uncommitted", tone: "bad", group: "Needs you" },
  unpushed: { label: "unpushed", tone: "ok", group: "Ready to push" },
  idle: { label: "idle", tone: "idle", group: "Idle" },
  working: { label: "working", tone: "run", group: "Working" },
  stopped: { label: "stopped", tone: "idle", group: "Stopped" },
};
const GROUPS = [...new Set(Object.values(STATES).map((st) => st.group))];

// The board orders workers by group, then within a group puts a waiting
// notification first, then the fullest inbox, then the one a person last
// prompted, then the title.
export function sortSessions(sessions, inboxCount = () => 0) {
  const counts = new Map(sessions.map((s) => [s, inboxCount(s)]));
  return [...sessions].sort(
    (a, b) => groupRank(a.state) - groupRank(b.state) || Number(Boolean(b.notified)) - Number(Boolean(a.notified))
      || counts.get(b) - counts.get(a) || time(b.prompt?.at) - time(a.prompt?.at)
      || String(a.title).localeCompare(String(b.title)),
  );
}

export function activity(s) {
  if (s.state === "unpushed" && s.git?.last) return time(s.git.last.at);
  return Math.max(time(s.message?.at), time(s.prompt?.at));
}

function time(v) {
  const d = toDate(v);
  return d && !isNaN(d) ? d.getTime() : 0;
}

function groupRank(state) {
  const i = GROUPS.indexOf(STATES[state]?.group);
  return i === -1 ? GROUPS.length : i;
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
  if (!usage?.five_hour && !usage?.seven_day) return null;
  return windowsLeft(usage, now);
}

function windowsLeft(limits, now) {
  return [["5h", limits.five_hour], ["7d", limits.seven_day]].map(([label, w]) => {
    const resetsAt = w && toDate(w.resets_at);
    if (!resetsAt || resetsAt.getTime() <= now) return { label, left: 100, resetsAt: null };
    return { label, left: Math.max(0, Math.min(100, Math.round(100 - w.used))), resetsAt };
  });
}

// antigravityLeft is usageLeft for each of Antigravity's pools, which share
// limits within a group of models such as Gemini.
export function antigravityLeft(usage, now) {
  const pools = usage?.antigravity ?? [];
  if (!pools.length) return null;
  return pools.map((p) => ({ name: p.name, windows: windowsLeft(p, now) }));
}

// tightest is the least left in each window across pools, so one bar per
// window never hides a pool that is about to run out.
export function tightest(pools) {
  return ["5h", "7d"].map((label, i) => pools.reduce(
    (min, p) => (p.windows[i].left < min.left ? p.windows[i] : min),
    { label, left: 100, resetsAt: null },
  ));
}

// quotaState is how a bar shows what is left: thinner and amber under 10%,
// a red hairline once nothing is.
export function quotaState(left) {
  if (left <= 0) return "out";
  if (left < 10) return "low";
  return "";
}

// actionsLeft is what is left of the month's included GitHub Actions
// minutes. A month past its reset is whole again, its repositories' usage gone.
export function actionsLeft(actions, now) {
  if (!actions?.included) return null;
  const at = toDate(actions.resets_at);
  const resetsAt = at && at.getTime() > now ? at : null;
  const used = resetsAt ? Math.max(0, Math.min(actions.included, actions.used ?? 0)) : 0;
  const left = actions.included - used;
  return { left, included: actions.included, pct: Math.round((100 * left) / actions.included), resetsAt, repos: resetsAt ? actions.repos ?? [] : [] };
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
  return { state: CI[ci.state] ? ci.state : "queued", long: `${ci.workflow || "CI"} ${c.word}`, tone: c.tone, url: ci.url };
}

// Tags are the facts about a worker that differ from committed, pushed and
// on the default branch, in a fixed order. "Pushed" and "clean" are never
// tags: a worker's state group already implies them.
export function tags(s) {
  const out = [];
  const g = s.git;
  if (g) {
    if (g.dirty > 0) out.push({ kind: "edit", tone: "warn", text: String(g.dirty), label: count(g.dirty, "uncommitted file") });
    if (g.unpushed > 0) out.push({ kind: "push", tone: "ok", text: String(g.unpushed), label: count(g.unpushed, "unpushed commit") });
    if (!g.upstream) {
      const text = g.unpushed > 0 ? "no remote branch" : "no remote";
      out.push({ kind: "noremote", tone: "", text, label: text });
    }
    if (!onDefaultBranch(g)) out.push({ kind: "branch", tone: "", text: g.branch || "detached", label: `branch ${g.branch || "detached"}` });
    const ci = ciStatus(g);
    if (ci) out.push({ kind: `ci-${ci.state}`, tone: ci.tone, text: "CI", label: ci.long, url: ci.url });
  }
  for (const kind of ["html", "design", "other"]) {
    const n = (s.prototypes ?? []).filter((p) => prototypeKind(p.kind) === kind).length;
    if (!n) continue;
    const k = PROTOTYPE_KINDS[kind];
    const noun = `${k.name ? `${k.name} ` : ""}prototype${n === 1 ? "" : "s"}`;
    out.push({ kind: k.icon, tone: k.tone, text: n > 1 ? String(n) : "", label: k.name || n > 1 ? `${n} ${noun}` : noun });
  }
  return out;
}

// A published page's kind, and a task's choice of one, share an icon and a
// tone everywhere they appear. A page from before kinds were read counts as
// other.
export const PROTOTYPE_KINDS = {
  html: { icon: "proto-html", tone: "html", name: "HTML" },
  design: { icon: "proto-design", tone: "design", name: "Design" },
  any: { icon: "proto-any", tone: "any", name: "Any" },
  other: { icon: "proto", tone: "", name: "" },
};

export function prototypeKind(kind) {
  return PROTOTYPE_KINDS[kind] && kind !== "any" ? kind : "other";
}

// The order a task's prototype toggle steps through; "" is an ordinary task.
const PROTOTYPE_STEPS = ["", "html", "design", "any"];

export function nextPrototype(kind) {
  return PROTOTYPE_STEPS[(PROTOTYPE_STEPS.indexOf(kind ?? "") + 1) % PROTOTYPE_STEPS.length];
}

function count(n, noun) {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
}

// quickAction is the one worker command offered on its page: a stopped
// worker can only start; a finished one is shut down when no task is ready
// for it, or cleared and given the next one when a task is. Tapping it is how
// you say the current task is done, since a worker's reply cannot tell a
// finished task from one that needs more back-and-forth. A busy or dirty
// worker gets none, so a tap cannot interrupt work or drop the context of
// uncommitted changes.
export function quickAction(s, ready) {
  if (s.aoe_status === "stopped") return "start";
  if (s.state !== "idle" && s.state !== "unpushed") return null;
  if (ready) return s.cleared ? null : "clearnext";
  return s.cleared ? "stop" : "clearstop";
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

// The rank that puts a new task above every queued one.
export function topRank(tasks, now) {
  const ranks = tasks.filter((t) => t.status === "queued").map(taskRank);
  return ranks.length ? Math.min(...ranks) - 1000 : now;
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

// A Done list that cannot be read says why in words, not Firestore's: a
// query whose index is still building fails with a console link.
export function doneError(err) {
  return err?.code === "failed-precondition"
    ? "Firestore is still preparing this list. Try again in a few minutes."
    : `Could not load done tasks: ${err?.message ?? err}`;
}

// What a done task offers for returning its worker to the task's
// conversation: nothing without a conversation or a worker, a note when the
// worker is already in it, and otherwise a resume that leaves the worker's
// current conversation, if it has one, and waits for the worker to be free.
export function resumeAction(t, project, worker) {
  if (!t.conversation_id || !project?.session_id || !worker) return null;
  if (worker.conversation === t.conversation_id) return { current: true };
  return { clear: !worker.cleared && worker.state !== "stopped", busy: project.free ? "" : project.busy || "busy" };
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

// withDelivered shows a reply the laptop has typed into a session as its
// prompt, with the agent working on it, until the session's own prompt
// changes. The laptop publishes that prompt only after the next full refresh.
export function withDelivered(s, d) {
  if (!d || (s.prompt?.id ?? "") !== d.after) return s;
  // A reply written after the send answered it, so the session's own prompt
  // is current even when the laptop could not tell this one apart.
  if (toDate(s.message?.at) > d.at) return s;
  const n = d.images;
  const text = d.text || `${n} image${n === 1 ? "" : "s"}`;
  return { ...s, prompt: { text, at: d.at }, state: "working", cleared: false };
}

// waitingReplies turns reply commands the laptop has not finished, pending
// or held, into what the phone shows and resends, oldest first.
export function waitingReplies(docs) {
  const out = [];
  for (const d of docs) {
    if (d.type !== "reply" || !["pending", "held"].includes(d.status)) continue;
    let p;
    try {
      p = JSON.parse(d.payload);
    } catch {
      continue;
    }
    out.push({ id: d.id, status: d.status, at: d.created_at, session: p.session, text: p.text ?? "", images: p.images ?? [],
      conversation: p.conversation ?? "", after: p.after ?? "" });
  }
  return out.sort((x, y) => (toDate(x.at) ?? Infinity) - (toDate(y.at) ?? Infinity));
}

// replyFields is what a reply to s signs: its text and images, and the
// conversation and message it answers, so the laptop can send it late.
export function replyFields(s, text, images) {
  return { session: s.id, text, ...(images.length ? { images } : {}), ...(s.conversation ? { conversation: s.conversation, after: s.latest ?? "" } : {}) };
}

// mergeReply folds a new reply into one still queued for the same session,
// so a session has one queued reply: two bound to the same message would
// leave the second held once the first goes out. It returns null when the
// images would not fit in one message.
export function mergeReply(queued, text, images, max) {
  const all = [...new Set([...queued.images, ...images])];
  if (all.length > max) return null;
  return { text: [queued.text, text].filter(Boolean).join("\n\n"), images: all };
}

// The lenses an ideation task looks through, in the order the phone offers
// them. The laptop holds what each one asks, so these are only ids and names.
export const LENSES = [
  ["reuse", "Reuse"], ["experience", "One experience"], ["architecture", "Architecture"], ["security", "Security"],
  ["features", "Features"], ["simplify", "Simplify"], ["tests", "Tests"],
  ["performance", "Performance & cost"], ["accessibility", "Accessibility"], ["ui", "UI polish"], ["show", "Show, don't tell"],
];
export const IDEA_LIMITS = [3, 5, 10];
export const DEFAULT_IDEATE = { lenses: ["reuse", "experience"], max: 5 };

export function lensName(id) {
  return LENSES.find(([l]) => l === id)?.[1] ?? id;
}

// An ideation task's text is its focus; with none, it looks everywhere.
export function ideateText(focus) {
  return focus.trim() || "Look across the whole repository.";
}

// A lens is turned on or off; the last one stays, since a run needs one.
export function toggleLens(ask, id) {
  const on = ask.lenses.includes(id);
  if (on && ask.lenses.length === 1) return ask;
  const lenses = on ? ask.lenses.filter((l) => l !== id) : LENSES.map(([l]) => l).filter((l) => l === id || ask.lenses.includes(l));
  return { ...ask, lenses };
}

// The open ideas, newest first, for one project or all of them.
export function openIdeas(ideas, project) {
  return ideas.filter((i) => i.status === "open" && (!project || i.project === project))
    .sort((a, b) => time(b.created_at) - time(a.created_at) || String(a.id).localeCompare(String(b.id)));
}

export function ideaCount(ideas, project) {
  return openIdeas(ideas, project).length;
}

// The models a worker can switch to, by the quota they draw on. Claude Code
// resolves each alias to the newest model of its family, so no version is
// named here. Pips are the budget a task uses, 1 to 5, against the priciest
// model in the pool, from output prices per 1M tokens: Fable $40, Opus $20,
// Sonnet $10, Haiku $0.50.
export const MODEL_POOLS = [
  {
    id: "claude", name: "Claude Code", kind: "cc",
    models: [
      { alias: "fable", name: "Fable", pips: 5 },
      { alias: "opus", name: "Opus", pips: 3 },
      { alias: "sonnet", name: "Sonnet", pips: 2 },
      { alias: "haiku", name: "Haiku", pips: 1 },
    ],
  },
];

// modelVersions names each Claude Code alias by the newest model of its
// family a worker has replied with, as Claude Code resolves the alias: opus
// is "Opus 5.5" once a worker has run it.
export function modelVersions(sessions) {
  const out = {};
  for (const s of sessions) {
    const id = s.tool === "antigravity" ? "" : s.message?.model;
    const family = modelFamily(id);
    if (family && (!out[family] || modelName(id).localeCompare(out[family], undefined, { numeric: true }) > 0)) out[family] = modelName(id);
  }
  return out;
}

// modelFamily is the alias a model id belongs to: "claude-opus-5-5" is opus.
export function modelFamily(id) {
  return /^claude-([a-z]+)-/.exec(id ?? "")?.[1] ?? "";
}

// modelName is a model id as people say it: "claude-opus-5-5" is "Opus 5.5".
export function modelName(id) {
  const m = /^claude-([a-z]+)-(\d+)(?:-(\d+))?/.exec(id ?? "");
  if (!m) return id ?? "";
  return `${m[1][0].toUpperCase()}${m[1].slice(1)} ${m[2]}${m[3] ? `.${m[3]}` : ""}`;
}

// Output prices per 1M tokens of Antigravity's models, by family, which set
// their pips against the priciest in the same pool. A family missing here
// shows no pips rather than a guess.
const ANTIGRAVITY_PRICES = {
  "gemini-4-carbon": 20, "gemini-4-argon": 20, "gemini-4-neon": 15, "gemini-4-hydrogen": 7.5, "gemini-3.8-flash": 3.75,
  "claude-opus-5-5": 20, "claude-sonnet-5-5": 10,
};
// The families the sheet offers at once; the rest of what Antigravity lists,
// such as its orchestrators and the models it is retiring, wait under More.
const ANTIGRAVITY_MAIN = new Set(["gemini-4-neon", "gemini-4-carbon", "gemini-4-hydrogen", "claude-opus-5-5", "claude-sonnet-5-5"]);
const EFFORT = /-(low|medium|high|xhigh)$/;
const EFFORT_NAME = / \((Low|Medium|High|XHigh)\)$/;

// modelBase is a model's name without the effort Antigravity appends.
export function modelBase(name) {
  return (name ?? "").replace(EFFORT_NAME, "");
}

// antigravityPools turns the models laptop/models lists into the sheet's
// rows: one per family, at high effort where the family offers it, in the
// pool whose quota it draws on. Claude and GPT share Antigravity's
// third-party pool, which peyk calls Claude.
export function antigravityPools(models) {
  const families = new Map();
  for (const m of models ?? []) {
    const family = m.id.replace(EFFORT, "");
    const effort = EFFORT.exec(m.id)?.[1];
    const had = families.get(family);
    const rank = (e) => ["high", "medium", "xhigh", "low"].indexOf(e);
    if (!had || (effort && (had.effort == null || rank(effort) < rank(had.effort)))) {
      families.set(family, { id: m.id, family, effort, name: modelBase(m.name) });
    }
  }
  const pools = [
    { id: "gemini", name: "Gemini", models: [] },
    { id: "3p", name: "Claude", models: [] },
  ];
  for (const f of families.values()) {
    pools[/^(claude|gpt)-/.test(f.id) ? 1 : 0].models.push(f);
  }
  for (const p of pools) {
    const max = Math.max(0, ...p.models.map((m) => ANTIGRAVITY_PRICES[m.family] ?? 0));
    for (const m of p.models) {
      const price = ANTIGRAVITY_PRICES[m.family];
      m.pips = price && max ? Math.max(1, Math.ceil((5 * price) / max)) : 0;
      m.more = !ANTIGRAVITY_MAIN.has(m.family);
      // Under its pool's name, a model needs no repeat of it.
      m.short = m.name.startsWith(`${p.name} `) ? m.name.slice(p.name.length + 1) : m.name;
    }
  }
  return pools.filter((p) => p.models.length);
}
