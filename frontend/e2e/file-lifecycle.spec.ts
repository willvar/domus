import { expect, test, type Locator, type Page } from '@playwright/test'
import {
  apiBaseURL,
  e2eCredentials,
  getStorageUsage,
  login,
  openFilesRoot,
  permanentlyDelete,
  permanentlyDeleteTrashEntry,
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

async function navigateToTrash(page: Page): Promise<void> {
  await page.goto('/files?place=trash', { waitUntil: 'domcontentloaded' })
  await waitForDirectory(page)
  await expect(page.locator('.breadcrumbs')).toContainText(/回收站|Trash/)
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
  const marker = `DOMUS_LIFECYCLE_${suffix}`
  const replacementMarker = `DOMUS_REUPLOAD_${suffix}`
  const originalContents = Buffer.from(`${marker}\nrename move trash restore\n`)
  const replacementContents = Buffer.from(`${replacementMarker}\nreuse the exact deleted filename\n`)
  let authenticated = false
  let trashEntryID: string | null = null

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

    const trashResponsePromise = page.waitForResponse((response) => {
      const url = new URL(response.url())
      return url.origin === new URL(apiBaseURL).origin &&
        url.pathname === '/trash/' &&
        response.request().method() === 'POST'
    })
    await chooseAction(page, itemNamed(page, renamedName), /^删除$|^Delete$/)
    await confirmDialog(page)
    const trashResponse = await trashResponsePromise
    expect(trashResponse.ok(), `trash failed with HTTP ${trashResponse.status()}`).toBeTruthy()
    trashEntryID = ((await trashResponse.json()) as { id?: string }).id || null
    expect(trashEntryID).toBeTruthy()
    await expect(itemNamed(page, renamedName)).toBeHidden()

    await navigateToTrash(page)
    await chooseAction(page, itemNamed(page, renamedName), /还原|Restore/)
    await expect(itemNamed(page, renamedName)).toBeHidden({ timeout: 30_000 })
    trashEntryID = null

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
      if (trashEntryID) await permanentlyDeleteTrashEntry(page, trashEntryID)
      await permanentlyDelete(page, finalPath)
      await permanentlyDelete(page, sourcePath)
      await permanentlyDelete(page, destinationPath)
    }
  }
})

test('目录可回收还原并可通过 Shift+Delete 永久删除', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const directoryName = `e2e-directory-delete-${suffix}`
  const childName = `child-${suffix}.txt`
  const remainingName = `remaining-${suffix}.txt`
  const directoryPath = `/${directoryName}/`
  const contents = Buffer.from(`DOMUS_DIRECTORY_DELETE_${suffix}\n`)
  let authenticated = false
  let cleanupPath: string | null = directoryPath
  let trashEntryID: string | null = null

  try {
    await login(page, credentials)
    authenticated = true
    await openFilesRoot(page)
    await waitForDirectory(page)
    const baselineUsage = await getStorageUsage(page)

    await createFolder(page, directoryName)
    await itemNamed(page, directoryName).dblclick()
    await waitForDirectory(page)
    await uploadFromToolbar(page, {
      name: childName,
      mimeType: 'text/plain',
      buffer: contents,
    })
    await uploadFromToolbar(page, {
      name: remainingName,
      mimeType: 'text/plain',
      buffer: contents,
    })
    const populatedUsage = { size: baselineUsage.size + contents.length * 2, count: baselineUsage.count + 2 }
    await expect.poll(() => getStorageUsage(page)).toEqual(populatedUsage)

    await navigateTo(page, '/')
    const directory = itemNamed(page, directoryName)
    await directory.click()
    await expect(directory).toHaveClass(/selected/)
    await page.locator('.selection-controls').getByRole('button', { name: /^\s*(删除|Delete)\s*$/ }).click()
    const deleteResponsePromise = page.waitForResponse((response) => {
      const url = new URL(response.url())
      return url.origin === new URL(apiBaseURL).origin &&
        url.pathname === '/trash/' &&
        response.request().method() === 'POST'
    })
    await confirmDialog(page)
    const deleteResponse = await deleteResponsePromise
    const deleteBody = await deleteResponse.text()
    expect(
      deleteResponse.ok(),
      `directory delete failed with HTTP ${deleteResponse.status()}: ${deleteBody}`,
    ).toBeTruthy()
    trashEntryID = (JSON.parse(deleteBody) as { id?: string }).id || null
    expect(trashEntryID).toBeTruthy()
    await expect(directory).toBeHidden()
    cleanupPath = null
    await expect.poll(() => getStorageUsage(page)).toEqual(populatedUsage)

    await navigateToTrash(page)
    const trashedDirectory = itemNamed(page, directoryName)
    await expect(trashedDirectory).toBeVisible()
    await trashedDirectory.dblclick()
    await waitForDirectory(page)
    await expect(itemNamed(page, childName)).toBeVisible()
    await expect(itemNamed(page, remainingName)).toBeVisible()

    // A descendant can be restored independently. Its missing original parent
    // is recreated, while the remainder stays inside the same trash entry.
    await itemNamed(page, childName).click()
    await page.locator('.selection-controls').getByRole('button', { name: /\s*(还原|Restore)\s*/ }).click()
    await expect(itemNamed(page, childName)).toBeHidden()
    await expect(itemNamed(page, remainingName)).toBeVisible()

    await navigateTo(page, directoryPath)
    await expect(itemNamed(page, childName)).toBeVisible()
    await expect(itemNamed(page, remainingName)).toBeHidden()

    await navigateToTrash(page)
    await itemNamed(page, directoryName).click()
    await page.locator('.selection-controls').getByRole('button', { name: /\s*(还原|Restore)\s*/ }).click()
    const mergeDialog = page.locator('.domus-dialog')
    await expect(mergeDialog).toBeVisible()
    await mergeDialog.getByRole('button', { name: /合并文件夹|Merge folders/ }).click()
    await expect(itemNamed(page, directoryName)).toBeHidden()
    cleanupPath = directoryPath
    trashEntryID = null

    await navigateTo(page, '/')
    const restoredDirectory = itemNamed(page, directoryName)
    await expect(restoredDirectory).toBeVisible()
    await restoredDirectory.dblclick()
    await waitForDirectory(page)
    await expect(itemNamed(page, childName)).toBeVisible()
    await expect(itemNamed(page, remainingName)).toBeVisible()
    await expect.poll(() => getStorageUsage(page)).toEqual(populatedUsage)

    await navigateTo(page, '/')
    const permanentDirectory = itemNamed(page, directoryName)
    await permanentDirectory.click()
    const permanentInode = await permanentDirectory.getAttribute('data-inode')
    const permanentResponsePromise = page.waitForResponse((response) => {
      const url = new URL(response.url())
      return url.origin === new URL(apiBaseURL).origin &&
        url.pathname === '/file/delete' &&
        url.searchParams.get('path') === directoryPath &&
        url.searchParams.get('permanent') === 'true' &&
        (!permanentInode || url.searchParams.get('expected_inode') === permanentInode) &&
        response.request().method() === 'DELETE'
    })
    await page.keyboard.press('Shift+Delete')
    await confirmDialog(page)
    const permanentResponse = await permanentResponsePromise
    const permanentBody = await permanentResponse.text()
    expect(
      permanentResponse.ok(),
      `Shift+Delete failed with HTTP ${permanentResponse.status()}: ${permanentBody}`,
    ).toBeTruthy()
    await expect(permanentDirectory).toBeHidden()
    cleanupPath = null
    await expect.poll(() => getStorageUsage(page)).toEqual(baselineUsage)

    await navigateToTrash(page)
    await expect(itemNamed(page, directoryName)).toBeHidden()
  } finally {
    if (authenticated && trashEntryID) {
      await permanentlyDeleteTrashEntry(page, trashEntryID)
    }
    if (authenticated && cleanupPath) {
      await permanentlyDelete(page, cleanupPath)
    }
  }
})

test('用户的 __trash__ 目录与产品回收站互不重叠', async ({ page }) => {
  const credentials = e2eCredentials()
  const markerName = `e2e-real-trash-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const userTrashPath = '/__trash__/'
  const markerPath = `${userTrashPath}${markerName}/`
  let authenticated = false
  let createdUserTrash = false

  try {
    await login(page, credentials)
    authenticated = true
    await openFilesRoot(page)
    await waitForDirectory(page)

    const userTrash = itemNamed(page, '__trash__')
    if (await userTrash.count() === 0) {
      await createFolder(page, '__trash__')
      createdUserTrash = true
    }

    await navigateTo(page, userTrashPath)
    await expect(page.locator('.breadcrumbs')).toContainText('__trash__')
    await createFolder(page, markerName)
    await expect(itemNamed(page, markerName)).toBeVisible()

    await navigateToTrash(page)
    await expect(page).toHaveURL(/\/files\?place=trash(?:&.*)?$/)
    await expect(itemNamed(page, markerName)).toBeHidden()

    await navigateTo(page, userTrashPath)
    await expect(itemNamed(page, markerName)).toBeVisible()
  } finally {
    if (authenticated) {
      await permanentlyDelete(page, markerPath)
      if (createdUserTrash) await permanentlyDelete(page, userTrashPath)
    }
  }
})

test('同一路径的多次删除保留独立版本并可分别操作', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const fileName = `e2e-trash-versions-${suffix}.txt`
  const filePath = `/${fileName}`
  const versions = [
    Buffer.from(`DOMUS_TRASH_VERSION_ONE_${suffix}\n`),
    Buffer.from(`DOMUS_TRASH_VERSION_TWO_${suffix}\n`),
  ]
  const trashIDs: string[] = []
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openFilesRoot(page)
    await waitForDirectory(page)
    const baselineUsage = await getStorageUsage(page)

    for (const contents of versions) {
      await uploadFromToolbar(page, { name: fileName, mimeType: 'text/plain', buffer: contents })
      const responsePromise = page.waitForResponse((response) => {
        const url = new URL(response.url())
        return url.origin === new URL(apiBaseURL).origin &&
          url.pathname === '/trash/' &&
          response.request().method() === 'POST'
      })
      await chooseAction(page, itemNamed(page, fileName), /^删除$|^Delete$/)
      await confirmDialog(page)
      const response = await responsePromise
      expect(response.ok(), `version trash failed with HTTP ${response.status()}`).toBeTruthy()
      const id = ((await response.json()) as { id?: string }).id
      expect(id).toBeTruthy()
      trashIDs.push(id!)
      await expect(itemNamed(page, fileName)).toBeHidden()
    }

    await navigateToTrash(page)
    const deletedVersions = itemNamed(page, fileName)
    await expect(deletedVersions).toHaveCount(2)

    // The preview reads through the ID-addressed trash access endpoint. A
    // permanent delete removes only that selected event, not its same-name peer.
    await waitForServiceWorker(page)
    await deletedVersions.first().dblclick()
    await expect(page.locator('.text-preview')).toContainText(/DOMUS_TRASH_VERSION_(ONE|TWO)_/)
    await page.getByRole('button', { name: /永久删除|Delete Permanently/ }).click()
    await confirmDialog(page)
    await expect(page).toHaveURL(/\/files\?place=trash(?:&.*)?$/)
    await expect(itemNamed(page, fileName)).toHaveCount(1)

    await itemNamed(page, fileName).click()
    await page.locator('.selection-controls').getByRole('button', { name: /\s*(还原|Restore)\s*/ }).click()
    await expect(itemNamed(page, fileName)).toBeHidden()

    await navigateTo(page, '/')
    await expect(itemNamed(page, fileName)).toBeVisible()
    await expect.poll(() => getStorageUsage(page)).toEqual({
      size: baselineUsage.size + Math.min(versions[0].length, versions[1].length),
      count: baselineUsage.count + 1,
    })
  } finally {
    if (authenticated) {
      for (const trashID of trashIDs) await permanentlyDeleteTrashEntry(page, trashID)
      await permanentlyDelete(page, filePath)
    }
  }
})
