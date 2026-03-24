import { watch, onUnmounted } from 'vue'
import { useWindowManagerStore } from '../stores/windowManager'

let installed = false
let skipNextPopstate = false

/**
 * Close a window and sync browser history.
 * Can be called from any component after useWindowHistory() has been initialized.
 */
export function historyClose(id) {
  const wm = useWindowManagerStore()
  skipNextPopstate = true
  wm.closeWindow(id)
  history.back()
}

/**
 * Initialize browser history ↔ window manager sync.
 * Call once in App.vue when the user is logged in.
 */
export function useWindowHistory() {
  if (installed) return
  installed = true

  const wm = useWindowManagerStore()

  let handlingPopstate = false

  // Mark initial state as desktop
  history.replaceState({ zephyrDesktop: true }, '')

  // Push history entry when active window changes
  watch(() => wm.activeWindowId, (id, oldId) => {
    if (handlingPopstate) return
    if (id === oldId) return

    if (id) {
      history.pushState({ zephyrWindowId: id }, '')
    } else {
      history.pushState({ zephyrDesktop: true }, '')
    }
  })

  // Handle browser back/forward
  function onPopstate(e) {
    if (skipNextPopstate) {
      skipNextPopstate = false
      return
    }

    handlingPopstate = true
    try {
      const state = e.state
      if (state?.zephyrWindowId) {
        const win = wm.findWindow(state.zephyrWindowId)
        if (win) {
          win.minimized = false
          wm.bringToFront(state.zephyrWindowId)
        }
      } else if (state?.zephyrDesktop) {
        const activeId = wm.activeWindowId
        if (activeId) {
          wm.minimizeWindow(activeId)
        }
      }
    } finally {
      handlingPopstate = false
    }
  }

  window.addEventListener('popstate', onPopstate)

  onUnmounted(() => {
    window.removeEventListener('popstate', onPopstate)
    installed = false
  })
}
