import { expect, test, type Page } from '@playwright/test'

interface FixtureUser {
  id: string
  username: string
  role: string
  email: string
  totp_enabled: boolean
  created_at: string
}

async function prepareAdmin(page: Page, { count = 20, root = true, corsFailed = false, locale = 'zh' } = {}) {
  await page.addInitScript(locale => localStorage.setItem('domus_locale', locale), locale)
  await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
    const request = JSON.parse(String(message))
    if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
  }))
  await page.route('**/user', route => route.fulfill({ json: { username: 'layout-admin', role: root ? 'root' : 'user' } }))
  let directoryReads = 0
  await page.route('**/file/?*', route => {
    directoryReads++
    return route.fulfill({ json: { files: [{ name: 'underlying.txt', path: '/underlying.txt', inode: 1, is_dir: false, size: 12, status: 'ready' }] } })
  })
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
  const users: FixtureUser[] = Array.from({ length: count }, (_, index) => ({
    id: `00000000-0000-4000-8000-${String(index + 1).padStart(12, '0')}`,
    username: index === 0 ? 'administrator' : `example-user-${index}`,
    role: index === 0 ? 'root' : 'user', email: `example-${index}@example.com`,
    totp_enabled: true, created_at: '2026-01-01T00:00:00Z',
  }))
  const mutations: Array<{ method: string; path: string; body?: Record<string, unknown> }> = []
  let userReads = 0
  await page.route('**/audit/user/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'GET') { userReads++; return route.fulfill({ json: users }) }
    const body = request.postData() ? request.postDataJSON() : undefined
    mutations.push({ method: request.method(), path: url.pathname, ...(body ? { body } : {}) })
    const parts = url.pathname.split('/').filter(Boolean)
    const user = users.find(user => user.id === parts[2])
    if (request.method() === 'POST') {
      users.push({ id: 'created-fixture', username: body.username, role: body.role, email: '', totp_enabled: false, created_at: '2026-01-01T00:00:00Z' })
    } else if (request.method() === 'PUT' && user) {
      if (body.role) user.role = body.role
    } else if (request.method() === 'DELETE' && user) {
      if (parts[3] === 'otp') user.totp_enabled = false
      else if (parts[3] === 'email') user.email = ''
      else users.splice(users.indexOf(user), 1)
    }
    await route.fulfill({ json: { ok: true } })
  })
  // The screen probes CORS on mount. Both control and probe requests are
  // fictional fixtures, never a live bucket or the current administrator.
  await page.route('**/admin/oss/cors-check', route => route.fulfill(corsFailed
    ? { status: 503, json: { error: 'fixture_cors_unavailable' } }
    : { json: { put_url: 'http://127.0.0.1:18489/__admin_fixture_cors/put', delete_url: 'http://127.0.0.1:18489/__admin_fixture_cors/delete' } }))
  await page.route('**/__admin_fixture_cors/**', route => route.fulfill({ status: 200, body: '' }))
  await page.goto('/files')
  await expect(page.locator('.file-item')).toHaveCount(1)
  return { users, mutations, userReads: () => userReads, directoryReads: () => directoryReads }
}

async function openAdmin(page: Page, mobile = false) {
  await page.locator(mobile ? '.mobile-account' : '.sidebar-account').click()
  await page.locator('.account-actions').getByRole('button', { name: /用户管理|Admin Panel/, exact: true }).click()
  await expect(page.locator('.admin-view')).toBeVisible()
}

function rowNamed(page: Page, name: string) {
  return page.locator('.admin-view .n-data-table-tbody tr').filter({ has: page.getByText(name, { exact: true }) })
}

async function openActions(page: Page, name: string) {
  await expect(page.locator('.user-actions-menu')).toHaveCount(0)
  await rowNamed(page, name).getByRole('button', { name: `管理 ${name}`, exact: true }).click()
  const menu = page.locator('.user-actions-menu')
  await expect(menu).toBeVisible()
  return menu
}

for (const [width, height, count] of [[1280, 800, 2], [1280, 800, 20], [1024, 768, 20], [900, 600, 40], [1600, 1000, 20]]) {
  test(`user management uses compact reachable rows (${width}x${height}, ${count} users)`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height })
    const fixture = await prepareAdmin(page, { count })
    const originalURL = page.url()
    await openAdmin(page)
    const modal = page.locator('.admin-modal')
    const rows = modal.locator('.n-data-table-tbody tr')
    await expect(rows).toHaveCount(count)
    if (count === 2) {
      await expect(rows.last()).toBeInViewport()
      expect((await modal.boundingBox())!.height).toBeLessThan(480)
    }
    await expect(page).toHaveURL(originalURL)
    await expect(modal).toHaveAttribute('aria-label', '用户管理')
    const geometry = await modal.evaluate(dialog => {
      const bounds = dialog.getBoundingClientRect()
      const rows = [...dialog.querySelectorAll('.n-data-table-tbody tr')]
      const first = rows[0].getBoundingClientRect()
      const actions = rows[0].querySelector('.action-buttons')!.getBoundingClientRect()
      return {
        firstRowHeight: first.height, firstRowOffset: first.top - bounds.top,
        visibleRows: rows.filter(row => { const r = row.getBoundingClientRect(); return r.top >= bounds.top && r.bottom <= bounds.bottom }).length,
        actionsInside: actions.left >= bounds.left && actions.right <= bounds.right,
        horizontalOverflow: dialog.scrollWidth > dialog.clientWidth,
      }
    })
    expect(geometry.firstRowHeight).toBeLessThanOrEqual(72)
    expect(geometry.firstRowOffset).toBeLessThan(220)
    expect(geometry.visibleRows).toBeGreaterThanOrEqual(Math.min(count, height < 700 ? 3 : 5))
    expect(geometry.actionsInside).toBe(true)
    expect(geometry.horizontalOverflow).toBe(false)
    await testInfo.attach('layout-geometry', { body: JSON.stringify(geometry), contentType: 'application/json' })
    expect(await rows.first().locator('.action-buttons button').count()).toBe(2)
    await modal.screenshot({ path: testInfo.outputPath('admin-modal.png') })
    const toolbarTop = (await modal.locator('.admin-toolbar').boundingBox())!.y
    if (count > 2) {
      await modal.locator('.users-table').hover()
      await page.mouse.wheel(0, 6000)
      await expect(rows.last()).toBeInViewport()
      expect(Math.abs((await modal.locator('.admin-toolbar').boundingBox())!.y - toolbarTop)).toBeLessThan(1)
      await expect(rows.last().getByRole('button', { name: `管理 example-user-${count - 1}`, exact: true })).toBeInViewport()
    }
    expect(fixture.mutations).toEqual([])
    await modal.getByRole('button', { name: '关闭用户管理', exact: true }).click()
    await expect(modal).toHaveCount(0)
    await expect(page).toHaveURL(originalURL)
  })
}

test('row menus preserve edit, reset, delete and cancellation request targets', async ({ page }) => {
  const fixture = await prepareAdmin(page, { count: 3 })
  const target = fixture.users[1]
  await openAdmin(page)
  await rowNamed(page, target.username).getByRole('button', { name: '编辑', exact: true }).click()
  const edit = page.locator('.admin-dialog')
  await expect(edit.locator('input').first()).toHaveValue(target.username)
  await expect(edit.locator('input').first()).toBeDisabled()
  await edit.locator('.n-base-selection').click()
  await page.getByText('Root', { exact: true }).click()
  await edit.getByRole('button', { name: '保存', exact: true }).click()
  await expect.poll(() => fixture.mutations.length).toBe(1)
  expect(fixture.mutations[0]).toEqual({ method: 'PUT', path: `/audit/user/${target.id}`, body: { role: 'root' } })
  await expect(edit).toHaveCount(0)

  let menu = await openActions(page, target.username)
  await menu.getByRole('menuitem', { name: '重置密码', exact: true }).click()
  const prompt = page.locator('.domus-prompt-dialog')
  await expect(prompt).toContainText(target.username)
  await prompt.getByRole('button', { name: /取消|Cancel/, exact: true }).click()
  expect(fixture.mutations.length).toBe(1)
  menu = await openActions(page, target.username)
  await menu.getByRole('menuitem', { name: '重置密码', exact: true }).click()
  await prompt.locator('input').fill('fixture-only-password')
  await prompt.getByRole('button', { name: /确定|OK/, exact: true }).click()
  await expect.poll(() => fixture.mutations.length).toBe(2)
  expect(fixture.mutations[1]).toEqual({ method: 'PUT', path: `/audit/user/${target.id}`, body: { password: 'fixture-only-password' } })

  for (const [label, suffix] of [['重置 OTP', '/otp'], ['清除邮箱', '/email'], ['删除', '']]) {
    menu = await openActions(page, target.username)
    await menu.getByRole('menuitem', { name: label, exact: true }).click()
    const confirm = page.locator('.domus-confirm-dialog')
    await expect(confirm).toContainText(target.username)
    const previous = fixture.mutations.length
    await confirm.getByRole('button', { name: /取消|Cancel/, exact: true }).click()
    expect(fixture.mutations.length).toBe(previous)
    menu = await openActions(page, target.username)
    await menu.getByRole('menuitem', { name: label, exact: true }).click()
    await confirm.getByRole('button', { name: /确定|OK/, exact: true }).click()
    await expect.poll(() => fixture.mutations.length).toBe(previous + 1)
    expect(fixture.mutations.at(-1)).toEqual({ method: 'DELETE', path: `/audit/user/${target.id}${suffix}` })
  }
  await expect(rowNamed(page, target.username)).toHaveCount(0)
})

test('new users keep their payload and unsupported reset actions stay hidden', async ({ page }) => {
  const fixture = await prepareAdmin(page, { count: 2 })
  fixture.users[1].email = ''
  fixture.users[1].totp_enabled = false
  await openAdmin(page)
  const menu = await openActions(page, fixture.users[1].username)
  await expect(menu.getByRole('menuitem', { name: '重置 OTP', exact: true })).toHaveCount(0)
  await expect(menu.getByRole('menuitem', { name: '清除邮箱', exact: true })).toHaveCount(0)
  await page.locator('.admin-toolbar h1').click()
  await expect(menu).toHaveCount(0)
  await page.locator('.admin-toolbar').getByRole('button', { name: '添加用户', exact: true }).click()
  const dialog = page.locator('.admin-dialog')
  await dialog.locator('input').nth(0).fill('fixture-new-user')
  await dialog.locator('input[type="password"]').fill('fixture-new-password')
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect.poll(() => fixture.mutations.length).toBe(1)
  expect(fixture.mutations[0]).toEqual({ method: 'POST', path: '/audit/user/', body: { username: 'fixture-new-user', password: 'fixture-new-password', role: 'user' } })
  await expect(rowNamed(page, 'fixture-new-user')).toHaveCount(1)
})

test('modal keyboard and nested dialogs cannot act on the underlying file selection', async ({ page }) => {
  const fixture = await prepareAdmin(page, { count: 2 })
  await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    ;(await import(url)).useFileSystemStore().enterSelectMode('/underlying.txt')
    ;(window as any).__adminSurface = new WeakRef(document.querySelector('.file-surface')!)
  })
  const reads = fixture.directoryReads()
  await openAdmin(page)
  await page.keyboard.press('Control+a')
  await page.keyboard.press('Delete')
  await expect(page.locator('.domus-confirm-dialog')).toHaveCount(0)
  const menu = await openActions(page, 'administrator')
  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)
  await expect(page.locator('.admin-modal')).toBeVisible()
  await page.locator('.admin-toolbar').getByRole('button', { name: '添加用户', exact: true }).click()
  await expect(page.locator('.admin-dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('.admin-dialog')).toHaveCount(0)
  await expect(page.locator('.admin-modal')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('.admin-modal')).toHaveCount(0)
  expect(await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    return (await import(url)).useFileSystemStore().selectedFiles
  })).toEqual(['/underlying.txt'])
  expect(await page.locator('.file-surface').evaluate(surface => surface === (window as any).__adminSurface.deref())).toBe(true)
  expect(fixture.directoryReads()).toBe(reads)
  expect(fixture.mutations).toEqual([])
})

for (const [count, corsFailed] of [[0, false], [20, true]] as const) {
  test(`empty and CORS-warning layouts stay reachable (${count} users, warning ${corsFailed})`, async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 650 })
    const fixture = await prepareAdmin(page, { count, corsFailed })
    await openAdmin(page)
    const modal = page.locator('.admin-modal')
    await expect.poll(fixture.userReads).toBeGreaterThan(0)
    await expect(modal.locator('.cors-warning')).toHaveCount(corsFailed ? 1 : 0)
    await expect(modal.getByRole('button', { name: '添加用户', exact: true })).toBeInViewport()
    if (count) {
      await modal.locator('.users-table').hover()
      await page.mouse.wheel(0, 6000)
      await expect(rowNamed(page, `example-user-${count - 1}`)).toBeInViewport()
    }
    await modal.getByRole('button', { name: '关闭用户管理', exact: true }).click()
    await expect(modal).toHaveCount(0)
    expect(fixture.mutations).toEqual([])
  })
}

test('mobile standalone management keeps its back navigation and visible actions', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const fixture = await prepareAdmin(page, { count: 20, locale: 'en' })
  await openAdmin(page, true)
  await expect(page).toHaveURL(/\/admin$/)
  const first = rowNamed(page, 'administrator')
  await expect(first.getByRole('button', { name: 'Actions for administrator', exact: true })).toBeInViewport()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('admin-mobile.png') })
  await page.locator('.admin-back').click()
  await expect(page).toHaveURL(/\/files$/)
  expect(fixture.mutations).toEqual([])
})

test('ordinary accounts still cannot open the administrator screen', async ({ page }) => {
  const fixture = await prepareAdmin(page, { root: false })
  await page.goto('/admin')
  await expect(page).toHaveURL(/\/files$/)
  await expect(page.locator('.admin-view')).toHaveCount(0)
  expect(fixture.userReads()).toBe(0)
  expect(fixture.mutations).toEqual([])
})

async function clickBackdrop(page: Page, touch = false, point = { x: 8, y: 8 }) {
  if (touch) await page.touchscreen.tap(point.x, point.y)
  else await page.mouse.click(point.x, point.y)
}

test('modal backdrop closes the management dialog without clicking through to files', async ({ page }) => {
  const fixture = await prepareAdmin(page, { count: 2 })
  await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    ;(await import(url)).useFileSystemStore().enterSelectMode('/underlying.txt')
    ;(window as any).__backdropClicks = 0
    document.querySelector('.file-shell')!.addEventListener('click', () => { (window as any).__backdropClicks++ })
  })
  await openAdmin(page)
  const modal = page.locator('.admin-modal')
  await modal.locator('h1').click()
  await expect(modal).toBeVisible()
  await page.evaluate(() => { (window as any).__backdropClicks = 0 })
  await clickBackdrop(page)
  await expect(modal).toHaveCount(0)
  expect(await page.evaluate(() => (window as any).__backdropClicks)).toBe(0)
  expect(await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    return (await import(url)).useFileSystemStore().selectedFiles
  })).toEqual(['/underlying.txt'])
  expect(fixture.mutations).toEqual([])
})

test('modal backdrop closes only the top create or edit dialog', async ({ page }) => {
  const fixture = await prepareAdmin(page, { count: 2 })
  await openAdmin(page)
  const modal = page.locator('.admin-modal')
  await modal.getByRole('button', { name: '添加用户', exact: true }).click()
  const child = page.locator('.admin-dialog')
  await child.locator('input').first().fill('unsaved-fixture')
  const parent = (await modal.boundingBox())!
  await clickBackdrop(page, false, { x: parent.x + 24, y: parent.y + 24 })
  await expect(child).toHaveCount(0)
  await expect(modal).toBeVisible()
  await rowNamed(page, 'example-user-1').getByRole('button', { name: '编辑', exact: true }).click()
  await expect(child).toBeVisible()
  await clickBackdrop(page)
  await expect(child).toHaveCount(0)
  await expect(modal).toBeVisible()
  expect(fixture.mutations).toEqual([])
  await clickBackdrop(page)
  await expect(modal).toHaveCount(0)
})

test('modal backdrop cancels management confirmation and password prompts without writes', async ({ page }) => {
  const fixture = await prepareAdmin(page, { count: 2 })
  await openAdmin(page)
  let menu = await openActions(page, 'example-user-1')
  await menu.getByRole('menuitem', { name: '删除', exact: true }).click()
  await expect(page.locator('.domus-confirm-dialog')).toBeVisible()
  await clickBackdrop(page)
  await expect(page.locator('.domus-confirm-dialog')).toHaveCount(0)
  await expect(page.locator('.admin-modal')).toBeVisible()
  menu = await openActions(page, 'example-user-1')
  await menu.getByRole('menuitem', { name: '重置密码', exact: true }).click()
  const prompt = page.locator('.domus-prompt-dialog')
  await prompt.locator('input').fill('unsubmitted-fixture-password')
  await clickBackdrop(page)
  await expect(prompt).toHaveCount(0)
  await expect(page.locator('.admin-modal')).toBeVisible()
  expect(fixture.mutations).toEqual([])
})

for (const mobile of [false, true]) {
  test.describe(`modal backdrop ${mobile ? 'touch' : 'mouse'}`, () => {
    test.use({ viewport: mobile ? { width: 390, height: 844 } : { width: 1280, height: 800 }, isMobile: mobile, hasTouch: mobile })

    for (const kind of ['prompt', 'confirm', 'duplicate'] as const) {
      test(`${kind} keeps internal clicks and treats outside clicks as cancellation`, async ({ page }) => {
        const fixture = await prepareAdmin(page, { count: 2 })
        await page.evaluate(async kind => {
          const url = '/src/composables/useNativeDialog.ts'
          const dialogs = await import(url)
          ;(window as any).__backdropResult = 'pending'
          const promise = kind === 'prompt' ? dialogs.showPrompt('Backdrop fixture', 'Fixture input')
            : kind === 'confirm' ? dialogs.showConfirm('Backdrop fixture', 'Fixture content')
            : dialogs.showDuplicateDialog({ title: 'Backdrop fixture', incomingName: 'fixture.txt', incomingSize: 12, existingName: 'fixture.txt', existingSize: 12, existingIsDir: false })
          promise.then((result: unknown) => { (window as any).__backdropResult = result })
        }, kind)
        const dialog = page.locator(kind === 'prompt' ? '.domus-prompt-dialog' : kind === 'confirm' ? '.domus-confirm-dialog' : '.domus-duplicate-dialog')
        await dialog.getByText('Backdrop fixture', { exact: true }).click()
        await expect(dialog).toBeVisible()
        expect(await page.evaluate(() => (window as any).__backdropResult)).toBe('pending')
        if (kind === 'prompt') {
          const input = dialog.locator('input')
          await input.fill('unsubmitted fixture value')
          if (!mobile) {
            const bounds = (await input.boundingBox())!
            await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
            await page.mouse.down()
            await page.mouse.move(8, 8, { steps: 5 })
            await page.mouse.up()
            await expect(dialog).toBeVisible()
            expect(await page.evaluate(() => (window as any).__backdropResult)).toBe('pending')
          }
        }
        await clickBackdrop(page, mobile)
        await expect(dialog).toHaveCount(0)
        await expect.poll(() => page.evaluate(() => (window as any).__backdropResult)).toBe(kind === 'confirm' ? false : null)
        expect(fixture.mutations).toEqual([])
      })
    }
  })
}

test('modal backdrop respects explicitly non-dismissible dialogs', async ({ page }) => {
  await prepareAdmin(page, { count: 2 })
  await page.evaluate(async () => {
    const url = '/src/ui/feedback.ts'
    ;(await import(url)).useAppDialog().create({ class: 'backdrop-locked-fixture', title: 'Protected fixture', content: 'Explicitly non-dismissible', maskClosable: false, positiveText: 'Close fixture' })
  })
  const dialog = page.locator('.backdrop-locked-fixture')
  await expect(dialog).toBeVisible()
  await clickBackdrop(page)
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Close fixture', exact: true }).click()
  await expect(dialog).toHaveCount(0)
})

test('modal backdrop retains real wheel scrolling for oversized dialogs', async ({ page }) => {
  await prepareAdmin(page, { count: 2 })
  await page.evaluate(async () => {
    const url = '/src/composables/useNativeDialog.ts'
    ;(window as any).__backdropResult = 'pending'
    ;(await import(url)).showConfirm('Long backdrop fixture', Array.from({ length: 100 }, (_, index) => `Long fixture entry ${index}`).join('\n'))
      .then((result: boolean) => { (window as any).__backdropResult = result })
  })
  const dialog = page.locator('.domus-confirm-dialog')
  await expect(dialog.getByText('Long backdrop fixture', { exact: true })).toBeInViewport()
  const bounds = (await dialog.boundingBox())!
  await page.mouse.move(bounds.x + bounds.width / 2, 300)
  await page.mouse.wheel(0, 8000)
  await expect(dialog.getByRole('button', { name: /确定|OK/, exact: true })).toBeInViewport()
  await clickBackdrop(page)
  await expect(dialog).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => (window as any).__backdropResult)).toBe(false)
})

async function observeModalLeave(page: Page, selector: string, contentSelector: string) {
  const modal = page.locator(selector)
  await expect(modal).not.toHaveClass(/transition-enter-/)
  // The mask can still be entering after the modal itself has settled.
  await expect.poll(() => modal.evaluate(element => {
    const mask = element.closest('.n-modal-container')!.querySelector('.n-modal-mask')!
    return [element, mask].every(layer =>
      !/transition-(enter|appear)-/.test(layer.className) &&
      getComputedStyle(layer).opacity === '1' &&
      layer.getAnimations().every(animation => animation.playState === 'finished'),
    )
  })).toBe(true)
  await modal.evaluate((element, contentSelector) => {
    const modal = element as HTMLElement
    const content = modal.querySelector(contentSelector)
    const mask = modal.closest('.n-modal-container')!.querySelector('.n-modal-mask')!
    const motion = {
      initialHeight: modal.offsetHeight, durationMs: 0, maskDurationMs: 0, removed: false,
      frames: [] as Array<{ opacity: number; maskOpacity: number; scale: number; height: number; sameContent: boolean }>,
    }
    ;(window as any).__modalLeaveMotion = motion
    const sample = () => {
      if (!modal.isConnected) { motion.removed = true; return }
      const style = getComputedStyle(modal)
      motion.frames.push({
        opacity: Number(style.opacity), maskOpacity: Number(getComputedStyle(mask).opacity), scale: new DOMMatrixReadOnly(style.transform).a,
        height: modal.offsetHeight, sameContent: !!content && modal.querySelector(contentSelector) === content,
      })
      requestAnimationFrame(sample)
    }
    const observer = new MutationObserver(() => {
      if (!modal.classList.contains('fade-in-scale-up-transition-leave-active')) return
      observer.disconnect()
      motion.durationMs = Math.max(...getComputedStyle(modal).transitionDuration.split(',').map(value => parseFloat(value) * 1000))
      motion.maskDurationMs = Math.max(...getComputedStyle(mask).transitionDuration.split(',').map(value => parseFloat(value) * 1000))
      sample()
    })
    observer.observe(modal, { attributes: true, attributeFilter: ['class'] })
  }, contentSelector)
}

async function expectModalLeave(page: Page, selector: string, reduced = false) {
  await expect(page.locator(selector)).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => (window as any).__modalLeaveMotion.removed)).toBe(true)
  const motion = await page.evaluate(() => (window as any).__modalLeaveMotion as {
    initialHeight: number; durationMs: number; maskDurationMs: number;
    frames: Array<{ opacity: number; maskOpacity: number; scale: number; height: number; sameContent: boolean }>;
  })
  expect(motion.frames.length).toBeGreaterThan(0)
  expect(motion.frames.every(frame => frame.sameContent)).toBe(true)
  expect(motion.frames.every(frame => Math.abs(frame.height - motion.initialHeight) <= 1)).toBe(true)
  if (reduced) {
    expect(motion.durationMs).toBeLessThanOrEqual(1)
    expect(motion.maskDurationMs).toBeLessThanOrEqual(1)
  } else {
    expect(motion.durationMs).toBeGreaterThanOrEqual(100)
    expect(motion.durationMs).toBeLessThanOrEqual(300)
    expect(motion.maskDurationMs).toBeGreaterThanOrEqual(100)
    expect(motion.maskDurationMs).toBeLessThanOrEqual(300)
    expect(motion.frames.some(frame => frame.opacity > 0.02 && frame.opacity < 0.98 && frame.scale < 0.999)).toBe(true)
    expect(motion.frames.some(frame => frame.maskOpacity > 0.02 && frame.maskOpacity < 0.98)).toBe(true)
  }
  return motion
}

async function prepareShellModal(page: Page, kind: 'admin' | 'applications') {
  const fixture = await prepareAdmin(page, { count: 2 })
  let applicationReads = 0
  await page.route('**/oauth/grants', route => { applicationReads++; return route.fulfill({ json: [] }) })
  await page.route('**/oauth/clients', route => route.fulfill({ json: [] }))
  await expect(page.locator('.admin-view, .applications-content')).toHaveCount(0)
  expect(fixture.userReads()).toBe(0)
  expect(applicationReads).toBe(0)
  const open = async () => {
    if (kind === 'admin') await openAdmin(page)
    else {
      await page.locator(page.viewportSize()!.width < 600 ? '.mobile-account' : '.sidebar-account').click()
      await page.locator('.account-actions').getByRole('button', { name: '已连接应用', exact: true }).click()
      await expect(page.locator('.applications-personal')).toBeVisible()
    }
  }
  await open()
  return {
    ...fixture, open, reads: kind === 'admin' ? fixture.userReads : () => applicationReads,
    selector: kind === 'admin' ? '.admin-modal' : '.applications-modal',
    content: kind === 'admin' ? '.admin-view' : '.applications-content',
  }
}

test.describe('modal close motion', () => {
  test.beforeEach(async ({ page }) => { await page.emulateMedia({ reducedMotion: 'no-preference' }) })

  for (const kind of ['admin', 'applications'] as const) {
    for (const trigger of ['backdrop', 'escape', 'button']) {
      test(`${kind} retains its content and size through ${trigger} dismissal`, async ({ page }, testInfo) => {
        const fixture = await prepareShellModal(page, kind)
        const reads = fixture.reads()
        await observeModalLeave(page, fixture.selector, fixture.content)
        if (trigger === 'backdrop') await clickBackdrop(page)
        else if (trigger === 'escape') await page.keyboard.press('Escape')
        else if (kind === 'admin') await page.getByRole('button', { name: '关闭用户管理', exact: true }).click()
        else await page.locator('.applications-modal .n-card-header__close').click()
        const motion = await expectModalLeave(page, fixture.selector)
        await testInfo.attach('modal-leave-frames', { body: JSON.stringify(motion), contentType: 'application/json' })
        await expect(page.locator(fixture.content)).toHaveCount(0)
        await fixture.open()
        await expect.poll(fixture.reads).toBeGreaterThan(reads)
        await expect(page.locator(fixture.content)).toHaveCount(1)
        expect(fixture.mutations).toEqual([])
      })
    }
  }

  test.describe('mobile', () => {
    test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

    test('applications retain their content through touch dismissal', async ({ page }, testInfo) => {
      const fixture = await prepareShellModal(page, 'applications')
      await observeModalLeave(page, fixture.selector, fixture.content)
      await clickBackdrop(page, true)
      const motion = await expectModalLeave(page, fixture.selector)
      await testInfo.attach('modal-leave-frames', { body: JSON.stringify(motion), contentType: 'application/json' })
      expect(fixture.mutations).toEqual([])
    })
  })

  test('nested management dialogs animate without closing their parent', async ({ page }, testInfo) => {
    const fixture = await prepareShellModal(page, 'admin')
    await page.getByRole('button', { name: '添加用户', exact: true }).click()
    await observeModalLeave(page, '.admin-dialog', '.n-form')
    await clickBackdrop(page)
    const motion = await expectModalLeave(page, '.admin-dialog')
    await testInfo.attach('modal-leave-frames', { body: JSON.stringify(motion), contentType: 'application/json' })
    await expect(page.locator('.admin-modal')).toBeVisible()
    expect(fixture.mutations).toEqual([])
  })

  for (const kind of ['prompt', 'confirm', 'duplicate'] as const) {
    test(`native ${kind} retains its content through cancellation`, async ({ page }, testInfo) => {
      const fixture = await prepareAdmin(page, { count: 2 })
      await page.evaluate(async kind => {
        const url = '/src/composables/useNativeDialog.ts'
        const dialogs = await import(url)
        ;(window as any).__leaveResult = 'pending'
        const promise = kind === 'prompt' ? dialogs.showPrompt('Leave fixture', 'Fixture input')
          : kind === 'confirm' ? dialogs.showConfirm('Leave fixture', 'Fixture content')
          : dialogs.showDuplicateDialog({ title: 'Leave fixture', incomingName: 'fixture.txt', existingName: 'fixture.txt' })
        promise.then((result: unknown) => { (window as any).__leaveResult = result })
      }, kind)
      const selector = `.domus-${kind}-dialog`
      await observeModalLeave(page, selector, kind === 'prompt' ? 'input' : kind === 'confirm' ? '.domus-dialog-content' : '.duplicate-dialog')
      await clickBackdrop(page)
      const motion = await expectModalLeave(page, selector)
      await testInfo.attach('modal-leave-frames', { body: JSON.stringify(motion), contentType: 'application/json' })
      await expect.poll(() => page.evaluate(() => (window as any).__leaveResult)).toBe(kind === 'confirm' ? false : null)
      expect(fixture.mutations).toEqual([])
    })
  }
})

test.describe('modal close reduced motion', () => {
  test.beforeEach(async ({ page }) => { await page.emulateMedia({ reducedMotion: 'reduce' }) })

  for (const kind of ['admin', 'applications'] as const) {
    test(`${kind} cleans up without a visible closing transition`, async ({ page }, testInfo) => {
      const fixture = await prepareShellModal(page, kind)
      await observeModalLeave(page, fixture.selector, fixture.content)
      await clickBackdrop(page)
      const motion = await expectModalLeave(page, fixture.selector, true)
      await testInfo.attach('modal-leave-frames', { body: JSON.stringify(motion), contentType: 'application/json' })
      expect(fixture.mutations).toEqual([])
    })
  }
})
