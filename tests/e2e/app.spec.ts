import { expect, test } from '@playwright/test';

// Both tests write ideas to the one shared server, and the first expects none,
// so they run in order.
test.describe('ideas', () => {
  test.describe.configure({ mode: 'serial' });

  test('creates an idea through the real browser flow', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByText('No ideas yet.')).toBeVisible();
    await page.getByLabel('New idea').fill('A promising experiment');
    await page.getByRole('button', { name: 'Add' }).click();
    await expect(page.getByText('A promising experiment')).toBeVisible();
  });

  // The dashboard reads the same event lines production sends to BigQuery, so an
  // idea created in the browser has to appear in its count.
  test('the dashboard counts an idea created in the browser', async ({ page }) => {
    await page.goto('/admin?key=test-admin-key&range=24h');
    const before = Number(await page.locator('[data-figure="ideas"]').textContent());

    await page.goto('/');
    await page.getByLabel('New idea').fill('Counted');
    await page.getByRole('button', { name: 'Add' }).click();

    await page.goto('/admin?key=test-admin-key&range=24h');
    await expect(page.locator('[data-figure="ideas"]')).toHaveText(String(before + 1));
  });

  test('the dashboard refuses a wrong key', async ({ page }) => {
    const response = await page.goto('/admin?key=wrong');
    expect(response?.status()).toBe(401);
  });
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
