import { onMounted, onUnmounted } from 'vue'
import { useFileSystemStore } from '../stores/fileSystem'
import { useAuthStore } from '../stores/auth'

export function useKeyboard() {
  const fs = useFileSystemStore()
  const auth = useAuthStore()

  function handler(e) {
    if (!auth.isLoggedIn) return

    // Don't intercept when typing in inputs
    const tag = e.target.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA' || e.target.isContentEditable) {
      if (e.key === 'Escape') {
        e.target.blur()
        fs.cancelRename()
      }
      return
    }

    const ctrl = e.ctrlKey || e.metaKey
    const alt = e.altKey

    switch (true) {
      // Alt+T - New tab
      case alt && e.key === 't':
        e.preventDefault()
        fs.createTab()
        break

      // Alt+W - Close tab
      case alt && e.key === 'w':
        e.preventDefault()
        fs.closeTab(fs.activeTabId)
        break

      // Alt+] - Next tab
      case alt && e.key === ']':
        e.preventDefault()
        fs.nextTab()
        break

      // Alt+[ - Previous tab
      case alt && e.key === '[':
        e.preventDefault()
        fs.prevTab()
        break

      // F2 - Rename
      case e.key === 'F2':
        e.preventDefault()
        if (fs.selectedFiles.length === 1) fs.startRename()
        break

      // Delete
      case e.key === 'Delete':
        e.preventDefault()
        if (fs.selectedFiles.length > 0) fs.deleteSelected()
        break

      // Ctrl+C - Copy
      case ctrl && e.key === 'c':
        e.preventDefault()
        fs.copySelected()
        break

      // Ctrl+X - Cut
      case ctrl && e.key === 'x':
        e.preventDefault()
        fs.cutSelected()
        break

      // Ctrl+V - Paste
      case ctrl && e.key === 'v':
        e.preventDefault()
        fs.paste()
        break

      // Ctrl+A - Select all
      case ctrl && e.key === 'a':
        e.preventDefault()
        fs.selectAll()
        break

      // Ctrl+L - Focus path bar
      case ctrl && e.key === 'l':
        e.preventDefault()
        fs.focusPathBar = true
        break

      // Alt+Left - Back
      case alt && e.key === 'ArrowLeft':
        e.preventDefault()
        fs.goBack()
        break

      // Alt+Right - Forward
      case alt && e.key === 'ArrowRight':
        e.preventDefault()
        fs.goForward()
        break

      // Alt+Up - Parent
      case alt && e.key === 'ArrowUp':
        e.preventDefault()
        fs.goUp()
        break

      // Ctrl+N - New folder
      case ctrl && e.key === 'n':
        e.preventDefault()
        fs.createFolder()
        break

      // F4 - Toggle terminal panel
      case e.key === 'F4':
        e.preventDefault()
        fs.toggleTerminal()
        break

      // F5 - Refresh
      case e.key === 'F5':
        e.preventDefault()
        fs.refresh()
        break

      // Ctrl+1/2/3 - View modes
      case ctrl && e.key === '1':
        e.preventDefault()
        fs.viewMode = 'icons'
        break
      case ctrl && e.key === '2':
        e.preventDefault()
        fs.viewMode = 'compact'
        break
      case ctrl && e.key === '3':
        e.preventDefault()
        fs.viewMode = 'details'
        break

      // Ctrl+F - Search
      case ctrl && e.key === 'f':
        e.preventDefault()
        fs.focusSearch = true
        break

      // Enter - Open
      case e.key === 'Enter':
        e.preventDefault()
        if (fs.selectedFiles.length === 1) fs.openSelected()
        break

      // Backspace - Go up
      case e.key === 'Backspace':
        e.preventDefault()
        fs.goUp()
        break

      // Escape - Clear selection (skip if app window is open — ViewerApp handles it)
      case e.key === 'Escape':
        if (fs.appWindows.length > 0) return
        e.preventDefault()
        fs.clearSelection()
        fs.cancelRename()
        break
    }
  }

  onMounted(() => window.addEventListener('keydown', handler))
  onUnmounted(() => window.removeEventListener('keydown', handler))
}
