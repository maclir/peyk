import { defineConfig, devices } from '@playwright/test';

const testURL = `http://127.0.0.1:${process.env.TEST_PORT ?? '8091'}`;

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: testURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'make test-server',
    url: `${testURL}/health`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
