import { describe, expect, it } from 'vitest';
// @ts-expect-error plain browser modules without type declarations
import { renderMarkdown } from '../../internal/web/static/markdown.js';
// @ts-expect-error plain browser modules without type declarations
import { STALE_AFTER_MS, ago, answerText, ciStatus, actionsLeft, laptopOnline, mergeReply, pushLabel, quickAction, replyFields, sortSessions, tags, nextPrototype, usageLeft, antigravityLeft, tightest, quotaState, MODEL_POOLS, modelFamily, modelName, antigravityPools, modelBase, modelVersions, sessionModel, onDefaultBranch, waitingReplies, LENSES, ideateText, toggleLens, openIdeas, ideaCount, lensName } from '../../internal/web/static/board.js';

describe('renderMarkdown', () => {
  it('escapes markup in replies', () => {
    const html = renderMarkdown('<img src=x onerror=alert(1)> and `<b>`');
    expect(html).not.toContain('<img');
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
    expect(html).toContain('<code>&lt;b&gt;</code>');
  });

  it('renders the structure agents use', () => {
    const html = renderMarkdown('## Done\n\nCommitted **3f2a1c4**.\n\n- one\n- two `x`\n\n1. first\n\n```\nmake test\n```');
    expect(html).toContain('<h4>Done</h4>');
    expect(html).toContain('<p>Committed <strong>3f2a1c4</strong>.</p>');
    expect(html).toContain('<ul><li>one</li><li>two <code>x</code></li></ul>');
    expect(html).toContain('<ol><li>first</li></ol>');
    expect(html).toContain('<pre><code>make test</code></pre>');
  });

  it('links only http and https URLs', () => {
    expect(renderMarkdown('[a](https://example.com)')).toContain('<a href="https://example.com"');
    expect(renderMarkdown('[a](javascript:alert(1))')).not.toContain('<a');
  });

  it('renders tables with alignment', () => {
    const html = renderMarkdown('Before\n| Name | Count |\n|:-----|------:|\n| **a** | 1 |\n| b \\| c |\n\nAfter');
    expect(html).toContain('<p>Before</p>');
    expect(html).toContain(
      '<div class="table"><table><thead><tr><th class="left">Name</th><th class="right">Count</th></tr></thead>' +
        '<tbody><tr><td class="left"><strong>a</strong></td><td class="right">1</td></tr>' +
        '<tr><td class="left">b | c</td><td class="right"></td></tr></tbody></table></div>',
    );
    expect(html).toContain('<p>After</p>');
  });

  it('leaves pipes outside a table as text', () => {
    expect(renderMarkdown('a | b\nc | d')).toBe('<p>a | b c | d</p>');
  });

  it('nests indented lists and keeps numbering across them', () => {
    const html = renderMarkdown(
      '1. **Push** `main`.\n2. Switch:\n  - Open the app\n  - Delete\n    the old one\n3. Pair:\n   - `make pair`\n\nDo step 3 last.',
    );
    expect(html).toBe(
      '<ol><li><strong>Push</strong> <code>main</code>.</li>' +
        '<li>Switch:<ul><li>Open the app</li><li>Delete the old one</li></ul></li>' +
        '<li>Pair:<ul><li><code>make pair</code></li></ul></li></ol>\n<p>Do step 3 last.</p>',
    );
  });

  it('continues a list across blank lines and keeps its start number', () => {
    expect(renderMarkdown('4. four\n\n5. five\n\n- a\n\n  more')).toBe(
      '<ol start="4"><li>four</li><li>five</li></ol>\n<ul><li>a more</li></ul>',
    );
  });

  it('keeps code spans literal', () => {
    expect(renderMarkdown('`**not bold**`')).toContain('<code>**not bold**</code>');
  });
});

describe('board', () => {
  it('offers one quick action by worker state and inbox', () => {
    const w = (aoe_status: string, state: string, cleared = false) => ({ aoe_status, state, cleared });
    expect(quickAction(w('stopped', 'stopped'), false)).toBe('start');
    expect(quickAction(w('stopped', 'unpushed'), true)).toBe('start');
    expect(quickAction(w('idle', 'unpushed'), false)).toBe('clearstop');
    expect(quickAction(w('idle', 'idle'), false)).toBe('clearstop');
    expect(quickAction(w('idle', 'idle', true), false)).toBe('stop');
    expect(quickAction(w('idle', 'unpushed'), true)).toBe('clearnext');
    expect(quickAction(w('idle', 'idle', true), true)).toBeNull();
    for (const state of ['working', 'needs_input', 'error', 'dirty']) expect(quickAction(w('idle', state), false)).toBeNull();
  });

  it('sorts by group, then by a waiting notification, then by inbox size, then by title', () => {
    const inbox: Record<string, number> = { c: 1, d: 3, e: 2 };
    const got = sortSessions([
      { title: 'w', state: 'working' },
      { title: 'a', state: 'idle' },
      { title: 'b', state: 'idle', notified: true },
      { title: 'c', state: 'idle' },
      { title: 'd', state: 'idle' },
      { title: 'e', state: 'idle', notified: true },
      { title: 'z', state: 'dirty' },
      { title: 'y', state: 'needs_input' },
      { title: 'u', state: 'unpushed' },
    ], (s: any) => inbox[s.title] ?? 0).map((s: any) => s.title);
    expect(got).toEqual(['y', 'z', 'u', 'e', 'b', 'd', 'c', 'a', 'w']);
  });

  it('sorts equal inboxes by the latest prompt, then by title', () => {
    const got = sortSessions([
      { title: 'a', state: 'idle' },
      { title: 'b', state: 'idle', prompt: { at: '2026-11-24T10:00:00Z' } },
      { title: 'c', state: 'idle', prompt: { at: new Date('2026-11-24T11:00:00Z') } },
      { title: 'd', state: 'idle', notified: true },
      { title: 'e', state: 'idle' },
    ]).map((s: any) => s.title);
    expect(got).toEqual(['d', 'c', 'b', 'a', 'e']);
  });

  it('sorts by title without inbox counts', () => {
    const got = sortSessions([{ title: 'b', state: 'idle' }, { title: 'a', state: 'idle' }]).map((s: any) => s.title);
    expect(got).toEqual(['a', 'b']);
  });

  it('treats a laptop not seen for longer than the heartbeat explains as offline', () => {
    const now = Date.now();
    expect(laptopOnline({ online: true, seen_at: new Date(now - 60_000) }, now)).toBe(true);
    expect(laptopOnline({ online: true, seen_at: new Date(now - STALE_AFTER_MS - 1) }, now)).toBe(false);
    expect(laptopOnline({ online: false, seen_at: new Date(now) }, now)).toBe(false);
    expect(laptopOnline(null, now)).toBe(false);
  });

  it('labels push state', () => {
    expect(pushLabel({ upstream: 'origin/main', unpushed: 0 })).toBe('pushed');
    expect(pushLabel({ upstream: 'origin/main', unpushed: 2 })).toBe('2 unpushed');
    expect(pushLabel({ upstream: '', unpushed: 1 })).toBe('1 unpushed · no remote branch');
    expect(pushLabel(null)).toBe('no repository');
  });

  it("labels a pushed commit's CI", () => {
    const url = 'https://github.com/o/r/actions/runs/1';
    expect(ciStatus({ ci: { workflow: 'Test and deploy', state: 'running', url } })).toEqual({ state: 'running', long: 'Test and deploy running', tone: 'run', url });
    expect(ciStatus({ ci: { workflow: 'CI', state: 'success', url } })?.long).toBe('CI passed');
    expect(ciStatus({ ci: { workflow: 'CI', state: 'failure', url } })?.tone).toBe('bad');
    expect(ciStatus({ unpushed: 0 })).toBeNull();
    expect(ciStatus(null)).toBeNull();
  });

  it('tags only what differs from committed, pushed and on the default branch', () => {
    const pushed = { branch: 'main', upstream: 'origin/main', dirty: 0, unpushed: 0 };
    expect(tags({ git: pushed })).toEqual([]);
    expect(tags({})).toEqual([]);
    const url = 'https://github.com/o/r/actions/runs/1';
    const all = tags({ git: { branch: 'fix', upstream: '', dirty: 1, unpushed: 3, ci: { workflow: 'CI', state: 'failure', url } },
      prototypes: [{ url: 'a' }, { url: 'b' }, { url: 'c', kind: 'design' }, { url: 'd', kind: 'html' }, { url: 'e', kind: 'design' }] });
    expect(all.map((t) => [t.kind, t.tone, t.text, t.label])).toEqual([
      ['edit', 'warn', '1', '1 uncommitted file'],
      ['push', 'ok', '3', '3 unpushed commits'],
      ['noremote', '', 'no remote branch', 'no remote branch'],
      ['branch', '', 'fix', 'branch fix'],
      ['ci-failure', 'bad', 'CI', 'CI failed'],
      ['proto-html', 'html', '', '1 HTML prototype'],
      ['proto-design', 'design', '2', '2 Design prototypes'],
      ['proto', '', '2', '2 prototypes'],
    ]);
    expect(all[4].url).toBe(url);
    expect(tags({ git: { ...pushed, upstream: '' } }).map((t) => t.text)).toEqual(['no remote']);
    expect(tags({ git: pushed, prototypes: [{ url: 'a' }] })).toEqual([{ kind: 'proto', tone: '', text: '', label: 'prototype' }]);
    expect(tags({ git: pushed, prototypes: [{ url: 'a', kind: 'slides' }] })[0].kind).toBe('proto');
    expect(tags({ git: { ...pushed, ci: { state: 'mystery' } } })[0].kind).toBe('ci-queued');
  });

  it('steps a task\'s prototype toggle from off through each kind and back', () => {
    const seen = [];
    let kind = '';
    for (let i = 0; i < 4; i++) seen.push(kind = nextPrototype(kind));
    expect(seen).toEqual(['html', 'design', 'any', '']);
    expect(nextPrototype(undefined)).toBe('html');
  });

  it('formats ages', () => {
    const now = Date.now();
    expect(ago(new Date(now - 30_000), now)).toBe('now');
    expect(ago(new Date(now - 12 * 60_000), now)).toBe('12m');
    expect(ago(new Date(now - 3 * 3600_000), now)).toBe('3h');
    expect(ago(new Date(now - 3 * 86400_000), now)).toBe('3d');
  });
});

describe('inbox', () => {
  // @ts-expect-error plain browser module
  const load = () => import('../../internal/web/static/board.js');

  it('orders the picker by recent activity, then name', async () => {
    const { sortProjects } = await load();
    const got = sortProjects([
      { name: 'b' },
      { name: 'old', active: new Date(2026, 0, 1) },
      { name: 'new', active: new Date(2026, 10, 1) },
      { name: 'a' },
    ]).map((p: any) => p.name);
    expect(got).toEqual(['new', 'old', 'a', 'b']);
  });

  it('queues tasks in the order set, marking which can be sent now', async () => {
    const { queue, queueCounts, filterQueue } = await load();
    const list = queue(
      [
        { project: 'knip', text: 'later', status: 'queued', created_at: new Date(2026, 0, 2) },
        { project: 'knip', text: 'first', status: 'queued', created_at: new Date(2026, 0, 1) },
        { project: 'knip', text: 'moved up', status: 'queued', created_at: new Date(2026, 0, 3), rank: 0 },
        { project: 'hamta', text: 'h', status: 'queued', created_at: new Date(2026, 0, 1, 12) },
        { project: 'gone', text: 'g', status: 'queued', created_at: new Date(2026, 0, 5) },
        { project: 'knip', text: 'sent', status: 'dispatched', created_at: new Date(2026, 0, 1) },
      ],
      [{ name: 'knip', free: true }, { name: 'hamta', free: false, busy: 'working' }],
    );
    expect(list.map((t: any) => t.text)).toEqual(['moved up', 'first', 'h', 'later', 'g']);
    expect(list.filter((t: any) => !t.ready).map((t: any) => `${t.text}:${t.why}`)).toEqual(['h:working', 'g:project no longer exists']);
    expect(queueCounts(list)).toEqual({ all: 5, ready: 3, waiting: 2 });
    expect(filterQueue(list, 'ready', null).map((t: any) => t.text)).toEqual(['moved up', 'first', 'later']);
    expect(filterQueue(list, 'all', 'hamta').map((t: any) => t.text)).toEqual(['h']);
  });

  it('holds back tasks for a hidden project', async () => {
    const { queue } = await load();
    const list = queue([{ project: 'areh', text: 'a', status: 'queued', created_at: new Date(2026, 0, 1) }], [{ name: 'areh', free: true, hidden: true }]);
    expect(list.map((t: any) => `${t.ready}:${t.why}`)).toEqual(['false:project hidden']);
  });

  it('moves a task by giving it a rank between its new neighbours', async () => {
    const { rankAt } = await load();
    const list = [{ rank: 10 }, { rank: 20 }, { rank: 30 }];
    expect(rankAt(list, 2, 1)).toBe(15);
    expect(rankAt(list, 0, 1)).toBe(25);
    expect(rankAt(list, 2, 0)).toBe(-990);
    expect(rankAt(list, 0, 2)).toBe(1030);
    expect(rankAt([{ rank: 5 }], 0, 0)).toBe(5);
  });

  it('ranks a new task above every queued one', async () => {
    const { topRank } = await load();
    const tasks = [{ status: 'queued', rank: 10 }, { status: 'queued', created_at: new Date(5) }, { status: 'dispatched', rank: -5000 }];
    expect(topRank(tasks, 99)).toBe(-995);
    expect(topRank([], 99)).toBe(99);
  });

  it('draws a project without a logo as its initial on a stable tint', async () => {
    const { monogram } = await load();
    expect(monogram('sakku')).toEqual(monogram('sakku'));
    expect(monogram('sakku').letter).toBe('S');
    expect(monogram('sakku').tint).toBeGreaterThanOrEqual(0);
    expect(monogram('sakku').tint).toBeLessThan(6);
  });

  it('finds projects by any part of the name, alphabetically', async () => {
    const { searchProjects } = await load();
    const projects = [{ name: 'popq' }, { name: 'knip' }, { name: 'dekode-firebase' }, { name: 'dekode-cloud' }];
    expect(searchProjects(projects, ' DEKODE ').map((p: any) => p.name)).toEqual(['dekode-cloud', 'dekode-firebase']);
    expect(searchProjects(projects, '').map((p: any) => p.name)).toEqual(['dekode-cloud', 'dekode-firebase', 'knip', 'popq']);
  });
});

describe('passkey helpers', () => {
  it('fingerprints a key the way the laptop does', async () => {
    // @ts-expect-error plain browser module
    const { fingerprint } = await import('../../internal/web/static/passkey.js');
    // SHA-256("abc") starts ba7816bf8f01; the laptop's Fingerprint gives "ba78 16bf 8f01".
    expect(await fingerprint(btoa('abc'))).toBe('ba78 16bf 8f01');
  });

  it('encodes base64url without padding', async () => {
    // @ts-expect-error plain browser module
    const { b64url } = await import('../../internal/web/static/passkey.js');
    expect(b64url(new Uint8Array([251, 255, 0]).buffer)).toBe('-_8A');
  });
});

describe('task lifecycle', () => {
  it('finds the task a session is on and lists sent tasks newest first', async () => {
    // @ts-expect-error plain browser module
    const { currentTask, sentTasks } = await import('../../internal/web/static/board.js');
    const tasks = [
      { id: 'a', status: 'dispatched', session_id: 'k', dispatched_at: new Date(2026, 0, 1), text: 'old' },
      { id: 'b', status: 'dispatched', session_id: 'k', dispatched_at: new Date(2026, 0, 2), text: 'new' },
      { id: 'c', status: 'done', session_id: 'k', done_at: new Date(2026, 0, 3) },
      { id: 'd', status: 'done', session_id: 'h', done_at: new Date(2026, 0, 4) },
    ];
    expect(currentTask(tasks, 'k').id).toBe('b');
    expect(currentTask(tasks, 'h')).toBe(null);
    expect(sentTasks(tasks).map((t: any) => t.id)).toEqual(['b', 'a']);
  });
});

describe('resume', () => {
  it('offers a done task\'s conversation to a free worker, clearing the one it has', async () => {
    // @ts-expect-error plain browser module
    const { resumeAction } = await import('../../internal/web/static/board.js');
    const t = { conversation_id: 'old' };
    const free = { session_id: 'k', free: true, busy: '' };
    expect(resumeAction({}, free, { conversation: 'now' })).toBe(null);
    expect(resumeAction(t, { free: true }, null)).toBe(null);
    expect(resumeAction(t, free, { conversation: 'old' })).toEqual({ current: true });
    expect(resumeAction(t, free, { conversation: 'now', state: 'idle' })).toEqual({ clear: true, busy: '' });
    expect(resumeAction(t, free, { conversation: 'now', cleared: true, state: 'idle' })).toEqual({ clear: false, busy: '' });
    expect(resumeAction(t, free, { conversation: 'now', state: 'stopped' })).toEqual({ clear: false, busy: '' });
    expect(resumeAction(t, { session_id: 'k', free: false, busy: 'working' }, { conversation: 'now', state: 'working' }))
      .toEqual({ clear: true, busy: 'working' });
  });
});

describe('done list errors', () => {
  it('names a building index plainly and passes other errors through', async () => {
    // @ts-expect-error plain browser module
    const { doneError } = await import('../../internal/web/static/board.js');
    expect(doneError({ code: 'failed-precondition', message: 'The query requires an index. https://console…' }))
      .toBe('Firestore is still preparing this list. Try again in a few minutes.');
    expect(doneError({ code: 'permission-denied', message: 'Missing permissions' })).toBe('Could not load done tasks: Missing permissions');
  });
});

describe('pending reply', () => {
  it('stands in for a reply older than the last prompt', async () => {
    // @ts-expect-error plain browser module
    const { awaitingReply, pendingLabel } = await import('../../internal/web/static/board.js');
    const early = new Date(2026, 0, 1);
    const late = new Date(2026, 0, 2);
    expect(awaitingReply({})).toBe(false);
    expect(awaitingReply({ prompt: { at: early } })).toBe(true);
    expect(awaitingReply({ prompt: { at: late }, message: { at: early } })).toBe(true);
    expect(awaitingReply({ prompt: { at: early }, message: { at: late } })).toBe(false);
    expect(awaitingReply({ prompt: { text: '/clear', at: late }, cleared: true })).toBe(false);
    expect(pendingLabel({ state: 'working' })).toBe('Working…');
    expect(pendingLabel({ state: 'idle' })).toBe('Not replied yet');
  });
});

describe('delivered reply', () => {
  it('stands in for the prompt until the session publishes its own', async () => {
    // @ts-expect-error plain browser module
    const { withDelivered, awaitingReply } = await import('../../internal/web/static/board.js');
    const at = new Date(2026, 0, 2);
    const s = { id: 'k', state: 'idle', prompt: { id: 'p1', text: 'old', at: new Date(2026, 0, 1) }, message: { text: 'done', at: new Date(2026, 0, 1, 1) } };
    expect(withDelivered(s, undefined)).toBe(s);
    const shown = withDelivered(s, { text: 'next', images: 0, at, after: 'p1' });
    expect(shown).toMatchObject({ state: 'working', prompt: { text: 'next', at, unrecorded: true } });
    expect(awaitingReply(shown)).toBe(true);
    expect(withDelivered(s, { text: '', images: 2, at, after: 'p1' }).prompt.text).toBe('2 images');
    expect(withDelivered({ id: 'k', cleared: true }, { text: 'go', images: 0, at, after: '' }).cleared).toBe(false);
    const moved = { ...s, prompt: { id: 'p2', text: 'next', at } };
    expect(withDelivered(moved, { text: 'next', images: 0, at, after: 'p1' })).toBe(moved);
    const answered = { ...s, message: { text: 'answer', at: new Date(2026, 0, 2, 1) } };
    expect(withDelivered(answered, { text: 'next', images: 0, at, after: 'p1' })).toBe(answered);
    // Antigravity numbers steps from 0 in every conversation, so a new conversation drops the stand-in.
    const nextConv = { ...s, conversation: 'conv-2', prompt: { id: '0', text: 'new', at } };
    expect(withDelivered(nextConv, { text: 'new', images: 0, at, after: '0', conversation: 'conv-1' })).toBe(nextConv);
  });
});

describe('task images', () => {
  it('names an image by its SHA-256 and keeps one a remaining task shares', async () => {
    // @ts-expect-error plain browser module
    const { imageID, orphanedImages } = await import('../../internal/web/static/images.js');
    expect(await imageID(new TextEncoder().encode('abc'))).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
    const tasks = [{ id: 'a', images: ['x', 'y'] }, { id: 'b', images: ['y'] }, { id: 'c' }];
    expect(orphanedImages(tasks, [tasks[0]])).toEqual(['x']);
    expect(orphanedImages(tasks, [tasks[2]])).toEqual([]);
    expect(orphanedImages(tasks, [tasks[0], tasks[1]])).toEqual(['x', 'y']);
  });
});

describe('shared text', () => {
  it('keeps each distinct part of a share once', async () => {
    // @ts-expect-error plain browser module
    const { sharedText } = await import('../../internal/web/static/share.js');
    expect(sharedText({})).toBe('');
    expect(sharedText({ title: '', text: '', url: '' })).toBe('');
    expect(sharedText({ title: 'Docs', text: 'Read https://x.dev/a', url: 'https://x.dev/a' })).toBe('Docs\nRead https://x.dev/a');
    expect(sharedText({ title: ' Docs ', url: 'https://x.dev/a' })).toBe('Docs\nhttps://x.dev/a');
  });
});

describe('answerText', () => {
  const question = {
    id: 'toolu_1',
    items: [
      { question: 'Which colour?', header: 'Colour', multi_select: false, options: [{ label: 'Red' }, { label: 'Blue' }] },
      { question: 'Which pets?', header: 'Pets', multi_select: true, options: [{ label: 'Cat' }, { label: 'Dog' }] },
    ],
  };

  it('names each question with its picks and own words', () => {
    expect(answerText(question, [{ chosen: ['Blue'] }, { chosen: ['Cat', 'Dog'], other: ' and a fish ' }])).toBe(
      'My answers to your questions:\n- Which colour? Blue\n- Which pets? Cat; Dog; and a fish',
    );
    expect(answerText(question, [{ other: 'Green' }, { chosen: ['Cat'] }])).toBe(
      'My answers to your questions:\n- Which colour? Green\n- Which pets? Cat',
    );
  });

  it('is empty until every question is answered', () => {
    expect(answerText(question, [{ chosen: ['Blue'] }])).toBe('');
    expect(answerText(question, [{ chosen: ['Blue'] }, { chosen: [], other: '  ' }])).toBe('');
  });
});

describe('usageLeft', () => {
  const now = Date.parse('2026-11-14T12:00:00Z');
  const at = (h: number) => new Date(now + h * 3600_000);

  it('is nothing until the laptop has reported usage', () => {
    expect(usageLeft(null, now)).toBeNull();
  });

  it('turns used into left for each window', () => {
    const left = usageLeft({ five_hour: { used: 23.4, resets_at: at(2) }, seven_day: { used: 100.2, resets_at: at(50) } }, now);
    expect(left.map((w: any) => [w.label, w.left])).toEqual([['5h', 77], ['7d', 0]]);
    expect(left[0].resetsAt).toEqual(at(2));
  });

  it('counts a reset or unreported window as whole', () => {
    const left = usageLeft({ five_hour: { used: 90, resets_at: at(-1) } }, now);
    expect(left.map((w: any) => [w.label, w.left, w.resetsAt])).toEqual([['5h', 100, null], ['7d', 100, null]]);
  });
});

describe('antigravityLeft', () => {
  const now = Date.parse('2026-11-14T12:00:00Z');
  const at = (h: number) => new Date(now + h * 3600_000);

  it('is nothing until Antigravity has reported a pool', () => {
    expect(antigravityLeft(null, now)).toBeNull();
    expect(antigravityLeft({ five_hour: { used: 1, resets_at: at(1) } }, now)).toBeNull();
  });

  it('gives each pool its windows, and the tightest per window across pools', () => {
    const pools = antigravityLeft({ antigravity: [
      { name: 'Gemini', five_hour: { used: 20, resets_at: at(1) }, seven_day: { used: 40, resets_at: at(50) } },
      { name: 'Claude', seven_day: { used: 92, resets_at: at(60) } },
    ] }, now);
    expect(pools.map((p: any) => [p.name, p.windows.map((w: any) => w.left)])).toEqual([['Gemini', [80, 60]], ['Claude', [100, 8]]]);
    expect(tightest(pools).map((w: any) => [w.label, w.left])).toEqual([['5h', 80], ['7d', 8]]);
  });

  it('leaves Claude Code unreported when only Antigravity has reported', () => {
    expect(usageLeft({ antigravity: [{ name: 'Gemini' }] }, now)).toBeNull();
  });
});

describe('quotaState', () => {
  it('is plain while enough is left, low under 10%, out at none', () => {
    expect([100, 10, 9, 1, 0].map(quotaState)).toEqual(['', '', 'low', 'low', 'out']);
  });
});

describe('actionsLeft', () => {
  const now = Date.parse('2026-11-14T12:00:00Z');
  const repos = [{ repo: 'maclir/a', minutes: 410 }];

  it('is nothing until the laptop has reported Actions usage', () => {
    expect(actionsLeft(null, now)).toBeNull();
  });

  it('turns used minutes into what is left this month', () => {
    const resets = new Date('2026-12-01T00:00:00Z');
    expect(actionsLeft({ included: 2000, used: 760, resets_at: resets, repos }, now))
      .toEqual({ left: 1240, included: 2000, pct: 62, resetsAt: resets, repos });
    expect(actionsLeft({ included: 2000, used: 2300, resets_at: resets, repos }, now).left).toBe(0);
  });

  it('counts a month past its reset as whole', () => {
    expect(actionsLeft({ included: 2000, used: 1900, resets_at: new Date('2026-11-01T00:00:00Z'), repos }, now))
      .toEqual({ left: 2000, included: 2000, pct: 100, resetsAt: null, repos: [] });
  });
});

describe('onDefaultBranch', () => {
  it('leaves main and master unmentioned and names any other branch', () => {
    expect(onDefaultBranch({ branch: 'main' })).toBe(true);
    expect(onDefaultBranch({ branch: 'master' })).toBe(true);
    expect(onDefaultBranch({ branch: 'usage-header' })).toBe(false);
    expect(onDefaultBranch({ branch: '' })).toBe(false);
  });
});

describe('queued replies', () => {
  const doc = (over: Record<string, unknown>) => ({
    id: 'c1', type: 'reply', status: 'pending', created_at: new Date(2026, 10, 14, 9),
    payload: JSON.stringify({ id: 'c1', type: 'reply', session: 'k', text: 'go on', conversation: 'conv', after: 'm1' }), ...over,
  });

  it('lists pending and held replies oldest first', () => {
    const got = waitingReplies([
      doc({ id: 'late', status: 'held', created_at: new Date(2026, 10, 14, 10) }),
      doc({ id: 'early' }),
      doc({ id: 'sent', status: 'done' }),
      doc({ id: 'stop', type: 'stop' }),
      doc({ id: 'bad', payload: '{' }),
    ]);
    expect(got.map((r: { id: string }) => r.id)).toEqual(['early', 'late']);
    expect(got[0]).toMatchObject({ session: 'k', text: 'go on', images: [], conversation: 'conv', after: 'm1' });
  });

  it('binds a reply to the conversation and message it answers', () => {
    expect(replyFields({ id: 'k', conversation: 'conv', latest: 'm1' }, 'go on', [])).toEqual({ session: 'k', text: 'go on', conversation: 'conv', after: 'm1' });
    expect(replyFields({ id: 'k' }, 'go on', ['i1'])).toEqual({ session: 'k', text: 'go on', images: ['i1'] });
  });

  it('folds a new reply into the queued one', () => {
    const queued = { text: 'first', images: ['a'] };
    expect(mergeReply(queued, 'second', ['a', 'b'], 4)).toEqual({ text: 'first\n\nsecond', images: ['a', 'b'] });
    expect(mergeReply({ text: '', images: ['a'] }, 'only', [], 4)).toEqual({ text: 'only', images: ['a'] });
    expect(mergeReply(queued, 'x', ['b', 'c', 'd', 'e'], 4)).toBeNull();
  });
});

describe('ideas', () => {
  it('an ideation task without a focus looks at the whole repository', () => {
    expect(ideateText('  ')).toBe('Look across the whole repository.');
    expect(ideateText(' the worker page ')).toBe('the worker page');
  });

  it('lenses turn on in the offered order and the last one stays on', () => {
    const ask = { lenses: ['experience'], max: 5 };
    expect(toggleLens(ask, 'reuse').lenses).toEqual(['reuse', 'experience']);
    expect(toggleLens(ask, 'experience')).toBe(ask);
    expect(toggleLens({ lenses: ['reuse', 'experience'], max: 3 }, 'reuse')).toEqual({ lenses: ['experience'], max: 3 });
  });

  it('names a lens, or shows an id it does not know', () => {
    expect(lensName('experience')).toBe('One experience');
    expect(lensName('vibes')).toBe('vibes');
    expect(LENSES.length).toBe(11);
  });

  it('lists open ideas newest first, for one project or all', () => {
    const ideas = [
      { id: 'a', project: 'knip', status: 'open', created_at: new Date(2026, 10, 26, 9) },
      { id: 'b', project: 'knip', status: 'open', created_at: new Date(2026, 10, 26, 10) },
      { id: 'c', project: 'zynk', status: 'open', created_at: new Date(2026, 10, 26, 8) },
      { id: 'd', project: 'knip', status: 'dismissed', created_at: new Date(2026, 10, 26, 11) },
    ];
    expect(openIdeas(ideas, 'knip').map((i) => i.id)).toEqual(['b', 'a']);
    expect(openIdeas(ideas, null).map((i) => i.id)).toEqual(['b', 'a', 'c']);
    expect(ideaCount(ideas, 'zynk')).toBe(1);
  });
});

describe('model names', () => {
  it('names a model id the way people say it, and its family', () => {
    expect(modelName('claude-opus-5-5')).toBe('Opus 5.5');
    expect(modelName('claude-fable-5-2')).toBe('Fable 5.2');
    expect(modelName('claude-sonnet-5')).toBe('Sonnet 5');
    expect(modelName('opus')).toBe('Opus');
    expect(modelName('gemini-4-neon')).toBe('gemini-4-neon');
    expect(modelFamily('claude-haiku-5-5')).toBe('haiku');
    expect(modelFamily('sonnet')).toBe('sonnet');
    expect(modelFamily(undefined)).toBe('');
  });

  it('offers each Claude Code alias with its budget from 1 to 5', () => {
    const claude = MODEL_POOLS.find((p: any) => p.id === 'claude');
    expect(claude.models.map((m: any) => [m.alias, m.pips])).toEqual([['fable', 5], ['opus', 3], ['sonnet', 2], ['haiku', 1]]);
  });
});

describe('antigravityPools', () => {
  const models = [
    { id: 'gemini-4-neon-high', name: 'Gemini 4 Neon (High)' }, { id: 'gemini-4-neon-low', name: 'Gemini 4 Neon (Low)' },
    { id: 'gemini-4-carbon-medium', name: 'Gemini 4 Carbon (Medium)' }, { id: 'gemini-4-carbon-high', name: 'Gemini 4 Carbon (High)' },
    { id: 'gemini-4-hydrogen-low', name: 'Gemini 4 Hydrogen (Low)' },
    { id: 'gemini-pro-orchestrator', name: 'Orchestrator (Carbon)' },
    { id: 'claude-opus-5-5-high', name: 'Claude Opus 5.5 (High)' }, { id: 'claude-sonnet-5-5-high', name: 'Claude Sonnet 5.5 (High)' },
    { id: 'gpt-oss-120b-medium', name: 'GPT-OSS 120B (Medium)' },
  ];

  it('offers one row per family at high effort, in the pool it draws on, with pips against the pool', () => {
    const pools = antigravityPools(models);
    expect(pools.map((p: any) => p.name)).toEqual(['Gemini', 'Claude']);
    expect(pools[0].models.map((m: any) => [m.id, m.short, m.pips, m.more])).toEqual([
      ['gemini-4-neon-high', '4 Neon', 4, false], ['gemini-4-carbon-high', '4 Carbon', 5, false],
      ['gemini-4-hydrogen-low', '4 Hydrogen', 2, false], ['gemini-pro-orchestrator', 'Orchestrator (Carbon)', 0, true],
    ]);
    expect(pools[1].models.map((m: any) => [m.short, m.pips, m.more])).toEqual([['Opus 5.5', 5, false], ['Sonnet 5.5', 3, false], ['GPT-OSS 120B', 0, true]]);
  });

  it('is nothing without a listing, and names a model without its effort', () => {
    expect(antigravityPools(undefined)).toEqual([]);
    expect(modelBase('Gemini 3.8 Flash (High)')).toBe('Gemini 3.8 Flash');
    expect(modelBase('Orchestrator (Neon)')).toBe('Orchestrator (Neon)');
  });
});

describe('modelVersions', () => {
  it('names each alias by the newest concrete model of its family a Claude Code worker has run', () => {
    expect(modelVersions([
      { model: 'claude-opus-5-5' }, { message: { model: 'claude-opus-5' } },
      { model: 'sonnet', message: { model: 'claude-sonnet-5-5' } }, { model: 'haiku' },
      { tool: 'antigravity', model: 'Gemini 4 Neon (High)' }, {},
    ])).toEqual({ opus: 'Opus 5.5', sonnet: 'Sonnet 5.5' });
  });
});

describe('sessionModel', () => {
  const ag = [
    { id: 'gemini-4-neon-high', name: 'Gemini 4 Neon (High)' },
    { id: 'gemini-4-neon-low', name: 'Gemini 4 Neon (Low)' },
  ];

  it('resolves transcript models, aliases, and Antigravity ids on a worker', () => {
    expect(sessionModel({ model: 'claude-opus-5-5' }, ag)).toEqual({ key: 'opus', name: 'Opus 5.5' });
    expect(sessionModel({ model: 'sonnet' }, ag, { sonnet: 'Sonnet 5.5' })).toEqual({ key: 'sonnet', name: 'Sonnet 5.5' });
    expect(sessionModel({ model: 'sonnet' }, ag, {})).toEqual({ key: 'sonnet', name: 'Sonnet' });
    expect(sessionModel({ message: { model: 'claude-haiku-5-5' } }, ag)).toEqual({ key: 'haiku', name: 'Haiku 5.5' });
    expect(sessionModel({ tool: 'antigravity', model: 'Gemini 4 Neon (High)' }, ag)).toEqual({ key: 'gemini-4-neon-high', name: 'Gemini 4 Neon' });
    expect(sessionModel({ tool: 'antigravity', model: 'gemini-4-neon-low' }, ag)).toEqual({ key: 'gemini-4-neon-high', name: 'Gemini 4 Neon' });
    expect(sessionModel({}, ag)).toEqual({ key: '', name: '' });
  });
});
