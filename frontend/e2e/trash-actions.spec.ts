import { expect, test, type Page } from '@playwright/test'

const entry = {
  name: 'fixture.txt', inode: 1, trash_id: 'fixture-trash', path: '/fixture.txt',
  original_path: '/fixture.txt', relative_path: '/', is_dir: false, size: 12,
  created_at: '2026-01-01T00:00:00Z', last_modified: '2026-01-01T00:00:00Z',
  deleted_at: '2026-01-02T00:00:00Z', status: 'deleted',
}

async function refreshTrash(page: Page) {
  await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    await (await import(url)).useFileSystemStore().refresh()
  })
}

async function tryEmptyTrash(page: Page) {
  await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    await (await import(url)).useFileSystemStore().emptyTrash()
  })
}

for (const width of [1280, 390]) {
  test(`Empty Trash requires a loaded nonempty Trash (${width}px)`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await page.addInitScript(() => localStorage.setItem('domus_show_hidden', '0'))
    await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
      const request = JSON.parse(String(message))
      if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
    }))
    await page.route('**/user', route => route.fulfill({ json: { username: 'trash-fixture', role: 'user' } }))
    await page.route('**/task/', route => route.fulfill({ json: [] }))
    await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
    await page.route('**/audit/', route => route.fulfill({ json: {} }))

    let entries: Array<typeof entry> = []
    let deletes = 0
    let reads = 0
    let failed = false
    let release!: () => void
    let pending: Promise<void> | null = new Promise(resolve => { release = resolve })
    await page.route('**/trash/**', async route => {
      if (route.request().method() === 'DELETE') {
        deletes++
        entries = []
        return route.fulfill({ json: {} })
      }
      reads++
      if (pending) await pending
      if (failed) return route.fulfill({ status: 400, json: { error: 'fixture list unavailable' } })
      return route.fulfill({ json: { files: entries } })
    })

    await page.goto('/files?place=trash')
    const button = page.getByRole('button', { name: /清空回收站|Empty Trash/, exact: true })
    await expect(button).toBeVisible()
    await expect(button).toBeDisabled()
    await tryEmptyTrash(page)
    await expect(page.locator('.domus-confirm-dialog')).toHaveCount(0)
    pending = null
    release()
    await expect(page.getByText(/回收站是空的|Trash is empty/, { exact: true })).toBeVisible()
    await expect(button).toBeDisabled()
    await tryEmptyTrash(page)
    await expect(page.locator('.domus-confirm-dialog')).toHaveCount(0)
    expect(deletes).toBe(0)

    entries = [entry]
    await refreshTrash(page)
    await expect(button).toBeEnabled()
    await button.click()
    const dialog = page.locator('.domus-confirm-dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /取消|Cancel/, exact: true }).click()
    expect(deletes).toBe(0)

    // Another client may empty the Trash while confirmation is still open.
    await button.click()
    await expect(dialog).toBeVisible()
    entries = []
    await refreshTrash(page)
    await expect(button).toBeDisabled()
    await dialog.getByRole('button', { name: /确定|OK/, exact: true }).click()
    await expect(dialog).toHaveCount(0)
    expect(deletes).toBe(0)
    entries = [entry]
    await refreshTrash(page)

    // Reloading must disable even if the previous listing still has records.
    pending = new Promise(resolve => { release = resolve })
    const previousReads = reads
    await page.evaluate(() => {
      const url = '/src/stores/fileSystem.ts'
      void import(url).then(module => module.useFileSystemStore().refresh())
    })
    await expect.poll(() => reads).toBeGreaterThan(previousReads)
    await expect(button).toBeDisabled()
    await tryEmptyTrash(page)
    await expect(dialog).toHaveCount(0)
    pending = null
    release()
    await expect(button).toBeEnabled()

    failed = true
    await refreshTrash(page)
    await expect(button).toBeDisabled()
    await tryEmptyTrash(page)
    await expect(dialog).toHaveCount(0)
    failed = false

    // Hidden-file filtering does not make a nonempty Trash actually empty.
    entries = [{ ...entry, name: '.hidden.txt' }]
    await refreshTrash(page)
    await expect(page.locator('.file-item')).toHaveCount(0)
    await expect(button).toBeEnabled()
    await button.click()
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /确定|OK/, exact: true }).click()
    await expect.poll(() => deletes).toBe(1)
    await expect(button).toBeDisabled()
    await expect(page.getByText(/回收站是空的|Trash is empty/, { exact: true })).toBeVisible()
  })
}
