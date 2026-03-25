<script setup>
import { ref, onMounted } from 'vue'
import { NCard, NForm, NFormItem, NInput, NButton, NTabs, NTabPane, NSpace, NIcon, useMessage } from 'naive-ui'
import { useAuthStore } from '../stores/auth'
import { useI18n } from '../composables/useI18n'
import api from '../composables/useApi'
import IconDolphin from '~icons/mdi/dolphin'

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
      message.success(t('login.success'))
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
    message.success(t('login.success'))
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
    message.success(t('login.success'))
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
    message.success(t('login.success'))
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
    <NCard class="login-card" :bordered="true">
      <div class="login-header">
        <div class="login-icon"><NIcon :size="48"><IconDolphin /></NIcon></div>
        <h1>{{ t('app.title') }}</h1>
        <p>{{ t('app.subtitle') }}</p>
      </div>

      <!-- Step 1: Login tabs -->
      <template v-if="step === 'input'">
        <NTabs v-model:value="tab" type="segment" animated @update:value="resetTo">
          <!-- Password -->
          <NTabPane :name="'password'" :tab="t('login.password_tab')">
            <NForm class="tab-form" @submit.prevent="handlePasswordLogin">
              <NFormItem :label="t('login.username')">
                <NInput v-model:value="username" :placeholder="t('login.username_placeholder')" autofocus />
              </NFormItem>
              <NFormItem :label="t('login.password')">
                <NInput v-model:value="password" type="password" :placeholder="t('login.password_placeholder')" show-password-on="click" />
              </NFormItem>
              <NButton type="primary" block :loading="loading" attr-type="submit">
                {{ t('login.submit') }}
              </NButton>
            </NForm>
          </NTabPane>

          <!-- Email -->
          <NTabPane v-if="smtpEnabled" :name="'email'" :tab="t('login.email_tab')">
            <NForm class="tab-form" @submit.prevent="emailCodeSent ? handleEmailLogin() : requestEmailCode()">
              <NFormItem :label="t('login.username')">
                <NInput v-model:value="username" :placeholder="t('login.username_placeholder')" :disabled="emailCodeSent" />
              </NFormItem>
              <template v-if="emailCodeSent">
                <NFormItem :label="t('login.email_code')">
                  <NInput v-model:value="verifyCode" :placeholder="t('login.email_code')" maxlength="6" autofocus />
                </NFormItem>
                <NButton type="primary" block :loading="loading" attr-type="submit">
                  {{ t('login.verify') }}
                </NButton>
              </template>
              <NButton v-else type="primary" block :loading="loading" attr-type="submit">
                {{ t('login.send_code') }}
              </NButton>
            </NForm>
          </NTabPane>

          <!-- OTP -->
          <NTabPane :name="'otp'" :tab="t('login.otp_tab')">
            <NForm class="tab-form" @submit.prevent="handleOTPLogin">
              <NFormItem :label="t('login.username')">
                <NInput v-model:value="username" :placeholder="t('login.username_placeholder')" />
              </NFormItem>
              <NFormItem :label="t('login.otp_code')">
                <NInput v-model:value="otpCode" :placeholder="t('login.otp_code')" maxlength="6" />
              </NFormItem>
              <NButton type="primary" block :loading="loading" attr-type="submit">
                {{ t('login.submit') }}
              </NButton>
            </NForm>
          </NTabPane>
        </NTabs>
      </template>

      <!-- Step 2: 2FA after password -->
      <template v-else-if="step === '2fa'">
        <div class="verify-section">
          <p class="verify-hint">{{ t('login.2fa_hint') }}</p>

          <NSpace v-if="availableMethods.length > 1" justify="center" style="margin-bottom: 16px">
            <NButton
              v-for="m in availableMethods" :key="m"
              :type="selectedMethod === m ? 'primary' : 'default'"
              size="small"
              @click="selectedMethod = m; verifyCode = ''"
            >
              {{ m === 'email' ? t('login.2fa_use_email') : t('login.2fa_use_otp') }}
            </NButton>
          </NSpace>

          <NForm @submit.prevent="handle2FA">
            <NFormItem :label="selectedMethod === 'email' ? t('login.email_code') : t('login.otp_code')">
              <NInput v-model:value="verifyCode" maxlength="6" autofocus />
            </NFormItem>
            <NButton type="primary" block :loading="loading" attr-type="submit">
              {{ t('login.verify') }}
            </NButton>
          </NForm>
        </div>
      </template>
    </NCard>
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
