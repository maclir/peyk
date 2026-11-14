import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { assertFails, initializeTestEnvironment, RulesTestEnvironment } from '@firebase/rules-unit-testing';
import { doc, getDoc, setDoc } from 'firebase/firestore';
import { readFileSync } from 'node:fs';

let env: RulesTestEnvironment;

beforeAll(async () => {
  env = await initializeTestEnvironment({
    projectId: 'peyk-test',
    firestore: { rules: readFileSync('firestore.rules', 'utf8'), host: '127.0.0.1', port: 8181 },
  });
});

afterAll(async () => env.cleanup());

describe('closed-by-default Firestore rules', () => {
  it('deny browser reads and writes', async () => {
    const db = env.unauthenticatedContext().firestore();
    await assertFails(getDoc(doc(db, 'ideas/one')));
    await assertFails(setDoc(doc(db, 'ideas/one'), { title: 'No direct writes' }));
    expect(true).toBe(true);
  });
});

