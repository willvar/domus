<script setup lang="ts">
import { computed, onMounted, onUnmounted, watchEffect } from 'vue'
import { useRoute } from 'vue-router'
import { NSpin } from 'naive-ui'
import { useAuthStore } from './stores/auth'
import { useI18n } from './composables/useI18n'
import { previewReloadKey } from './composables/usePreviewReload'
import LoginPage from './components/LoginPage.vue'
import NaiveProvider from './components/NaiveProvider.vue'

const auth = useAuthStore()
const route = useRoute()
const { t } = useI18n()
const routeViewKey = computed(() => `${route.fullPath}:${previewReloadKey.value}`)

watchEffect(() => {
  document.title = t('app.title')
})

function preventContextMenu(event: MouseEvent): void {
  event.preventDefault()
}

onMounted(async () => {
  document.addEventListener('contextmenu', preventContextMenu)
  await auth.ensureAuthInitialized()
})

onUnmounted(() => {
  document.removeEventListener('contextmenu', preventContextMenu)
})
</script>

<template>
  <NaiveProvider>
    <div v-if="auth.loading" class="loading-screen">
      <NSpin size="large" />
    </div>

    <LoginPage v-else-if="!auth.isLoggedIn" />

    <router-view v-else :key="routeViewKey" />
  </NaiveProvider>
</template>

<style lang="scss" scoped>
.loading-screen {
  @include flex-center;
  height: 100%;
  background: #f6f7fb;
}
</style>
