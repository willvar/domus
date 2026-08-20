<script setup lang="ts">
import { onMounted, onUnmounted, watchEffect } from 'vue'
import { NSpin } from 'naive-ui'
import { useAuthStore } from './stores/auth'
import { useI18n } from './composables/useI18n'
import LoginPage from './components/LoginPage.vue'
import NaiveProvider from './components/NaiveProvider.vue'

const auth = useAuthStore()
const { t } = useI18n()

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

    <router-view v-else />
  </NaiveProvider>
</template>

<style lang="scss" scoped>
.loading-screen {
  @include flex-center;
  height: 100%;
  background: #f6f7fb;
}
</style>
