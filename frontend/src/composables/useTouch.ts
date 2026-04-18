/**
 * Touch helpers for mobile support.
 * - Tap detection (single tap with long-press suppression)
 * - Long-press detection (since contextmenu doesn't fire reliably on touch)
 * - Touch drag (since mousedown/mousemove/mouseup don't fire on touch)
 */

import type { TouchHandlerOptions, TouchDragOptions } from '../types'

const LONG_PRESS_DELAY: number = 500
const LONG_PRESS_MOVE_THRESHOLD: number = 10

export function useTouchHandlers(opts: TouchHandlerOptions = {}): {
  onTouchStart: (e: TouchEvent) => void
  onTouchMove: (e: TouchEvent) => void
  onTouchEnd: (e: TouchEvent) => void
} {
  let longPressTimer: ReturnType<typeof setTimeout> | null = null
  let startX: number = 0
  let startY: number = 0
  let longPressFired: boolean = false

  function onTouchStart(e: TouchEvent): void {
    const touch: Touch = e.touches[0]
    startX = touch.clientX
    startY = touch.clientY
    longPressFired = false

    if (opts.onLongPress) {
      longPressTimer = setTimeout(() => {
        longPressFired = true
        opts.onLongPress!(e)
      }, LONG_PRESS_DELAY)
    }
  }

  function onTouchMove(e: TouchEvent): void {
    if (!longPressTimer) return
    const touch: Touch = e.touches[0]
    const dx: number = Math.abs(touch.clientX - startX)
    const dy: number = Math.abs(touch.clientY - startY)
    if (dx > LONG_PRESS_MOVE_THRESHOLD || dy > LONG_PRESS_MOVE_THRESHOLD) {
      clearTimeout(longPressTimer)
      longPressTimer = null
    }
  }

  function onTouchEnd(e: TouchEvent): void {
    clearTimeout(longPressTimer!)
    longPressTimer = null

    if (longPressFired) return

    if (opts.onTap) {
      e.preventDefault()
      opts.onTap(e)
    }
  }

  return { onTouchStart, onTouchMove, onTouchEnd }
}

/**
 * Creates touch-drag handlers (for window dragging, etc.)
 */
export function useTouchDrag(opts: TouchDragOptions = {}): {
  onTouchStart: (e: TouchEvent) => void
} {
  function onTouchStart(e: TouchEvent): void {
    if (e.touches.length !== 1) return
    const t: Touch = e.touches[0]
    opts.onStart?.(t.clientX, t.clientY, e)

    const onMove = (ev: TouchEvent): void => {
      const t2: Touch = ev.touches[0]
      opts.onMove?.(t2.clientX, t2.clientY, ev)
    }
    const onEnd = (ev: TouchEvent): void => {
      window.removeEventListener('touchmove', onMove)
      window.removeEventListener('touchend', onEnd)
      opts.onEnd?.(ev)
    }
    window.addEventListener('touchmove', onMove, { passive: false })
    window.addEventListener('touchend', onEnd)
  }

  return { onTouchStart }
}
