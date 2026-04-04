import { inject, ref, onBeforeUnmount } from 'vue'
import type { Ref } from 'vue'
import type { PanelSize } from '../types'

const MAX_WIDTH: number = 560
const MIN_WIDTH: number = 280
const MIN_HEIGHT: number = 200

/**
 * Provides top-edge and left-edge drag-to-resize for system tray panels.
 * Returns { panelSize, onMouseDown (top), onLeftMouseDown (left) }.
 */
export function usePanelResize(): {
  panelSize: Ref<PanelSize>
  onMouseDown: (e: MouseEvent) => void
  onLeftMouseDown: (e: MouseEvent) => void
} {
  const panelSize: Ref<PanelSize> = inject('trayPanelSize', ref({ width: 360, height: 420 }))
  const updatePanelSize: (size: PanelSize) => void = inject('updateTrayPanelSize', () => {})

  let dragging: boolean = false

  // --- Top edge (height) ---
  let startY: number = 0
  let startHeight: number = 0

  function onMouseDown(e: MouseEvent): void {
    e.preventDefault()
    startY = e.clientY
    startHeight = panelSize.value.height
    dragging = true
    document.addEventListener('mousemove', onTopMove)
    document.addEventListener('mouseup', onTopUp)
    document.body.style.cursor = 'ns-resize'
    document.body.style.userSelect = 'none'
  }

  function onTopMove(e: MouseEvent): void {
    if (!dragging) return
    const delta: number = startY - e.clientY
    const newHeight: number = Math.max(MIN_HEIGHT, Math.min(startHeight + delta, window.innerHeight - 100))
    updatePanelSize({ width: panelSize.value.width, height: newHeight })
  }

  function onTopUp(): void {
    dragging = false
    document.removeEventListener('mousemove', onTopMove)
    document.removeEventListener('mouseup', onTopUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  // --- Left edge (width) ---
  let startX: number = 0
  let startWidth: number = 0

  function onLeftMouseDown(e: MouseEvent): void {
    e.preventDefault()
    startX = e.clientX
    startWidth = panelSize.value.width
    dragging = true
    document.addEventListener('mousemove', onLeftMove)
    document.addEventListener('mouseup', onLeftUp)
    document.body.style.cursor = 'ew-resize'
    document.body.style.userSelect = 'none'
  }

  function onLeftMove(e: MouseEvent): void {
    if (!dragging) return
    const delta: number = startX - e.clientX
    const newWidth: number = Math.max(MIN_WIDTH, Math.min(startWidth + delta, MAX_WIDTH))
    updatePanelSize({ width: newWidth, height: panelSize.value.height })
  }

  function onLeftUp(): void {
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
