<script setup lang="ts">
import { useRouter } from 'vue-router'
import { IconChevronLeft } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import { useI18n } from '../../composables/useI18n'
import TerminalTabs from '../../components/konsole/TerminalTabs.vue'

const router = useRouter()
const { t } = useI18n()
</script>

<template>
  <section class="mobile-page terminal-page">
    <MobileTopBar :title="t('app.terminal')" :subtitle="t('mobile.terminal.subtitle')" show-back @back="router.back()">
      <template #back><IconChevronLeft width="18" height="18" /></template>
    </MobileTopBar>
    <div class="terminal-shell">
      <TerminalTabs class="terminal-tabs" @exit="router.back()" />
    </div>
  </section>
</template>

<style lang="scss" scoped>
.mobile-page {
  min-height: 100dvh;
}

.terminal-page {
  display: flex;
  flex-direction: column;
}

.terminal-shell {
  flex: 1;
  min-height: 0;
  padding: 16px 16px 120px;
}

.terminal-tabs {
  min-height: 70vh;
  border-radius: 20px;
  overflow: hidden;
  box-shadow: 0 16px 36px rgba(11, 24, 36, 0.16);
}
</style>
