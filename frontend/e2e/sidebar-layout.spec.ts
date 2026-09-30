import { expect, test } from '@playwright/test'

test('place options keep inset spacing inside selected and hover backgrounds', async ({ page }, testInfo) => {
  await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
    const request = JSON.parse(String(message))
    if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
  }))
  await page.route('**/user', route => route.fulfill({ json: { username: 'layout-fixture', role: 'user' } }))
  await page.route('**/file/?*', route => route.fulfill({ json: { files: [] } }))
  await page.route('**/trash/**', route => route.fulfill({ json: { files: [] } }))
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
  await page.goto('/files')

  const places = page.locator('.place-list')
  const items = places.locator('.n-menu-item-content')
  await expect(items).toHaveCount(2)
  for (const width of [1280, 900, 768]) {
    await page.setViewportSize({ width, height: 800 })
    await expect(places).toBeVisible()
    for (const item of await items.all()) {
      const inset = await item.evaluate(element => {
        const background = getComputedStyle(element, '::before')
        const bounds = element.getBoundingClientRect()
        const icon = element.querySelector('.n-menu-item-content__icon')!.getBoundingClientRect()
        const header = element.querySelector('.n-menu-item-content-header')!
        return {
          left: icon.left - bounds.left - parseFloat(background.left),
          overflowing: header.scrollWidth > header.clientWidth,
        }
      })
      expect(inset.left).toBe(16)
      expect(inset.overflowing).toBeFalsy()
    }
    if (width === 1280) {
      await page.locator('.file-sidebar').screenshot({ path: testInfo.outputPath('places.png') })
    }
  }

  await places.getByRole('menuitem', { name: /回收站|Trash/, exact: true }).click()
  await expect(page).toHaveURL(/place=trash/)
  await expect(items.filter({ hasText: /回收站|Trash/ })).toHaveClass(/selected/)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('.file-sidebar')).toBeHidden()
  await expect(page.locator('.mobile-brand')).toBeVisible()
  await expect(page.getByRole('button', { name: /清空回收站|Empty Trash/, exact: true })).toBeVisible()
})
