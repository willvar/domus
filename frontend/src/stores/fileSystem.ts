import { defineStore } from 'pinia'
import { ref, reactive, computed } from 'vue'
import type { Ref, ComputedRef, WritableComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useAuthStore } from './auth'
import { useWindowManagerStore } from './windowManager'
import { useI18n } from '../composables/useI18n'
import { ICONS } from '../composables/useFileIcon'
import { showPrompt, showConfirm, showDuplicateDialog } from '../composables/useNativeDialog'
import { useAppMessage } from '../ui/feedback'
import { usePreferences } from '../composables/usePreferences'
import { useServiceWorker } from '../composables/useServiceWorker'
import { registerFileDecrypt } from '../composables/useFileAccess'
import { readMigratedStorage } from '../utils/storageCompat'
import { writeEncryptedFile } from '../composables/useCryptoUpload'
import {
  TRASH_ROOT_LOCATION,
  isTrashLocation,
  normalizeTrashDirectoryLocation,
  parseTrashLocation,
  trashItemLocation,
  trashParentLocation,
} from '../utils/trashLocation'
import type {
  FileListItem,
  FileTab,
  SerializedTab,
  ClipboardState,
  ClipboardItem,
  PathSegment,
  AppWindowState,
  ViewerType,
  TextChunkResult,
  PageMapEntry,
  RemoteFlag,
  TaskUpdateEvent,
  ViewerWindowInfo,
  FileAccessResponse,
  SearchResult,
  PendingOpInput,
} from '../types'

const TEXT_CHUNK_SIZE: number = 256 * 1024 // 256KB — aligns with 4 encryption chunks
const NON_CHUNKABLE_TYPES: Set<string> = new Set(['notebook', 'archive'])

function getLargeFileLimit(): number {
  const { prefs } = usePreferences()
  return prefs.largeFileLimitMB * 1024 * 1024
}

function normalizeAppPath(path: string): string {
  if (!path) return '/'
  if (!path.startsWith('/')) path = '/' + path
  return path
}

function normalizeDirPath(path: string): string {
  if (isTrashLocation(path)) return normalizeTrashDirectoryLocation(path)
  path = normalizeAppPath(path)
  if (path !== '/' && !path.endsWith('/')) path += '/'
  return path
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

let tabIdCounter: number = 0
function nextTabId(): string {
  return 'tab-' + (++tabIdCounter)
}

async function fetchTextChunk(decryptUrl: string, byteStart: number, totalSize: number): Promise<TextChunkResult> {
  const byteEnd: number = Math.min(byteStart + TEXT_CHUNK_SIZE - 1, totalSize - 1)
  const res = await fetch(decryptUrl, {
    headers: { Range: `bytes=${byteStart}-${byteEnd}` },
  })

  const buffer = new Uint8Array(await res.arrayBuffer())
  const text: string = new TextDecoder('utf-8').decode(buffer)
  const isLast: boolean = byteEnd >= totalSize - 1

  if (isLast) {
    return { text, byteLength: buffer.byteLength, nextByteStart: totalSize }
  }

  // Find last newline for clean line-aligned boundary
  const lastNL: number = text.lastIndexOf('\n')
  if (lastNL > 0) {
    const clean: string = text.substring(0, lastNL + 1)
    const cleanBytes: number = new TextEncoder().encode(clean).byteLength
    return { text: clean, byteLength: cleanBytes, nextByteStart: byteStart + cleanBytes }
  }

  // No newline found — use full buffer
  return { text, byteLength: buffer.byteLength, nextByteStart: byteStart + buffer.byteLength }
}

export const useFileSystemStore = defineStore('fileSystem', () => {
  const auth = useAuthStore()
  const wm = useWindowManagerStore()
  const ws = useWebSocket()
  const { t, te } = useI18n()
  const message = useAppMessage()

  function pendingLabel(type: PendingOpInput['type']): string {
    return type ? t(`pending.type_${type}`) : t('pending.title')
  }

  function pendingDescription(type: PendingOpInput['type'], detail?: string): string {
    const label = pendingLabel(type)
    return detail ? `${label}: ${detail}` : label
  }

  function pendingToast(type: PendingOpInput['type'], detail?: string): string {
    const label = pendingLabel(type)
    const description = detail ? `${label} ${detail}` : label
    return t('pending.queued_with_description', { description })
  }

  function pendingDetailFromRecord(record: PendingOpInput): string | undefined {
    if (typeof record.description !== 'string' || !record.description) return undefined
    const prefix = `${pendingLabel(record.type)}: `
    return record.description.startsWith(prefix) ? record.description.slice(prefix.length) : record.description
  }

  async function maybeQueuePendingOp(error: any, record: PendingOpInput): Promise<boolean> {
    const { usePendingOpsStore } = await import('./pendingOps')
    const pendingOps = usePendingOpsStore()
    if (!pendingOps.isQueueableError(error)) return false
    await pendingOps.enqueue(record)
    message.warning(pendingToast(record.type, pendingDetailFromRecord(record)))
    return true
  }

  // --- Multi-tab state ---
  const tabs: Ref<FileTab[]> = ref([])
  const activeTabId: Ref<string> = ref('')

  const activeTab: ComputedRef<FileTab | undefined> = computed(() => tabs.value.find(t => t.id === activeTabId.value))

  function bindActiveTabField<T>(key: keyof FileTab, fallback: T): WritableComputedRef<T> {
    return computed({
      get: (): T => (activeTab.value?.[key] as T) ?? (typeof fallback === 'function' ? (fallback as () => T)() : fallback),
      set: (value: T) => {
        if (activeTab.value) (activeTab.value[key] as T) = value
      },
    })
  }

  // --- Proxy computed properties (delegate to active tab) ---
  const currentPath = bindActiveTabField<string>('path', '')
  const files = bindActiveTabField<FileListItem[]>('files', (() => []) as unknown as FileListItem[])
  const loading = bindActiveTabField<boolean>('loading', false)
  const error = bindActiveTabField<string | null>('error', null)
  const selectedFiles = bindActiveTabField<string[]>('selectedFiles', (() => []) as unknown as string[])
  const lastSelectedIndex = bindActiveTabField<number>('lastSelectedIndex', -1)
  const history = bindActiveTabField<string[]>('history', (() => []) as unknown as string[])
  const historyIndex = bindActiveTabField<number>('historyIndex', -1)
  const viewMode = bindActiveTabField<string>('viewMode', 'icons')
  const sortBy = bindActiveTabField<string>('sortBy', 'name')
  const sortOrder = bindActiveTabField<string>('sortOrder', 'asc')
  const searchQuery = bindActiveTabField<string>('searchQuery', '')
  const showHidden: Ref<boolean> = ref(readMigratedStorage('domus_show_hidden', 'zephyr_show_hidden') === '1')

  // --- Search state ---
  const searchMode: Ref<boolean> = ref(false)
  const searchResults: Ref<SearchResult[]> = ref([])
  const searchLoading: Ref<boolean> = ref(false)

  // --- Global (non-tab) state ---
  const clipboard: Ref<ClipboardState> = ref({ items: [], mode: null })
  const iconSize: Ref<number> = ref(48)
  const focusPathBar: Ref<boolean> = ref(false)
  const focusSearch: Ref<boolean> = ref(false)
  const renamingFile: Ref<string | null> = ref(null)
  const showInfoPanel: Ref<boolean> = ref(false)
  const showSidebar: Ref<boolean> = ref(true)
  // App windows (multi-window: array of { windowId, file, url, type, ... })
  const appWindows: Ref<AppWindowState[]> = ref([])

  // Directory cache (global, shared across tabs)
  const cache: Map<string, { data: FileListItem[]; timestamp: number }> = new Map()
  const trashEntryNames: Map<string, string> = new Map()
  const CACHE_TTL: number = 30000

  async function performSearch(query: string): Promise<void> {
    if (!query || query.length < 2) return
    clearSelection()
    searchMode.value = true
    searchLoading.value = true
    try {
      const res = await api.get<{ results?: SearchResult[] }>('/file/search', {
        params: { query, limit: 100 },
      })
      const results = res.data.results || []
      registerThumbnails(results)
      searchResults.value = results
    } catch {
      searchResults.value = []
    } finally {
      searchLoading.value = false
    }
  }

  function exitSearch(): void {
    clearSelection()
    searchMode.value = false
    searchResults.value = []
    searchQuery.value = ''
  }

  // Sorted and filtered files
  const sortedFiles: ComputedRef<FileListItem[]> = computed(() => {
    if (searchMode.value) return searchResults.value

    let items: FileListItem[] = [...files.value]

    if (!showHidden.value) {
      items = items.filter(f => !f.name.startsWith('.'))
    }

    items.sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      const mult: number = sortOrder.value === 'asc' ? 1 : -1
      switch (sortBy.value) {
        case 'name':
          return mult * a.name.localeCompare(b.name, undefined, { numeric: true })
        case 'size':
          return mult * (a.size - b.size)
        case 'date':
          return mult * (new Date(a.last_modified).getTime() - new Date(b.last_modified).getTime())
        default:
          return 0
      }
    })

    return items
  })

  const selectedFile: ComputedRef<FileListItem | undefined> = computed(() => {
    if (selectedFiles.value.length === 1) {
      return files.value.find(f => f.path === selectedFiles.value[0])
    }
    return undefined
  })

  const isTrash: ComputedRef<boolean> = computed(() => isTrashLocation(currentPath.value))
  const isTrashRoot: ComputedRef<boolean> = computed(() => currentPath.value === TRASH_ROOT_LOCATION)
  const isShared: ComputedRef<boolean> = computed(() => currentPath.value === '__shared__/')

  const canGoBack: ComputedRef<boolean> = computed(() => historyIndex.value > 0)
  const canGoForward: ComputedRef<boolean> = computed(() => historyIndex.value < history.value.length - 1)
  const canGoUp: ComputedRef<boolean> = computed(() => currentPath.value !== '/' && !isShared.value && !isTrashRoot.value)

  const pathSegments: ComputedRef<PathSegment[]> = computed(() => {
    if (isTrash.value) {
      const location = parseTrashLocation(currentPath.value)
      if (!location?.id) return []
      const segments: PathSegment[] = [{
        name: trashEntryNames.get(location.id) || t('places.trash'),
        path: trashItemLocation(location.id, '/', true),
      }]
      const parts = location.relativePath.replace(/^\//, '').replace(/\/$/, '').split('/').filter(Boolean)
      let relative = '/'
      for (const part of parts) {
        relative += part + '/'
        segments.push({ name: part, path: trashItemLocation(location.id, relative, true) })
      }
      return segments
    }
    if (!currentPath.value || currentPath.value === '/') return []
    const trimmed: string = currentPath.value.replace(/^\//, '').replace(/\/$/, '')
    const parts: string[] = trimmed.split('/')
    const segments: PathSegment[] = []
    let accumulated: string = '/'
    for (const part of parts) {
      accumulated += part + '/'
      segments.push({ name: part, path: accumulated })
    }
    return segments
  })

  // --- Tab operations ---
  function createTab(path?: string, { remote }: RemoteFlag = {}): string {
    const id: string = nextTabId()
    const requestedPath: string = path || '/'
    const initialPath: string = requestedPath === '__shared__/' ? requestedPath : normalizeDirPath(requestedPath)
    const tab: FileTab = {
      id,
      path: initialPath,
      files: [],
      selectedFiles: [],
      lastSelectedIndex: -1,
      history: [initialPath],
      historyIndex: 0,
      viewMode: 'icons',
      sortBy: 'name',
      sortOrder: 'asc',
      searchQuery: '',
      loading: false,
      error: null,
    }
    tabs.value.push(tab)
    activeTabId.value = id

    // Trash is an ID-addressed product view, not a DOFS directory
    // subscription. It is invalidated through trash.changed instead.
    if (initialPath !== '__shared__/' && !isTrashLocation(initialPath)) {
      tabSubs.set(id, initialPath)
      ws.request('subscribe.directory', { path: initialPath }).catch(() => {})
    }

    loadFiles(initialPath)

    return id
  }

  function closeTab(id: string, { remote }: RemoteFlag = {}): void {
    const idx: number = tabs.value.findIndex(t => t.id === id)
    if (tabs.value.length <= 1) {
      wm.closeWindow('files', { remote })
      return
    }
    // Unsubscribe directory for this tab
    const sub = tabSubs.get(id)
    if (sub) {
      ws.request('unsubscribe.directory', { path: sub }).catch(() => {})
      tabSubs.delete(id)
    }
    if (idx < 0) return

    tabs.value.splice(idx, 1)
    if (activeTabId.value === id) {
      const newIdx: number = Math.min(idx, tabs.value.length - 1)
      activeTabId.value = tabs.value[newIdx].id
    }
  }

  function switchTab(id: string, { remote }: RemoteFlag = {}): void {
    const idx: number = tabs.value.findIndex(t => t.id === id)
    if (idx >= 0) {
      activeTabId.value = id
    }
  }

  function nextTab(): void {
    const idx: number = tabs.value.findIndex(t => t.id === activeTabId.value)
    if (idx < 0) return
    const next: number = (idx + 1) % tabs.value.length
    activeTabId.value = tabs.value[next].id
  }

  function prevTab(): void {
    const idx: number = tabs.value.findIndex(t => t.id === activeTabId.value)
    if (idx < 0) return
    const prev: number = (idx - 1 + tabs.value.length) % tabs.value.length
    activeTabId.value = tabs.value[prev].id
  }

  function moveTab(fromIndex: number, toIndex: number): void {
    if (fromIndex === toIndex) return
    const [tab] = tabs.value.splice(fromIndex, 1)
    tabs.value.splice(toIndex, 0, tab)
  }

  // Track subscribed directories per tab
  const tabSubs: Map<string, string> = new Map() // tabId -> subscribedPath

  // --- Navigation ---
  async function navigate(path: string, addToHistory: boolean = true, { remote }: RemoteFlag = {}): Promise<void> {
    path = path === '__shared__/' ? path : normalizeDirPath(path)

    // Exit search mode when navigating
    if (searchMode.value) {
      searchMode.value = false
      searchResults.value = []
      searchQuery.value = ''
    }

    // Unsubscribe old directory for this tab
    const tabId: string = activeTabId.value
    const oldSub = tabSubs.get(tabId)
    if (oldSub && oldSub !== path) {
      ws.request('unsubscribe.directory', { path: oldSub }).catch(() => {})
      tabSubs.delete(tabId)
    }

    currentPath.value = path
    clearSelection()
    searchQuery.value = ''

    if (addToHistory) {
      history.value = history.value.slice(0, historyIndex.value + 1)
      history.value.push(path)
      historyIndex.value = history.value.length - 1
    }

    // Subscribe to new directory
    if (path !== '__shared__/' && !isTrashLocation(path)) {
      tabSubs.set(tabId, path)
      ws.request('subscribe.directory', { path }).catch(() => {})
    }

    await loadFiles(path)
  }

  async function loadFiles(path: string): Promise<void> {
    const cached = cache.get(path)
    if (cached && Date.now() - cached.timestamp < CACHE_TTL) {
      files.value = cached.data
      fetchFiles(path).then(data => {
        if (data && currentPath.value === path) {
          files.value = data
          cache.set(path, { data, timestamp: Date.now() })
        }
      }).catch(() => {})
      return
    }

    loading.value = true
    error.value = null
    try {
      const data = await fetchFiles(path)
      if (data && currentPath.value === path) {
        files.value = data
        cache.set(path, { data, timestamp: Date.now() })
      }
    } catch (e: any) {
      if (currentPath.value === path && parseTrashLocation(path)?.id && e?.response?.status === 404) {
        await navigate(TRASH_ROOT_LOCATION)
        return
      }
      error.value = te(e)
    } finally {
      loading.value = false
    }
  }

  async function fetchFiles(path: string): Promise<FileListItem[]> {
    if (isTrashLocation(path)) {
      const location = parseTrashLocation(path)
      if (!location?.id) {
        const entries: FileListItem[] = []
        for (let offset = 0; ; offset += 500) {
          const res = await api.get<{ files?: FileListItem[] }>('/trash/', {
            params: { limit: 500, offset },
          })
          const page = res.data.files || []
          entries.push(...page)
          if (page.length < 500) break
        }
        const list = entries.flatMap(file => {
          if (!file.trash_id) return []
          trashEntryNames.set(file.trash_id, file.name)
          return {
            ...file,
            path: trashItemLocation(file.trash_id, file.relative_path || '/', file.is_dir),
            last_modified: file.deleted_at || file.last_modified,
          }
        })
        registerThumbnails(list)
        await useServiceWorker().flush()
        return list
      }
      const res = await api.get<{ files?: FileListItem[]; trash?: { id: string; name: string } }>(
        `/trash/${encodeURIComponent(location.id)}/list`,
        { params: { path: location.relativePath } },
      )
      const trashID = location.id
      if (res.data.trash?.name) trashEntryNames.set(trashID, res.data.trash.name)
      const list = (res.data.files || []).map(file => ({
        ...file,
        path: trashItemLocation(trashID, file.relative_path),
      }))
      registerThumbnails(list)
      await useServiceWorker().flush()
      return list
    }
    if (path === '__shared__/') {
      const res = await api.get<Array<{
        id: number
        share_id: string
        file_name: string
        file_size: number
        content_type: string
        owner_username?: string
        permission: string
        expires_at?: string | null
        created_at: string
      }>>('/file/shared')
      const items = res.data || []
      return items.map(item => ({
        name: item.file_name + (item.owner_username ? ` (${item.owner_username})` : ''),
        path: '__shared__/' + item.share_id,
        is_dir: false,
        size: item.file_size,
        content_type: item.content_type,
        created_at: item.created_at,
        last_modified: item.created_at,
        _shareId: item.share_id,
        _shareDbId: item.id,
        _sharedBy: item.owner_username,
        _permission: item.permission as 'read' | 'write',
        _expiresAt: item.expires_at,
        _originalName: item.file_name,
      }))
    }
    const res = await api.get<{ files?: FileListItem[] }>('/file/', { params: { path } })
    const list = res.data.files || []
    registerThumbnails(list)
    await useServiceWorker().flush()
    return list
  }

  function notifyDecryptUnavailable(): void {
    message.warning(t('preview.decrypt_unavailable'))
  }

  /** Register thumbnails with the Service Worker for client-side decryption. */
  function registerThumbnails(list: FileListItem[]): void {
    const sw = useServiceWorker()
    for (const file of list) {
      if (!file.thumbnail_url || !file.thumbnail_dek) continue
      const decryptUrl = sw.registerDecrypt({
        url: file.thumbnail_url,
        size: 0,
        chunkSize: 0,
        contentType: 'image/webp',
        filename: '',
        dek: file.thumbnail_dek,
      })
      if (!decryptUrl) continue
      file.thumbnail_url = decryptUrl
      file.thumbnail_dek = undefined
    }
  }

  function goBack(): void {
    if (!canGoBack.value) return
    historyIndex.value--
    navigate(history.value[historyIndex.value], false)
  }

  function goForward(): void {
    if (!canGoForward.value) return
    historyIndex.value++
    navigate(history.value[historyIndex.value], false)
  }

  function goUp(): void {
    if (!canGoUp.value) return
    if (isTrash.value) {
      navigate(trashParentLocation(currentPath.value))
      return
    }
    const parts: string[] = currentPath.value.replace(/\/$/, '').split('/')
    parts.pop()
    const parent: string = parts.join('/') + '/'
    navigate(parent === '/' ? '/' : parent)
  }

  function refresh(): Promise<void> {
    cache.delete(currentPath.value)
    return loadFiles(currentPath.value)
  }

  function invalidateCache(): void {
    cache.delete(currentPath.value)
  }

  async function reloadCurrentDir(): Promise<void> {
    invalidateCache()
    await refresh()
  }

  function getFileByPath(path: string): FileListItem | undefined {
    return files.value.find(f => f.path === path)
  }

  // Selection. A regular selection can contain one item without entering the
  // explicit batch mode used by touch input and modifier-assisted selection.
  const selectMode: Ref<boolean> = ref(false)

  function selectFile(path: string, event?: MouseEvent | KeyboardEvent): void {
    const ctrl: boolean = !!(event && ('ctrlKey' in event ? event.ctrlKey : false)) || !!(event && ('metaKey' in event ? event.metaKey : false))
    const shift: boolean = !!(event && ('shiftKey' in event ? event.shiftKey : false))

    if (shift && lastSelectedIndex.value >= 0) {
      const currentIndex: number = sortedFiles.value.findIndex(f => f.path === path)
      const start: number = Math.min(lastSelectedIndex.value, currentIndex)
      const end: number = Math.max(lastSelectedIndex.value, currentIndex)
      selectedFiles.value = sortedFiles.value.slice(start, end + 1).map(f => f.path)
    } else if (ctrl) {
      const idx: number = selectedFiles.value.indexOf(path)
      if (idx >= 0) {
        selectedFiles.value.splice(idx, 1)
      } else {
        selectedFiles.value.push(path)
      }
    } else {
      selectedFiles.value = [path]
    }

    lastSelectedIndex.value = sortedFiles.value.findIndex(f => f.path === path)
  }

  function selectAll(): void {
    selectMode.value = true
    selectedFiles.value = sortedFiles.value.map(f => f.path)
  }

  function clearSelection(): void {
    selectMode.value = false
    selectedFiles.value = []
    lastSelectedIndex.value = -1
  }

  function enterSelectMode(path?: string): void {
    selectMode.value = true
    if (path && !selectedFiles.value.includes(path)) {
      selectedFiles.value = [path]
    }
    if (path) lastSelectedIndex.value = sortedFiles.value.findIndex(file => file.path === path)
  }

  function exitSelectMode(): void {
    clearSelection()
  }

  function toggleSelect(path: string): void {
    const idx: number = selectedFiles.value.indexOf(path)
    if (idx >= 0) {
      selectedFiles.value.splice(idx, 1)
    } else {
      selectedFiles.value.push(path)
      lastSelectedIndex.value = sortedFiles.value.findIndex(file => file.path === path)
    }
  }

  // Clipboard
  function buildClipboardItems(): ClipboardItem[] {
    return selectedFiles.value.map(path => {
      const file = getFileByPath(path)
      return { path, is_dir: file?.is_dir || false, name: file?.name || '' }
    })
  }

  function copySelected(): void {
    clipboard.value = {
      items: buildClipboardItems(),
      mode: 'copy',
    }
  }

  function cutSelected(): void {
    clipboard.value = {
      items: buildClipboardItems(),
      mode: 'cut',
    }
  }

  async function transcodeFile(file: FileListItem, profile: 'video-720p' | 'audio-mp3'): Promise<void> {
    try {
      await api.post('/file/transcode', { path: file.path, profile })
      message.success(t('transcode.queued', { name: file.name }))
    } catch (e: any) {
      message.error(te(e))
    }
  }

  async function paste(): Promise<void> {
    if (isTrash.value) return
    if (clipboard.value.items.length === 0) return
    const mode = clipboard.value.mode
    const results = await Promise.allSettled(clipboard.value.items.map(async item => {
      const name: string = item.is_dir ? item.name.replace(/\/+$/, '') : item.name
      const dstPath: string = currentPath.value + name + (item.is_dir ? '/' : '')
      const apiUrl = mode === 'copy' ? '/file/copy' : '/file/move'
      const apiData = { src_path: item.path, dst_path: dstPath, is_dir: item.is_dir }
      try {
        await api.post(apiUrl, apiData)
      } catch (e: any) {
        const queued = await maybeQueuePendingOp(e, {
          apiUrl,
          apiMethod: 'post',
          apiData,
          type: mode === 'copy' ? 'copy' : 'move',
          description: pendingDescription(mode === 'copy' ? 'copy' : 'move', item.name),
        })
        if (!queued) throw e
      }
    }))

    const failed = results.filter(r => r.status === 'rejected')
    if (failed.length > 0) {
      message.error(t('paste.partial_failed', { n: failed.length }))
    }

    if (mode === 'cut') {
      clipboard.value = { items: [], mode: null }
    }

    await reloadCurrentDir()
  }

  // File operations
  async function createFolder(): Promise<void> {
    if (isTrash.value) return
    const name = await showPrompt(t('dialog.new_folder_name'))
    if (!name) return

    try {
      await api.post('/file/mkdir', { path: currentPath.value + name })
      await reloadCurrentDir()
    } catch (e: any) {
      const queued = await maybeQueuePendingOp(e, {
        apiUrl: '/file/mkdir',
        apiMethod: 'post',
        apiData: { path: currentPath.value + name },
        type: 'mkdir',
        description: pendingDescription('mkdir', name),
      })
      if (queued) return
      console.error('Create folder failed:', e)
    }
  }

  function startRename(): void {
    if (selectedFiles.value.length !== 1) return
    renamingFile.value = selectedFiles.value[0]
  }

  function cancelRename(): void {
    renamingFile.value = null
  }

  async function rename(oldPath: string, newName: string, isDir: boolean): Promise<void> {
    const parts: string[] = oldPath.replace(/\/$/, '').split('/')
    parts[parts.length - 1] = newName
    const newPath: string = parts.join('/') + (isDir ? '/' : '')

    try {
      await api.post('/file/rename', {
        old_path: oldPath,
        new_path: newPath,
        is_dir: isDir,
      })
      renamingFile.value = null
      await reloadCurrentDir()
    } catch (e: any) {
      const queued = await maybeQueuePendingOp(e, {
        apiUrl: '/file/rename',
        apiMethod: 'post',
        apiData: {
          old_path: oldPath,
          new_path: newPath,
          is_dir: isDir,
        },
        type: 'rename',
        description: pendingDescription('rename', newName),
      })
      if (queued) {
        renamingFile.value = null
        return
      }
      console.error('Rename failed:', e)
      throw e
    }
  }

  async function deleteSelected(permanent: boolean = false): Promise<void> {
    if (selectedFiles.value.length === 0) return
    const paths: string[] = [...selectedFiles.value]
    const count: number = paths.length
    const failedPaths: string[] = []
    const wasSelectMode: boolean = selectMode.value

    if (isShared.value) {
      if (!await showConfirm(t('share.stop_share'), t('share.confirm_stop'), { icon: 'warning', positiveType: 'error' })) return
      for (const path of paths) {
        const file = getFileByPath(path)
        if (!file?._shareDbId) continue
        try {
          await api.delete('/file/share/' + file._shareDbId)
        } catch (e: any) {
          console.error('Stop share failed:', e)
          failedPaths.push(path)
        }
      }
    } else if (isTrash.value || permanent) {
      if (!await showConfirm(t('dialog.permanent_delete_title'), t('dialog.confirm_permanent_delete', { n: count }), { icon: 'warning', positiveType: 'error' })) return
      for (const path of paths) {
        const file = getFileByPath(path)
        if (!file?.inode) {
          failedPaths.push(path)
          continue
        }
        const apiUrl = isTrash.value && file.trash_id
          ? `/trash/${encodeURIComponent(file.trash_id)}`
          : '/file/delete'
        const apiData = isTrash.value && file.trash_id
          ? { path: file.relative_path || '/', expected_inode: file.inode }
          : { path, permanent: true, expected_inode: file.inode }
        try {
          await api.delete(apiUrl, { params: apiData })
        } catch (e: any) {
          const queued = await maybeQueuePendingOp(e, {
            apiUrl,
            apiMethod: 'delete',
            apiData,
            type: 'deleteTrash',
            description: pendingDescription('deleteTrash', file?.name || path),
          })
          if (queued) continue
          console.error('Permanent delete failed:', e)
          failedPaths.push(path)
        }
      }
    } else {
      if (!await showConfirm(t('dialog.delete_title'), t('dialog.confirm_delete', { n: count }), { icon: 'warning', positiveType: 'error' })) return
      await Promise.all(paths.map(async path => {
        const file = getFileByPath(path)
        if (!file?.inode) {
          failedPaths.push(path)
          return
        }
        const apiData = { path, expected_inode: file.inode }
        try {
          await api.post('/trash/', apiData)
        } catch (e: any) {
          const queued = await maybeQueuePendingOp(e, {
            apiUrl: '/trash/',
            apiMethod: 'post',
            apiData,
            type: 'delete',
            description: pendingDescription('delete', file?.name || path),
          })
          if (!queued) {
            console.error('Delete failed:', e)
            failedPaths.push(path)
          }
        }
      }))
    }

    await reloadCurrentDir()
    if (isTrash.value && error.value && parseTrashLocation(currentPath.value)?.id) {
      await navigate(TRASH_ROOT_LOCATION)
    }
    if (failedPaths.length > 0) {
      selectedFiles.value = failedPaths.filter(path => getFileByPath(path) !== undefined)
      selectMode.value = wasSelectMode && selectedFiles.value.length > 0
      lastSelectedIndex.value = selectedFiles.value.length > 0
        ? sortedFiles.value.findIndex(file => file.path === selectedFiles.value[selectedFiles.value.length - 1])
        : -1
      message.error(t('files.delete_failed', { n: failedPaths.length }))
      return
    }
    clearSelection()
  }

  async function restoreSelected(): Promise<void> {
    if (!isTrash.value || selectedFiles.value.length === 0) return
    let applyAction: 'skip' | 'replace' | 'rename' | 'merge' | undefined
    let applyNestedAction: 'skip' | 'replace' | 'rename' | undefined
    for (const path of selectedFiles.value) {
      const file = getFileByPath(path)
      if (!file?.trash_id || !file.inode) continue
      const apiUrl = `/trash/${encodeURIComponent(file.trash_id)}/restore`
      const baseData = { path: file.relative_path || '/', expected_inode: file.inode }
      let conflict = applyAction
      let nestedConflict = applyNestedAction
      try {
        while (true) {
          const apiData = { ...baseData, conflict, nested_conflict: nestedConflict }
          try {
            await api.post(apiUrl, apiData)
            break
          } catch (e: any) {
            const detail = e?.response?.data
            if (e?.response?.status === 400 && detail?.error === 'merge_not_available' && conflict === 'merge') {
              conflict = undefined
              if (applyAction === 'merge') applyAction = undefined
              continue
            }
            if (e?.response?.status !== 409 || detail?.error !== 'restore_conflict') {
              const queued = await maybeQueuePendingOp(e, {
                apiUrl,
                apiMethod: 'post',
                apiData,
                type: 'restore',
                description: pendingDescription('restore', file.name),
              })
              if (!queued) throw e
              break
            }
            const nested = conflict === 'merge' && Array.isArray(detail.conflicts) && detail.conflicts.length > 0
            const incoming = nested ? detail.conflicts[0] : detail.incoming
            const existing = nested ? {
              name: detail.conflicts[0].existing_name,
              size: detail.conflicts[0].existing_size,
              is_dir: detail.conflicts[0].existing_is_dir,
            } : detail.existing
            const decision = await showDuplicateDialog({
              title: t('upload.duplicate_title'),
              incomingName: incoming?.name || file.name,
              incomingSize: incoming?.size || file.size,
              existingName: existing?.name || file.name,
              existingSize: existing?.size || 0,
              existingIsDir: !!existing?.is_dir,
              allowMerge: !nested && !!detail.merge_available,
            })
            if (!decision) return
            if (nested) {
              if (decision.action === 'merge') continue
              nestedConflict = decision.action
              if (decision.applyToAll) applyNestedAction = nestedConflict
            } else {
              conflict = decision.action
              if (decision.applyToAll) applyAction = conflict
            }
          }
        }
      } catch (e: any) {
        console.error('Restore failed:', e)
        message.error(te(e))
      }
    }
    clearSelection()
    await reloadCurrentDir()
    if (error.value && parseTrashLocation(currentPath.value)?.id) {
      await navigate(TRASH_ROOT_LOCATION)
    }
  }

  async function emptyTrash(): Promise<void> {
    if (!await showConfirm(t('dialog.empty_trash_title'), t('dialog.confirm_empty_trash'), { icon: 'warning', positiveType: 'error' })) return
    try {
      await api.delete('/trash/')
    } catch (e: any) {
      // Emptying Trash is an unbounded destructive command. Replaying it
      // after connectivity returns could also delete entries created after
      // the user's confirmation, so it must never enter the offline queue.
      console.error('Empty trash failed:', e)
      message.error(te(e))
    }
    await reloadCurrentDir()
  }

  function openSelected(): void {
    if (selectedFiles.value.length !== 1) return
    const file = getFileByPath(selectedFiles.value[0])
    if (!file) return

    const name: string = file._originalName || file.name
    const viewerType = getViewerType(name)
    if (file.is_dir) {
      navigate(file.path)
    } else if (viewerType) {
      openViewer(file)
    } else {
      downloadFile(file._shareId ? file : file.path)
    }
  }

  const imageExts: Set<string> = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'ico', 'avif'])
  const videoExts: Set<string> = new Set(['mp4', 'webm', 'mov', 'm4v'])
  const audioExts: Set<string> = new Set(['mp3', 'wav', 'ogg', 'aac', 'm4a', 'flac', 'opus'])
  const fontExts: Set<string> = new Set(['ttf', 'otf', 'woff', 'woff2'])
  const archiveExts: Set<string> = new Set(['zip'])
  const notebookExts: Set<string> = new Set(['ipynb'])
  const textExts: Set<string> = new Set([
    'txt', 'json', 'yaml', 'yml', 'xml', 'log', 'ini', 'conf', 'cfg',
    'js', 'ts', 'jsx', 'tsx', 'vue', 'html', 'htm', 'css', 'scss', 'less',
    'sass', 'styl', 'pug', 'coffee', 'liquid',
    'go', 'py', 'rb', 'java', 'c', 'cpp', 'h', 'hpp', 'rs', 'swift', 'kt',
    'd', 'pas', 'f90', 'f95', 'f', 'v', 'sv', 'vhd', 'vhdl',
    'sh', 'bash', 'zsh', 'fish', 'ps1', 'bat', 'cmd',
    'pl', 'pm', 'tcl',
    'sql', 'proto', 'wast', 'wat',
    'toml', 'properties', 'env', 'gitignore', 'dockerignore', 'editorconfig',
    'dockerfile', 'makefile', 'cmake',
    'tex', 'latex', 'textile', 'diff', 'patch',
    'php', 'lua', 'r', 'scala', 'clj', 'cljs', 'erl', 'hrl',
    'ex', 'exs', 'hs', 'lhs', 'ml', 'mli', 'fs', 'fsx', 'fsi',
    'jl', 'elm', 'groovy', 'gradle',
    'scm', 'rkt', 'lisp', 'cl',
    'm', 'nb', 'vb', 'bas', 'pp', 'epp', 'cr', 'nim',
    'sparql', 'ttl', 'nt', 'xq', 'xquery',
    'mod', 'sum',
  ])
  const extToLanguage: Record<string, string> = {
    // Web
    js: 'javascript', ts: 'typescript', jsx: 'javascript', tsx: 'typescript',
    vue: 'vue', html: 'html', htm: 'html', css: 'css', scss: 'scss', less: 'less', liquid: 'liquid',
    sass: 'sass', styl: 'stylus', pug: 'pug', coffee: 'coffeescript',
    // Systems / compiled
    go: 'go', py: 'python', rb: 'ruby', java: 'java',
    c: 'c', cpp: 'cpp', h: 'c', hpp: 'cpp', rs: 'rust', swift: 'swift', kt: 'kotlin',
    d: 'd', pas: 'pascal', f90: 'fortran', f95: 'fortran', f: 'fortran',
    v: 'verilog', sv: 'verilog', vhd: 'vhdl', vhdl: 'vhdl',
    // Shell / scripting
    sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
    ps1: 'powershell', bat: 'powershell',
    pl: 'perl', pm: 'perl', tcl: 'tcl',
    // Data / config
    json: 'json', yaml: 'yaml', yml: 'yaml', xml: 'xml', toml: 'toml',
    ini: 'ini', properties: 'properties', conf: 'nginx',
    sql: 'sql', proto: 'protobuf', wast: 'wast', wat: 'wast',
    // Markup / docs
    md: 'markdown', tex: 'stex', latex: 'stex', textile: 'textile',
    diff: 'diff', patch: 'diff',
    // Languages
    php: 'php', lua: 'lua', r: 'r', scala: 'scala',
    clj: 'clojure', cljs: 'clojure', erl: 'erlang', hrl: 'erlang',
    ex: 'elixir', exs: 'elixir', hs: 'haskell', lhs: 'haskell',
    ml: 'ocaml', mli: 'ocaml', fs: 'fsharp', fsx: 'fsharp', fsi: 'fsharp',
    jl: 'julia', elm: 'elm', groovy: 'groovy', gradle: 'groovy',
    cmake: 'cmake', dockerfile: 'dockerfile',
    scm: 'scheme', rkt: 'scheme', lisp: 'commonlisp', cl: 'commonlisp',
    m: 'octave', nb: 'mathematica', vb: 'vb', bas: 'vb',
    pp: 'puppet', epp: 'puppet',
    cr: 'crystal', nim: 'nim',
    // Misc
    sparql: 'sparql', ttl: 'turtle', nt: 'ntriples',
    xq: 'xquery', xquery: 'xquery',
  }

  const appIcons: Record<string, any> = {
    image: ICONS.image,
    video: ICONS.video,
    text: ICONS.text,
    pdf: ICONS.pdf,
    audio: ICONS.audio,
    markdown: ICONS.markdown,
    csv: ICONS.text,
    font: ICONS.file,
    archive: ICONS.archive,
    notebook: ICONS.code,
  }

  function parseFileName(name: string): { baseName: string; ext: string } {
    const baseName: string = name.toLowerCase()
    return {
      baseName,
      ext: baseName.split('.').pop()!,
    }
  }

  function isImageFile(name: string): boolean {
    const { ext } = parseFileName(name)
    return imageExts.has(ext)
  }

  function isVideoFile(name: string): boolean {
    const { ext } = parseFileName(name)
    return videoExts.has(ext)
  }

  function isTextFile(name: string): boolean {
    const { ext, baseName } = parseFileName(name)
    return textExts.has(ext) || textExts.has(baseName)
  }

  function isPdfFile(name: string): boolean {
    const { ext } = parseFileName(name)
    return ext === 'pdf'
  }

  function getLanguage(name: string): string | null {
    const { ext, baseName } = parseFileName(name)
    return extToLanguage[ext] || extToLanguage[baseName] || null
  }

  function getViewerType(name: string): ViewerType | null {
    const { ext } = parseFileName(name)
    if (isVideoFile(name)) return 'video'
    if (audioExts.has(ext)) return 'audio'
    if (ext === 'md') return 'markdown'
    if (ext === 'csv') return 'csv'
    if (notebookExts.has(ext)) return 'notebook'
    if (fontExts.has(ext)) return 'font'
    if (archiveExts.has(ext)) return 'archive'
    if (isTextFile(name)) return 'text'
    if (isImageFile(name)) return 'image'
    if (isPdfFile(name)) return 'pdf'
    return null
  }

  function appWindowId(filePath: string): string {
    return 'app-' + filePath
  }

  function findApp(windowId: string): AppWindowState | undefined {
    return appWindows.value.find(p => p.windowId === windowId)
  }

  async function openViewer(file: FileListItem, { forceType, remote }: { forceType?: ViewerType; remote?: boolean } = {}): Promise<void> {
    const windowId: string = appWindowId(file.path) + (forceType ? `-${forceType}` : '')
    const fileName: string = file._originalName || file.name
    const viewerType = getViewerType(fileName)
    if (!viewerType && !forceType) {
      downloadFile(file._shareId ? file : file.path)
      return
    }
    const type: ViewerType = forceType || viewerType || 'image'
    const shouldOpenTextFully: boolean =
      type !== 'notebook' &&
      ['text', 'markdown', 'csv'].includes(type) &&
      (file.size || 0) > TEXT_CHUNK_SIZE

    // If already open, just bring to front
    const existing = findApp(windowId)
    if (existing) {
      wm.bringToFront(windowId)
      const win = wm.findWindow(windowId)
      if (win) win.minimized = false
      return
    }

    if (shouldOpenTextFully) {
      const sizeStr: string = formatFileSize(file.size || 0)
      const ok = await showConfirm(
        t('dialog.text_open_title'),
        t('dialog.text_open_body', { size: sizeStr }),
      )
      if (!ok) return
    }

    const state = reactive<AppWindowState>({
      windowId,
      file,
      url: '',
      blob: null,
      type,
      content: null,
      language: null,
      editing: false,
      dirty: false,
      saving: false,
      openedAt: Date.now(),
    })

    appWindows.value.push(state)

    wm.openWindow({
      id: windowId,
      title: file.name,
      icon: appIcons[type] || appIcons.image,
      type: 'viewer',
      data: { filePath: file.path },
    }, { remote })

    const blobTypes: string[] = ['video', 'image', 'pdf', 'audio', 'font', 'archive']
    const textTypes: string[] = ['text', 'markdown', 'csv', 'notebook']

    // Size guard for non-chunkable types
    if (NON_CHUNKABLE_TYPES.has(type) && file.size > getLargeFileLimit()) {
      const sizeStr: string = formatFileSize(file.size)
      const limitStr: string = formatFileSize(getLargeFileLimit())
      const ok = await showConfirm(
        t('dialog.large_file_title'),
        t('dialog.large_file_body', { size: sizeStr, limit: limitStr }),
      )
      if (!ok) {
        // User declined — close the window we just opened
        appWindows.value = appWindows.value.filter(a => a.windowId !== windowId)
        wm.closeWindow(windowId)
        return
      }
    }

    if (blobTypes.includes(type)) {
      try {
        // The shared helper waits for the Service Worker FIFO barrier before
        // returning the synthetic URL. Assigning it earlier lets an <img>,
        // <video> or iframe race ahead of the registry message and see 404.
        const { decryptUrl } = await registerFileDecrypt(file)
        ;(state as any)._decryptUrl = decryptUrl

        state.url = decryptUrl
      } catch {
        state.url = ''
      }
    } else if (textTypes.includes(type)) {
      state.language = getLanguage(file.name)
      ;(state as any).totalSize = file.size || 0
      ;(state as any).baseSize = file.size || 0

      // Register SW decrypt URL for text files
      let decryptUrl: string | null = null
      try {
        // Text fetches start immediately, so the same Service Worker barrier
        // is required here as for media elements.
        const registered = await registerFileDecrypt(file)
        const { dek, generation } = registered.access
        state._dek = dek
        state._generation = generation
        decryptUrl = registered.decryptUrl
        ;(state as any)._decryptUrl = decryptUrl
      } catch {
        state.content = ''
        return
      }

      if (type === 'notebook') {
        // Notebook: JSON needs full parse
        ;(state as any).chunked = false
        try {
          const res = await fetch(decryptUrl!)
          const text: string = await res.text()
          state.content = text
          ;(state as any).baseSize = new TextEncoder().encode(text).length
          ;(state as any).totalSize = (state as any).baseSize
        } catch {
          state.content = ''
          ;(state as any).baseSize = 0
        }
      } else if (shouldOpenTextFully) {
        ;(state as any).chunked = false
        ;(state as any).page = 0
        ;(state as any).totalPages = 1
        ;(state as any).isFullyLoaded = true
        try {
          const res = await fetch(decryptUrl!)
          const text: string = await res.text()
          state.content = text
          const byteLength = new TextEncoder().encode(text).byteLength
          ;(state as any).baseSize = byteLength
          ;(state as any).totalSize = byteLength
        } catch {
          state.content = ''
          ;(state as any).baseSize = 0
          ;(state as any).totalSize = 0
        }
      } else {
        // Range-based chunked loading via SW
        ;(state as any).chunked = true
        ;(state as any).page = 0
        ;(state as any).pageByteStart = 0
        ;(state as any).pageByteEnd = 0
        ;(state as any).pageMap = [{ byteStart: 0 }]
        ;(state as any).totalPages = Math.max(1, Math.ceil((file.size || 1) / TEXT_CHUNK_SIZE))
        ;(state as any).isFullyLoaded = false
        try {
          const result: TextChunkResult = await fetchTextChunk(decryptUrl!, 0, (state as any).totalSize as number)
          state.content = result.text
          ;(state as any).pageByteEnd = result.byteLength
          ;(state as any).isFullyLoaded = result.nextByteStart >= ((state as any).totalSize as number)
          if ((state as any).isFullyLoaded) {
            ;(state as any).totalPages = 1
            ;(state as any).baseSize = new TextEncoder().encode(result.text).byteLength
          } else {
            ;((state as any).pageMap as PageMapEntry[]).push({ byteStart: result.nextByteStart })
          }
        } catch {
          state.content = ''
        }
      }
    }
  }

  async function saveViewer(windowId: string): Promise<void> {
    const state = findApp(windowId)
    if (!state || state.saving || isTrashLocation(state.file.path)) return
    state.saving = true
    try {
      const newContent: string = state.content!
      const newBytes: Uint8Array = new TextEncoder().encode(newContent)
      const generation = await writeEncryptedFile(
        state.file.path,
        newBytes.slice().buffer,
        state.file.content_type || 'text/plain',
        {
          dek: state._dek,
          expectedGeneration: state._generation,
          shareId: state.file._shareId,
        },
      )
      if (generation) state._generation = generation
      ;(state as any).baseSize = newBytes.length
      state.editing = false
      state.dirty = false
    } catch (e: any) {
      console.error('Save failed:', e)
    } finally {
      state.saving = false
    }
  }

  function closeViewer(windowId: string, { remote }: RemoteFlag = {}): void {
    const state = findApp(windowId)
    if (state) {
      if (state.openedAt) {
        const duration: number = Date.now() - state.openedAt
        api.post('/audit/', {
          path: state.file.path,
          duration_ms: duration,
          type: state.type,
        }).catch(() => {})
      }
      if (state.url && state.url.startsWith('blob:')) {
        URL.revokeObjectURL(state.url)
      }
      // Unregister SW decrypt mapping
      if (state._decryptUrl) {
        useServiceWorker().unregisterDecrypt(state._decryptUrl)
      }
    }
    wm.closeWindow(windowId, { remote })
    const idx: number = appWindows.value.findIndex(p => p.windowId === windowId)
    if (idx >= 0) appWindows.value.splice(idx, 1)
  }

  async function downloadFile(pathOrFile: string | FileListItem): Promise<void> {
    let objectUrl: string | null = null
    let decryptUrl: string | null = null
    try {
      const sw = useServiceWorker()
      const path: string = typeof pathOrFile === 'string' ? pathOrFile : pathOrFile.path
      const shareId: string | undefined = typeof pathOrFile === 'object' ? pathOrFile._shareId : undefined
      const trash = parseTrashLocation(path)
      const res = shareId
        ? await api.get<FileAccessResponse>('/file/shared/' + shareId)
        : trash?.id
          ? await api.get<FileAccessResponse>(`/trash/${encodeURIComponent(trash.id)}/access`, {
              params: { path: trash.relativePath },
            })
        : await api.get<FileAccessResponse>('/file/access', { params: { path } })
      const { url, size, name, content_type, chunk_size, dek, content_hash } = res.data
      decryptUrl = sw.registerDecrypt({
        url, size, chunkSize: chunk_size, contentType: content_type,
        filename: name, dek, download: true, contentHash: content_hash,
      })
      if (!decryptUrl) {
        notifyDecryptUnavailable()
        return
      }
      await sw.flush()
      const resp = await fetch(decryptUrl)
      if (!resp.ok) {
        throw new Error(`Download fetch failed: ${resp.status}`)
      }
      const blob = await resp.blob()
      objectUrl = URL.createObjectURL(blob)
      const a: HTMLAnchorElement = document.createElement('a')
      a.href = objectUrl
      a.download = name
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
    } catch (e: any) {
      console.error('Download failed:', e)
    } finally {
      if (objectUrl) {
        setTimeout(() => URL.revokeObjectURL(objectUrl!), 60000)
      }
      if (decryptUrl) {
        useServiceWorker().unregisterDecrypt(decryptUrl)
      }
    }
  }

  // Listen for task updates — update processing files in file list with progress/phase
  ws.on('task.update', (data: TaskUpdateEvent) => {
    if (!data.task_id) return
    for (const tab of tabs.value) {
      for (const file of tab.files) {
        if (file.task_id === data.task_id) {
          file.task_progress = data.progress
          file.task_phase = data.phase
          file.status = data.status === 'completed' ? 'ready' : file.status
        }
      }
    }
  })

  // Listen for directory change push events
  ws.on('dir.changed', ({ path }: { path: string }) => {
    const changedPath = path === '__shared__/' ? path : normalizeDirPath(path)
    // Invalidate cache for this directory
    cache.delete(changedPath)
    // Refresh all tabs viewing this directory
    for (const tab of tabs.value) {
      const tabPath = tab.path === '__shared__/' ? tab.path : normalizeDirPath(tab.path)
      if (tabPath === changedPath) {
        loadFilesForTab(tab, changedPath)
      }
    }
  })

  ws.on('trash.changed', () => {
    for (const key of cache.keys()) {
      if (isTrashLocation(key)) cache.delete(key)
    }
    for (const tab of tabs.value) {
      if (isTrashLocation(tab.path)) loadFilesForTab(tab, tab.path)
    }
  })

  // Load files directly into a specific tab (works for any tab, not just active)
  async function loadFilesForTab(tab: FileTab, path: string): Promise<void> {
    try {
      const data = await fetchFiles(path)
      if (data) {
        tab.files = data
        cache.set(path, { data, timestamp: Date.now() })
      }
    } catch (e: any) {
      if (!isTrashLocation(path) || !parseTrashLocation(path)?.id || e?.response?.status !== 404) return
      // Another device may have restored or emptied the entry currently open
      // in this tab. Move that tab back to the still-valid Trash root instead
      // of leaving stale children on screen.
      tab.path = TRASH_ROOT_LOCATION
      tab.selectedFiles = []
      tab.lastSelectedIndex = -1
      tab.history = tab.history.slice(0, tab.historyIndex + 1)
      tab.history.push(TRASH_ROOT_LOCATION)
      tab.historyIndex = tab.history.length - 1
      try {
        const rootData = await fetchFiles(TRASH_ROOT_LOCATION)
        tab.files = rootData
        tab.error = null
        cache.set(TRASH_ROOT_LOCATION, { data: rootData, timestamp: Date.now() })
      } catch (rootError: any) {
        tab.files = []
        tab.error = te(rootError)
      }
    }
  }

  // Re-subscribe directories and reload files after WS (re)connection
  let initialized: boolean = false
  ws.onReconnect(() => {
    // Re-subscribe all tabs
    for (const [, subPath] of tabSubs) {
      if (subPath) {
        ws.request('subscribe.directory', { path: subPath }).catch(() => {})
      }
    }
    // On first connect, load files for the initial tab
    if (!initialized && tabs.value.length > 0) {
      initialized = true
      const tab = tabs.value.find(t => t.id === activeTabId.value)
      if (tab) loadFiles(tab.path)
    }
  })

  // Initialize
  function init(_opts?: { skipRestore?: boolean }): void {
    if (auth.isLoggedIn) {
      // Reset tabs
      tabs.value = []
      activeTabId.value = ''
      appWindows.value = []
    }
  }

  // Restore tabs from a serialised snapshot
  function restoreTabs(serializedTabs: SerializedTab[], activeIndex?: number): void {
    tabs.value = []
    activeTabId.value = ''

    for (const st of serializedTabs) {
      const id: string = nextTabId()
      const requestedPath = st.path || '/'
      const restoredPath = requestedPath === '__shared__/' ? requestedPath : normalizeDirPath(requestedPath)
      const tab: FileTab = {
        id,
        path: restoredPath,
        files: [],
        selectedFiles: [],
        lastSelectedIndex: -1,
        history: [restoredPath],
        historyIndex: 0,
        viewMode: (st.viewMode as FileTab['viewMode']) || 'icons',
        sortBy: (st.sortBy as FileTab['sortBy']) || 'name',
        sortOrder: (st.sortOrder as FileTab['sortOrder']) || 'asc',
        searchQuery: '',
        loading: false,
        error: null,
      }
      tabs.value.push(tab)
      if (tab.path !== '__shared__/' && !isTrashLocation(tab.path)) {
        tabSubs.set(id, tab.path)
        ws.request('subscribe.directory', { path: tab.path }).catch(() => {})
      }
      loadFilesForTab(tab, tab.path)
    }

    // Set active tab
    const idx: number = typeof activeIndex === 'number' ? Math.min(activeIndex, tabs.value.length - 1) : 0
    if (tabs.value[idx]) {
      activeTabId.value = tabs.value[idx].id
    }
  }

  // Restore viewer windows (re-open files to fetch content)
  function restoreViewers(viewerWindows: ViewerWindowInfo[]): void {
    for (const vw of viewerWindows) {
      const filePath = vw.data?.filePath
      if (!filePath) continue
      const fileName: string = filePath.split('/').pop() || ''
      openViewer({ path: filePath, name: fileName, size: 0, is_dir: false, created_at: '', last_modified: '' }, { remote: true })
    }
  }

  async function viewerNextPage(windowId: string): Promise<void> {
    const state = findApp(windowId)
    if (!state?.chunked || state.page! >= state.totalPages! - 1) return
    const nextIdx: number = state.page! + 1
    const entry = state.pageMap![nextIdx]
    if (!entry || entry.byteStart >= state.totalSize!) return
    try {
      const result: TextChunkResult = await fetchTextChunk(state._decryptUrl!, entry.byteStart, state.totalSize!)
      state.content = result.text
      ;(state as any).page = nextIdx
      ;(state as any).pageByteStart = entry.byteStart
      ;(state as any).pageByteEnd = entry.byteStart + result.byteLength
      if (!state.pageMap![nextIdx + 1] && result.nextByteStart < state.totalSize!) {
        state.pageMap!.push({ byteStart: result.nextByteStart })
      }
      if (result.nextByteStart >= state.totalSize!) {
        ;(state as any).totalPages = nextIdx + 1
      }
    } catch { /* fetch error */ }
  }

  async function viewerPrevPage(windowId: string): Promise<void> {
    const state = findApp(windowId)
    if (!state?.chunked || state.page! <= 0) return
    const prevIdx: number = state.page! - 1
    const entry = state.pageMap![prevIdx]
    if (!entry) return
    try {
      const result: TextChunkResult = await fetchTextChunk(state._decryptUrl!, entry.byteStart, state.totalSize!)
      state.content = result.text
      ;(state as any).page = prevIdx
      ;(state as any).pageByteStart = entry.byteStart
      ;(state as any).pageByteEnd = entry.byteStart + result.byteLength
    } catch { /* fetch error */ }
  }

  return {
    // Tab state
    tabs,
    activeTabId,
    activeTab,
    createTab,
    closeTab,
    switchTab,
    nextTab,
    prevTab,

    // Proxied per-tab state
    currentPath,
    files,
    loading,
    error,
    selectedFiles,
    selectedFile,
    clipboard,
    viewMode,
    sortBy,
    sortOrder,
    iconSize,
    focusPathBar,
    focusSearch,
    renamingFile,
    searchQuery,
    showHidden,
    toggleHidden(): void {
      showHidden.value = !showHidden.value
      localStorage.setItem('domus_show_hidden', showHidden.value ? '1' : '0')
    },
    searchMode,
    searchResults,
    searchLoading,
    performSearch,
    exitSearch,
    showInfoPanel,
    showSidebar,
    sortedFiles,
    pathSegments,
    canGoBack,
    canGoForward,
    canGoUp,
    navigate,
    goBack,
    goForward,
    goUp,
    refresh,
    invalidateCache,
    selectFile,
    selectAll,
    clearSelection,
    selectMode,
    enterSelectMode,
    exitSelectMode,
    toggleSelect,
    copySelected,
    cutSelected,
    transcodeFile,
    paste,
    createFolder,
    startRename,
    cancelRename,
    rename,
    deleteSelected,
    restoreSelected,
    emptyTrash,
    isTrash,
    isTrashRoot,
    isShared,
    openSelected,
    downloadFile,
    appWindows,
    getViewerType,
    getLanguage,
    findApp,
    openViewer,
    saveViewer,
    closeViewer,
    viewerNextPage,
    viewerPrevPage,
    moveTab,
    init,
    restoreTabs,
    restoreViewers,
  }
})
