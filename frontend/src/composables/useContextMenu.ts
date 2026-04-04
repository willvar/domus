import { ref } from 'vue'
import type { Ref } from 'vue'
import { useFileSystemStore } from '../stores/fileSystem'
import type { FileListItem } from '../types'

const show: Ref<boolean> = ref(false)
const x: Ref<number> = ref(0)
const y: Ref<number> = ref(0)
const targetFile: Ref<FileListItem | null> = ref(null)
const context: Ref<'dolphin' | 'desktop'> = ref('dolphin') // 'dolphin' | 'desktop'

// Shared flag: when any context menu is dismissed by right-click,
// suppress the next context menu open (mimics desktop DE behavior).
let _suppressNext: boolean = false

export function suppressNextContextMenu(): void {
  _suppressNext = true
}

export function consumeContextMenuSuppress(): boolean {
  if (_suppressNext) {
    _suppressNext = false
    return true
  }
  return false
}

export function openContextMenu(event: MouseEvent, file: FileListItem | null = null, ctx: 'dolphin' | 'desktop' = 'dolphin'): void {
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

export function closeContextMenu(): void {
  show.value = false
}

export function useContextMenuState(): {
  show: Ref<boolean>
  x: Ref<number>
  y: Ref<number>
  targetFile: Ref<FileListItem | null>
  context: Ref<'dolphin' | 'desktop'>
} {
  return { show, x, y, targetFile, context }
}
