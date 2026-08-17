import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef, Component } from 'vue'
import { usePreferences } from '../composables/usePreferences'
import { useI18n } from '../composables/useI18n'
import { useWorkspaceSync } from '../composables/useWorkspaceSync'
import { IconFolderHome, IconAccountCircle, IconConsole } from '../barrels/icons'
import type { WindowState, WindowGeo, OpenWindowOptions, RemoteFlag } from '../types'
import { readMigratedStorage } from '../utils/storageCompat'

export const FILES_ICON = IconFolderHome
export const PROFILE_ICON = IconAccountCircle
export const KONSOLE_ICON = IconConsole

export const useWindowManagerStore = defineStore('windowManager', () => {
  const windows: Ref<WindowState[]> = ref([])
  // { id, title, icon, type, minimized, maximized, tiled, x, y, width, height, zIndex, data,
  //   _restoreRect: { x, y, width, height } }

  let zCounter: number = 100
  let _userId: string | null = null
  let _savedGeo: Record<string, WindowGeo> | null = null // { id: { x, y, width, height, maximized } }
  let _saveTimer: ReturnType<typeof setTimeout> | null = null
  const { prefs: _prefs, load: loadPrefs, update: updatePrefsRemote } = usePreferences()
  const { t } = useI18n()
  const sync = useWorkspaceSync()

  const alwaysCenter: ComputedRef<boolean> = computed(() => _prefs.alwaysCenter)
  const defaultWidth: ComputedRef<number> = computed(() => _prefs.defaultWidth)
  const defaultHeight: ComputedRef<number> = computed(() => _prefs.defaultHeight)

  function _storageKey(): string | null {
    return _userId ? `domus_window_geo_${_userId}` : null
  }

  function _legacyStorageKey(): string | null {
    return _userId ? `zephyr_window_geo_${_userId}` : null
  }

  function _loadGeo(): void {
    const key = _storageKey()
    if (!key) { _savedGeo = null; return }
    try {
      _savedGeo = JSON.parse(readMigratedStorage(key, _legacyStorageKey()!) || '{}') || {}
    } catch {
      _savedGeo = {}
    }
  }

  function _saveGeo(): void {
    if (_saveTimer) clearTimeout(_saveTimer)
    _saveTimer = setTimeout(() => {
      const key = _storageKey()
      if (key && _savedGeo) {
        localStorage.setItem(key, JSON.stringify(_savedGeo))
      }
    }, 300)
  }

  function _recordGeo(win: WindowState): void {
    if (!_savedGeo || !win) return
    _savedGeo[win.id] = { x: win.x, y: win.y, width: win.width, height: win.height, maximized: win.maximized }
    _saveGeo()
  }

  function _applySavedGeo(win: WindowState): void {
    if (alwaysCenter.value) {
      win.width = defaultWidth.value
      win.height = defaultHeight.value
      // x=-1, y=-1 keeps PlasmaWindow's auto-center logic
      return
    }
    if (!_savedGeo) return
    const saved = _savedGeo[win.id]
    if (saved) {
      win.x = saved.x
      win.y = saved.y
      win.width = saved.width
      win.height = saved.height
      win.maximized = saved.maximized || false
    }
  }

  function setUser(userId: string): void {
    _userId = userId
    loadPrefs()
    _loadGeo()
  }

  function clearUser(): void {
    _userId = null
    _savedGeo = null
    windows.value = []
  }

  function updatePrefs(partial: Record<string, unknown>): void {
    updatePrefsRemote(partial)
  }

  const activeWindowId: ComputedRef<string | null> = computed(() => {
    const nonMinimized = windows.value.filter(w => !w.minimized)
    if (nonMinimized.length === 0) return null
    return nonMinimized.reduce((a, b) => a.zIndex > b.zIndex ? a : b).id
  })

  function findWindow(id: string): WindowState | undefined {
    return windows.value.find(w => w.id === id)
  }

  function openWindow({ id, title, icon, type, data, maximized: startMaximized, width, height }: OpenWindowOptions, { remote }: RemoteFlag = {}): WindowState {
    const existing = findWindow(id)
    if (existing) {
      existing.minimized = false
      bringToFront(id)
      return existing
    }

    const win: WindowState = {
      id,
      title: title || 'Window',
      icon: icon || '',
      type: type || 'generic',
      minimized: false,
      maximized: !!startMaximized,
      tiled: null, // 'left' | 'right' | null
      x: -1,
      y: -1,
      width: width || 800,
      height: height || 600,
      zIndex: ++zCounter,
      data: data || {},
      _restoreRect: null,
    }
    _applySavedGeo(win)
    windows.value.push(win)

    if (!remote) {
      sync.emitEvent({ action: 'window.open', id, type: type || 'generic', title: title || 'Window', data: data || {}, maximized: !!startMaximized })
    }

    return win
  }

  function closeWindow(id: string, { remote }: RemoteFlag = {}): void {
    const win = findWindow(id)
    if (!win) return
    if (win._closing) return // already closing

    if (!remote) {
      sync.emitEvent({ action: 'window.close', id })
    }

    // Trigger close animation, then remove
    win._closing = true
    setTimeout(() => {
      const idx = windows.value.findIndex(w => w.id === id)
      if (idx >= 0) windows.value.splice(idx, 1)
    }, 150)
  }

  function minimizeWindow(id: string, { remote }: RemoteFlag = {}): void {
    const win = findWindow(id)
    if (win) {
      win.minimized = true
      if (!remote) {
        sync.emitEvent({ action: 'window.minimize', id })
      }
    }
  }

  function restoreWindow(id: string, { remote }: RemoteFlag = {}): void {
    const win = findWindow(id)
    if (win) {
      win.minimized = false
      bringToFront(id)
      if (!remote) {
        sync.emitEvent({ action: 'window.restore', id })
      }
    }
  }

  function saveRestoreRect(win: WindowState): void {
    if (!win._restoreRect) {
      win._restoreRect = { x: win.x, y: win.y, width: win.width, height: win.height }
    }
  }

  function restoreWindowRect(win: WindowState): void {
    if (!win._restoreRect) return
    Object.assign(win, win._restoreRect)
    win._restoreRect = null
  }

  function toggleMaximize(id: string, { remote }: RemoteFlag = {}): void {
    const win = findWindow(id)
    if (!win) return
    if (win.maximized || win.tiled) {
      // Restore from maximized or tiled
      win.maximized = false
      win.tiled = null
      restoreWindowRect(win)
    } else {
      // Save current rect then maximize
      saveRestoreRect(win)
      win.maximized = true
    }
    bringToFront(id)
    _recordGeo(win)
    if (!remote) {
      sync.emitEvent({ action: 'window.maximize', id })
    }
  }

  function tileWindow(id: string, side: 'left' | 'right', containerWidth: number, containerHeight: number): void {
    const win = findWindow(id)
    if (!win) return
    // Save restore rect if not already saved
    saveRestoreRect(win)
    win.maximized = false
    win.tiled = side
    if (side === 'left') {
      win.x = 0
      win.y = 0
      win.width = Math.floor(containerWidth / 2)
      win.height = containerHeight
    } else if (side === 'right') {
      win.width = Math.floor(containerWidth / 2)
      win.height = containerHeight
      win.x = containerWidth - win.width
      win.y = 0
    }
    bringToFront(id)
  }

  function maximizeWindow(id: string): void {
    const win = findWindow(id)
    if (!win || win.maximized) return
    saveRestoreRect(win)
    win.tiled = null
    win.maximized = true
    bringToFront(id)
  }

  function bringToFront(id: string): void {
    const win = findWindow(id)
    if (win) {
      win.zIndex = ++zCounter
    }
  }

  function updateWindow(id: string, props: Partial<WindowState>): void {
    const win = findWindow(id)
    if (win) {
      Object.assign(win, props)
      _recordGeo(win)
    }
  }

  function openFilesApp({ remote }: RemoteFlag = {}): WindowState {
    return openWindow({
      id: 'files',
      title: t('app.files'),
      icon: FILES_ICON,
      type: 'files',
      maximized: true,
    }, { remote })
  }

  function openKonsoleApp(cwd?: string, { remote }: RemoteFlag = {}): WindowState {
    return openWindow({
      id: 'konsole',
      title: t('app.terminal'),
      icon: IconConsole,
      type: 'generic',
      width: 720,
      height: 480,
      data: { cwd },
    }, { remote })
  }

  function openProfileApp(tab?: string, { remote }: RemoteFlag = {}): WindowState {
    const win = openWindow({
      id: 'profile',
      title: t('app.profile'),
      icon: PROFILE_ICON,
      type: 'generic',
      width: 480,
      height: 620,
    }, { remote })
    if (tab) win.data = { tab }
    return win
  }


  return {
    windows,
    activeWindowId,
    openWindow,
    closeWindow,
    minimizeWindow,
    restoreWindow,
    toggleMaximize,
    tileWindow,
    maximizeWindow,
    bringToFront,
    updateWindow,
    findWindow,
    openFilesApp,
    openKonsoleApp,
    openProfileApp,
    setUser,
    clearUser,
    alwaysCenter,
    defaultWidth,
    defaultHeight,
    updatePrefs,
  }
})
