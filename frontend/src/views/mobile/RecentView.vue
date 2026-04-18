<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconRefresh } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import MobileFileListItem from '../../components/mobile/MobileFileListItem.vue'
import MobileEmptyState from '../../components/mobile/MobileEmptyState.vue'
import { useMobileUiStore } from '../../stores/mobileUi'
import { useUploadStore } from '../../stores/upload'
import { useTasksStore } from '../../stores/tasks'
import { useI18n } from '../../composables/useI18n'
import type { FileListItem } from '../../types'

const route = useRoute()
const router = useRouter()
const mobileUi = useMobileUiStore()
const upload = useUploadStore()
const tasks = useTasksStore()
const { t } = useI18n()
const items = computed(() => mobileUi.recentItems)
const uploadItems = computed(() => upload.uploads.slice(0, 6))
const taskItems = computed(() => tasks.completedTasks.slice(0, 6))

onMounted(() => {
  tasks.fetchTasks().catch(() => {})
})

function openItem(file: FileListItem): void {
  if (file.is_dir) {
    router.push({ path: '/m/files', query: { path: file.path } })
    return
  }
  router.push({ path: '/m/preview', query: { path: file.path, shareId: file._shareId || '', name: file._originalName || file.name, from: route.fullPath } })
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.recent.title')" :subtitle="t('mobile.recent.subtitle')">
      <template #actions>
        <button class="mobile-page-icon-btn" @click="router.push('/m/activity')"><IconRefresh width="18" height="18" /></button>
      </template>
    </MobileTopBar>

    <div class="mobile-page-body">
      <section class="recent-section">
        <div class="recent-section-title">{{ t('mobile.recent.opened') }}</div>
        <MobileEmptyState v-if="items.length === 0" :title="t('mobile.recent.empty_title')" :body="t('mobile.recent.empty_body')" />
        <div v-else class="mobile-file-list">
          <MobileFileListItem
            v-for="file in items"
            :key="`${file.path}-${file.opened_at}`"
            :file="file as FileListItem"
            :meta="file.opened_at"
            @open="openItem(file as FileListItem)"
            @more="mobileUi.openFileSheet(file as FileListItem)"
            @longpress="mobileUi.openFileSheet(file as FileListItem)"
            @toggle-select="void 0"
          />
        </div>
      </section>

      <section class="recent-section">
        <div class="recent-section-title">{{ t('mobile.recent.uploads') }}</div>
        <MobileEmptyState v-if="uploadItems.length === 0" :title="t('mobile.recent.uploads_empty_title')" :body="t('mobile.recent.uploads_empty_body')" />
        <div v-else class="recent-upload-list">
          <div v-for="item in uploadItems" :key="item.id" class="recent-upload-card">
            <strong>{{ item.fileName }}</strong>
            <span>{{ item.phase || item.status }} · {{ item.progress }}%</span>
          </div>
        </div>
      </section>

      <section class="recent-section">
        <div class="recent-section-title">{{ t('mobile.recent.tasks') }}</div>
        <MobileEmptyState v-if="taskItems.length === 0" :title="t('mobile.recent.tasks_empty_title')" :body="t('mobile.recent.tasks_empty_body')" />
        <div v-else class="recent-upload-list">
          <div v-for="task in taskItems" :key="task.task_id" class="recent-upload-card">
            <strong>{{ task.name }}</strong>
            <span>{{ task.phase || task.status }}</span>
          </div>
        </div>
      </section>
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

.mobile-file-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.recent-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.recent-section-title {
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: rgba(16, 32, 48, 0.5);
}

.recent-upload-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.recent-upload-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 16px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
  transition: transform 0.16s ease, box-shadow 0.16s ease;

  &:active {
    transform: scale(0.99);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.09);
  }

  strong {
    color: #102030;
  }

  span {
    font-size: 12px;
    color: rgba(16, 32, 48, 0.58);
  }
}

.mobile-page-icon-btn {
  width: 40px;
  height: 40px;
  border: none;
  border-radius: 12px;
  background: rgba(16, 32, 48, 0.06);
  @include inline-flex-center;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.94);
    background: rgba(16, 32, 48, 0.1);
  }
}
</style>
