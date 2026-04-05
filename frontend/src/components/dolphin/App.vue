<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { IconMenu } from '../../barrels/icons'
import { Button, Drawer } from '../../barrels/breeze'
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
import StatusBar from './StatusBar.vue'

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
      <Button
        class="sidebar-toggle"
        size="tiny"
        quaternary
        @click="mobileSidebar = !mobileSidebar"
      >
        <template #icon><IconMenu width="16" height="16" /></template>
      </Button>

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
    <Drawer
      v-if="isMobile"
      :show="fs.showInfoPanel"
      placement="bottom"
      @close="fs.showInfoPanel = false"
    >
      <InfoPanel v-if="fs.showInfoPanel" class="info-panel-mobile" />
    </Drawer>

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
