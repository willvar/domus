<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import {
  NButton,
  NCard,
  NForm,
  NFormItem,
  NInput,
  NTabPane,
  NTabs,
} from 'naive-ui'
import { useAuthStore } from '../stores/auth'
import { useI18n } from '../composables/useI18n'
import { useAppMessage } from '../ui/feedback'
import api from '../composables/useApi'
import { loadPublicAvatar, revokeAvatarURL } from '../composables/useAvatar'
import { IconCheck } from '../barrels/icons'

const auth = useAuthStore()
const message = useAppMessage()
const { t, te } = useI18n()

// State
const tab = ref('password')       // 'password' | 'email' | 'otp'
const step = ref('input')         // 'input' | '2fa'
const token = ref('')
const availableMethods = ref<string[]>([])
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

function replaceLoginAvatar(next: string): void {
  const previous = loginAvatarUrl.value
  loginAvatarUrl.value = next
  if (previous && previous !== next) revokeAvatarURL(previous)
}

async function fetchAvatar() {
  const name = username.value.trim()
  if (!name) {
    lastAvatarUser = ''
    replaceLoginAvatar('')
    return
  }
  if (name === lastAvatarUser) return
  lastAvatarUser = name
  try {
    const resolved = await loadPublicAvatar(name)
    if (username.value.trim() !== name) {
      revokeAvatarURL(resolved)
      return
    }
    replaceLoginAvatar(resolved)
  } catch {
    if (lastAvatarUser === name) replaceLoginAvatar('')
  }
}

onBeforeUnmount(() => replaceLoginAvatar(''))

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
  } catch (e: any) {
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
  } catch (e: any) {
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
  } catch (e: any) {
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
  } catch (e: any) {
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
  } catch (e: any) {
    message.error(te(e, 'login.verify_failed'))
  } finally {
    loading.value = false
  }
}

function resetTo(newTab: string) {
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
    <section class="login-story">
      <div class="login-story__inner">
        <div class="login-brand">
          <span class="login-brand__mark">D</span>
          <span>DOMUS</span>
        </div>

        <div class="login-story__copy">
          <span class="login-kicker">{{ t('login.kicker') }}</span>
          <h1>{{ t('login.headline') }}</h1>
          <p>{{ t('login.subtitle') }}</p>
          <ul>
            <li><span><IconCheck /></span>{{ t('login.feature_organize') }}</li>
            <li><span><IconCheck /></span>{{ t('login.feature_preview') }}</li>
            <li><span><IconCheck /></span>{{ t('login.feature_isolated') }}</li>
          </ul>
        </div>

        <div class="storage-visual" aria-hidden="true">
          <div class="storage-visual__glow" />
          <div class="storage-visual__card storage-visual__card--back">
            <span />
            <span />
            <span />
          </div>
          <div class="storage-visual__card storage-visual__card--front">
            <div class="storage-visual__lock">D</div>
            <div>
              <strong>{{ t('login.visual_title') }}</strong>
              <small>{{ t('login.visual_caption') }}</small>
            </div>
            <IconCheck />
          </div>
        </div>
      </div>
    </section>

    <section class="login-panel">
      <NCard class="login-card" :bordered="false">
      <div class="login-header">
        <div class="login-icon">
          <img v-if="loginAvatarUrl" :src="loginAvatarUrl" class="login-avatar" />
          <span v-else>D</span>
        </div>
        <h2>{{ t('login.form_title') }}</h2>
        <p>{{ t('login.form_hint') }}</p>
      </div>

      <!-- Step 1: Login tabs -->
      <template v-if="step === 'input'">
        <NTabs :value="tab" type="line" animated @update:value="value => resetTo(String(value))">
          <!-- Password -->
          <NTabPane name="password" :tab="t('login.password_tab')">
            <NForm class="tab-form" @submit.prevent="handlePasswordLogin">
              <NFormItem :label="t('login.username')">
                <NInput :value="username" size="large" :placeholder="t('login.username_placeholder')" autofocus @update:value="v => username = v" @blur="fetchAvatar" />
              </NFormItem>
              <NFormItem :label="t('login.password')">
                <NInput :value="password" size="large" type="password" :placeholder="t('login.password_placeholder')" show-password-on="click" @update:value="v => password = v" />
              </NFormItem>
              <NButton type="primary" size="large" block :loading="loading" attr-type="submit">
                {{ t('login.submit') }}
              </NButton>
            </NForm>
          </NTabPane>

          <!-- Email -->
          <NTabPane v-if="smtpEnabled" name="email" :tab="t('login.email_tab')">
            <NForm class="tab-form" @submit.prevent="emailCodeSent ? handleEmailLogin() : requestEmailCode()">
              <NFormItem :label="t('login.username')">
                <NInput :value="username" size="large" :placeholder="t('login.username_placeholder')" :disabled="emailCodeSent" @update:value="v => username = v" @blur="fetchAvatar" />
              </NFormItem>
              <template v-if="emailCodeSent">
                <NFormItem :label="t('login.email_code')">
                  <NInput :value="verifyCode" size="large" :placeholder="t('login.email_code')" maxlength="6" autofocus @update:value="v => verifyCode = v" />
                </NFormItem>
                <NButton type="primary" size="large" block :loading="loading" attr-type="submit">
                  {{ t('login.verify') }}
                </NButton>
              </template>
              <NButton v-else type="primary" size="large" block :loading="loading" attr-type="submit">
                {{ t('login.send_code') }}
              </NButton>
            </NForm>
          </NTabPane>

          <!-- OTP -->
          <NTabPane name="otp" :tab="t('login.otp_tab')">
            <NForm class="tab-form" @submit.prevent="handleOTPLogin">
              <NFormItem :label="t('login.username')">
                <NInput :value="username" size="large" :placeholder="t('login.username_placeholder')" @update:value="v => username = v" @blur="fetchAvatar" />
              </NFormItem>
              <NFormItem :label="t('login.otp_code')">
                <NInput :value="otpCode" size="large" :placeholder="t('login.otp_code')" maxlength="6" @update:value="v => otpCode = v" />
              </NFormItem>
              <NButton type="primary" size="large" block :loading="loading" attr-type="submit">
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

          <div v-if="availableMethods.length > 1" style="display:flex;justify-content:center;gap:8px;margin-bottom:16px">
            <NButton
              v-for="m in availableMethods" :key="m"
              :type="selectedMethod === m ? 'primary' : 'default'"
              size="small"
              @click="selectedMethod = m; verifyCode = ''"
            >
              {{ m === 'email' ? t('login.2fa_use_email') : t('login.2fa_use_otp') }}
            </NButton>
          </div>

          <NForm @submit.prevent="handle2FA">
            <NFormItem :label="selectedMethod === 'email' ? t('login.email_code') : t('login.otp_code')">
              <NInput :value="verifyCode" size="large" maxlength="6" autofocus @update:value="v => verifyCode = v" />
            </NFormItem>
            <NButton type="primary" size="large" block :loading="loading" attr-type="submit">
              {{ t('login.verify') }}
            </NButton>
          </NForm>
        </div>
      </template>
      </NCard>
    </section>
  </div>
</template>

<style lang="scss" scoped>
.login-page {
  display: grid;
  min-height: 100dvh;
  grid-template-columns: minmax(480px, 1.05fr) minmax(480px, .95fr);
  overflow: auto;
  background: var(--domus-canvas);
}

.login-story {
  position: relative;
  min-height: 100dvh;
  overflow: hidden;
  color: #fff;
  background:
    radial-gradient(circle at 15% 8%, rgb(130 145 255 / 38%), transparent 31%),
    radial-gradient(circle at 92% 88%, rgb(82 202 170 / 18%), transparent 35%),
    linear-gradient(145deg, #18223f 0%, #27346b 58%, #3443a1 100%);
}

.login-story::before {
  position: absolute;
  inset: 0;
  background-image: linear-gradient(rgb(255 255 255 / 4%) 1px, transparent 1px), linear-gradient(90deg, rgb(255 255 255 / 4%) 1px, transparent 1px);
  background-size: 46px 46px;
  content: '';
  mask-image: linear-gradient(to bottom, black, transparent 78%);
}

.login-story__inner {
  position: relative;
  z-index: 1;
  display: flex;
  width: min(680px, 100%);
  min-height: 100%;
  flex-direction: column;
  margin: 0 auto;
  padding: clamp(38px, 5vw, 72px);
}

.login-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 15px;
  font-weight: 800;
  letter-spacing: .16em;
}

.login-brand__mark {
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  border: 1px solid rgb(255 255 255 / 22%);
  border-radius: 13px;
  background: rgb(255 255 255 / 13%);
  box-shadow: inset 0 1px rgb(255 255 255 / 16%);
  font-size: 18px;
  letter-spacing: 0;
}

.login-story__copy {
  max-width: 600px;
  margin-top: clamp(80px, 13vh, 150px);
}

.login-kicker {
  display: block;
  margin-bottom: 18px;
  color: #aeb9ff;
  font-size: 12px;
  font-weight: 750;
  letter-spacing: .13em;
  text-transform: uppercase;
}

.login-story__copy h1 {
  max-width: 560px;
  margin: 0;
  font-size: clamp(42px, 5vw, 68px);
  font-weight: 720;
  letter-spacing: -.055em;
  line-height: 1.02;
}

.login-story__copy > p {
  max-width: 560px;
  margin: 26px 0 0;
  color: rgb(235 238 255 / 72%);
  font-size: 16px;
  line-height: 1.7;
}

.login-story__copy ul {
  display: grid;
  gap: 13px;
  margin: 30px 0 0;
  padding: 0;
  list-style: none;
}

.login-story__copy li {
  display: flex;
  align-items: center;
  gap: 11px;
  color: rgb(255 255 255 / 88%);
  font-size: 14px;
  font-weight: 560;
}

.login-story__copy li span {
  display: grid;
  width: 21px;
  height: 21px;
  flex: 0 0 auto;
  place-items: center;
  border-radius: 50%;
  color: #fff;
  background: #35a47f;
}

.login-story__copy li svg { width: 13px; height: 13px; }

.storage-visual {
  position: relative;
  height: 165px;
  margin-top: auto;
}

.storage-visual__glow {
  position: absolute;
  right: 7%;
  bottom: -90px;
  width: 360px;
  height: 210px;
  border-radius: 50%;
  background: rgb(113 130 255 / 28%);
  filter: blur(48px);
}

.storage-visual__card {
  position: absolute;
  right: 0;
  display: flex;
  width: min(450px, 92%);
  align-items: center;
  border: 1px solid rgb(255 255 255 / 17%);
  background: rgb(255 255 255 / 11%);
  box-shadow: 0 26px 60px rgb(8 12 35 / 28%), inset 0 1px rgb(255 255 255 / 12%);
  backdrop-filter: blur(18px);
}

.storage-visual__card--back {
  top: 8px;
  right: 32px;
  height: 92px;
  gap: 7px;
  padding: 0 20px;
  border-radius: 18px;
  opacity: .42;
  transform: rotate(-3deg);
}

.storage-visual__card--back span { width: 8px; height: 8px; border-radius: 50%; background: #d8ddff; }

.storage-visual__card--front {
  right: 2px;
  bottom: 0;
  min-height: 86px;
  gap: 14px;
  padding: 16px 18px;
  border-radius: 20px;
}

.storage-visual__card--front > svg { width: 21px; height: 21px; margin-left: auto; color: #72d9b6; }
.storage-visual__card--front strong,
.storage-visual__card--front small { display: block; }
.storage-visual__card--front strong { font-size: 13px; }
.storage-visual__card--front small { margin-top: 4px; color: rgb(235 238 255 / 78%); font-size: 11px; }

.storage-visual__lock {
  display: grid;
  width: 46px;
  height: 46px;
  flex: 0 0 auto;
  place-items: center;
  border-radius: 14px;
  color: #4050c7;
  background: #fff;
  font-weight: 800;
}

.login-panel {
  display: grid;
  min-height: 100dvh;
  place-items: center;
  padding: 48px clamp(34px, 6vw, 94px);
}

.login-card {
  width: min(440px, 100%);
  background: transparent;
  box-shadow: none;
}

.login-header {
  margin-bottom: 30px;

  h2 {
    margin: 18px 0 0;
    color: var(--domus-ink);
    font-size: 30px;
    font-weight: 720;
    letter-spacing: -.035em;
  }

  p {
    margin: 8px 0 0;
    color: var(--domus-muted);
    font-size: 14px;
    line-height: 1.55;
  }
}

.login-icon {
  display: grid;
  width: 52px;
  height: 52px;
  place-items: center;
  border-radius: 16px;
  color: #fff;
  background: linear-gradient(145deg, #6574f0, #4453d3);
  box-shadow: 0 12px 24px rgb(79 95 231 / 24%);
  font-size: 21px;
  font-weight: 800;
}

.login-avatar {
  width: 52px;
  height: 52px;
  border-radius: 50%;
  object-fit: cover;
}

.tab-form { margin-top: 22px; }
.tab-form :deep(.n-form-item) { margin-bottom: 18px; }
.tab-form :deep(.n-form-item-label) { color: #3f495d; font-size: 13px; font-weight: 650; }
.login-card :deep(.n-tabs-tab) { padding-top: 12px; padding-bottom: 12px; }
.verify-section { padding-top: 8px; }

.verify-hint {
  text-align: center;
  color: var(--domus-muted);
  margin-bottom: 16px;
}

@media (max-width: 980px) {
  .login-page { grid-template-columns: minmax(370px, .85fr) minmax(430px, 1fr); }
  .login-story__inner { padding: 42px; }
  .login-story__copy h1 { font-size: 44px; }
  .storage-visual { display: none; }
}

@include mobile {
  .login-page { display: block; background: #fff; }
  .login-story { min-height: 210px; }
  .login-story__inner { min-height: 210px; padding: 24px; }
  .login-brand__mark { width: 36px; height: 36px; border-radius: 11px; font-size: 15px; }
  .login-story__copy { margin-top: 32px; }
  .login-kicker,
  .login-story__copy > p,
  .login-story__copy ul { display: none; }
  .login-story__copy h1 { max-width: 310px; font-size: 31px; line-height: 1.08; }
  .login-panel { min-height: auto; place-items: start stretch; margin-top: -20px; padding: 0; }
  .login-card { width: 100%; border-radius: 22px 22px 0 0; background: var(--domus-surface); }
  .login-card :deep(.n-card__content) { padding: 28px 24px 34px; }
  .login-icon { display: none; }
  .login-header h2 { margin-top: 0; font-size: 27px; }
}
</style>
