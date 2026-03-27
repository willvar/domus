<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import IconMenu from '~icons/mdi/menu'
import BButton from '../breeze/BButton.vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useWindowManagerStore, FILES_ICON } from '../../stores/windowManager'
import PlasmaWindow from '../plasma/Window.vue'
import ToolBar from './ToolBar.vue'
import Breadcrumb from './Breadcrumb.vue'
import TabBar from './TabBar.vue'
import Places from './Places.vue'
import View from './View.vue'
import InfoPanel from './InfoPanel.vue'
import StatusBar from './StatusBar.vue'

const fs = useFileSystemStore()
const wm = useWindowManagerStore()

const WINDOW_ID = 'files'
const mobileSidebar = ref(false)
const isMobile = ref(window.innerWidth < 768)

function onResize() { isMobile.value = window.innerWidth < 768 }
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

const showSidebarComputed = computed(() =>
  isMobile.value ? mobileSidebar.value : fs.showSidebar
)

const windowOpen = computed(() => !!wm.findWindow(WINDOW_ID))

function handleClose() {
  wm.closeWindow(WINDOW_ID)
}
</script>

<template>
  <PlasmaWindow
    v-if="windowOpen"
    :window-id="WINDOW_ID"
    title="文件"
    :icon="FILES_ICON"
    @close="handleClose"
  >
    <ToolBar />
    <Breadcrumb class="mobile-hide" />
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
    <StatusBar />
  </PlasmaWindow>
</template>

<style scoped>
.files-main-content {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  position: relative;
}

.sidebar-toggle {
  display: none;
  position: absolute;
  left: 4px;
  top: 4px;
  z-index: 50;
}

@media (max-width: 1023px) {
  .info-panel-desktop { display: none; }
}

@media (max-width: 767px) {
  .sidebar-toggle { display: block; }
  .mobile-hide { display: none; }
}

.slide-left-enter-active, .slide-left-leave-active {
  transition: transform 0.2s ease;
}
.slide-left-enter-from, .slide-left-leave-to {
  transform: translateX(-100%);
}
</style>
