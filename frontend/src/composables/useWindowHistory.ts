import { watch, onUnmounted } from 'vue'
import { useWindowManagerStore } from '../stores/windowManager'

interface ZephyrHistoryState {
  zephyrDesktop?: boolean
  zephyrWindowId?: string
}

let installed: boolean = false
let skipNextPopstate: boolean = false

/**
 * Close a window and sync browser history.
 * Can be called from any component after useWindowHistory() has been initialized.
 */
export function historyClose(id: string): void {
  const wm = useWindowManagerStore()
  skipNextPopstate = true
  wm.closeWindow(id)
  history.back()
}

/**
 * Initialize browser history <-> window manager sync.
 * Call once in App.vue when the user is logged in.
 */
export function useWindowHistory(): void {
  if (installed) return
  installed = true

  const wm = useWindowManagerStore()

  let handlingPopstate: boolean = false

  // Mark initial state as desktop
  history.replaceState({ zephyrDesktop: true } as ZephyrHistoryState, '')

  // Push history entry when active window changes
  watch(() => wm.activeWindowId, (id: string | null, oldId: string | null) => {
    if (handlingPopstate) return
    if (id === oldId) return

    if (id) {
      history.pushState({ zephyrWindowId: id } as ZephyrHistoryState, '')
    } else {
      history.pushState({ zephyrDesktop: true } as ZephyrHistoryState, '')
    }
  })

  // Handle browser back/forward
  function onPopstate(e: PopStateEvent): void {
    if (skipNextPopstate) {
      skipNextPopstate = false
      return
    }

    handlingPopstate = true
    try {
      const state = e.state as ZephyrHistoryState | null
      if (state?.zephyrWindowId) {
        const win = wm.findWindow(state.zephyrWindowId)
        if (win) {
          win.minimized = false
          wm.bringToFront(state.zephyrWindowId)
        }
      } else if (state?.zephyrDesktop) {
        const activeId: string | null = wm.activeWindowId
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
