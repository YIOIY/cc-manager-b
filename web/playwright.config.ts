import { defineConfig, devices } from '@playwright/test'

const port = 18095
const dbPath = '/tmp/cc-manager-e2e.db'
export const ADMIN_PASSWORD = 'e2e-password-123456'

// The suite drives the real Go binary serving the built frontend, so run
// `make build` (or `make go-build` + `npm run build`) first. Each run starts
// from an empty database so tests can assume the bootstrap admin only.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  timeout: 30_000,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: 'retain-on-failure',
    locale: 'zh-CN',
    ...devices['Desktop Chrome'],
  },
  webServer: {
    command: [
      `rm -f ${dbPath} ${dbPath}-wal ${dbPath}-shm`,
      `cd .. && CC_MANAGER_LISTEN=127.0.0.1:${port} CC_MANAGER_DB=${dbPath} CC_MANAGER_ADMIN_PASSWORD=${ADMIN_PASSWORD}` +
        ' CC_MANAGER_ENCRYPTION_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= CC_MANAGER_WEB_DIR=web/dist CC_MANAGER_LOG_LEVEL=warn' +
        // The fixture proxies are fake; skip the live proxy / Claude reachability checks.
        ' CC_MANAGER_STUB_NETWORK_PROBES=1 ./bin/cc-manager',
    ].join(' && '),
    url: `http://127.0.0.1:${port}/healthz`,
    reuseExistingServer: false,
    timeout: 30_000,
  },
})
