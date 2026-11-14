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
    await expect(page.getByText('laptop online')).toBeVisible();
  });

  test('the board groups sessions and shows what each is doing', async () => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Needs you' })).toBeVisible();
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
    await page.getByRole('button', { name: 'Push 2' }).click();
    await expect(page.getByText('Push knip: waiting for the laptop…')).toBeVisible();
    const [cmd] = await list(page, 'commands');
    expect(cmd.fields.type.stringValue).toBe('push');
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'push', session: 'k' });
    expect(cmd.fields.assertion).toBeUndefined();
    await put(page, `commands/${cmd.name.split('/').pop()}`, { ...plain(cmd.fields), status: 'done' });
    await expect(page.getByText('Push knip: done.')).toBeVisible();
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
  });

  test('a send that does not go out says so on its task', async () => {
    await put(page, 'laptop/status', { online: false, seen_at: new Date() });
    await put(page, 'tasks/proto', { project: 'knip', text: 'Open prototypes from a session', status: 'queued', created_at: new Date() });
    await page.goto('/#inbox');
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
    const editor = page.locator('form.task.editing');
    await editor.getByLabel('Task text').fill('Add --json and --csv output');
    await editor.getByLabel('Project').selectOption('hamta');
    await editor.getByRole('button', { name: 'Save' }).click();
    const edited = page.locator('.task', { hasText: 'Add --json and --csv output' });
    await expect(edited.locator('.proj')).toContainText('hamta');
    await expect(edited.getByText('needs input')).toBeVisible();

    await expect(page.locator('.task.done', { hasText: 'Detect unused exports' })).toContainText('3f2a1c4');
    await edited.getByRole('button', { name: /More actions/ }).click();
    await edited.getByRole('button', { name: /Delete task/ }).click();
    await expect(edited).toHaveCount(0);
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

  test('the inbox filters by state and project, and keeps the order you set', async () => {
    await put(page, 'tasks/one', { project: 'knip', text: 'First thing', status: 'queued', created_at: new Date(Date.now() - 3000) });
    await put(page, 'tasks/two', { project: 'knip', text: 'Second thing', status: 'queued', created_at: new Date(Date.now() - 2000) });
    await put(page, 'tasks/wait', { project: 'hamta', text: 'Waits on hamta', status: 'queued', created_at: new Date(Date.now() - 1000) });
    await page.goto('/#inbox');
    const texts = page.locator('.tasks .ttext');
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

  test('a worker is cleared on its own and shows when it is cleared and clean', async () => {
    const git = { branch: 'main', upstream: 'origin/main', unpushed: 0, dirty: 0 };
    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: false, git });
    await page.goto('/#s-k');
    await passkeyOn(page);
    const flags = page.locator('.detail h2 .flag');
    await expect(flags).toHaveText(['clean']);
    await page.getByRole('button', { name: 'Clear', exact: true }).click();
    await expect(page.getByText('Clear knip: waiting for the laptop…')).toBeVisible();
    const cmd = (await list(page, 'commands')).find((c) => c.fields.type.stringValue === 'clear')!;
    expect(JSON.parse(cmd.fields.payload.stringValue)).toMatchObject({ type: 'clear', session: 'k' });

    await put(page, 'sessions/k', { id: 'k', title: 'knip', path: '/c/knip', aoe_status: 'idle', state: 'idle', updated_at: new Date(), cleared: true, git });
    await expect(flags).toHaveText(['cleared', 'clean']);
    await expect(page.getByRole('button', { name: 'Clear', exact: true })).toHaveCount(0);
    await page.goto('/');
    await expect(page.locator('.card', { hasText: 'knip' }).locator('.flag')).toHaveText(['cleared', 'clean']);
    await expect(page.locator('.card', { hasText: 'hamta' }).locator('.flag')).toHaveCount(0);
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
