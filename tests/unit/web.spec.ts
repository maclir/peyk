import { describe, expect, it } from 'vitest';
// @ts-expect-error plain browser modules without type declarations
import { renderMarkdown } from '../../internal/web/static/markdown.js';
// @ts-expect-error plain browser modules without type declarations
import { STALE_AFTER_MS, ago, answerText, ciStatus, laptopOnline, mergeReply, pushLabel, quickAction, replyFields, sortSessions, tags, usageLeft, onDefaultBranch, waitingReplies } from '../../internal/web/static/board.js';

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

  it('keeps code spans literal', () => {
    expect(renderMarkdown('`**not bold**`')).toContain('<code>**not bold**</code>');
  });
});

describe('board', () => {
  it('offers one quick action by worker state and inbox', () => {
    const w = (aoe_status: string, state: string, cleared = false) => ({ aoe_status, state, cleared });
    expect(quickAction(w('stopped', 'stopped'), 0)).toBe('start');
    expect(quickAction(w('stopped', 'unpushed'), 2)).toBe('start');
    expect(quickAction(w('idle', 'unpushed'), 0)).toBe('clearstop');
    expect(quickAction(w('idle', 'idle'), 0)).toBe('clearstop');
    expect(quickAction(w('idle', 'idle', true), 0)).toBe('stop');
    expect(quickAction(w('idle', 'unpushed'), 1)).toBe('clear');
    expect(quickAction(w('idle', 'idle', true), 1)).toBeNull();
    for (const state of ['working', 'needs_input', 'error', 'dirty']) expect(quickAction(w('idle', state), 0)).toBeNull();
  });

  it('sorts by what needs attention, then by title when nothing has happened', () => {
    const got = sortSessions([
      { title: 'b', state: 'idle' },
      { title: 'z', state: 'needs_input' },
      { title: 'a', state: 'unpushed' },
      { title: 'a', state: 'idle' },
    ]).map((s: any) => `${s.state}:${s.title}`);
    expect(got).toEqual(['needs_input:z', 'unpushed:a', 'idle:a', 'idle:b']);
  });

  it('sorts within a state by most recent activity, and unpushed work by its newest commit', () => {
    const got = sortSessions([
      { title: 'a', state: 'needs_input', message: { at: '2026-11-17T12:01:00Z' } },
      { title: 'b', state: 'needs_input', message: { at: '2026-11-17T12:01:00Z' }, prompt: { at: '2026-11-17T12:05:00Z' } },
      { title: 'c', state: 'error', message: { at: '2026-11-17T12:09:00Z' } },
      { title: 'd', state: 'unpushed', message: { at: '2026-11-17T12:09:00Z' }, git: { last: { at: '2026-11-17T12:01:00Z' } } },
      { title: 'e', state: 'unpushed', git: { last: { at: '2026-11-17T12:02:00Z' } } },
      { title: 'f', state: 'idle' },
      { title: 'g', state: 'idle', message: { at: new Date('2026-11-17T12:03:00Z') } },
    ]).map((s: any) => s.title);
    expect(got).toEqual(['b', 'a', 'c', 'e', 'd', 'g', 'f']);
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
      prototypes: [{ url: 'a' }, { url: 'b' }] });
    expect(all.map((t) => [t.kind, t.tone, t.text, t.label])).toEqual([
      ['edit', 'warn', '1', '1 uncommitted file'],
      ['push', 'ok', '3', '3 unpushed commits'],
      ['noremote', '', 'no remote branch', 'no remote branch'],
      ['branch', '', 'fix', 'branch fix'],
      ['ci-failure', 'bad', 'CI', 'CI failed'],
      ['frame', 'acc', '2', '2 prototypes'],
    ]);
    expect(all[4].url).toBe(url);
    expect(tags({ git: { ...pushed, upstream: '' } }).map((t) => t.text)).toEqual(['no remote']);
    expect(tags({ git: pushed, prototypes: [{ url: 'a' }] })).toEqual([{ kind: 'frame', tone: 'acc', text: '', label: 'prototype' }]);
    expect(tags({ git: { ...pushed, ci: { state: 'mystery' } } })[0].kind).toBe('ci-queued');
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
  it('finds the task a session is on and lists finished tasks newest first', async () => {
    // @ts-expect-error plain browser module
    const { currentTask, doneTasks, sentTasks } = await import('../../internal/web/static/board.js');
    const tasks = [
      { id: 'a', status: 'dispatched', session_id: 'k', dispatched_at: new Date(2026, 0, 1), text: 'old' },
      { id: 'b', status: 'dispatched', session_id: 'k', dispatched_at: new Date(2026, 0, 2), text: 'new' },
      { id: 'c', status: 'done', session_id: 'k', done_at: new Date(2026, 0, 3) },
      { id: 'd', status: 'done', session_id: 'h', done_at: new Date(2026, 0, 4) },
    ];
    expect(currentTask(tasks, 'k').id).toBe('b');
    expect(currentTask(tasks, 'h')).toBe(null);
    expect(doneTasks(tasks).map((t: any) => t.id)).toEqual(['d', 'c']);
    expect(sentTasks(tasks).map((t: any) => t.id)).toEqual(['b', 'a']);
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
