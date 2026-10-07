import { defineConfig } from '@playwright/test';

// The tests drive the real server binary (built UI embedded) on a throwaway
// data directory. PW_CHROMIUM points at a system browser when Playwright's
// own is not installed.
export default defineConfig({
  testDir: 'e2e',
  workers: 1,
  timeout: 30_000,
  reporter: [['list']],
  use: {
    baseURL: 'http://127.0.0.1:3199',
    launchOptions: process.env.PW_CHROMIUM ? { executablePath: process.env.PW_CHROMIUM, args: ['--no-sandbox'] } : {},
  },
  webServer: {
    command: 'rm -rf /tmp/portal-e2e && PORTAL_CONFIG=e2e/portal.yaml PORTAL_DATA=/tmp/portal-e2e PORTAL_ADDR=127.0.0.1:3199 go run ../cmd/portal',
    url: 'http://127.0.0.1:3199/healthz',
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
