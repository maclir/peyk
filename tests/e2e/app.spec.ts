import { expect, Page, test } from '@playwright/test';
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
  await put(page, 'tasks/old', { project: 'knip', text: 'Detect unused exports', status: 'done', session_id: 'k', created_at: minutesAgo(90), done_at: minutesAgo(12), commit: '3f2a1c4' });
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
    await expect(page.getByRole('img', { name: 'Laptop online' })).toBeVisible();
    const usage = page.getByRole('link', { name: 'Claude usage: 5h 77% left, 7d 5% left' });
    await expect(usage).toHaveAttribute('href', 'https://claude.ai/settings/usage');
    await expect(usage.locator('.meter.low')).toHaveCount(1);
    await expect(usage.locator('.meter i').first()).toHaveAttribute('style', 'width: 23%;');
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
    await expect(knip.getByText('2 unpushed')).toBeVisible();

    await knip.click();
    await expect(page.getByRole('heading', { name: 'Unpushed commits · 2' })).toBeVisible();
    await expect(page.locator('.pending li')).toHaveText([/3f2a1c4 Report unused exports/, /9b0e7d2 Scan barrel files/]);
    await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible();
    await expect(page.locator('.reply.sent')).toHaveText('Detect unused exports');
    await expect(page.locator('.reply strong')).toHaveText('3f2a1c4');
    await expect(page.locator('.reply code')).toHaveText('make test');
  });

  test('push asks the laptop and shows its answer', async () => {
    await page.goto('/#s-k');
    await expect(page.locator('.facts')).toContainText('main → origin/main');
    await expect(page.locator('.dhead .branch')).toHaveCount(0);
    await page.getByRole('button', { name: 'Push 2' }).click();
    await expect(page.getByText('Push knip: waiting for the laptop…')).toBeVisible();
    const [cmd] = await list(page, 'commands');
    expect(cmd.fields.type.stringValue).toBe('push');
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'push', session: 'k' });
    expect(cmd.fields.assertion).toBeUndefined();
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(page.getByText('Push knip: done.')).toBeVisible();
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

  test('a send that does not go out says so on its task', async () => {
    await put(page, 'laptop/status', { online: false, seen_at: new Date() });
    await put(page, 'tasks/proto', { project: 'knip', text: 'Open prototypes from a session', status: 'queued', created_at: new Date() });
    await page.goto('/#inbox');
    await expect(page.getByRole('img', { name: 'Laptop offline, seen just now' })).toBeVisible();
    const task = page.locator('.task', { hasText: 'Open prototypes from a session' });
    await task.getByRole('button', { name: /More actions/ }).click();
    await task.getByRole('button', { name: 'Send without clearing' }).click();
    await expect(task.getByRole('status')).toHaveText('Send to knip: the laptop is offline, so nothing was sent.');
    await page.request.delete(`${FIRESTORE}/tasks/proto`, { headers: admin });
    await put(page, 'laptop/status', { online: true, seen_at: new Date() });
  });

  test('a task clears its worker first unless the worker is already cleared', async () => {
    await put(page, 'tasks/ctx', { project: 'knip', text: 'Start fresh', status: 'queued', created_at: new Date() });
    await page.goto('/#inbox');
    await passkeyOn(page);
    const task = page.locator('.task', { hasText: 'Start fresh' });
    await expect(task).toContainText('Free · clears the conversation first');
    await task.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(task.getByRole('status')).toHaveText('Clear knip and send: waiting for the laptop…');
    await expect(task.getByRole('button', { name: 'Send', exact: true })).toHaveAttribute('aria-busy', 'true');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'dispatch')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'dispatch', project: 'knip', task: 'ctx', clear: true });
    expect(cmd.fields.assertion).toBeUndefined();
    expect(Object.keys(cmd.fields.grant.mapValue.fields).sort()).toEqual(['assertion', 'signature', 'statement']);
    expect(JSON.parse(cmd.fields.grant.mapValue.fields.statement.stringValue)).toMatchObject({ kind: 'peyk-session' });
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(task.getByRole('status')).toHaveText('Clear knip and send: done.');
    await expect(task.getByRole('button', { name: 'Send', exact: true })).not.toHaveAttribute('aria-busy', 'true');
    await page.request.delete(`${FIRESTORE}/${cmd.name.split('/documents/')[1]}`, { headers: admin });

    // The approval outlives a reload, so the next send asks for no passkey.
    await page.reload();
    await passkeyOn(page);
    await task.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(task.getByRole('status')).toHaveText('Clear knip and send: waiting for the laptop…');
    expect(await prompts(page)).toBe(0);
    const again = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'dispatch')!;
    expect(again.fields.grant.mapValue.fields.statement.stringValue).toBe(cmd.fields.grant.mapValue.fields.statement.stringValue);
    await page.request.delete(`${FIRESTORE}/${again.name.split('/documents/')[1]}`, { headers: admin });

    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: true });
    await task.getByRole('button', { name: /More actions/ }).click();
    await expect(task.getByRole('button', { name: 'Move to top' })).toBeVisible();
    await expect(task.getByRole('button', { name: 'Send without clearing' })).toHaveCount(0);
    await page.request.delete(`${FIRESTORE}/tasks/ctx`, { headers: admin });
  });

  test('the inbox adds, edits and deletes tasks', async () => {
    await page.goto('/#inbox');
    await expect(page.getByRole('button', { name: /^Ready/ })).toHaveAttribute('aria-pressed', 'true');
    await page.getByRole('button', { name: 'Project: hamta' }).click();
    const sheet = page.getByRole('dialog', { name: 'Choose a project' });
    await expect(sheet.getByRole('radio', { name: /hamta/ })).toHaveAttribute('aria-checked', 'true');
    await sheet.getByLabel('Search projects').fill('kn');
    await expect(sheet.getByRole('radio')).toHaveCount(1);
    await sheet.getByRole('radio', { name: /knip/ }).click();
    await expect(sheet).toHaveCount(0);
    await page.getByLabel('New task').fill('Add --json output');
    await page.getByRole('button', { name: 'Add task' }).click();
    await expect(page.getByLabel('New task')).toHaveValue('');

    const task = page.locator('.task', { hasText: 'Add --json output' });
    await expect(task.locator('.proj')).toContainText('knip');
    await expect(task.getByRole('button', { name: 'Send', exact: true })).toBeVisible();

    await task.getByRole('button', { name: /More actions/ }).click();
    await task.getByRole('button', { name: /Edit task/ }).click();
    const editor = page.locator('form.composer.edit');
    await editor.getByLabel('Task text').fill('Add --json and --csv output');
    await editor.getByRole('button', { name: 'Project: knip' }).click();
    await page.getByRole('dialog', { name: 'Choose a project' }).getByRole('radio', { name: /hamta/ }).click();
    await expect(editor.getByRole('button', { name: 'Project: hamta' })).toBeVisible();
    await expect(editor.getByLabel('Task text')).toHaveValue('Add --json and --csv output');
    await editor.getByRole('button', { name: 'Save' }).click();
    const edited = page.locator('.task', { hasText: 'Add --json and --csv output' });
    await expect(edited.locator('.proj')).toContainText('hamta');
    await expect(edited.getByText('needs input')).toBeVisible();
    await expect(page.getByRole('button', { name: /^All/ })).toHaveAttribute('aria-pressed', 'true');

    await expect(page.locator('.task.done')).toHaveCount(0);
    await page.getByRole('button', { name: 'Done 1' }).click();
    await expect(page.locator('.task.done', { hasText: 'Detect unused exports' })).toContainText('3f2a1c4');
    await edited.getByRole('button', { name: /More actions/ }).click();
    await edited.getByRole('button', { name: /Delete task/ }).click();
    await expect(edited).toHaveCount(0);
  });

  test('a long press on Add task sends the task when its worker is free', async () => {
    await page.goto('/#inbox');
    await passkeyOn(page);
    const press = async () => {
      await page.getByRole('button', { name: 'Add task' }).hover();
      await page.mouse.down();
      await page.waitForTimeout(700);
      await page.mouse.up();
    };

    await page.getByRole('button', { name: /^Project:/ }).click();
    await page.getByRole('dialog').getByRole('radio', { name: /hamta/ }).click();
    await page.getByLabel('New task').fill('Wait for hamta');
    await press();
    const waiting = page.locator('.task', { hasText: 'Wait for hamta' });
    await expect(waiting).toContainText('Waiting · needs input');
    expect((await list(page, 'commands')).filter((c) => c.fields.type.stringValue === 'dispatch')).toHaveLength(0);
    await page.request.delete(`${FIRESTORE}/${(await list(page, 'tasks')).find((t) => t.fields.text.stringValue === 'Wait for hamta')!.name.split('/documents/')[1]}`, { headers: admin });

    await page.getByRole('button', { name: /^Project:/ }).click();
    await page.getByRole('dialog').getByRole('radio', { name: /knip/ }).click();
    await page.getByLabel('New task').fill('Ship it now');
    await press();
    await expect(page.getByLabel('New task')).toHaveValue('');
    const task = page.locator('.task', { hasText: 'Ship it now' });
    await expect(task.getByRole('status')).toHaveText('Send to knip: waiting for the laptop…');
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
    await expect(starting).toContainText('Starting the worker…');
    await page.locator('.task.sent', { hasText: 'Add CSV export' }).getByRole('link', { name: 'hamta · needs input' }).click();
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
      await task.getByRole('button', { name: /Edit task/ }).click();
      return page.locator('form.composer.edit');
    };
    let editor = await edit();
    await expect(editor.locator('.thumb img')).toHaveCount(1);
    await editor.getByRole('button', { name: 'Remove image 1' }).click();
    await editor.getByRole('button', { name: 'Save' }).click();
    await expect(editor).toHaveCount(0);
    await expect(task.locator('.thumb')).toHaveCount(0);
    expect((await page.request.get(`${FIRESTORE}/attachments/${id}`, { headers: admin })).status()).toBe(404);
    editor = await edit();
    await editor.getByLabel('Attach images').setInputFiles({ name: 'shot.png', mimeType: 'image/png', buffer: PNG });
    await expect(editor.locator('.thumb img')).toHaveCount(1);
    await editor.getByRole('button', { name: 'Save' }).click();
    await expect(editor).toHaveCount(0);
    await expect(task.locator('.thumb img')).toHaveCount(1);

    await task.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(task.getByRole('status')).toContainText('waiting for the laptop…');
    const cmd = (await list(page, 'commands')).find((c) => JSON.parse(c.fields.payload.stringValue).text === 'Match this layout')!;
    expect(JSON.parse(cmd.fields.payload.stringValue).images).toEqual([id]);

    await task.getByRole('button', { name: /More actions/ }).click();
    await task.getByRole('button', { name: /Delete task/ }).click();
    await expect(task).toHaveCount(0);
    const gone = await page.request.get(`${FIRESTORE}/attachments/${id}`, { headers: admin });
    expect(gone.status()).toBe(404);
  });

  test('a reply carries a screenshot to its worker', async () => {
    await put(page, 'laptop/status', { online: true, seen_at: new Date() });
    await page.goto('/#s-k');
    await passkeyOn(page);
    const reply = page.locator('form.composer.respond');
    await reply.getByLabel('Attach images').setInputFiles({ name: 'shot.png', mimeType: 'image/png', buffer: PNG });
    await expect(reply.locator('.thumb img')).toHaveCount(1);
    await reply.getByRole('button', { name: 'Send reply' }).click();
    await expect(page.getByText('Reply to knip: waiting for the laptop…')).toBeVisible();
    await expect(reply.locator('.thumb')).toHaveCount(0);
    const id = createHash('sha256').update(PNG).digest('hex');
    expect((await get(page, `attachments/${id}`)).type.stringValue).toBe('image/png');
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'reply')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'reply', session: 'k', text: '', images: [id] });

    // The laptop has saved the image once it answers, so it is not kept.
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(page.getByText('Reply to knip: done.')).toBeVisible();
    await expect.poll(async () => (await page.request.get(`${FIRESTORE}/attachments/${id}`, { headers: admin })).status()).toBe(404);
    await page.request.delete(`${FIRESTORE}/${cmd.name.split('/documents/')[1]}`, { headers: admin });
  });

  test('the inbox filters by state and project, and keeps the order you set', async () => {
    await put(page, 'tasks/one', { project: 'knip', text: 'First thing', status: 'queued', created_at: new Date(Date.now() - 3000) });
    await put(page, 'tasks/two', { project: 'knip', text: 'Second thing', status: 'queued', created_at: new Date(Date.now() - 2000) });
    await put(page, 'tasks/wait', { project: 'hamta', text: 'Waits on hamta', status: 'queued', created_at: new Date(Date.now() - 1000) });
    await page.goto('/#inbox');
    const texts = page.locator('.tasks .ttext');
    await page.getByRole('button', { name: /^Ready/ }).click();
    await expect(texts).toHaveText(['First thing', 'Second thing']);
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
    await page.locator('.task', { hasText: 'Second thing' }).getByRole('button', { name: /More actions/ }).click();
    await page.getByRole('button', { name: 'Move to top' }).click();
    await expect(texts).toHaveText(['Second thing', 'First thing', 'Waits on hamta']);
    expect((await get(page, 'tasks/two')).rank).toBeDefined();

    for (const id of ['one', 'two', 'wait']) await page.request.delete(`${FIRESTORE}/tasks/${id}`, { headers: admin });
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
    await expect(page.locator('.card', { hasText: 'knip' }).locator('.flag.proto')).toHaveText('prototype');
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
    await expect(page.locator('.card', { hasText: 'knip' }).locator('.meta .ci')).toHaveText('ci failed');
    await page.locator('.card', { hasText: 'knip' }).click();
    const link = page.getByRole('link', { name: 'Test and deploy failed' });
    await expect(link).toHaveAttribute('href', 'https://github.com/o/knip/actions/runs/7');
    await expect(link).toHaveAttribute('target', '_blank');
  });

  test('a worker is cleared on its own and shows when it is cleared and clean', async () => {
    const git = { branch: 'main', upstream: 'origin/main', unpushed: 0, dirty: 0 };
    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: false, git });
    await page.goto('/#s-k');
    await passkeyOn(page);
    await expect(page.locator('.detail h2 .chip.idle')).toHaveText('idle');
    const flags = page.locator('.detail h2 .flag');
    await expect(flags).toHaveText(['clean']);
    await expect(page.locator('.reply.sent.none')).toHaveText('Nothing sent yet.');
    await page.getByRole('button', { name: 'Clear', exact: true }).click();
    await expect(page.getByText('Clear knip: waiting for the laptop…')).toBeVisible();
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'clear')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'clear', session: 'k' });

    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: true, git,
      prompt: { text: '/clear', at: new Date() } });
    await expect(flags).toHaveText(['cleared', 'clean']);
    await expect(page.locator('.reply.sent:not(.none)')).toHaveText('/clear');
    await expect(page.locator('.reply.pending')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Clear', exact: true })).toHaveCount(0);
    await page.goto('/');
    await expect(page.locator('.card', { hasText: 'knip' }).locator('.flag')).toHaveText(['cleared', 'clean']);
    await expect(page.locator('.card', { hasText: 'hamta' }).locator('.flag')).toHaveCount(0);
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
    await expect(page.getByText('Answer quiz: waiting for the laptop…')).toBeVisible();
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'answer')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({
      type: 'answer', session: 'q', question: 'toolu_1',
      text: 'My answers to your questions:\n- Which colour? Blue\n- Which pets? Cat; a fish',
    });
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(page.getByText('Answer quiz: done.')).toBeVisible();

    const { question, ...answered } = asking;
    await put(page, 'sessions/q', { ...answered, aoe_status: 'running', state: 'working' });
    await expect(page.locator('.qcard')).toHaveCount(0);
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
