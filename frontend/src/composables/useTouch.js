/**
 * Touch helpers for mobile support.
 * - Double-tap detection (since dblclick doesn't fire on touch)
 * - Long-press detection (since contextmenu doesn't fire reliably on touch)
 * - Touch drag (since mousedown/mousemove/mouseup don't fire on touch)
 */

const DOUBLE_TAP_DELAY = 300
const LONG_PRESS_DELAY = 500
const LONG_PRESS_MOVE_THRESHOLD = 10

let lastTapTime = 0
let lastTapTarget = null

/**
 * Returns touch event handlers for an element.
 * @param {Object} opts
 * @param {Function} opts.onDoubleTap - called on double-tap
 * @param {Function} opts.onLongPress - called with (touch event) on long press
 * @param {Function} opts.onTap - called on single tap (after delay to distinguish from double)
 */
export function useTouchHandlers(opts = {}) {
  let longPressTimer = null
  let startX = 0
  let startY = 0
  let longPressFired = false

  function onTouchStart(e) {
    const touch = e.touches[0]
    startX = touch.clientX
    startY = touch.clientY
    longPressFired = false

    if (opts.onLongPress) {
      longPressTimer = setTimeout(() => {
        longPressFired = true
        opts.onLongPress(e)
      }, LONG_PRESS_DELAY)
    }
  }

  function onTouchMove(e) {
    if (!longPressTimer) return
    const touch = e.touches[0]
    const dx = Math.abs(touch.clientX - startX)
    const dy = Math.abs(touch.clientY - startY)
    if (dx > LONG_PRESS_MOVE_THRESHOLD || dy > LONG_PRESS_MOVE_THRESHOLD) {
      clearTimeout(longPressTimer)
      longPressTimer = null
    }
  }

  function onTouchEnd(e) {
    clearTimeout(longPressTimer)
    longPressTimer = null

    if (longPressFired) return

    const now = Date.now()
    const target = e.target

    if (opts.onDoubleTap && (now - lastTapTime < DOUBLE_TAP_DELAY) && lastTapTarget === target) {
      e.preventDefault()
      lastTapTime = 0
      lastTapTarget = null
      opts.onDoubleTap(e)
    } else {
      lastTapTime = now
      lastTapTarget = target
    }
  }

  return { onTouchStart, onTouchMove, onTouchEnd }
}

/**
 * Creates touch-drag handlers (for window dragging, etc.)
 * @param {Object} opts
 * @param {Function} opts.onStart - (x, y, event) called on touch start
 * @param {Function} opts.onMove - (x, y, event) called on touch move
 * @param {Function} opts.onEnd - (event) called on touch end
 */
export function useTouchDrag(opts = {}) {
  function onTouchStart(e) {
    if (e.touches.length !== 1) return
    const t = e.touches[0]
    opts.onStart?.(t.clientX, t.clientY, e)

    const onMove = (ev) => {
      const t2 = ev.touches[0]
      opts.onMove?.(t2.clientX, t2.clientY, ev)
    }
    const onEnd = (ev) => {
      window.removeEventListener('touchmove', onMove)
      window.removeEventListener('touchend', onEnd)
      opts.onEnd?.(ev)
    }
    window.addEventListener('touchmove', onMove, { passive: false })
    window.addEventListener('touchend', onEnd)
  }

  return { onTouchStart }
}
