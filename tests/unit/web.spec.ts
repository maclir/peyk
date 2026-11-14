import { describe, expect, it } from 'vitest';
// @ts-expect-error plain browser modules without type declarations
import { renderMarkdown } from '../../internal/web/static/markdown.js';
// @ts-expect-error plain browser modules without type declarations
import { STALE_AFTER_MS, ago, laptopOnline, pushLabel, sortSessions } from '../../internal/web/static/board.js';

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

  it('keeps code spans literal', () => {
    expect(renderMarkdown('`**not bold**`')).toContain('<code>**not bold**</code>');
  });
});

describe('board', () => {
  it('sorts by what needs attention, then by title', () => {
    const got = sortSessions([
      { title: 'b', state: 'idle' },
      { title: 'z', state: 'needs_input' },
      { title: 'a', state: 'unpushed' },
      { title: 'a', state: 'idle' },
    ]).map((s: any) => `${s.state}:${s.title}`);
    expect(got).toEqual(['needs_input:z', 'unpushed:a', 'idle:a', 'idle:b']);
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

  it('splits queued tasks by whether their project is free', async () => {
    const { inboxGroups } = await load();
    const { ready, waiting } = inboxGroups(
      [
        { project: 'knip', text: 'later', status: 'queued', created_at: new Date(2026, 0, 2) },
        { project: 'knip', text: 'first', status: 'queued', created_at: new Date(2026, 0, 1) },
        { project: 'hamta', text: 'h', status: 'queued', created_at: new Date(2026, 0, 1) },
        { project: 'gone', text: 'g', status: 'queued', created_at: new Date(2026, 0, 1) },
        { project: 'knip', text: 'sent', status: 'dispatched', created_at: new Date(2026, 0, 1) },
      ],
      [{ name: 'knip', free: true }, { name: 'hamta', free: false, busy: 'working' }],
    );
    expect(ready.map((t: any) => t.text)).toEqual(['first', 'later']);
    expect(waiting.map((t: any) => `${t.text}:${t.why}`)).toEqual(['h:working', 'g:project no longer exists']);
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
