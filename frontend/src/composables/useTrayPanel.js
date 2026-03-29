import { ref, watch, onBeforeUnmount } from 'vue'

/**
 * Handles KDE Plasma-style tray panel behavior:
 * - Default: click outside the panel closes it
 * - "Keep Open" (pinned): ignores click-outside until unpinned
 *
 * @param {Ref<boolean>} isOpen - reactive visibility state
 * @param {Function} close - function to close the panel
 * @returns {{ panelRef, pinned, togglePin }}
 */
export function useTrayPanel(isOpen, close) {
  const panelRef = ref(null)
  const pinned = ref(false)

  function togglePin() {
    pinned.value = !pinned.value
  }

  function onClickOutside(e) {
    if (pinned.value) return
    if (!panelRef.value) return
    // Ignore clicks inside the panel
    if (panelRef.value.contains(e.target)) return
    // Ignore clicks on tray buttons (they handle their own toggle)
    if (e.target.closest('.taskbar-tray-btn')) return
    close()
  }

  watch(isOpen, (val) => {
    if (val) {
      // Delay to avoid the same click that opened the panel from closing it
      requestAnimationFrame(() => {
        document.addEventListener('mousedown', onClickOutside, true)
      })
    } else {
      document.removeEventListener('mousedown', onClickOutside, true)
      pinned.value = false
    }
  })

  onBeforeUnmount(() => {
    document.removeEventListener('mousedown', onClickOutside, true)
  })

  return { panelRef, pinned, togglePin }
}
