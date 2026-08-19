<script setup lang="ts">
import { onMounted, watchEffect } from 'vue'
import { useAuthStore } from './stores/auth'
import { useI18n } from './composables/useI18n'
import LoginPage from './components/LoginPage.vue'
import GlobalDialog from './components/GlobalDialog.vue'
import { Spin, MessageList, NotificationList } from './barrels/breeze'

const auth = useAuthStore()
const { t } = useI18n()

watchEffect(() => {
  document.title = t('app.title')
})

onMounted(async () => {
  document.addEventListener('contextmenu', (e) => e.preventDefault())
  await auth.ensureAuthInitialized()
})
</script>

<template>
  <div v-if="auth.loading" class="loading-screen">
    <Spin size="large" />
  </div>

  <LoginPage v-else-if="!auth.isLoggedIn" />

  <router-view v-else />

  <GlobalDialog />
  <MessageList />
  <NotificationList />
</template>

<style lang="scss" scoped>
.loading-screen {
  @include flex-center;
  height: 100%;
  background: var(--breeze-bg);
}
</style>
