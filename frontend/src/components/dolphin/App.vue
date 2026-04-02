<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import IconMenu from '~icons/mdi/menu'
import BButton from '../breeze/BButton.vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useWindowManagerStore, FILES_ICON } from '../../stores/windowManager'
import { useI18n } from '../../composables/useI18n'
import PlasmaWindow from '../plasma/Window.vue'
import ToolBar from './ToolBar.vue'
import Breadcrumb from './Breadcrumb.vue'
import TabBar from './TabBar.vue'
import Places from './Places.vue'
import View from './View.vue'
import InfoPanel from './InfoPanel.vue'
import BDrawer from '../breeze/BDrawer.vue'
import StatusBar from './StatusBar.vue'
import KonsoleTerminalTabs from '../konsole/TerminalTabs.vue'

const fs = useFileSystemStore()
const wm = useWindowManagerStore()
const { t } = useI18n()

const WINDOW_ID = 'files'
const mobileSidebar = ref(false)
const isMobile = ref(window.innerWidth < 768)

function onResize() { isMobile.value = window.innerWidth < 768 }
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

const showSidebarComputed = computed(() =>
  isMobile.value ? mobileSidebar.value : fs.showSidebar
)

const windowOpen = computed(() => {
  const open = !!wm.findWindow(WINDOW_ID)
  // Ensure at least one tab exists when the files window is open
  if (open && fs.tabs.length === 0) {
    fs.createTab()
  }
  return open
})

function handleClose() {
  wm.closeWindow(WINDOW_ID)
}

// --- Terminal panel resize ---
let resizing = false
let resizeStartY = 0
let resizeStartH = 0

function startTermResize(e) {
  e.preventDefault()
  resizing = true
  resizeStartY = e.clientY
  resizeStartH = fs.terminalHeight
  document.addEventListener('mousemove', onTermResize)
  document.addEventListener('mouseup', stopTermResize)
  document.body.style.cursor = 'ns-resize'
  document.body.style.userSelect = 'none'
}

function onTermResize(e) {
  if (!resizing) return
  const delta = resizeStartY - e.clientY
  const newH = Math.max(120, Math.min(resizeStartH + delta, window.innerHeight - 200))
  fs.terminalHeight = newH
}

function stopTermResize() {
  resizing = false
  document.removeEventListener('mousemove', onTermResize)
  document.removeEventListener('mouseup', stopTermResize)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
}
</script>

<template>
  <PlasmaWindow
    v-if="windowOpen"
    :window-id="WINDOW_ID"
    :title="t('app.files')"
    :icon="FILES_ICON"
    @close="handleClose"
  >
    <ToolBar />
    <Breadcrumb />
    <TabBar />
    <div class="files-main-content">
      <BButton
        class="sidebar-toggle"
        size="tiny"
        quaternary
        @click="mobileSidebar = !mobileSidebar"
      >
        <template #icon><IconMenu width="16" height="16" /></template>
      </BButton>

      <Transition name="slide-left">
        <Places
          v-if="showSidebarComputed"
          @click="mobileSidebar = false"
        />
      </Transition>

      <View />

      <InfoPanel v-if="fs.showInfoPanel" class="info-panel-desktop" />
    </div>

    <!-- Mobile: InfoPanel as bottom drawer -->
    <BDrawer
      v-if="isMobile"
      :show="fs.showInfoPanel"
      placement="bottom"
      @close="fs.showInfoPanel = false"
    >
      <InfoPanel v-if="fs.showInfoPanel" class="info-panel-mobile" />
    </BDrawer>

    <div
      v-if="fs.showTerminal"
      class="terminal-panel"
      :style="{ height: fs.terminalHeight + 'px' }"
    >
      <div class="terminal-resize-handle" @mousedown="startTermResize" />
      <KonsoleTerminalTabs :initial-cwd="fs.currentPath" @exit="fs.toggleTerminal()" />
    </div>

    <StatusBar />
  </PlasmaWindow>
</template>

<style lang="scss" scoped>
.files-main-content {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  position: relative;
}

.terminal-panel {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  border-top: 1px solid var(--breeze-border);
  min-height: 120px;
  position: relative;
}

.terminal-resize-handle {
  height: 4px;
  cursor: ns-resize;
  flex-shrink: 0;
  background: var(--breeze-border);
  transition: background 0.15s;

  &:hover {
    background: var(--breeze-accent);
  }
}

.sidebar-toggle {
  display: none;
  position: absolute;
  left: 4px;
  top: 4px;
  z-index: $z-overlay;

  @include mobile {
    display: block;
  }
}

.info-panel-desktop {
  @media (max-width: 1023px) {
    display: none;
  }
}

.info-panel-mobile {
  max-height: 60vh;
  overflow-y: auto;
}

.slide-left-enter-active,
.slide-left-leave-active {
  transition: transform 0.2s ease;
}

.slide-left-enter-from,
.slide-left-leave-to {
  transform: translateX(-100%);
}
</style>
