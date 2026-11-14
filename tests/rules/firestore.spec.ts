import { afterAll, beforeAll, beforeEach, describe, it } from 'vitest';
import { assertFails, assertSucceeds, initializeTestEnvironment, RulesTestEnvironment } from '@firebase/rules-unit-testing';
import { addDoc, collection, deleteDoc, doc, getDoc, getDocs, serverTimestamp, setDoc, Timestamp, updateDoc } from 'firebase/firestore';
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
    await setDoc(doc(db, 'other/thing'), { secret: true });
    await setDoc(doc(db, 'projects/knip'), { name: 'knip', free: true });
    await setDoc(doc(db, 'tasks/queued'), { project: 'knip', text: 'x', status: 'queued' });
    await setDoc(doc(db, 'tasks/sent'), { project: 'knip', text: 'y', status: 'dispatched' });
  });
});

afterAll(async () => env.cleanup());

const owner = () => env.authenticatedContext('owner-uid', { owner: true, email: 'me@example.com' }).firestore();
const stranger = () => env.authenticatedContext('someone', { email: 'someone@example.com' }).firestore();
const anonymous = () => env.unauthenticatedContext().firestore();

describe('owner', () => {
  it('reads sessions and laptop status', async () => {
    await assertSucceeds(getDocs(collection(owner(), 'sessions')));
    await assertSucceeds(getDoc(doc(owner(), 'sessions/knip')));
    await assertSucceeds(getDoc(doc(owner(), 'laptop/status')));
  });

  it('cannot write anything, because only the laptop and server write', async () => {
    await assertFails(setDoc(doc(owner(), 'sessions/knip'), { title: 'forged' }));
    await assertFails(deleteDoc(doc(owner(), 'sessions/knip')));
    await assertFails(setDoc(doc(owner(), 'laptop/status'), { online: false }));
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
  });

  it('the owner deletes only queued tasks and edits none', async () => {
    await assertSucceeds(deleteDoc(doc(owner(), 'tasks/queued')));
    await assertFails(deleteDoc(doc(owner(), 'tasks/sent')));
    await assertFails(updateDoc(doc(owner(), 'tasks/sent'), { status: 'queued' }));
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
