import { expect, test, type Locator, type Page } from '@playwright/test'
import {
  e2eCredentials,
  getStorageUsage,
  login,
  openFilesRoot,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

function itemNamed(page: Page, name: string): Locator {
  return page.locator('.file-item').filter({
    has: page.locator('.file-name').getByText(name, { exact: true }),
  })
}

async function waitForDirectory(page: Page): Promise<void> {
  await expect(page.locator('.file-shell')).toBeVisible()
  await expect(page.locator('.file-surface [data-state="loading"]')).toBeHidden({ timeout: 30_000 })
}

async function navigateTo(page: Page, path: string): Promise<void> {
  await page.goto(`/files?path=${encodeURIComponent(path)}`, { waitUntil: 'domcontentloaded' })
  await waitForDirectory(page)
}

async function chooseAction(page: Page, item: Locator, label: RegExp): Promise<void> {
  await item.click({ button: 'right' })
  const menu = page.locator('.action-menu')
  await expect(menu).toBeVisible()
  await menu.getByRole('menuitem', { name: label }).click()
}

async function confirmDialog(page: Page): Promise<void> {
  const dialog = page.locator('.domus-dialog')
  await expect(dialog).toBeVisible()
  await dialog.locator('.n-dialog__action button').last().click()
  await expect(dialog).toBeHidden()
}

async function createFolder(page: Page, name: string): Promise<void> {
  await page.locator('.primary-actions').getByRole('button', { name: /新建文件夹|New Folder/ }).click()
  const dialog = page.locator('.domus-dialog')
  await expect(dialog).toBeVisible()
  await dialog.locator('input').fill(name)
  await dialog.locator('input').press('Enter')
  await expect(dialog).toBeHidden()
  await expect(itemNamed(page, name)).toBeVisible()
}

test('统一文件界面完成新建、上传、重命名、移动、回收与还原', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const sourceName = `e2e-source-${suffix}`
  const destinationName = `e2e-destination-${suffix}`
  const originalName = `e2e-lifecycle-${suffix}.txt`
  const renamedName = `e2e-renamed-${suffix}.txt`
  const rootPath = '/'
  const sourcePath = `${rootPath}${sourceName}/`
  const destinationPath = `${rootPath}${destinationName}/`
  const finalPath = `${destinationPath}${renamedName}`
  const trashPath = `/__trash__/${destinationName}/`
  const marker = `DOMUS_LIFECYCLE_${suffix}`
  const replacementMarker = `DOMUS_REUPLOAD_${suffix}`
  const originalContents = Buffer.from(`${marker}\nrename move trash restore\n`)
  const replacementContents = Buffer.from(`${replacementMarker}\nreuse the exact deleted filename\n`)
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openFilesRoot(page)
    await waitForDirectory(page)

    await createFolder(page, sourceName)
    await createFolder(page, destinationName)
    const baselineUsage = await getStorageUsage(page)

    // A regular click remains an exclusive single selection. Batch selection
    // is an explicit mode and reuses the existing heading controls, so the
    // file surface must not jump when either state is entered.
    const surfaceBeforeSelection = await page.locator('.file-surface').boundingBox()
    const selectModeToggle = page.locator('.select-mode-toggle')
    const selectModeToggleBefore = await selectModeToggle.boundingBox()
    expect(surfaceBeforeSelection).not.toBeNull()
    expect(selectModeToggleBefore).not.toBeNull()
    await itemNamed(page, sourceName).click()
    const selectionControls = page.locator('.selection-controls')
    await expect(selectionControls).toBeVisible()
    await expect(selectModeToggle).toBeVisible()
    await expect(selectModeToggle).toHaveAttribute('aria-pressed', 'false')
    await expect(page.locator('.file-surface')).not.toHaveClass(/selection-mode/)
    await expect(page.locator('.selection-toolbar')).toHaveCount(0)
    await itemNamed(page, destinationName).click()
    await expect(page.locator('.file-item.selected')).toHaveCount(1)
    await expect(page.locator('.file-surface')).not.toHaveClass(/selection-mode/)
    await expect(itemNamed(page, destinationName)).toHaveClass(/selected/)
    await selectModeToggle.click()
    await expect(selectModeToggle).toHaveAttribute('aria-pressed', 'true')
    await expect(page.locator('.file-surface')).toHaveClass(/selection-mode/)
    await expect(page.locator('.selection-check')).toHaveCount(await page.locator('.file-item').count())
    await itemNamed(page, sourceName).click()
    await expect(page.locator('.file-item.selected')).toHaveCount(2)
    await expect(selectionControls).toContainText(/已选择 2 项|2 selected/)
    const surfaceAfterSelection = await page.locator('.file-surface').boundingBox()
    expect(surfaceAfterSelection).not.toBeNull()
    expect(surfaceAfterSelection!.y).toBeCloseTo(surfaceBeforeSelection!.y, 1)
    expect(surfaceAfterSelection!.height).toBeCloseTo(surfaceBeforeSelection!.height, 1)
    const selectModeToggleAfter = await selectModeToggle.boundingBox()
    expect(selectModeToggleAfter).not.toBeNull()
    expect(selectModeToggleAfter!.x).toBeCloseTo(selectModeToggleBefore!.x, 1)
    expect(selectModeToggleAfter!.width).toBeCloseTo(selectModeToggleBefore!.width, 1)
    await selectionControls.getByRole('button', { name: /清除选择|Clear selection/ }).click()
    await expect(selectionControls).toBeHidden()
    await expect(selectModeToggle).toBeVisible()
    await expect(selectModeToggle).toHaveAttribute('aria-pressed', 'false')

    // Desktop users can enter batch selection directly by drawing a classic
    // file-manager rubber band from an empty part of the surface.
    const firstItemBox = await page.locator('.file-item').nth(0).boundingBox()
    const secondItemBox = await page.locator('.file-item').nth(1).boundingBox()
    expect(firstItemBox).not.toBeNull()
    expect(secondItemBox).not.toBeNull()
    await page.mouse.move(firstItemBox!.x - 5, firstItemBox!.y - 5)
    await page.mouse.down()
    await page.mouse.move(
      secondItemBox!.x + secondItemBox!.width + 5,
      secondItemBox!.y + secondItemBox!.height + 5,
      { steps: 8 },
    )
    await expect(page.locator('.rubber-band')).toBeVisible()
    await expect(page.locator('.file-item.selected')).toHaveCount(2)
    await page.mouse.up()
    await expect(page.locator('.rubber-band')).toBeHidden()
    await expect(page.locator('.file-surface')).toHaveClass(/selection-mode/)
    await expect(selectModeToggle).toBeVisible()
    await expect(selectModeToggle).toHaveAttribute('aria-pressed', 'true')
    await expect(selectionControls).toContainText(/已选择 2 项|2 selected/)
    await selectionControls.getByRole('button', { name: /清除选择|Clear selection/ }).click()
    await expect(selectionControls).toBeHidden()

    await page.setViewportSize({ width: 390, height: 844 })
    const mobileSelectEntry = page.locator('.mobile-select-entry')
    await expect(mobileSelectEntry).toBeVisible()
    await mobileSelectEntry.click()
    const mobileSelectionNav = page.locator('.mobile-bottom-nav.is-selection')
    await expect(mobileSelectionNav).toBeVisible()
    await expect(mobileSelectionNav.getByRole('button')).toHaveCount(5)
    await expect(page.locator('.selection-check')).toHaveCount(await page.locator('.file-item').count())
    await mobileSelectionNav.getByRole('button', { name: /完成|Done/ }).click()
    await expect(mobileSelectionNav).toBeHidden()

    await itemNamed(page, sourceName).click()
    await waitForDirectory(page)
    const breadcrumbViewport = page.locator('.breadcrumbs-viewport')
    const mobileBreadcrumbItems = page.locator('.breadcrumbs .n-breadcrumb-item')
    await expect(mobileBreadcrumbItems).toHaveCount(2)
    await expect(mobileBreadcrumbItems.first()).toBeVisible()
    await expect(mobileBreadcrumbItems.last()).toContainText(sourceName)
    await expect(breadcrumbViewport).toHaveClass(/is-clipped-left/)
    const breadcrumbScroll = await breadcrumbViewport.evaluate(element => ({
      left: element.scrollLeft,
      viewport: element.clientWidth,
      content: element.scrollWidth,
    }))
    expect(breadcrumbScroll.content).toBeGreaterThan(breadcrumbScroll.viewport)
    expect(breadcrumbScroll.left).toBeGreaterThan(0)
    await page.locator('.breadcrumbs').getByRole('button', { name: /我的文件|My Files/ }).click()
    await waitForDirectory(page)
    await expect(itemNamed(page, sourceName)).toBeVisible()
    await page.setViewportSize({ width: 1280, height: 800 })

    await itemNamed(page, sourceName).dblclick()
    await waitForDirectory(page)
    await uploadFromToolbar(page, {
      name: originalName,
      mimeType: 'text/plain',
      buffer: originalContents,
    })
    await expect.poll(async () => await getStorageUsage(page), {
      message: 'uploaded file was not reflected in logical storage usage',
    }).toEqual({ size: baselineUsage.size + originalContents.length, count: baselineUsage.count + 1 })

    await chooseAction(page, itemNamed(page, originalName), /重命名|Rename/)
    const renameDialog = page.locator('.domus-dialog')
    await expect(renameDialog).toBeVisible()
    await renameDialog.locator('input').fill(renamedName)
    await renameDialog.locator('input').press('Enter')
    await expect(itemNamed(page, renamedName)).toBeVisible()

    await chooseAction(page, itemNamed(page, renamedName), /剪切|Cut/)
    await page.locator('.breadcrumbs').getByRole('button', { name: /我的文件|My Files/ }).click()
    await expect(itemNamed(page, destinationName)).toBeVisible({ timeout: 30_000 })
    await itemNamed(page, destinationName).dblclick()
    await expect(page.locator('.breadcrumbs')).toContainText(destinationName)
    const clipboard = page.locator('.clipboard-banner')
    await expect(clipboard).toBeVisible()
    await clipboard.getByRole('button', { name: /粘贴|Paste/ }).click()
    await expect(itemNamed(page, renamedName)).toBeVisible({ timeout: 30_000 })

    await chooseAction(page, itemNamed(page, renamedName), /^删除$|^Delete$/)
    await confirmDialog(page)
    await expect(itemNamed(page, renamedName)).toBeHidden()

    await navigateTo(page, trashPath)
    await chooseAction(page, itemNamed(page, renamedName), /还原|Restore/)
    await expect(itemNamed(page, renamedName)).toBeHidden({ timeout: 30_000 })
    await navigateTo(page, '/__trash__/')
    await expect(itemNamed(page, destinationName)).toBeHidden()

    await navigateTo(page, destinationPath)
    const restored = itemNamed(page, renamedName)
    await expect(restored).toBeVisible()
    await waitForServiceWorker(page)
    await restored.dblclick()
    await expect(page.locator('.text-preview')).toContainText(marker)

    await page.goto(`/files?path=${encodeURIComponent(destinationPath)}`, { waitUntil: 'domcontentloaded' })
    await waitForDirectory(page)
    await expect(itemNamed(page, renamedName)).toBeVisible()

    // A completed upload leaves retry state keyed by its upload ID. Permanently
    // deleting the inode must not let that transient row reserve the old path.
    await permanentlyDelete(page, finalPath)
    await expect(itemNamed(page, renamedName)).toBeHidden({ timeout: 30_000 })
    await expect.poll(async () => await getStorageUsage(page), {
      message: 'permanent deletion did not release logical storage usage',
    }).toEqual(baselineUsage)
    await uploadFromToolbar(page, {
      name: renamedName,
      mimeType: 'text/plain',
      buffer: replacementContents,
    })
    const replacement = itemNamed(page, renamedName)
    await expect(replacement).toBeVisible({ timeout: 30_000 })
    await replacement.dblclick()
    await expect(page.locator('.text-preview')).toContainText(replacementMarker)
  } finally {
    if (authenticated) {
      await permanentlyDelete(page, finalPath)
      await permanentlyDelete(page, sourcePath)
      await permanentlyDelete(page, destinationPath)
    }
  }
})
