import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { usePreferences } from '../composables/usePreferences'
import IconFolderHome from '~icons/mdi/folder-home'
import IconAccountCircle from '~icons/mdi/account-circle'
export const FILES_ICON = IconFolderHome
export const PROFILE_ICON = IconAccountCircle

export const useWindowManagerStore = defineStore('windowManager', () => {
  const windows = ref([])
  // { id, title, icon, type, minimized, maximized, tiled, x, y, width, height, zIndex, data,
  //   _restoreRect: { x, y, width, height } }

  let zCounter = 100
  let _userId = null
  let _savedGeo = null // { id: { x, y, width, height, maximized } }
  let _saveTimer = null
  const { prefs: _prefs, load: loadPrefs, update: updatePrefsRemote } = usePreferences()

  const alwaysCenter = computed(() => _prefs.alwaysCenter)
  const defaultWidth = computed(() => _prefs.defaultWidth)
  const defaultHeight = computed(() => _prefs.defaultHeight)

  function _storageKey() {
    return _userId ? `zephyr_window_geo_${_userId}` : null
  }

  function _loadGeo() {
    const key = _storageKey()
    if (!key) { _savedGeo = null; return }
    try {
      _savedGeo = JSON.parse(localStorage.getItem(key)) || {}
    } catch {
      _savedGeo = {}
    }
  }

  function _saveGeo() {
    if (_saveTimer) clearTimeout(_saveTimer)
    _saveTimer = setTimeout(() => {
      const key = _storageKey()
      if (key && _savedGeo) {
        localStorage.setItem(key, JSON.stringify(_savedGeo))
      }
    }, 300)
  }

  function _recordGeo(win) {
    if (!_savedGeo || !win) return
    _savedGeo[win.id] = { x: win.x, y: win.y, width: win.width, height: win.height, maximized: win.maximized }
    _saveGeo()
  }

  function _applySavedGeo(win) {
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
      win.maximized = saved.maximized
    }
  }

  function setUser(userId) {
    _userId = userId
    loadPrefs()
    _loadGeo()
  }

  function clearUser() {
    _userId = null
    _savedGeo = null
  }

  function updatePrefs(partial) {
    updatePrefsRemote(partial)
  }

  const activeWindowId = computed(() => {
    const nonMinimized = windows.value.filter(w => !w.minimized)
    if (nonMinimized.length === 0) return null
    return nonMinimized.reduce((a, b) => a.zIndex > b.zIndex ? a : b).id
  })

  function findWindow(id) {
    return windows.value.find(w => w.id === id)
  }

  function openWindow({ id, title, icon, type, data, maximized: startMaximized, width, height }) {
    const existing = findWindow(id)
    if (existing) {
      existing.minimized = false
      bringToFront(id)
      return existing
    }

    const win = {
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
    return win
  }

  function closeWindow(id) {
    const idx = windows.value.findIndex(w => w.id === id)
    if (idx >= 0) {
      windows.value.splice(idx, 1)
    }
  }

  function minimizeWindow(id) {
    const win = findWindow(id)
    if (win) win.minimized = true
  }

  function restoreWindow(id) {
    const win = findWindow(id)
    if (win) {
      win.minimized = false
      bringToFront(id)
    }
  }

  function saveRestoreRect(win) {
    if (!win._restoreRect) {
      win._restoreRect = { x: win.x, y: win.y, width: win.width, height: win.height }
    }
  }

  function restoreWindowRect(win) {
    if (!win._restoreRect) return
    Object.assign(win, win._restoreRect)
    win._restoreRect = null
  }

  function toggleMaximize(id) {
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
  }

  function tileWindow(id, side, containerWidth, containerHeight) {
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

  function maximizeWindow(id) {
    const win = findWindow(id)
    if (!win || win.maximized) return
    saveRestoreRect(win)
    win.tiled = null
    win.maximized = true
    bringToFront(id)
  }

  function bringToFront(id) {
    const win = findWindow(id)
    if (win) {
      win.zIndex = ++zCounter
    }
  }

  function updateWindow(id, props) {
    const win = findWindow(id)
    if (win) {
      Object.assign(win, props)
      _recordGeo(win)
    }
  }

  function openFilesApp() {
    return openWindow({
      id: 'files',
      title: '文件',
      icon: FILES_ICON,
      type: 'files',
      maximized: true,
    })
  }

  function openProfileApp(tab) {
    const win = openWindow({
      id: 'profile',
      title: '我',
      icon: PROFILE_ICON,
      type: 'generic',
      width: 480,
      height: 620,
    })
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
    openProfileApp,
    setUser,
    clearUser,
    alwaysCenter,
    defaultWidth,
    defaultHeight,
    updatePrefs,
  }
})
