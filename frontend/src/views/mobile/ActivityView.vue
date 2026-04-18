<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { IconChevronLeft } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import MobileEmptyState from '../../components/mobile/MobileEmptyState.vue'
import { useI18n } from '../../composables/useI18n'
import { useUploadStore } from '../../stores/upload'
import { useTasksStore } from '../../stores/tasks'
import { usePendingOpsStore } from '../../stores/pendingOps'

const router = useRouter()
const upload = useUploadStore()
const tasks = useTasksStore()
const pendingOps = usePendingOpsStore()
const { t } = useI18n()

onMounted(() => {
  tasks.fetchTasks()
})

const activeTaskIds = computed(() => new Set(upload.uploads.map(item => item.taskId).filter(Boolean)))
const filteredTasks = computed(() => tasks.tasks.filter(task => !activeTaskIds.value.has(task.task_id)))
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.activity.title')" :subtitle="t('mobile.activity.subtitle')" show-back @back="router.back()">
      <template #back><IconChevronLeft width="18" height="18" /></template>
    </MobileTopBar>

    <div class="mobile-page-body activity-body">
      <section class="activity-card">
        <div class="activity-title">{{ t('mobile.activity.uploads') }}</div>
        <MobileEmptyState v-if="upload.uploads.length === 0" :title="t('mobile.activity.uploads')" :body="t('mobile.activity.no_uploads')" />
        <div v-for="item in upload.uploads" :key="item.id" class="activity-row">
          <div>
            <div class="activity-name">{{ item.fileName }}</div>
            <div class="activity-meta">{{ item.phase || item.status }}</div>
          </div>
          <div class="activity-progress">{{ item.progress }}%</div>
          <div class="activity-progress-bar"><span :style="{ width: `${item.progress}%` }" /></div>
        </div>
      </section>

      <section class="activity-card">
        <div class="activity-title">{{ t('mobile.activity.tasks') }}</div>
        <MobileEmptyState v-if="filteredTasks.length === 0" :title="t('mobile.activity.tasks')" :body="t('mobile.activity.no_tasks')" />
        <div v-for="task in filteredTasks" :key="task.task_id" class="activity-row">
          <div>
            <div class="activity-name">{{ task.name }}</div>
            <div class="activity-meta">{{ task.phase || task.status }}</div>
          </div>
          <div class="activity-progress">{{ Math.round(task.progress * 100) }}%</div>
          <div class="activity-progress-bar"><span :style="{ width: `${Math.round(task.progress * 100)}%` }" /></div>
        </div>
      </section>

      <section class="activity-card">
        <div class="activity-title">{{ t('mobile.activity.pending') }}</div>
        <MobileEmptyState v-if="pendingOps.ops.length === 0" :title="t('mobile.activity.pending')" :body="t('mobile.activity.no_pending')" />
        <div v-for="op in pendingOps.ops" :key="op.id" class="activity-row">
          <div>
            <div class="activity-name">{{ op.description || op.type || t('pending.title') }}</div>
            <div class="activity-meta">{{ op.lastError || t('mobile.activity.pending_waiting') }}</div>
          </div>
          <button class="activity-btn" @click="pendingOps.retry(op.id)">{{ t('mobile.activity.retry') }}</button>
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

.activity-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.activity-card {
  padding: 18px;
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
}

.activity-title {
  margin-bottom: 12px;
  font-size: 14px;
  font-weight: 700;
  color: #102030;
}

.activity-row {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 0;
  border-top: 1px solid rgba(16, 32, 48, 0.08);
}

.activity-row:first-of-type {
  border-top: none;
  padding-top: 0;
}

.activity-name {
  font-size: 14px;
  font-weight: 700;
  color: #102030;
}

.activity-meta,
.activity-empty {
  font-size: 12px;
  color: rgba(16, 32, 48, 0.56);
}

.activity-progress,
.activity-btn {
  flex-shrink: 0;
}

.activity-progress {
  min-width: 44px;
  text-align: right;
}

.activity-progress-bar {
  width: 100%;
  height: 6px;
  border-radius: 999px;
  background: rgba(16, 32, 48, 0.08);
  overflow: hidden;

  span {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: linear-gradient(90deg, #1576b8, #3daee9);
  }
}

.activity-btn {
  min-height: 36px;
  border: none;
  border-radius: 12px;
  padding: 0 12px;
  background: rgba(61, 174, 233, 0.12);
  color: #1576b8;
  font-weight: 700;
  @include inline-flex-center;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.96);
    background: rgba(61, 174, 233, 0.2);
  }
}
</style>
