<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NForm, NFormItem, NInput, NSpin, NSwitch } from 'naive-ui'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { showConfirm } from '../composables/useNativeDialog'
import { useAppMessage } from '../ui/feedback'
import { useAuthStore } from '../stores/auth'

interface Grant { id: string; client_name: string; created_at: string }
interface Client { client_id: string; name: string; redirect_uris: string[]; public: boolean }
interface CreatedClient extends Client { client_secret?: string }

const { t, te } = useI18n()
const auth = useAuthStore()
const message = useAppMessage()
const grants = ref<Grant[]>([])
const clients = ref<Client[]>([])
const created = ref<CreatedClient | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const name = ref('')
const callbacks = ref('')
const publicClient = ref(false)
const noRetry = { 'axios-retry': { retries: 0 } }

async function load(): Promise<void> {
  error.value = ''
  try {
    const [authorized, registered] = await Promise.all([
      api.get<Grant[]>('/oauth/grants'),
      auth.isRoot ? api.get<Client[]>('/oauth/clients') : Promise.resolve(null),
    ])
    grants.value = authorized.data
    clients.value = registered?.data || []
  } catch (e: any) {
    error.value = te(e)
  } finally {
    loading.value = false
  }
}

async function revoke(id: string, client: boolean): Promise<void> {
  if (!await showConfirm(t(client ? 'oauth.disable' : 'oauth.revoke'), t(client ? 'oauth.disable_confirm' : 'oauth.revoke_confirm'), { icon: 'warning' })) return
  busy.value = true
  try {
    await api.delete(`/oauth/${client ? 'clients' : 'grants'}/${encodeURIComponent(id)}`, noRetry)
    await load()
  } catch (e: any) {
    message.error(te(e))
  } finally {
    busy.value = false
  }
}

async function register(): Promise<void> {
  busy.value = true
  created.value = null
  try {
    const response = await api.post<CreatedClient>('/oauth/clients', {
      name: name.value,
      redirect_uris: callbacks.value.split('\n').map(value => value.trim()).filter(Boolean),
      public: publicClient.value,
    }, noRetry)
    created.value = response.data
    const { client_secret: _secret, ...registered } = response.data
    clients.value.unshift(registered)
    name.value = callbacks.value = ''
  } catch (e: any) {
    message.error(te(e))
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="applications-content">
    <NAlert v-if="error" type="error" :show-icon="false">{{ error }}</NAlert>
    <NSpin v-if="loading" size="small" />
    <template v-else-if="!error">
      <NCard class="applications-personal" :title="t('oauth.authorized')" role="region" :aria-label="t('oauth.authorized')">
        <p class="scope-note">{{ t('oauth.personal_hint', { account: auth.username }) }}</p>
        <p class="muted">{{ t('oauth.read_scope') }} {{ t('oauth.exclusions') }}</p>
        <NEmpty v-if="!grants.length" :description="t('oauth.none')" />
        <article v-for="grant in grants" :key="grant.id" class="application-row">
          <div><strong>{{ grant.client_name }}</strong><small>{{ new Date(grant.created_at).toLocaleString() }}</small></div>
          <NButton type="error" secondary :disabled="busy" @click="revoke(grant.id, false)">{{ t('oauth.revoke') }}</NButton>
        </article>
      </NCard>
      <NCard v-if="auth.isRoot" class="applications-admin" :title="t('oauth.management')" role="region" :aria-label="t('oauth.management')">
        <p class="scope-note">{{ t('oauth.management_hint') }}</p>
        <NAlert type="info" :show-icon="false">{{ t('oauth.registration_hint') }}</NAlert>
        <h3>{{ t('oauth.registered') }}</h3>
        <NEmpty v-if="!clients.length" :description="t('oauth.no_registered')" />
        <article v-for="client in clients" :key="client.client_id" class="application-row">
          <div><strong>{{ client.name }}</strong><small>{{ client.client_id }}</small><small v-for="uri in client.redirect_uris" :key="uri">{{ uri }}</small></div>
          <NButton type="error" secondary :disabled="busy" @click="revoke(client.client_id, true)">{{ t('oauth.disable') }}</NButton>
        </article>
        <h3>{{ t('oauth.register') }}</h3>
        <NForm @submit.prevent="register">
          <NFormItem :label="t('oauth.name')" :label-props="{ for: 'oauth-client-name' }">
            <NInput v-model:value="name" :input-props="{ id: 'oauth-client-name', autocomplete: 'off' }" :maxlength="100" />
          </NFormItem>
          <NFormItem :label="t('oauth.callbacks')" :label-props="{ for: 'oauth-client-callbacks' }">
            <NInput v-model:value="callbacks" type="textarea" :input-props="{ id: 'oauth-client-callbacks' }" :rows="3" placeholder="https://app.example/oauth/callback" />
          </NFormItem>
          <NFormItem :label="t('oauth.public_client')"><NSwitch v-model:value="publicClient" :aria-label="t('oauth.public_client')" /></NFormItem>
          <NButton attr-type="submit" type="primary" :loading="busy" :disabled="!name.trim() || !callbacks.trim()">{{ t('oauth.register') }}</NButton>
        </NForm>
        <NAlert v-if="created" type="warning" :show-icon="false" class="created-client">
          <p>{{ t('oauth.client_id') }}: <code>{{ created.client_id }}</code></p>
          <template v-if="created.client_secret"><p>{{ t('oauth.secret_once') }}</p><code>{{ created.client_secret }}</code></template>
          <NButton size="small" @click="created = null">{{ t('oauth.saved') }}</NButton>
        </NAlert>
      </NCard>
    </template>
  </section>
</template>

<style scoped>
.applications-content { min-width: 0; }
.n-card + .n-card { margin-top: 20px; }
.scope-note { margin: 0 0 12px; line-height: 1.6; }
.applications-admin { border-top: 3px solid var(--domus-primary); }
h3 { margin: 24px 0 14px; font-size: 14px; font-weight: 650; }
.muted, small { color: var(--domus-muted); }
.application-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 16px 0; border-top: 1px solid var(--domus-line); }
.application-row > div { min-width: 0; overflow-wrap: anywhere; }
small { display: block; margin-top: 4px; overflow-wrap: anywhere; }
.created-client { margin-top: 20px; }
code { overflow-wrap: anywhere; user-select: text; }
.created-client .n-button { display: block; margin-top: 12px; }
</style>
