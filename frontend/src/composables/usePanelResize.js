import { inject, ref, onBeforeUnmount } from 'vue'

/**
 * Provides top-edge drag-to-resize for system tray panels.
 * Returns { panelSize, panelRef, handleRef, onMouseDown }.
 */
export function usePanelResize() {
  const panelSize = inject('trayPanelSize', ref({ width: 360, height: 420 }))
  const updatePanelSize = inject('updateTrayPanelSize', () => {})

  let startY = 0
  let startHeight = 0
  let dragging = false

  function onMouseDown(e) {
    e.preventDefault()
    startY = e.clientY
    startHeight = panelSize.value.height
    dragging = true
    document.addEventListener('mousemove', onMouseMove)
    document.addEventListener('mouseup', onMouseUp)
    document.body.style.cursor = 'ns-resize'
    document.body.style.userSelect = 'none'
  }

  function onMouseMove(e) {
    if (!dragging) return
    // Dragging up increases height (startY - currentY = delta)
    const delta = startY - e.clientY
    const newHeight = Math.max(200, Math.min(startHeight + delta, window.innerHeight - 100))
    updatePanelSize({ width: panelSize.value.width, height: newHeight })
  }

  function onMouseUp() {
    dragging = false
    document.removeEventListener('mousemove', onMouseMove)
    document.removeEventListener('mouseup', onMouseUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  onBeforeUnmount(() => {
    if (dragging) onMouseUp()
  })

  return { panelSize, onMouseDown }
}
