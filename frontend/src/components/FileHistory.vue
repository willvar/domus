<script setup lang="ts">
import { ref, watch } from 'vue'
import dayjs from 'dayjs'
import { NButton, NDrawer, NDrawerContent, NEmpty, NList, NListItem, NSpin, NTag } from 'naive-ui'
import { IconDownload, IconRestore } from '../barrels/icons'
import api from '../composables/useApi'
import { useServiceWorker } from '../composables/useServiceWorker'
import { useI18n } from '../composables/useI18n'
import { useAppMessage } from '../ui/feedback'
import { parseTrashLocation } from '../utils/trashLocation'
import type { FileAccessResponse, FileHistoryResponse, FileHistoryVersion, FileListItem } from '../types'

const props = defineProps<{
  show: boolean
  file: FileListItem | null
}>()

const emit = defineEmits<{
  'update:show': [value: boolean]
  restored: []
}>()

const { t } = useI18n()
const message = useAppMessage()
const versions = ref<FileHistoryVersion[]>([])
const loading = ref(false)
const error = ref(false)
const busyGeneration = ref<number | null>(null)

function requestTarget(): { url: string; path: string } | null {
  if (!props.file) return null
  const trash = parseTrashLocation(props.file.path)
  if (trash?.id) {
    return { url: `/trash/${encodeURIComponent(trash.id)}/history`, path: trash.relativePath }
  }
  return { url: '/file/history', path: props.file.path }
}

async function load(): Promise<void> {
  const target = requestTarget()
  if (!props.show || !target) return
  loading.value = true
  error.value = false
  try {
    const response = await api.get<FileHistoryResponse>(target.url, { params: { path: target.path } })
    versions.value = response.data.versions || []
  } catch {
    versions.value = []
    error.value = true
  } finally {
    loading.value = false
  }
}

function accessTarget(): { url: string; path: string } | null {
  const target = requestTarget()
  if (!target) return null
  const trash = parseTrashLocation(props.file?.path || '')
  if (trash?.id) {
    return { url: `/trash/${encodeURIComponent(trash.id)}/history/access`, path: trash.relativePath }
  }
  return { url: '/file/history/access', path: props.file?.path || '' }
}

async function download(version: FileHistoryVersion): Promise<void> {
  const target = accessTarget()
  if (!target || !props.file) return
  busyGeneration.value = version.generation
  let decryptUrl: string | null = null
  let objectUrl: string | null = null
  try {
    const response = await api.get<FileAccessResponse>(target.url, {
      params: { path: target.path, generation: version.generation },
    })
    const access = response.data
    const sw = useServiceWorker()
    decryptUrl = sw.registerDecrypt({
      url: access.url,
      size: access.size,
      chunkSize: access.chunk_size,
      contentType: access.content_type,
      filename: access.name,
      dek: access.dek,
      download: true,
    })
    if (!decryptUrl) throw new Error('decrypt unavailable')
    await sw.flush()
    const decrypted = await fetch(decryptUrl)
    if (!decrypted.ok) throw new Error(`download failed: ${decrypted.status}`)
    objectUrl = URL.createObjectURL(await decrypted.blob())
    const anchor = document.createElement('a')
    anchor.href = objectUrl
    anchor.download = access.name || props.file.name
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
  } catch {
    message.error(t('history.download_failed'))
  } finally {
    if (objectUrl) window.setTimeout(() => URL.revokeObjectURL(objectUrl!), 60000)
    if (decryptUrl) useServiceWorker().unregisterDecrypt(decryptUrl)
    busyGeneration.value = null
  }
}

async function restore(version: FileHistoryVersion): Promise<void> {
  if (!props.file || version.current) return
  const target = requestTarget()
  if (!target) return
  busyGeneration.value = version.generation
  try {
    const trash = parseTrashLocation(props.file.path)
    const url = trash?.id
      ? `/trash/${encodeURIComponent(trash.id)}/history/restore`
      : '/file/history/restore'
    await api.post(url, {
      path: target.path,
      generation: version.generation,
      expected_generation: props.file.generation,
    })
    message.success(t('history.restored'))
    emit('update:show', false)
    emit('restored')
  } catch (cause: any) {
    if (cause?.response?.status === 409) message.error(t('history.conflict'))
    else if (cause?.response?.status === 507) message.error(t('history.quota_exceeded'))
    else message.error(t('history.restore_failed'))
  } finally {
    busyGeneration.value = null
  }
}

function date(value: string): string {
  return value ? dayjs(value).format('YYYY-MM-DD HH:mm') : '—'
}

function size(value: number): string {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const amount = value / (1024 ** index)
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}

watch(() => [props.show, props.file?.path] as const, () => { void load() }, { immediate: true })
</script>

<template>
  <NDrawer :show="props.show" :placement="'right'" :width="380" @update:show="emit('update:show', $event)">
    <NDrawerContent v-if="props.file" :title="t('history.title')" closable>
      <div class="history-file">{{ props.file.name }}</div>
      <NSpin v-if="loading" size="small" />
      <NEmpty v-else-if="error" :description="t('history.load_failed')" />
      <NEmpty v-else-if="!versions.length" :description="t('history.empty')" />
      <NList v-else bordered>
        <NListItem v-for="version in versions" :key="version.generation">
          <div class="history-version">
            <div class="history-version__meta">
              <strong>{{ t('history.version', { n: version.generation }) }}</strong>
              <NTag v-if="version.current" size="small" type="success">{{ t('history.current') }}</NTag>
              <span>{{ date(version.created_at) }}</span>
              <span>{{ size(version.size) }}</span>
            </div>
            <div class="history-version__actions">
              <NButton size="small" quaternary :loading="busyGeneration === version.generation" @click="download(version)">
                <template #icon><IconDownload /></template>{{ t('menu.download') }}
              </NButton>
              <NButton v-if="!version.current" size="small" secondary type="primary" :loading="busyGeneration === version.generation" @click="restore(version)">
                <template #icon><IconRestore /></template>{{ t('history.restore') }}
              </NButton>
            </div>
          </div>
        </NListItem>
      </NList>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.history-file { margin-bottom: 16px; overflow: hidden; color: var(--domus-muted); text-overflow: ellipsis; white-space: nowrap; }
.history-version { display: grid; gap: 10px; width: 100%; }
.history-version__meta { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; font-size: 12px; }
.history-version__meta strong { margin-right: auto; font-size: 13px; }
.history-version__meta span { color: var(--domus-muted); }
.history-version__actions { display: flex; gap: 6px; }
</style>
