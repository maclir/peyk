import { expect, test } from '@playwright/test';

// The test server has no Firebase config, so the shell says so instead of
// offering a sign-in that cannot work. Signed-in behaviour is covered by the
// rules tests and the module unit tests.
test('the shell loads its modules and explains a missing Firebase config', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'peyk' })).toBeVisible();
  await expect(page.getByText('not connected to Firebase')).toBeVisible();
  expect(errors).toEqual([]);
});

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
