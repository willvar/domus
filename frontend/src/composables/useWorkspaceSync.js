import { useWebSocket } from './useWebSocket'
import { usePreferences } from './usePreferences'
import { getFileIcon } from './useFileIcon'

// Re-export icon constants so windowManager can use them for restore
import IconFolderHome from '~icons/mdi/folder-home'
import IconAccountCircle from '~icons/mdi/account-circle'
import IconConsole from '~icons/mdi/console'

let _saveTimer = null
let _isRemote = false // flag to prevent event loops

// Registry for viewer playback callbacks: windowId -> { applyRemote(action, currentTime) }
const viewerCallbacks = new Map()

export function registerViewerCallback(windowId, cb) {
  viewerCallbacks.set(windowId, cb)
}

export function unregisterViewerCallback(windowId) {
  viewerCallbacks.delete(windowId)
}

function isEnabled() {
  const { prefs } = usePreferences()
  return !prefs.sessionIsolation
}

// --- Icon resolution for restoring windows ---

function resolveIcon(id, type) {
  if (id === 'files') return IconFolderHome
  if (id === 'profile') return IconAccountCircle
  if (id === 'konsole') return IconConsole
  if (type === 'viewer') {
    // Extract filename from id like "app-/home/user/photo.jpg"
    const filePath = id.startsWith('app-') ? id.slice(4) : id
    const fileName = filePath.split('/').pop() || ''
    return getFileIcon(fileName, false)
  }
  return IconFolderHome
}

// --- Public API ---

/**
 * Send a workspace event for real-time relay to other connections.
 * No-op if session isolation is on or if this is a remote event being applied.
 */
function emitEvent(event) {
  if (_isRemote || !isEnabled()) return
  const ws = useWebSocket()
  ws.request('workspace.event', event).catch(() => {})
}

/**
 * Debounced save of the full workspace snapshot.
 */
function scheduleSave(wm, fs) {
  if (!isEnabled()) return
  if (_saveTimer) clearTimeout(_saveTimer)
  _saveTimer = setTimeout(() => {
    const state = buildSnapshot(wm, fs)
    const ws = useWebSocket()
    ws.request('workspace.save', { state }).catch(() => {})
  }, 2000)
}

/**
 * Build a serialisable workspace snapshot from store state.
 */
function buildSnapshot(wm, fs) {
  const windows = wm.windows.map(w => {
    const entry = {
      id: w.id,
      type: w.type,
      title: w.title,
      minimized: w.minimized,
      maximized: w.maximized,
      tiled: w.tiled,
    }
    // For viewer windows, save the file path
    if (w.type === 'viewer' && w.data?.filePath) {
      entry.data = { filePath: w.data.filePath }
    }
    return entry
  })

  const tabs = fs.tabs.map(t => ({
    path: t.path,
    viewMode: t.viewMode,
    sortBy: t.sortBy,
    sortOrder: t.sortOrder,
  }))

  const activeTabIndex = fs.tabs.findIndex(t => t.id === fs.activeTabId)

  return { version: 1, windows, tabs, activeTabIndex: Math.max(activeTabIndex, 0) }
}

/**
 * Load saved workspace snapshot from the server.
 * Returns the parsed state object or null.
 */
async function load() {
  if (!isEnabled()) return null
  const ws = useWebSocket()
  try {
    const res = await ws.request('workspace.load')
    if (res?.state && typeof res.state === 'object') {
      return res.state
    }
    // Could be a JSON string
    if (res?.state && typeof res.state === 'string') {
      return JSON.parse(res.state)
    }
  } catch { /* silent */ }
  return null
}

/**
 * Clear saved workspace state (on explicit logout).
 */
async function clear() {
  const ws = useWebSocket()
  try {
    await ws.request('workspace.clear')
  } catch { /* silent */ }
}

/**
 * Restore windows from a snapshot. Rebuilds icons from id/type.
 */
function restoreWindows(wm, serializedWindows) {
  if (!serializedWindows?.length) return
  for (const sw of serializedWindows) {
    const icon = resolveIcon(sw.id, sw.type)
    const win = wm.openWindow({
      id: sw.id,
      title: sw.title || '',
      icon,
      type: sw.type || 'generic',
      data: sw.data || {},
      maximized: sw.maximized,
    }, { remote: true })
    // Override geometry/state from snapshot
    if (win) {
      win.minimized = !!sw.minimized
      win.maximized = !!sw.maximized
      win.tiled = sw.tiled || null
    }
  }
}

/**
 * Set up push event handler for incoming workspace events from other devices.
 */
function setupPushHandler(wm, fs) {
  const ws = useWebSocket()
  const { prefs } = usePreferences()

  ws.on('workspace.event', (data) => {
    if (!data?.action) return

    // prefs.changed is always processed regardless of sessionIsolation
    if (data.action === 'prefs.changed') {
      if (data.data && typeof data.data === 'object') {
        Object.assign(prefs, data.data)
      }
      return
    }

    // All other events gated by sessionIsolation
    if (prefs.sessionIsolation) return

    _isRemote = true
    try {
      switch (data.action) {
        // --- Window events ---
        case 'window.open': {
          const icon = resolveIcon(data.id, data.type)
          wm.openWindow({
            id: data.id,
            title: data.title || '',
            icon,
            type: data.type || 'generic',
            data: data.data || {},
            maximized: data.maximized,
          }, { remote: true })
          // If it's a viewer, open the viewer to load content
          if (data.type === 'viewer' && data.data?.filePath) {
            const filePath = data.data.filePath
            const fileName = filePath.split('/').pop() || ''
            fs.openViewer({ path: filePath, name: fileName, size: 0, is_dir: false }, { remote: true })
          }
          break
        }
        case 'window.close':
          // If it's a viewer window, close the viewer too
          if (data.id?.startsWith('app-')) {
            fs.closeViewer(data.id, { remote: true })
          }
          wm.closeWindow(data.id, { remote: true })
          break
        case 'window.minimize':
          wm.minimizeWindow(data.id, { remote: true })
          break
        case 'window.restore':
          wm.restoreWindow(data.id, { remote: true })
          break
        case 'window.maximize':
          wm.toggleMaximize(data.id, { remote: true })
          break

        // --- Tab events ---
        case 'tab.open':
          fs.createTab(data.path, { remote: true })
          break
        case 'tab.close':
          if (typeof data.index === 'number' && fs.tabs[data.index]) {
            fs.closeTab(fs.tabs[data.index].id, { remote: true })
          }
          break
        case 'tab.switch':
          if (typeof data.index === 'number' && fs.tabs[data.index]) {
            fs.switchTab(fs.tabs[data.index].id, { remote: true })
          }
          break
        case 'tab.navigate':
          if (typeof data.index === 'number' && fs.tabs[data.index]) {
            // Ensure this tab is active before navigating
            fs.switchTab(fs.tabs[data.index].id, { remote: true })
            fs.navigate(data.path, true, { remote: true })
          }
          break
        case 'tab.update':
          if (typeof data.index === 'number' && fs.tabs[data.index]) {
            const tab = fs.tabs[data.index]
            if (data.viewMode != null) tab.viewMode = data.viewMode
            if (data.sortBy != null) tab.sortBy = data.sortBy
            if (data.sortOrder != null) tab.sortOrder = data.sortOrder
          }
          break

        // --- Viewer playback events ---
        case 'viewer.play':
        case 'viewer.pause':
        case 'viewer.seek':
        case 'viewer.timeSync': {
          const cb = viewerCallbacks.get(data.windowId)
          if (cb) cb(data.action.split('.')[1], data.currentTime)
          break
        }
      }
    } finally {
      _isRemote = false
    }
  })

  // Re-register on reconnect
  ws.onReconnect(() => {
    // On reconnect, load full state if sync is enabled
    if (!prefs.sessionIsolation) {
      load().then(saved => {
        if (saved?.version === 1 && saved.windows?.length > 0) {
          // Close existing and restore
          wm.windows.splice(0)
          fs.init({ skipRestore: true })
          if (saved.tabs?.length > 0) {
            fs.restoreTabs(saved.tabs, saved.activeTabIndex ?? 0)
          } else {
            fs.createTab(undefined, { remote: true })
          }
          const viewers = saved.windows.filter(w => w.type === 'viewer')
          const others = saved.windows.filter(w => w.type !== 'viewer')
          restoreWindows(wm, others)
          if (viewers.length > 0) {
            fs.restoreViewers(viewers)
          }
          if (saved.tabs?.length > 0 && !wm.findWindow('files')) {
            wm.openFilesApp({ remote: true })
          }
        }
      })
    }
  })
}

export function isRemote() {
  return _isRemote
}

export function useWorkspaceSync() {
  return {
    emitEvent,
    scheduleSave,
    load,
    clear,
    restoreWindows,
    setupPushHandler,
    resolveIcon,
    isRemote,
  }
}
