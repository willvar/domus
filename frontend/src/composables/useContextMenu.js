import { ref } from 'vue'
import { useFileSystemStore } from '../stores/fileSystem'

const show = ref(false)
const x = ref(0)
const y = ref(0)
const targetFile = ref(null)
const context = ref('dolphin') // 'dolphin' | 'desktop'

// Shared flag: when any context menu is dismissed by right-click,
// suppress the next context menu open (mimics desktop DE behavior).
let _suppressNext = false

export function suppressNextContextMenu() {
  _suppressNext = true
}

export function consumeContextMenuSuppress() {
  if (_suppressNext) {
    _suppressNext = false
    return true
  }
  return false
}

export function openContextMenu(event, file = null, ctx = 'dolphin') {
  if (consumeContextMenuSuppress()) return
  const fs = useFileSystemStore()
  targetFile.value = file
  context.value = ctx
  x.value = event.clientX
  y.value = event.clientY
  // Select the file if not already selected (right-click highlight)
  if (file && ctx === 'dolphin' && !fs.selectedFiles.includes(file.path)) {
    fs.selectedFiles = [file.path]
  }
  show.value = true
}

export function closeContextMenu() {
  show.value = false
}

export function useContextMenuState() {
  return { show, x, y, targetFile, context }
}
