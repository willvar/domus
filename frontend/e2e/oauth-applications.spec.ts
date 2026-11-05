import { expect, test, type Page } from '@playwright/test'

const account = { id: 'oauth-fixture', username: 'alice', display_name: 'Alice', role: 'user' }
const query = new URLSearchParams({
  client_id: 'consumer-client', redirect_uri: 'https://consumer.example/callback', response_type: 'code',
  scope: 'files:read', state: 'application-owned-random-state', code_challenge: 'challenge-fixture', code_challenge_method: 'S256',
}).toString()

async function mockSession(page: Page, loggedIn: boolean, root = false, locale = 'en') {
  await page.addInitScript(locale => localStorage.setItem('domus_locale', locale), locale)
  const user = { ...account, role: root ? 'root' : 'user' }
  await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
    const request = JSON.parse(String(message))
    if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
  }))
  await page.route('**/user', route => route.fulfill(loggedIn ? { json: user } : { status: 401, json: { error: 'unauthorized' } }))
  await page.route('**/user/avatar/*', route => route.fulfill({ status: 404, json: {} }))
  await page.route('**/auth', route => route.fulfill({ json: route.request().method() === 'POST' ? { user } : { smtp_enabled: false } }))
  await page.route('**/auth/verify', route => route.fulfill({ json: { token: 'login-fixture', methods: [] } }))
}

async function mockWorkspace(page: Page) {
  await page.route('**/file/?*', route => route.fulfill({ json: { files: [{
    name: 'fixture.txt', path: '/fixture.txt', inode: 1, is_dir: false, size: 12,
    created_at: '2026-01-01T00:00:00Z', last_modified: '2026-01-01T00:00:00Z', status: 'ready',
  }] } }))
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
}

async function openApplications(page: Page, width: number, label = 'Connected applications') {
  await page.locator(width < 600 ? '.mobile-account' : '.sidebar-account').click()
  await page.locator('.account-actions').getByText(label, { exact: true }).click()
  await expect(page.locator('.applications-modal')).toBeVisible()
}

for (const decision of ['allow', 'deny']) {
  test(`consent keeps its route through login and submits explicit ${decision}`, async ({ page }) => {
    await mockSession(page, false)
    let receivedQuery = ''
    let receivedDecision = ''
    await page.route('**/oauth/consent', async route => {
      receivedQuery = route.request().postDataJSON().query
      await route.fulfill({ json: { challenge: 'single-use-challenge', client_name: 'Consumer application', client_id: 'consumer-client', redirect_uri: 'https://consumer.example/callback', expires_at: new Date(Date.now() + 600_000).toISOString() } })
    })
    await page.route('**/oauth/decision', async route => {
      const form = new URLSearchParams(route.request().postData() || '')
      expect(form.get('challenge')).toBe('single-use-challenge')
      receivedDecision = form.get('decision') || ''
      await route.fulfill({ contentType: 'text/html', body: '<p>Application callback reached</p>' })
    })
    await page.goto(`/authorize?${query}`)
    await expect(page.locator('.login-card')).toBeVisible()
    await page.locator('.login-card input').first().fill('alice')
    await page.locator('.login-card input[type="password"]').fill('fixture-password')
    await page.locator('.login-card button[type="submit"]').first().click()
    await expect(page.locator('.authorization-card')).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/authorize\\?${query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`))
    await expect.poll(() => receivedQuery).toBe(query)
    await expect(page.locator('.authorization-card')).toContainText('including files added in the future')
    await expect(page.locator('.authorization-card')).toContainText('No access to private application data, trash or version history')
    const button = page.getByRole('button', { name: decision === 'allow' ? 'Allow read access' : 'Deny', exact: true })
    await expect(button).toHaveAttribute('type', 'submit')
    await button.click()
    await expect.poll(() => receivedDecision).toBe(decision)
    await expect(page.locator('body')).toContainText('Application callback reached')
  })
}

for (const width of [1280, 390]) {
  test(`authorized applications can be revoked and registered securely (${width}px)`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await mockSession(page, true, true)
    await mockWorkspace(page)
    let grants = [{ id: 'grant-fixture', client_name: 'Consumer application', created_at: new Date().toISOString() }]
    let registration: any
    await page.route('**/oauth/grants', route => route.fulfill({ json: grants }))
    await page.route('**/oauth/grants/grant-fixture', async route => { grants = []; await route.fulfill({ json: { ok: true } }) })
    await page.route('**/oauth/clients', async route => {
      if (route.request().method() !== 'POST') { await route.fulfill({ json: [] }); return }
      registration = route.request().postDataJSON()
      await route.fulfill({ status: 201, json: { ...registration, client_id: 'new-client', client_secret: 'one-time-client-secret' } })
    })
    await page.goto('/files')
    const originalURL = page.url()
    await openApplications(page, width)
    await expect(page).toHaveURL(originalURL)
    await expect(page.getByText('Consumer application', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Revoke my access', exact: true }).click()
    await page.locator('.domus-confirm-dialog').getByRole('button', { name: 'OK', exact: true }).click()
    await expect(page.getByText('No applications have access', { exact: true })).toBeVisible()
    await page.getByLabel('Application name', { exact: true }).fill('Consumer application')
    await page.getByLabel('Exact callback URLs', { exact: false }).fill('https://consumer.example/callback')
    await page.getByRole('button', { name: 'Add application', exact: true }).click()
    await expect.poll(() => registration?.name).toBe('Consumer application')
    await expect(page.locator('.created-client')).toContainText('one-time-client-secret')
    await expect(page.locator('.created-client')).toContainText('shown only once')
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))).not.toContain('one-time-client-secret')
    await page.getByRole('button', { name: 'Saved; hide credentials', exact: true }).click()
    await expect(page.locator('.created-client')).toHaveCount(0)
    const modal = page.locator('.applications-modal')
    expect(await modal.evaluate(element => element.scrollWidth <= element.clientWidth)).toBeTruthy()
    await page.getByLabel('Application name', { exact: true }).fill('Another application')
    await page.getByLabel('Exact callback URLs', { exact: false }).fill('https://consumer.example/callback')
    await page.getByRole('button', { name: 'Add application', exact: true }).click()
    await expect(page.locator('.created-client')).toContainText('one-time-client-secret')
    await page.mouse.click(8, 8)
    await expect(modal).toHaveCount(0)
    await openApplications(page, width)
    await expect(page.locator('.created-client')).toHaveCount(0)
    await expect(page.getByLabel('Application name', { exact: true })).toHaveValue('')
    await expect(page.locator('body')).not.toContainText('one-time-client-secret')
    await page.keyboard.press('Escape')
    await expect(modal).toHaveCount(0)
    await expect(page).toHaveURL(originalURL)
  })

  for (const root of [false, true]) {
    test(`personal authorizations and administrator scope stay distinct (${root ? 'root' : 'user'}, ${width}px)`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 844 })
      await mockSession(page, true, root, 'zh')
      await mockWorkspace(page)
      let grants = [{ id: 'my-grant', client_name: '示例工具', created_at: new Date().toISOString() }]
      let clients = [{ client_id: 'server-client', name: '示例工具', redirect_uris: ['https://consumer.example/callback'], public: false }]
      let revoked = 0, disabled = 0, clientReads = 0
      await page.route('**/oauth/grants', route => route.fulfill({ json: grants }))
      await page.route('**/oauth/grants/my-grant', route => {
        expect(route.request().method()).toBe('DELETE')
        revoked++; grants = []
        return route.fulfill({ json: { ok: true } })
      })
      await page.route('**/oauth/clients', route => { clientReads++; return route.fulfill({ json: clients }) })
      await page.route('**/oauth/clients/server-client', route => {
        expect(route.request().method()).toBe('DELETE')
        disabled++; clients = []
        return route.fulfill({ json: { ok: true } })
      })
      await page.goto('/files')
      await openApplications(page, width, '已连接应用')
      const modal = page.locator('.applications-modal')
      const personal = modal.getByRole('region', { name: '我的应用授权', exact: true })
      const admin = modal.getByRole('region', { name: '应用接入管理 · 管理员', exact: true })
      await expect(personal).toContainText('当前账号 @alice')
      await expect(personal).toContainText('不代表其他用户的授权')
      await expect(personal.getByText('示例工具', { exact: true })).toBeVisible()
      await expect(admin).toHaveCount(root ? 1 : 0)
      if (root) {
        await expect(admin).toContainText('全站设置')
        await expect(admin).toContainText('每位用户仍须自行同意授权')
        await expect(admin.getByRole('button', { name: '添加接入应用', exact: true })).toBeVisible()
        expect(clientReads).toBe(1)
      } else {
        expect(clientReads).toBe(0)
        await expect(modal.getByRole('button', { name: '添加接入应用', exact: true })).toHaveCount(0)
      }
      expect(await modal.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
      const body = modal.locator('.applications-modal-body')
      await expect(body).toHaveCSS('overflow-y', 'auto')
      if (root) {
        await body.hover()
        await page.mouse.wheel(0, 1600)
        await expect.poll(() => body.evaluate(element => element.scrollTop)).toBeGreaterThan(0)
        await expect(admin.getByRole('button', { name: '添加接入应用', exact: true })).toBeInViewport()
        await page.mouse.wheel(0, -1600)
        await expect.poll(() => body.evaluate(element => element.scrollTop)).toBe(0)
      }
      await modal.screenshot({ path: testInfo.outputPath('application-scopes.png') })
      await personal.getByRole('button', { name: '撤销我的授权', exact: true }).click()
      const confirm = page.locator('.domus-confirm-dialog')
      await expect(confirm).toContainText('其他用户不受影响')
      await confirm.getByRole('button', { name: /确定|OK/, exact: true }).click()
      await expect.poll(() => revoked).toBe(1)
      expect(disabled).toBe(0)
      if (root) {
        await admin.getByRole('button', { name: '全站停用', exact: true }).click()
        await expect(confirm).toContainText('所有用户')
        await confirm.getByRole('button', { name: /取消|Cancel/, exact: true }).click()
        expect(disabled).toBe(0)
        await admin.getByRole('button', { name: '全站停用', exact: true }).click()
        await confirm.getByRole('button', { name: /确定|OK/, exact: true }).click()
        await expect.poll(() => disabled).toBe(1)
      } else {
        expect(clientReads).toBe(0)
      }
    })
  }

  test(`connected applications opens as a modal without workspace navigation (${width}px)`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await mockSession(page, true)
    await mockWorkspace(page)
    let grantReads = 0
    let clientReads = 0
    await page.route('**/oauth/grants', route => { grantReads++; return route.fulfill({ json: [] }) })
    await page.route('**/oauth/clients', route => { clientReads++; return route.fulfill({ json: [] }) })
    await page.goto('/files')
    await expect(page.locator('.file-item')).toHaveCount(1)
    await page.evaluate(async () => {
      const url = '/src/stores/fileSystem.ts'
      ;(await import(url)).useFileSystemStore().enterSelectMode('/fixture.txt')
    })
    await expect(page.locator('.file-item.selected')).toHaveCount(1)
    const originalURL = page.url()
    await page.locator('.file-surface').evaluate(surface => { (window as any).__applicationsSurface = new WeakRef(surface) })
    expect(grantReads).toBe(0)
    await openApplications(page, width)
    const modal = page.locator('.applications-modal')
    await expect(modal).toHaveAttribute('role', 'dialog')
    await expect(modal).toHaveAttribute('aria-modal', 'true')
    await expect(modal.getByText('No applications have access', { exact: true })).toBeVisible()
    await expect(modal.getByText('Add application', { exact: true })).toHaveCount(0)
    expect(clientReads).toBe(0)
    await expect(page).toHaveURL(originalURL)
    await page.keyboard.press('Control+a')
    await page.keyboard.press('Delete')
    await expect(page.locator('.domus-confirm-dialog')).toHaveCount(0)
    expect(await page.evaluate(async () => {
      const url = '/src/stores/fileSystem.ts'
      return (await import(url)).useFileSystemStore().selectedFiles
    })).toEqual(['/fixture.txt'])
    await page.keyboard.press('Escape')
    await expect(modal).toHaveCount(0)
    expect(await page.locator('.file-surface').evaluate(surface => surface === (window as any).__applicationsSurface.deref())).toBe(true)
    expect(await page.evaluate(async () => {
      const url = '/src/stores/fileSystem.ts'
      return (await import(url)).useFileSystemStore().selectedFiles
    })).toEqual(['/fixture.txt'])
    await page.locator(width < 600 ? '.mobile-account' : '.sidebar-account').click()
    const entry = page.locator('.account-actions').getByRole('button', { name: 'Connected applications', exact: true })
    await expect(entry).not.toHaveAttribute('href')
    await expect(entry.locator('.connected-applications-icon')).toHaveCount(1)
    await entry.click()
    await expect(modal).toBeVisible()
    await expect.poll(() => grantReads).toBe(2)
    await modal.locator('.n-card-header__close').click()
    await expect(modal).toHaveCount(0)
  })
}

test('legacy applications address returns to the workspace instead of a standalone management page', async ({ page }) => {
  await mockSession(page, true)
  await mockWorkspace(page)
  await page.goto('/applications')
  await expect(page).toHaveURL(/\/files$/)
  await expect(page.locator('.file-item')).toHaveCount(1)
  await expect(page.locator('.applications-page')).toHaveCount(0)
})
