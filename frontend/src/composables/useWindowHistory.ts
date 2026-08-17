import { watch, onUnmounted } from 'vue'
import { useWindowManagerStore } from '../stores/windowManager'

interface DomusHistoryState {
  domusDesktop?: boolean
  domusWindowId?: string
  domusWindowDepth?: number
  zephyrDesktop?: boolean
  zephyrWindowId?: string
}

const MAX_WINDOW_HISTORY_DEPTH = 32
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
  const initialState = (history.state && typeof history.state === 'object') ? history.state : {}
  history.replaceState({
    ...initialState,
    domusDesktop: true,
    domusWindowId: undefined,
    domusWindowDepth: 0,
  } as DomusHistoryState, '')

  // Push history entry when active window changes
  watch(() => wm.activeWindowId, (id: string | null, oldId: string | null) => {
    if (handlingPopstate) return
    if (id === oldId) return

    const current = (history.state && typeof history.state === 'object') ? history.state : {}
    const depth = typeof current.domusWindowDepth === 'number' ? current.domusWindowDepth : 0
    const nextState = {
      ...current,
      domusDesktop: !id,
      domusWindowId: id || undefined,
      domusWindowDepth: Math.min(depth + 1, MAX_WINDOW_HISTORY_DEPTH),
    } as DomusHistoryState

    if (depth >= MAX_WINDOW_HISTORY_DEPTH) {
      history.replaceState(nextState, '')
    } else {
      history.pushState(nextState, '')
    }
  }, { flush: 'sync' })

  // Handle browser back/forward
  function onPopstate(e: PopStateEvent): void {
    if (skipNextPopstate) {
      skipNextPopstate = false
      return
    }

    handlingPopstate = true
    try {
      const state = e.state as DomusHistoryState | null
      const windowId = state?.domusWindowId || state?.zephyrWindowId
      if (windowId) {
        const win = wm.findWindow(windowId)
        if (win) {
          win.minimized = false
          wm.bringToFront(windowId)
        }
      } else if (state?.domusDesktop || state?.zephyrDesktop) {
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
