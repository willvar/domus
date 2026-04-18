<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { IconChevronLeft } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import api from '../../composables/useApi'
import { useI18n } from '../../composables/useI18n'
import { useMessage } from '../../composables/useMessage'
import { showConfirm } from '../../composables/useNativeDialog'
import QRCode from 'qrcode'

const router = useRouter()
const msg = useMessage()
const { t } = useI18n()

const status = ref({ email: '', has_email: false, totp_enabled: false, smtp_enabled: false })
const oldPwd = ref('')
const newPwd = ref('')
const bindEmail = ref('')
const bindCode = ref('')
const emailStep = ref<'idle' | 'code_sent'>('idle')
const otpStep = ref<'idle' | 'setup'>('idle')
const otpSecret = ref('')
const otpCode = ref('')
const otpQR = ref('')

async function loadStatus(): Promise<void> {
  const { data } = await api.get('/user/security')
  status.value = data
}

onMounted(() => {
  loadStatus().catch(() => {})
})

async function changePassword(): Promise<void> {
  if (!oldPwd.value || !newPwd.value) return
  await api.put('/user/security/password', { old_password: oldPwd.value, new_password: newPwd.value })
  oldPwd.value = ''
  newPwd.value = ''
  msg.success(t('mobile.security.password_updated'))
}

async function sendBindCode(): Promise<void> {
  await api.post('/user/security/email/bind', { email: bindEmail.value })
  emailStep.value = 'code_sent'
  msg.success(t('mobile.security.code_sent'))
}

async function verifyBindCode(): Promise<void> {
  await api.post('/user/security/email/verify', { email: bindEmail.value, code: bindCode.value })
  emailStep.value = 'idle'
  bindCode.value = ''
  msg.success(t('mobile.security.email_linked'))
  await loadStatus()
}

async function unbindEmail(): Promise<void> {
  if (!await showConfirm(t('mobile.security.remove_email_confirm'))) return
  await api.delete('/user/security/email')
  msg.success(t('mobile.security.email_removed'))
  await loadStatus()
}

async function toggleOtp(): Promise<void> {
  if (!status.value.totp_enabled) {
    const { data } = await api.post('/user/security/otp/setup')
    otpSecret.value = data.secret
    otpQR.value = await QRCode.toDataURL(data.uri, { width: 220, margin: 2 })
    otpCode.value = ''
    otpStep.value = 'setup'
    return
  }
  if (!await showConfirm(t('mobile.security.disable_otp_confirm'))) return
  await api.delete('/user/security/otp')
  msg.success(t('mobile.security.otp_disabled'))
  await loadStatus()
}

async function enableOtp(): Promise<void> {
  if (!otpCode.value) return
  await api.post('/user/security/otp/enable', { code: otpCode.value })
  otpStep.value = 'idle'
  otpCode.value = ''
  msg.success(t('mobile.security.otp_enabled'))
  await loadStatus()
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.security.title')" :subtitle="t('mobile.security.subtitle')" show-back @back="router.back()">
      <template #back><IconChevronLeft width="18" height="18" /></template>
    </MobileTopBar>
    <div class="mobile-page-body security-body">
      <section class="security-card">
        <div class="security-title">{{ t('mobile.security.password') }}</div>
        <input v-model="oldPwd" type="password" class="security-input" :placeholder="t('mobile.security.current_password')" />
        <input v-model="newPwd" type="password" class="security-input" :placeholder="t('mobile.security.new_password')" />
        <button class="security-btn" @click="changePassword">{{ t('mobile.security.update_password') }}</button>
      </section>

      <section class="security-card">
        <div class="security-title">{{ t('mobile.security.email') }}</div>
        <div class="security-meta">{{ status.has_email ? status.email : t('mobile.security.no_email') }}</div>
        <input v-model="bindEmail" type="email" class="security-input" :placeholder="t('mobile.security.email_address')" />
        <button class="security-btn" :disabled="!bindEmail" @click="sendBindCode">{{ t('mobile.security.send_code') }}</button>
        <template v-if="emailStep === 'code_sent'">
          <input v-model="bindCode" type="text" class="security-input" :placeholder="t('mobile.security.verify_code')" />
          <button class="security-btn" :disabled="!bindCode" @click="verifyBindCode">{{ t('mobile.security.confirm_email') }}</button>
        </template>
        <button v-if="status.has_email" class="security-btn security-btn--danger" @click="unbindEmail">{{ t('mobile.security.remove_email') }}</button>
      </section>

      <section class="security-card">
        <div class="security-title">{{ t('mobile.security.otp') }}</div>
        <div class="security-meta">{{ status.totp_enabled ? t('common.enabled') : t('mobile.security.not_enabled') }}</div>
        <button class="security-btn" @click="toggleOtp">{{ status.totp_enabled ? t('mobile.security.disable_otp') : t('mobile.security.setup_otp') }}</button>
        <template v-if="otpStep === 'setup'">
          <img :src="otpQR" :alt="t('mobile.security.otp_qr_alt')" class="security-qr" />
          <div class="security-meta security-meta--break">{{ otpSecret }}</div>
          <input v-model="otpCode" type="text" class="security-input" :placeholder="t('mobile.security.otp_code')" />
          <button class="security-btn" :disabled="!otpCode" @click="enableOtp">{{ t('mobile.security.confirm_otp') }}</button>
        </template>
      </section>
    </div>
  </section>
</template>

<style lang="scss" scoped>
.mobile-page {
  min-height: 100dvh;
}

.mobile-page-body {
  padding: 16px 16px 188px;
}

.security-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.security-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
  transition: box-shadow 0.16s ease;
}

.security-title {
  font-size: 14px;
  font-weight: 700;
  color: #102030;
}

.security-meta {
  font-size: 13px;
  color: rgba(16, 32, 48, 0.56);

  &--break {
    word-break: break-all;
  }
}

.security-input {
  min-height: 48px;
  border: 1px solid rgba(16, 32, 48, 0.08);
  border-radius: 14px;
  padding: 0 14px;
  background: #fff;
  font-size: 14px;
  color: #102030;
  outline: none;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;

  &:focus {
    border-color: rgba(61, 174, 233, 0.45);
    box-shadow: 0 0 0 4px rgba(61, 174, 233, 0.12);
  }
}

.security-btn {
  min-height: 46px;
  border: none;
  border-radius: 14px;
  background: rgba(61, 174, 233, 0.14);
  color: #1576b8;
  font-weight: 700;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.97);
    background: rgba(61, 174, 233, 0.2);
  }

  &:disabled {
    opacity: 0.45;
  }

  &--danger {
    background: rgba(218, 68, 83, 0.12);
    color: #c23242;
  }
}

.security-qr {
  width: 220px;
  max-width: 100%;
  align-self: center;
  border-radius: 18px;
  background: #fff;
  padding: 10px;
  box-shadow: inset 0 0 0 1px rgba(16, 32, 48, 0.06);
}
</style>
