<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue'
import { readThumbnail, thumbnailIdentity } from '../composables/useThumbnail'
import { supportsThumbnail } from '../utils/fileKind'
import { useI18n } from '../composables/useI18n'
import type { FileListItem } from '../types'

const props = defineProps<{ file: FileListItem; enabled: boolean }>()
const { t } = useI18n()
const element = ref<HTMLElement | null>(null)
const visible = ref(false)
const pageVisible = ref(document.visibilityState !== 'hidden')
const image = ref('')
const failed = ref(false)
let observer: IntersectionObserver | null = null
let request: AbortController | null = null
const identity = computed(() => thumbnailIdentity(props.file))
const pending = computed(() => visible.value && pageVisible.value && props.enabled && supportsThumbnail(props.file.content_type) &&
  (!props.file.thumbnail_url || ['pending','running','failed'].includes(props.file.thumbnail_status || '')) &&
  (props.file.thumbnail_status !== 'failed' || (props.file.thumbnail_attempts || 0) < 3))

function cancel(): void { request?.abort(); request = null }
function clearImage(): void { if (image.value) URL.revokeObjectURL(image.value); image.value = '' }
function imageFailed(): void { clearImage(); failed.value = true }

async function load(): Promise<void> {
  if (!visible.value || !pageVisible.value || !props.enabled || !props.file.thumbnail_url || !props.file.thumbnail_dek || image.value || request || failed.value) return
  const control = new AbortController()
  request = control
  const timeout = setTimeout(() => control.abort(new DOMException('Thumbnail timed out', 'TimeoutError')), 30_000)
  try {
    const blob = await readThumbnail(props.file, control.signal)
    if (!control.signal.aborted && request === control) image.value = URL.createObjectURL(blob)
  } catch {
    if (!control.signal.aborted || control.signal.reason?.name === 'TimeoutError') failed.value = true
  } finally {
    clearTimeout(timeout)
    if (request === control) request = null
  }
}

function start(): void {
  if (observer || !element.value) return
  observer = new IntersectionObserver(entries => {
    visible.value = entries.some(entry => entry.isIntersecting && entry.intersectionRatio > 0)
    if (visible.value) void load()
    else cancel()
  }, { threshold: [0, 0.01] })
  observer.observe(element.value)
  navigator.serviceWorker?.addEventListener('controllerchange', workerChanged)
  document.addEventListener('visibilitychange', visibilityChanged)
  visibilityChanged()
}
function visibilityChanged(): void { pageVisible.value = document.visibilityState !== 'hidden'; if (pageVisible.value) void load(); else cancel() }
function workerChanged(): void { failed.value = false; void load() }
function stop(): void {
  observer?.disconnect(); observer = null
  navigator.serviceWorker?.removeEventListener('controllerchange', workerChanged)
  document.removeEventListener('visibilitychange', visibilityChanged)
  visible.value = false
  cancel(); failed.value = false
}
watch(identity, () => { cancel(); clearImage(); failed.value = false; void load() })
watch(() => props.enabled, enabled => {
  if (enabled) void load()
  else { cancel(); clearImage(); failed.value = false }
})
// Re-signed metadata may recover an expired read without reloading good images.
watch(() => props.file.thumbnail_url, () => { if (failed.value) { failed.value = false; void load() } })
onMounted(start)
onActivated(start)
onDeactivated(stop)
onBeforeUnmount(() => { stop(); clearImage() })
</script>

<template>
  <span ref="element" class="thumbnail-surface" :data-thumbnail-available="Boolean(file.thumbnail_url)" :data-thumbnail-state="failed ? 'failed' : image ? 'ready' : pending ? 'pending' : 'idle'" :title="failed ? t('files.thumbnail_load_failed') : undefined">
    <img v-if="enabled && image" :src="image" class="file-thumbnail" alt="" draggable="false" decoding="async" @error="imageFailed" />
    <slot v-else />
  </span>
</template>

<style scoped>
.thumbnail-surface { display: inline-flex; align-items: center; justify-content: center; width: 100%; height: 100%; min-width: 0; }
.file-thumbnail { width: 100%; height: 100%; object-fit: contain; }
</style>
