import { expect, Locator, Page, test } from '@playwright/test';
import { createHash } from 'node:crypto';

// The test server runs against the Auth and Firestore emulators (make
// test-server). Data the laptop agent would publish is seeded through the
// emulator's REST API with the admin token, which bypasses the rules.
const PROJECT = 'peyk-test';
const FIRESTORE = `http://127.0.0.1:8181/v1/projects/${PROJECT}/databases/(default)/documents`;
const AUTH = `http://127.0.0.1:9190`;
const admin = { Authorization: 'Bearer owner' };
const EMAIL = 'owner@example.com';
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==', 'base64');

type Value = string | number | boolean | Date | null | Value[] | { [k: string]: Value };

function encode(v: Value): Record<string, unknown> {
  if (v === null) return { nullValue: null };
  if (v instanceof Date) return { timestampValue: v.toISOString() };
  if (Array.isArray(v)) return { arrayValue: { values: v.map(encode) } };
  if (typeof v === 'string') return { stringValue: v };
  if (typeof v === 'boolean') return { booleanValue: v };
  if (typeof v === 'number') return Number.isInteger(v) ? { integerValue: String(v) } : { doubleValue: v };
  return { mapValue: { fields: Object.fromEntries(Object.entries(v).map(([k, x]) => [k, encode(x)])) } };
}

async function put(page: Page, path: string, data: Record<string, Value>) {
  const res = await page.request.patch(`${FIRESTORE}/${path}`, {
    headers: admin,
    data: { fields: Object.fromEntries(Object.entries(data).map(([k, v]) => [k, encode(v)])) },
  });
  expect(res.ok(), await res.text()).toBe(true);
}

async function get(page: Page, path: string) {
  const res = await page.request.get(`${FIRESTORE}/${path}`, { headers: admin });
  expect(res.ok(), await res.text()).toBe(true);
  return (await res.json()).fields;
}

async function list(page: Page, collection: string) {
  const res = await page.request.get(`${FIRESTORE}/${collection}`, { headers: admin });
  return ((await res.json()).documents ?? []) as { name: string; fields: Record<string, any> }[];
}

async function signIn(page: Page) {
  const popupPromise = page.waitForEvent('popup');
  await page.getByRole('button', { name: 'Sign in with Google' }).click();
  const popup = await popupPromise;
  await popup.waitForLoadState();
  const existing = popup.getByText(EMAIL);
  if (await existing.isVisible().catch(() => false)) {
    await existing.click();
  } else {
    await popup.getByText('Add new account').click();
    await popup.getByLabel('Email').fill(EMAIL);
    await popup.getByRole('button', { name: /Sign in with Google\.com/ }).click();
  }
  await popup.waitForEvent('close');
}

async function makeOwner(page: Page) {
  const lookup = await page.request.post(`${AUTH}/identitytoolkit.googleapis.com/v1/projects/${PROJECT}/accounts:lookup`, {
    headers: admin, data: { email: [EMAIL] },
  });
  const uid = (await lookup.json()).users[0].localId;
  const update = await page.request.post(`${AUTH}/identitytoolkit.googleapis.com/v1/projects/${PROJECT}/accounts:update`, {
    headers: admin, data: { localId: uid, customAttributes: JSON.stringify({ owner: true }) },
  });
  expect(update.ok()).toBe(true);
}

// Passkeys need a real domain, and the test server is reached by IP, so the
// browser's assertion is stubbed; Go tests verify real signatures. The stub
// counts its prompts in window.prompts.
async function passkeyOn(page: Page) {
  await page.evaluate(() => {
    const bytes = () => new Uint8Array([1, 2, 3]).buffer;
    (window as any).prompts = 0;
    Object.defineProperty(navigator.credentials, 'get', {
      configurable: true,
      value: async () => {
        (window as any).prompts++;
        return { rawId: bytes(), response: { authenticatorData: bytes(), clientDataJSON: bytes(), signature: bytes() } };
      },
    });
  });
}

const prompts = (page: Page) => page.evaluate(() => (window as any).prompts as number);

async function seed(page: Page) {
  const now = new Date();
  const minutesAgo = (m: number) => new Date(now.getTime() - m * 60_000);
  await put(page, 'laptop/status', { online: true, seen_at: now });
  await put(page, 'laptop/usage', {
    five_hour: { used: 23.4, resets_at: new Date(now.getTime() + 2 * 3600_000) },
    seven_day: { used: 95, resets_at: new Date(now.getTime() + 50 * 3600_000) },
  });
  await put(page, 'laptop/actions', {
    included: 2000, used: 760, resets_at: new Date(now.getTime() + 10 * 86400_000),
    repos: [{ repo: 'maclir/hamta', minutes: 410 }, { repo: 'maclir/knip', minutes: 250 }],
  });
  await put(page, 'sessions/k', {
    id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'unpushed', updated_at: now,
    git: {
      branch: 'main', upstream: 'origin/main', unpushed: 2, dirty: 0, last: { hash: '3f2a1c4', subject: 'Report unused exports', at: minutesAgo(12) },
      pending: [{ hash: '3f2a1c4', subject: 'Report unused exports', at: minutesAgo(12) }, { hash: '9b0e7d2', subject: 'Scan barrel files', at: minutesAgo(40) }],
    },
    message: { text: '## Done\n\nCommitted **3f2a1c4**.\n\n- `make test` passes', at: minutesAgo(12) },
    prompt: { text: 'Detect unused exports', at: minutesAgo(20) },
  });
  await put(page, 'sessions/h', {
    id: 'h', title: 'hamta', path: '/c/hamta', aoe_status: 'waiting', state: 'needs_input', updated_at: now,
    message: { text: 'Include archived rows?', at: minutesAgo(4) },
  });
  await put(page, 'projects/knip', { name: 'knip', path: '/c/knip', session_id: 'k', free: true, busy: '', active: minutesAgo(12) });
  await put(page, 'projects/hamta', { name: 'hamta', path: '/c/hamta', session_id: 'h', free: false, busy: 'needs input', active: minutesAgo(4) });
  await put(page, 'tasks/sent', { project: 'hamta', text: 'Add CSV export', status: 'dispatched', session_id: 'h', created_at: minutesAgo(60), dispatched_at: minutesAgo(30) });
  await put(page, 'tasks/old', { project: 'knip', text: 'Detect unused exports', status: 'done', session_id: 'k', conversation_id: 'c-old', created_at: minutesAgo(90), done_at: minutesAgo(12), commit: '3f2a1c4' });
}

test.describe('signed in', () => {
  test.describe.configure({ mode: 'serial' });
  // One browser stays signed in across these steps, as a phone would.
  let page: Page;

  test.beforeAll(async ({ browser, request }) => {
    await request.delete(`http://127.0.0.1:8181/emulator/v1/projects/${PROJECT}/databases/(default)/documents`);
    await request.delete(`${AUTH}/emulator/v1/projects/${PROJECT}/accounts`);
    page = await (await browser.newContext()).newPage();
  });

  test.afterAll(async () => page.context().close());

  test('a new account is not the owner until the laptop says so', async () => {
    await seed(page);
    await page.goto('/');
    await signIn(page);
    await expect(page.getByText(`${EMAIL} is signed in but is not the owner.`)).toBeVisible();
    await expect(page.getByText(`make owner EMAIL=${EMAIL}`)).toBeVisible();

    await makeOwner(page);
    await page.getByRole('button', { name: 'Sign out' }).click();
    await signIn(page);
    await expect(page.getByRole('button', { name: 'Laptop online. Check for updates' })).toBeVisible();
    const usage = page.getByRole('button', { name: 'Usage: Claude 5h 77% left, 7d 5% left; Actions 1,240 of 2,000 minutes left' });
    await expect(usage.locator('.meter.low')).toHaveCount(1);
    await expect(usage.locator('.meter i').first()).toHaveAttribute('style', 'width: 23%;');
    await expect(usage.locator('.meter.gh i')).toHaveAttribute('style', 'width: 38%;');

    await usage.click();
    const pop = page.getByRole('dialog', { name: 'Usage' });
    await expect(pop.getByRole('link', { name: /^Claude 5h/ })).toHaveAttribute('href', 'https://claude.ai/settings/usage');
    const actions = pop.getByRole('link', { name: /^GitHub Actions/ });
    await expect(actions).toHaveAttribute('href', 'https://github.com/settings/billing');
    await expect(actions).toContainText('1,240 left');
    await expect(actions).toContainText('Most used: hamta 410 · knip 250');
    await page.keyboard.press('Escape');
    await expect(pop).toBeHidden();

    const viewport = page.viewportSize()!;
    for (const [width, fits] of [[360, 280], [300, 268]]) {
      await page.setViewportSize({ width, height: viewport.height });
      await usage.click();
      const box = (await pop.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(16);
      expect(box.width).toBe(fits);
      await page.keyboard.press('Escape');
    }
    await page.setViewportSize(viewport);
  });

  test('the board groups sessions and shows what each is doing', async () => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Needs you' })).toBeVisible();
    const workersTab = page.getByRole('link', { name: /^Workers/ });
    await expect(workersTab).toHaveText('Workers2');
    await put(page, 'sessions/z', { id: 'z', title: 'zzz', path: '/c/zzz', aoe_status: 'stopped', state: 'idle', updated_at: new Date() });
    await expect(page.locator('.card', { hasText: 'zzz' })).toBeVisible();
    await expect(workersTab).toHaveText('Workers2');
    await page.request.delete(`${FIRESTORE}/sessions/z`, { headers: admin });
    const hamta = page.locator('.card', { hasText: 'hamta' });
    await expect(hamta.getByText('needs input')).toBeVisible();
    await expect(hamta.getByText('Task: Add CSV export')).toBeVisible();
    const knip = page.locator('.card', { hasText: 'knip' });
    await expect(knip.getByLabel('2 unpushed commits')).toHaveText('2');
    await expect(knip.locator('.chip')).toHaveCount(0);

    await knip.click();
    await expect(page.getByRole('heading', { name: 'Unpushed commits · 2' })).toBeVisible();
    await expect(page.locator('.pending li')).toHaveText([/3f2a1c4 Report unused exports/, /9b0e7d2 Scan barrel files/]);
    await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible();
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');
    await expect(page.locator('.reply strong')).toHaveText('3f2a1c4');
    await expect(page.locator('.reply code')).toHaveText('make test');

    await page.getByRole('link', { name: 'peyk' }).click();
    await expect(page).toHaveURL(/\/#$/);
    await expect(page.getByRole('heading', { name: 'Needs you' })).toBeVisible();
  });

  test('push asks the laptop and shows its answer', async () => {
    await page.goto('/#s-k');
    await expect(page.locator('.facts')).toContainText('main → origin/main');
    await expect(page.locator('.dhead .tag.branch')).toHaveCount(0);
    await page.getByRole('button', { name: 'Push 2' }).click();
    await expect(page.locator('.pushcard .outcome')).toHaveText('Waiting for the laptop…');
    const [cmd] = await list(page, 'commands');
    expect(cmd.fields.type.stringValue).toBe('push');
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'push', session: 'k' });
    expect(cmd.fields.assertion).toBeUndefined();
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(page.locator('.pushcard .outcome')).toHaveText('Pushed');

    // A failure stays beside the button until it is dismissed.
    await page.getByRole('button', { name: 'Push 2' }).click();
    await expect(page.locator('.pushcard .outcome')).toHaveText('Waiting for the laptop…');
    const again = (await list(page, 'commands')).find((c) => c.fields.status.stringValue === 'pending')!;
    await put(page, `commands/${again.name.split('/').pop()}`, { ...plain(again.fields), status: 'failed', error: 'rejected (fetch first)' });
    const failure = page.locator('.pushcard .outcome');
    await expect(failure).toHaveText('Push failed: rejected (fetch first)');
    await expect(failure).toHaveAttribute('data-tone', 'bad');
    await page.waitForTimeout(3500);
    await expect(failure).toBeVisible();
    await failure.getByRole('button', { name: 'Dismiss' }).click();
    await expect(failure).toHaveCount(0);
    for (const c of await list(page, 'commands')) await page.request.delete(`${FIRESTORE}/${c.name.split('/documents/')[1]}`, { headers: admin });
  });

  // Headless Chromium refuses notification permission, so the registration's
  // notifications are stubbed: one per tag, each recording its own close.
  test("opening a worker dismisses its notifications and no one else's", async () => {
    await page.goto('/');
    await expect(page.locator('.card', { hasText: 'knip' })).toBeVisible();
    await page.evaluate(() => {
      const open = new Set(['k', 'h']);
      (window as any).open_notes = open;
      ServiceWorkerRegistration.prototype.getNotifications = async (filter?: { tag?: string }) =>
        [...open].filter((t) => !filter?.tag || t === filter.tag).map((tag) => ({ tag, close: () => open.delete(tag) }) as any);
    });

    await page.evaluate(() => { location.hash = '#s-k'; });
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');
    await expect.poll(() => page.evaluate(() => [...(window as any).open_notes])).toEqual(['h']);
  });

  test('opening a worker tells the laptop, so other devices close its notifications', async () => {
    await page.goto('/#s-k');
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');
    await expect.poll(async () => (await list(page, 'seen')).map((d) => [d.name.split('/').pop(), Boolean(d.fields.seen_at)])).toEqual([['k', true]]);
  });

  test('a dismissal closes its worker\'s notification without showing one', async () => {
    await page.goto('/');
    await page.waitForFunction(() => navigator.serviceWorker.controller);
    const [worker] = page.context().serviceWorkers();
    const got = await worker.evaluate(async () => {
      const reg = (self as any).registration;
      const open = new Set(['k', 'h']);
      const shown: string[] = [];
      reg.showNotification = async (_: string, o: any) => { open.add(o.tag); shown.push(`${o.tag}${o.silent ? ' silently' : ''}`); };
      reg.getNotifications = async ({ tag }: { tag: string }) => (open.has(tag) ? [{ close: () => open.delete(tag) }] : []);
      await (self as any).notify({ session: 'k', dismiss: true });
      await (self as any).notify({ session: 'z', dismiss: true });
      return { open: [...open], shown };
    });
    expect(got).toEqual({ open: ['h'], shown: [] });
  });

  test("a push for the worker on screen is shown silently and closed, others with sound", async () => {
    await page.goto('/');
    await page.waitForFunction(() => navigator.serviceWorker.controller);
    await page.evaluate(() => { location.hash = '#s-k'; });
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');
    const [worker] = page.context().serviceWorkers();
    const shown = await worker.evaluate(async () => {
      const reg = (self as any).registration;
      const out: Record<string, { silent: boolean; renotify: boolean; closed: boolean }> = {};
      reg.showNotification = async (_: string, o: any) => { out[o.tag] = { silent: o.silent, renotify: o.renotify, closed: false }; };
      reg.getNotifications = async ({ tag }: { tag: string }) => [{ close: () => { out[tag].closed = true; } }];
      for (const session of ['k', 'h']) await (self as any).notify({ title: 't', body: '', url: `/#s-${session}`, session });
      return out;
    });
    expect(shown).toEqual({
      k: { silent: true, renotify: false, closed: true },
      h: { silent: false, renotify: true, closed: false },
    });
  });

  test("on Android a push for the worker on screen shows nothing", async () => {
    await page.goto('/');
    await page.waitForFunction(() => navigator.serviceWorker.controller);
    await page.evaluate(() => { location.hash = '#s-k'; });
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');
    const [worker] = page.context().serviceWorkers();
    const shown = await worker.evaluate(async () => {
      const reg = (self as any).registration;
      const out: string[] = [];
      reg.showNotification = async (_: string, o: any) => { out.push(o.tag); };
      reg.getNotifications = async () => [];
      // @ts-ignore the worker's top-level binding
      closeLeavesNote = true;
      try {
        for (const session of ['k', 'h']) await (self as any).notify({ title: 't', body: '', url: `/#s-${session}`, session });
      } finally {
        // @ts-ignore
        closeLeavesNote = false;
      }
      return out;
    });
    expect(shown).toEqual(['h']);
  });

  test("a notification click shows its worker in the open window and is logged", async () => {
    await page.goto('/');
    await page.waitForFunction(() => navigator.serviceWorker.controller);
    await expect(page.locator('.card', { hasText: 'knip' })).toBeVisible();
    const [worker] = page.context().serviceWorkers();
    await worker.evaluate(() => (self as any).open('/#s-k', 'k'));
    await expect(page).toHaveURL(/#s-k$/);
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');

    await page.evaluate(() => { location.hash = '#log'; });
    const click = page.locator('.notelog li', { hasText: '"event":"click"' });
    await expect(click).toContainText('"answered":true');
    await expect(page.locator('.notelog li', { hasText: '"event":"opened"' })).toContainText('#s-k');
  });

  // A phone that freezes the app leaves its Firestore connection dead without
  // an offline event, and the SDK's next retry can be a minute away. Held
  // requests stand in for that dead connection.
  test('coming back to the app reconnects at once', async () => {
    await page.goto('/#s-k');
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');
    let frozen = true;
    await page.context().route(/:8181/, (route) => (frozen ? undefined : route.continue()));
    await page.context().setOffline(true);
    await page.context().setOffline(false);
    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'running', state: 'working', updated_at: new Date(),
      prompt: { text: 'Now add --json', at: new Date() }, message: { text: 'Halfway there', at: new Date() } });
    await page.waitForTimeout(3000);
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');

    frozen = false;
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await expect(page.locator('.reply.sent')).toHaveText('Now add --json', { timeout: 3000 });
    await expect(page.locator('.reply').last()).toHaveText('Halfway there');
    await page.context().unrouteAll({ behavior: 'ignoreErrors' });

    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'running', state: 'working', updated_at: new Date(),
      prompt: { text: 'And --yaml', at: new Date() }, message: { text: 'Halfway there', at: new Date(Date.now() - 60000) } });
    await expect(page.locator('.reply.sent')).toHaveText('And --yaml');
    await expect(page.locator('.reply.pending')).toHaveText('Working…');
  });

  test('a stopped worker can be started and a running one restarted, each signed', async () => {
    await put(page, 'sessions/z', { id: 'z', title: 'zzz', path: '/c/zzz', aoe_status: 'stopped', state: 'idle', updated_at: new Date() });
    await page.goto('/#s-z');
    await passkeyOn(page);
    const actions = page.getByRole('button', { name: 'Worker actions' });
    await actions.click();
    await expect(page.getByRole('menuitem', { name: 'Restart worker' })).toHaveCount(0);
    await expect(page.getByRole('menuitem', { name: 'Stop worker' })).toHaveCount(0);
    await page.getByRole('menuitem', { name: 'Start worker', exact: true }).click();
    await expect(page.getByRole('menu')).toHaveCount(0);
    await expect(page.locator('.dhead .outcome')).toHaveText('Waiting for the laptop…');
    const start = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'start')!;
    expect(JSON.parse(start.fields.payload.stringValue)).toMatchObject({ type: 'start', session: 'z' });
    expect(start.fields.grant).toBeDefined();
    await put(page, `commands/${start.name.split('/').pop()}`, { ...plain(start.fields), status: 'done' });
    await expect(page.locator('.dhead .outcome')).toHaveText('Started');
    await put(page, 'sessions/z', { id: 'z', title: 'zzz', path: '/c/zzz', aoe_status: 'idle', state: 'idle', updated_at: new Date() });
    await actions.click();
    await expect(page.getByRole('menuitem', { name: 'Stop worker' })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: 'Start worker', exact: true })).toHaveCount(0);
    await page.locator('.detail h2').click();
    await expect(page.getByRole('menu')).toHaveCount(0);
    await actions.click();
    await page.getByRole('menuitem', { name: 'Restart worker' }).click();
    await expect(page.locator('.dhead .outcome')).toHaveText('Waiting for the laptop…');
    const restart = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'restart')!;
    expect(JSON.parse(restart.fields.payload.stringValue)).toMatchObject({ type: 'restart', session: 'z' });
    await put(page, `commands/${restart.name.split('/').pop()}`, { ...plain(restart.fields), status: 'done' });
    await expect(page.locator('.dhead .outcome')).toHaveText('Restarted');
    await page.request.delete(`${FIRESTORE}/sessions/z`, { headers: admin });
  });

  test('tapping the laptop checks for updates, and Update is signed', async () => {
    await page.goto('/');
    await expect(page.getByRole('button', { name: /^Update the laptop/ })).toBeHidden();
    await page.getByRole('button', { name: 'Laptop online. Check for updates' }).click();
    await expect(page.locator('.hpop')).toHaveText('Waiting for the laptop…');
    const check = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'check')!;
    expect(check.fields.assertion).toBeUndefined();
    expect(check.fields.grant).toBeUndefined();
    await put(page, 'laptop/version', { commit: 'abc1234', behind: 2 });
    await put(page, `commands/${check.name.split('/').pop()}`, { ...plain(check.fields), status: 'done' });
    await expect(page.locator('.hpop')).toHaveText('2 new commits to update to');
    await expect(page.getByRole('button', { name: 'Laptop online at abc1234. Check for updates' })).toBeVisible();

    await passkeyOn(page);
    await page.getByRole('button', { name: 'Update the laptop: 2 new commits' }).click();
    await expect(page.locator('.hpop')).toHaveText('Waiting for the laptop…');
    const upd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'update')!;
    expect(upd.fields.grant).toBeDefined();
    await put(page, 'laptop/version', { commit: 'def5678', behind: 0 });
    await put(page, `commands/${upd.name.split('/').pop()}`, { ...plain(upd.fields), status: 'done' });
    await expect(page.locator('.hpop')).toHaveText('Updated');
    await expect(page.getByRole('button', { name: /^Update the laptop/ })).toBeHidden();
  });

  test('a send that does not go out says so on its task', async () => {
    await put(page, 'laptop/status', { online: false, seen_at: new Date() });
    await put(page, 'tasks/proto', { project: 'knip', text: 'Open prototypes from a session', status: 'queued', created_at: new Date() });
    await page.goto('/#inbox');
    await expect(page.getByRole('button', { name: 'Laptop offline, seen just now. Check for updates' })).toBeVisible();
    const task = page.locator('.task', { hasText: 'Open prototypes from a session' });
    await task.getByRole('button', { name: /More actions/ }).click();
    await task.getByRole('menuitem', { name: 'Send without clearing' }).click();
    await expect(task.getByRole('status')).toHaveText('Not sent: the laptop is offline.');
    await page.request.delete(`${FIRESTORE}/tasks/proto`, { headers: admin });
    await put(page, 'laptop/status', { online: true, seen_at: new Date() });
  });

  test('a task clears its worker first unless the worker is already cleared', async () => {
    await put(page, 'tasks/ctx', { project: 'knip', text: 'Start fresh', status: 'queued', created_at: new Date() });
    await page.goto('/#inbox');
    await passkeyOn(page);
    const task = page.locator('.task', { hasText: 'Start fresh' });
    await expect(task.locator('.tstatus')).toHaveText('Free');
    await expect(task.locator('button.send .ico.broom')).toHaveCount(1);
    await task.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(task.getByRole('status')).toHaveText('Waiting for the laptop…');
    await expect(task.getByRole('button', { name: 'Send', exact: true })).toHaveAttribute('aria-busy', 'true');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'dispatch')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'dispatch', project: 'knip', task: 'ctx', clear: true });
    expect(cmd.fields.assertion).toBeUndefined();
    expect(Object.keys(cmd.fields.grant.mapValue.fields).sort()).toEqual(['assertion', 'signature', 'statement']);
    expect(JSON.parse(cmd.fields.grant.mapValue.fields.statement.stringValue)).toMatchObject({ kind: 'peyk-session' });
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(task.getByRole('status')).toHaveText('Sent');
    await expect(task.getByRole('button', { name: 'Send', exact: true })).not.toHaveAttribute('aria-busy', 'true');
    await page.request.delete(`${FIRESTORE}/${cmd.name.split('/documents/')[1]}`, { headers: admin });

    // The approval outlives a reload, so the next send asks for no passkey.
    await page.reload();
    await passkeyOn(page);
    await task.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(task.getByRole('status')).toHaveText('Waiting for the laptop…');
    expect(await prompts(page)).toBe(0);
    const again = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'dispatch')!;
    expect(again.fields.grant.mapValue.fields.statement.stringValue).toBe(cmd.fields.grant.mapValue.fields.statement.stringValue);
    await page.request.delete(`${FIRESTORE}/${again.name.split('/documents/')[1]}`, { headers: admin });

    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: true });
    await task.getByRole('button', { name: /More actions/ }).click();
    await expect(task.getByRole('menuitem', { name: 'Edit task: Start fresh' })).toBeVisible();
    await expect(task.getByRole('menuitem', { name: 'Send without clearing' })).toHaveCount(0);
    // A tap outside the menu closes it.
    await task.locator('.ttext').click();
    await expect(task.getByRole('menu')).toHaveCount(0);
    await expect(task.locator('button.send .ico.broom')).toHaveCount(0);
    await page.request.delete(`${FIRESTORE}/tasks/ctx`, { headers: admin });
  });

  test('the inbox adds, edits and deletes tasks', async () => {
    await page.goto('/#inbox');
    await expect(page.getByRole('button', { name: /^Ready/ })).toHaveAttribute('aria-pressed', 'true');
    await page.getByRole('button', { name: 'Project: hamta' }).click();
    const sheet = page.getByRole('dialog', { name: 'Choose a project' });
    await expect(sheet.getByRole('radio', { name: /hamta/ })).toHaveAttribute('aria-checked', 'true');
    // Keys typed one at a time land in the same input.
    const search = sheet.getByLabel('Search projects');
    await search.evaluate((el) => { (el as HTMLInputElement & { kept?: boolean }).kept = true; });
    await search.pressSequentially('kn');
    await expect(search).toHaveValue('kn');
    await expect(search).toBeFocused();
    expect(await search.evaluate((el) => (el as HTMLInputElement & { kept?: boolean }).kept)).toBe(true);
    await expect(sheet.getByRole('radio')).toHaveCount(1);
    await sheet.getByRole('radio', { name: /knip/ }).click();
    await expect(sheet).toHaveCount(0);
    await page.getByLabel('New task').fill('Add --json output');
    await page.getByLabel('New task').press('Meta+Enter');
    await expect(page.getByLabel('New task')).toHaveValue('');

    const task = page.locator('.task', { hasText: 'Add --json output' });
    await expect(task.locator('.proj')).toContainText('knip');
    await expect(task.getByRole('button', { name: 'Send', exact: true })).toBeVisible();

    await task.getByRole('button', { name: /More actions/ }).click();
    await task.getByRole('menuitem', { name: /Edit task/ }).click();
    const editor = page.locator('form.composer.edit');
    await editor.getByLabel('Task text').fill('Add --json and --csv output');
    await editor.getByRole('button', { name: 'Project: knip' }).click();
    await page.getByRole('dialog', { name: 'Choose a project' }).getByRole('radio', { name: /hamta/ }).click();
    await expect(editor.getByRole('button', { name: 'Project: hamta' })).toBeVisible();
    await expect(editor.getByLabel('Task text')).toHaveValue('Add --json and --csv output');
    await editor.getByLabel('Task text').press('Control+Enter');
    const edited = page.locator('.task', { hasText: 'Add --json and --csv output' });
    await expect(edited.locator('.proj')).toContainText('hamta');
    await expect(edited.getByText('needs input')).toBeVisible();
    await expect(page.getByRole('button', { name: /^All/ })).toHaveAttribute('aria-pressed', 'true');

    await expect(page.locator('.task.done')).toHaveCount(0);
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(page.locator('.task.done', { hasText: 'Detect unused exports' })).toContainText('3f2a1c4');
    await edited.getByRole('button', { name: /More actions/ }).click();
    await edited.getByRole('menuitem', { name: /Delete task/ }).click();
    await expect(edited).toHaveCount(0);
  });

  test('the picker hides projects, finds hidden ones, and undoes a hide', async () => {
    const areh = { name: 'areh', path: '/c/areh', session_id: '', free: true, busy: '' };
    await put(page, 'projects/areh', areh);
    await page.goto('/#inbox');
    await passkeyOn(page);
    await page.getByRole('button', { name: /^Project:/ }).click();
    const sheet = page.getByRole('dialog', { name: 'Choose a project' });
    await expect(sheet.getByLabel('Search projects')).toHaveAttribute('placeholder', 'Search 3 projects');
    await sheet.getByRole('button', { name: 'Edit' }).click();
    await expect(sheet.getByRole('heading', { name: 'Hide projects' })).toBeVisible();
    await expect(sheet.getByRole('button', { name: 'Hide', exact: true })).toBeDisabled();
    await sheet.getByRole('checkbox', { name: /areh/ }).click();
    await expect(sheet.getByRole('checkbox', { name: /areh/ })).toHaveAttribute('aria-checked', 'true');
    await sheet.getByRole('button', { name: 'Hide 1' }).click();

    const sent = async (type: string) => (await list(page, 'commands')).find((c) => c.fields.type.stringValue === type);
    await expect.poll(() => sent('hide')).toBeTruthy();
    const hide = (await sent('hide'))!;
    expect(JSON.parse(hide.fields.payload.stringValue)).toMatchObject({ type: 'hide', projects: ['areh'] });
    await expect(sheet.getByRole('heading', { name: 'Send to' })).toBeVisible();
    await put(page, 'projects/areh', { ...areh, hidden: true });
    await put(page, `commands/${hide.name.split('/').pop()}`, { ...plain(hide.fields), status: 'done' });
    await expect(sheet.getByText('Hid areh.')).toBeVisible();
    await expect(sheet.getByRole('radio', { name: /areh/ })).toHaveCount(0);
    await expect(sheet.getByLabel('Search projects')).toHaveAttribute('placeholder', 'Search 2 projects');

    await expect(sheet.getByRole('button', { name: 'Show areh' })).toHaveCount(0);
    await sheet.getByRole('button', { name: /^Hidden/ }).click();
    await expect(sheet.getByRole('button', { name: 'Show areh' })).toBeVisible();
    await sheet.getByLabel('Search projects').fill('are');
    await expect(sheet.getByText('Matches · 1')).toBeVisible();
    await expect(sheet.getByRole('button', { name: 'Show areh' })).toBeVisible();

    await sheet.getByRole('button', { name: 'Undo' }).click();
    await expect.poll(() => sent('show')).toBeTruthy();
    const show = (await sent('show'))!;
    expect(JSON.parse(show.fields.payload.stringValue)).toMatchObject({ type: 'show', projects: ['areh'] });
    await put(page, 'projects/areh', areh);
    await expect(sheet.getByRole('radio', { name: /areh/ })).toBeVisible();
    await sheet.getByRole('button', { name: 'Cancel' }).click();
    await expect(sheet).toHaveCount(0);

    for (const c of [hide, show]) await page.request.delete(`${FIRESTORE}/${c.name.split('/documents/')[1]}`, { headers: admin });
    await page.request.delete(`${FIRESTORE}/projects/areh`, { headers: admin });
  });

  test('a long press on Add task sends the task when its worker is free', async () => {
    await page.goto('/#inbox');
    await passkeyOn(page);
    const button = page.getByRole('button', { name: 'Add task' });
    // The button shows the send icon during a press only when the press will send.
    const press = async (sends: boolean) => {
      await button.hover();
      await page.mouse.down();
      await page.waitForTimeout(300);
      await expect(button.locator('.ico.held')).toBeVisible({ visible: sends });
      await expect(button.locator('.ico.addtask')).toBeVisible({ visible: !sends });
      await page.waitForTimeout(400);
      await page.mouse.up();
    };

    await page.getByRole('button', { name: /^Project:/ }).click();
    await page.getByRole('dialog').getByRole('radio', { name: /hamta/ }).click();
    await page.getByLabel('New task').fill('Wait for hamta');
    await press(false);
    const waiting = page.locator('.task', { hasText: 'Wait for hamta' });
    await expect(waiting).toContainText('Waiting · needs input');
    expect((await list(page, 'commands')).filter((c) => c.fields.type.stringValue === 'dispatch')).toHaveLength(0);
    // The page shows the task before the server has committed it.
    const stored = async () => (await list(page, 'tasks')).find((t) => t.fields.text.stringValue === 'Wait for hamta');
    await expect.poll(stored).toBeTruthy();
    await page.request.delete(`${FIRESTORE}/${(await stored())!.name.split('/documents/')[1]}`, { headers: admin });

    await page.getByRole('button', { name: /^Project:/ }).click();
    await page.getByRole('dialog').getByRole('radio', { name: /knip/ }).click();
    await page.getByLabel('New task').fill('Ship it now');
    await press(true);
    await expect(page.getByLabel('New task')).toHaveValue('');
    const task = page.locator('.task', { hasText: 'Ship it now' });
    await expect(task.getByRole('status')).toHaveText('Waiting for the laptop…');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'dispatch')!;
    const payload = JSON.parse(cmd.fields.payload.stringValue);
    expect(payload).toMatchObject({ type: 'dispatch', project: 'knip', text: 'Ship it now', clear: false });
    await page.request.delete(`${FIRESTORE}/${cmd.name.split('/documents/')[1]}`, { headers: admin });
    await page.request.delete(`${FIRESTORE}/tasks/${payload.task}`, { headers: admin });
  });

  test('a sent task stays in the inbox, linked to its worker once the worker appears', async () => {
    await put(page, 'tasks/starting', { project: 'lala', text: 'Set up the repo', status: 'dispatched', session_id: 'new', created_at: new Date(Date.now() - 120_000), dispatched_at: new Date(Date.now() - 60_000) });
    await page.goto('/#inbox');
    await expect(page.getByRole('heading', { name: 'Sent 2' })).toBeVisible();
    const starting = page.locator('.task.sent', { hasText: 'Set up the repo' });
    await expect(starting.locator('.rstat')).toHaveText('starting · 1m');
    const csv = page.locator('.task.sent', { hasText: 'Add CSV export' });
    await expect(csv.getByRole('link', { name: 'hamta · needs input' })).toBeHidden();
    await csv.getByRole('button', { name: /^hamta Add CSV export/ }).click();
    await expect(csv.getByRole('button', { name: /^hamta Add CSV export/ })).toHaveAttribute('aria-expanded', 'true');
    await csv.getByRole('link', { name: 'hamta · needs input' }).click();
    await expect(page).toHaveURL(/#s-h$/);
    await page.request.delete(`${FIRESTORE}/tasks/starting`, { headers: admin });
  });

  test('a task carries a screenshot to its worker and drops it when deleted', async () => {
    await page.goto('/#inbox');
    await passkeyOn(page);
    await page.getByRole('button', { name: /^Project:/ }).click();
    await page.getByRole('dialog').getByRole('radio', { name: /knip/ }).click();
    await page.getByLabel('New task').fill('Match this layout');
    await page.getByLabel('Attach images').setInputFiles({ name: 'shot.png', mimeType: 'image/png', buffer: PNG });
    await expect(page.locator('form.add .thumb img')).toHaveCount(1);
    await page.getByRole('button', { name: 'Add task' }).click();
    await expect(page.getByLabel('New task')).toHaveValue('');
    await expect(page.locator('form.add .thumb')).toHaveCount(0);

    const task = page.locator('.task', { hasText: 'Match this layout' });
    await expect(task.locator('.thumb img')).toHaveCount(1);
    const id = createHash('sha256').update(PNG).digest('hex');
    const stored = await get(page, `attachments/${id}`);
    expect(Buffer.from(stored.data.bytesValue, 'base64')).toEqual(PNG);
    expect(stored.type.stringValue).toBe('image/png');

    // An edit takes images the same way, and drops the ones removed.
    const edit = async () => {
      await task.getByRole('button', { name: /More actions/ }).click();
      await task.getByRole('menuitem', { name: /Edit task/ }).click();
      return page.locator('form.composer.edit');
    };
    let editor = await edit();
    await expect(editor.locator('.thumb img')).toHaveCount(1);
    await editor.getByRole('button', { name: 'Remove image 1' }).click();
    await editor.getByRole('button', { name: 'Save' }).click();
    await expect(editor).toHaveCount(0);
    await expect(task.locator('.thumb')).toHaveCount(0);
    await expect.poll(async () => (await page.request.get(`${FIRESTORE}/attachments/${id}`, { headers: admin })).status()).toBe(404);
    editor = await edit();
    await editor.getByLabel('Attach images').setInputFiles({ name: 'shot.png', mimeType: 'image/png', buffer: PNG });
    await expect(editor.locator('.thumb img')).toHaveCount(1);
    await editor.getByRole('button', { name: 'Save' }).click();
    await expect(editor).toHaveCount(0);
    await expect(task.locator('.thumb img')).toHaveCount(1);

    await task.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(task.getByRole('status')).toHaveText('Waiting for the laptop…');
    const cmd = (await list(page, 'commands')).find((c) => JSON.parse(c.fields.payload.stringValue).text === 'Match this layout')!;
    expect(JSON.parse(cmd.fields.payload.stringValue).images).toEqual([id]);

    await task.getByRole('button', { name: /More actions/ }).click();
    await task.getByRole('menuitem', { name: /Delete task/ }).click();
    await expect(task).toHaveCount(0);
    await expect.poll(async () => (await page.request.get(`${FIRESTORE}/attachments/${id}`, { headers: admin })).status()).toBe(404);
  });

  test("Android's share sheet attaches a screenshot to a new task", async () => {
    await page.goto('/');
    await page.waitForFunction(() => navigator.serviceWorker.controller);
    // The share sheet posts the manifest's share_target form as a navigation.
    await page.evaluate((png) => {
      const form = Object.assign(document.createElement('form'), { method: 'post', action: '/share', enctype: 'multipart/form-data' });
      const input = Object.assign(document.createElement('input'), { type: 'file', name: 'images' });
      const files = new DataTransfer();
      files.items.add(new File([Uint8Array.from(atob(png), (c) => c.charCodeAt(0))], 'shot.png', { type: 'image/png' }));
      input.files = files.files;
      const text = Object.assign(document.createElement('input'), { type: 'hidden', name: 'text', value: 'Shared from Photos' });
      form.append(input, text);
      document.body.append(form);
      form.submit();
    }, PNG.toString('base64'));
    await expect(page).toHaveURL(/\/#inbox$/);
    await expect(page.locator('form.add .thumb img')).toHaveCount(1);
    await expect(page.getByLabel('New task')).toHaveValue('Shared from Photos');

    // The share is used once; a reload starts empty.
    await page.reload();
    await expect(page.getByLabel('New task')).toBeVisible();
    await expect(page.locator('form.add .thumb')).toHaveCount(0);
    await expect(page.getByLabel('New task')).toHaveValue('');
  });

  test('a reply carries a screenshot to its worker', async () => {
    await put(page, 'laptop/status', { online: true, seen_at: new Date() });
    await page.goto('/#s-k');
    await passkeyOn(page);
    const reply = page.locator('form.composer.respond');
    await reply.getByLabel('Attach images').setInputFiles({ name: 'shot.png', mimeType: 'image/png', buffer: PNG });
    await expect(reply.locator('.thumb img')).toHaveCount(1);
    await reply.getByRole('button', { name: 'Send reply' }).click();
    await expect(page.locator('.waitrow')).toContainText('Sending…');
    await expect(reply.locator('.thumb')).toHaveCount(0);
    const id = createHash('sha256').update(PNG).digest('hex');
    expect((await get(page, `attachments/${id}`)).type.stringValue).toBe('image/png');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'reply')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'reply', session: 'k', text: '', images: [id] });

    // The laptop has saved the image once it answers, so it is not kept.
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(page.locator('.waitrow')).toHaveCount(0);
    await expect(page.locator('form.composer.respond .outcome')).toHaveCount(0);
    await expect.poll(async () => (await page.request.get(`${FIRESTORE}/attachments/${id}`, { headers: admin })).status()).toBe(404);

    // The delivered reply is the prompt until the laptop publishes its own.
    await expect(page.locator('.reply.sent')).toHaveText('1 image');
    await expect(page.locator('.reply.pending')).toHaveText('Working…');
    const promptOnly = `${FIRESTORE}/sessions/k?updateMask.fieldPaths=prompt`;
    const before = await get(page, 'sessions/k');
    const patched = await page.request.patch(promptOnly, { headers: admin, data: { fields: { prompt: encode({ id: 'p-shot', text: '[Image #1]', at: new Date() }) } } });
    expect(patched.ok()).toBe(true);
    await expect(page.locator('.reply.sent')).toHaveText('[Image #1]');
    const restored = await page.request.patch(promptOnly, { headers: admin, data: { fields: { prompt: before.prompt } } });
    expect(restored.ok()).toBe(true);
    await page.request.delete(`${FIRESTORE}/${cmd.name.split('/documents/')[1]}`, { headers: admin });
  });

  test('a reply written while the laptop is away waits for it', async () => {
    await put(page, 'laptop/status', { online: false, seen_at: new Date(Date.now() - 3600_000) });
    const bound = await page.request.patch(`${FIRESTORE}/sessions/k?updateMask.fieldPaths=conversation&updateMask.fieldPaths=latest`, {
      headers: admin, data: { fields: { conversation: encode('conv'), latest: encode('m1') } },
    });
    expect(bound.ok()).toBe(true);
    const viewport = page.viewportSize()!;
    await page.setViewportSize({ width: viewport.width, height: 300 });
    await page.goto('/#s-k');
    await passkeyOn(page);
    const reply = page.locator('form.composer.respond');
    await reply.getByLabel('Reply', { exact: true }).fill('Also add --csv');
    await page.evaluate(() => window.scrollTo(0, 0));
    await reply.getByRole('button', { name: 'Send reply' }).click();
    await expect(page.getByText('Queued until the laptop is back online')).toBeVisible();
    await expect(page.locator('.reply.queued')).toBeInViewport();
    await page.setViewportSize(viewport);

    // A second reply joins the queued one rather than waiting behind it.
    await reply.getByLabel('Reply', { exact: true }).fill('And --yaml');
    await reply.getByRole('button', { name: 'Send reply' }).click();
    await expect(page.locator('.reply.queued')).toHaveCount(1);
    await expect(page.locator('.reply.queued')).toContainText('And --yaml');
    // The page draws its own batch before the emulator has committed it.
    const queuedReplies = async () => (await list(page, 'commands')).filter((c) => c.fields.type.stringValue === 'reply' && c.fields.payload.stringValue.includes('--csv'));
    await expect.poll(async () => (await queuedReplies()).map((c) => c.fields.status.stringValue).sort()).toEqual(['cancelled', 'pending']);
    const mine = await queuedReplies();
    const waiting = mine.find((c) => c.fields.status.stringValue === 'pending')!;
    expect(JSON.parse(waiting.fields.payload.stringValue)).toMatchObject({ session: 'k', text: 'Also add --csv\n\nAnd --yaml', conversation: 'conv', after: 'm1' });

    // The laptop came back to a conversation that moved on, so it asks first.
    await put(page, `commands/${waiting.name.split('/').pop()}`, { ...plain(waiting.fields), status: 'held' });
    await expect(page.getByText('Held: the conversation moved on since you wrote this')).toBeVisible();
    await page.goto('/#');
    await expect(page.locator('.card', { hasText: 'knip' })).toContainText('reply held');
    await page.goto('/#s-k');
    await page.getByRole('button', { name: 'Discard' }).click();
    await expect(page.locator('.reply.queued')).toHaveCount(0);
    await expect.poll(async () => (await get(page, `commands/${waiting.name.split('/').pop()}`)).status.stringValue).toBe('cancelled');

    for (const c of mine) await page.request.delete(`${FIRESTORE}/${c.name.split('/documents/')[1]}`, { headers: admin });
    await put(page, 'laptop/status', { online: true, seen_at: new Date() });
  });

  test('the inbox filters by state and project, and keeps the order you set', async () => {
    await put(page, 'tasks/one', { project: 'knip', text: 'First thing', status: 'queued', created_at: new Date(Date.now() - 3000) });
    await put(page, 'tasks/two', { project: 'knip', text: 'Second thing', status: 'queued', created_at: new Date(Date.now() - 2000) });
    await put(page, 'tasks/wait', { project: 'hamta', text: 'Waits on hamta', status: 'queued', created_at: new Date(Date.now() - 1000) });
    await page.goto('/#inbox');
    const texts = page.locator('.tasks .ttext');
    await page.getByRole('button', { name: /^Ready/ }).click();
    await expect(texts).toHaveText(['First thing', 'Second thing']);
    await expect(page.locator('a[href="#inbox"] .n')).toHaveText('2');
    await page.getByRole('button', { name: /^All/ }).click();
    await expect(texts).toHaveText(['First thing', 'Second thing', 'Waits on hamta']);

    await page.getByRole('button', { name: /^Waiting/ }).click();
    await expect(texts).toHaveText(['Waits on hamta']);
    await expect(page.locator('.task', { hasText: 'Waits on hamta' })).toContainText('Waiting · needs input');
    await page.getByRole('button', { name: /^All/ }).click();
    await page.getByRole('group', { name: 'Filter by project' }).getByRole('button', { name: 'knip' }).click();
    await expect(texts).toHaveText(['First thing', 'Second thing']);
    await page.getByRole('group', { name: 'Filter by project' }).getByRole('button', { name: 'knip' }).click();

    await page.getByRole('button', { name: 'Reorder' }).click();
    await page.getByRole('button', { name: 'Move up: Waits on hamta' }).click();
    await expect(texts).toHaveText(['First thing', 'Waits on hamta', 'Second thing']);
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(texts).toHaveText(['First thing', 'Waits on hamta', 'Second thing']);
    expect((await get(page, 'tasks/wait')).rank).toBeDefined();

    for (const id of ['one', 'two', 'wait']) await page.request.delete(`${FIRESTORE}/tasks/${id}`, { headers: admin });
  });

  test("a worker's inbox sheet lists, adds and sends its project's tasks", async () => {
    await put(page, 'tasks/one', { project: 'knip', text: 'First thing', status: 'queued', created_at: new Date(Date.now() - 3000) });
    await put(page, 'tasks/two', { project: 'knip', text: 'Second thing', status: 'queued', created_at: new Date(Date.now() - 2000) });
    await put(page, 'tasks/wait', { project: 'hamta', text: 'Waits on hamta', status: 'queued', created_at: new Date(Date.now() - 1000) });
    await page.goto('/#s-h');
    await expect(page.locator('.detail')).toBeVisible();
    await expect(page.locator('nav.tabs')).toHaveCount(0);
    await page.getByRole('button', { name: 'Inbox, 1 queued' }).click();
    let sheet = page.getByRole('dialog', { name: 'hamta inbox' });
    await expect(sheet.locator('.ttext')).toHaveText(['Waits on hamta']);
    await expect(sheet.locator('.sheethead')).toHaveText(/^.?Inbox$/);
    await expect(sheet.getByRole('button', { name: 'Send', exact: true })).toHaveCount(0);
    await sheet.getByRole('button', { name: 'Close' }).click();
    await expect(sheet).toBeHidden();

    await page.goto('/#s-k');
    await passkeyOn(page);
    await page.getByRole('button', { name: 'Inbox, 2 queued' }).click();
    sheet = page.getByRole('dialog', { name: 'knip inbox' });
    await expect(sheet.locator('.ttext')).toHaveText(['First thing', 'Second thing']);
    await expect(sheet.getByRole('button', { name: 'Send', exact: true })).toHaveCount(2);

    await sheet.getByRole('button', { name: 'Reorder' }).click();
    await sheet.getByRole('button', { name: 'Move down: First thing' }).click();
    await expect(sheet.locator('.ttext')).toHaveText(['Second thing', 'First thing']);
    await sheet.getByRole('button', { name: 'Move up: First thing' }).click();
    await expect(sheet.locator('.ttext')).toHaveText(['First thing', 'Second thing']);
    await sheet.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(sheet.getByRole('button', { name: 'Send', exact: true })).toHaveCount(2);

    // The worker turning busy mid-sentence leaves the same box, text and focus.
    const box = sheet.getByLabel('New task for knip');
    await box.evaluate((el) => { (el as HTMLTextAreaElement & { kept?: boolean }).kept = true; });
    await box.pressSequentially('Third');
    const knip = { name: 'knip', path: '/c/knip', session_id: 'k', active: new Date() };
    await put(page, 'projects/knip', { ...knip, free: false, busy: 'working' });
    await expect(sheet.getByRole('button', { name: 'Send', exact: true })).toHaveCount(0);
    await box.pressSequentially(' thing');
    await expect(box).toHaveValue('Third thing');
    await expect(box).toBeFocused();
    expect(await box.evaluate((el) => (el as HTMLTextAreaElement & { kept?: boolean }).kept)).toBe(true);
    await put(page, 'projects/knip', { ...knip, free: true, busy: '' });

    await sheet.getByRole('button', { name: 'Add task' }).click();
    await expect(box).toHaveValue('');
    await expect(sheet.locator('.ttext')).toHaveText(['Third thing', 'First thing', 'Second thing']);
    await expect(page.getByRole('button', { name: 'Inbox, 3 queued' })).toBeVisible();
    const stored = async () => (await list(page, 'tasks')).find((t) => t.fields.text.stringValue === 'Third thing');
    await expect.poll(stored).toBeTruthy();
    expect((await stored())!.fields.project.stringValue).toBe('knip');

    await sheet.locator('.task', { hasText: 'First thing' }).getByRole('button', { name: 'Send', exact: true }).click();
    await expect(sheet.locator('.task', { hasText: 'First thing' }).getByRole('status')).toHaveText('Waiting for the laptop…');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'dispatch' && JSON.parse(c.fields.payload.stringValue).task === 'one')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'dispatch', project: 'knip', task: 'one' });
    await put(page, 'tasks/one', { project: 'knip', text: 'First thing', status: 'dispatched', session_id: 'k', created_at: new Date(Date.now() - 3000), dispatched_at: new Date() });
    await expect(sheet).toBeHidden();
    await expect(page.getByRole('button', { name: 'Inbox, 2 queued' })).toBeVisible();

    await page.request.delete(`${FIRESTORE}/${cmd.name.split('/documents/')[1]}`, { headers: admin });
    await page.request.delete(`${FIRESTORE}/${(await stored())!.name.split('/documents/')[1]}`, { headers: admin });
    for (const id of ['one', 'two', 'wait']) await page.request.delete(`${FIRESTORE}/tasks/${id}`, { headers: admin });
  });

  test("a worker's inbox sheet reads its done tasks only when opened, and resumes one's conversation", async () => {
    const conversation = (id: string) => page.request.patch(`${FIRESTORE}/sessions/k?updateMask.fieldPaths=conversation&updateMask.fieldPaths=cleared`,
      { headers: admin, data: { fields: { conversation: { stringValue: id }, cleared: { booleanValue: false } } } });
    await conversation('c-now');
    await page.goto('/#s-k');
    await passkeyOn(page);
    await page.getByRole('button', { name: /^Inbox/ }).click();
    const sheet = page.getByRole('dialog', { name: 'knip inbox' });
    await expect(sheet.locator('.task.done')).toHaveCount(0);
    const fold = sheet.getByRole('button', { name: 'Done', exact: true });
    await fold.click();
    const old = sheet.locator('.task.done', { hasText: 'Detect unused exports' });
    await expect(old).toContainText('3f2a1c4');

    await old.getByRole('button', { name: 'Clear and resume: Detect unused exports' }).click();
    await expect(sheet.getByRole('status')).toHaveText('Waiting for the laptop…');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'resume')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'resume', session: 'k', conversation: 'c-old' });
    expect(cmd.fields.grant).toBeDefined();
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(sheet).toBeHidden();
    await expect(page.locator('.toast')).toBeHidden();

    await conversation('c-old');
    await page.getByRole('button', { name: /^Inbox/ }).click();
    await expect(sheet.locator('.alert')).toBeEmpty();
    await expect(old).toContainText('current conversation');
    await expect(old.getByRole('button', { name: /resume/i })).toHaveCount(0);
    await fold.click();
    await expect(sheet.locator('.task.done')).toHaveCount(0);
    await sheet.getByRole('button', { name: 'Close' }).click();
    await page.request.delete(`${FIRESTORE}/${cmd.name.split('/documents/')[1]}`, { headers: admin });
  });

  test('Show more turns into a spinner until the next page of done tasks arrives', async () => {
    const ids = Array.from({ length: 10 }, (_, i) => `more${i}`);
    for (const [i, id] of ids.entries()) {
      await put(page, `tasks/${id}`, { project: 'knip', text: `Older task ${i}`, status: 'done', created_at: new Date(Date.now() - 3600_000), done_at: new Date(Date.now() - 60_000 * (i + 1)) });
    }
    await page.goto('/#inbox');
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(page.locator('.task.done')).toHaveCount(10);
    await page.evaluate(() => {
      const w = window as any;
      w.sawPending = false;
      new MutationObserver(() => { w.sawPending ||= Boolean(document.querySelector('[aria-label="Loading more done tasks"] .spin')); })
        .observe(document.body, { childList: true, subtree: true });
    });
    await page.getByRole('button', { name: 'Show more' }).click();
    await expect(page.locator('.task.done')).toHaveCount(11);
    expect(await page.evaluate(() => (window as any).sawPending)).toBe(true);
    await expect(page.getByRole('button', { name: 'Show more' })).toHaveCount(0);
    await expect(page.getByRole('status', { name: 'Loading more done tasks' })).toHaveCount(0);
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(page.locator('.task.done')).toHaveCount(0);
    for (const id of ids) await page.request.delete(`${FIRESTORE}/tasks/${id}`, { headers: admin });
  });

  test('a sheet goes away with a drag down its top or a tap on its bar, and stays for a short pull', async () => {
    await put(page, 'tasks/one', { project: 'knip', text: 'First thing', status: 'queued', created_at: new Date() });
    const pull = async (from: Locator, to: number) => {
      const box = (await from.boundingBox())!;
      const x = box.x + box.width / 2, y = box.y + box.height / 2;
      await page.mouse.move(x, y);
      await page.mouse.down();
      await page.mouse.move(x, to < 0 ? page.viewportSize()!.height - 2 : y + to, { steps: 10 });
      await page.mouse.up();
    };

    await page.goto('/#inbox');
    const open = () => page.getByRole('button', { name: /^Project: / }).first().click();
    const picker = page.getByRole('dialog', { name: 'Choose a project' });
    await open();
    await pull(picker.locator('.sheethead h2'), 20);
    await expect(picker).toBeVisible();
    expect(await picker.evaluate((el) => (el as HTMLElement).style.transform)).toBe('');
    await pull(picker.locator('.sheethead h2'), -1);
    await expect(picker).toHaveCount(0);
    await open();
    await picker.locator('.grabber').click();
    await expect(picker).toHaveCount(0);

    await page.goto('/#s-k');
    await page.getByRole('button', { name: 'Inbox, 1 queued' }).click();
    const sheet = page.getByRole('dialog', { name: 'knip inbox' });
    await expect(sheet).toBeVisible();
    await pull(sheet.locator('.grabber'), -1);
    await expect(sheet).toBeHidden();

    await page.request.delete(`${FIRESTORE}/tasks/one`, { headers: admin });
  });

  test('the composer tucks while scrolling down and returns at the end without changing the page length', async () => {
    const ids = Array.from({ length: 30 }, (_, i) => `long${i}`);
    for (const [i, id] of ids.entries()) {
      await put(page, `tasks/${id}`, { project: 'knip', text: `Long list item ${i}`, status: 'queued', created_at: new Date(Date.now() - 60000 + i) });
    }
    await page.goto('/#inbox');
    await expect(page.locator('.task', { hasText: 'Long list item 29' })).toBeVisible();
    await page.locator('#task-text').blur();
    const edges = await page.evaluate(() => {
      const r = document.querySelector('form.composer:not(.edit)')!.getBoundingClientRect();
      const y = r.top + r.height / 2;
      return [r.left + 1, r.right - 1].map((x) => !!document.elementFromPoint(x, y)?.closest('.composer'));
    });
    expect(edges).toEqual([true, true]);
    const height = await page.evaluate(() => document.documentElement.scrollHeight);
    await page.mouse.wheel(0, 300);
    await expect(page.locator('html')).toHaveClass(/tuck/);
    expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBe(height);
    for (let i = 0; i < 20; i++) await page.mouse.wheel(0, 400);
    await expect.poll(() => page.evaluate(() => window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 1)).toBe(true);
    await expect(page.locator('html')).not.toHaveClass(/tuck/);
    expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBe(height);
    for (const id of ids) await page.request.delete(`${FIRESTORE}/tasks/${id}`, { headers: admin });
  });

  test('projects show their logo, or their initial when they have none', async () => {
    const logo = `data:image/png;base64,${PNG.toString('base64')}`;
    await put(page, 'projects/knip', { name: 'knip', path: '/c/knip', session_id: 'k', free: true, busy: '', active: new Date(), logo });
    await page.goto('/');
    await expect(page.locator('.card', { hasText: 'knip' }).locator('img.face')).toHaveAttribute('src', logo);
    await expect(page.locator('.card', { hasText: 'hamta' }).locator('.face.mono')).toHaveText('H');
    await page.goto('/#inbox');
    await expect(page.getByRole('button', { name: /^Project:/ }).locator('.face')).toHaveCount(1);
  });

  test('a worker that published a prototype links to it', async () => {
    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(),
      prototypes: [{ url: 'https://claude.ai/artifact/abc', title: 'Knip table sign', at: new Date() }] });
    await page.goto('/');
    await expect(page.locator('.card', { hasText: 'knip' }).getByLabel('prototype', { exact: true })).toHaveText('');
    await page.locator('.card', { hasText: 'knip' }).click();
    const link = page.getByRole('link', { name: 'Knip table sign' });
    await expect(link).toHaveAttribute('href', 'https://claude.ai/artifact/abc');
    await expect(link).toHaveAttribute('target', '_blank');
  });

  test("a worker shows its pushed commit's CI and links to the run", async () => {
    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(),
      git: { branch: 'main', upstream: 'origin/main', unpushed: 0, dirty: 0,
        ci: { sha: '3f2a1c4e', workflow: 'Test and deploy', state: 'failure', url: 'https://github.com/o/knip/actions/runs/7' } } });
    await page.goto('/');
    const ci = page.locator('.card', { hasText: 'knip' }).getByLabel('Test and deploy failed');
    await expect(ci).toHaveText('CI');
    await expect(ci).toHaveClass(/ci-failure bad/);
    await page.locator('.card', { hasText: 'knip' }).click();
    const link = page.getByRole('link', { name: 'Test and deploy failed' });
    await expect(link).toHaveAttribute('href', 'https://github.com/o/knip/actions/runs/7');
    await expect(link).toHaveAttribute('target', '_blank');
  });

  test('a worker is cleared on its own and shows a new conversation', async () => {
    const git = { branch: 'main', upstream: 'origin/main', unpushed: 0, dirty: 0 };
    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: false, git });
    await page.goto('/#s-k');
    await passkeyOn(page);
    await expect(page.locator('.detail h2 .chip.idle')).toHaveText('idle');
    await expect(page.locator('.dhead .tag')).toHaveCount(0);
    await expect(page.locator('.reply.sent.none')).toHaveText('Nothing sent yet.');
    await page.getByRole('button', { name: 'Worker actions' }).click();
    await page.getByRole('menuitem', { name: 'Clear conversation' }).click();
    await expect(page.locator('.dhead .outcome')).toHaveText('Waiting for the laptop…');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'clear')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'clear', session: 'k' });

    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: true, git,
      prompt: { text: '/clear', at: new Date() } });
    await expect(page.locator('.reply.sent:not(.none)')).toHaveText('/clear');
    await expect(page.locator('.reply.pending')).toHaveCount(0);
    await page.getByRole('button', { name: 'Worker actions' }).click();
    await expect(page.getByRole('menuitem', { name: 'Clear conversation' })).toHaveCount(0);
    await page.goto('/');
    await expect(page.locator('.card', { hasText: 'knip' }).locator('.msg')).toHaveText('New conversation');
    await expect(page.locator('.card', { hasText: 'knip' }).locator('.tag')).toHaveCount(0);
    await expect(page.locator('.card', { hasText: 'hamta' }).getByText('New conversation')).toHaveCount(0);
  });

  test('Clear and stop sends one signed stop that clears', async () => {
    await put(page, 'sessions/c', { id: 'c', title: 'cleo', path: '/c/cleo', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: false });
    await page.goto('/#s-c');
    await passkeyOn(page);
    await page.getByRole('button', { name: 'Worker actions' }).click();
    await page.getByRole('menuitem', { name: 'Clear and stop' }).click();
    await expect(page.getByRole('menu')).toHaveCount(0);
    await expect(page.locator('.dhead .outcome')).toHaveText('Waiting for the laptop…');
    const sent = (await list(page, 'commands')).filter((c) => JSON.parse(c.fields.payload.stringValue).session === 'c');
    expect(sent).toHaveLength(1);
    expect(JSON.parse(sent[0].fields.payload.stringValue)).toMatchObject({ type: 'stop', session: 'c', clear: true });
    expect(sent[0].fields.grant).toBeDefined();

    await put(page, 'sessions/c', { id: 'c', title: 'cleo', path: '/c/cleo', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: true });
    await page.getByRole('button', { name: 'Worker actions' }).click();
    await expect(page.getByRole('menuitem', { name: 'Stop worker' })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: 'Clear and stop' })).toHaveCount(0);
    await page.request.delete(`${FIRESTORE}/sessions/c`, { headers: admin });
  });

  test('the quick action beside the inbox clears and stops a finished worker in one tap', async () => {
    await put(page, 'sessions/f', { id: 'f', title: 'fini', path: '/c/fini', aoe_status: 'idle', state: 'unpushed', updated_at: new Date(), cleared: false });
    await page.goto('/#s-f');
    await passkeyOn(page);
    await page.getByRole('button', { name: 'Clear and stop' }).click();
    await expect(page.locator('.dhead .outcome')).toHaveText('Waiting for the laptop…');
    const sent = (await list(page, 'commands')).filter((c) => JSON.parse(c.fields.payload.stringValue).session === 'f');
    expect(sent).toHaveLength(1);
    expect(JSON.parse(sent[0].fields.payload.stringValue)).toMatchObject({ type: 'stop', session: 'f', clear: true });

    await put(page, 'sessions/f', { id: 'f', title: 'fini', path: '/c/fini', aoe_status: 'stopped', state: 'unpushed', updated_at: new Date(), cleared: true });
    await expect(page.getByRole('button', { name: 'Start', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Clear and stop' })).toHaveCount(0);
    await put(page, 'sessions/f', { id: 'f', title: 'fini', path: '/c/fini', aoe_status: 'running', state: 'working', updated_at: new Date(), cleared: false });
    await expect(page.getByRole('button', { name: 'Start', exact: true })).toHaveCount(0);
    await page.request.delete(`${FIRESTORE}/sessions/f`, { headers: admin });
  });

  test("a worker's question is answered from the phone", async () => {
    const asking = {
      id: 'q', title: 'quiz', path: '/c/quiz', aoe_status: 'waiting', state: 'needs_input', updated_at: new Date(),
      question: { id: 'toolu_1', at: new Date(), items: [
        { question: 'Which colour?', header: 'Colour', multi_select: false, options: [{ label: 'Red', description: 'warm' }, { label: 'Blue', description: 'cool' }] },
        { question: 'Which pets?', header: 'Pets', multi_select: true, options: [{ label: 'Cat', description: '' }, { label: 'Dog', description: '' }] },
      ] },
    };
    await put(page, 'sessions/q', asking);
    await page.goto('/');
    await expect(page.locator('.card', { hasText: 'quiz' }).locator('.msg')).toHaveText('Asks: Which colour? · Which pets?');
    await page.locator('.card', { hasText: 'quiz' }).click();
    await passkeyOn(page);
    const send = page.getByRole('button', { name: 'Send answers' });
    await expect(send).toBeDisabled();
    await page.getByLabel('Blue').check();
    await expect(send).toBeDisabled();
    await page.getByLabel('Cat').check();
    await page.getByLabel('Own answer: Which pets?').fill('a fish');
    // A redraw keeps the picks made so far.
    await put(page, 'sessions/q', { ...asking, updated_at: new Date() });
    await expect(page.getByLabel('Blue')).toBeChecked();
    await send.click();
    await expect(page.locator('.qcard .outcome')).toHaveText('Waiting for the laptop…');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'answer')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({
      type: 'answer', session: 'q', question: 'toolu_1',
      text: 'My answers to your questions:\n- Which colour? Blue\n- Which pets? Cat; a fish',
    });
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(page.locator('.qcard .outcome')).toHaveText('Sent');

    const { question, ...answered } = asking;
    await put(page, 'sessions/q', { ...answered, aoe_status: 'running', state: 'working' });
    await expect(page.locator('.qcard')).toHaveCount(0);
  });

  test('opening the whole conversation keeps the latest messages in view', async () => {
    const viewport = page.viewportSize()!;
    await page.setViewportSize({ width: viewport.width, height: 400 });
    await put(page, 'sessions/w', { id: 'w', title: 'wide', path: '/c/wide', aoe_status: 'idle', state: 'idle', updated_at: new Date(),
      prompt: { text: 'Last ask', at: new Date() }, message: { text: 'Last answer', at: new Date() }, latest: 'r1' });
    await page.goto('/#s-w');
    const fromBottom = () => page.evaluate(() => document.documentElement.scrollHeight - window.scrollY);
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
    const before = await fromBottom();
    // A dispatched click, unlike a pointer click, does not scroll the button into view first.
    await page.getByRole('button', { name: 'Whole conversation' }).dispatchEvent('click');
    await expect.poll(async () => (await list(page, 'history')).length).toBe(1);
    const asked = (await get(page, 'history/w')).requested_at.timestampValue;
    const at = new Date();
    const entries = Array.from({ length: 30 }, (_, i) => ({ kind: i % 2 ? 'reply' : 'prompt', text: `Message ${i}`, at }));
    await put(page, 'history/w', { requested_at: new Date(asked), served_at: new Date(asked), truncated: false,
      entries: [...entries, { kind: 'prompt', text: 'Last ask', at }, { kind: 'reply', text: 'Last answer', at }] });
    await expect(page.getByText('Message 0', { exact: true })).toBeAttached();
    expect(await fromBottom()).toBe(before);
    await page.getByText('Message 15', { exact: true }).evaluate((el) => el.scrollIntoView({ block: 'center' }));
    const less = page.getByRole('button', { name: 'Latest only' });
    await expect(less).toBeInViewport();
    // A dispatched click keeps the scroll position the user closed it from.
    await less.dispatchEvent('click');
    await expect.poll(async () => (await list(page, 'history')).length).toBe(0);
    await expect(page.getByRole('button', { name: 'Whole conversation' })).toBeInViewport();
    await expect(page.getByText('Last ask', { exact: true })).toBeInViewport();
    await page.setViewportSize(viewport);
  });

  test("a worker's whole conversation is read from the laptop only while it is open", async () => {
    const worker = { id: 'w', title: 'wide', path: '/c/wide', aoe_status: 'idle', state: 'idle', updated_at: new Date(),
      prompt: { text: 'Second ask', at: new Date() }, message: { text: 'Second answer', at: new Date() }, latest: 'r2' };
    await put(page, 'sessions/w', worker);
    await page.goto('/#s-w');
    await page.getByRole('button', { name: 'Whole conversation' }).click();
    await expect(page.getByText('Reading the conversation on the laptop…')).toBeVisible();
    await expect.poll(async () => (await list(page, 'history')).length).toBe(1);
    const asked = (await get(page, 'history/w')).requested_at.timestampValue;

    // The laptop answers by copying the request time it served.
    const at = new Date();
    await put(page, 'history/w', { requested_at: new Date(asked), served_at: new Date(asked), truncated: false, entries: [
      { kind: 'prompt', text: 'First ask', at },
      { kind: 'reply', text: 'Looking.', at },
      { kind: 'tool', tool: 'Bash', text: 'make test', at },
      { kind: 'tool', tool: 'Edit', text: 'main.go', at },
      { kind: 'reply', text: 'First **answer**', at },
      { kind: 'prompt', text: 'Second ask', at },
      { kind: 'reply', text: 'Second answer', at },
    ] });
    const convo = page.locator('.detail .convo').first();
    await expect(convo.locator('.reply.sent')).toHaveText(['First ask', 'Second ask']);
    await expect(convo.locator('.reply:not(.sent)')).toHaveText(['Looking.', 'First answer', 'Second answer']);
    await expect(convo.locator('.who')).toHaveText([/^You/, /^wide/, /^You/, /^wide/]);
    const tools = convo.locator('details.tools');
    await expect(tools.locator('summary')).toHaveText('2 tool calls');
    await tools.locator('summary').click();
    await expect(tools.locator('li')).toHaveText(['Bash make test', 'Edit main.go']);

    // A new reply asks the laptop again.
    await put(page, 'sessions/w', { ...worker, message: { text: 'Third answer', at: new Date() }, latest: 'r3' });
    await expect.poll(async () => (await get(page, 'history/w')).requested_at.timestampValue).not.toBe(asked);

    await page.getByRole('button', { name: 'Latest only' }).click();
    await expect(page.getByRole('button', { name: 'Whole conversation' })).toBeVisible();
    await expect.poll(async () => (await list(page, 'history')).length).toBe(0);
    await expect(convo.locator('.reply:not(.sent)')).toHaveText('Third answer');
  });
});

// Strips Firestore's typed values back to plain ones for a rewrite.
function plain(fields: Record<string, any>): Record<string, Value> {
  const out: Record<string, Value> = {};
  for (const [k, v] of Object.entries(fields)) {
    if ('stringValue' in v) out[k] = v.stringValue;
    else if ('timestampValue' in v) out[k] = new Date(v.timestampValue);
    else if ('booleanValue' in v) out[k] = v.booleanValue;
  }
  return out;
}

// An icon on a home screen has no browser chrome behind it, so a launch with no
// signal must reach the app rather than the browser's error page. Data is never
// cached — only this honest stand-in for it.
test('opens with no signal and says so', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();

  await page.goto('/');
  await page.evaluate(() => navigator.serviceWorker.ready);

  await context.setOffline(true);
  await page.reload();

  await expect(page.getByRole('heading', { name: 'Offline' })).toBeVisible();

  await context.close();
});

test('the footer checks the deployed revision on tap and reloads when another is deployed', async ({ page }) => {
  const deployed = (await (await page.request.get('/version')).json()).revision;
  let checks = 0;
  // context.route also sees the request when the service worker makes it.
  await page.context().route('**/version', (route) => { checks++; return route.fallback(); });
  await page.goto('/');
  const rev = page.locator('#rev');
  await expect(rev).toHaveText(deployed);
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
  expect(checks).toBe(0);

  await rev.click();
  await expect.poll(() => checks).toBe(1);
  await expect(rev).toBeEnabled();
  await expect(rev).not.toHaveClass(/stale/);
  await expect(rev).toHaveText(deployed);

  await page.context().unroute('**/version');
  await page.context().route('**/version', (route) => route.fulfill({ json: { revision: 'f00dfee' } }));
  await rev.click();
  await expect(rev).toHaveClass(/stale/);
  await page.evaluate(() => caches.open('peyk-old').then((c) => c.put('/x', new Response('x'))));

  await page.context().unroute('**/version');
  await Promise.all([page.waitForEvent('load'), rev.click()]);
  await expect(rev).not.toHaveClass(/stale/);
  expect(await page.evaluate(() => caches.has('peyk-old'))).toBe(false);
});
