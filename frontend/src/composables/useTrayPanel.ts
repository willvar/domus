import { ref, watch, onBeforeUnmount } from 'vue'
import type { Ref } from 'vue'

/**
 * Handles KDE Plasma-style tray panel behavior:
 * - Default: click outside the panel closes it
 * - "Keep Open" (pinned): ignores click-outside until unpinned
 *
 * @param isOpen - reactive visibility state
 * @param close - function to close the panel
 * @returns {{ panelRef, pinned, togglePin }}
 */
export function useTrayPanel(isOpen: Ref<boolean>, close: () => void): {
  panelRef: Ref<HTMLElement | null>
  pinned: Ref<boolean>
  togglePin: () => void
} {
  const panelRef: Ref<HTMLElement | null> = ref(null)
  const pinned: Ref<boolean> = ref(false)

  function togglePin(): void {
    pinned.value = !pinned.value
  }

  function onClickOutside(e: MouseEvent): void {
    if (pinned.value) return
    if (!panelRef.value) return
    // Ignore clicks inside the panel
    if (panelRef.value.contains(e.target as Node)) return
    // Ignore clicks on tray buttons (they handle their own toggle)
    if ((e.target as HTMLElement).closest('.taskbar-tray-btn')) return
    close()
  }

  watch(isOpen, (val: boolean) => {
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
