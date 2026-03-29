import { inject, ref, onBeforeUnmount } from 'vue'

const MAX_WIDTH = 560
const MIN_WIDTH = 280
const MIN_HEIGHT = 200

/**
 * Provides top-edge and left-edge drag-to-resize for system tray panels.
 * Returns { panelSize, onMouseDown (top), onLeftMouseDown (left) }.
 */
export function usePanelResize() {
  const panelSize = inject('trayPanelSize', ref({ width: 360, height: 420 }))
  const updatePanelSize = inject('updateTrayPanelSize', () => {})

  let dragging = false

  // --- Top edge (height) ---
  let startY = 0
  let startHeight = 0

  function onMouseDown(e) {
    e.preventDefault()
    startY = e.clientY
    startHeight = panelSize.value.height
    dragging = true
    document.addEventListener('mousemove', onTopMove)
    document.addEventListener('mouseup', onTopUp)
    document.body.style.cursor = 'ns-resize'
    document.body.style.userSelect = 'none'
  }

  function onTopMove(e) {
    if (!dragging) return
    const delta = startY - e.clientY
    const newHeight = Math.max(MIN_HEIGHT, Math.min(startHeight + delta, window.innerHeight - 100))
    updatePanelSize({ width: panelSize.value.width, height: newHeight })
  }

  function onTopUp() {
    dragging = false
    document.removeEventListener('mousemove', onTopMove)
    document.removeEventListener('mouseup', onTopUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  // --- Left edge (width) ---
  let startX = 0
  let startWidth = 0

  function onLeftMouseDown(e) {
    e.preventDefault()
    startX = e.clientX
    startWidth = panelSize.value.width
    dragging = true
    document.addEventListener('mousemove', onLeftMove)
    document.addEventListener('mouseup', onLeftUp)
    document.body.style.cursor = 'ew-resize'
    document.body.style.userSelect = 'none'
  }

  function onLeftMove(e) {
    if (!dragging) return
    const delta = startX - e.clientX
    const newWidth = Math.max(MIN_WIDTH, Math.min(startWidth + delta, MAX_WIDTH))
    updatePanelSize({ width: newWidth, height: panelSize.value.height })
  }

  function onLeftUp() {
    dragging = false
    document.removeEventListener('mousemove', onLeftMove)
    document.removeEventListener('mouseup', onLeftUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  onBeforeUnmount(() => {
    if (dragging) {
      onTopUp()
      onLeftUp()
    }
  })

  return { panelSize, onMouseDown, onLeftMouseDown }
}
