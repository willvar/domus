import { expect, test, type Locator, type Page } from '@playwright/test'
import {
  e2eCredentials,
  login,
  openHomeDirectory,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

function itemNamed(page: Page, name: string): Locator {
  return page.locator('.file-item').filter({
    has: page.locator('.file-name').getByText(name, { exact: true }),
  })
}

async function chooseContextAction(page: Page, label: RegExp): Promise<void> {
  const menu = page.locator('.plasma-context-menu')
  await expect(menu).toBeVisible()
  await menu.locator('.ctx-item').filter({ hasText: label }).click()
}

async function navigateTo(page: Page, path: string): Promise<void> {
  await page.locator('.breadcrumb-bar').click()
  const input = page.locator('.path-input')
  await expect(input).toBeVisible()
  await input.fill(path)
  await input.press('Enter')
  await expect(input).toBeHidden()
  const leaf = path.replace(/\/$/, '').split('/').pop()
  if (leaf) await expect(page.locator('.breadcrumb-bar')).toContainText(leaf)
}

async function createFolderFromUI(page: Page, name: string): Promise<void> {
  await page.locator('.file-view').click({ button: 'right', position: { x: 8, y: 8 } })
  await chooseContextAction(page, /^(新建文件夹|New Folder)$/)
  const dialog = page.locator('.breeze-modal-dialog')
  await expect(dialog).toBeVisible()
  const input = dialog.locator('input')
  await input.fill(name)
  await input.press('Enter')
  await expect(dialog).toBeHidden()
  await expect(itemNamed(page, name)).toBeVisible()
}

test('文件可经 UI 完成新建、上传、重命名、移动、回收与还原', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const sourceName = `e2e-source-${suffix}`
  const destinationName = `e2e-destination-${suffix}`
  const originalName = `e2e-lifecycle-${suffix}.txt`
  const renamedName = `e2e-renamed-${suffix}.txt`
  const homePath = `/home/${credentials.username}/`
  const sourcePath = `${homePath}${sourceName}/`
  const destinationPath = `${homePath}${destinationName}/`
  const finalPath = `${destinationPath}${renamedName}`
  const trashPath = `/__trash__/home/${credentials.username}/${destinationName}/`
  const content = `Domus lifecycle E2E ${suffix}\nrename move trash restore\n`
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)

    await createFolderFromUI(page, sourceName)
    await createFolderFromUI(page, destinationName)

    await itemNamed(page, sourceName).dblclick()
    await expect(page.locator('.breadcrumb-bar')).toContainText(sourceName)
    await uploadFromToolbar(page, {
      name: originalName,
      mimeType: 'text/plain',
      buffer: Buffer.from(content),
    })

    const original = itemNamed(page, originalName)
    await expect(original).toBeVisible()
    await original.click({ button: 'right' })
    await chooseContextAction(page, /^(重命名|Rename)$/)
    const renameInput = page.locator('.rename-input input')
    await expect(renameInput).toBeVisible()
    await renameInput.fill(renamedName)
    await renameInput.press('Enter')
    await expect(itemNamed(page, originalName)).toBeHidden()

    const renamed = itemNamed(page, renamedName)
    await expect(renamed).toBeVisible()
    await renamed.click({ button: 'right' })
    await chooseContextAction(page, /^(剪切|Cut)$/)

    await navigateTo(page, destinationPath)
    await page.locator('.file-view').click({ button: 'right', position: { x: 8, y: 8 } })
    await chooseContextAction(page, /^(粘贴|Paste)$/)
    const moved = itemNamed(page, renamedName)
    await expect(moved).toBeVisible()

    await moved.click({ button: 'right' })
    await chooseContextAction(page, /^(删除|Delete)$/)
    const deleteDialog = page.locator('.breeze-modal-dialog')
    await expect(deleteDialog).toBeVisible()
    await deleteDialog.locator('.breeze-modal-dialog__footer button').last().click()
    await expect(deleteDialog).toBeHidden()
    await expect(itemNamed(page, renamedName)).toBeHidden()

    await navigateTo(page, trashPath)
    const trashed = itemNamed(page, renamedName)
    await expect(trashed).toBeVisible()
    await trashed.click({ button: 'right' })
    await chooseContextAction(page, /^(还原|Restore)$/)
    await expect(trashed).toBeHidden()

    await navigateTo(page, destinationPath)
    const restored = itemNamed(page, renamedName)
    await expect(restored).toBeVisible()
    await waitForServiceWorker(page)
    await restored.dblclick()
    const viewer = page.locator('.plasma-window').filter({
      has: page.locator('.plasma-titlebar-title', { hasText: renamedName }),
    })
    await expect(viewer.locator('.cm-content')).toContainText(`Domus lifecycle E2E ${suffix}`)

    await page.reload({ waitUntil: 'domcontentloaded' })
    await expect(page).toHaveURL(/\/desktop$/)
    await openHomeDirectory(page, credentials.username)
    await navigateTo(page, destinationPath)
    await expect(itemNamed(page, renamedName)).toBeVisible()
  } finally {
    if (authenticated) {
      await permanentlyDelete(page, finalPath)
      await permanentlyDelete(page, sourcePath)
      await permanentlyDelete(page, destinationPath)
    }
  }
})
