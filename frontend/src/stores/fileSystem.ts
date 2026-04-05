import { defineStore } from 'pinia'
import { ref, reactive, computed } from 'vue'
import type { Ref, ComputedRef, WritableComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useAuthStore } from './auth'
import { useWindowManagerStore } from './windowManager'
import { useI18n } from '../composables/useI18n'
import { ICONS } from '../composables/useFileIcon'
import { showPrompt, showConfirm } from '../composables/useNativeDialog'
import { usePreferences } from '../composables/usePreferences'
import { useWorkspaceSync } from '../composables/useWorkspaceSync'
import { useServiceWorker } from '../composables/useServiceWorker'
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
} from '../types'

const TEXT_CHUNK_SIZE: number = 256 * 1024 // 256KB — aligns with 4 encryption chunks
const NON_CHUNKABLE_TYPES: Set<string> = new Set(['notebook', 'archive'])

function getLargeFileLimit(): number {
  const { prefs } = usePreferences()
  return prefs.largeFileLimitMB * 1024 * 1024
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
  const sync = useWorkspaceSync()

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
  const showHidden: Ref<boolean> = ref(localStorage.getItem('zephyr_show_hidden') === '1')

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
  const CACHE_TTL: number = 30000

  async function performSearch(query: string): Promise<void> {
    if (!query || query.length < 2) return
    searchMode.value = true
    searchLoading.value = true
    try {
      const res = await ws.request<{ results?: SearchResult[] }>('file.search', { query, limit: 100 })
      searchResults.value = res.results || []
    } catch {
      searchResults.value = []
    } finally {
      searchLoading.value = false
    }
  }

  function exitSearch(): void {
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

  const isTrash: ComputedRef<boolean> = computed(() => currentPath.value === '__trash__/')
  const isShared: ComputedRef<boolean> = computed(() => currentPath.value === '__shared__/')

  const canGoBack: ComputedRef<boolean> = computed(() => historyIndex.value > 0)
  const canGoForward: ComputedRef<boolean> = computed(() => historyIndex.value < history.value.length - 1)
  const canGoUp: ComputedRef<boolean> = computed(() => currentPath.value !== '/' && !isTrash.value && !isShared.value)

  const pathSegments: ComputedRef<PathSegment[]> = computed(() => {
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
    const initialPath: string = path || `/home/${auth.username}/`
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

    // Subscribe to directory changes for this tab
    tabSubs.set(id, initialPath)
    ws.request('subscribe.directory', { path: initialPath }).catch(() => {})

    loadFiles(initialPath)

    if (!remote) {
      sync.emitEvent({ action: 'tab.open', path: initialPath })
    }

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

    if (!remote) {
      sync.emitEvent({ action: 'tab.close', index: idx })
    }

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
      if (!remote) {
        sync.emitEvent({ action: 'tab.switch', index: idx })
      }
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
    path = path || '/'
    if (path !== '/' && !path.endsWith('/')) path += '/'

    // Exit search mode when navigating
    if (searchMode.value) {
      searchMode.value = false
      searchResults.value = []
      searchQuery.value = ''
    }

    // Unsubscribe old directory for this tab
    const tabId: string = activeTabId.value
    const oldSub = tabSubs.get(tabId)
    if (oldSub && oldSub !== path && path !== '__trash__/' && path !== '__shared__/') {
      ws.request('unsubscribe.directory', { path: oldSub }).catch(() => {})
    }

    currentPath.value = path
    selectedFiles.value = []
    searchQuery.value = ''

    if (addToHistory) {
      history.value = history.value.slice(0, historyIndex.value + 1)
      history.value.push(path)
      historyIndex.value = history.value.length - 1
    }

    if (!remote) {
      const tabIndex: number = tabs.value.findIndex(t => t.id === tabId)
      if (tabIndex >= 0) {
        sync.emitEvent({ action: 'tab.navigate', index: tabIndex, path })
      }
    }

    // Subscribe to new directory
    if (path !== '__trash__/' && path !== '__shared__/') {
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
      error.value = te(e)
    } finally {
      loading.value = false
    }
  }

  async function fetchFiles(path: string): Promise<FileListItem[]> {
    if (path === '__trash__/') {
      const items = await ws.request<Array<{
        id: number
        original_path: string
        is_dir: boolean
        size: number
        deleted_at: string
      }>>('trash.list') || []
      return items.map(item => ({
        name: item.original_path.replace(/\/$/, '').split('/').pop()!,
        path: '__trash__/' + item.id,
        is_dir: item.is_dir,
        size: item.size,
        created_at: item.deleted_at,
        last_modified: item.deleted_at,
        _trashId: item.id,
        _originalPath: item.original_path,
      }))
    }
    if (path === '__shared__/') {
      const items = await ws.request<Array<{
        id: number
        share_id: string
        file_name: string
        file_size: number
        content_type: string
        owner_username?: string
        permission: string
        expires_at?: string | null
        created_at: string
      }>>('share.list') || []
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
    const res = await ws.request<{ files?: FileListItem[] }>('file.list', { path })
    const list = res.files || []
    registerThumbnails(list)
    return list
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

  // Selection
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
    selectedFiles.value = sortedFiles.value.map(f => f.path)
  }

  function clearSelection(): void {
    selectedFiles.value = []
    lastSelectedIndex.value = -1
  }

  // Mobile select mode
  const selectMode: Ref<boolean> = ref(false)

  function enterSelectMode(path?: string): void {
    selectMode.value = true
    if (path && !selectedFiles.value.includes(path)) {
      selectedFiles.value = [path]
    }
  }

  function exitSelectMode(): void {
    selectMode.value = false
    clearSelection()
  }

  function toggleSelect(path: string): void {
    const idx: number = selectedFiles.value.indexOf(path)
    if (idx >= 0) {
      selectedFiles.value.splice(idx, 1)
      if (selectedFiles.value.length === 0) selectMode.value = false
    } else {
      selectedFiles.value.push(path)
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

  async function paste(): Promise<void> {
    if (clipboard.value.items.length === 0) return
    const mode = clipboard.value.mode
    const action: string = mode === 'copy' ? 'file.copy' : 'file.move'

    const results = await Promise.allSettled(clipboard.value.items.map(item => {
      const name: string = item.is_dir ? item.name.replace(/\/+$/, '') : item.name
      const dstPath: string = currentPath.value + name + (item.is_dir ? '/' : '')
      return ws.request(action, { src_path: item.path, dst_path: dstPath, is_dir: item.is_dir })
    }))

    const failed = results.filter(r => r.status === 'rejected')
    if (failed.length > 0) {
      const { useMessage } = await import('../composables/useMessage')
      const msg = useMessage()
      msg.error(t('paste.partial_failed', { n: failed.length }))
    }

    if (mode === 'cut') {
      clipboard.value = { items: [], mode: null }
    }

    await reloadCurrentDir()
  }

  // File operations
  async function createFolder(): Promise<void> {
    const name = await showPrompt(t('dialog.new_folder_name'))
    if (!name) return

    try {
      await ws.request('file.mkdir', { path: currentPath.value + name })
      await reloadCurrentDir()
    } catch (e: any) {
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
      await ws.request('file.rename', {
        old_path: oldPath,
        new_path: newPath,
        is_dir: isDir,
      })
      renamingFile.value = null
      await reloadCurrentDir()
    } catch (e: any) {
      console.error('Rename failed:', e)
      throw e
    }
  }

  async function deleteSelected(): Promise<void> {
    if (selectedFiles.value.length === 0) return
    const count: number = selectedFiles.value.length

    if (isTrash.value) {
      if (!await showConfirm(t('dialog.permanent_delete_title'), t('dialog.confirm_permanent_delete', { n: count }), { icon: 'warning', positiveType: 'error' })) return
      for (const path of selectedFiles.value) {
        const file = getFileByPath(path)
        if (!file?._trashId) continue
        try {
          await ws.request('trash.delete', { id: file._trashId })
        } catch (e: any) {
          console.error('Permanent delete failed:', e)
        }
      }
    } else if (isShared.value) {
      if (!await showConfirm(t('share.stop_share'), t('share.confirm_stop'), { icon: 'warning', positiveType: 'error' })) return
      for (const path of selectedFiles.value) {
        const file = getFileByPath(path)
        if (!file?._shareDbId) continue
        try {
          await api.delete('/file/share/' + file._shareDbId)
        } catch (e: any) {
          console.error('Stop share failed:', e)
        }
      }
    } else {
      if (!await showConfirm(t('dialog.delete_title'), t('dialog.confirm_delete', { n: count }), { icon: 'warning', positiveType: 'error' })) return
      const promises = selectedFiles.value.map(path => {
        return ws.request('file.delete', { path })
      })
      await Promise.all(promises)
    }

    selectedFiles.value = []
    await reloadCurrentDir()
  }

  async function restoreSelected(): Promise<void> {
    if (!isTrash.value || selectedFiles.value.length === 0) return
    for (const path of selectedFiles.value) {
      const file = getFileByPath(path)
      if (!file?._trashId) continue
      try {
        await ws.request('trash.restore', { id: file._trashId })
      } catch (e: any) {
        console.error('Restore failed:', e)
      }
    }
    selectedFiles.value = []
    await reloadCurrentDir()
  }

  async function emptyTrash(): Promise<void> {
    if (!await showConfirm(t('dialog.empty_trash_title'), t('dialog.confirm_empty_trash'), { icon: 'warning', positiveType: 'error' })) return
    try {
      await ws.request('trash.clear')
    } catch (e: any) {
      console.error('Empty trash failed:', e)
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
  const videoExts: Set<string> = new Set(['mp4', 'webm'])
  const audioExts: Set<string> = new Set(['mp3', 'wav', 'ogg', 'aac', 'm4a', 'flac', 'opus'])
  const fontExts: Set<string> = new Set(['ttf', 'otf', 'woff', 'woff2'])
  const archiveExts: Set<string> = new Set(['zip'])
  const officeExts: Set<string> = new Set([
    'doc', 'docx', 'docm', 'dotm', 'dotx',
    'xls', 'xlsx', 'xlsb', 'xlsm',
    'ppt', 'pptx', 'ppsx', 'pps', 'pptm', 'potm', 'ppam', 'potx', 'ppsm',
  ])
  const notebookExts: Set<string> = new Set(['ipynb'])
  const textExts: Set<string> = new Set([
    'txt', 'json', 'yaml', 'yml', 'xml', 'log', 'ini', 'conf', 'cfg',
    'js', 'ts', 'jsx', 'tsx', 'vue', 'html', 'css', 'scss', 'less',
    'go', 'py', 'rb', 'java', 'c', 'cpp', 'h', 'hpp', 'rs', 'swift', 'kt',
    'sh', 'bash', 'zsh', 'fish', 'ps1', 'bat', 'cmd',
    'sql', 'graphql', 'proto',
    'toml', 'env', 'gitignore', 'dockerignore', 'editorconfig',
    'dockerfile', 'makefile',
    'php', 'pl', 'lua', 'r', 'scala', 'clj', 'ex', 'exs', 'erl', 'hs',
    'mod', 'sum',
  ])
  const extToLanguage: Record<string, string> = {
    js: 'javascript', ts: 'typescript', jsx: 'javascript', tsx: 'typescript',
    vue: 'xml', html: 'xml', css: 'css', scss: 'scss', less: 'less',
    go: 'go', py: 'python', rb: 'ruby', java: 'java',
    c: 'c', cpp: 'cpp', h: 'c', hpp: 'cpp', rs: 'rust', swift: 'swift', kt: 'kotlin',
    sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
    json: 'json', yaml: 'yaml', yml: 'yaml', xml: 'xml', toml: 'ini',
    sql: 'sql', graphql: 'graphql', proto: 'protobuf',
    md: 'markdown', php: 'php', lua: 'lua', r: 'r', scala: 'scala',
    dockerfile: 'dockerfile', makefile: 'makefile',
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
    office: ICONS.document,
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
    if (officeExts.has(ext)) return 'office'
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
    const type: ViewerType = forceType || getViewerType(fileName) || 'image'

    // If already open, just bring to front
    const existing = findApp(windowId)
    if (existing) {
      wm.bringToFront(windowId)
      const win = wm.findWindow(windowId)
      if (win) win.minimized = false
      return
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

    if (type === 'office') {
      // Privacy confirmation for Microsoft Office Online preview
      const ok = await showConfirm(
        t('dialog.office_privacy_title'),
        t('dialog.office_privacy_body'),
      )
      if (!ok) {
        appWindows.value = appWindows.value.filter(a => a.windowId !== windowId)
        wm.closeWindow(windowId)
        return
      }
      try {
        const res = await api.get<{ url: string }>('/file/preview', {
          params: { path: file.path, type: 'office' },
        })
        const presignedUrl: string = res.data.url
        state.url = `https://view.officeapps.live.com/op/embed.aspx?src=${encodeURIComponent(presignedUrl)}`
      } catch {
        state.url = ''
      }
    } else if (blobTypes.includes(type)) {
      try {
        const sw = useServiceWorker()
        const res = file._shareId
          ? await api.get<FileAccessResponse>('/file/shared/' + file._shareId)
          : await api.get<FileAccessResponse>('/file/access', { params: { path: file.path } })
        const { url, size, name, content_type, chunk_size, dek, content_hash } = res.data
        const decryptUrl = sw.registerDecrypt({
          url, size, chunkSize: chunk_size, contentType: content_type, filename: name, dek,
          contentHash: content_hash,
        })
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
      let decryptUrl: string | undefined
      try {
        const sw = useServiceWorker()
        const accessRes = file._shareId
          ? await api.get<FileAccessResponse>('/file/shared/' + file._shareId)
          : await api.get<FileAccessResponse>('/file/access', { params: { path: file.path } })
        const { url, size, name, content_type, chunk_size, dek, content_hash } = accessRes.data
        decryptUrl = sw.registerDecrypt({
          url, size, chunkSize: chunk_size, contentType: content_type, filename: name, dek,
          contentHash: content_hash,
        })
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
    if (!state || state.saving) return
    state.saving = true
    try {
      const newContent: string = state.content!
      const newBytes: Uint8Array = new TextEncoder().encode(newContent)
      if (state.file._shareId) {
        await ws.request('share.patchContent', {
          share_id: state.file._shareId,
          base_size: state.baseSize,
          edits: [{ offset: 0, delete: state.baseSize, insert: newContent }],
        })
      } else {
        await ws.request('file.patchContent', {
          path: state.file.path,
          base_size: state.baseSize,
          edits: [{ offset: 0, delete: state.baseSize, insert: newContent }],
        })
      }
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
        ws.request('audit.preview', {
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
    try {
      const sw = useServiceWorker()
      const path: string = typeof pathOrFile === 'string' ? pathOrFile : pathOrFile.path
      const shareId: string | undefined = typeof pathOrFile === 'object' ? pathOrFile._shareId : undefined
      const res = shareId
        ? await api.get<FileAccessResponse>('/file/shared/' + shareId)
        : await api.get<FileAccessResponse>('/file/access', { params: { path } })
      const { url, size, name, content_type, chunk_size, dek, content_hash } = res.data
      const decryptUrl: string = sw.registerDecrypt({
        url, size, chunkSize: chunk_size, contentType: content_type,
        filename: name, dek, download: true, contentHash: content_hash,
      })
      const a: HTMLAnchorElement = document.createElement('a')
      a.href = decryptUrl
      a.download = name
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      // Clean up after browser starts the download
      setTimeout(() => sw.unregisterDecrypt(decryptUrl), 60000)
    } catch (e: any) {
      console.error('Download failed:', e)
    }
  }

  // Listen for task updates — update processing files in file list with progress/phase
  ws.on('task.update', (data: TaskUpdateEvent) => {
    if (!data.task_id) return
    for (const tab of tabs.value) {
      for (const file of tab.files) {
        if (file.job_id === data.task_id) {
          file.job_progress = data.progress
          file.job_phase = data.phase
          file.status = data.status === 'completed' ? 'ready' : file.status
        }
      }
    }
  })

  // Listen for directory change push events
  ws.on('dir.changed', ({ path }: { path: string }) => {
    // Invalidate cache for this directory
    cache.delete(path)
    // Refresh all tabs viewing this directory
    for (const tab of tabs.value) {
      if (tab.path === path) {
        loadFilesForTab(tab, path)
      }
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
    } catch { /* ignore refresh errors */ }
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
      const tab: FileTab = {
        id,
        path: st.path || `/home/${auth.username}/`,
        files: [],
        selectedFiles: [],
        lastSelectedIndex: -1,
        history: [st.path || `/home/${auth.username}/`],
        historyIndex: 0,
        viewMode: (st.viewMode as FileTab['viewMode']) || 'icons',
        sortBy: (st.sortBy as FileTab['sortBy']) || 'name',
        sortOrder: (st.sortOrder as FileTab['sortOrder']) || 'asc',
        searchQuery: '',
        loading: false,
        error: null,
      }
      tabs.value.push(tab)
      tabSubs.set(id, tab.path)
      ws.request('subscribe.directory', { path: tab.path }).catch(() => {})
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
      localStorage.setItem('zephyr_show_hidden', showHidden.value ? '1' : '0')
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
    paste,
    createFolder,
    startRename,
    cancelRename,
    rename,
    deleteSelected,
    restoreSelected,
    emptyTrash,
    isTrash,
    isShared,
    openSelected,
    downloadFile,
    appWindows,
    isImageFile,
    isVideoFile,
    isTextFile,
    isPdfFile,
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
