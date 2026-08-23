import type { FunctionalComponent, SVGAttributes } from 'vue'
import api from './useApi'
import { useWebSocket } from './useWebSocket'
import { usePreferences } from './usePreferences'
import { getFileIcon } from './useFileIcon'
import type { WorkspaceSnapshot, SerializedWindow, SerializedTab, WorkspaceEvent, ViewerCallbackFn, UserPreferences } from '../types'
import { TRASH_ROOT_LOCATION } from '../utils/trashLocation'

// Re-export icon constants so windowManager can use them for restore
import { IconFolderHome, IconAccountCircle, IconConsole } from '../barrels/icons'

let _saveTimer: ReturnType<typeof setTimeout> | null = null
let _isRemote: boolean = false // flag to prevent event loops

// Registry for viewer playback callbacks: windowId -> { applyRemote(action, currentTime) }
const viewerCallbacks: Map<string, ViewerCallbackFn> = new Map()

export function registerViewerCallback(windowId: string, cb: ViewerCallbackFn): void {
  viewerCallbacks.set(windowId, cb)
}

export function unregisterViewerCallback(windowId: string): void {
  viewerCallbacks.delete(windowId)
}

function isEnabled(): boolean {
  const { prefs } = usePreferences()
  return !prefs.sessionIsolation
}

// --- Icon resolution for restoring windows ---

function resolveIcon(id: string, type: string): FunctionalComponent<SVGAttributes> {
  if (id === 'files') return IconFolderHome
  if (id === 'profile') return IconAccountCircle
  if (id === 'konsole') return IconConsole
  if (type === 'viewer') {
    // Extract filename from an app id such as "app-/documents/photo.jpg".
    const filePath: string = id.startsWith('app-') ? id.slice(4) : id
    const fileName: string = filePath.split('/').pop() || ''
    return getFileIcon(fileName, false)
  }
  return IconFolderHome
}

// --- Public API ---

// Store interfaces (avoid importing store types to prevent circular deps)
interface WindowManagerLike {
  windows: Array<{
    id: string
    type: string
    title: string
    minimized: boolean
    maximized: boolean
    tiled: string | null
    data?: Record<string, unknown>
  }>
  openWindow: (opts: any, flags?: any) => any
  closeWindow: (id: string, flags?: any) => void
  minimizeWindow: (id: string, flags?: any) => void
  restoreWindow: (id: string, flags?: any) => void
  toggleMaximize: (id: string, flags?: any) => void
  findWindow: (id: string) => any
  openFilesApp: (flags?: any) => void
}

interface FileSystemLike {
  tabs: Array<{
    id: string
    path: string
    viewMode: string
    sortBy: string
    sortOrder: string
  }>
  activeTabId: string
  createTab: (path?: string, flags?: any) => void
  closeTab: (id: string, flags?: any) => void
  switchTab: (id: string, flags?: any) => void
  navigate: (path: string, push?: boolean, flags?: any) => void
  init: (opts?: any) => void
  restoreTabs: (tabs: SerializedTab[], activeIndex?: number) => void
  restoreViewers: (viewers: any[]) => void
  openViewer: (file: any, flags?: any) => void
  closeViewer: (id: string, flags?: any) => void
}

/**
 * Send a workspace event for real-time relay to other connections.
 * No-op if session isolation is on or if this is a remote event being applied.
 */
function emitEvent(event: WorkspaceEvent): void {
  if (_isRemote || !isEnabled()) return
  const ws = useWebSocket()
  ws.request('workspace.event', event).catch(() => {})
}

/**
 * Debounced save of the full workspace snapshot.
 */
function scheduleSave(wm: WindowManagerLike, fs: FileSystemLike): void {
  if (!isEnabled()) return
  if (_saveTimer) clearTimeout(_saveTimer)
  _saveTimer = setTimeout(() => {
    const state: WorkspaceSnapshot = buildSnapshot(wm, fs)
    api.put('/workspace/', { state }).catch(() => {})
  }, 2000)
}

/**
 * Build a serialisable workspace snapshot from store state.
 */
function buildSnapshot(wm: WindowManagerLike, fs: FileSystemLike): WorkspaceSnapshot {
  const windows: SerializedWindow[] = wm.windows.map(w => {
    const entry: SerializedWindow = {
      id: w.id,
      type: w.type,
      title: w.title,
      minimized: w.minimized,
      maximized: w.maximized,
      tiled: w.tiled,
    }
    // For viewer windows, save the file path
    if (w.type === 'viewer' && w.data?.filePath) {
      entry.data = { filePath: w.data.filePath as string }
    }
    return entry
  })

  const tabs: SerializedTab[] = fs.tabs.map(t => ({
    path: t.path,
    viewMode: t.viewMode,
    sortBy: t.sortBy,
    sortOrder: t.sortOrder,
  }))

  const activeTabIndex: number = fs.tabs.findIndex(t => t.id === fs.activeTabId)

  return { version: 2, windows, tabs, activeTabIndex: Math.max(activeTabIndex, 0) }
}

function migrateWorkspaceSnapshot(snapshot: WorkspaceSnapshot): WorkspaceSnapshot | null {
  if (snapshot.version === 2) return snapshot
  if (snapshot.version !== 1) return null

  const legacyTrashPath = (value: unknown): boolean =>
    typeof value === 'string' && (value === '/__trash__' || value.startsWith('/__trash__/'))
  const tabs = (snapshot.tabs || []).map(tab => ({
    ...tab,
    // Path-shaped Trash v1 entries cannot be mapped to opaque v2 IDs. Keep
    // the place open, but intentionally discard the stale descendant path.
    path: legacyTrashPath(tab.path) ? TRASH_ROOT_LOCATION : tab.path,
  }))
  const windows = (snapshot.windows || []).filter(window =>
    window.type !== 'viewer' || !legacyTrashPath(window.data?.filePath),
  )
  return { ...snapshot, version: 2, tabs, windows }
}

/**
 * Load saved workspace snapshot from the server.
 * Returns the parsed state object or null.
 */
async function load(): Promise<WorkspaceSnapshot | null> {
  if (!isEnabled()) return null
  try {
    const { data: res } = await api.get<{ state?: WorkspaceSnapshot | string }>('/workspace/')
    if (res?.state && typeof res.state === 'object') {
      return migrateWorkspaceSnapshot(res.state as WorkspaceSnapshot)
    }
    // Could be a JSON string
    if (res?.state && typeof res.state === 'string') {
      return migrateWorkspaceSnapshot(JSON.parse(res.state) as WorkspaceSnapshot)
    }
  } catch { /* silent */ }
  return null
}

/**
 * Clear saved workspace state (on explicit logout).
 */
async function clear(): Promise<void> {
  try {
    await api.delete('/workspace/')
  } catch { /* silent */ }
}

/**
 * Restore windows from a snapshot. Rebuilds icons from id/type.
 */
function restoreWindows(wm: WindowManagerLike, serializedWindows: SerializedWindow[]): void {
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
      (win as Record<string, unknown>).minimized = !!sw.minimized;
      (win as Record<string, unknown>).maximized = !!sw.maximized;
      (win as Record<string, unknown>).tiled = sw.tiled || null
    }
  }
}

/**
 * Set up push event handler for incoming workspace events from other devices.
 */
function setupPushHandler(wm: WindowManagerLike, fs: FileSystemLike): void {
  const ws = useWebSocket()
  const { prefs }: { prefs: UserPreferences } = usePreferences()

  ws.on('workspace.event', (data: unknown) => {
    const eventData = data as Record<string, unknown> | null
    if (!eventData?.action) return

    // prefs.changed is always processed regardless of sessionIsolation
    if (eventData.action === 'prefs.changed') {
      if (eventData.data && typeof eventData.data === 'object') {
        Object.assign(prefs, eventData.data)
      }
      return
    }

    // All other events gated by sessionIsolation
    if (prefs.sessionIsolation) return

    _isRemote = true
    try {
      switch (eventData.action) {
        // --- Window events ---
        case 'window.open': {
          const icon = resolveIcon(eventData.id as string, eventData.type as string)
          wm.openWindow({
            id: eventData.id,
            title: (eventData.title as string) || '',
            icon,
            type: (eventData.type as string) || 'generic',
            data: (eventData.data as Record<string, unknown>) || {},
            maximized: eventData.maximized,
          }, { remote: true })
          // If it's a viewer, open the viewer to load content
          if (eventData.type === 'viewer' && (eventData.data as Record<string, unknown>)?.filePath) {
            const filePath = (eventData.data as Record<string, unknown>).filePath as string
            const fileName: string = filePath.split('/').pop() || ''
            fs.openViewer({ path: filePath, name: fileName, size: 0, is_dir: false }, { remote: true })
          }
          break
        }
        case 'window.close':
          // If it's a viewer window, close the viewer too
          if ((eventData.id as string)?.startsWith('app-')) {
            fs.closeViewer(eventData.id as string, { remote: true })
          }
          wm.closeWindow(eventData.id as string, { remote: true })
          break
        case 'window.minimize':
          wm.minimizeWindow(eventData.id as string, { remote: true })
          break
        case 'window.restore':
          wm.restoreWindow(eventData.id as string, { remote: true })
          break
        case 'window.maximize':
          wm.toggleMaximize(eventData.id as string, { remote: true })
          break

        // --- Tab events ---
        case 'tab.open':
          fs.createTab(eventData.path as string, { remote: true })
          break
        case 'tab.close':
          if (typeof eventData.index === 'number' && fs.tabs[eventData.index]) {
            fs.closeTab(fs.tabs[eventData.index].id, { remote: true })
          }
          break
        case 'tab.switch':
          if (typeof eventData.index === 'number' && fs.tabs[eventData.index]) {
            fs.switchTab(fs.tabs[eventData.index].id, { remote: true })
          }
          break
        case 'tab.navigate':
          if (typeof eventData.index === 'number' && fs.tabs[eventData.index]) {
            // Ensure this tab is active before navigating
            fs.switchTab(fs.tabs[eventData.index].id, { remote: true })
            fs.navigate(eventData.path as string, true, { remote: true })
          }
          break
        case 'tab.update':
          if (typeof eventData.index === 'number' && fs.tabs[eventData.index]) {
            const tab = fs.tabs[eventData.index]
            if (eventData.viewMode != null) tab.viewMode = eventData.viewMode as string
            if (eventData.sortBy != null) tab.sortBy = eventData.sortBy as string
            if (eventData.sortOrder != null) tab.sortOrder = eventData.sortOrder as string
          }
          break

        // --- Viewer playback events ---
        case 'viewer.play':
        case 'viewer.pause':
        case 'viewer.seek':
        case 'viewer.timeSync': {
          const cb: ViewerCallbackFn | undefined = viewerCallbacks.get(eventData.windowId as string)
          if (cb) cb((eventData.action as string).split('.')[1], eventData.currentTime as number | undefined)
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
      load().then((saved: WorkspaceSnapshot | null) => {
        if (saved?.version === 2 && (saved.windows?.length > 0 || saved.tabs?.length > 0)) {
          // Close existing and restore
          wm.windows.splice(0)
          fs.init({ skipRestore: true })
          if (saved.tabs?.length > 0) {
            fs.restoreTabs(saved.tabs, saved.activeTabIndex ?? 0)
          } else {
            fs.createTab(undefined, { remote: true })
          }
          const viewers: SerializedWindow[] = saved.windows.filter(w => w.type === 'viewer')
          const others: SerializedWindow[] = saved.windows.filter(w => w.type !== 'viewer')
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

export function useWorkspaceSync(): {
  emitEvent: (event: WorkspaceEvent) => void
  scheduleSave: (wm: WindowManagerLike, fs: FileSystemLike) => void
  load: () => Promise<WorkspaceSnapshot | null>
  clear: () => Promise<void>
  restoreWindows: (wm: WindowManagerLike, serializedWindows: SerializedWindow[]) => void
  setupPushHandler: (wm: WindowManagerLike, fs: FileSystemLike) => void
  resolveIcon: (id: string, type: string) => FunctionalComponent<SVGAttributes>
} {
  return {
    emitEvent,
    scheduleSave,
    load,
    clear,
    restoreWindows,
    setupPushHandler,
    resolveIcon,
  }
}
