import { defineConfig, devices } from '@playwright/test'

const reuseServers = process.env.E2E_REUSE_SERVERS === '1'
const frontendURL = new URL(process.env.DOMUS_E2E_BASE_URL || 'http://127.0.0.1:8089')
const apiBaseURL = new URL(process.env.DOMUS_E2E_API_BASE || 'http://127.0.0.1:8088')
const configPath = process.env.DOMUS_E2E_CONFIG || 'config.yaml'
const runtimeRoot = process.env.DOMUS_E2E_RUNTIME_ROOT || 'tmp/dev'
const workspaceImage = process.env.DOMUS_E2E_WORKSPACE_IMAGE || 'domus-workspace:0.1.0'
const browserExecutable = process.env.DOMUS_E2E_BROWSER_EXECUTABLE?.trim()
const headed = process.env.DOMUS_E2E_HEADED === '1'
const enableZeroCopy = process.env.DOMUS_E2E_ENABLE_ZERO_COPY === '1'

if (!frontendURL.port) throw new Error('DOMUS_E2E_BASE_URL must include an explicit port')
if (!apiBaseURL.port) throw new Error('DOMUS_E2E_API_BASE must include an explicit port')

export default defineConfig({
  testDir: './e2e',
  timeout: 180_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: frontendURL.origin,
    headless: !headed,
    actionTimeout: 20_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    launchOptions: browserExecutable || enableZeroCopy
      ? {
          ...(browserExecutable ? { executablePath: browserExecutable } : {}),
          ...(enableZeroCopy ? { args: ['--enable-zero-copy'] } : {}),
        }
      : undefined,
  },
  webServer: [
    {
      command: 'bash frontend/e2e/backend-supervisor.sh',
      cwd: '..',
      url: `${apiBaseURL.origin}/auth`,
      reuseExistingServer: reuseServers,
      timeout: 180_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 120_000 },
      env: {
        DOMUS_E2E_CONFIG: configPath,
        DOMUS_E2E_RUNTIME_ROOT: runtimeRoot,
        DOMUS_E2E_WORKSPACE_IMAGE: workspaceImage,
        DOMUS_E2E_API_BASE: apiBaseURL.origin,
      },
    },
    {
      command: `npm run dev -- --host ${JSON.stringify(frontendURL.hostname)} --port ${JSON.stringify(frontendURL.port)} --strictPort`,
      url: frontendURL.origin,
      reuseExistingServer: reuseServers,
      timeout: 120_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
      env: { VITE_API_BASE: apiBaseURL.origin },
    },
  ],
  projects: [
    {
      name: 'chromium-landscape',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 } },
    },
  ],
})
