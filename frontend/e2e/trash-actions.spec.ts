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

const restoreID = 'ab17f399-a934-4f09-bcb4-4a68a9e9e052'

async function prepareRestoreConflict(page: Page, isDir: boolean, partial = false, nested = false) {
  await page.addInitScript(() => localStorage.setItem('domus_locale', 'zh'))
  await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
    const request = JSON.parse(String(message))
    if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
  }))
  await page.route('**/user', route => route.fulfill({ json: { username: 'trash-conflict-fixture', role: 'user' } }))
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
  const name = nested ? 'Project' : isDir ? 'Photos' : 'photo.png'
  const target = {
    ...entry, inode: 42, trash_id: restoreID, name, is_dir: nested || isDir, size: nested ? 4096 : 7_549_747,
    relative_path: partial ? `/${name}${isDir ? '/' : ''}` : '/',
    original_path: `${partial ? '/Archive' : ''}/${name}${nested || isDir ? '/' : ''}`,
  }
  const root = partial ? { ...target, inode: 41, name: 'Archive', relative_path: '/', original_path: '/Archive/', is_dir: true } : target
  const nestedName = isDir ? 'Reports' : 'empty.txt'
  const requests: Array<{ path: string; expected_inode: number; conflict?: string; nested_conflict?: string }> = []
  let restored = false
  await page.route('**/trash/**', async route => {
    if (route.request().method() === 'GET') {
      const childView = new URL(route.request().url()).pathname.endsWith('/list')
      return route.fulfill({ json: { files: restored ? [] : [childView ? target : root], trash: { id: restoreID, name: root.name } } })
    }
    expect(route.request().method()).toBe('POST')
    const body = route.request().postDataJSON()
    requests.push(body)
    if (body.nested_conflict || (body.conflict && body.conflict !== 'merge')) {
      restored = true
      return route.fulfill({ json: { ok: true, path: target.original_path } })
    }
    return route.fulfill({ status: 409, json: {
      error: 'restore_conflict', target_path: target.original_path, merge_available: target.is_dir,
      // Keep a legacy file-root response here: a frontend refresh must fix the
      // displayed name even before the existing backend is restarted.
      incoming: { name: partial || target.is_dir ? target.name : restoreID, size: target.size, is_dir: target.is_dir },
      existing: { name: target.name, size: target.size, is_dir: target.is_dir },
      conflicts: body.conflict === 'merge' ? [{
        name: nestedName, relative_path: `/${nestedName}${isDir ? '/' : ''}`, size: 0, is_dir: isDir,
        existing_name: nestedName, existing_size: 321, existing_is_dir: false,
      }] : [],
    } })
  })
  await page.goto('/files?place=trash')
  await expect(page.locator('.file-item')).toHaveCount(1)
  if (partial) {
    await page.evaluate(async id => {
      const url = '/src/stores/fileSystem.ts'
      await (await import(url)).useFileSystemStore().navigate(`trash:///${id}/`)
    }, restoreID)
    await expect(page.locator('.file-item .file-name')).toHaveText(target.name)
  }
  await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    const fs = (await import(url)).useFileSystemStore()
    fs.selectedFiles = [fs.files[0].path]
    ;(window as any).__restoreConflict = fs.restoreSelected()
  })
  return { name, nestedName, target, requests }
}

for (const width of [1280, 390]) {
  for (const isDir of [false, true]) {
    for (const partial of [false, true]) {
      test(`restore conflict shows the original ${isDir ? 'directory' : 'file'} name (${partial ? 'child' : 'root'}, ${width}px)`, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 })
        const fixture = await prepareRestoreConflict(page, isDir, partial)
        const dialog = page.locator('.domus-duplicate-dialog')
        await expect(dialog).toBeVisible()
        await expect(dialog.locator('.duplicate-dialog__summary')).toHaveText(`目标位置已经包含名为“${fixture.name}”的项目。请选择要执行的操作。`)
        await expect(dialog.locator('.duplicate-dialog__name')).toHaveText([fixture.name, fixture.name])
        await expect(dialog).not.toContainText(restoreID)
        await expect(dialog.locator('.duplicate-dialog__meta')).toHaveText(isDir ? ['文件夹', '文件夹'] : ['7.2 MB', '7.2 MB'])
        await expect(dialog.getByRole('button', { name: '合并文件夹', exact: true })).toHaveCount(isDir ? 1 : 0)
        if (!isDir && !partial) await page.mouse.click(8, 8)
        else await dialog.getByRole('button', { name: '取消', exact: true }).click()
        await page.evaluate(() => (window as any).__restoreConflict)
        expect(fixture.requests).toEqual([{ path: fixture.target.relative_path, expected_inode: fixture.target.inode }])
        await expect(page.locator('.file-item')).toHaveCount(1)
      })
    }

    test(`merged restore conflict keeps child ${isDir ? 'directory' : 'zero-byte file'} metadata (${width}px)`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      const fixture = await prepareRestoreConflict(page, isDir, false, true)
      const dialog = page.locator('.domus-duplicate-dialog')
      await expect(dialog.locator('.duplicate-dialog__name')).toHaveText(['Project', 'Project'])
      await dialog.getByRole('button', { name: '合并文件夹', exact: true }).click()
      await expect(dialog.locator('.duplicate-dialog__name')).toHaveText([fixture.nestedName, fixture.nestedName])
      await expect(dialog.locator('.duplicate-dialog__summary')).toContainText(`“${fixture.nestedName}”`)
      await expect(dialog.locator('.duplicate-dialog__meta')).toHaveText(['321.0 B', isDir ? '文件夹' : '0 B'])
      await expect(dialog.getByRole('button', { name: '合并文件夹', exact: true })).toHaveCount(0)
      await dialog.getByRole('button', { name: '重命名', exact: true }).click()
      await page.evaluate(() => (window as any).__restoreConflict)
      expect(fixture.requests).toEqual([
        { path: '/', expected_inode: fixture.target.inode },
        { path: '/', expected_inode: fixture.target.inode, conflict: 'merge' },
        { path: '/', expected_inode: fixture.target.inode, conflict: 'merge', nested_conflict: 'rename' },
      ])
      await expect(page.locator('.file-item')).toHaveCount(0)
    })
  }
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
