<script setup lang="ts">
import { ref, h, provide, watch, defineAsyncComponent } from 'vue'
import TranscodeDialog from '../components/TranscodeDialog.vue'
import ShareDialog from '../components/ShareDialog.vue'
import WallpaperDialog from '../components/plasma/WallpaperDialog.vue'
import ActivityPanel from '../components/plasma/systemtray/ActivityPanel.vue'
import PreferencesPanel from '../components/plasma/systemtray/PreferencesPanel.vue'
import { useAuthStore } from '../stores/auth'
import { useFileSystemStore } from '../stores/fileSystem'
import { useUploadStore } from '../stores/upload'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useKeyboard } from '../composables/useKeyboard'
import { useWindowHistory } from '../composables/useWindowHistory'
import { useNotification } from '../composables/useNotification'
import { useWindowManagerStore } from '../stores/windowManager'
import { useWorkspaceSync } from '../composables/useWorkspaceSync'
import { Button } from '../barrels/breeze'
import { useI18n } from '../composables/useI18n'

// Critical path — needed on first render
import PlasmaDesktop from '../components/plasma/Desktop.vue'
import DolphinApp from '../components/dolphin/App.vue'
import AppRenderer from '../components/plasma/AppRenderer.vue'
import PlasmaPanel from '../components/plasma/Panel.vue'
import ContextMenu from '../components/plasma/ContextMenu.vue'
import GlobalDialog from '../components/GlobalDialog.vue'

// Lazy — heavy or rarely used
const KonsoleApp = defineAsyncComponent(() => import('../components/konsole/App.vue'))
const ProfileApp = defineAsyncComponent(() => import('../components/plasma/systemtray/ProfileApp.vue'))

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
function updateTrayPanelSize(size: { width: number; height: number }) {
  trayPanelSize.value = size
  localStorage.setItem(PANEL_SIZE_KEY, JSON.stringify(size))
}
provide('trayPanelSize', trayPanelSize)
provide('updateTrayPanelSize', updateTrayPanelSize)

const showPrefs = ref(false)
const transcodeDialogRef = ref<any>(null)
const shareDialogShow = ref(false)
const shareDialogPath = ref('')

provide('openTranscodeDialog', (path: string, name: string, mediaType: string) => {
  transcodeDialogRef.value?.open(path, name, mediaType)
})
provide('openShareDialog', (path: string) => {
  shareDialogPath.value = path
  shareDialogShow.value = true
})

const wallpaperDialogRef = ref<any>(null)
provide('pickWallpaper', () => { wallpaperDialogRef.value?.open() })

// Initialize file system with workspace restore
const workspace = useWorkspaceSync()
useWindowHistory()

pendingOpsStore.init(auth.username)

;(async () => {
  const saved = await workspace.load()

  if (saved?.version === 1 && saved.windows?.length > 0) {
    // Restore from saved workspace
    fs.init({ skipRestore: true })

    // Restore tabs
    if (saved.tabs?.length > 0) {
      fs.restoreTabs(saved.tabs, saved.activeTabIndex ?? 0)
    } else {
      fs.createTab(undefined, { remote: true })
    }

    // Restore window frames (non-viewer)
    const viewerWindows = saved.windows.filter(w => w.type === 'viewer')
    const regularWindows = saved.windows.filter(w => w.type !== 'viewer')
    workspace.restoreWindows(wm, regularWindows)

    // Restore viewer windows (async content fetch)
    if (viewerWindows.length > 0) {
      fs.restoreViewers(viewerWindows)
    }

    // Ensure files window is open if tabs exist
    if (saved.tabs?.length > 0 && !wm.findWindow('files')) {
      wm.openFilesApp({ remote: true })
    }
  } else {
    // Fresh start
    fs.init()
  }

  // Set up push event handler for cross-device sync
  workspace.setupPushHandler(wm, fs)

  // Auto-save workspace on state changes
  watch(
    () => JSON.stringify({
      w: wm.windows.map(w => [w.id, w.minimized, w.maximized, w.tiled]),
      t: fs.tabs.map(t => [t.path, t.viewMode, t.sortBy, t.sortOrder]),
      a: fs.activeTabId,
    }),
    () => { workspace.scheduleSave(wm, fs) },
    { flush: 'post' },
  )
})()

// Show setup reminder if needed
if (auth.needsSetup) {
  auth.needsSetup = false
  const n = notification.warning({
    title: t('login.setup_reminder_title'),
    content: t('login.setup_reminder'),
    duration: 15000,
    keepAliveOnHover: true,
    action: () => h(Button, {
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
      <KonsoleApp />
      <AppRenderer />
    </div>

    <PlasmaPanel v-model:show-prefs="showPrefs" />

    <ContextMenu />
    <TranscodeDialog ref="transcodeDialogRef" />
    <ShareDialog :show="shareDialogShow" :file-path="shareDialogPath" @close="shareDialogShow = false" />
    <ActivityPanel />
    <GlobalDialog />
    <ProfileApp />
    <PreferencesPanel v-model:show="showPrefs" />
    <WallpaperDialog ref="wallpaperDialogRef" />
  </div>
</template>

<style lang="scss" scoped>
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
