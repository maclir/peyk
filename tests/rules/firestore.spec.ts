import { afterAll, beforeAll, beforeEach, describe, it } from 'vitest';
import { assertFails, assertSucceeds, initializeTestEnvironment, RulesTestEnvironment } from '@firebase/rules-unit-testing';
import { addDoc, Bytes, collection, deleteDoc, deleteField, doc, getDoc, getDocs, serverTimestamp, setDoc, Timestamp, updateDoc, writeBatch } from 'firebase/firestore';
import { readFileSync } from 'node:fs';

let env: RulesTestEnvironment;

beforeAll(async () => {
  env = await initializeTestEnvironment({
    projectId: 'peyk-test',
    firestore: { rules: readFileSync('firestore.rules', 'utf8'), host: '127.0.0.1', port: 8181 },
  });
});

beforeEach(async () => {
  await env.clearFirestore();
  await env.withSecurityRulesDisabled(async (ctx) => {
    const db = ctx.firestore();
    await setDoc(doc(db, 'sessions/knip'), { title: 'knip', state: 'idle' });
    await setDoc(doc(db, 'laptop/status'), { online: true });
    await setDoc(doc(db, 'laptop/push'), { vapid_public_key: 'k' });
    await setDoc(doc(db, 'laptop/usage'), { five_hour: { used: 20 } });
    await setDoc(doc(db, 'laptop/version'), { commit: 'abc1234', behind: 2 });
    await setDoc(doc(db, 'laptop/secret'), { x: 1 });
    await setDoc(doc(db, 'other/thing'), { secret: true });
    await setDoc(doc(db, 'projects/knip'), { name: 'knip', free: true });
    await setDoc(doc(db, 'tasks/queued'), { project: 'knip', text: 'x', status: 'queued' });
    await setDoc(doc(db, 'tasks/sent'), { project: 'knip', text: 'y', status: 'dispatched' });
    await setDoc(doc(db, 'tasks/finished'), { project: 'knip', text: 'z', status: 'done' });
    await setDoc(doc(db, 'projects/hamta'), { name: 'hamta', free: false });
  });
});

afterAll(async () => env.cleanup());

const owner = () => env.authenticatedContext('owner-uid', { owner: true, email: 'me@example.com' }).firestore();
const stranger = () => env.authenticatedContext('someone', { email: 'someone@example.com' }).firestore();
const anonymous = () => env.unauthenticatedContext().firestore();

describe('owner', () => {
  it('reads sessions, laptop status and Claude usage', async () => {
    await assertSucceeds(getDocs(collection(owner(), 'sessions')));
    await assertSucceeds(getDoc(doc(owner(), 'sessions/knip')));
    await assertSucceeds(getDoc(doc(owner(), 'laptop/status')));
    await assertSucceeds(getDoc(doc(owner(), 'laptop/usage')));
    await assertSucceeds(getDoc(doc(owner(), 'laptop/version')));
  });

  it('cannot write anything, because only the laptop and server write', async () => {
    await assertFails(setDoc(doc(owner(), 'sessions/knip'), { title: 'forged' }));
    await assertFails(deleteDoc(doc(owner(), 'sessions/knip')));
    await assertFails(setDoc(doc(owner(), 'laptop/status'), { online: false }));
    await assertFails(setDoc(doc(owner(), 'laptop/usage'), { five_hour: { used: 0 } }));
    await assertFails(setDoc(doc(owner(), 'laptop/version'), { commit: 'x', behind: 0 }));
  });

  it('cannot list the laptop collection or read other collections', async () => {
    await assertFails(getDocs(collection(owner(), 'laptop')));
    await assertFails(getDoc(doc(owner(), 'other/thing')));
  });
});

describe('anyone else', () => {
  it('a signed-in account without the owner claim reads nothing', async () => {
    await assertFails(getDocs(collection(stranger(), 'sessions')));
    await assertFails(getDoc(doc(stranger(), 'laptop/status')));
    await assertFails(getDoc(doc(stranger(), 'laptop/usage')));
    await assertFails(getDoc(doc(stranger(), 'laptop/version')));
  });

  it('a claim of false is not the owner', async () => {
    const db = env.authenticatedContext('x', { owner: false }).firestore();
    await assertFails(getDoc(doc(db, 'sessions/knip')));
  });

  it('an anonymous browser reads and writes nothing', async () => {
    await assertFails(getDoc(doc(anonymous(), 'sessions/knip')));
    await assertFails(setDoc(doc(anonymous(), 'sessions/x'), { title: 'x' }));
  });
});

describe('tasks', () => {
  const task = (over: Record<string, unknown> = {}) => ({
    project: 'knip', text: 'Add --json output', created_at: serverTimestamp(), status: 'queued', ...over,
  });

  it('the owner adds a queued task for a published project and reads the inbox', async () => {
    await assertSucceeds(addDoc(collection(owner(), 'tasks'), task()));
    await assertSucceeds(getDocs(collection(owner(), 'tasks')));
    await assertSucceeds(getDocs(collection(owner(), 'projects')));
  });

  it('rejects tasks that are not plain queued tasks', async () => {
    const tasks = collection(owner(), 'tasks');
    await assertFails(addDoc(tasks, task({ project: 'unknown' })));
    await assertFails(addDoc(tasks, task({ project: 'a/b' })));
    await assertFails(addDoc(tasks, task({ text: '' })));
    await assertFails(addDoc(tasks, task({ text: 'x'.repeat(20001) })));
    await assertFails(addDoc(tasks, task({ status: 'dispatched' })));
    await assertFails(addDoc(tasks, task({ created_at: Timestamp.fromDate(new Date(2020, 0, 1)) })));
    await assertFails(addDoc(tasks, task({ session_id: 'k' })));
    await assertFails(addDoc(tasks, task({ rank: 'first' })));
  });

  it('a task carries the phone\'s order', async () => {
    await assertSucceeds(addDoc(collection(owner(), 'tasks'), task({ rank: 1763100000000 })));
  });

  it('the owner deletes queued and done tasks, not ones being worked on', async () => {
    await assertSucceeds(deleteDoc(doc(owner(), 'tasks/queued')));
    await assertSucceeds(deleteDoc(doc(owner(), 'tasks/finished')));
    await assertFails(deleteDoc(doc(owner(), 'tasks/sent')));
  });

  it('the owner edits a queued task\'s text, project and place only', async () => {
    await assertSucceeds(updateDoc(doc(owner(), 'tasks/queued'), { text: 'edited' }));
    await assertSucceeds(updateDoc(doc(owner(), 'tasks/queued'), { rank: -1000.5 }));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { rank: 'top' }));
    await assertFails(updateDoc(doc(owner(), 'tasks/sent'), { rank: 1 }));
    await assertSucceeds(updateDoc(doc(owner(), 'tasks/queued'), { project: 'hamta' }));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { project: 'unknown' }));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { text: '' }));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { status: 'dispatched' }));
    await assertFails(updateDoc(doc(owner(), 'tasks/sent'), { text: 'too late' }));
    await assertFails(updateDoc(doc(owner(), 'tasks/finished'), { status: 'queued' }));
    await assertFails(updateDoc(doc(stranger(), 'tasks/queued'), { text: 'x' }));
  });

  it('a hidden project takes no new tasks, but its queued ones can still be edited or moved away', async () => {
    await env.withSecurityRulesDisabled(async (ctx) => {
      const db = ctx.firestore();
      await setDoc(doc(db, 'projects/areh'), { name: 'areh', free: true, hidden: true });
      await setDoc(doc(db, 'tasks/stuck'), { project: 'areh', text: 'x', status: 'queued' });
    });
    await assertFails(addDoc(collection(owner(), 'tasks'), task({ project: 'areh' })));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { project: 'areh' }));
    await assertSucceeds(updateDoc(doc(owner(), 'tasks/stuck'), { text: 'edited', rank: 5 }));
    await assertSucceeds(updateDoc(doc(owner(), 'tasks/stuck'), { project: 'knip' }));
    await assertSucceeds(addDoc(collection(owner(), 'tasks'), task({ project: 'hamta' })));
  });

  it('nobody else reads or adds tasks', async () => {
    await assertFails(getDocs(collection(stranger(), 'tasks')));
    await assertFails(addDoc(collection(stranger(), 'tasks'), task()));
    await assertFails(getDocs(collection(stranger(), 'projects')));
  });

  it('the owner cannot write projects', async () => {
    await assertFails(setDoc(doc(owner(), 'projects/new'), { name: 'new' }));
  });
});

describe('attachments', () => {
  const id = 'ab'.repeat(32);
  const image = (over: Record<string, unknown> = {}) => ({
    data: Bytes.fromUint8Array(new Uint8Array([137, 80, 78, 71])), type: 'image/png', created_at: serverTimestamp(), ...over,
  });
  const task = (images: unknown) => ({ project: 'knip', text: 'See the screenshot', created_at: serverTimestamp(), status: 'queued', images });

  it('the owner adds a task with its images in one batch, reads and deletes them', async () => {
    const db = owner();
    const batch = writeBatch(db);
    batch.set(doc(db, `attachments/${id}`), image());
    batch.set(doc(collection(db, 'tasks')), task([id]));
    await assertSucceeds(batch.commit());
    await assertSucceeds(getDoc(doc(db, `attachments/${id}`)));
    await assertFails(getDocs(collection(db, 'attachments')));
    await assertSucceeds(deleteDoc(doc(db, `attachments/${id}`)));
  });

  it('a task names only images that exist, at most four', async () => {
    const tasks = collection(owner(), 'tasks');
    await assertFails(addDoc(tasks, task([id])));
    await assertFails(addDoc(tasks, task([])));
    await assertFails(addDoc(tasks, task(id)));
    await env.withSecurityRulesDisabled(async (ctx) => {
      for (const c of 'abcde') await setDoc(doc(ctx.firestore(), `attachments/${c.repeat(64)}`), { type: 'image/png' });
    });
    await assertSucceeds(addDoc(tasks, task(['a', 'b', 'c', 'd'].map((c) => c.repeat(64)))));
    await assertFails(addDoc(tasks, task(['a', 'b', 'c', 'd', 'e'].map((c) => c.repeat(64)))));
    await assertSucceeds(updateDoc(doc(owner(), 'tasks/queued'), { images: ['a'.repeat(64)] }));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { images: ['f'.repeat(64)] }));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { images: [] }));
    await assertFails(updateDoc(doc(owner(), 'tasks/queued'), { images: ['a', 'b', 'c', 'd', 'e'].map((c) => c.repeat(64)) }));
    await assertSucceeds(updateDoc(doc(owner(), 'tasks/queued'), { images: deleteField() }));
    await assertFails(updateDoc(doc(owner(), 'tasks/sent'), { images: ['a'.repeat(64)] }));
  });

  it('images are write-once, named by a hash, and bounded', async () => {
    await assertSucceeds(setDoc(doc(owner(), `attachments/${id}`), image()));
    await assertFails(setDoc(doc(owner(), `attachments/${id}`), image()));
    await assertFails(setDoc(doc(owner(), 'attachments/not-a-hash'), image()));
    await assertFails(setDoc(doc(owner(), `attachments/${'cd'.repeat(32)}`), image({ type: 'text/html' })));
    await assertFails(setDoc(doc(owner(), `attachments/${'cd'.repeat(32)}`), image({ data: 'not bytes' })));
    await assertFails(setDoc(doc(owner(), `attachments/${'cd'.repeat(32)}`), image({ data: Bytes.fromUint8Array(new Uint8Array(1000001)) })));
    await assertFails(setDoc(doc(owner(), `attachments/${'cd'.repeat(32)}`), image({ extra: 1 })));
  });

  it('nobody else reads or adds images', async () => {
    await assertFails(setDoc(doc(stranger(), `attachments/${id}`), image()));
    await env.withSecurityRulesDisabled(async (ctx) => setDoc(doc(ctx.firestore(), `attachments/${id}`), { type: 'image/png' }));
    await assertFails(getDoc(doc(stranger(), `attachments/${id}`)));
    await assertFails(deleteDoc(doc(anonymous(), `attachments/${id}`)));
  });
});

describe('commands', () => {
  const cmd = (over: Record<string, unknown> = {}) => ({
    type: 'reply', payload: '{"id":"c1"}', status: 'pending', created_at: serverTimestamp(),
    assertion: { credential_id: 'a', authenticator_data: 'b', client_data_json: 'c', signature: 'd' },
    ...over,
  });

  it('the owner creates pending commands and reads them', async () => {
    await assertSucceeds(setDoc(doc(owner(), 'commands/c1'), cmd()));
    const { assertion, ...push } = cmd({ type: 'push' });
    await assertSucceeds(setDoc(doc(owner(), 'commands/c2'), push));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c3'), cmd({ type: 'clear' })));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c5'), cmd({ type: 'answer' })));
    const { assertion: unsigned, ...check } = cmd({ type: 'check' });
    await assertSucceeds(setDoc(doc(owner(), 'commands/c6'), check));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c7'), cmd({ type: 'update' })));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c8'), cmd({ type: 'start' })));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c9'), cmd({ type: 'restart' })));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c10'), cmd({ type: 'hide' })));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c11'), cmd({ type: 'show' })));
    const { assertion: a, ...bare } = cmd();
    await assertSucceeds(setDoc(doc(owner(), 'commands/c4'), { ...bare, grant: { statement: '{}', assertion: a, signature: 'e' } }));
    await assertSucceeds(getDoc(doc(owner(), 'commands/c1')));
  });

  it('commands are write-once', async () => {
    await assertSucceeds(setDoc(doc(owner(), 'commands/c1'), cmd()));
    await assertFails(setDoc(doc(owner(), 'commands/c1'), cmd()));
    await assertFails(updateDoc(doc(owner(), 'commands/c1'), { status: 'done' }));
    await assertFails(deleteDoc(doc(owner(), 'commands/c1')));
  });

  it('the owner cancels a reply only while it waits for the laptop', async () => {
    await assertSucceeds(setDoc(doc(owner(), 'commands/c1'), cmd()));
    await assertFails(updateDoc(doc(owner(), 'commands/c1'), { status: 'cancelled', payload: '{"id":"c1","text":"other"}' }));
    await assertFails(updateDoc(doc(owner(), 'commands/c1'), { status: 'held' }));
    await assertFails(updateDoc(doc(stranger(), 'commands/c1'), { status: 'cancelled' }));
    await assertSucceeds(updateDoc(doc(owner(), 'commands/c1'), { status: 'cancelled' }));
    await assertFails(updateDoc(doc(owner(), 'commands/c1'), { status: 'pending' }));
    await assertSucceeds(setDoc(doc(owner(), 'commands/c2'), cmd({ type: 'stop' })));
    await assertFails(updateDoc(doc(owner(), 'commands/c2'), { status: 'cancelled' }));
    await env.withSecurityRulesDisabled(async (ctx) => {
      await setDoc(doc(ctx.firestore(), 'commands/held'), { ...cmd(), status: 'held' });
      await setDoc(doc(ctx.firestore(), 'commands/sent'), { ...cmd(), status: 'done' });
    });
    await assertSucceeds(updateDoc(doc(owner(), 'commands/held'), { status: 'cancelled' }));
    await assertFails(updateDoc(doc(owner(), 'commands/sent'), { status: 'cancelled' }));
    await assertFails(deleteDoc(doc(owner(), 'commands/held')));
  });

  it('rejects malformed commands', async () => {
    await assertFails(setDoc(doc(owner(), 'commands/x1'), cmd({ type: 'shell' })));
    await assertFails(setDoc(doc(owner(), 'commands/x2'), cmd({ status: 'done' })));
    await assertFails(setDoc(doc(owner(), 'commands/x3'), cmd({ extra: 1 })));
    await assertFails(setDoc(doc(owner(), 'commands/x4'), cmd({ payload: '' })));
    await assertFails(setDoc(doc(owner(), 'commands/x5'), cmd({ assertion: { other: 1 } })));
    const grant = { statement: '{}', assertion: cmd().assertion, signature: 'e' };
    await assertFails(setDoc(doc(owner(), 'commands/x6'), cmd({ grant: { ...grant, extra: 1 } })));
    await assertFails(setDoc(doc(owner(), 'commands/x7'), cmd({ grant: { ...grant, assertion: { other: 1 } } })));
    await assertFails(setDoc(doc(owner(), 'commands/x8'), cmd({ grant: { ...grant, statement: 'x'.repeat(1025) } })));
    await assertFails(setDoc(doc(owner(), 'commands/x9'), cmd({ grant: 'x' })));
  });

  it('nobody else creates or reads commands', async () => {
    await assertFails(setDoc(doc(stranger(), 'commands/c1'), cmd()));
    await assertFails(setDoc(doc(anonymous(), 'commands/c1'), cmd()));
  });
});

describe('pairings', () => {
  const code = 'abcdefghijklmnopqrstuvwx';
  const pairing = (over: Record<string, unknown> = {}) => ({
    credential_id: 'cred', public_key: 'pk', alg: -7, created_at: serverTimestamp(), ...over,
  });

  it('the owner writes a pairing under a long code but cannot read it back', async () => {
    await assertSucceeds(setDoc(doc(owner(), `pairings/${code}`), pairing()));
    await assertFails(getDoc(doc(owner(), `pairings/${code}`)));
    await assertFails(setDoc(doc(owner(), `pairings/${code}`), pairing()));
  });

  it('rejects short codes, extra fields, and strangers', async () => {
    await assertFails(setDoc(doc(owner(), 'pairings/short'), pairing()));
    await assertFails(setDoc(doc(owner(), `pairings/${code}x`), pairing({ label: 'x' })));
    await assertFails(setDoc(doc(stranger(), `pairings/${code}y`), pairing()));
  });
});

describe('push', () => {
  const id = 'a'.repeat(40);
  const sub = (over: Record<string, unknown> = {}) => ({
    endpoint: 'https://fcm.googleapis.com/fcm/send/abc', p256dh: 'key', auth: 'auth', created_at: serverTimestamp(), ...over,
  });

  it('the owner reads the push key and subscribes, unsubscribes', async () => {
    await assertSucceeds(getDoc(doc(owner(), 'laptop/push')));
    await assertFails(getDoc(doc(owner(), 'laptop/secret')));
    await assertSucceeds(setDoc(doc(owner(), `subscriptions/${id}`), sub()));
    await assertSucceeds(getDoc(doc(owner(), `subscriptions/${id}`)));
    await assertSucceeds(deleteDoc(doc(owner(), `subscriptions/${id}`)));
  });

  it('rejects odd subscriptions and strangers', async () => {
    await assertFails(setDoc(doc(owner(), `subscriptions/${id}`), sub({ endpoint: 'http://evil' })));
    await assertFails(setDoc(doc(owner(), 'subscriptions/short'), sub()));
    await assertFails(setDoc(doc(owner(), `subscriptions/${id}`), sub({ extra: 1 })));
    await assertFails(getDocs(collection(owner(), 'subscriptions')));
    await assertFails(setDoc(doc(stranger(), `subscriptions/${id}`), sub()));
    await assertFails(getDoc(doc(stranger(), 'laptop/push')));
  });
});

describe('history', () => {
  const request = (db: ReturnType<typeof owner>, id = 'knip') =>
    setDoc(doc(db, `history/${id}`), { requested_at: serverTimestamp() }, { merge: true });

  it('the owner requests a live session\'s history, reads it, asks again and deletes it', async () => {
    await assertSucceeds(request(owner()));
    await env.withSecurityRulesDisabled((ctx) =>
      updateDoc(doc(ctx.firestore(), 'history/knip'), { entries: [{ kind: 'prompt', text: 'hi' }], served_at: Timestamp.now() }));
    await assertSucceeds(getDoc(doc(owner(), 'history/knip')));
    await assertSucceeds(request(owner()));
    await assertSucceeds(deleteDoc(doc(owner(), 'history/knip')));
  });

  it('cannot request a session the laptop has not published', async () => {
    await assertFails(request(owner(), 'nope'));
  });

  it('cannot write the history itself or a request time of its own', async () => {
    await assertFails(setDoc(doc(owner(), 'history/knip'), { requested_at: serverTimestamp(), entries: [] }));
    await assertFails(setDoc(doc(owner(), 'history/knip'), { requested_at: Timestamp.fromMillis(0) }));
    await assertSucceeds(request(owner()));
    await assertFails(updateDoc(doc(owner(), 'history/knip'), { entries: [{ kind: 'reply', text: 'forged' }] }));
    await assertFails(updateDoc(doc(owner(), 'history/knip'), { served_at: serverTimestamp() }));
  });

  it('nobody else reads or requests a history', async () => {
    await assertSucceeds(request(owner()));
    await assertFails(getDoc(doc(stranger(), 'history/knip')));
    await assertFails(request(stranger()));
    await assertFails(deleteDoc(doc(stranger(), 'history/knip')));
    await assertFails(getDocs(collection(owner(), 'history')));
  });
});

describe('seen', () => {
  const see = (db: ReturnType<typeof owner>, id = 'knip') => setDoc(doc(db, `seen/${id}`), { seen_at: serverTimestamp() });

  it('the owner says it showed a live session, again and again', async () => {
    await assertSucceeds(see(owner()));
    await assertSucceeds(see(owner()));
  });

  it('cannot name an unpublished session, add fields, choose the time, or read', async () => {
    await assertFails(see(owner(), 'nope'));
    await assertFails(setDoc(doc(owner(), 'seen/knip'), { seen_at: serverTimestamp(), extra: 1 }));
    await assertFails(setDoc(doc(owner(), 'seen/knip'), { seen_at: Timestamp.fromMillis(0) }));
    await assertSucceeds(see(owner()));
    await assertFails(getDoc(doc(owner(), 'seen/knip')));
    await assertFails(deleteDoc(doc(owner(), 'seen/knip')));
  });

  it('nobody else says it showed a session', async () => {
    await assertFails(see(stranger()));
    await assertFails(see(anonymous()));
  });
});
