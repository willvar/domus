<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useFileSystemStore } from '../stores/fileSystem'
import { useUploadStore } from '../stores/upload'
import { useTasksStore } from '../stores/tasks'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useMobileUiStore } from '../stores/mobileUi'
import { useMessage } from '../composables/useMessage'
import { useI18n } from '../composables/useI18n'
import { useDevice } from '../composables/useDevice'
import MobileBottomNav from '../components/mobile/MobileBottomNav.vue'
import MobileFab from '../components/mobile/MobileFab.vue'
import MobileActionSheet from '../components/mobile/MobileActionSheet.vue'
import ShareDialog from '../components/ShareDialog.vue'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const fs = useFileSystemStore()
const upload = useUploadStore()
const tasks = useTasksStore()
const pendingOps = usePendingOpsStore()
const mobileUi = useMobileUiStore()
const msg = useMessage()
const { t } = useI18n()
const { prefersReducedMotion } = useDevice()

const uploadInputRef = ref<HTMLInputElement | null>(null)
const uploadAccept = ref('*/*')
const uploadCapture = ref<boolean | 'user' | 'environment' | undefined>(undefined)
const shareDialogShow = ref(false)
const shareDialogPath = ref('')

const activeTaskIds = computed(() => new Set(upload.uploads.map(item => item.taskId).filter(Boolean)))
const activityCount = computed(() => upload.activeUploads.length + tasks.activeTasks.filter(task => !activeTaskIds.value.has(task.task_id)).length + pendingOps.pendingCount)
const sheetOpen = computed(() => mobileUi.sheet.kind !== 'closed')
const routeTransitionName = computed(() => prefersReducedMotion.value ? 'mobile-fade' : 'mobile-slide')

function syncTabFromRoute(path: string): void {
  if (path.startsWith('/m/recent')) mobileUi.setActiveTab('recent')
  else if (path.startsWith('/m/shared')) mobileUi.setActiveTab('shared')
  else if (path.startsWith('/m/me')) mobileUi.setActiveTab('me')
  else mobileUi.setActiveTab('files')
}

watch(() => route.path, syncTabFromRoute, { immediate: true })
watch(() => route.fullPath, () => {
  closeSheet()
  if (fs.selectMode && !route.path.startsWith('/m/files') && !route.path.startsWith('/m/search')) {
    fs.exitSelectMode()
  }
})

onMounted(async () => {
  if (fs.tabs.length === 0) {
    fs.init()
    fs.createTab('/')
  }
  await pendingOps.init(auth.username)
  await tasks.fetchTasks()
})

function goTab(tab: string): void {
  const target = tab === 'me' ? '/m/me' : `/m/${tab}`
  router.push(target)
}

function openCreateSheet(): void {
  mobileUi.openCreateSheet()
}

function closeSheet(): void {
  mobileUi.closeSheet()
}

function triggerUpload(accept = '*/*', capture?: boolean | 'user' | 'environment'): void {
  uploadAccept.value = accept
  uploadCapture.value = capture
  closeSheet()
  uploadInputRef.value?.click()
}

function handleUploadSelection(e: Event): void {
  const input = e.target as HTMLInputElement
  const files = input.files
  if (files?.length) {
    upload.uploadFiles(files, fs.currentPath)
    msg.success(t('upload.status_uploading'))
  }
  input.value = ''
}

function openFileFromSheet(): void {
  if (mobileUi.sheet.kind !== 'file') return
  const item = mobileUi.sheet.item
  const from = route.fullPath
  closeSheet()
  if (item.is_dir) {
    router.push({ path: '/m/files', query: { path: item.path } })
    return
  }
  mobileUi.trackRecent(item)
  router.push({ path: '/m/preview', query: { path: item.path, shareId: item._shareId || '', name: item._originalName || item.name, from } })
}

function openDetailsFromSheet(): void {
  if (mobileUi.sheet.kind !== 'file') return
  const item = mobileUi.sheet.item
  const from = route.fullPath
  closeSheet()
  router.push({ path: '/m/details', query: { path: item.path, shareId: item._shareId || '', name: item._originalName || item.name, from } })
}

function shareFromSheet(): void {
  if (mobileUi.sheet.kind !== 'file') return
  const item = mobileUi.sheet.item
  closeSheet()
  if (item.is_dir || item._shareId) return
  shareDialogPath.value = item.path
  shareDialogShow.value = true
}

function copyFromSheet(): void {
  if (mobileUi.sheet.kind !== 'file') return
  fs.selectedFiles = [mobileUi.sheet.item.path]
  fs.copySelected()
  closeSheet()
  msg.success(t('menu.copy'))
}

function cutFromSheet(): void {
  if (mobileUi.sheet.kind !== 'file') return
  fs.selectedFiles = [mobileUi.sheet.item.path]
  fs.cutSelected()
  closeSheet()
  msg.success(t('menu.cut'))
}

async function deleteFromSheet(): Promise<void> {
  if (mobileUi.sheet.kind !== 'file') return
  fs.selectedFiles = [mobileUi.sheet.item.path]
  closeSheet()
  await fs.deleteSelected()
}

async function downloadFromSheet(): Promise<void> {
  if (mobileUi.sheet.kind !== 'file') return
  const item = mobileUi.sheet.item
  closeSheet()
  await fs.downloadFile(item._shareId ? item : item.path)
}

async function createFolder(): Promise<void> {
  closeSheet()
  await fs.createFolder()
}
</script>

<template>
  <div class="mobile-shell">
    <RouterView v-slot="{ Component, route: currentRoute }">
      <Transition :name="routeTransitionName" mode="out-in">
        <component :is="Component" :key="currentRoute.fullPath" />
      </Transition>
    </RouterView>

    <MobileFab @click="openCreateSheet" />
    <MobileBottomNav :active="mobileUi.activeTab" :activity-count="activityCount" @select="goTab" />

    <MobileActionSheet :show="sheetOpen" :title="mobileUi.sheet.kind === 'create' ? t('mobile.sheet.create_title') : t('mobile.sheet.file_title')" @update:show="v => { if (!v) closeSheet() }">
      <div v-if="mobileUi.sheet.kind === 'create'" class="sheet-actions">
        <button class="sheet-action" @click="triggerUpload('*/*')">{{ t('mobile.action.choose_files') }}</button>
        <button class="sheet-action" @click="triggerUpload('image/*', 'environment')">{{ t('mobile.action.take_photo') }}</button>
        <button class="sheet-action" @click="triggerUpload('audio/*')">{{ t('mobile.action.record_audio') }}</button>
        <button class="sheet-action" @click="createFolder">{{ t('mobile.action.new_folder') }}</button>
        <button class="sheet-action" @click="router.push('/m/activity'); closeSheet()">{{ t('mobile.action.open_activity') }}</button>
      </div>
      <div v-else-if="mobileUi.sheet.kind === 'file'" class="sheet-actions">
        <button class="sheet-action" @click="openFileFromSheet">{{ t('common.open') }}</button>
        <button class="sheet-action" @click="openDetailsFromSheet">{{ t('common.details') }}</button>
        <button class="sheet-action" :disabled="mobileUi.sheet.item.is_dir || !!mobileUi.sheet.item._shareId" @click="shareFromSheet">{{ t('share.title') }}</button>
        <button class="sheet-action" @click="downloadFromSheet">{{ t('common.download') }}</button>
        <button class="sheet-action" @click="copyFromSheet">{{ t('mobile.selection.copy') }}</button>
        <button class="sheet-action" @click="cutFromSheet">{{ t('mobile.selection.cut') }}</button>
        <button class="sheet-action sheet-action--danger" @click="deleteFromSheet">{{ t('mobile.selection.delete') }}</button>
      </div>
    </MobileActionSheet>

    <input ref="uploadInputRef" :accept="uploadAccept" :capture="uploadCapture || undefined" type="file" multiple style="display: none" @change="handleUploadSelection" />
    <ShareDialog :show="shareDialogShow" :file-path="shareDialogPath" @close="shareDialogShow = false" />
  </div>
</template>

<style lang="scss" scoped>
.mobile-shell {
  min-height: 100vh;
  min-height: 100dvh;
  overflow-x: hidden;
  background:
    radial-gradient(circle at top right, rgba(61, 174, 233, 0.12), transparent 34%),
    linear-gradient(180deg, #f5f8fb 0%, #eef3f7 100%);
}

.mobile-slide-enter-active,
.mobile-slide-leave-active,
.mobile-fade-enter-active,
.mobile-fade-leave-active {
  transition: transform 0.24s ease, opacity 0.24s ease;
}

.mobile-slide-enter-from,
.mobile-slide-leave-to {
  opacity: 0;
  transform: translate3d(20px, 0, 0);
}

.mobile-fade-enter-from,
.mobile-fade-leave-to {
  opacity: 0;
}

.sheet-actions {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.sheet-action {
  min-height: 52px;
  border: none;
  border-radius: 16px;
  background: rgba(16, 32, 48, 0.05);
  color: #102030;
  text-align: left;
  padding: 0 16px;
  font-size: 15px;
  font-weight: 600;
  transition: transform 0.16s ease, background 0.16s ease, box-shadow 0.16s ease;

  &:active {
    transform: scale(0.985);
    background: rgba(16, 32, 48, 0.08);
  }

  &:disabled {
    opacity: 0.45;
  }

  &--danger {
    background: rgba(218, 68, 83, 0.12);
    color: #c23242;
  }
}
</style>
