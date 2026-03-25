<script setup>
import { ref, watch } from 'vue'
import { NCard, NForm, NFormItem, NInput, NInputNumber, NButton, NSpace, NSwitch, NTag, NIcon, useMessage } from 'naive-ui'
import IconClose from '~icons/mdi/close'
import { usePanelResize } from '../../../composables/usePanelResize'
import { useWebSocket } from '../../../composables/useWebSocket'
import { useI18n } from '../../../composables/useI18n'
import { showConfirm } from '../../../composables/useNativeDialog'
import { useWindowManagerStore } from '../../../stores/windowManager'
import QRCode from 'qrcode'

const ws = useWebSocket()
const { t, te } = useI18n()
const message = useMessage()
const wm = useWindowManagerStore()

const props = defineProps({ show: Boolean })
const emit = defineEmits(['update:show'])
const { panelSize, onMouseDown } = usePanelResize()

// Security status
const status = ref({ email: '', has_email: false, totp_enabled: false, smtp_enabled: false })

// Password change
const oldPwd = ref('')
const newPwd = ref('')
const pwdLoading = ref(false)

// Email binding
const bindEmail = ref('')
const bindCode = ref('')
const emailStep = ref('idle') // 'idle' | 'code_sent'
const emailLoading = ref(false)

// Preferences
const LARGE_FILE_LIMIT_KEY = 'zephyr_large_file_limit'
const DEFAULT_LIMIT_MB = 10
const largeFileLimitMB = ref(DEFAULT_LIMIT_MB)

function loadPreferences() {
  const saved = localStorage.getItem(LARGE_FILE_LIMIT_KEY)
  largeFileLimitMB.value = saved ? Number(saved) / (1024 * 1024) : DEFAULT_LIMIT_MB
}

function updateLargeFileLimit(val) {
  largeFileLimitMB.value = val
  localStorage.setItem(LARGE_FILE_LIMIT_KEY, String(Math.round(val * 1024 * 1024)))
}

// OTP setup
const otpStep = ref('idle') // 'idle' | 'setup'
const otpSecret = ref('')
const otpQR = ref('')
const otpCode = ref('')
const otpLoading = ref(false)

async function loadStatus() {
  try {
    status.value = await ws.request('user.security')
  } catch (e) {
    message.error(te(e, 'account.load_failed'))
  }
}

watch(() => props.show, (v) => {
  if (v) { loadStatus(); loadPreferences() }
})

// Password
async function changePassword() {
  if (!oldPwd.value || !newPwd.value) return
  pwdLoading.value = true
  try {
    await ws.request('user.changePassword', { old_password: oldPwd.value, new_password: newPwd.value })
    message.success(t('account.password_changed'))
    oldPwd.value = ''
    newPwd.value = ''
  } catch (e) {
    message.error(te(e, 'account.password_wrong'))
  } finally {
    pwdLoading.value = false
  }
}

// Email bind
async function sendBindCode() {
  if (!bindEmail.value) return
  emailLoading.value = true
  try {
    await ws.request('user.bindEmail', { email: bindEmail.value })
    emailStep.value = 'code_sent'
    message.success(t('login.code_sent'))
  } catch (e) {
    message.error(te(e, 'login.failed'))
  } finally {
    emailLoading.value = false
  }
}

async function verifyBindCode() {
  if (!bindCode.value) return
  emailLoading.value = true
  try {
    await ws.request('user.verifyBindEmail', { email: bindEmail.value, code: bindCode.value })
    message.success(t('account.email_bound'))
    emailStep.value = 'idle'
    bindEmail.value = ''
    bindCode.value = ''
    await loadStatus()
  } catch (e) {
    message.error(te(e, 'login.verify_failed'))
  } finally {
    emailLoading.value = false
  }
}

async function unbindEmail() {
  if (!await showConfirm(t('account.email_unbind') + '?')) return
  try {
    await ws.request('user.unbindEmail')
    message.success(t('account.email_unbound'))
    await loadStatus()
  } catch (e) {
    message.error(te(e, 'account.unbind_failed'))
  }
}

// OTP
async function setupOTP() {
  otpLoading.value = true
  try {
    const data = await ws.request('user.otpSetup')
    otpSecret.value = data.secret
    otpQR.value = await QRCode.toDataURL(data.uri, { width: 200, margin: 2 })
    otpStep.value = 'setup'
    otpCode.value = ''
  } catch (e) {
    message.error(te(e))
  } finally {
    otpLoading.value = false
  }
}

async function enableOTP() {
  if (!otpCode.value) return
  otpLoading.value = true
  try {
    await ws.request('user.otpEnable', { code: otpCode.value })
    message.success(t('account.otp_enabled'))
    otpStep.value = 'idle'
    otpSecret.value = ''
    otpQR.value = ''
    otpCode.value = ''
    await loadStatus()
  } catch (e) {
    message.error(te(e, 'login.verify_failed'))
  } finally {
    otpLoading.value = false
  }
}

async function disableOTP() {
  if (!await showConfirm(t('account.otp_confirm_disable'))) return
  try {
    await ws.request('user.otpDisable')
    message.success(t('account.otp_disabled'))
    await loadStatus()
  } catch (e) {
    message.error(te(e, 'account.disable_failed'))
  }
}
</script>

<template>
  <Transition name="slide-up">
    <div v-if="show" class="account-panel" :style="{ width: panelSize.width + 'px', height: panelSize.height + 'px' }">
      <div class="panel-resize-handle" @mousedown="onMouseDown" />
      <div class="account-header">
        <span class="account-title">{{ t('account.security') }}</span>
        <NButton size="tiny" quaternary @click="emit('update:show', false)">
          <template #icon><NIcon><IconClose /></NIcon></template>
        </NButton>
      </div>
      <div class="account-scroll">
      <!-- Preferences -->
      <NCard :title="t('account.preferences')" size="small" style="margin-bottom: 16px">
        <NFormItem :label="t('account.large_file_limit')">
          <NInputNumber
            :value="largeFileLimitMB"
            :min="1"
            :max="500"
            :step="5"
            style="width: 100%"
            @update:value="updateLargeFileLimit"
          >
            <template #suffix>MB</template>
          </NInputNumber>
        </NFormItem>
      </NCard>

      <!-- Window Settings -->
      <NCard :title="t('account.window_title')" size="small" style="margin-bottom: 16px">
        <NFormItem :label="t('account.always_center')">
          <NSwitch
            :value="wm.alwaysCenter"
            @update:value="v => wm.updatePrefs({ alwaysCenter: v })"
          />
        </NFormItem>
        <template v-if="wm.alwaysCenter">
          <NFormItem :label="t('account.default_width')">
            <NInputNumber
              :value="wm.defaultWidth"
              :min="400"
              :max="3840"
              :step="50"
              style="width: 100%"
              @update:value="v => wm.updatePrefs({ defaultWidth: v })"
            >
              <template #suffix>px</template>
            </NInputNumber>
          </NFormItem>
          <NFormItem :label="t('account.default_height')">
            <NInputNumber
              :value="wm.defaultHeight"
              :min="300"
              :max="2160"
              :step="50"
              style="width: 100%"
              @update:value="v => wm.updatePrefs({ defaultHeight: v })"
            >
              <template #suffix>px</template>
            </NInputNumber>
          </NFormItem>
        </template>
      </NCard>

      <!-- Password Change -->
      <NCard :title="t('account.change_password')" size="small" style="margin-bottom: 16px">
        <NForm @submit.prevent="changePassword">
          <NFormItem :label="t('account.old_password')">
            <NInput v-model:value="oldPwd" type="password" show-password-on="click" />
          </NFormItem>
          <NFormItem :label="t('account.new_password')">
            <NInput v-model:value="newPwd" type="password" show-password-on="click" />
          </NFormItem>
          <NButton type="primary" :loading="pwdLoading" attr-type="submit" block>
            {{ t('account.change_password') }}
          </NButton>
        </NForm>
      </NCard>

      <!-- Email Binding -->
      <NCard :title="t('account.email_title')" size="small" style="margin-bottom: 16px">
        <template v-if="!status.smtp_enabled">
          <p style="color: var(--breeze-text-secondary)">{{ t('account.email_unavailable') }}</p>
        </template>
        <template v-else-if="status.has_email">
          <NSpace align="center" justify="space-between">
            <span>{{ status.email }}</span>
            <NButton size="small" type="warning" @click="unbindEmail">{{ t('account.email_unbind') }}</NButton>
          </NSpace>
        </template>
        <template v-else>
          <NForm @submit.prevent="emailStep === 'code_sent' ? verifyBindCode() : sendBindCode()">
            <NFormItem :label="t('account.email_input')">
              <NInput v-model:value="bindEmail" :disabled="emailStep === 'code_sent'" />
            </NFormItem>
            <template v-if="emailStep === 'code_sent'">
              <NFormItem :label="t('account.email_code')">
                <NInput v-model:value="bindCode" maxlength="6" />
              </NFormItem>
              <NButton type="primary" :loading="emailLoading" attr-type="submit" block>
                {{ t('login.verify') }}
              </NButton>
            </template>
            <NButton v-else type="primary" :loading="emailLoading" attr-type="submit" block>
              {{ t('account.email_send_code') }}
            </NButton>
          </NForm>
        </template>
      </NCard>

      <!-- OTP Setup -->
      <NCard :title="t('account.otp_title')" size="small">
        <template v-if="status.totp_enabled">
          <NSpace align="center" justify="space-between">
            <NTag type="success">{{ t('account.otp_enabled') }}</NTag>
            <NButton size="small" type="warning" @click="disableOTP">{{ t('account.otp_disable') }}</NButton>
          </NSpace>
        </template>
        <template v-else-if="otpStep === 'setup'">
          <p style="color: var(--breeze-text-secondary); margin-bottom: 12px">{{ t('account.otp_scan_hint') }}</p>
          <div style="text-align: center; margin-bottom: 12px">
            <img v-if="otpQR" :src="otpQR" alt="QR Code" style="border-radius: 4px" />
          </div>
          <NFormItem :label="t('account.otp_manual_key')">
            <NInput :value="otpSecret" readonly />
          </NFormItem>
          <NForm @submit.prevent="enableOTP">
            <NFormItem :label="t('account.otp_verify_hint')">
              <NInput v-model:value="otpCode" maxlength="6" />
            </NFormItem>
            <NButton type="primary" :loading="otpLoading" attr-type="submit" block>
              {{ t('login.verify') }}
            </NButton>
          </NForm>
        </template>
        <template v-else>
          <NButton type="primary" :loading="otpLoading" block @click="setupOTP">
            {{ t('account.otp_enable') }}
          </NButton>
        </template>
      </NCard>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.account-panel {
  position: fixed;
  bottom: 68px;
  right: 8px;
  z-index: 1000;
  background: #2a2e32;
  border: 1px solid #3b4045;
  border-radius: 8px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.panel-resize-handle {
  height: 4px;
  cursor: ns-resize;
  flex-shrink: 0;
  background: transparent;
}
.panel-resize-handle:hover {
  background: rgba(61, 174, 233, 0.3);
}

.account-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  border-bottom: 1px solid #3b4045;
  flex-shrink: 0;
}

.account-title {
  font-size: 13px;
  font-weight: 600;
  color: #bfc5ca;
}

.account-scroll {
  overflow-y: auto;
  padding: 12px;
  flex: 1;
  min-height: 0;
}

.account-scroll :deep(.n-card) {
  background: rgba(255, 255, 255, 0.02);
  border-color: #3b4045;
}

.slide-up-enter-active,
.slide-up-leave-active {
  transition: all 0.2s ease;
}
.slide-up-enter-from,
.slide-up-leave-to {
  opacity: 0;
  transform: translateY(12px);
}
</style>
