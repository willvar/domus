<script setup>
import { ref, h, provide, onMounted } from 'vue'
import { useAuthStore } from '../stores/auth'
import { useFileSystemStore } from '../stores/fileSystem'
import { useUploadStore } from '../stores/upload'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useKeyboard } from '../composables/useKeyboard'
import { useWindowHistory } from '../composables/useWindowHistory'
import { useNotification } from '../composables/useNotification'
import { useWindowManagerStore } from '../stores/windowManager'
import BButton from '../components/breeze/BButton.vue'
import { useI18n } from '../composables/useI18n'

import PlasmaDesktop from '../components/plasma/Desktop.vue'
import DolphinApp from '../components/dolphin/App.vue'
import AppRenderer from '../components/plasma/AppRenderer.vue'
import PlasmaPanel from '../components/plasma/Panel.vue'
import ContextMenu from '../components/plasma/ContextMenu.vue'
import GlobalDialog from '../components/GlobalDialog.vue'
import TranscodeDialog from '../components/TranscodeDialog.vue'
import JobsPanel from '../components/plasma/systemtray/JobsPanel.vue'
import PendingOpsPanel from '../components/plasma/systemtray/PendingOpsPanel.vue'
import ProfileApp from '../components/plasma/systemtray/ProfileApp.vue'
import PreferencesPanel from '../components/plasma/systemtray/PreferencesPanel.vue'

const auth = useAuthStore()
const fs = useFileSystemStore()
const uploadStore = useUploadStore()
const pendingOpsStore = usePendingOpsStore()
const wm = useWindowManagerStore()
const notification = useNotification()
const { t } = useI18n()

useKeyboard()

// --- Tray panel size (shared across all tray panels) ---
const PANEL_SIZE_KEY = 'zephyr_tray_panel_size'
function loadPanelSize() {
  try {
    const raw = localStorage.getItem(PANEL_SIZE_KEY)
    if (raw) return JSON.parse(raw)
  } catch { /* ignore */ }
  return { width: 360, height: 420 }
}
const trayPanelSize = ref(loadPanelSize())
function updateTrayPanelSize(size) {
  trayPanelSize.value = size
  localStorage.setItem(PANEL_SIZE_KEY, JSON.stringify(size))
}
provide('trayPanelSize', trayPanelSize)
provide('updateTrayPanelSize', updateTrayPanelSize)

const showPrefs = ref(false)
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
    action: () => h(BButton, {
      type: 'primary',
      size: 'small',
      onClick: () => {
        wm.openProfileApp('security')
        n.destroy()
      },
    }, () => t('login.setup_now')),
  })
}
</script>

<template>
  <div class="app-layout">
    <div class="desktop-area">
      <PlasmaDesktop />
      <DolphinApp />
      <AppRenderer />
    </div>

    <PlasmaPanel v-model:show-prefs="showPrefs" />

    <ContextMenu />
    <TranscodeDialog ref="transcodeDialogRef" />
    <JobsPanel />
    <PendingOpsPanel />
    <GlobalDialog />
    <ProfileApp />
    <PreferencesPanel v-model:show="showPrefs" />
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
