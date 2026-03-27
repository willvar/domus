<script setup>
import { onMounted, watchEffect } from 'vue'
import { useAuthStore } from './stores/auth'
import { useI18n } from './composables/useI18n'
import LoginPage from './components/LoginPage.vue'
import BSpin from './components/breeze/BSpin.vue'
import MessageContainer from './components/breeze/MessageContainer.vue'
import NotificationContainer from './components/breeze/NotificationContainer.vue'

const auth = useAuthStore()
const { t } = useI18n()

watchEffect(() => {
  document.title = t('app.title')
})

onMounted(async () => {
  document.addEventListener('contextmenu', (e) => e.preventDefault())
  await auth.checkAuth()
})
</script>

<template>
  <div v-if="auth.loading" class="loading-screen">
    <BSpin size="large" />
  </div>

  <LoginPage v-else-if="!auth.isLoggedIn" />

  <router-view v-else />

  <MessageContainer />
  <NotificationContainer />
</template>

<style scoped>
.loading-screen {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  background: var(--breeze-bg);
}
</style>
