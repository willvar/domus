import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import iconFileManager from '../assets/icons/app-file-manager.svg?raw'

export const FILES_ICON = iconFileManager

export const useWindowManagerStore = defineStore('windowManager', () => {
  const windows = ref([])
  // { id, title, icon, type, minimized, maximized, tiled, x, y, width, height, zIndex, data,
  //   _restoreRect: { x, y, width, height } }

  let zCounter = 100

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
  }
})
