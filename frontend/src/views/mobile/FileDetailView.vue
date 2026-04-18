<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconChevronLeft } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import MobileEmptyState from '../../components/mobile/MobileEmptyState.vue'
import { useI18n } from '../../composables/useI18n'
import { showPrompt } from '../../composables/useNativeDialog'
import { useFileSystemStore } from '../../stores/fileSystem'
import type { FileListItem } from '../../types'

const route = useRoute()
const router = useRouter()
const fs = useFileSystemStore()
const { t } = useI18n()
const file = ref<FileListItem | null>(null)

const displayName = computed(() => file.value?._originalName || file.value?.name || t('common.details'))
const fromPath = computed(() => typeof route.query.from === 'string' ? route.query.from : '')

onMounted(async () => {
  const path = route.query.path
  if (typeof path !== 'string') return
  file.value = fs.sortedFiles.find(item => item.path === path) || null
  if (!file.value && path.includes('/')) {
    const parent = path.endsWith('/') ? path.replace(/[^/]+\/$/, '') : path.replace(/[^/]+$/, '')
    const entries = await fs.listFilesAtPath(parent || '/')
    file.value = entries.find(item => item.path === path) || null
  }
})

const parentPath = computed(() => {
  if (!file.value) return '/'
  const normalized = file.value.path.endsWith('/') ? file.value.path.slice(0, -1) : file.value.path
  const parent = normalized.slice(0, normalized.lastIndexOf('/')) || '/'
  return parent.endsWith('/') ? parent : parent + '/'
})

async function renameFile(): Promise<void> {
  if (!file.value) return
  const current = file.value._originalName || file.value.name.replace(/\/$/, '')
  const next = await showPrompt(t('mobile.details.rename'), current)
  if (!next || next === current) return
  await fs.rename(file.value.path, next, file.value.is_dir)
  file.value = { ...file.value, name: next }
}

function handleBack(): void {
  if (fromPath.value) {
    router.push(fromPath.value)
    return
  }
  router.back()
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="displayName" :subtitle="t('mobile.details.title')" show-back @back="handleBack">
      <template #back><IconChevronLeft width="18" height="18" /></template>
    </MobileTopBar>
    <div class="mobile-page-body">
      <MobileEmptyState v-if="!file" :title="t('mobile.details.title')" :body="t('mobile.details.unavailable')" />
      <template v-else>
        <div class="detail-card">
          <div class="detail-row"><span>{{ t('common.name') }}</span><strong>{{ displayName }}</strong></div>
          <div class="detail-row"><span>{{ t('common.path') }}</span><strong>{{ file.path }}</strong></div>
          <div class="detail-row"><span>{{ t('common.type') }}</span><strong>{{ file.is_dir ? t('mobile.files.folder') : (fs.getViewerType(displayName) || file.content_type || t('mobile.files.file')) }}</strong></div>
          <div class="detail-row"><span>{{ t('info.size') }}</span><strong>{{ t('common.size_bytes', { n: file.size || 0 }) }}</strong></div>
          <div class="detail-row"><span>{{ t('common.created') }}</span><strong>{{ file.created_at || '—' }}</strong></div>
          <div class="detail-row"><span>{{ t('common.modified') }}</span><strong>{{ file.last_modified || '—' }}</strong></div>
        </div>
        <div class="detail-actions">
          <button @click="renameFile">{{ t('mobile.details.rename') }}</button>
          <button @click="fs.downloadFile(file._shareId ? file : file.path)">{{ t('common.download') }}</button>
          <button @click="router.push({ path: '/m/files', query: { path: parentPath } })">{{ t('mobile.details.go_folder') }}</button>
          <button class="danger detail-actions-full" @click="fs.selectedFiles = [file.path]; fs.deleteSelected()">{{ t('mobile.selection.delete') }}</button>
        </div>
      </template>
    </div>
  </section>
</template>

<style lang="scss" scoped>
.mobile-page {
  min-height: 100dvh;
}

.mobile-page-body {
  padding: 16px 16px 188px;
}

.detail-card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 18px;
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
}

.detail-row {
  display: flex;
  flex-direction: column;
  gap: 6px;

  span {
    font-size: 12px;
    text-transform: uppercase;
    color: rgba(16, 32, 48, 0.48);
  }

  strong {
    word-break: break-word;
    color: #102030;
  }
}

.detail-actions {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
  margin-top: 16px;

  button {
    min-height: 48px;
    border: none;
    border-radius: 16px;
    background: rgba(255, 255, 255, 0.94);
    box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
    color: #102030;
    font-weight: 700;
    transition: transform 0.16s ease, box-shadow 0.16s ease, background 0.16s ease;

    &:active {
      transform: scale(0.98);
      box-shadow: 0 8px 18px rgba(11, 24, 36, 0.09);
    }
  }

  .danger {
    color: #c23242;
  }
}

.detail-actions-full {
  grid-column: 1 / -1;
}
</style>
