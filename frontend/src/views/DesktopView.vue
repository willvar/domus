<script setup>
import { ref, h, provide } from 'vue'
import { useAuthStore } from '../stores/auth'
import { useFileSystemStore } from '../stores/fileSystem'
import { useUploadStore } from '../stores/upload'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useKeyboard } from '../composables/useKeyboard'
import { useWindowHistory } from '../composables/useWindowHistory'
import { useNotification, NButton } from 'naive-ui'
import { useI18n } from '../composables/useI18n'

import Desktop from '../components/Desktop.vue'
import FilesApp from '../components/FilesApp.vue'
import AppRenderer from '../components/AppRenderer.vue'
import TaskBar from '../components/TaskBar.vue'
import ContextMenu from '../components/ContextMenu.vue'
import UploadProgress from '../components/UploadProgress.vue'
import OperationProgress from '../components/OperationProgress.vue'
import GlobalDialog from '../components/GlobalDialog.vue'
import TranscodeDialog from '../components/TranscodeDialog.vue'
import JobsPanel from '../components/JobsPanel.vue'
import PendingOpsPanel from '../components/PendingOpsPanel.vue'
import AccountSettings from '../components/AccountSettings.vue'

const auth = useAuthStore()
const fs = useFileSystemStore()
const uploadStore = useUploadStore()
const pendingOpsStore = usePendingOpsStore()
const notification = useNotification()
const { t } = useI18n()

useKeyboard()

const showAccount = ref(false)
const transcodeDialogRef = ref(null)

provide('openTranscodeDialog', (path, name, mediaType) => {
  transcodeDialogRef.value?.open(path, name, mediaType)
})

// Initialize file system and check for interrupted uploads
fs.init()
uploadStore.checkInterrupted()
pendingOpsStore.init(auth.username)
useWindowHistory()

// Show setup reminder if needed
if (auth.needsSetup) {
  auth.needsSetup = false
  const n = notification.warning({
    title: t('login.setup_reminder_title'),
    content: t('login.setup_reminder'),
    duration: 15000,
    keepAliveOnHover: true,
    action: () => h(NButton, {
      type: 'primary',
      size: 'small',
      onClick: () => {
        showAccount.value = true
        n.destroy()
      },
    }, () => t('login.setup_now')),
  })
}
</script>

<template>
  <div class="app-layout">
    <div class="desktop-area">
      <Desktop />
      <FilesApp />
      <AppRenderer />
    </div>

    <TaskBar @show-account="showAccount = true" />

    <ContextMenu />
    <UploadProgress />
    <OperationProgress />
    <TranscodeDialog ref="transcodeDialogRef" />
    <JobsPanel />
    <PendingOpsPanel />
    <GlobalDialog />
    <AccountSettings v-model:show="showAccount" />
  </div>
</template>

<style scoped>
.app-layout {
  display: flex;
  flex-direction: column;
  height: 100vh;
  height: 100dvh;
  overflow: hidden;
  background: var(--breeze-bg);
}

.desktop-area {
  flex: 1;
  min-height: 0;
  position: relative;
  overflow: hidden;
}
</style>
