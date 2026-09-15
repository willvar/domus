<script setup lang="ts">
import { computed, h, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import type { Component } from 'vue'
import {
  NBadge,
  NAlert,
  NBreadcrumb,
  NBreadcrumbItem,
  NButton,
  NButtonGroup,
  NDescriptions,
  NDescriptionsItem,
  NDropdown,
  NDrawer,
  NDrawerContent,
  NEmpty,
  NInput,
  NMenu,
  NPopover,
  NResult,
  NSelect,
  NSpin,
  NTag,
} from 'naive-ui'
import type { DropdownOption, InputInst, MenuOption } from 'naive-ui'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import {
  IconAccountCircle,
  IconArrowLeft,
  IconArrowRight,
  IconArrowUp,
  IconCheck,
  IconCheckboxMultipleMarkedOutline,
  IconChevronDown,
  IconClose,
  IconContentCopy,
  IconContentCut,
  IconDeleteOutline,
  IconDotsHorizontal,
  IconDownload,
  IconFolderHome,
  IconFolderPlusOutline,
  IconInformationOutline,
  IconMagnify,
  IconPencilOutline,
  IconPlus,
  IconProgressClock,
  IconRefresh,
  IconRestore,
  IconSortAscending,
  IconSortDescending,
  IconUpload,
  IconViewGridOutline,
  IconViewListOutline,
} from '../barrels/icons'
import { getFileIcon } from '../composables/useFileIcon'
import { useDevice } from '../composables/useDevice'
import { useI18n } from '../composables/useI18n'
import { useAppMessage } from '../ui/feedback'
import { showConfirm, showPrompt } from '../composables/useNativeDialog'
import api from '../composables/useApi'
import { useAuthStore } from '../stores/auth'
import { useFileSystemStore } from '../stores/fileSystem'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useTasksStore } from '../stores/tasks'
import { useUploadStore } from '../stores/upload'
import type { FileListItem } from '../types'
import {
  TRASH_ROOT_LOCATION,
  trashLocationFromRouteQuery,
  trashLocationRouteQuery,
} from '../utils/trashLocation'
import AccountMenu from '../components/AccountMenu.vue'
import ActivityCenter from '../components/ActivityCenter.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const fs = useFileSystemStore()
const upload = useUploadStore()
const tasks = useTasksStore()
const pendingOps = usePendingOpsStore()
const { height: viewportHeight, isMobile, isTouchInput } = useDevice()
const { t } = useI18n()
const message = useAppMessage()

const uploadInput = ref<HTMLInputElement | null>(null)
const searchInput = ref<InputInst | null>(null)
const fileSurface = ref<HTMLDivElement | null>(null)
const breadcrumbViewport = ref<HTMLDivElement | null>(null)
const breadcrumbOverflowLeft = ref(false)
const breadcrumbOverflowRight = ref(false)
const searchText = ref('')
const showAccount = ref(false)
const showActivity = ref(false)
const showInspector = ref(false)
const showThumbnails = ref(localStorage.getItem('domus_show_thumbnails') === '1')
const detailsFile = ref<FileListItem | null>(null)
const actionFile = ref<FileListItem | null>(null)
const showActionMenu = ref(false)
const actionMenuEpoch = ref(0)
const actionMenuX = ref(0)
const actionMenuY = ref(0)
const draggingFiles = ref(false)
let searchTimer: number | null = null
let longPressTimer: number | null = null
let suppressClickPath = ''
let suppressSurfaceClick = false
let suppressSurfaceClickTimer: number | null = null
let rubberBandInitialSelection: string[] = []
let bodyUserSelectBeforeRubberBand: string | null = null
let breadcrumbResizeObserver: ResizeObserver | null = null

const rubberBand = reactive({
  tracking: false,
  active: false,
  pointerId: -1,
  startClientX: 0,
  startClientY: 0,
  startX: 0,
  startY: 0,
  currentX: 0,
  currentY: 0,
  additive: false,
})

const rubberBandStyle = computed(() => {
  const left = Math.min(rubberBand.startX, rubberBand.currentX)
  const top = Math.min(rubberBand.startY, rubberBand.currentY)
  return {
    left: `${left}px`,
    top: `${top}px`,
    width: `${Math.abs(rubberBand.currentX - rubberBand.startX)}px`,
    height: `${Math.abs(rubberBand.currentY - rubberBand.startY)}px`,
  }
})

const homePath = '/'
const isHome = computed(() => fs.currentPath === homePath && !fs.searchMode)
const activePlace = computed(() => fs.isTrash ? 'trash' : 'files')
const currentTitle = computed(() => {
  if (fs.searchMode) return t('files.search_results')
  if (fs.isTrash) return fs.pathSegments[fs.pathSegments.length - 1]?.name || t('places.trash')
  if (isHome.value) return t('files.my_files')
  const value = fs.currentPath.replace(/\/$/, '').split('/').pop()
  return value || t('files.my_files')
})
const directoryFiles = computed(() => fs.sortedFiles)
const foldersCount = computed(() => directoryFiles.value.filter(file => file.is_dir).length)
const filesCount = computed(() => directoryFiles.value.length - foldersCount.value)
const activeTaskIDs = computed(() => new Set(upload.uploads.map(item => item.taskId).filter(Boolean)))
const serverTasks = computed(() => tasks.activeTasks
  .filter(task => task.type === 'upload' && !activeTaskIDs.value.has(task.task_id))
  .slice(0, 20))
const activityCount = computed(() => upload.activeUploads.length + tasks.activeTasks.filter(task => !activeTaskIDs.value.has(task.task_id)).length + pendingOps.pendingCount)
const mobileActivityDrawerHeight = computed(() => {
  const maximum = Math.max(260, Math.min(560, Math.round(viewportHeight.value * 0.72)))
  if (activityCount.value === 0) return Math.min(304, maximum)
  return Math.min(maximum, 210 + Math.min(activityCount.value, 3) * 104)
})
const mobileActivityListHeight = computed(() => `${Math.max(160, mobileActivityDrawerHeight.value - 108)}px`)
const hasClipboard = computed(() => fs.clipboard.items.length > 0)
const selectionCount = computed(() => fs.selectedFiles.length)
const selectedItem = computed(() => {
  if (selectionCount.value !== 1) return null
  return directoryFiles.value.find(file => file.path === fs.selectedFiles[0]) || null
})
const displaySegments = computed(() => {
  return fs.pathSegments
})
const activityLabel = computed(() => {
  if (activityCount.value > 0) return t('files.activity_active', { n: activityCount.value })
  return t('files.activity_idle')
})

function renderIcon(icon: Component, size = 18): () => ReturnType<typeof h> {
  return () => h(icon, { width: size, height: size })
}

const placeOptions = computed<MenuOption[]>(() => [
  {
    key: 'files',
    label: t('files.my_files'),
    icon: renderIcon(IconFolderHome, 21),
  },
  {
    key: 'trash',
    label: t('places.trash'),
    icon: renderIcon(IconDeleteOutline, 21),
  },
])

const sortOptions = computed(() => [
  { label: t('toolbar.sort_name'), value: 'name' },
  { label: t('toolbar.sort_date'), value: 'date' },
  { label: t('toolbar.sort_size'), value: 'size' },
])

const createOptions = computed<DropdownOption[]>(() => [
  { key: 'upload', label: t('toolbar.upload'), icon: renderIcon(IconUpload) },
  { key: 'folder', label: t('toolbar.new_folder'), icon: renderIcon(IconFolderPlusOutline) },
])

const actionMenuOptions = computed<DropdownOption[]>(() => {
  const file = actionFile.value
  if (!file) return []
  const fileIsTrashItem = !!file.trash_id
  const options: DropdownOption[] = [
    { key: 'open', label: t('menu.open'), icon: renderIcon(IconArrowRight) },
  ]
  if (!file.is_dir) {
    options.push({ key: 'download', label: t('menu.download'), icon: renderIcon(IconDownload) })
  }
  options.push(
    { key: 'details', label: t('menu.details'), icon: renderIcon(IconInformationOutline) },
    { key: 'main-divider', type: 'divider' },
  )
  if (!fileIsTrashItem) {
    options.push(
      { key: 'copy', label: t('menu.copy'), icon: renderIcon(IconContentCopy) },
      { key: 'cut', label: t('menu.cut'), icon: renderIcon(IconContentCut) },
      { key: 'rename', label: t('menu.rename'), icon: renderIcon(IconPencilOutline) },
    )
  } else {
    options.push({ key: 'restore', label: t('menu.restore'), icon: renderIcon(IconRestore) })
  }
  options.push({
    key: 'delete',
    label: fileIsTrashItem ? t('menu.permanent_delete') : t('menu.delete'),
    icon: renderIcon(IconDeleteOutline),
    props: { class: 'domus-danger-option' },
  })
  return options
})

onMounted(async () => {
  if (fs.tabs.length === 0) {
    fs.init()
    const trashLocation = trashLocationFromRouteQuery(
      route.query.place,
      route.query.trash,
      route.query.path,
    )
    const requested = trashLocation || (
      typeof route.query.path === 'string' && route.query.path.startsWith('/')
        ? route.query.path
        : homePath
    )
    fs.createTab(requested)
  }
  await Promise.allSettled([
    pendingOps.init(auth.username),
    tasks.fetchTasks(),
  ])
  window.addEventListener('keydown', handleKeyboard)
  await nextTick()
  if (breadcrumbViewport.value) {
    breadcrumbResizeObserver = new ResizeObserver(updateBreadcrumbOverflow)
    breadcrumbResizeObserver.observe(breadcrumbViewport.value)
  }
  revealCurrentBreadcrumb()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeyboard)
  if (searchTimer !== null) window.clearTimeout(searchTimer)
  if (longPressTimer !== null) window.clearTimeout(longPressTimer)
  if (suppressSurfaceClickTimer !== null) window.clearTimeout(suppressSurfaceClickTimer)
  breadcrumbResizeObserver?.disconnect()
  if (bodyUserSelectBeforeRubberBand !== null) {
    document.body.style.userSelect = bodyUserSelectBeforeRubberBand
    bodyUserSelectBeforeRubberBand = null
  }
})

watch(() => fs.currentPath, (path) => {
  revealCurrentBreadcrumb()
  if (!path || fs.searchMode) return
  if (fs.isTrash) {
    void router.replace({ path: '/files', query: trashLocationRouteQuery(path) })
    return
  }
  const queryPath = typeof route.query.path === 'string' ? route.query.path : ''
  const hasVirtualQuery = route.query.place != null || route.query.trash != null
  if (queryPath !== path || hasVirtualQuery) {
    void router.replace({ path: '/files', query: path === homePath ? {} : { path } })
  }
})

watch(isMobile, () => revealCurrentBreadcrumb())

watch(selectedItem, (file) => {
  if (showInspector.value) detailsFile.value = file
})

function closeFloatingPanels(): void {
  showAccount.value = false
  showActivity.value = false
  showActionMenu.value = false
  actionFile.value = null
}

function updateBreadcrumbOverflow(): void {
  const viewport = breadcrumbViewport.value
  if (!viewport || !isMobile.value) {
    breadcrumbOverflowLeft.value = false
    breadcrumbOverflowRight.value = false
    return
  }
  breadcrumbOverflowLeft.value = viewport.scrollLeft > 2
  breadcrumbOverflowRight.value = viewport.scrollLeft + viewport.clientWidth < viewport.scrollWidth - 2
}

function revealCurrentBreadcrumb(): void {
  void nextTick(() => {
    const viewport = breadcrumbViewport.value
    if (!viewport) return
    if (isMobile.value) viewport.scrollLeft = viewport.scrollWidth
    updateBreadcrumbOverflow()
  })
}

function setThumbnailDisplay(value: boolean): void {
  showThumbnails.value = value
  localStorage.setItem('domus_show_thumbnails', showThumbnails.value ? '1' : '0')
}

async function goTo(path: string): Promise<void> {
  closeFloatingPanels()
  if (fs.searchMode) fs.exitSearch()
  searchText.value = ''
  await fs.navigate(path)
}

async function openItem(file: FileListItem): Promise<void> {
  closeFloatingPanels()
  if (file.is_dir) {
    await goTo(file.path)
    return
  }
  fs.clearSelection()
  await router.push({
    path: '/preview',
    query: {
      path: file.path,
      name: file.name,
      inode: file.inode ? String(file.inode) : undefined,
      from: route.fullPath,
    },
  })
}

function handleItemClick(file: FileListItem, event: MouseEvent): void {
  if (suppressClickPath === file.path) {
    suppressClickPath = ''
    return
  }
  if (fs.searchMode) {
    void openItem(file)
    return
  }
  if (isMobile.value || isTouchInput.value) {
    if (fs.selectMode) fs.toggleSelect(file.path)
    else void openItem(file)
    return
  }

  if (fs.selectMode) {
    if (event.shiftKey) fs.selectFile(file.path, event)
    else fs.toggleSelect(file.path)
    return
  }

  if (event.ctrlKey || event.metaKey || event.shiftKey) {
    fs.enterSelectMode()
    if (event.shiftKey) fs.selectFile(file.path, event)
    else fs.toggleSelect(file.path)
    return
  }

  fs.selectFile(file.path, event)
}

function enterSelectMode(path?: string): void {
  closeFloatingPanels()
  fs.enterSelectMode(path)
}

function toggleSelectMode(): void {
  closeFloatingPanels()
  if (fs.selectMode) fs.exitSelectMode()
  else fs.enterSelectMode(selectedItem.value?.path)
}

function beginLongPress(file: FileListItem, event: PointerEvent): void {
  if (event.pointerType !== 'touch' && event.pointerType !== 'pen') return
  if (fs.selectMode || fs.searchMode) return
  if (longPressTimer !== null) window.clearTimeout(longPressTimer)
  longPressTimer = window.setTimeout(() => openTouchMenu(file, event), 520)
}

function endLongPress(): void {
  if (longPressTimer !== null) window.clearTimeout(longPressTimer)
  longPressTimer = null
}

let touchMenuAt = 0
let touchMenuPath = ''

function openTouchMenu(file: FileListItem, event: MouseEvent): void {
  endLongPress()
  const now = Date.now()
  if (now - touchMenuAt < 900 && touchMenuPath === file.path) return
  touchMenuAt = now
  touchMenuPath = file.path
  suppressClickPath = file.path
  navigator.vibrate?.(18)
  openActionMenu(file, event, false)
}

function handleContextMenu(file: FileListItem, event: MouseEvent): void {
  if (longPressTimer !== null) {
    openTouchMenu(file, event)
    return
  }
  if (Date.now() - touchMenuAt < 900 && touchMenuPath === file.path) return
  openActionMenu(file, event)
}

function rubberBandPoint(event: PointerEvent): { x: number; y: number } | null {
  const surface = fileSurface.value
  if (!surface) return null
  const rect = surface.getBoundingClientRect()
  return {
    x: Math.max(0, Math.min(surface.scrollWidth, event.clientX - rect.left + surface.scrollLeft)),
    y: Math.max(0, Math.min(surface.scrollHeight, event.clientY - rect.top + surface.scrollTop)),
  }
}

function beginRubberBand(event: PointerEvent): void {
  if (
    event.pointerType !== 'mouse'
    || event.button !== 0
    || isMobile.value
    || fs.searchMode
    || fs.loading
    || !!fs.error
    || directoryFiles.value.length === 0
    || draggingFiles.value
    || rubberBand.tracking
  ) return

  const target = event.target as HTMLElement | null
  if (target?.closest('.file-item, .file-list__head, button, a, input, textarea, select, [contenteditable="true"]')) return

  const surface = fileSurface.value
  const point = rubberBandPoint(event)
  if (!surface || !point) return
  const surfaceRect = surface.getBoundingClientRect()
  const scrollbarWidth = surface.offsetWidth - surface.clientWidth
  const scrollbarHeight = surface.offsetHeight - surface.clientHeight
  if (
    (scrollbarWidth > 0 && event.clientX >= surfaceRect.right - scrollbarWidth)
    || (scrollbarHeight > 0 && event.clientY >= surfaceRect.bottom - scrollbarHeight)
  ) return

  closeFloatingPanels()
  rubberBand.tracking = true
  rubberBand.active = false
  rubberBand.pointerId = event.pointerId
  rubberBand.startClientX = event.clientX
  rubberBand.startClientY = event.clientY
  rubberBand.startX = point.x
  rubberBand.startY = point.y
  rubberBand.currentX = point.x
  rubberBand.currentY = point.y
  rubberBand.additive = event.ctrlKey || event.metaKey
  rubberBandInitialSelection = [...fs.selectedFiles]
  surface.setPointerCapture(event.pointerId)
}

function updateRubberBandSelection(): void {
  const surface = fileSurface.value
  if (!surface) return
  const surfaceRect = surface.getBoundingClientRect()
  const bandLeft = Math.min(rubberBand.startX, rubberBand.currentX)
  const bandTop = Math.min(rubberBand.startY, rubberBand.currentY)
  const bandRight = Math.max(rubberBand.startX, rubberBand.currentX)
  const bandBottom = Math.max(rubberBand.startY, rubberBand.currentY)
  const hitPaths: string[] = []

  for (const item of surface.querySelectorAll<HTMLElement>('.file-item[data-path]')) {
    const itemRect = item.getBoundingClientRect()
    const itemLeft = itemRect.left - surfaceRect.left + surface.scrollLeft
    const itemTop = itemRect.top - surfaceRect.top + surface.scrollTop
    const itemRight = itemLeft + itemRect.width
    const itemBottom = itemTop + itemRect.height
    if (bandLeft < itemRight && bandRight > itemLeft && bandTop < itemBottom && bandBottom > itemTop) {
      const path = item.dataset.path
      if (path) hitPaths.push(path)
    }
  }

  fs.selectedFiles = rubberBand.additive
    ? [...new Set([...rubberBandInitialSelection, ...hitPaths])]
    : hitPaths
  fs.enterSelectMode(fs.selectedFiles[fs.selectedFiles.length - 1])
}

function moveRubberBand(event: PointerEvent): void {
  if (!rubberBand.tracking || event.pointerId !== rubberBand.pointerId) return
  const point = rubberBandPoint(event)
  if (!point) return

  if (!rubberBand.active) {
    const distance = Math.max(
      Math.abs(event.clientX - rubberBand.startClientX),
      Math.abs(event.clientY - rubberBand.startClientY),
    )
    if (distance < 5) return
    rubberBand.active = true
    bodyUserSelectBeforeRubberBand = document.body.style.userSelect
    document.body.style.userSelect = 'none'
  }

  event.preventDefault()
  rubberBand.currentX = point.x
  rubberBand.currentY = point.y
  updateRubberBandSelection()
}

function endRubberBand(event: PointerEvent): void {
  if (!rubberBand.tracking || event.pointerId !== rubberBand.pointerId) return
  const surface = fileSurface.value
  const used = rubberBand.active
  if (surface?.hasPointerCapture(event.pointerId)) surface.releasePointerCapture(event.pointerId)
  rubberBand.tracking = false
  rubberBand.active = false
  rubberBand.pointerId = -1
  if (bodyUserSelectBeforeRubberBand !== null) {
    document.body.style.userSelect = bodyUserSelectBeforeRubberBand
    bodyUserSelectBeforeRubberBand = null
  }

  if (!used) return
  suppressSurfaceClick = true
  if (suppressSurfaceClickTimer !== null) window.clearTimeout(suppressSurfaceClickTimer)
  suppressSurfaceClickTimer = window.setTimeout(() => {
    suppressSurfaceClick = false
    suppressSurfaceClickTimer = null
  }, 0)
}

function handleSurfaceClick(): void {
  if (suppressSurfaceClick) {
    suppressSurfaceClick = false
    if (suppressSurfaceClickTimer !== null) window.clearTimeout(suppressSurfaceClickTimer)
    suppressSurfaceClickTimer = null
    return
  }
  fs.exitSelectMode()
}

function openActionMenu(file: FileListItem, event?: MouseEvent, select = true): void {
  showActionMenu.value = false
  actionMenuEpoch.value++
  if (select && !fs.searchMode && !fs.selectedFiles.includes(file.path)) fs.selectedFiles = [file.path]
  actionFile.value = file
  detailsFile.value = file
  if (event) {
    actionMenuX.value = event.clientX
    actionMenuY.value = event.clientY
  }
  // A prior teleported menu may emit clickoutside during this same pointer event.
  // Reopen after Vue has retired that overlay so repeated actions use this file.
  void nextTick(() => {
    showActionMenu.value = true
  })
}

async function handleActionSelect(key: string | number): Promise<void> {
  const file = actionFile.value
  if (!file) return
  showActionMenu.value = false
  switch (key) {
    case 'open': await openItem(file); break
    case 'download': await downloadItem(file); break
    case 'details': showDetails(file); break
    case 'copy': setClipboard(file, 'copy'); break
    case 'cut': setClipboard(file, 'cut'); break
    case 'rename': await renameItem(file); break
    case 'restore': await restoreItem(file); break
    case 'delete': await deleteItem(file); break
  }
}

function handleCreateSelect(key: string | number): void {
  if (key === 'upload') triggerUpload()
  if (key === 'folder') void fs.createFolder()
}

function openAdmin(): void {
  showAccount.value = false
  void router.push('/admin')
}

function dropdownNodeProps(): { role: string } {
  return { role: 'menuitem' }
}

function showDetails(file: FileListItem): void {
  detailsFile.value = file
  showInspector.value = true
  actionFile.value = null
}

function setClipboard(file: FileListItem, mode: 'copy' | 'cut'): void {
  if (fs.searchMode) {
    fs.clipboard = { items: [{ path: file.path, is_dir: file.is_dir, name: file.name }], mode }
  } else {
    fs.selectedFiles = [file.path]
    if (mode === 'copy') fs.copySelected()
    else fs.cutSelected()
  }
  actionFile.value = null
  message.success(mode === 'copy' ? t('files.copied') : t('files.cut'))
}

async function renameItem(file: FileListItem): Promise<void> {
  actionFile.value = null
  const name = await showPrompt(t('menu.rename'), file.name)
  if (!name || name === file.name) return
  await fs.rename(file.path, name, file.is_dir)
  if (fs.searchMode && searchText.value.trim().length >= 2) await fs.performSearch(searchText.value.trim())
}

async function deleteItem(file: FileListItem): Promise<void> {
  actionFile.value = null
  if (!fs.searchMode) {
    fs.selectedFiles = [file.path]
    await fs.deleteSelected()
    return
  }
  const ok = await showConfirm(t('dialog.delete_title'), t('dialog.confirm_delete', { n: 1 }), { icon: 'warning', positiveType: 'error' })
  if (!ok) return
  if (!file.inode) {
    message.error(t('files.delete_failed', { n: 1 }))
    return
  }
  await api.post('/trash/', { path: file.path, expected_inode: file.inode })
  await fs.performSearch(searchText.value.trim())
}

async function restoreItem(file: FileListItem): Promise<void> {
  actionFile.value = null
  fs.selectedFiles = [file.path]
  await fs.restoreSelected()
}

async function downloadItem(file: FileListItem): Promise<void> {
  actionFile.value = null
  await fs.downloadFile(file.path)
}

async function openSelected(): Promise<void> {
  if (selectedItem.value) await openItem(selectedItem.value)
}

async function renameSelected(): Promise<void> {
  if (selectedItem.value) await renameItem(selectedItem.value)
}

function triggerUpload(): void {
  closeFloatingPanels()
  uploadInput.value?.click()
}

function handleUploadSelection(event: Event): void {
  const input = event.target as HTMLInputElement
  if (input.files?.length) {
    void upload.uploadFiles(input.files, fs.currentPath)
  }
  input.value = ''
}

function onDragEnter(event: DragEvent): void {
  if (fs.isTrash || fs.searchMode || !event.dataTransfer?.types.includes('Files')) return
  draggingFiles.value = true
}

function onDragLeave(event: DragEvent): void {
  if (event.currentTarget === event.target) draggingFiles.value = false
}

function onDrop(event: DragEvent): void {
  draggingFiles.value = false
  if (fs.isTrash || fs.searchMode || !event.dataTransfer?.files.length) return
  void upload.uploadFiles(event.dataTransfer.files, fs.currentPath)
}

function runSearch(): void {
  if (searchTimer !== null) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(async () => {
    const query = searchText.value.trim()
    if (query.length < 2) {
      fs.exitSearch()
      return
    }
    fs.clearSelection()
    await fs.performSearch(query)
  }, 260)
}

function clearSearch(): void {
  searchText.value = ''
  fs.exitSearch()
  searchInput.value?.focus()
}

function toggleSortOrder(): void {
  fs.sortOrder = fs.sortOrder === 'asc' ? 'desc' : 'asc'
}

function choosePlace(place: 'files' | 'trash'): void {
  void goTo(place === 'trash' ? TRASH_ROOT_LOCATION : homePath)
}

async function logout(): Promise<void> {
  closeFloatingPanels()
  await auth.logout()
  await router.replace('/files')
}

function handleKeyboard(event: KeyboardEvent): void {
  const target = event.target as HTMLElement | null
  const typing = target?.matches('input, textarea, [contenteditable="true"]')
  if ((event.ctrlKey || event.metaKey) && ['f', 'k'].includes(event.key.toLowerCase())) {
    event.preventDefault()
    searchInput.value?.focus()
    return
  }
  if (typing) return
  if (event.key === 'Escape') {
    closeFloatingPanels()
    showInspector.value = false
    fs.exitSelectMode()
  } else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'a' && !fs.searchMode) {
    event.preventDefault()
    fs.selectAll()
  } else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'c' && selectionCount.value) {
    event.preventDefault()
    fs.copySelected()
  } else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'x' && selectionCount.value) {
    event.preventDefault()
    fs.cutSelected()
  } else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'v' && hasClipboard.value && !fs.isTrash) {
    event.preventDefault()
    void fs.paste()
  } else if (event.key === 'Delete' && selectionCount.value) {
    event.preventDefault()
    void fs.deleteSelected(event.shiftKey)
  } else if (event.key === 'Enter' && selectionCount.value === 1) {
    void openSelected()
  } else if (event.key === 'F2' && selectionCount.value === 1) {
    event.preventDefault()
    void renameSelected()
  }
}

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / (1024 ** index)
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`
}

function displayDate(value: string): string {
  if (!value) return '—'
  return dayjs(value).format(fs.isTrash && !fs.searchMode ? 'YYYY-MM-DD HH:mm:ss' : 'YYYY-MM-DD HH:mm')
}

function phaseLabel(phase?: string): string {
  const labels: Record<string, string> = {
    queued: t('files.phase_waiting'),
    generating: t('files.phase_preparing'),
    encrypting: t('files.phase_encrypting'),
    uploading: t('files.phase_uploading'),
    thumbnail: t('files.phase_preview'),
    processing: t('files.phase_finishing'),
  }
  return (phase && labels[phase]) || phase || t('files.phase_waiting')
}
</script>

<template>
  <div
    class="file-shell"
    @click="closeFloatingPanels"
    @dragenter.prevent="onDragEnter"
    @dragover.prevent
    @dragleave="onDragLeave"
    @drop.prevent="onDrop"
  >
    <aside class="file-sidebar" aria-label="File locations">
      <div class="brand-lockup">
        <div class="brand-mark">D</div>
        <div>
          <div class="brand-name">DOMUS</div>
          <div class="brand-caption">{{ t('files.private_space') }}</div>
        </div>
      </div>

      <NMenu
        class="place-list"
        :value="activePlace"
        :options="placeOptions"
        :indent="12"
        :root-indent="8"
        @update:value="value => choosePlace(value as 'files' | 'trash')"
      />

      <NPopover
        :show="showAccount && !isMobile"
        trigger="click"
        placement="right-end"
        :show-arrow="false"
        @update:show="showAccount = $event"
      >
        <template #trigger>
        <NButton quaternary class="sidebar-account" @click.stop="showActivity = false">
          <img v-if="auth.user?.avatar_url" :src="auth.user.avatar_url" alt="" />
          <IconAccountCircle v-else width="34" height="34" />
          <span>
            <strong>{{ auth.user?.display_name || auth.username }}</strong>
            <small>@{{ auth.username }}</small>
          </span>
          <IconChevronDown width="18" height="18" />
        </NButton>
        </template>
        <AccountMenu
          :avatar-url="auth.user?.avatar_url"
          :display-name="auth.user?.display_name || auth.username"
          :username="auth.username"
          :role="auth.user?.role"
          :root="auth.isRoot"
          :show-thumbnails="showThumbnails"
          @update:show-thumbnails="setThumbnailDisplay"
          @admin="openAdmin"
          @logout="logout"
        />
      </NPopover>
    </aside>

    <main class="file-main">
      <header class="file-header">
        <div class="mobile-brand">
          <div class="brand-mark">D</div>
          <strong>DOMUS</strong>
        </div>
        <NInput
          ref="searchInput"
          v-model:value="searchText"
          class="global-search"
          type="text"
          clearable
          :placeholder="t('files.search_placeholder')"
          :aria-label="t('files.search_placeholder')"
          @update:value="runSearch"
          @clear="clearSearch"
        >
          <template #prefix><IconMagnify width="20" height="20" /></template>
          <template #suffix><kbd>Ctrl K</kbd></template>
        </NInput>
        <div class="header-actions">
          <NPopover
            v-if="!isMobile"
            :show="showActivity"
            trigger="manual"
            placement="bottom-end"
            :show-arrow="false"
            :style="{ width: '360px' }"
            @clickoutside="showActivity = false"
          >
            <template #trigger>
              <NBadge :value="activityCount" :show="activityCount > 0" :max="99">
                <NButton circle class="activity-button" :aria-label="t('files.activity')" :title="t('files.activity')" @click.stop="showAccount = false; showActivity = !showActivity">
                  <template #icon><IconProgressClock class="activity-center-icon" width="20" height="20" /></template>
                </NButton>
              </NBadge>
            </template>
            <ActivityCenter
              :uploads="upload.uploads"
              :server-tasks="serverTasks"
              :pending-count="pendingOps.pendingCount"
              :activity-label="activityLabel"
              @cancel-upload="upload.cancelUpload"
            />
          </NPopover>
          <NBadge v-else :value="activityCount" :show="activityCount > 0" :max="99">
            <NButton circle class="activity-button" :aria-label="t('files.activity')" :title="t('files.activity')" @click.stop="showAccount = false; showActivity = !showActivity">
              <template #icon><IconProgressClock class="activity-center-icon" width="20" height="20" /></template>
            </NButton>
          </NBadge>
          <NPopover
            :show="showAccount && isMobile"
            trigger="click"
            placement="bottom-end"
            :show-arrow="false"
            @update:show="showAccount = $event"
          >
            <template #trigger>
            <NButton quaternary circle class="mobile-account" @click.stop="showActivity = false">
              <img v-if="auth.user?.avatar_url" :src="auth.user.avatar_url" alt="" />
              <IconAccountCircle v-else width="34" height="34" />
            </NButton>
            </template>
            <AccountMenu
              :avatar-url="auth.user?.avatar_url"
              :display-name="auth.user?.display_name || auth.username"
              :username="auth.username"
              :role="auth.user?.role"
              :root="auth.isRoot"
              :show-thumbnails="showThumbnails"
              @update:show-thumbnails="setThumbnailDisplay"
              @admin="openAdmin"
              @logout="logout"
            />
          </NPopover>
        </div>
      </header>

      <section class="file-workspace">
        <div class="location-toolbar">
          <NButtonGroup class="history-buttons">
            <NButton quaternary size="small" :disabled="!fs.canGoBack" :title="t('toolbar.back')" @click="fs.goBack()">
              <template #icon><IconArrowLeft /></template>
            </NButton>
            <NButton quaternary size="small" :disabled="!fs.canGoForward" :title="t('toolbar.forward')" @click="fs.goForward()">
              <template #icon><IconArrowRight /></template>
            </NButton>
            <NButton quaternary size="small" :disabled="!fs.canGoUp" :title="t('toolbar.up')" @click="fs.goUp()">
              <template #icon><IconArrowUp /></template>
            </NButton>
          </NButtonGroup>
          <div
            ref="breadcrumbViewport"
            class="breadcrumbs-viewport"
            :class="{
              'is-clipped-left': breadcrumbOverflowLeft,
              'is-clipped-right': breadcrumbOverflowRight,
            }"
            @scroll.passive="updateBreadcrumbOverflow"
          >
            <NBreadcrumb class="breadcrumbs" :aria-label="t('files.location')">
              <NBreadcrumbItem v-if="!fs.isTrash">
                <NButton text size="tiny" @click="goTo(homePath)">{{ t('files.my_files') }}</NButton>
              </NBreadcrumbItem>
              <NBreadcrumbItem v-else>
                <NButton text size="tiny" @click="goTo(TRASH_ROOT_LOCATION)">{{ t('places.trash') }}</NButton>
              </NBreadcrumbItem>
              <NBreadcrumbItem v-for="segment in displaySegments" :key="segment.path">
                <NButton text size="tiny" @click="goTo(segment.path)">{{ segment.name }}</NButton>
              </NBreadcrumbItem>
            </NBreadcrumb>
          </div>
          <div class="primary-actions">
            <NButton
              v-if="!fs.isTrash && !fs.searchMode"
              size="small"
              :aria-label="t('toolbar.new_folder')"
              :title="t('toolbar.new_folder')"
              @click.stop="fs.createFolder()"
            >
              <template #icon><IconFolderPlusOutline /></template>
              <span class="action-label">{{ t('toolbar.new_folder') }}</span>
            </NButton>
            <NButton
              v-if="!fs.isTrash && !fs.searchMode"
              type="primary"
              size="small"
              :aria-label="t('toolbar.upload')"
              :title="t('toolbar.upload')"
              @click.stop="triggerUpload"
            >
              <template #icon><IconUpload /></template>
              <span class="action-label">{{ t('toolbar.upload') }}</span>
            </NButton>
            <NButton
              v-if="fs.isTrashRoot && !fs.searchMode"
              type="error"
              secondary
              size="small"
              :aria-label="t('menu.empty_trash')"
              :title="t('menu.empty_trash')"
              @click="fs.emptyTrash()"
            >
              <template #icon><IconDeleteOutline /></template>
              <span class="action-label">{{ t('menu.empty_trash') }}</span>
            </NButton>
            <NButton
              v-if="!fs.searchMode && directoryFiles.length"
              size="small"
              class="select-mode-toggle"
              :type="fs.selectMode ? 'primary' : 'default'"
              :secondary="fs.selectMode"
              :aria-label="fs.selectMode ? t('files.clear_selection') : t('files.select_multiple')"
              :aria-pressed="fs.selectMode"
              :title="fs.selectMode ? t('files.clear_selection') : t('files.select_multiple')"
              @click.stop="toggleSelectMode"
            >
              <template #icon><IconCheckboxMultipleMarkedOutline /></template>
              <span class="action-label">{{ t('files.select_multiple') }}</span>
            </NButton>
          </div>
          <NButton
            v-if="!fs.searchMode && !selectionCount && !fs.selectMode && directoryFiles.length"
            quaternary
            size="small"
            class="mobile-select-entry"
            @click.stop="enterSelectMode()"
          >
            <template #icon><IconCheckboxMultipleMarkedOutline /></template>
            {{ t('files.select_multiple') }}
          </NButton>
        </div>

        <div class="content-heading">
          <div>
            <div class="eyebrow">{{ fs.searchMode ? t('files.searching_for', { query: searchText }) : t('files.location') }}</div>
            <h1>{{ currentTitle }}</h1>
            <p v-if="!fs.loading">
              {{ fs.selectMode && isMobile ? t('files.selection_count', { n: selectionCount }) : t('files.item_summary', { folders: foldersCount, files: filesCount }) }}
            </p>
          </div>
          <div v-if="!isMobile && !fs.searchMode && (selectionCount || fs.selectMode)" class="selection-controls">
            <span class="selection-count">{{ t('files.selection_count', { n: selectionCount }) }}</span>
            <NButton v-if="selectionCount === 1" quaternary size="small" @click="openSelected">
              <template #icon><IconArrowRight /></template><span class="action-label">{{ t('menu.open') }}</span>
            </NButton>
            <NButton quaternary size="small" :disabled="selectionCount === directoryFiles.length" @click="fs.selectAll()">
              <template #icon><IconCheckboxMultipleMarkedOutline /></template><span class="action-label">{{ t('menu.select_all') }}</span>
            </NButton>
            <NButton quaternary size="small" :disabled="!selectionCount" @click="fs.copySelected(); message.success(t('files.copied'))">
              <template #icon><IconContentCopy /></template><span class="action-label">{{ t('menu.copy') }}</span>
            </NButton>
            <NButton v-if="!fs.isTrash" quaternary size="small" :disabled="!selectionCount" @click="fs.cutSelected(); message.success(t('files.cut'))">
              <template #icon><IconContentCut /></template><span class="action-label">{{ t('menu.cut') }}</span>
            </NButton>
            <NButton v-else quaternary size="small" :disabled="!selectionCount" @click="fs.restoreSelected()">
              <template #icon><IconRestore /></template><span class="action-label">{{ t('menu.restore') }}</span>
            </NButton>
            <NButton v-if="selectionCount === 1" quaternary size="small" @click="showDetails(selectedItem!)">
              <template #icon><IconInformationOutline /></template><span class="action-label">{{ t('menu.details') }}</span>
            </NButton>
            <NButton quaternary type="error" size="small" :disabled="!selectionCount" @click="fs.deleteSelected()">
              <template #icon><IconDeleteOutline /></template><span class="action-label">{{ fs.isTrash ? t('menu.permanent_delete') : t('menu.delete') }}</span>
            </NButton>
            <NButton quaternary circle size="small" :aria-label="t('files.clear_selection')" @click="fs.exitSelectMode()">
              <template #icon><IconClose /></template>
            </NButton>
          </div>
          <div v-else-if="!fs.selectMode" class="view-controls">
            <div class="sort-control">
              <span>{{ t('toolbar.sort') }}</span>
              <NSelect v-model:value="fs.sortBy" size="small" :options="sortOptions" :consistent-menu-width="false" />
            </div>
            <NButton quaternary circle size="small" class="sort-order-button" :title="fs.sortOrder === 'asc' ? t('files.sort_ascending') : t('files.sort_descending')" @click="toggleSortOrder">
              <template #icon>
                <IconSortAscending v-if="fs.sortOrder === 'asc'" />
                <IconSortDescending v-else />
              </template>
            </NButton>
            <NButtonGroup class="view-switch" role="group" aria-label="View mode">
              <NButton :type="fs.viewMode === 'icons' ? 'primary' : 'default'" :secondary="fs.viewMode === 'icons'" size="small" :title="t('toolbar.view_icons')" @click="fs.viewMode = 'icons'">
                <template #icon><IconViewGridOutline /></template>
              </NButton>
              <NButton :type="fs.viewMode !== 'icons' ? 'primary' : 'default'" :secondary="fs.viewMode !== 'icons'" size="small" :title="t('toolbar.view_details')" @click="fs.viewMode = 'list'">
                <template #icon><IconViewListOutline /></template>
              </NButton>
            </NButtonGroup>
            <NButton quaternary circle size="small" class="refresh-button" :title="t('toolbar.refresh')" @click="fs.refresh()">
              <template #icon><IconRefresh /></template>
            </NButton>
          </div>
        </div>

        <NAlert v-if="hasClipboard && !fs.isTrash && !fs.searchMode" class="clipboard-banner" type="info" :show-icon="false">
          <div class="clipboard-banner__content">
            <span>{{ t('files.clipboard_ready', { n: fs.clipboard.items.length }) }}</span>
            <NButton text type="primary" size="small" @click="fs.paste()">{{ t('menu.paste') }}</NButton>
            <NButton text circle size="small" class="icon-only" @click="fs.clipboard = { items: [], mode: null }">
              <template #icon><IconClose /></template>
            </NButton>
          </div>
        </NAlert>

        <div
          ref="fileSurface"
          class="file-surface"
          :class="{
            'is-grid': fs.viewMode === 'icons',
            'selection-mode': fs.selectMode,
            'rubber-banding': rubberBand.active,
          }"
          @click="handleSurfaceClick"
          @pointerdown="beginRubberBand"
          @pointermove="moveRubberBand"
          @pointerup="endRubberBand"
          @pointercancel="endRubberBand"
        >
          <div v-if="fs.loading || fs.searchLoading" class="surface-state" data-state="loading">
            <NSpin size="large" />
            <strong>{{ fs.searchMode ? t('files.searching') : t('files.loading') }}</strong>
          </div>
          <div v-else-if="fs.error" class="surface-state" data-state="error">
            <NResult status="error" :title="t('files.load_failed')" :description="fs.error">
              <template #footer><NButton @click="fs.refresh()">{{ t('toolbar.refresh') }}</NButton></template>
            </NResult>
          </div>
          <div v-else-if="directoryFiles.length === 0" class="surface-state" data-state="empty">
            <NEmpty :description="fs.searchMode ? t('files.no_search_results') : fs.isTrash ? t('files.trash_empty') : t('fileview.empty')">
              <template #extra>
                <div class="empty-state-extra">
                  <span>{{ fs.searchMode ? t('files.search_hint') : fs.isTrash ? t('files.trash_empty_hint') : t('files.empty_hint') }}</span>
                  <NButton v-if="!fs.searchMode && !fs.isTrash" type="primary" secondary @click="triggerUpload">
                    <template #icon><IconUpload /></template>{{ t('toolbar.upload') }}
                  </NButton>
                </div>
              </template>
            </NEmpty>
          </div>

          <div v-else-if="fs.viewMode === 'icons'" class="file-grid" role="list">
            <article
              v-for="file in directoryFiles"
              :key="file.path"
              class="file-card file-item"
              :class="{ selected: fs.selectedFiles.includes(file.path), cut: fs.clipboard.mode === 'cut' && fs.clipboard.items.some(item => item.path === file.path) }"
              :data-path="file.path"
              role="listitem"
              tabindex="0"
              @click.stop="handleItemClick(file, $event)"
              @dblclick.stop="openItem(file)"
              @contextmenu.prevent.stop="handleContextMenu(file, $event)"
              @pointerdown="beginLongPress(file, $event)"
              @pointerup="endLongPress"
              @pointercancel="endLongPress"
              @pointermove="endLongPress"
              @keydown.enter="openItem(file)"
            >
              <div class="file-card__preview">
                <img v-if="showThumbnails && file.thumbnail_url" :src="file.thumbnail_url" class="file-thumbnail" alt="" draggable="false" />
                <component :is="getFileIcon(file.name, file.is_dir)" v-else width="56" height="56" />
                <span
                  v-if="fs.selectMode || fs.selectedFiles.includes(file.path)"
                  class="selection-check"
                  :class="{ 'is-empty': !fs.selectedFiles.includes(file.path) }"
                >
                  <IconCheck v-if="fs.selectedFiles.includes(file.path)" />
                </span>
                <NTag v-if="file.status && file.status !== 'ready'" class="file-status" size="tiny" round type="info">
                  {{ phaseLabel(file.task_phase || file.status) }}
                </NTag>
              </div>
              <div class="file-card__copy">
                <strong class="file-name" :title="file.name">{{ file.name }}</strong>
                <span>{{ file.is_dir ? t('info.directory') : formatSize(file.size) }}</span>
                <span v-if="fs.isTrashRoot && !fs.searchMode">{{ t('info.deleted') }} {{ displayDate(file.last_modified) }}</span>
              </div>
              <NButton quaternary circle size="tiny" class="more-button" :aria-label="t('common.more')" @click.stop="openActionMenu(file, $event)">
                <template #icon><IconDotsHorizontal /></template>
              </NButton>
            </article>
          </div>

          <div v-else class="file-list" role="table">
            <div class="file-list__head" role="row">
              <span>{{ t('fileview.col_name') }}</span>
              <span>{{ t('info.type') }}</span>
              <span>{{ t('fileview.col_size') }}</span>
              <span>{{ t('fileview.col_modified') }}</span>
              <span />
            </div>
            <div
              v-for="file in directoryFiles"
              :key="file.path"
              class="file-row file-item"
              :class="{ selected: fs.selectedFiles.includes(file.path), cut: fs.clipboard.mode === 'cut' && fs.clipboard.items.some(item => item.path === file.path) }"
              :data-path="file.path"
              role="row"
              tabindex="0"
              @click.stop="handleItemClick(file, $event)"
              @dblclick.stop="openItem(file)"
              @contextmenu.prevent.stop="handleContextMenu(file, $event)"
              @pointerdown="beginLongPress(file, $event)"
              @pointerup="endLongPress"
              @pointercancel="endLongPress"
              @pointermove="endLongPress"
              @keydown.enter="openItem(file)"
            >
              <div class="file-row__name" role="cell">
                <span class="file-row__icon">
                  <img v-if="showThumbnails && file.thumbnail_url" :src="file.thumbnail_url" class="file-thumbnail" alt="" draggable="false" />
                  <component :is="getFileIcon(file.name, file.is_dir)" v-else />
                </span>
                <span>
                  <strong class="file-name">{{ file.name }}</strong>
                  <small class="mobile-meta">{{ displayDate(file.last_modified) }}</small>
                </span>
                <span
                  v-if="fs.selectMode || fs.selectedFiles.includes(file.path)"
                  class="selection-check"
                  :class="{ 'is-empty': !fs.selectedFiles.includes(file.path) }"
                >
                  <IconCheck v-if="fs.selectedFiles.includes(file.path)" />
                </span>
              </div>
              <span role="cell">{{ file.is_dir ? t('info.directory') : (file.content_type || t('info.file')) }}</span>
              <span role="cell">{{ file.is_dir ? '—' : formatSize(file.size) }}</span>
              <span role="cell">{{ displayDate(file.last_modified) }}</span>
              <NButton quaternary circle size="tiny" class="more-button" :aria-label="t('common.more')" @click.stop="openActionMenu(file, $event)">
                <template #icon><IconDotsHorizontal /></template>
              </NButton>
            </div>
          </div>

          <div v-if="rubberBand.active" class="rubber-band" :style="rubberBandStyle" aria-hidden="true" />
        </div>

        <footer class="file-statusbar">
          <span>{{ t('files.item_summary', { folders: foldersCount, files: filesCount }) }}</span>
        </footer>
      </section>
    </main>

    <nav class="mobile-bottom-nav" :class="{ 'is-selection': fs.selectMode }" :aria-label="fs.selectMode ? t('files.select_multiple') : 'File locations'">
      <template v-if="fs.selectMode">
        <NButton quaternary :disabled="!directoryFiles.length || selectionCount === directoryFiles.length" @click.stop="fs.selectAll()">
          <IconCheckboxMultipleMarkedOutline /><span>{{ t('menu.select_all') }}</span>
        </NButton>
        <NButton quaternary :disabled="!selectionCount" @click.stop="fs.copySelected(); message.success(t('files.copied'))">
          <IconContentCopy /><span>{{ t('mobile.selection.copy') }}</span>
        </NButton>
        <NButton v-if="!fs.isTrash" quaternary :disabled="!selectionCount" @click.stop="fs.cutSelected(); message.success(t('files.cut'))">
          <IconContentCut /><span>{{ t('mobile.selection.cut') }}</span>
        </NButton>
        <NButton v-else quaternary :disabled="!selectionCount" @click.stop="fs.restoreSelected()">
          <IconRestore /><span>{{ t('menu.restore') }}</span>
        </NButton>
        <NButton quaternary type="error" class="mobile-selection-delete" :disabled="!selectionCount" @click.stop="fs.deleteSelected()">
          <IconDeleteOutline /><span>{{ fs.isTrash ? t('menu.permanent_delete') : t('mobile.selection.delete') }}</span>
        </NButton>
        <NButton quaternary @click.stop="fs.exitSelectMode()">
          <IconCheck /><span>{{ t('mobile.selection.done') }}</span>
        </NButton>
      </template>
      <template v-else>
        <NButton quaternary :class="{ active: activePlace === 'files' }" @click.stop="choosePlace('files')">
          <IconFolderHome /><span>{{ t('files.my_files') }}</span>
        </NButton>
        <NDropdown
          trigger="click"
          placement="top"
          :options="createOptions"
          :menu-props="() => ({ role: 'menu' })"
          :node-props="dropdownNodeProps"
          @select="handleCreateSelect"
        >
          <NButton circle type="primary" class="mobile-create" :disabled="fs.isTrash || fs.searchMode" @click.stop>
            <template #icon><IconPlus /></template>
          </NButton>
        </NDropdown>
        <NButton quaternary :class="{ active: activePlace === 'trash' }" @click.stop="choosePlace('trash')">
          <IconDeleteOutline /><span>{{ t('places.trash') }}</span>
        </NButton>
      </template>
    </nav>

    <NDropdown
      :key="actionMenuEpoch"
      :show="showActionMenu"
      trigger="manual"
      placement="bottom-start"
      :x="actionMenuX"
      :y="actionMenuY"
      :options="actionMenuOptions"
      :menu-props="() => ({ class: 'action-menu', role: 'menu' })"
      :node-props="dropdownNodeProps"
      @select="handleActionSelect"
      @clickoutside="showActionMenu = false"
    />

    <NDrawer
      v-if="isMobile"
      :show="showActivity"
      placement="bottom"
      :height="mobileActivityDrawerHeight"
      content-class="mobile-activity-drawer"
      :content-style="{ borderRadius: '20px 20px 0 0', overflow: 'hidden' }"
      @update:show="showActivity = $event"
    >
      <NDrawerContent
        :title="t('files.activity')"
        closable
        :body-content-style="{ padding: '0 20px max(20px, env(safe-area-inset-bottom))', overflow: 'hidden' }"
      >
        <ActivityCenter
          :uploads="upload.uploads"
          :server-tasks="serverTasks"
          :pending-count="pendingOps.pendingCount"
          :activity-label="activityLabel"
          :show-heading="false"
          :max-height="mobileActivityListHeight"
          @cancel-upload="upload.cancelUpload"
        />
      </NDrawerContent>
    </NDrawer>

    <NDrawer
      :show="showInspector && Boolean(detailsFile)"
      :placement="isMobile ? 'bottom' : 'right'"
      :width="isMobile ? undefined : 380"
      :height="isMobile ? '78vh' : undefined"
      @update:show="showInspector = $event"
    >
      <NDrawerContent v-if="detailsFile" class="inspector" :title="detailsFile.name" closable>
        <div class="inspector__preview">
          <img v-if="showThumbnails && detailsFile.thumbnail_url" :src="detailsFile.thumbnail_url" alt="" />
          <component :is="getFileIcon(detailsFile.name, detailsFile.is_dir)" v-else />
        </div>
        <NDescriptions :column="1" label-placement="top" bordered size="small">
          <NDescriptionsItem :label="t('info.type')">{{ detailsFile.is_dir ? t('info.directory') : (detailsFile.content_type || t('info.file')) }}</NDescriptionsItem>
          <NDescriptionsItem :label="t('info.size')">{{ detailsFile.is_dir ? '—' : formatSize(detailsFile.size) }}</NDescriptionsItem>
          <NDescriptionsItem :label="t('info.modified')">{{ displayDate(detailsFile.last_modified) }}</NDescriptionsItem>
          <NDescriptionsItem :label="t('info.created')">{{ displayDate(detailsFile.created_at) }}</NDescriptionsItem>
          <NDescriptionsItem v-if="detailsFile.deleted_at" :label="t('info.deleted')">{{ displayDate(detailsFile.deleted_at) }}</NDescriptionsItem>
          <NDescriptionsItem v-if="detailsFile.media_width" :label="t('info.dimensions')">{{ detailsFile.media_width }} × {{ detailsFile.media_height }}</NDescriptionsItem>
          <NDescriptionsItem :label="detailsFile.original_path ? t('info.original_path') : t('files.path')">
            <span class="path-value">{{ detailsFile.original_path || detailsFile.path }}</span>
          </NDescriptionsItem>
        </NDescriptions>
        <template #footer>
          <div class="inspector__actions">
            <NButton v-if="!detailsFile.is_dir" @click="downloadItem(detailsFile)">
              <template #icon><IconDownload /></template>{{ t('menu.download') }}
            </NButton>
            <NButton type="primary" @click="openItem(detailsFile)">
              <template #icon><IconArrowRight /></template>{{ t('menu.open') }}
            </NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>

    <div v-if="draggingFiles" class="drop-overlay">
      <div><IconUpload /><strong>{{ t('fileview.drop') }}</strong><span>{{ t('files.drop_hint') }}</span></div>
    </div>

    <input ref="uploadInput" class="upload-input" type="file" multiple @change="handleUploadSelection" />
  </div>
</template>

<style lang="scss" scoped>
.file-shell {
  --ink: #172033;
  --muted: #657087;
  --line: #e4e8f0;
  --surface: #fff;
  --canvas: #f4f6fa;
  --accent: #4f5fe7;
  --accent-soft: #e9edff;
  height: 100dvh;
  overflow: hidden;
  color: var(--ink);
  background: var(--canvas);
  font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 14px;
  user-select: none;
}

button, input, select { font: inherit; }

.file-sidebar {
  position: fixed;
  inset: 0 auto 0 0;
  z-index: 30;
  display: flex;
  width: 260px;
  flex-direction: column;
  padding: 28px 18px 18px;
  border-right: 1px solid var(--line);
  background: rgba(252, 253, 255, .96);
  box-shadow: 8px 0 28px rgb(26 36 58 / 2.5%);
  backdrop-filter: blur(20px);
}

.brand-lockup, .mobile-brand { display: flex; align-items: center; gap: 12px; padding: 0 8px; }
.brand-mark {
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  border-radius: 13px;
  color: #fff;
  background: linear-gradient(145deg, #6574f0, #4352d0);
  box-shadow: 0 10px 22px rgb(79 95 231 / 24%);
  font-size: 19px;
  font-weight: 800;
}
.brand-name { font-size: 16px; font-weight: 800; letter-spacing: .14em; }
.brand-caption { margin-top: 3px; color: #68748a; font-size: 10px; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }

.place-list { margin-top: 44px; }
.place-list :deep(.n-menu-item-content) { border-radius: 12px; }
.place-list :deep(.n-menu-item-content-header) { font-weight: 620; }

.sidebar-account {
  width: 100%;
  height: 58px;
  margin-top: auto;
  padding: 8px 10px;
  border: 1px solid transparent;
  border-radius: 13px;
  text-align: left;
}
.sidebar-account:hover { border-color: #e6e9f0; background: #fff; box-shadow: 0 5px 16px rgb(26 36 58 / 5%); }
.sidebar-account :deep(.n-button__content) { display: grid; width: 100%; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 10px; }
.sidebar-account img, .mobile-account img { width: 36px; height: 36px; border-radius: 50%; object-fit: cover; }
.sidebar-account span { min-width: 0; }
.sidebar-account strong, .sidebar-account small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sidebar-account strong { font-size: 13px; font-weight: 680; }
.sidebar-account small { margin-top: 2px; color: var(--muted); font-size: 11px; }

.file-main { height: 100dvh; margin-left: 260px; overflow: hidden; }
.file-header {
  position: sticky;
  top: 0;
  z-index: 25;
  display: flex;
  height: 76px;
  align-items: center;
  justify-content: flex-start;
  padding: 0 32px;
  border-bottom: 1px solid var(--line);
  background: rgba(250, 251, 253, .88);
  backdrop-filter: blur(20px);
}
.mobile-brand { display: none; }
.global-search {
  width: min(620px, 58vw);
  height: 42px;
  box-shadow: 0 4px 16px rgb(24 36 59 / 4%);
}
.global-search :deep(.n-input__input-el) { font-size: 14px; }
.global-search kbd { padding: 3px 8px; border: 1px solid #e2e6ee; border-radius: 6px; color: #657087; background: #f7f8fb; font-size: 11px; }
.header-actions { position: absolute; right: 32px; display: flex; align-items: center; gap: 10px; }
.activity-button, .mobile-account {
  position: relative;
  display: grid;
  width: 42px;
  height: 42px;
  border: 1px solid #dfe4ed;
  place-items: center;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 3px 10px rgb(25 35 58 / 4%);
  cursor: pointer;
}
.mobile-account { display: none; border: 0; background: transparent; }

.file-workspace { display: flex; height: calc(100dvh - 76px); min-height: 0; flex-direction: column; padding: 0 32px; }
.location-toolbar { display: grid; min-height: 68px; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 18px; border-bottom: 1px solid var(--line); }
.history-buttons { display: flex; }
.history-buttons svg, .view-controls svg { width: 18px; height: 18px; }
.breadcrumbs-viewport { min-width: 0; overflow: hidden; }
.breadcrumbs { display: flex; min-width: 0; align-items: center; gap: 5px; overflow: hidden; color: #9aa1b0; }
.breadcrumbs :deep(.n-breadcrumb-item__link) { overflow: hidden; max-width: 180px; }
.breadcrumbs :deep(.n-button) { max-width: 180px; color: #5d687e; font-size: 13px; font-weight: 650; }
.breadcrumbs :deep(.n-button__content) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.primary-actions { display: flex; gap: 10px; }
.mobile-select-entry { display: none; }

.content-heading { display: flex; align-items: end; justify-content: space-between; gap: 24px; padding: 28px 0 20px; }
.eyebrow { margin-bottom: 7px; color: var(--accent); font-size: 11px; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
.content-heading h1 { margin: 0; font-size: 30px; font-weight: 720; line-height: 1.1; letter-spacing: -.04em; }
.content-heading p { margin: 8px 0 0; color: var(--muted); font-size: 13px; }
.view-controls { display: flex; align-items: center; gap: 7px; }
.sort-control { display: flex; align-items: center; gap: 9px; color: var(--muted); font-size: 12px; }
.sort-control .n-select { width: 122px; }
.selection-controls {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: flex-end;
  gap: 3px;
  font-size: 13px;
}
.selection-controls svg { width: 16px; height: 16px; }
.selection-count {
  flex: 0 0 auto;
  margin-right: 5px;
  padding: 7px 11px;
  border-radius: 10px;
  color: #4050cd;
  background: #e9edff;
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}
.clipboard-banner { margin-bottom: 14px; border-radius: 13px; }
.clipboard-banner__content { display: flex; align-items: center; gap: 10px; }
.clipboard-banner__content > :first-child { margin-right: auto; }

.file-surface {
  position: relative;
  min-height: 0;
  flex: 1;
  overflow: auto;
  padding: 20px;
  border: 1px solid var(--line);
  border-radius: 18px;
  background: rgb(255 255 255 / 82%);
  box-shadow: 0 8px 30px rgb(26 36 58 / 4%);
}
.file-surface.rubber-banding { cursor: crosshair; user-select: none; }
.rubber-band {
  position: absolute;
  z-index: 4;
  border: 1px solid rgb(79 95 231 / 72%);
  border-radius: 3px;
  background: rgb(79 95 231 / 13%);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 38%);
  pointer-events: none;
}
.file-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(172px, 192px)); align-content: start; gap: 16px; }
.file-card {
  position: relative;
  min-width: 0;
  padding: 9px;
  border: 1px solid #e6eaf1;
  border-radius: 16px;
  background: #fff;
  box-shadow: 0 3px 12px rgb(25 35 58 / 3%);
  cursor: default;
  outline: none;
  transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease, background .16s ease;
}
.file-card:hover { border-color: #cfd5e3; box-shadow: 0 10px 26px rgb(25 35 58 / 9%); transform: translateY(-2px); }
.file-card:focus-visible { box-shadow: 0 0 0 3px rgb(79 95 231 / 16%); }
.file-card.selected { border-color: #97a4f6; background: #f6f7ff; box-shadow: 0 0 0 2px rgb(79 95 231 / 10%); }
.file-card.cut, .file-row.cut { opacity: .48; }
.file-card__preview {
  position: relative;
  display: grid;
  height: 128px;
  place-items: center;
  overflow: hidden;
  border: 1px solid #edf0f5;
  border-radius: 12px;
  color: #5c6de4;
  background: linear-gradient(145deg, #f2f4f8, #e9edf4);
}
.file-card__preview img { width: 100%; height: 100%; object-fit: cover; }
.file-card__copy { padding: 12px 30px 6px 4px; }
.file-card__copy strong, .file-card__copy span { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-card__copy strong { font-size: 13px; font-weight: 680; }
.file-card__copy span { margin-top: 5px; color: var(--muted); font-size: 11px; }
.more-button { display: grid; width: 30px; height: 30px; padding: 0; border: 0; place-items: center; border-radius: 9px; color: #5f6b82; background: transparent; cursor: pointer; }
.more-button:hover { color: var(--ink); background: #edf0f5; }
.file-card > .more-button { position: absolute; right: 8px; bottom: 9px; opacity: .75; transition: opacity .16s ease; }
.file-card:hover > .more-button,
.file-card.selected > .more-button,
.file-card:focus-within > .more-button { opacity: 1; }
.more-button svg { width: 17px; height: 17px; }
.selection-check { position: absolute; top: 9px; right: 9px; display: grid; width: 24px; height: 24px; place-items: center; border: 2px solid #fff; border-radius: 50%; color: #fff; background: var(--accent); box-shadow: 0 3px 8px rgb(38 48 120 / 24%); }
.selection-check svg { width: 14px; height: 14px; }
.selection-check.is-empty { border-color: #9da7ba; background: rgb(255 255 255 / 94%); box-shadow: 0 2px 7px rgb(38 48 120 / 10%); }
.file-surface.selection-mode .file-card,
.file-surface.selection-mode .file-row { cursor: pointer; }
.file-status { position: absolute; right: 7px; bottom: 7px; }

.file-list { overflow: hidden; border: 1px solid var(--line); border-radius: 14px; background: #fff; }
.file-list__head, .file-row { display: grid; grid-template-columns: minmax(240px, 2fr) minmax(120px, 1fr) 100px 145px 38px; align-items: center; gap: 12px; }
.file-list__head { min-height: 44px; padding: 0 16px; border-bottom: 1px solid var(--line); color: #626d82; background: #f7f8fb; font-size: 11px; font-weight: 720; letter-spacing: .04em; text-transform: uppercase; }
.file-row { min-height: 64px; padding: 0 16px; border-bottom: 1px solid #edf0f4; color: #667087; outline: 0; font-size: 12px; }
.file-row:last-child { border-bottom: 0; }
.file-row:hover { background: #f8f9fc; }
.file-row.selected { background: var(--accent-soft); }
.file-row__name { position: relative; display: flex; min-width: 0; align-items: center; gap: 11px; color: var(--ink); }
.file-row__name > span:nth-child(2) { min-width: 0; }
.file-row__name strong { display: block; overflow: hidden; font-size: 13px; font-weight: 660; text-overflow: ellipsis; white-space: nowrap; }
.file-row__icon { display: grid; width: 40px; height: 40px; flex: 0 0 auto; place-items: center; overflow: hidden; border-radius: 11px; color: #5c6de4; background: #eef1f6; }
.file-row__icon svg { width: 23px; height: 23px; }
.file-row__icon img { width: 100%; height: 100%; object-fit: cover; }
.file-row .selection-check { position: static; margin-left: auto; flex: 0 0 auto; }
.mobile-meta { display: none; }

.surface-state { display: flex; min-height: 100%; align-items: center; justify-content: center; flex-direction: column; gap: 10px; color: var(--muted); text-align: center; }
.surface-state strong { color: var(--ink); font-size: 16px; }
.surface-state > span { max-width: 360px; font-size: 13px; line-height: 1.55; }
.empty-state-extra { display: flex; align-items: center; flex-direction: column; gap: 16px; color: var(--muted); font-size: 13px; }

.file-statusbar { display: flex; min-height: 42px; align-items: center; justify-content: space-between; color: #657087; font-size: 11px; }

.mobile-bottom-nav { display: none; }
.upload-input { display: none; }

.inspector__preview { display: grid; height: 210px; place-items: center; overflow: hidden; margin: 22px 0; border: 1px solid #e8ebf1; border-radius: 16px; color: var(--accent); background: linear-gradient(145deg, #f4f5f8, #e9edf3); }
.inspector__preview svg { width: 68px; height: 68px; }
.inspector__preview img { width: 100%; height: 100%; object-fit: contain; }
.path-value { font-family: ui-monospace, monospace; }
.inspector__actions { display: flex; justify-content: flex-end; gap: 8px; }

.drop-overlay { position: fixed; inset: 14px; z-index: 100; display: grid; border: 2px dashed #8f9cf4; place-items: center; border-radius: 24px; background: rgb(243 245 255 / 94%); box-shadow: inset 0 0 0 8px rgb(255 255 255 / 52%); backdrop-filter: blur(14px); pointer-events: none; }
.drop-overlay > div { display: flex; align-items: center; flex-direction: column; gap: 9px; color: var(--accent); }
.drop-overlay svg { width: 42px; height: 42px; }
.drop-overlay strong { color: var(--ink); font-size: 20px; }
.drop-overlay span { color: var(--muted); font-size: 13px; }

@media (max-width: 1150px) and (min-width: 768px) {
  .selection-controls .action-label { display: none; }
  .selection-controls :deep(.n-button:not(.n-button--circle)) { width: 36px; padding: 0; }
}

@media (max-width: 940px) and (min-width: 768px) {
  .primary-actions .action-label { display: none; }
  .primary-actions :deep(.n-button) { width: 36px; padding: 0; }
}

@media (max-width: 980px) {
  .file-sidebar { width: 220px; }
  .file-main { margin-left: 220px; }
  .file-workspace { padding: 0 22px; }
  .global-search { width: min(480px, 56vw); }
  .file-list__head, .file-row { grid-template-columns: minmax(200px, 2fr) 90px 125px 38px; }
  .file-list__head > :nth-child(2), .file-row > :nth-child(2) { display: none; }
}

@media (max-width: 767px) {
  .file-shell { --canvas: #f6f7fa; min-height: 100dvh; }
  .file-sidebar { display: none; }
  .file-main { height: 100dvh; margin-left: 0; }
  .file-header { height: auto; min-height: 72px; justify-content: space-between; gap: 12px; padding: max(12px, env(safe-area-inset-top)) 16px 11px; background: rgb(255 255 255 / 92%); }
  .mobile-brand { display: flex; flex: 0 0 auto; }
  .mobile-brand { padding: 0; }
  .mobile-brand .brand-mark { width: 38px; height: 38px; border-radius: 12px; font-size: 16px; }
  .mobile-brand strong { display: none; font-size: 14px; letter-spacing: .1em; }
  .global-search { order: 2; width: 100%; height: 44px; }
  .global-search kbd { display: none; }
  .header-actions { position: static; order: 3; gap: 4px; }
  .activity-button { width: 40px; height: 40px; border-color: #e1e5ed; background: #fff; }
  .mobile-account { display: grid; width: 40px; height: 40px; border: 1px solid #e1e5ed; background: #fff; }
  .file-workspace { height: calc(100dvh - 72px); min-height: 0; padding: 0 15px 92px; }
  .location-toolbar { min-height: 58px; grid-template-columns: auto minmax(0, 1fr) auto; gap: 9px; }
  .history-buttons button:nth-child(2) { display: none; }
  .history-buttons button { width: 36px; height: 36px; }
  .breadcrumbs-viewport {
    width: 100%;
    overflow-x: auto;
    overflow-y: hidden;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
    overscroll-behavior-inline: contain;
    scrollbar-width: none;
    touch-action: pan-x;
  }
  .breadcrumbs-viewport::-webkit-scrollbar { display: none; }
  .breadcrumbs-viewport.is-clipped-left:not(.is-clipped-right) {
    -webkit-mask-image: linear-gradient(to right, transparent, #000 14px, #000 100%);
    mask-image: linear-gradient(to right, transparent, #000 14px, #000 100%);
  }
  .breadcrumbs-viewport.is-clipped-right:not(.is-clipped-left) {
    -webkit-mask-image: linear-gradient(to right, #000 0, #000 calc(100% - 14px), transparent);
    mask-image: linear-gradient(to right, #000 0, #000 calc(100% - 14px), transparent);
  }
  .breadcrumbs-viewport.is-clipped-left.is-clipped-right {
    -webkit-mask-image: linear-gradient(to right, transparent, #000 14px, #000 calc(100% - 14px), transparent);
    mask-image: linear-gradient(to right, transparent, #000 14px, #000 calc(100% - 14px), transparent);
  }
  .breadcrumbs { width: max-content; min-width: max-content; gap: 2px; overflow: visible; }
  .breadcrumbs :deep(.n-breadcrumb-item) { flex: 0 0 auto; }
  .breadcrumbs :deep(.n-breadcrumb-item__link),
  .breadcrumbs :deep(.n-button) { max-width: none; font-size: 12px; }
  .primary-actions { display: none; }
  .mobile-select-entry { display: flex; }
  .content-heading { align-items: center; padding: 21px 2px 16px; }
  .eyebrow { margin-bottom: 6px; font-size: 10px; }
  .content-heading h1 { font-size: 25px; }
  .content-heading p { margin-top: 6px; font-size: 12px; }
  .view-controls { gap: 3px; }
  .sort-control > span, .sort-order-button, .refresh-button { display: none; }
  .sort-control .n-select { width: 104px; }
  .clipboard-banner { font-size: 12px; }
  .clipboard-banner__content { gap: 6px; }
  .file-surface { padding: 0; border: 0; border-radius: 0; background: transparent; box-shadow: none; }
  .file-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
  .file-card { padding: 8px; border-radius: 15px; background: #fff; box-shadow: 0 5px 18px rgb(34 43 67 / 5%); transform: none; }
  .file-card:hover { transform: none; }
  .file-card__preview { height: auto; aspect-ratio: 1.18; }
  .file-card__copy { padding: 11px 24px 5px 2px; }
  .file-card__copy strong { font-size: 13px; }
  .file-card__copy span { font-size: 11px; }
  .file-card > .more-button { right: 5px; bottom: 5px; }
  .file-card > .more-button { opacity: .82; }
  .file-list { border: 0; border-radius: 0; background: transparent; }
  .file-list__head { display: none; }
  .file-row { grid-template-columns: minmax(0, 1fr) auto; min-height: 68px; padding: 6px 4px; border-bottom-color: #e6eaf0; font-size: 0; }
  .file-row > :nth-child(2), .file-row > :nth-child(3), .file-row > :nth-child(4) { display: none; }
  .file-row__icon { width: 44px; height: 44px; border-radius: 12px; }
  .file-row__name strong { font-size: 13px; }
  .mobile-meta { display: block; margin-top: 4px; color: var(--muted); font-size: 11px; font-weight: 400; }
  .surface-state { min-height: 330px; }
  .file-statusbar { display: none; }
  .mobile-bottom-nav {
    position: fixed;
    left: 10px;
    right: 10px;
    bottom: max(10px, env(safe-area-inset-bottom));
    z-index: 40;
    display: grid;
    height: 68px;
    grid-template-columns: 1fr 64px 1fr;
    align-items: center;
    border: 1px solid rgba(224, 227, 236, .9);
    border-radius: 21px;
    background: rgba(255, 255, 255, .94);
    box-shadow: 0 14px 42px rgba(33, 42, 64, .16);
    backdrop-filter: blur(20px);
  }
  .mobile-bottom-nav button { display: flex; height: 100%; align-items: center; justify-content: center; border: 0; color: #657187; background: transparent; font-size: 10px; font-weight: 700; }
  .mobile-bottom-nav button :deep(.n-button__content) { flex-direction: column; gap: 4px; }
  .mobile-bottom-nav button svg { width: 21px; height: 21px; }
  .mobile-bottom-nav button.active { color: var(--accent); }
  .mobile-bottom-nav.is-selection { grid-template-columns: repeat(5, minmax(0, 1fr)); }
  .mobile-bottom-nav.is-selection button { min-width: 0; padding: 0 3px; }
  .mobile-bottom-nav.is-selection .mobile-selection-delete { color: var(--domus-danger); }
  .mobile-bottom-nav .mobile-create { width: 52px; height: 52px; place-self: center; border-radius: 17px; color: #fff; background: var(--accent); box-shadow: 0 8px 18px rgba(85, 104, 232, .3); }
  .mobile-bottom-nav .mobile-create:disabled { opacity: .38; }
  .mobile-bottom-nav .mobile-create svg { width: 25px; height: 25px; }
  .inspector__preview { height: 150px; margin: 16px 0; }
}

@media (max-width: 420px) {
  .file-card__preview { aspect-ratio: 1.08; }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
}
</style>
