import { expect, test, type Page } from '@playwright/test'
import {
  e2eCredentials,
  login,
  openHomeDirectory,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

async function openTerminal(page: Page): Promise<void> {
  const filesWindow = page.locator('.plasma-window').filter({
    has: page.locator('.plasma-titlebar-title', { hasText: /文件|Files/ }),
  })
  await expect(filesWindow).toBeVisible()
  await filesWindow.locator('.plasma-btn-minimize').click()
  await expect(filesWindow).toBeHidden()

  const terminalIcon = page.locator('.desktop-icon').filter({ hasText: /终端|Terminal/ })
  await expect(terminalIcon).toBeVisible()
  await terminalIcon.dblclick()
  const terminalWindow = page.locator('.plasma-window').filter({
    has: page.locator('.plasma-titlebar-title', { hasText: /终端|Terminal/ }),
  })
  await expect(terminalWindow.locator('.xterm')).toBeVisible({ timeout: 120_000 })
}

async function runTerminalCommand(page: Page, command: string, completionMarker: string): Promise<void> {
  const terminal = page.locator('.plasma-window').filter({
    has: page.locator('.plasma-titlebar-title', { hasText: /终端|Terminal/ }),
  })
  const input = terminal.locator('.xterm-helper-textarea')
  await input.focus()
  await page.keyboard.type(command, { delay: 1 })
  await page.keyboard.press('Enter')
  await expect(terminal.locator('.xterm-rows')).toContainText(completionMarker, { timeout: 30_000 })
}

test('浏览器上传与 Workspace 终端通过同一 DOFS 文件空间双向可见', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const browserFile = `e2e-browser-${suffix}.txt`
  const terminalFile = `e2e-terminal-${suffix}.txt`
  const browserMarker = `BROWSER_FILE_${suffix}`
  const terminalMarker = `TERMINAL_FILE_${suffix}`
  const homePath = `/home/${credentials.username}/`
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)

    await uploadFromToolbar(page, {
      name: browserFile,
      mimeType: 'text/plain',
      buffer: Buffer.from(`${browserMarker}\n`),
    })
    await expect(page.locator('.file-item').filter({ hasText: browserFile })).toBeVisible()

    await openTerminal(page)
    const securityDone = `SECURITY_DONE_${suffix}`
    await runTerminalCommand(
      page,
      `printf 'UID='; id -u; if test -w /etc; then echo ETC_WRITABLE; else echo ETC_READONLY; fi; echo ${securityDone}`,
      securityDone,
    )
    const terminalRows = page.locator('.plasma-window').filter({
      has: page.locator('.plasma-titlebar-title', { hasText: /终端|Terminal/ }),
    }).locator('.xterm-rows')
    await expect(terminalRows).toContainText('UID=1000')
    await expect(terminalRows).toContainText('ETC_READONLY')

    const browserSeen = `BROWSER_SEEN_${suffix}`
    await runTerminalCommand(
      page,
      `grep -F '${browserMarker}' '${browserFile}' >/dev/null && echo ${browserSeen}`,
      browserSeen,
    )

    const terminalWritten = `TERMINAL_WRITTEN_${suffix}`
    await runTerminalCommand(
      page,
      `printf '${terminalMarker}\\n' > '${terminalFile}' && echo ${terminalWritten}`,
      terminalWritten,
    )

    const filesTask = page.locator('.taskbar').getByRole('button', { name: /文件|Files/ })
    await filesTask.click()
    const terminalItem = page.locator('.file-item').filter({ hasText: terminalFile })
    await expect(terminalItem).toBeVisible({ timeout: 30_000 })
    await waitForServiceWorker(page)
    await terminalItem.dblclick()
    const viewer = page.locator('.plasma-window').filter({
      has: page.locator('.plasma-titlebar-title', { hasText: terminalFile }),
    })
    await expect(viewer.locator('.cm-content')).toContainText(terminalMarker)
  } finally {
    if (authenticated) {
      await permanentlyDelete(page, `${homePath}${browserFile}`)
      await permanentlyDelete(page, `${homePath}${terminalFile}`)
    }
  }
})
