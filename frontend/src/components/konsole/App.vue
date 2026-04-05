<script setup lang="ts">
import { computed } from 'vue'
import { useWindowManagerStore, KONSOLE_ICON } from '../../stores/windowManager'
import { useI18n } from '../../composables/useI18n'
import PlasmaWindow from '../plasma/Window.vue'
import TerminalTabs from './TerminalTabs.vue'

const wm = useWindowManagerStore()
const { t } = useI18n()

const WINDOW_ID = 'konsole'
const windowOpen = computed(() => !!wm.findWindow(WINDOW_ID))

function handleClose() {
  wm.closeWindow(WINDOW_ID)
}
</script>

<template>
  <PlasmaWindow
    v-if="windowOpen"
    :window-id="WINDOW_ID"
    :title="t('app.terminal')"
    :icon="KONSOLE_ICON"
    @close="handleClose"
  >
    <TerminalTabs class="konsole-body" @exit="handleClose" />
  </PlasmaWindow>
</template>

<style lang="scss" scoped>
.konsole-body {
  flex: 1;
  min-height: 0;
}
</style>
