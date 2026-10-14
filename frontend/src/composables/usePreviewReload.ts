import { ref } from 'vue'

export const previewReloadKey = ref(0)

export function reloadPreviewComponent(): void {
  previewReloadKey.value++
}
