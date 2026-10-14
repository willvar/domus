<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import dayjs from 'dayjs'
import { NButton, NEmpty, NList, NListItem, NSpin, NTag } from 'naive-ui'
import { IconRestore } from '../barrels/icons'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { useAppMessage } from '../ui/feedback'
import { useAuthStore } from '../stores/auth'
import { parseTrashLocation } from '../utils/trashLocation'
import type { FileHistoryResponse, FileHistoryVersion, FileListItem } from '../types'

const props = defineProps<{
  show: boolean
  file: FileListItem | null
}>()

const emit = defineEmits<{
  restored: [generation: number, size: number]
}>()

const { t } = useI18n()
const auth = useAuthStore()
const message = useAppMessage()
const versions = ref<FileHistoryVersion[]>([])
const loading = ref(false)
const error = ref(false)
const busyGeneration = ref<number | null>(null)
const historyLimit = computed(() => {
  const value = auth.user?.history_limit
  return typeof value === 'number' && value >= 1 ? Math.floor(value) : 1
})
const visibleVersions = computed(() => versions.value.slice(0, historyLimit.value))

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
    const response = await api.post<{ generation: number; size: number }>(url, {
      path: target.path,
      generation: version.generation,
      expected_generation: props.file.generation,
    })
    message.success(t('history.restored'))
    await load()
    emit('restored', response.data.generation, response.data.size)
  } catch (cause: any) {
    if (cause?.response?.status === 409) message.error(t('history.conflict'))
    else if (cause?.response?.status === 507) message.error(t('history.quota_exceeded'))
    else message.error(t('history.restore_failed'))
  } finally {
    busyGeneration.value = null
  }
}

function date(value: string): string {
  return value ? dayjs(value).format('YYYY-MM-DD HH:mm:ss') : '—'
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
  <section v-if="props.show && props.file" class="history-section">
    <div class="history-heading">{{ t('history.title') }}</div>
    <NSpin v-if="loading" size="small" />
    <NEmpty v-else-if="error" :description="t('history.load_failed')" />
    <NEmpty v-else-if="!visibleVersions.length" :description="t('history.empty')" />
    <NList v-else bordered>
      <NListItem v-for="version in visibleVersions" :key="version.generation">
        <div class="history-version">
          <div class="history-version__meta">
            <span class="history-version__date">{{ date(version.created_at) }}</span>
            <span class="history-version__size">{{ size(version.size) }}</span>
            <div class="history-version__actions">
              <NTag v-if="version.current" size="small" type="success">{{ t('history.current') }}</NTag>
              <NButton
                v-else
                size="small"
                quaternary
                type="primary"
                :title="t('history.restore')"
                :aria-label="t('history.restore')"
                :loading="busyGeneration === version.generation"
                @click="restore(version)"
              >
                <template #icon><IconRestore /></template>
              </NButton>
            </div>
          </div>
        </div>
      </NListItem>
    </NList>
  </section>
</template>

<style scoped>
.history-section { margin-top: 20px; padding-top: 20px; border-top: 1px solid var(--domus-line, #e4e8f0); }
.history-heading { margin-bottom: 12px; color: var(--domus-muted); font-size: 12px; font-weight: 700; letter-spacing: .04em; text-transform: uppercase; }
.history-version { width: 100%; }
.history-version__meta { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; width: 100%; font-size: 12px; }
.history-version__meta span { color: var(--domus-muted); }
.history-version__date, .history-version__size { white-space: nowrap; }
.history-version__actions {
  --history-action-width: 44px;
  display: flex;
  width: var(--history-action-width);
  min-width: var(--history-action-width);
  flex: 0 0 var(--history-action-width);
  gap: 6px;
  margin-left: auto;
}
.history-version__actions :deep(.n-button), .history-version__actions :deep(.n-tag) {
  box-sizing: border-box;
  width: var(--history-action-width);
  min-width: var(--history-action-width);
  justify-content: center;
}
.history-version__actions :deep(.n-button) { padding: 0; }
.history-version__actions :deep(.n-tag) { padding-right: 0; padding-left: 0; }
</style>
