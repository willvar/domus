<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NSpin } from 'naive-ui'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { useAuthStore } from '../stores/auth'

interface Consent {
  challenge: string
  client_name: string
  client_id: string
  redirect_uri: string
  expires_at: string
}

const { t, te } = useI18n()
const auth = useAuthStore()
const consent = ref<Consent | null>(null)
const error = ref('')
const submitting = ref(false)
const decisionURL = api.getUri({ url: '/oauth/decision' })

onMounted(async () => {
  if (window.top !== window.self) {
    error.value = t('oauth.no_frames')
    return
  }
  try {
    const response = await api.post<Consent>('/oauth/consent', { query: window.location.search.slice(1) }, { 'axios-retry': { retries: 0 } })
    consent.value = response.data
  } catch (e: any) {
    error.value = te(e, 'oauth.invalid_request')
  }
})
</script>

<template>
  <main class="authorization-page">
    <NCard class="authorization-card" :title="t('oauth.authorize_title')">
      <NAlert v-if="error" type="error" :show-icon="false">{{ error }}</NAlert>
      <NSpin v-else-if="!consent" size="small" />
      <template v-else>
        <p>{{ t('oauth.request', { app: consent.client_name, account: auth.username }) }}</p>
        <NAlert type="warning" :show-icon="false">{{ t('oauth.read_scope') }}</NAlert>
        <p class="muted">{{ t('oauth.exclusions') }}</p>
        <dl>
          <dt>{{ t('oauth.callback') }}</dt><dd>{{ consent.redirect_uri }}</dd>
          <dt>{{ t('oauth.client_id') }}</dt><dd>{{ consent.client_id }}</dd>
        </dl>
        <p class="muted">{{ t('oauth.revocable') }}</p>
        <div class="decisions">
          <form :action="decisionURL" method="post" @submit="submitting = true">
            <input type="hidden" name="challenge" :value="consent.challenge">
            <input type="hidden" name="decision" value="deny">
            <NButton attr-type="submit" :disabled="submitting" block>{{ t('oauth.deny') }}</NButton>
          </form>
          <form :action="decisionURL" method="post" @submit="submitting = true">
            <input type="hidden" name="challenge" :value="consent.challenge">
            <input type="hidden" name="decision" value="allow">
            <NButton attr-type="submit" type="primary" :disabled="submitting" block>{{ t('oauth.allow') }}</NButton>
          </form>
        </div>
      </template>
      <NButton class="back" text tag="a" href="/files">{{ t('oauth.back') }}</NButton>
    </NCard>
  </main>
</template>

<style scoped>
.authorization-page { height: 100dvh; overflow-y: auto; display: grid; place-items: center; padding: 24px; background: var(--domus-surface-2); }
.authorization-card { width: min(100%, 560px); }
.muted, dt { color: var(--domus-muted); }
dt { margin-top: 16px; font-size: 12px; }
dd { margin: 4px 0 0; overflow-wrap: anywhere; }
.decisions { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 24px; }
.back { margin-top: 24px; }
</style>
