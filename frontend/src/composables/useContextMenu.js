import { ref } from 'vue'
import { useFileSystemStore } from '../stores/fileSystem'

const show = ref(false)
const x = ref(0)
const y = ref(0)
const targetFile = ref(null)

export function openContextMenu(event, file = null) {
  const fs = useFileSystemStore()
  targetFile.value = file
  x.value = event.clientX
  y.value = event.clientY
  // Select the file if not already selected (right-click highlight)
  if (file && !fs.selectedFiles.includes(file.path)) {
    fs.selectedFiles = [file.path]
  }
  show.value = true
}

export function closeContextMenu() {
  show.value = false
}

export function useContextMenuState() {
  return { show, x, y, targetFile }
}
