<script setup>
import { ref, computed, watch } from 'vue'
import { useWebSocket } from '../../../composables/useWebSocket'
import { useI18n } from '../../../composables/useI18n'
import { useMessage } from '../../../composables/useMessage'
import { showConfirm } from '../../../composables/useNativeDialog'
import { useWindowManagerStore, PROFILE_ICON } from '../../../stores/windowManager'
import { useAuthStore } from '../../../stores/auth'
import api from '../../../composables/useApi'
import QRCode from 'qrcode'
import PlasmaWindow from '../Window.vue'
import BTabs from '../../breeze/BTabs.vue'
import BTabPane from '../../breeze/BTabPane.vue'
import BCard from '../../breeze/BCard.vue'
import BForm from '../../breeze/BForm.vue'
import BFormItem from '../../breeze/BFormItem.vue'
import BInput from '../../breeze/BInput.vue'
import BButton from '../../breeze/BButton.vue'
import BTag from '../../breeze/BTag.vue'

const WINDOW_ID = 'profile'

const ws = useWebSocket()
const { t, te } = useI18n()
const message = useMessage()
const wm = useWindowManagerStore()
const auth = useAuthStore()

const win = computed(() => wm.findWindow(WINDOW_ID))
const windowOpen = computed(() => !!win.value)
const activeTab = ref('profile')

function handleClose() {
  wm.closeWindow(WINDOW_ID)
}

// --- Profile tab ---
const avatarUrl = ref('')
const displayName = ref('')
const editingName = ref(false)
const nameLoading = ref(false)
const storageSize = ref(0)
const storageCount = ref(0)
const storageLoading = ref(false)

const avatarInitial = computed(() => (auth.username || '?')[0].toUpperCase())
const avatarColor = computed(() => {
  let hash = 0
  for (const c of auth.username || '') hash = ((hash << 5) - hash + c.charCodeAt(0)) | 0
  const hue = ((hash % 360) + 360) % 360
  return `hsl(${hue}, 50%, 40%)`
})

async function loadProfile() {
  try {
    const data = await ws.request('user.me')
    displayName.value = data.display_name || ''
    avatarUrl.value = data.avatar_url || ''
  } catch { /* ignore */ }
}

async function loadStorage() {
  storageLoading.value = true
  try {
    const data = await ws.request('user.storageUsage')
    storageSize.value = data.size
    storageCount.value = data.count
  } catch { /* ignore */ }
  storageLoading.value = false
}

async function saveDisplayName() {
  if (!displayName.value.trim()) return
  nameLoading.value = true
  try {
    await ws.request('user.updateDisplayName', { display_name: displayName.value.trim() })
    message.success(t('profile.name_updated'))
    editingName.value = false
  } catch (e) {
    message.error(te(e, 'profile.name_failed'))
  } finally {
    nameLoading.value = false
  }
}

function cancelEditName() {
  editingName.value = false
  loadProfile()
}

const fileInputRef = ref(null)

function triggerAvatarUpload() {
  fileInputRef.value?.click()
}

async function handleAvatarFile(e) {
  const file = e.target.files?.[0]
  if (!file) return
  if (file.size > 10 * 1024 * 1024) {
    message.warning(t('profile.avatar_too_large'))
    e.target.value = ''
    return
  }
  try {
    const form = new FormData()
    form.append('file', file)
    const res = await api.post('/user/avatar', form)
    avatarUrl.value = res.data.avatar_url
    message.success(t('profile.avatar_updated'))
  } catch (err) {
    message.error(te(err, 'profile.avatar_failed'))
  }
  e.target.value = ''
}

function formatSize(bytes) {
  if (!bytes) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0, size = bytes
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return `${size.toFixed(1)} ${units[i]}`
}

// --- Security tab ---
const status = ref({ email: '', has_email: false, totp_enabled: false, smtp_enabled: false })
const oldPwd = ref('')
const newPwd = ref('')
const pwdLoading = ref(false)
const bindEmail = ref('')
const bindCode = ref('')
const emailStep = ref('idle')
const emailLoading = ref(false)
const otpStep = ref('idle')
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

// Load data when window opens
watch(windowOpen, (v) => {
  if (v) {
    if (win.value?.data?.tab) activeTab.value = win.value.data.tab
    loadProfile()
    loadStorage()
    loadStatus()
  }
})

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
  <PlasmaWindow
    v-if="windowOpen"
    :window-id="WINDOW_ID"
    title="我"
    :icon="PROFILE_ICON"
    @close="handleClose"
  >
    <div class="profile-app">
      <BTabs :value="activeTab" @update:value="v => activeTab = v">
        <BTabPane name="profile" :tab="t('profile.tab_profile')">
          <div class="profile-content">
            <!-- Avatar -->
            <div class="profile-avatar-section">
              <div class="profile-avatar" :style="{ background: avatarUrl ? 'none' : avatarColor }" @click="triggerAvatarUpload">
                <img v-if="avatarUrl" :src="avatarUrl" class="profile-avatar-img" />
                <span v-else class="profile-avatar-initial">{{ avatarInitial }}</span>
                <div class="profile-avatar-overlay">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M23 19a2 2 0 01-2 2H3a2 2 0 01-2-2V8a2 2 0 012-2h4l2-3h6l2 3h4a2 2 0 012 2z"/><circle cx="12" cy="13" r="4"/></svg>
                </div>
              </div>
              <input ref="fileInputRef" type="file" accept="image/*" style="display:none" @change="handleAvatarFile" />
            </div>

            <!-- Username (account name, readonly) -->
            <div class="profile-field">
              <span class="profile-label">{{ t('profile.account_name') }}</span>
              <span class="profile-value profile-value--muted">{{ auth.username }}</span>
            </div>

            <!-- Display name -->
            <div class="profile-field">
              <span class="profile-label">{{ t('profile.display_name') }}</span>
              <div v-if="editingName" class="profile-name-edit">
                <BInput :value="displayName" size="small" @update:value="v => displayName = v" @keyup.enter="saveDisplayName" @keyup.escape="cancelEditName" />
                <BButton size="small" type="primary" :loading="nameLoading" @click="saveDisplayName">{{ t('dialog.ok') }}</BButton>
                <BButton size="small" @click="cancelEditName">{{ t('dialog.cancel') }}</BButton>
              </div>
              <div v-else class="profile-name-display">
                <span class="profile-value">{{ displayName || '—' }}</span>
                <button class="profile-edit-btn" @click="editingName = true">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
                </button>
              </div>
            </div>

            <!-- Role -->
            <div class="profile-field">
              <span class="profile-label">{{ t('profile.role') }}</span>
              <BTag :type="auth.isRoot ? 'warning' : 'default'">{{ auth.user?.role }}</BTag>
            </div>

            <!-- Storage usage -->
            <div class="profile-field">
              <span class="profile-label">{{ t('profile.storage') }}</span>
              <span v-if="storageLoading" class="profile-value profile-value--muted">...</span>
              <span v-else class="profile-value">{{ formatSize(storageSize) }} · {{ storageCount }} {{ t('profile.files') }}</span>
            </div>
          </div>
        </BTabPane>

        <BTabPane name="security" :tab="t('account.security')">
          <div class="security-content">
            <!-- Password Change -->
            <BCard :title="t('account.change_password')" size="small" style="margin-bottom: 16px">
              <BForm @submit.prevent="changePassword">
                <BFormItem :label="t('account.old_password')">
                  <BInput :value="oldPwd" type="password" show-password-on="click" @update:value="v => oldPwd = v" />
                </BFormItem>
                <BFormItem :label="t('account.new_password')">
                  <BInput :value="newPwd" type="password" show-password-on="click" @update:value="v => newPwd = v" />
                </BFormItem>
                <BButton type="primary" :loading="pwdLoading" attr-type="submit" block>
                  {{ t('account.change_password') }}
                </BButton>
              </BForm>
            </BCard>

            <!-- Email Binding -->
            <BCard :title="t('account.email_title')" size="small" style="margin-bottom: 16px">
              <template v-if="!status.smtp_enabled">
                <p style="color: var(--breeze-text-secondary)">{{ t('account.email_unavailable') }}</p>
              </template>
              <template v-else-if="status.has_email">
                <div style="display:flex;align-items:center;justify-content:space-between">
                  <span>{{ status.email }}</span>
                  <BButton size="small" type="warning" @click="unbindEmail">{{ t('account.email_unbind') }}</BButton>
                </div>
              </template>
              <template v-else>
                <BForm @submit.prevent="emailStep === 'code_sent' ? verifyBindCode() : sendBindCode()">
                  <BFormItem :label="t('account.email_input')">
                    <BInput :value="bindEmail" :disabled="emailStep === 'code_sent'" @update:value="v => bindEmail = v" />
                  </BFormItem>
                  <template v-if="emailStep === 'code_sent'">
                    <BFormItem :label="t('account.email_code')">
                      <BInput :value="bindCode" maxlength="6" @update:value="v => bindCode = v" />
                    </BFormItem>
                    <BButton type="primary" :loading="emailLoading" attr-type="submit" block>
                      {{ t('login.verify') }}
                    </BButton>
                  </template>
                  <BButton v-else type="primary" :loading="emailLoading" attr-type="submit" block>
                    {{ t('account.email_send_code') }}
                  </BButton>
                </BForm>
              </template>
            </BCard>

            <!-- OTP Setup -->
            <BCard :title="t('account.otp_title')" size="small">
              <template v-if="status.totp_enabled">
                <div style="display:flex;align-items:center;justify-content:space-between">
                  <BTag type="success">{{ t('account.otp_enabled') }}</BTag>
                  <BButton size="small" type="warning" @click="disableOTP">{{ t('account.otp_disable') }}</BButton>
                </div>
              </template>
              <template v-else-if="otpStep === 'setup'">
                <p style="color: var(--breeze-text-secondary); margin-bottom: 12px">{{ t('account.otp_scan_hint') }}</p>
                <div style="text-align: center; margin-bottom: 12px">
                  <img v-if="otpQR" :src="otpQR" alt="QR Code" style="border-radius: 4px" />
                </div>
                <BFormItem :label="t('account.otp_manual_key')">
                  <BInput :value="otpSecret" readonly />
                </BFormItem>
                <BForm @submit.prevent="enableOTP">
                  <BFormItem :label="t('account.otp_verify_hint')">
                    <BInput :value="otpCode" maxlength="6" @update:value="v => otpCode = v" />
                  </BFormItem>
                  <BButton type="primary" :loading="otpLoading" attr-type="submit" block>
                    {{ t('login.verify') }}
                  </BButton>
                </BForm>
              </template>
              <template v-else>
                <BButton type="primary" :loading="otpLoading" block @click="setupOTP">
                  {{ t('account.otp_enable') }}
                </BButton>
              </template>
            </BCard>
          </div>
        </BTabPane>
      </BTabs>
    </div>
  </PlasmaWindow>
</template>

<style lang="scss" scoped>
.profile-app {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
}

.profile-content {
  padding-top: 8px;
}

// Avatar
.profile-avatar-section {
  display: flex;
  justify-content: center;
  margin-bottom: 20px;
}

.profile-avatar {
  position: relative;
  width: 80px;
  height: 80px;
  border-radius: 50%;
  @include flex-center;
  cursor: pointer;
  overflow: hidden;
  border: 2px solid var(--breeze-border);

  &:hover .profile-avatar-overlay {
    opacity: 1;
  }
}

.profile-avatar-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.profile-avatar-initial {
  font-size: 32px;
  font-weight: 600;
  color: #fff;
  user-select: none;
}

.profile-avatar-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  @include flex-center;
  color: #fff;
  opacity: 0;
  transition: opacity 0.15s;
}

// Fields
.profile-field {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  border-bottom: 1px solid $hover-white-subtle;

  &:last-child {
    border-bottom: none;
  }
}

.profile-label {
  font-size: 13px;
  color: var(--breeze-text-secondary);
  flex-shrink: 0;
}

.profile-value {
  font-size: 13px;
  color: var(--breeze-text);

  &--muted {
    color: var(--breeze-text-disabled);
  }
}

// Display name edit
.profile-name-display {
  display: flex;
  align-items: center;
  gap: 6px;
}

.profile-name-edit {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  max-width: 260px;
}

.profile-edit-btn {
  @include inline-flex-center;
  width: 22px;
  height: 22px;
  border: none;
  background: none;
  color: var(--breeze-text-secondary);
  border-radius: 3px;
  cursor: pointer;
  padding: 0;

  &:hover {
    background: $hover-white-medium;
    color: var(--breeze-accent);
  }
}

.profile-locked {
  display: inline-flex;
  align-items: center;
  color: var(--breeze-text-disabled);
}

// Security tab
.security-content {
  padding-top: 8px;

  :deep(.breeze-card) {
    background: rgba(255, 255, 255, 0.02);
    border-color: #3b4045;
  }
}
</style>
