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
