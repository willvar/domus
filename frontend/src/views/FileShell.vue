<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import {
  IconAccountCircle,
  IconArrowLeft,
  IconArrowRight,
  IconArrowUp,
  IconCheck,
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
import { useMessage } from '../composables/useMessage'
import { showConfirm, showPrompt } from '../composables/useNativeDialog'
import api from '../composables/useApi'
import { useAuthStore } from '../stores/auth'
import { useFileSystemStore } from '../stores/fileSystem'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useTasksStore } from '../stores/tasks'
import { useUploadStore } from '../stores/upload'
import type { FileListItem } from '../types'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const fs = useFileSystemStore()
const upload = useUploadStore()
const tasks = useTasksStore()
const pendingOps = usePendingOpsStore()
const { isMobile, isTouchInput } = useDevice()
const { t } = useI18n()
const message = useMessage()

const uploadInput = ref<HTMLInputElement | null>(null)
const searchInput = ref<HTMLInputElement | null>(null)
const searchText = ref('')
const showAccount = ref(false)
const showActivity = ref(false)
const showInspector = ref(false)
const showThumbnails = ref(localStorage.getItem('domus_show_thumbnails') === '1')
const detailsFile = ref<FileListItem | null>(null)
const actionFile = ref<FileListItem | null>(null)
const actionMenuX = ref(0)
const actionMenuY = ref(0)
const createMenuOpen = ref(false)
const draggingFiles = ref(false)
let searchTimer: number | null = null
let longPressTimer: number | null = null
let suppressClickPath = ''

const homePath = '/'
const isHome = computed(() => fs.currentPath === homePath && !fs.searchMode)
const activePlace = computed(() => fs.isTrash ? 'trash' : 'files')
const currentTitle = computed(() => {
  if (fs.searchMode) return t('files.search_results')
  if (fs.isTrash) return t('places.trash')
  if (isHome.value) return t('files.my_files')
  const value = fs.currentPath.replace(/\/$/, '').split('/').pop()
  return value || t('files.my_files')
})
const directoryFiles = computed(() => fs.sortedFiles)
const foldersCount = computed(() => directoryFiles.value.filter(file => file.is_dir).length)
const filesCount = computed(() => directoryFiles.value.length - foldersCount.value)
const activeTaskIDs = computed(() => new Set(upload.uploads.map(item => item.taskId).filter(Boolean)))
const serverTasks = computed(() => tasks.tasks
  .filter(task => task.type === 'upload' && !activeTaskIDs.value.has(task.task_id))
  .slice(0, 20))
const activityCount = computed(() => upload.activeUploads.length + tasks.activeTasks.filter(task => !activeTaskIDs.value.has(task.task_id)).length + pendingOps.pendingCount)
const hasClipboard = computed(() => fs.clipboard.items.length > 0)
const selectionCount = computed(() => fs.selectedFiles.length)
const selectedItem = computed(() => {
  if (selectionCount.value !== 1) return null
  return directoryFiles.value.find(file => file.path === fs.selectedFiles[0]) || null
})
const displaySegments = computed(() => {
  if (fs.isTrash) return []
  return fs.pathSegments
})
const activityLabel = computed(() => {
  if (activityCount.value > 0) return t('files.activity_active', { n: activityCount.value })
  return t('files.activity_idle')
})

onMounted(async () => {
  if (fs.tabs.length === 0) {
    fs.init()
    const requested = typeof route.query.path === 'string' && route.query.path.startsWith('/')
      ? route.query.path
      : homePath
    fs.createTab(requested)
  }
  await Promise.allSettled([
    pendingOps.init(auth.username),
    tasks.fetchTasks(),
  ])
  window.addEventListener('keydown', handleKeyboard)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeyboard)
  if (searchTimer !== null) window.clearTimeout(searchTimer)
  if (longPressTimer !== null) window.clearTimeout(longPressTimer)
})

watch(() => fs.currentPath, (path) => {
  if (!path || fs.searchMode) return
  const queryPath = typeof route.query.path === 'string' ? route.query.path : ''
  if (queryPath !== path) void router.replace({ path: '/files', query: path === homePath ? {} : { path } })
})

watch(selectedItem, (file) => {
  if (showInspector.value) detailsFile.value = file
})

function closeFloatingPanels(): void {
  showAccount.value = false
  actionFile.value = null
  createMenuOpen.value = false
}

function toggleThumbnailDisplay(): void {
  showThumbnails.value = !showThumbnails.value
  localStorage.setItem('domus_show_thumbnails', showThumbnails.value ? '1' : '0')
}

async function goTo(path: string): Promise<void> {
  closeFloatingPanels()
  if (fs.searchMode) fs.exitSearch()
  searchText.value = ''
  await fs.navigate(path)
}

function parentPath(path: string): string {
  const trimmed = path.replace(/\/$/, '')
  const index = trimmed.lastIndexOf('/')
  return index <= 0 ? '/' : `${trimmed.slice(0, index)}/`
}

async function openItem(file: FileListItem): Promise<void> {
  closeFloatingPanels()
  if (file.is_dir) {
    await goTo(file.path)
    return
  }
  await router.push({
    path: '/preview',
    query: { path: file.path, name: file.name, from: route.fullPath },
  })
}

function handleItemClick(file: FileListItem, event: MouseEvent): void {
  if (suppressClickPath === file.path) {
    suppressClickPath = ''
    return
  }
  if (fs.searchMode || isMobile.value || isTouchInput.value) {
    if (fs.selectMode) fs.toggleSelect(file.path)
    else void openItem(file)
    return
  }
  fs.selectFile(file.path, event)
}

function beginLongPress(file: FileListItem, event: PointerEvent): void {
  if (event.pointerType !== 'touch' && event.pointerType !== 'pen') return
  if (longPressTimer !== null) window.clearTimeout(longPressTimer)
  longPressTimer = window.setTimeout(() => {
    suppressClickPath = file.path
    fs.enterSelectMode(file.path)
    navigator.vibrate?.(18)
  }, 520)
}

function endLongPress(): void {
  if (longPressTimer !== null) window.clearTimeout(longPressTimer)
  longPressTimer = null
}

function openActionMenu(file: FileListItem, event?: MouseEvent): void {
  if (!fs.searchMode && !fs.selectedFiles.includes(file.path)) fs.selectedFiles = [file.path]
  actionFile.value = file
  detailsFile.value = file
  createMenuOpen.value = false
  showAccount.value = false
  if (event) {
    actionMenuX.value = Math.min(event.clientX, window.innerWidth - 232)
    actionMenuY.value = Math.min(event.clientY, window.innerHeight - 360)
  }
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
  await api.delete('/file/delete', { params: { path: file.path } })
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
  void goTo(place === 'trash' ? '/__trash__/' : homePath)
}

async function logout(): Promise<void> {
  closeFloatingPanels()
  await auth.logout()
  await router.replace('/files')
}

function handleKeyboard(event: KeyboardEvent): void {
  const target = event.target as HTMLElement | null
  const typing = target?.matches('input, textarea, [contenteditable="true"]')
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') {
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
    void fs.deleteSelected()
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
  return dayjs(value).format('YYYY-MM-DD HH:mm')
}

function phaseLabel(phase?: string): string {
  const labels: Record<string, string> = {
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

      <nav class="place-list">
        <button class="place-item" :class="{ active: activePlace === 'files' }" data-place="files" @click.stop="choosePlace('files')">
          <IconFolderHome width="21" height="21" />
          <span>{{ t('files.my_files') }}</span>
        </button>
        <button class="place-item" :class="{ active: activePlace === 'trash' }" data-place="trash" @click.stop="choosePlace('trash')">
          <IconDeleteOutline width="21" height="21" />
          <span>{{ t('places.trash') }}</span>
        </button>
      </nav>

      <div class="security-note">
        <div class="security-note__icon"><IconCheck width="16" height="16" /></div>
        <div>
          <strong>{{ t('files.encrypted_title') }}</strong>
          <span>{{ t('files.encrypted_body') }}</span>
        </div>
      </div>

      <button class="sidebar-account" @click.stop="showAccount = !showAccount; showActivity = false">
        <img v-if="auth.user?.avatar_url" :src="auth.user.avatar_url" alt="" />
        <IconAccountCircle v-else width="34" height="34" />
        <span>
          <strong>{{ auth.user?.display_name || auth.username }}</strong>
          <small>@{{ auth.username }}</small>
        </span>
        <IconChevronDown width="18" height="18" />
      </button>
    </aside>

    <main class="file-main">
      <header class="file-header">
        <div class="mobile-brand">
          <div class="brand-mark">D</div>
          <strong>DOMUS</strong>
        </div>
        <label class="global-search">
          <IconMagnify width="20" height="20" />
          <input
            ref="searchInput"
            v-model="searchText"
            type="search"
            :placeholder="t('files.search_placeholder')"
            aria-label="Search files"
            @input="runSearch"
          />
          <button v-if="searchText" type="button" :aria-label="t('files.clear_search')" @click="clearSearch"><IconClose width="17" height="17" /></button>
          <kbd>Ctrl K</kbd>
        </label>
        <div class="header-actions">
          <button class="activity-button" :aria-label="activityLabel" @click.stop="showActivity = !showActivity; showAccount = false">
            <IconUpload width="20" height="20" />
            <span v-if="activityCount" class="activity-badge">{{ activityCount }}</span>
          </button>
          <button class="mobile-account" @click.stop="showAccount = !showAccount; showActivity = false">
            <img v-if="auth.user?.avatar_url" :src="auth.user.avatar_url" alt="" />
            <IconAccountCircle v-else width="34" height="34" />
          </button>
        </div>
      </header>

      <section class="file-workspace">
        <div class="location-toolbar">
          <div class="history-buttons">
            <button :disabled="!fs.canGoBack" :title="t('toolbar.back')" @click="fs.goBack()"><IconArrowLeft /></button>
            <button :disabled="!fs.canGoForward" :title="t('toolbar.forward')" @click="fs.goForward()"><IconArrowRight /></button>
            <button :disabled="!fs.canGoUp" :title="t('toolbar.up')" @click="fs.goUp()"><IconArrowUp /></button>
          </div>
          <div class="breadcrumbs" aria-label="Current path">
            <button @click="goTo(homePath)">{{ t('files.my_files') }}</button>
            <template v-for="segment in displaySegments" :key="segment.path">
              <span>/</span>
              <button @click="goTo(segment.path)">{{ segment.name }}</button>
            </template>
            <template v-if="fs.isTrash">
              <button @click="goTo('/__trash__/')">{{ t('places.trash') }}</button>
            </template>
          </div>
          <div class="primary-actions">
            <button v-if="!fs.isTrash && !fs.searchMode" class="quiet-action" @click.stop="fs.createFolder()">
              <IconFolderPlusOutline /> <span>{{ t('toolbar.new_folder') }}</span>
            </button>
            <button v-if="!fs.isTrash && !fs.searchMode" class="primary-action" @click.stop="triggerUpload">
              <IconUpload /> <span>{{ t('toolbar.upload') }}</span>
            </button>
            <button v-if="fs.isTrash" class="danger-action" @click="fs.emptyTrash()">
              <IconDeleteOutline /> <span>{{ t('menu.empty_trash') }}</span>
            </button>
          </div>
        </div>

        <div class="content-heading">
          <div>
            <div class="eyebrow">{{ fs.searchMode ? t('files.searching_for', { query: searchText }) : t('files.location') }}</div>
            <h1>{{ currentTitle }}</h1>
            <p v-if="!fs.loading">{{ t('files.item_summary', { folders: foldersCount, files: filesCount }) }}</p>
          </div>
          <div class="view-controls">
            <label>
              <span>{{ t('toolbar.sort') }}</span>
              <select v-model="fs.sortBy">
                <option value="name">{{ t('toolbar.sort_name') }}</option>
                <option value="date">{{ t('toolbar.sort_date') }}</option>
                <option value="size">{{ t('toolbar.sort_size') }}</option>
              </select>
            </label>
            <button :title="fs.sortOrder === 'asc' ? t('files.sort_ascending') : t('files.sort_descending')" @click="toggleSortOrder">
              <IconSortAscending v-if="fs.sortOrder === 'asc'" />
              <IconSortDescending v-else />
            </button>
            <div class="view-switch" role="group" aria-label="View mode">
              <button :class="{ active: fs.viewMode === 'icons' }" :title="t('toolbar.view_icons')" @click="fs.viewMode = 'icons'"><IconViewGridOutline /></button>
              <button :class="{ active: fs.viewMode !== 'icons' }" :title="t('toolbar.view_details')" @click="fs.viewMode = 'list'"><IconViewListOutline /></button>
            </div>
            <button :title="t('toolbar.refresh')" @click="fs.refresh()"><IconRefresh /></button>
          </div>
        </div>

        <div v-if="selectionCount && !fs.searchMode" class="selection-toolbar">
          <strong>{{ t('info.items_selected', { n: selectionCount }) }}</strong>
          <div>
            <button v-if="selectionCount === 1" @click="openSelected"><IconArrowRight />{{ t('menu.open') }}</button>
            <button @click="fs.copySelected(); message.success(t('files.copied'))"><IconContentCopy />{{ t('menu.copy') }}</button>
            <button v-if="!fs.isTrash" @click="fs.cutSelected(); message.success(t('files.cut'))"><IconContentCut />{{ t('menu.cut') }}</button>
            <button v-if="fs.isTrash" @click="fs.restoreSelected()"><IconRestore />{{ t('menu.restore') }}</button>
            <button v-if="selectionCount === 1" @click="showDetails(selectedItem!)"><IconInformationOutline />{{ t('menu.details') }}</button>
            <button class="danger" @click="fs.deleteSelected()"><IconDeleteOutline />{{ fs.isTrash ? t('menu.permanent_delete') : t('menu.delete') }}</button>
            <button class="icon-only" :aria-label="t('files.clear_selection')" @click="fs.exitSelectMode(); fs.clearSelection()"><IconClose /></button>
          </div>
        </div>

        <div v-if="hasClipboard && !fs.isTrash && !fs.searchMode" class="clipboard-banner">
          <span>{{ t('files.clipboard_ready', { n: fs.clipboard.items.length }) }}</span>
          <button @click="fs.paste()">{{ t('menu.paste') }}</button>
          <button class="icon-only" @click="fs.clipboard = { items: [], mode: null }"><IconClose /></button>
        </div>

        <div class="file-surface" :class="{ 'is-grid': fs.viewMode === 'icons' }" @click.self="fs.clearSelection()">
          <div v-if="fs.loading || fs.searchLoading" class="surface-state" data-state="loading">
            <div class="loading-orbit"><span /></div>
            <strong>{{ fs.searchMode ? t('files.searching') : t('files.loading') }}</strong>
          </div>
          <div v-else-if="fs.error" class="surface-state" data-state="error">
            <IconInformationOutline width="34" height="34" />
            <strong>{{ t('files.load_failed') }}</strong>
            <span>{{ fs.error }}</span>
            <button @click="fs.refresh()">{{ t('toolbar.refresh') }}</button>
          </div>
          <div v-else-if="directoryFiles.length === 0" class="surface-state" data-state="empty">
            <div class="empty-folder"><span /><span /></div>
            <strong>{{ fs.searchMode ? t('files.no_search_results') : fs.isTrash ? t('files.trash_empty') : t('fileview.empty') }}</strong>
            <span>{{ fs.searchMode ? t('files.search_hint') : fs.isTrash ? t('files.trash_empty_hint') : t('files.empty_hint') }}</span>
            <button v-if="!fs.searchMode && !fs.isTrash" @click="triggerUpload"><IconUpload />{{ t('toolbar.upload') }}</button>
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
              @contextmenu.prevent.stop="openActionMenu(file, $event)"
              @pointerdown="beginLongPress(file, $event)"
              @pointerup="endLongPress"
              @pointercancel="endLongPress"
              @pointermove="endLongPress"
              @keydown.enter="openItem(file)"
            >
              <div class="file-card__preview">
                <img v-if="showThumbnails && file.thumbnail_url" :src="file.thumbnail_url" class="file-thumbnail" alt="" draggable="false" />
                <component :is="getFileIcon(file.name, file.is_dir)" v-else width="46" height="46" />
                <span v-if="fs.selectedFiles.includes(file.path)" class="selection-check"><IconCheck /></span>
                <span v-if="file.status && file.status !== 'ready'" class="file-status">{{ phaseLabel(file.task_phase || file.status) }}</span>
              </div>
              <div class="file-card__copy">
                <strong class="file-name" :title="file.name">{{ file.name }}</strong>
                <span>{{ file.is_dir ? t('info.directory') : formatSize(file.size) }}</span>
              </div>
              <button class="more-button" :aria-label="t('common.more')" @click.stop="openActionMenu(file)"><IconDotsHorizontal /></button>
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
              @contextmenu.prevent.stop="openActionMenu(file, $event)"
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
                <span v-if="fs.selectedFiles.includes(file.path)" class="selection-check"><IconCheck /></span>
              </div>
              <span role="cell">{{ file.is_dir ? t('info.directory') : (file.content_type || t('info.file')) }}</span>
              <span role="cell">{{ file.is_dir ? '—' : formatSize(file.size) }}</span>
              <span role="cell">{{ displayDate(file.last_modified) }}</span>
              <button class="more-button" :aria-label="t('common.more')" @click.stop="openActionMenu(file)"><IconDotsHorizontal /></button>
            </div>
          </div>
        </div>

        <footer class="file-statusbar">
          <span>{{ t('files.item_summary', { folders: foldersCount, files: filesCount }) }}</span>
          <span>DOFS · {{ t('files.client_encrypted') }}</span>
        </footer>
      </section>
    </main>

    <nav class="mobile-bottom-nav" aria-label="File locations">
      <button :class="{ active: activePlace === 'files' }" @click.stop="choosePlace('files')"><IconFolderHome /><span>{{ t('files.my_files') }}</span></button>
      <button class="mobile-create" :disabled="fs.isTrash || fs.searchMode" @click.stop="createMenuOpen = !createMenuOpen"><IconPlus /></button>
      <button :class="{ active: activePlace === 'trash' }" @click.stop="choosePlace('trash')"><IconDeleteOutline /><span>{{ t('places.trash') }}</span></button>
    </nav>

    <div v-if="createMenuOpen" class="create-menu" @click.stop>
      <button @click="triggerUpload"><IconUpload />{{ t('toolbar.upload') }}</button>
      <button @click="createMenuOpen = false; fs.createFolder()"><IconFolderPlusOutline />{{ t('toolbar.new_folder') }}</button>
    </div>

    <div
      v-if="actionFile"
      class="action-menu"
      :style="isMobile ? undefined : { left: `${actionMenuX}px`, top: `${actionMenuY}px` }"
      @click.stop
    >
      <div class="action-menu__handle" />
      <div class="action-menu__title">
        <component :is="getFileIcon(actionFile.name, actionFile.is_dir)" />
        <span>{{ actionFile.name }}</span>
        <button @click="actionFile = null"><IconClose /></button>
      </div>
      <button @click="openItem(actionFile)"><IconArrowRight />{{ t('menu.open') }}</button>
      <button v-if="!actionFile.is_dir" @click="downloadItem(actionFile)"><IconDownload />{{ t('menu.download') }}</button>
      <button @click="showDetails(actionFile)"><IconInformationOutline />{{ t('menu.details') }}</button>
      <div class="action-menu__separator" />
      <button v-if="!fs.isTrash" @click="setClipboard(actionFile, 'copy')"><IconContentCopy />{{ t('menu.copy') }}</button>
      <button v-if="!fs.isTrash" @click="setClipboard(actionFile, 'cut')"><IconContentCut />{{ t('menu.cut') }}</button>
      <button v-if="!fs.isTrash" @click="renameItem(actionFile)"><IconPencilOutline />{{ t('menu.rename') }}</button>
      <button v-if="fs.isTrash" @click="restoreItem(actionFile)"><IconRestore />{{ t('menu.restore') }}</button>
      <button class="danger" @click="deleteItem(actionFile)"><IconDeleteOutline />{{ fs.isTrash ? t('menu.permanent_delete') : t('menu.delete') }}</button>
    </div>

    <aside v-if="showInspector && detailsFile" class="inspector" @click.stop>
      <div class="inspector__header">
        <div>
          <span>{{ t('menu.details') }}</span>
          <strong>{{ detailsFile.name }}</strong>
        </div>
        <button @click="showInspector = false"><IconClose /></button>
      </div>
      <div class="inspector__preview">
        <img v-if="showThumbnails && detailsFile.thumbnail_url" :src="detailsFile.thumbnail_url" alt="" />
        <component :is="getFileIcon(detailsFile.name, detailsFile.is_dir)" v-else />
      </div>
      <dl>
        <div><dt>{{ t('info.type') }}</dt><dd>{{ detailsFile.is_dir ? t('info.directory') : (detailsFile.content_type || t('info.file')) }}</dd></div>
        <div><dt>{{ t('info.size') }}</dt><dd>{{ detailsFile.is_dir ? '—' : formatSize(detailsFile.size) }}</dd></div>
        <div><dt>{{ t('info.modified') }}</dt><dd>{{ displayDate(detailsFile.last_modified) }}</dd></div>
        <div><dt>{{ t('info.created') }}</dt><dd>{{ displayDate(detailsFile.created_at) }}</dd></div>
        <div v-if="detailsFile.media_width"><dt>{{ t('info.dimensions') }}</dt><dd>{{ detailsFile.media_width }} × {{ detailsFile.media_height }}</dd></div>
        <div><dt>{{ t('files.path') }}</dt><dd class="path-value">{{ detailsFile.path }}</dd></div>
      </dl>
      <div class="inspector__actions">
        <button v-if="!detailsFile.is_dir" @click="downloadItem(detailsFile)"><IconDownload />{{ t('menu.download') }}</button>
        <button @click="openItem(detailsFile)"><IconArrowRight />{{ t('menu.open') }}</button>
      </div>
    </aside>

    <section v-if="showActivity" class="activity-panel" @click.stop>
      <div class="panel-heading">
        <div><span>{{ t('files.activity') }}</span><strong>{{ activityLabel }}</strong></div>
        <button @click="showActivity = false"><IconClose /></button>
      </div>
      <div v-if="upload.uploads.length === 0 && serverTasks.length === 0 && pendingOps.pendingCount === 0" class="panel-empty">
        <IconCheck width="30" height="30" />
        <strong>{{ t('files.all_done') }}</strong>
        <span>{{ t('files.all_done_hint') }}</span>
      </div>
      <div v-else class="activity-list">
        <article v-for="item in upload.uploads" :key="item.id">
          <div><strong>{{ item.fileName }}</strong><span>{{ phaseLabel(item.phase) }}</span></div>
          <span>{{ Math.round(item.progress) }}%</span>
          <div class="progress"><i :style="{ width: `${Math.max(2, item.progress)}%` }" /></div>
          <button v-if="item.status === 'uploading' || item.status === 'paused'" @click="upload.cancelUpload(item.id)">{{ t('upload.cancel') }}</button>
        </article>
        <article v-for="item in serverTasks" :key="item.task_id">
          <div><strong>{{ item.name || item.type }}</strong><span>{{ item.phase || item.status }}</span></div>
          <span>{{ Math.round((item.progress || 0) * 100) }}%</span>
          <div class="progress"><i :style="{ width: `${Math.max(2, (item.progress || 0) * 100)}%` }" /></div>
        </article>
        <article v-if="pendingOps.pendingCount" class="pending-row">
          <div><strong>{{ t('pending.title') }}</strong><span>{{ t('pending.status_queued') }}</span></div>
          <span>{{ pendingOps.pendingCount }}</span>
        </article>
      </div>
    </section>

    <section v-if="showAccount" class="account-menu" @click.stop>
      <div class="account-menu__identity">
        <img v-if="auth.user?.avatar_url" :src="auth.user.avatar_url" alt="" />
        <IconAccountCircle v-else />
        <div><strong>{{ auth.user?.display_name || auth.username }}</strong><span>@{{ auth.username }} · {{ auth.user?.role }}</span></div>
      </div>
      <button
        class="preference-toggle"
        role="switch"
        :aria-checked="showThumbnails"
        @click="toggleThumbnailDisplay"
      >
        <IconViewGridOutline />
        <span><strong>{{ t('files.show_thumbnails') }}</strong><small>{{ t('files.show_thumbnails_hint') }}</small></span>
        <i :class="{ on: showThumbnails }"><b /></i>
      </button>
      <button v-if="auth.isRoot" @click="router.push('/admin')"><IconAccountCircle />{{ t('titlebar.admin_panel') }}</button>
      <button class="danger" @click="logout"><IconArrowLeft />{{ t('titlebar.sign_out') }}</button>
    </section>

    <div v-if="draggingFiles" class="drop-overlay">
      <div><IconUpload /><strong>{{ t('fileview.drop') }}</strong><span>{{ t('files.drop_encrypted') }}</span></div>
    </div>

    <input ref="uploadInput" class="upload-input" type="file" multiple @change="handleUploadSelection" />
  </div>
</template>

<style lang="scss" scoped>
.file-shell {
  --ink: #18243b;
  --muted: #778197;
  --line: #e8ebf2;
  --surface: #fff;
  --canvas: #f6f7fb;
  --accent: #5568e8;
  --accent-soft: #edf0ff;
  height: 100dvh;
  overflow: hidden;
  color: var(--ink);
  background: var(--canvas);
  font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  user-select: none;
}

button, input, select { font: inherit; }
button { color: inherit; }

.file-sidebar {
  position: fixed;
  inset: 0 auto 0 0;
  z-index: 30;
  display: flex;
  width: 244px;
  flex-direction: column;
  padding: 26px 18px 20px;
  border-right: 1px solid var(--line);
  background: rgba(255, 255, 255, 0.92);
  backdrop-filter: blur(24px);
}

.brand-lockup, .mobile-brand { display: flex; align-items: center; gap: 11px; }
.brand-mark {
  display: grid;
  width: 38px;
  height: 38px;
  place-items: center;
  border-radius: 12px;
  color: #fff;
  background: linear-gradient(145deg, #6578f4, #4354d3);
  box-shadow: 0 8px 18px rgba(85, 104, 232, 0.25);
  font-size: 18px;
  font-weight: 800;
}
.brand-name { font-size: 16px; font-weight: 800; letter-spacing: .12em; }
.brand-caption { margin-top: 2px; color: var(--muted); font-size: 10px; font-weight: 700; letter-spacing: .07em; text-transform: uppercase; }

.place-list { display: grid; gap: 5px; margin-top: 42px; }
.place-item {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-height: 46px;
  padding: 0 13px;
  border: 0;
  border-radius: 12px;
  background: transparent;
  color: #5e687c;
  cursor: pointer;
  font-size: 14px;
  font-weight: 650;
  text-align: left;
  transition: .16s ease;
}
.place-item:hover { background: #f4f5fa; color: var(--ink); }
.place-item.active { color: var(--accent); background: var(--accent-soft); }

.security-note {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 10px;
  margin-top: auto;
  margin-bottom: 18px;
  padding: 14px;
  border: 1px solid #e2e6f6;
  border-radius: 14px;
  background: #f9faff;
}
.security-note__icon { display: grid; width: 24px; height: 24px; place-items: center; border-radius: 50%; color: #fff; background: #42a579; }
.security-note strong, .security-note span { display: block; }
.security-note strong { font-size: 12px; }
.security-note span { margin-top: 3px; color: var(--muted); font-size: 10px; line-height: 1.45; }

.sidebar-account {
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px;
  border: 0;
  border-radius: 12px;
  background: transparent;
  cursor: pointer;
  text-align: left;
}
.sidebar-account:hover { background: #f4f5fa; }
.sidebar-account img, .mobile-account img { width: 34px; height: 34px; border-radius: 50%; object-fit: cover; }
.sidebar-account span { min-width: 0; }
.sidebar-account strong, .sidebar-account small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sidebar-account strong { font-size: 12px; }
.sidebar-account small { margin-top: 2px; color: var(--muted); font-size: 10px; }

.file-main { height: 100dvh; margin-left: 244px; overflow: hidden; }
.file-header {
  position: sticky;
  top: 0;
  z-index: 25;
  display: flex;
  height: 72px;
  align-items: center;
  justify-content: center;
  padding: 0 30px;
  border-bottom: 1px solid var(--line);
  background: rgba(246, 247, 251, .88);
  backdrop-filter: blur(22px);
}
.mobile-brand { display: none; }
.global-search {
  display: grid;
  width: min(540px, 52vw);
  height: 42px;
  grid-template-columns: auto 1fr auto auto;
  align-items: center;
  gap: 10px;
  padding: 0 12px;
  border: 1px solid #e3e6ef;
  border-radius: 13px;
  background: #fff;
  color: #8992a5;
  box-shadow: 0 3px 12px rgba(24, 36, 59, .03);
}
.global-search:focus-within { border-color: #aeb8fa; box-shadow: 0 0 0 3px rgba(85, 104, 232, .09); }
.global-search input { min-width: 0; border: 0; outline: 0; color: var(--ink); background: transparent; font-size: 13px; }
.global-search input::placeholder { color: #9ba3b3; }
.global-search button { display: grid; padding: 3px; border: 0; place-items: center; background: none; cursor: pointer; }
.global-search kbd { padding: 3px 7px; border: 1px solid #e4e7ef; border-radius: 6px; color: #959daf; background: #f8f9fb; font-size: 10px; }
.header-actions { position: absolute; right: 30px; display: flex; align-items: center; gap: 9px; }
.activity-button, .mobile-account {
  position: relative;
  display: grid;
  width: 40px;
  height: 40px;
  border: 1px solid #e3e6ef;
  place-items: center;
  border-radius: 12px;
  background: #fff;
  cursor: pointer;
}
.activity-badge { position: absolute; top: -4px; right: -4px; min-width: 17px; height: 17px; padding: 0 4px; border: 2px solid var(--canvas); border-radius: 10px; color: #fff; background: #ee5b68; font-size: 9px; line-height: 13px; }
.mobile-account { display: none; border: 0; background: transparent; }

.file-workspace { display: flex; height: calc(100dvh - 72px); min-height: 0; flex-direction: column; padding: 0 30px; }
.location-toolbar { display: grid; min-height: 70px; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 18px; border-bottom: 1px solid var(--line); }
.history-buttons { display: flex; gap: 4px; }
.history-buttons button, .view-controls > button, .view-switch button {
  display: grid;
  width: 34px;
  height: 34px;
  padding: 0;
  border: 0;
  place-items: center;
  border-radius: 9px;
  color: #667086;
  background: transparent;
  cursor: pointer;
}
.history-buttons button:hover:not(:disabled), .view-controls > button:hover, .view-switch button:hover { color: var(--ink); background: #eceef4; }
.history-buttons button:disabled { opacity: .3; cursor: default; }
.history-buttons svg, .view-controls svg { width: 18px; height: 18px; }
.breadcrumbs { display: flex; min-width: 0; align-items: center; gap: 5px; overflow: hidden; color: #9aa1b0; }
.breadcrumbs button { overflow: hidden; padding: 6px 7px; border: 0; border-radius: 7px; color: #657086; background: transparent; cursor: pointer; font-size: 12px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.breadcrumbs button:hover { color: var(--accent); background: var(--accent-soft); }
.primary-actions { display: flex; gap: 9px; }
.primary-actions button, .surface-state button {
  display: inline-flex;
  min-height: 38px;
  align-items: center;
  gap: 8px;
  padding: 0 14px;
  border: 1px solid #dfe3ec;
  border-radius: 10px;
  background: #fff;
  cursor: pointer;
  font-size: 12px;
  font-weight: 700;
}
.primary-actions svg, .surface-state button svg { width: 17px; height: 17px; }
.primary-actions .primary-action { border-color: var(--accent); color: #fff; background: var(--accent); box-shadow: 0 7px 16px rgba(85, 104, 232, .2); }
.primary-actions .danger-action { color: #c74652; border-color: #f0d6d9; background: #fff7f8; }

.content-heading { display: flex; align-items: end; justify-content: space-between; gap: 20px; padding: 27px 0 18px; }
.eyebrow { margin-bottom: 5px; color: var(--accent); font-size: 9px; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
.content-heading h1 { margin: 0; font-size: 25px; line-height: 1.15; letter-spacing: -.03em; }
.content-heading p { margin: 6px 0 0; color: var(--muted); font-size: 11px; }
.view-controls { display: flex; align-items: center; gap: 6px; }
.view-controls label { display: flex; height: 34px; align-items: center; gap: 7px; padding: 0 8px 0 11px; border: 1px solid #e1e4ec; border-radius: 9px; color: var(--muted); background: #fff; font-size: 10px; }
.view-controls select { max-width: 100px; border: 0; outline: 0; color: var(--ink); background: transparent; font-size: 11px; font-weight: 650; }
.view-switch { display: flex; padding: 3px; border: 1px solid #e1e4ec; border-radius: 10px; background: #fff; }
.view-switch button { width: 28px; height: 27px; border-radius: 7px; }
.view-switch button.active { color: var(--accent); background: var(--accent-soft); }

.selection-toolbar, .clipboard-banner {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
  padding: 7px 10px 7px 15px;
  border: 1px solid #dce1ff;
  border-radius: 12px;
  color: #4456cc;
  background: #f2f4ff;
  font-size: 11px;
}
.selection-toolbar > div { display: flex; align-items: center; gap: 3px; }
.selection-toolbar button, .clipboard-banner button {
  display: inline-flex;
  min-height: 32px;
  align-items: center;
  gap: 5px;
  padding: 0 9px;
  border: 0;
  border-radius: 8px;
  color: #4e5db9;
  background: transparent;
  cursor: pointer;
  font-size: 10px;
  font-weight: 700;
}
.selection-toolbar button:hover, .clipboard-banner button:hover { background: #e6e9ff; }
.selection-toolbar button.danger { color: #ca4653; }
.selection-toolbar svg { width: 15px; height: 15px; }
.selection-toolbar .icon-only, .clipboard-banner .icon-only { width: 30px; padding: 0; justify-content: center; }
.clipboard-banner { justify-content: flex-start; color: #526074; border-color: #e3e6ed; background: #fafbfc; }
.clipboard-banner button:first-of-type { margin-left: auto; color: var(--accent); }

.file-surface { position: relative; min-height: 0; flex: 1; overflow: auto; padding-bottom: 24px; }
.file-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 13px; }
.file-card {
  position: relative;
  min-width: 0;
  padding: 10px;
  border: 1px solid transparent;
  border-radius: 14px;
  background: transparent;
  cursor: default;
  outline: none;
  transition: .14s ease;
}
.file-card:hover { border-color: #e3e6ef; background: rgba(255, 255, 255, .72); }
.file-card:focus-visible { box-shadow: 0 0 0 3px rgba(85, 104, 232, .15); }
.file-card.selected { border-color: #cbd2ff; background: var(--accent-soft); }
.file-card.cut, .file-row.cut { opacity: .48; }
.file-card__preview {
  position: relative;
  display: grid;
  height: 116px;
  place-items: center;
  overflow: hidden;
  border-radius: 11px;
  color: #6979df;
  background: #eef0f5;
}
.file-card__preview img { width: 100%; height: 100%; object-fit: cover; }
.file-card__copy { padding: 10px 24px 2px 2px; }
.file-card__copy strong, .file-card__copy span { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-card__copy strong { font-size: 12px; font-weight: 650; }
.file-card__copy span { margin-top: 4px; color: var(--muted); font-size: 10px; }
.more-button { display: grid; width: 28px; height: 28px; padding: 0; border: 0; place-items: center; border-radius: 8px; color: #7e8798; background: transparent; cursor: pointer; }
.more-button:hover { color: var(--ink); background: #e5e8ef; }
.file-card > .more-button { position: absolute; right: 8px; bottom: 8px; }
.more-button svg { width: 17px; height: 17px; }
.selection-check { position: absolute; top: 8px; right: 8px; display: grid; width: 20px; height: 20px; place-items: center; border-radius: 50%; color: #fff; background: var(--accent); box-shadow: 0 2px 6px rgba(38, 48, 120, .25); }
.selection-check svg { width: 13px; height: 13px; }
.file-status { position: absolute; right: 7px; bottom: 7px; padding: 4px 7px; border-radius: 7px; color: #fff; background: rgba(24, 36, 59, .72); font-size: 8px; backdrop-filter: blur(5px); }

.file-list { overflow: hidden; border: 1px solid var(--line); border-radius: 13px; background: rgba(255, 255, 255, .72); }
.file-list__head, .file-row { display: grid; grid-template-columns: minmax(240px, 2fr) minmax(120px, 1fr) 100px 145px 38px; align-items: center; gap: 12px; }
.file-list__head { min-height: 38px; padding: 0 12px; border-bottom: 1px solid var(--line); color: #8b93a3; background: #f9fafc; font-size: 9px; font-weight: 750; letter-spacing: .04em; text-transform: uppercase; }
.file-row { min-height: 54px; padding: 0 12px; border-bottom: 1px solid #eef0f4; color: #6f788a; outline: 0; font-size: 10px; }
.file-row:last-child { border-bottom: 0; }
.file-row:hover { background: #f8f9fc; }
.file-row.selected { background: var(--accent-soft); }
.file-row__name { position: relative; display: flex; min-width: 0; align-items: center; gap: 11px; color: var(--ink); }
.file-row__name > span:nth-child(2) { min-width: 0; }
.file-row__name strong { display: block; overflow: hidden; font-size: 11px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.file-row__icon { display: grid; width: 34px; height: 34px; flex: 0 0 auto; place-items: center; overflow: hidden; border-radius: 9px; color: #6979df; background: #eef0f5; }
.file-row__icon svg { width: 20px; height: 20px; }
.file-row__icon img { width: 100%; height: 100%; object-fit: cover; }
.file-row .selection-check { position: static; margin-left: auto; flex: 0 0 auto; }
.mobile-meta { display: none; }

.surface-state { display: flex; min-height: 360px; align-items: center; justify-content: center; flex-direction: column; gap: 8px; color: var(--muted); text-align: center; }
.surface-state strong { color: var(--ink); font-size: 14px; }
.surface-state > span { max-width: 330px; font-size: 11px; line-height: 1.5; }
.surface-state button { margin-top: 7px; color: var(--accent); }
.loading-orbit { position: relative; width: 34px; height: 34px; margin-bottom: 4px; border: 2px solid #e2e5ef; border-top-color: var(--accent); border-radius: 50%; animation: orbit .8s linear infinite; }
.loading-orbit span { position: absolute; top: 1px; right: 2px; width: 5px; height: 5px; border-radius: 50%; background: var(--accent); }
@keyframes orbit { to { transform: rotate(360deg); } }
.empty-folder { position: relative; width: 64px; height: 47px; margin-bottom: 10px; border-radius: 8px 10px 10px 10px; background: #e8eaf2; }
.empty-folder::before { content: ''; position: absolute; top: -8px; left: 5px; width: 27px; height: 12px; border-radius: 6px 6px 0 0; background: #e8eaf2; }
.empty-folder span:first-child { position: absolute; inset: 11px 10px 9px; border: 1px dashed #b9bfcd; border-radius: 5px; }

.file-statusbar { display: flex; min-height: 44px; align-items: center; justify-content: space-between; border-top: 1px solid var(--line); color: #9299a8; font-size: 9px; }

.mobile-bottom-nav, .create-menu { display: none; }
.upload-input { display: none; }

.action-menu, .account-menu, .activity-panel, .inspector {
  position: fixed;
  z-index: 70;
  border: 1px solid #e2e5ec;
  border-radius: 14px;
  background: rgba(255, 255, 255, .98);
  box-shadow: 0 18px 55px rgba(29, 38, 61, .16);
  backdrop-filter: blur(22px);
}
.action-menu { width: 220px; padding: 7px; }
.action-menu__handle { display: none; }
.action-menu__title { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 8px; padding: 7px 6px 10px; border-bottom: 1px solid var(--line); margin-bottom: 5px; }
.action-menu__title > svg { width: 20px; height: 20px; color: var(--accent); }
.action-menu__title span { overflow: hidden; font-size: 11px; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }
.action-menu__title button, .panel-heading button, .inspector__header button { display: grid; width: 28px; height: 28px; padding: 0; border: 0; place-items: center; border-radius: 8px; background: transparent; cursor: pointer; }
.action-menu > button, .account-menu > button { display: flex; width: 100%; min-height: 36px; align-items: center; gap: 9px; padding: 0 9px; border: 0; border-radius: 8px; background: transparent; cursor: pointer; font-size: 11px; font-weight: 600; text-align: left; }
.action-menu > button:hover, .account-menu > button:hover { background: #f1f3f7; }
.action-menu > button svg, .account-menu > button svg { width: 16px; height: 16px; color: #70798c; }
.action-menu > button.danger, .account-menu > button.danger { color: #c84753; }
.action-menu__separator { height: 1px; margin: 5px 4px; background: var(--line); }

.inspector { top: 88px; right: 18px; bottom: 18px; display: flex; width: 320px; flex-direction: column; padding: 18px; }
.inspector__header, .panel-heading { display: flex; align-items: start; justify-content: space-between; gap: 14px; }
.inspector__header span, .inspector__header strong, .panel-heading span, .panel-heading strong { display: block; }
.inspector__header span, .panel-heading span { color: var(--muted); font-size: 9px; font-weight: 800; letter-spacing: .1em; text-transform: uppercase; }
.inspector__header strong, .panel-heading strong { margin-top: 4px; max-width: 230px; overflow: hidden; font-size: 14px; text-overflow: ellipsis; white-space: nowrap; }
.inspector__preview { display: grid; height: 180px; place-items: center; overflow: hidden; margin: 22px 0; border-radius: 14px; color: var(--accent); background: #f0f1f6; }
.inspector__preview svg { width: 58px; height: 58px; }
.inspector__preview img { width: 100%; height: 100%; object-fit: contain; }
.inspector dl { margin: 0; overflow: auto; }
.inspector dl div { display: grid; grid-template-columns: 88px minmax(0, 1fr); gap: 10px; padding: 10px 2px; border-bottom: 1px solid var(--line); font-size: 10px; }
.inspector dt { color: var(--muted); }
.inspector dd { margin: 0; overflow-wrap: anywhere; }
.path-value { font-family: ui-monospace, monospace; }
.inspector__actions { display: flex; gap: 8px; margin-top: auto; padding-top: 16px; }
.inspector__actions button { display: flex; min-height: 38px; flex: 1; align-items: center; justify-content: center; gap: 6px; border: 1px solid #e1e4ec; border-radius: 9px; background: #fff; cursor: pointer; font-size: 10px; font-weight: 700; }
.inspector__actions svg { width: 15px; height: 15px; }

.activity-panel { top: 64px; right: 24px; width: 360px; max-height: min(560px, calc(100dvh - 82px)); overflow: auto; padding: 17px; }
.panel-empty { display: flex; min-height: 190px; align-items: center; justify-content: center; flex-direction: column; gap: 7px; color: #52a27d; text-align: center; }
.panel-empty strong { color: var(--ink); font-size: 13px; }
.panel-empty span { color: var(--muted); font-size: 10px; }
.activity-list { display: grid; gap: 9px; margin-top: 15px; }
.activity-list article { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 7px 12px; padding: 12px; border-radius: 11px; background: #f7f8fb; font-size: 9px; }
.activity-list article strong, .activity-list article span { display: block; }
.activity-list article strong { overflow: hidden; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.activity-list article div span { margin-top: 3px; color: var(--muted); }
.progress { grid-column: 1 / -1; height: 4px; overflow: hidden; border-radius: 4px; background: #e4e7ee; }
.progress i { display: block; height: 100%; border-radius: inherit; background: var(--accent); transition: width .2s ease; }
.activity-list article > button { grid-column: 1 / -1; justify-self: start; padding: 4px 8px; border: 0; border-radius: 6px; color: #c74652; background: #fff0f2; cursor: pointer; font-size: 9px; }

.account-menu { left: 16px; bottom: 78px; width: 218px; padding: 8px; }
.account-menu__identity { display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 9px; padding: 8px 7px 12px; margin-bottom: 4px; border-bottom: 1px solid var(--line); }
.account-menu__identity > img, .account-menu__identity > svg { width: 35px; height: 35px; border-radius: 50%; object-fit: cover; }
.account-menu__identity strong, .account-menu__identity span { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.account-menu__identity strong { font-size: 11px; }
.account-menu__identity span { margin-top: 3px; color: var(--muted); font-size: 9px; }
.account-menu > button.preference-toggle { min-height: 52px; }
.preference-toggle > span { min-width: 0; flex: 1; }
.preference-toggle strong, .preference-toggle small { display: block; }
.preference-toggle small { margin-top: 3px; color: var(--muted); font-size: 9px; font-weight: 500; line-height: 1.25; }
.preference-toggle > i { position: relative; width: 30px; height: 17px; flex: 0 0 auto; border-radius: 999px; background: #ccd1dc; transition: .16s ease; }
.preference-toggle > i b { position: absolute; top: 2px; left: 2px; width: 13px; height: 13px; border-radius: 50%; background: #fff; box-shadow: 0 1px 3px rgba(0, 0, 0, .2); transition: .16s ease; }
.preference-toggle > i.on { background: var(--accent); }
.preference-toggle > i.on b { transform: translateX(13px); }

.drop-overlay { position: fixed; inset: 12px; z-index: 100; display: grid; border: 2px dashed #8f9cf4; place-items: center; border-radius: 20px; background: rgba(243, 245, 255, .93); backdrop-filter: blur(12px); pointer-events: none; }
.drop-overlay > div { display: flex; align-items: center; flex-direction: column; gap: 9px; color: var(--accent); }
.drop-overlay svg { width: 42px; height: 42px; }
.drop-overlay strong { color: var(--ink); font-size: 17px; }
.drop-overlay span { color: var(--muted); font-size: 11px; }

@media (max-width: 980px) {
  .file-sidebar { width: 205px; }
  .file-main { margin-left: 205px; }
  .file-workspace { padding: 0 22px; }
  .file-list__head, .file-row { grid-template-columns: minmax(200px, 2fr) 90px 125px 38px; }
  .file-list__head > :nth-child(2), .file-row > :nth-child(2) { display: none; }
}

@media (max-width: 767px) {
  .file-shell { --canvas: #f8f9fc; min-height: 100dvh; }
  .file-sidebar { display: none; }
  .file-main { height: 100dvh; margin-left: 0; }
  .file-header { height: auto; min-height: 70px; justify-content: space-between; gap: 12px; padding: max(12px, env(safe-area-inset-top)) 15px 10px; }
  .mobile-brand { display: flex; flex: 0 0 auto; }
  .mobile-brand .brand-mark { width: 34px; height: 34px; border-radius: 11px; font-size: 15px; }
  .mobile-brand strong { display: none; font-size: 13px; letter-spacing: .08em; }
  .global-search { order: 2; width: 100%; height: 40px; grid-template-columns: auto 1fr auto; }
  .global-search kbd { display: none; }
  .header-actions { position: static; order: 3; gap: 4px; }
  .activity-button { width: 36px; height: 36px; border: 0; background: transparent; }
  .mobile-account { display: grid; width: 36px; height: 36px; }
  .file-workspace { height: calc(100dvh - 70px); min-height: 0; padding: 0 14px 88px; }
  .location-toolbar { min-height: 54px; grid-template-columns: auto minmax(0, 1fr); gap: 8px; }
  .history-buttons button:nth-child(2) { display: none; }
  .history-buttons button { width: 30px; height: 30px; }
  .breadcrumbs { gap: 2px; }
  .breadcrumbs button { max-width: 110px; padding: 5px 4px; font-size: 10px; }
  .breadcrumbs button:not(:last-child), .breadcrumbs span:not(:last-of-type) { display: none; }
  .primary-actions { display: none; }
  .content-heading { align-items: center; padding: 20px 2px 14px; }
  .content-heading h1 { font-size: 22px; }
  .content-heading p { font-size: 10px; }
  .view-controls { gap: 3px; }
  .view-controls label span, .view-controls > button, .view-controls > button:last-child { display: none; }
  .view-controls label { height: 32px; padding: 0 6px; }
  .view-switch { padding: 2px; }
  .selection-toolbar { position: fixed; left: 10px; right: 10px; bottom: calc(77px + env(safe-area-inset-bottom)); z-index: 45; min-height: 56px; padding: 8px 10px; box-shadow: 0 12px 34px rgba(38, 47, 71, .18); }
  .selection-toolbar > strong { display: none; }
  .selection-toolbar > div { width: 100%; justify-content: space-around; }
  .selection-toolbar button { min-width: 38px; min-height: 38px; justify-content: center; padding: 0 7px; font-size: 0; }
  .selection-toolbar button svg { width: 18px; height: 18px; }
  .clipboard-banner { font-size: 9px; }
  .file-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
  .file-card { padding: 7px; border-radius: 13px; background: #fff; box-shadow: 0 4px 14px rgba(34, 43, 67, .035); }
  .file-card__preview { height: auto; aspect-ratio: 1.18; }
  .file-card__copy { padding: 9px 22px 3px 1px; }
  .file-card__copy strong { font-size: 11px; }
  .file-card__copy span { font-size: 9px; }
  .file-card > .more-button { right: 5px; bottom: 5px; }
  .file-list { border: 0; border-radius: 0; background: transparent; }
  .file-list__head { display: none; }
  .file-row { grid-template-columns: minmax(0, 1fr) auto; min-height: 62px; padding: 5px 4px; border-bottom-color: #e9ebf1; font-size: 0; }
  .file-row > :nth-child(2), .file-row > :nth-child(3), .file-row > :nth-child(4) { display: none; }
  .file-row__icon { width: 40px; height: 40px; border-radius: 11px; }
  .file-row__name strong { font-size: 12px; }
  .mobile-meta { display: block; margin-top: 3px; color: var(--muted); font-size: 9px; font-weight: 400; }
  .surface-state { min-height: 330px; }
  .file-statusbar { display: none; }
  .mobile-bottom-nav {
    position: fixed;
    left: 10px;
    right: 10px;
    bottom: max(10px, env(safe-area-inset-bottom));
    z-index: 40;
    display: grid;
    height: 66px;
    grid-template-columns: 1fr 64px 1fr;
    align-items: center;
    border: 1px solid rgba(224, 227, 236, .9);
    border-radius: 20px;
    background: rgba(255, 255, 255, .94);
    box-shadow: 0 14px 42px rgba(33, 42, 64, .16);
    backdrop-filter: blur(20px);
  }
  .mobile-bottom-nav button { display: flex; height: 100%; align-items: center; justify-content: center; flex-direction: column; gap: 3px; border: 0; color: #8b93a3; background: transparent; font-size: 8px; font-weight: 700; }
  .mobile-bottom-nav button svg { width: 21px; height: 21px; }
  .mobile-bottom-nav button.active { color: var(--accent); }
  .mobile-bottom-nav .mobile-create { width: 52px; height: 52px; place-self: center; border-radius: 17px; color: #fff; background: var(--accent); box-shadow: 0 8px 18px rgba(85, 104, 232, .3); }
  .mobile-bottom-nav .mobile-create:disabled { opacity: .38; }
  .mobile-bottom-nav .mobile-create svg { width: 25px; height: 25px; }
  .create-menu { position: fixed; right: 50%; bottom: calc(85px + env(safe-area-inset-bottom)); z-index: 65; display: grid; width: 190px; padding: 7px; transform: translateX(50%); border: 1px solid var(--line); border-radius: 14px; background: #fff; box-shadow: 0 14px 40px rgba(33, 42, 64, .18); }
  .create-menu button { display: flex; min-height: 43px; align-items: center; gap: 10px; padding: 0 12px; border: 0; border-radius: 9px; background: transparent; font-size: 11px; font-weight: 650; text-align: left; }
  .create-menu svg { width: 18px; height: 18px; color: var(--accent); }
  .action-menu { left: 0 !important; right: 0; top: auto !important; bottom: 0; z-index: 80; width: auto; padding: 7px 14px calc(15px + env(safe-area-inset-bottom)); border-width: 1px 0 0; border-radius: 22px 22px 0 0; box-shadow: 0 -12px 44px rgba(29, 38, 61, .16); }
  .action-menu__handle { display: block; width: 36px; height: 4px; margin: 1px auto 7px; border-radius: 4px; background: #d9dde6; }
  .action-menu > button { min-height: 43px; font-size: 12px; }
  .inspector { top: auto; right: 0; bottom: 0; left: 0; z-index: 82; width: auto; max-height: 85dvh; padding: 18px 18px calc(18px + env(safe-area-inset-bottom)); border-width: 1px 0 0; border-radius: 22px 22px 0 0; }
  .inspector__preview { height: 150px; margin: 16px 0; }
  .activity-panel { top: 62px; right: 10px; left: 10px; width: auto; max-height: calc(100dvh - 150px); }
  .account-menu { top: 62px; right: 10px; bottom: auto; left: auto; width: 220px; }
}

@media (max-width: 420px) {
  .file-header { display: grid; grid-template-columns: auto 1fr; }
  .mobile-brand { grid-column: 1; }
  .header-actions { grid-column: 2; justify-self: end; }
  .global-search { grid-column: 1 / -1; order: 3; }
  .file-workspace { height: calc(100dvh - 122px); min-height: 0; }
  .file-card__preview { aspect-ratio: 1.08; }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
}
</style>
