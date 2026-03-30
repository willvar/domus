import { defineStore } from 'pinia'
import { ref, reactive, computed } from 'vue'
import api, { API_BASE } from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useAuthStore } from './auth'
import { useWindowManagerStore } from './windowManager'
import { useI18n } from '../composables/useI18n'
import { ICONS } from '../composables/useFileIcon'
import { showPrompt, showConfirm } from '../composables/useNativeDialog'
import { usePendingOpsStore } from './pendingOps'
import { usePreferences } from '../composables/usePreferences'
import { useWorkspaceSync } from '../composables/useWorkspaceSync'

const TEXT_CHUNK_SIZE = 256 * 1024 // 256KB — aligns with 4 encryption chunks
const NON_CHUNKABLE_TYPES = new Set(['notebook', 'archive'])

function getLargeFileLimit() {
  const { prefs } = usePreferences()
  return prefs.largeFileLimitMB * 1024 * 1024
}

function formatFileSize(bytes) {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

let tabIdCounter = 0
function nextTabId() {
  return 'tab-' + (++tabIdCounter)
}

async function fetchTextChunk(path, byteStart, totalSize) {
  const byteEnd = Math.min(byteStart + TEXT_CHUNK_SIZE - 1, totalSize - 1)
  const res = await api.get('/file/content/raw', {
    params: { path },
    headers: { Range: `bytes=${byteStart}-${byteEnd}` },
    responseType: 'arraybuffer',
  })

  const buffer = new Uint8Array(res.data)
  const text = new TextDecoder('utf-8').decode(buffer)
  const isLast = byteEnd >= totalSize - 1

  if (isLast) {
    return { text, byteLength: buffer.byteLength, nextByteStart: totalSize }
  }

  // Find last newline for clean line-aligned boundary
  const lastNL = text.lastIndexOf('\n')
  if (lastNL > 0) {
    const clean = text.substring(0, lastNL + 1)
    const cleanBytes = new TextEncoder().encode(clean).byteLength
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
  const tabs = ref([])
  const activeTabId = ref('')

  const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value))

  function bindActiveTabField(key, fallback) {
    return computed({
      get: () => activeTab.value?.[key] ?? (typeof fallback === 'function' ? fallback() : fallback),
      set: (value) => {
        if (activeTab.value) activeTab.value[key] = value
      },
    })
  }

  // --- Proxy computed properties (delegate to active tab) ---
  const currentPath = bindActiveTabField('path', '')
  const files = bindActiveTabField('files', () => [])
  const loading = bindActiveTabField('loading', false)
  const error = bindActiveTabField('error', null)
  const selectedFiles = bindActiveTabField('selectedFiles', () => [])
  const lastSelectedIndex = bindActiveTabField('lastSelectedIndex', -1)
  const history = bindActiveTabField('history', () => [])
  const historyIndex = bindActiveTabField('historyIndex', -1)
  const viewMode = bindActiveTabField('viewMode', 'icons')
  const sortBy = bindActiveTabField('sortBy', 'name')
  const sortOrder = bindActiveTabField('sortOrder', 'asc')
  const searchQuery = bindActiveTabField('searchQuery', '')
  const showHidden = ref(localStorage.getItem('zephyr_show_hidden') === '1')

  // --- Search state ---
  const searchMode = ref(false)
  const searchResults = ref([])
  const searchLoading = ref(false)

  // --- Global (non-tab) state ---
  const clipboard = ref({ items: [], mode: null })
  const iconSize = ref(48)
  const focusPathBar = ref(false)
  const focusSearch = ref(false)
  const renamingFile = ref(null)
  const showInfoPanel = ref(false)
  const showSidebar = ref(true)
  const showTerminal = ref(false)
  const terminalHeight = ref(200)

  // App windows (multi-window: array of { windowId, file, url, type, ... })
  const appWindows = ref([])

  // Directory cache (global, shared across tabs)
  const cache = new Map()
  const CACHE_TTL = 30000

  async function performSearch(query) {
    if (!query || query.length < 2) return
    searchMode.value = true
    searchLoading.value = true
    try {
      const res = await ws.request('file.search', { query, limit: 100 })
      searchResults.value = res.results || []
    } catch {
      searchResults.value = []
    } finally {
      searchLoading.value = false
    }
  }

  function exitSearch() {
    searchMode.value = false
    searchResults.value = []
    searchQuery.value = ''
  }

  // Sorted and filtered files
  const sortedFiles = computed(() => {
    if (searchMode.value) return searchResults.value

    let items = [...files.value]

    if (!showHidden.value) {
      items = items.filter(f => !f.name.startsWith('.'))
    }

    items.sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      const mult = sortOrder.value === 'asc' ? 1 : -1
      switch (sortBy.value) {
        case 'name':
          return mult * a.name.localeCompare(b.name, undefined, { numeric: true })
        case 'size':
          return mult * (a.size - b.size)
        case 'date':
          return mult * (new Date(a.last_modified) - new Date(b.last_modified))
        default:
          return 0
      }
    })

    return items
  })

  const selectedFile = computed(() => {
    if (selectedFiles.value.length === 1) {
      return files.value.find(f => f.path === selectedFiles.value[0])
    }
    return null
  })

  const isTrash = computed(() => currentPath.value === '__trash__/')

  const canGoBack = computed(() => historyIndex.value > 0)
  const canGoForward = computed(() => historyIndex.value < history.value.length - 1)
  const canGoUp = computed(() => currentPath.value !== '/' && !isTrash.value)

  const pathSegments = computed(() => {
    if (!currentPath.value || currentPath.value === '/') return []
    const trimmed = currentPath.value.replace(/^\//, '').replace(/\/$/, '')
    const parts = trimmed.split('/')
    const segments = []
    let accumulated = '/'
    for (const part of parts) {
      accumulated += part + '/'
      segments.push({ name: part, path: accumulated })
    }
    return segments
  })

  // --- Tab operations ---
  function createTab(path, { remote } = {}) {
    const id = nextTabId()
    const initialPath = path || `/home/${auth.username}/`
    const tab = {
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

  function closeTab(id, { remote } = {}) {
    const idx = tabs.value.findIndex(t => t.id === id)
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
      const newIdx = Math.min(idx, tabs.value.length - 1)
      activeTabId.value = tabs.value[newIdx].id
    }
  }

  function switchTab(id, { remote } = {}) {
    const idx = tabs.value.findIndex(t => t.id === id)
    if (idx >= 0) {
      activeTabId.value = id
      if (!remote) {
        sync.emitEvent({ action: 'tab.switch', index: idx })
      }
    }
  }

  function nextTab() {
    const idx = tabs.value.findIndex(t => t.id === activeTabId.value)
    if (idx < 0) return
    const next = (idx + 1) % tabs.value.length
    activeTabId.value = tabs.value[next].id
  }

  function prevTab() {
    const idx = tabs.value.findIndex(t => t.id === activeTabId.value)
    if (idx < 0) return
    const prev = (idx - 1 + tabs.value.length) % tabs.value.length
    activeTabId.value = tabs.value[prev].id
  }

  function moveTab(fromIndex, toIndex) {
    if (fromIndex === toIndex) return
    const [tab] = tabs.value.splice(fromIndex, 1)
    tabs.value.splice(toIndex, 0, tab)
  }

  // Track subscribed directories per tab
  const tabSubs = new Map() // tabId -> subscribedPath

  // --- Navigation ---
  async function navigate(path, addToHistory = true, { remote } = {}) {
    path = path || '/'
    if (path !== '/' && !path.endsWith('/')) path += '/'

    // Exit search mode when navigating
    if (searchMode.value) {
      searchMode.value = false
      searchResults.value = []
      searchQuery.value = ''
    }

    // Unsubscribe old directory for this tab
    const tabId = activeTabId.value
    const oldSub = tabSubs.get(tabId)
    if (oldSub && oldSub !== path && path !== '__trash__/') {
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
      const tabIndex = tabs.value.findIndex(t => t.id === tabId)
      if (tabIndex >= 0) {
        sync.emitEvent({ action: 'tab.navigate', index: tabIndex, path })
      }
    }

    // Subscribe to new directory
    if (path !== '__trash__/') {
      tabSubs.set(tabId, path)
      ws.request('subscribe.directory', { path }).catch(() => {})
    }

    await loadFiles(path)
  }

  async function loadFiles(path) {
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
    } catch (e) {
      error.value = te(e)
    } finally {
      loading.value = false
    }
  }

  async function fetchFiles(path) {
    if (path === '__trash__/') {
      const items = await ws.request('trash.list') || []
      return items.map(item => ({
        name: item.original_path.replace(/\/$/, '').split('/').pop(),
        path: '__trash__/' + item.id,
        is_dir: item.is_dir,
        size: item.size,
        last_modified: item.deleted_at,
        _trashId: item.id,
        _originalPath: item.original_path,
      }))
    }
    const res = await ws.request('file.list', { path })
    return res.files || []
  }

  function goBack() {
    if (!canGoBack.value) return
    historyIndex.value--
    navigate(history.value[historyIndex.value], false)
  }

  function goForward() {
    if (!canGoForward.value) return
    historyIndex.value++
    navigate(history.value[historyIndex.value], false)
  }

  function goUp() {
    if (!canGoUp.value) return
    const parts = currentPath.value.replace(/\/$/, '').split('/')
    parts.pop()
    const parent = parts.join('/') + '/'
    navigate(parent === '/' ? '/' : parent)
  }

  function refresh() {
    cache.delete(currentPath.value)
    return loadFiles(currentPath.value)
  }

  function invalidateCache() {
    cache.delete(currentPath.value)
  }

  async function reloadCurrentDir() {
    invalidateCache()
    await refresh()
  }

  function getFileByPath(path) {
    return files.value.find(f => f.path === path)
  }

  // Selection
  function selectFile(path, event) {
    const ctrl = event?.ctrlKey || event?.metaKey
    const shift = event?.shiftKey

    if (shift && lastSelectedIndex.value >= 0) {
      const currentIndex = sortedFiles.value.findIndex(f => f.path === path)
      const start = Math.min(lastSelectedIndex.value, currentIndex)
      const end = Math.max(lastSelectedIndex.value, currentIndex)
      selectedFiles.value = sortedFiles.value.slice(start, end + 1).map(f => f.path)
    } else if (ctrl) {
      const idx = selectedFiles.value.indexOf(path)
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

  function selectAll() {
    selectedFiles.value = sortedFiles.value.map(f => f.path)
  }

  function clearSelection() {
    selectedFiles.value = []
    lastSelectedIndex.value = -1
  }

  // Mobile select mode
  const selectMode = ref(false)

  function enterSelectMode(path) {
    selectMode.value = true
    if (path && !selectedFiles.value.includes(path)) {
      selectedFiles.value = [path]
    }
  }

  function exitSelectMode() {
    selectMode.value = false
    clearSelection()
  }

  function toggleSelect(path) {
    const idx = selectedFiles.value.indexOf(path)
    if (idx >= 0) {
      selectedFiles.value.splice(idx, 1)
      if (selectedFiles.value.length === 0) selectMode.value = false
    } else {
      selectedFiles.value.push(path)
    }
  }

  // Clipboard
  function buildClipboardItems() {
    return selectedFiles.value.map(path => {
      const file = getFileByPath(path)
      return { path, is_dir: file?.is_dir || false, name: file?.name || '' }
    })
  }

  function copySelected() {
    clipboard.value = {
      items: buildClipboardItems(),
      mode: 'copy',
    }
  }

  function cutSelected() {
    if (!auth.canEdit) return
    clipboard.value = {
      items: buildClipboardItems(),
      mode: 'cut',
    }
  }

  async function paste() {
    if (!auth.canEdit) return
    if (clipboard.value.items.length === 0) return
    const mode = clipboard.value.mode
    const action = mode === 'copy' ? 'file.copy' : 'file.move'

    const results = await Promise.allSettled(clipboard.value.items.map(item => {
      const name = item.is_dir ? item.name.replace(/\/+$/, '') : item.name
      const dstPath = currentPath.value + name + (item.is_dir ? '/' : '')
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
  async function createFolder() {
    if (!auth.canUpload) return
    const name = await showPrompt(t('dialog.new_folder_name'))
    if (!name) return

    try {
      await ws.request('file.mkdir', { path: currentPath.value + name })
      await reloadCurrentDir()
    } catch (e) {
      console.error('Create folder failed:', e)
    }
  }

  function startRename() {
    if (!auth.canEdit) return
    if (selectedFiles.value.length !== 1) return
    renamingFile.value = selectedFiles.value[0]
  }

  function cancelRename() {
    renamingFile.value = null
  }

  async function rename(oldPath, newName, isDir) {
    if (!auth.canEdit) return
    const parts = oldPath.replace(/\/$/, '').split('/')
    parts[parts.length - 1] = newName
    const newPath = parts.join('/') + (isDir ? '/' : '')

    try {
      await ws.request('file.rename', {
        old_path: oldPath,
        new_path: newPath,
        is_dir: isDir,
      })
      renamingFile.value = null
      await reloadCurrentDir()
    } catch (e) {
      console.error('Rename failed:', e)
      throw e
    }
  }

  async function deleteSelected() {
    if (!auth.canDelete) return
    if (selectedFiles.value.length === 0) return
    const count = selectedFiles.value.length

    if (isTrash.value) {
      if (!await showConfirm(t('dialog.confirm_permanent_delete', { n: count }))) return
      for (const path of selectedFiles.value) {
        const file = getFileByPath(path)
        if (!file?._trashId) continue
        try {
          await ws.request('trash.delete', { id: file._trashId })
        } catch (e) {
          console.error('Permanent delete failed:', e)
        }
      }
    } else {
      if (!await showConfirm(t('dialog.confirm_delete', { n: count }))) return
      const promises = selectedFiles.value.map(path => {
        return ws.request('file.delete', { path })
      })
      await Promise.all(promises)
    }

    selectedFiles.value = []
    await reloadCurrentDir()
  }

  async function restoreSelected() {
    if (!isTrash.value || selectedFiles.value.length === 0) return
    for (const path of selectedFiles.value) {
      const file = getFileByPath(path)
      if (!file?._trashId) continue
      try {
        await ws.request('trash.restore', { id: file._trashId })
      } catch (e) {
        console.error('Restore failed:', e)
      }
    }
    selectedFiles.value = []
    await reloadCurrentDir()
  }

  async function emptyTrash() {
    if (!await showConfirm(t('dialog.confirm_empty_trash'))) return
    try {
      await ws.request('trash.clear')
    } catch (e) {
      console.error('Empty trash failed:', e)
    }
    await reloadCurrentDir()
  }

  function openSelected() {
    if (selectedFiles.value.length !== 1) return
    const file = getFileByPath(selectedFiles.value[0])
    if (!file) return

    const viewerType = getViewerType(file.name)
    if (file.is_dir) {
      navigate(file.path)
    } else if (viewerType) {
      openViewer(file)
    } else {
      downloadFile(file.path)
    }
  }

  const imageExts = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'ico', 'avif'])
  const videoExts = new Set(['mp4', 'webm'])
  const audioExts = new Set(['mp3', 'wav', 'ogg', 'aac', 'm4a', 'flac', 'opus'])
  const fontExts = new Set(['ttf', 'otf', 'woff', 'woff2'])
  const archiveExts = new Set(['zip'])
  const officeExts = new Set([
    'doc', 'docx', 'docm', 'dotm', 'dotx',
    'xls', 'xlsx', 'xlsb', 'xlsm',
    'ppt', 'pptx', 'ppsx', 'pps', 'pptm', 'potm', 'ppam', 'potx', 'ppsm',
  ])
  const notebookExts = new Set(['ipynb'])
  const textExts = new Set([
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
  const extToLanguage = {
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

  const appIcons = {
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

  function parseFileName(name) {
    const baseName = name.toLowerCase()
    return {
      baseName,
      ext: baseName.split('.').pop(),
    }
  }

  function isImageFile(name) {
    const { ext } = parseFileName(name)
    return imageExts.has(ext)
  }

  function isVideoFile(name) {
    const { ext } = parseFileName(name)
    return videoExts.has(ext)
  }

  function isTextFile(name) {
    const { ext, baseName } = parseFileName(name)
    return textExts.has(ext) || textExts.has(baseName)
  }

  function isPdfFile(name) {
    const { ext } = parseFileName(name)
    return ext === 'pdf'
  }

  function getLanguage(name) {
    const { ext, baseName } = parseFileName(name)
    return extToLanguage[ext] || extToLanguage[baseName] || null
  }

  function getViewerType(name) {
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

  function appWindowId(filePath) {
    return 'app-' + filePath
  }

  function findApp(windowId) {
    return appWindows.value.find(p => p.windowId === windowId)
  }

  async function openViewer(file, { forceType, remote } = {}) {
    const windowId = appWindowId(file.path) + (forceType ? `-${forceType}` : '')
    const type = forceType || getViewerType(file.name) || 'image'

    // If already open, just bring to front
    const existing = findApp(windowId)
    if (existing) {
      wm.bringToFront(windowId)
      const win = wm.findWindow(windowId)
      if (win) win.minimized = false
      return
    }

    const state = reactive({
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

    const blobTypes = ['video', 'image', 'pdf', 'audio', 'font', 'archive']
    const textTypes = ['text', 'markdown', 'csv', 'notebook']

    // Size guard for non-chunkable types
    if (NON_CHUNKABLE_TYPES.has(type) && file.size > getLargeFileLimit()) {
      const sizeStr = formatFileSize(file.size)
      const limitStr = formatFileSize(getLargeFileLimit())
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
        const res = await api.get('/file/preview', {
          params: { path: file.path, type: 'office' },
        })
        const presignedUrl = res.data.url
        state.url = `https://view.officeapps.live.com/op/embed.aspx?src=${encodeURIComponent(presignedUrl)}`
      } catch {
        state.url = ''
      }
    } else if (blobTypes.includes(type)) {
      try {
        const res = await api.get('/file/content/raw', {
          params: { path: file.path },
          responseType: 'blob',
        })
        state.url = URL.createObjectURL(res.data)
        state.blob = res.data
      } catch {
        state.url = ''
      }
    } else if (textTypes.includes(type)) {
      state.language = getLanguage(file.name)
      state.totalSize = file.size || 0
      state.baseSize = file.size || 0

      if (type === 'notebook') {
        // Notebook: JSON needs full parse
        state.chunked = false
        try {
          const res = await api.get('/file/content/raw', {
            params: { path: file.path },
            responseType: 'text',
          })
          state.content = res.data
          state.baseSize = new TextEncoder().encode(res.data).length
          state.totalSize = state.baseSize
        } catch {
          state.content = ''
          state.baseSize = 0
        }
      } else {
        // Range-based chunked loading
        state.chunked = true
        state.page = 0
        state.pageByteStart = 0
        state.pageByteEnd = 0
        state.pageMap = [{ byteStart: 0 }]
        state.totalPages = Math.max(1, Math.ceil((file.size || 1) / TEXT_CHUNK_SIZE))
        state.isFullyLoaded = false
        try {
          const result = await fetchTextChunk(file.path, 0, state.totalSize)
          state.content = result.text
          state.pageByteEnd = result.byteLength
          state.isFullyLoaded = result.nextByteStart >= state.totalSize
          if (state.isFullyLoaded) {
            state.totalPages = 1
            state.baseSize = new TextEncoder().encode(result.text).byteLength
          } else {
            state.pageMap.push({ byteStart: result.nextByteStart })
          }
        } catch {
          state.content = ''
        }
      }
    }
  }

  async function saveViewer(windowId) {
    if (!auth.canEdit) return
    const state = findApp(windowId)
    if (!state || state.saving) return
    state.saving = true
    try {
      const newContent = state.content
      const newBytes = new TextEncoder().encode(newContent)
      await ws.request('file.patchContent', {
        path: state.file.path,
        base_size: state.baseSize,
        edits: [{ offset: 0, delete: state.baseSize, insert: newContent }],
      })
      state.baseSize = newBytes.length
      state.editing = false
      state.dirty = false
    } catch (e) {
      console.error('Save failed:', e)
    } finally {
      state.saving = false
    }
  }

  function closeViewer(windowId, { remote } = {}) {
    const state = findApp(windowId)
    if (state) {
      if (state.openedAt) {
        const duration = Date.now() - state.openedAt
        ws.request('audit.preview', {
          path: state.file.path,
          duration_ms: duration,
          type: state.type,
        }).catch(() => {})
      }
      if (state.url && state.url.startsWith('blob:')) {
        URL.revokeObjectURL(state.url)
      }
    }
    wm.closeWindow(windowId, { remote })
    const idx = appWindows.value.findIndex(p => p.windowId === windowId)
    if (idx >= 0) appWindows.value.splice(idx, 1)
  }

  async function downloadFile(path) {
    try {
      const res = await api.get('/file/download', { params: { path } })
      window.open((API_BASE) + res.data.url, '_blank')
    } catch (e) {
      console.error('Download failed:', e)
    }
  }

  // Listen for task updates — update processing files in file list with progress/phase
  ws.on('task.update', (data) => {
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
  ws.on('dir.changed', ({ path }) => {
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
  async function loadFilesForTab(tab, path) {
    try {
      const data = await fetchFiles(path)
      if (data) {
        tab.files = data
        cache.set(path, { data, timestamp: Date.now() })
      }
    } catch { /* ignore refresh errors */ }
  }

  // Re-subscribe directories and reload files after WS (re)connection
  let initialized = false
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
  function init({ skipRestore } = {}) {
    if (auth.isLoggedIn) {
      // Reset tabs
      tabs.value = []
      activeTabId.value = ''
      appWindows.value = []
    }
  }

  // Restore tabs from a serialised snapshot
  function restoreTabs(serializedTabs, activeIndex) {
    tabs.value = []
    activeTabId.value = ''

    for (const st of serializedTabs) {
      const id = nextTabId()
      const tab = {
        id,
        path: st.path || `/home/${auth.username}/`,
        files: [],
        selectedFiles: [],
        lastSelectedIndex: -1,
        history: [st.path || `/home/${auth.username}/`],
        historyIndex: 0,
        viewMode: st.viewMode || 'icons',
        sortBy: st.sortBy || 'name',
        sortOrder: st.sortOrder || 'asc',
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
    const idx = typeof activeIndex === 'number' ? Math.min(activeIndex, tabs.value.length - 1) : 0
    if (tabs.value[idx]) {
      activeTabId.value = tabs.value[idx].id
    }
  }

  // Restore viewer windows (re-open files to fetch content)
  function restoreViewers(viewerWindows) {
    for (const vw of viewerWindows) {
      const filePath = vw.data?.filePath
      if (!filePath) continue
      const fileName = filePath.split('/').pop() || ''
      openViewer({ path: filePath, name: fileName, size: 0, is_dir: false }, { remote: true })
    }
  }

  async function viewerNextPage(windowId) {
    const state = findApp(windowId)
    if (!state?.chunked || state.page >= state.totalPages - 1) return
    const nextIdx = state.page + 1
    const entry = state.pageMap[nextIdx]
    if (!entry || entry.byteStart >= state.totalSize) return
    try {
      const result = await fetchTextChunk(state.file.path, entry.byteStart, state.totalSize)
      state.content = result.text
      state.page = nextIdx
      state.pageByteStart = entry.byteStart
      state.pageByteEnd = entry.byteStart + result.byteLength
      if (!state.pageMap[nextIdx + 1] && result.nextByteStart < state.totalSize) {
        state.pageMap.push({ byteStart: result.nextByteStart })
      }
      if (result.nextByteStart >= state.totalSize) {
        state.totalPages = nextIdx + 1
      }
    } catch { /* fetch error */ }
  }

  async function viewerPrevPage(windowId) {
    const state = findApp(windowId)
    if (!state?.chunked || state.page <= 0) return
    const prevIdx = state.page - 1
    const entry = state.pageMap[prevIdx]
    if (!entry) return
    try {
      const result = await fetchTextChunk(state.file.path, entry.byteStart, state.totalSize)
      state.content = result.text
      state.page = prevIdx
      state.pageByteStart = entry.byteStart
      state.pageByteEnd = entry.byteStart + result.byteLength
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
    toggleHidden() {
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
    showTerminal,
    terminalHeight,
    toggleTerminal() { showTerminal.value = !showTerminal.value },
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
