<script setup>
import { ref, onMounted } from 'vue'
import { useAuthStore } from '../stores/auth'
import { useI18n } from '../composables/useI18n'
import { useMessage } from '../composables/useMessage'
import api from '../composables/useApi'
import IconSailBoat from '~icons/mdi/sail-boat'
import BCard from './breeze/BCard.vue'
import BForm from './breeze/BForm.vue'
import BFormItem from './breeze/BFormItem.vue'
import BInput from './breeze/BInput.vue'
import BButton from './breeze/BButton.vue'
import BTabs from './breeze/BTabs.vue'
import BTabPane from './breeze/BTabPane.vue'

const auth = useAuthStore()
const message = useMessage()
const { t, te } = useI18n()

// State
const tab = ref('password')       // 'password' | 'email' | 'otp'
const step = ref('input')         // 'input' | '2fa'
const token = ref('')
const availableMethods = ref([])
const selectedMethod = ref('')
const smtpEnabled = ref(false)

// Form fields
const username = ref('')
const password = ref('')
const otpCode = ref('')
const emailCodeSent = ref(false)
const verifyCode = ref('')
const loading = ref(false)

// Avatar lookup
const loginAvatarUrl = ref('')
let lastAvatarUser = ''

async function fetchAvatar() {
  const name = username.value.trim()
  if (!name || name === lastAvatarUser) return
  lastAvatarUser = name
  try {
    const res = await api.get(`/user/avatar/${encodeURIComponent(name)}`)
    loginAvatarUrl.value = res.data.avatar_url || ''
  } catch {
    loginAvatarUrl.value = ''
  }
}

onMounted(async () => {
  try {
    const res = await api.get('/auth')
    smtpEnabled.value = res.data.smtp_enabled
  } catch { /* SMTP status defaults to disabled */ }
})

// Password login: verify → maybe 2FA → login
async function handlePasswordLogin() {
  if (!username.value || !password.value) {
    message.warning(t('login.required'))
    return
  }
  loading.value = true
  try {
    const data = await auth.verify({ username: username.value, password: password.value })
    token.value = data.token
    if (data.methods.length > 0) {
      // 2FA required
      availableMethods.value = data.methods
      selectedMethod.value = data.methods[0]
      verifyCode.value = ''
      step.value = '2fa'
    } else {
      // Direct login
      await auth.login({ token: data.token })
      message.success(t('login.success', { name: auth.user?.display_name || auth.username }))
    }
  } catch (e) {
    message.error(te(e, 'login.failed'))
  } finally {
    loading.value = false
  }
}

// Email login step 1: verify with method:"email" → get token
async function requestEmailCode() {
  if (!username.value) {
    message.warning(t('login.required'))
    return
  }
  loading.value = true
  try {
    const data = await auth.verify({ username: username.value, method: 'email' })
    token.value = data.token
    emailCodeSent.value = true
    verifyCode.value = ''
    message.success(t('login.code_sent'))
  } catch (e) {
    message.error(te(e, 'login.failed'))
  } finally {
    loading.value = false
  }
}

// Email login step 2: login with token + code
async function handleEmailLogin() {
  if (!verifyCode.value) return
  loading.value = true
  try {
    await auth.login({ token: token.value, code: verifyCode.value, method: 'email' })
    message.success(t('login.success', { name: auth.user?.display_name || auth.username }))
  } catch (e) {
    message.error(te(e, 'login.verify_failed'))
  } finally {
    loading.value = false
  }
}

// OTP login: verify → direct login
async function handleOTPLogin() {
  if (!username.value || !otpCode.value) {
    message.warning(t('login.required'))
    return
  }
  loading.value = true
  try {
    const data = await auth.verify({ username: username.value, otp: otpCode.value })
    await auth.login({ token: data.token })
    message.success(t('login.success', { name: auth.user?.display_name || auth.username }))
  } catch (e) {
    message.error(te(e, 'login.failed'))
  } finally {
    loading.value = false
  }
}

// 2FA step: login with token + code + method
async function handle2FA() {
  if (!verifyCode.value) return
  loading.value = true
  try {
    await auth.login({ token: token.value, code: verifyCode.value, method: selectedMethod.value })
    message.success(t('login.success', { name: auth.user?.display_name || auth.username }))
  } catch (e) {
    message.error(te(e, 'login.verify_failed'))
  } finally {
    loading.value = false
  }
}

function resetTo(newTab) {
  step.value = 'input'
  token.value = ''
  emailCodeSent.value = false
  otpCode.value = ''
  verifyCode.value = ''
  tab.value = newTab
}
</script>

<template>
  <div class="login-page">
    <BCard class="login-card" :bordered="true">
      <div class="login-header">
        <div class="login-icon">
          <img v-if="loginAvatarUrl" :src="loginAvatarUrl" class="login-avatar" />
          <IconSailBoat v-else width="48" height="48" />
        </div>
        <h1>{{ t('app.title') }}</h1>
      </div>

      <!-- Step 1: Login tabs -->
      <template v-if="step === 'input'">
        <BTabs :value="tab" @update:value="resetTo">
          <!-- Password -->
          <BTabPane name="password" :tab="t('login.password_tab')">
            <BForm class="tab-form" @submit.prevent="handlePasswordLogin">
              <BFormItem :label="t('login.username')">
                <BInput :value="username" :placeholder="t('login.username_placeholder')" autofocus @update:value="v => username = v" @blur="fetchAvatar" />
              </BFormItem>
              <BFormItem :label="t('login.password')">
                <BInput :value="password" type="password" :placeholder="t('login.password_placeholder')" show-password-on="click" @update:value="v => password = v" />
              </BFormItem>
              <BButton type="primary" block :loading="loading" attr-type="submit">
                {{ t('login.submit') }}
              </BButton>
            </BForm>
          </BTabPane>

          <!-- Email -->
          <BTabPane v-if="smtpEnabled" name="email" :tab="t('login.email_tab')">
            <BForm class="tab-form" @submit.prevent="emailCodeSent ? handleEmailLogin() : requestEmailCode()">
              <BFormItem :label="t('login.username')">
                <BInput :value="username" :placeholder="t('login.username_placeholder')" :disabled="emailCodeSent" @update:value="v => username = v" @blur="fetchAvatar" />
              </BFormItem>
              <template v-if="emailCodeSent">
                <BFormItem :label="t('login.email_code')">
                  <BInput :value="verifyCode" :placeholder="t('login.email_code')" maxlength="6" autofocus @update:value="v => verifyCode = v" />
                </BFormItem>
                <BButton type="primary" block :loading="loading" attr-type="submit">
                  {{ t('login.verify') }}
                </BButton>
              </template>
              <BButton v-else type="primary" block :loading="loading" attr-type="submit">
                {{ t('login.send_code') }}
              </BButton>
            </BForm>
          </BTabPane>

          <!-- OTP -->
          <BTabPane name="otp" :tab="t('login.otp_tab')">
            <BForm class="tab-form" @submit.prevent="handleOTPLogin">
              <BFormItem :label="t('login.username')">
                <BInput :value="username" :placeholder="t('login.username_placeholder')" @update:value="v => username = v" @blur="fetchAvatar" />
              </BFormItem>
              <BFormItem :label="t('login.otp_code')">
                <BInput :value="otpCode" :placeholder="t('login.otp_code')" maxlength="6" @update:value="v => otpCode = v" />
              </BFormItem>
              <BButton type="primary" block :loading="loading" attr-type="submit">
                {{ t('login.submit') }}
              </BButton>
            </BForm>
          </BTabPane>
        </BTabs>
      </template>

      <!-- Step 2: 2FA after password -->
      <template v-else-if="step === '2fa'">
        <div class="verify-section">
          <p class="verify-hint">{{ t('login.2fa_hint') }}</p>

          <div v-if="availableMethods.length > 1" style="display:flex;justify-content:center;gap:8px;margin-bottom:16px">
            <BButton
              v-for="m in availableMethods" :key="m"
              :type="selectedMethod === m ? 'primary' : 'default'"
              size="small"
              @click="selectedMethod = m; verifyCode = ''"
            >
              {{ m === 'email' ? t('login.2fa_use_email') : t('login.2fa_use_otp') }}
            </BButton>
          </div>

          <BForm @submit.prevent="handle2FA">
            <BFormItem :label="selectedMethod === 'email' ? t('login.email_code') : t('login.otp_code')">
              <BInput :value="verifyCode" maxlength="6" autofocus @update:value="v => verifyCode = v" />
            </BFormItem>
            <BButton type="primary" block :loading="loading" attr-type="submit">
              {{ t('login.verify') }}
            </BButton>
          </BForm>
        </div>
      </template>
    </BCard>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
  background: var(--breeze-bg);
}
.login-card {
  width: 400px;
  border-radius: 8px;
}

@media (max-width: 767px) {
  .login-page { padding: 0; }
  .login-card {
    width: 100%;
    border-radius: 0;
    border: none;
    background: transparent;
    box-shadow: none;
  }
}
.login-header {
  text-align: center;
  margin-bottom: 24px;
}
.login-icon {
  font-size: 48px;
  margin-bottom: 8px;
  display: flex;
  justify-content: center;
}
.login-avatar {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  object-fit: cover;
  border: 2px solid var(--breeze-border);
}
.login-header h1 {
  font-size: 24px;
  font-weight: 600;
  margin: 0;
  color: var(--breeze-text);
}
.login-header p {
  color: var(--breeze-text-secondary);
  margin: 4px 0 0;
  font-size: var(--font-size-md);
}
.tab-form { margin-top: 16px; }
.verify-section { padding-top: 8px; }
.verify-hint {
  text-align: center;
  color: var(--breeze-text-secondary);
  margin-bottom: 16px;
}
</style>
