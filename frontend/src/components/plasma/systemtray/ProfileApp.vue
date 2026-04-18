<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from '../../../composables/useI18n'
import { useMessage } from '../../../composables/useMessage'
import { showConfirm } from '../../../composables/useNativeDialog'
import { useWindowManagerStore, PROFILE_ICON } from '../../../stores/windowManager'
import { useAuthStore } from '../../../stores/auth'
import api from '../../../composables/useApi'
import QRCode from 'qrcode'
import PlasmaWindow from '../Window.vue'
import { Tabs, TabPane, Card, Form, FormItem, Input, Button, Tag } from '../../../barrels/breeze'

const WINDOW_ID = 'profile'

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
    const { data } = await api.get('/user/')
    displayName.value = data.display_name || ''
    avatarUrl.value = data.avatar_url || ''
  } catch { /* ignore */ }
}

async function loadStorage() {
  storageLoading.value = true
  try {
    const { data } = await api.get('/user/storage')
    storageSize.value = data.size
    storageCount.value = data.count
  } catch { /* ignore */ }
  storageLoading.value = false
}

async function saveDisplayName() {
  if (!displayName.value.trim()) return
  nameLoading.value = true
  try {
    await api.put('/user/display-name', { display_name: displayName.value.trim() })
    if (auth.user) auth.user.display_name = displayName.value.trim()
    message.success(t('profile.name_updated'))
    editingName.value = false
  } catch (e: any) {
    message.error(te(e, 'profile.name_failed'))
  } finally {
    nameLoading.value = false
  }
}

function cancelEditName() {
  editingName.value = false
  loadProfile()
}

const fileInputRef = ref<HTMLInputElement | null>(null)

function triggerAvatarUpload() {
  fileInputRef.value?.click()
}

async function handleAvatarFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (file.size > 10 * 1024 * 1024) {
    message.warning(t('profile.avatar_too_large'))
    ;(e.target as HTMLInputElement).value = ''
    return
  }
  try {
    // Resize to 512px via canvas, convert to webp
    const img = new Image()
    const url = URL.createObjectURL(file)
    img.src = url
    await img.decode()
    const scale = Math.min(1, 512 / Math.max(img.naturalWidth, img.naturalHeight))
    const w = Math.round(img.naturalWidth * scale)
    const h = Math.round(img.naturalHeight * scale)
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    canvas.getContext('2d')!.drawImage(img, 0, 0, w, h)
    URL.revokeObjectURL(url)
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(b => b ? resolve(b) : reject(new Error('toBlob failed')), 'image/webp', 0.8)
    })
    // Upload via encrypted pipeline
    const { writeEncryptedFile } = await import('../../../composables/useCryptoUpload')
    await writeEncryptedFile('/.user/avatar.webp', await blob.arrayBuffer(), 'image/webp')
    // Refresh avatar display (backend cache already invalidated on upload complete)
    avatarUrl.value = `/user/avatar/${encodeURIComponent(auth.username)}?t=${Date.now()}`
    message.success(t('profile.avatar_updated'))
  } catch (err: any) {
    message.error(te(err, 'profile.avatar_failed'))
  }
  ;(e.target as HTMLInputElement).value = ''
}

function formatSize(bytes: number) {
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
    const { data } = await api.get('/user/security')
    status.value = data
  } catch (e: any) {
    message.error(te(e, 'account.load_failed'))
  }
}

// Load data when window opens
watch(windowOpen, (v) => {
  if (v) {
    if (win.value?.data?.tab) activeTab.value = win.value.data.tab as string
    loadProfile()
    loadStorage()
    loadStatus()
  }
})

async function changePassword() {
  if (!oldPwd.value || !newPwd.value) return
  pwdLoading.value = true
  try {
    await api.put('/user/security/password', { old_password: oldPwd.value, new_password: newPwd.value })
    message.success(t('account.password_changed'))
    oldPwd.value = ''
    newPwd.value = ''
  } catch (e: any) {
    message.error(te(e, 'account.password_wrong'))
  } finally {
    pwdLoading.value = false
  }
}

async function sendBindCode() {
  if (!bindEmail.value) return
  emailLoading.value = true
  try {
    await api.post('/user/security/email/bind', { email: bindEmail.value })
    emailStep.value = 'code_sent'
    message.success(t('login.code_sent'))
  } catch (e: any) {
    message.error(te(e, 'login.failed'))
  } finally {
    emailLoading.value = false
  }
}

async function verifyBindCode() {
  if (!bindCode.value) return
  emailLoading.value = true
  try {
    await api.post('/user/security/email/verify', { email: bindEmail.value, code: bindCode.value })
    message.success(t('account.email_bound'))
    emailStep.value = 'idle'
    bindEmail.value = ''
    bindCode.value = ''
    await loadStatus()
  } catch (e: any) {
    message.error(te(e, 'login.verify_failed'))
  } finally {
    emailLoading.value = false
  }
}

async function unbindEmail() {
  if (!await showConfirm(t('account.email_unbind') + '?')) return
  try {
    await api.delete('/user/security/email')
    message.success(t('account.email_unbound'))
    await loadStatus()
  } catch (e: any) {
    message.error(te(e, 'account.unbind_failed'))
  }
}

async function setupOTP() {
  otpLoading.value = true
  try {
    const { data } = await api.post('/user/security/otp/setup')
    otpSecret.value = data.secret
    otpQR.value = await QRCode.toDataURL(data.uri, { width: 200, margin: 2 })
    otpStep.value = 'setup'
    otpCode.value = ''
  } catch (e: any) {
    message.error(te(e))
  } finally {
    otpLoading.value = false
  }
}

async function enableOTP() {
  if (!otpCode.value) return
  otpLoading.value = true
  try {
    await api.post('/user/security/otp/enable', { code: otpCode.value })
    message.success(t('account.otp_enabled'))
    otpStep.value = 'idle'
    otpSecret.value = ''
    otpQR.value = ''
    otpCode.value = ''
    await loadStatus()
  } catch (e: any) {
    message.error(te(e, 'login.verify_failed'))
  } finally {
    otpLoading.value = false
  }
}

async function disableOTP() {
  if (!await showConfirm(t('account.otp_confirm_disable'))) return
  try {
    await api.delete('/user/security/otp')
    message.success(t('account.otp_disabled'))
    await loadStatus()
  } catch (e: any) {
    message.error(te(e, 'account.disable_failed'))
  }
}
</script>

<template>
  <PlasmaWindow
    v-if="windowOpen"
    :window-id="WINDOW_ID"
    :title="t('app.profile')"
    :icon="PROFILE_ICON"
    @close="handleClose"
  >
    <div class="profile-app">
      <Tabs :value="activeTab" @update:value="v => activeTab = v">
        <TabPane name="profile" :tab="t('profile.tab_profile')">
          <div class="profile-content">
            <!-- Avatar -->
            <div class="profile-avatar-section">
              <div class="profile-avatar" :style="{ background: avatarUrl ? 'none' : avatarColor }" @click="triggerAvatarUpload">
                <img v-if="avatarUrl" :src="avatarUrl" class="profile-avatar-img" />
                <span v-else class="profile-avatar-initial">{{ avatarInitial }}</span>
                <div class="profile-avatar-overlay">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M23 19a2 2 0 01-2 2H3a2 2 0 01-2-2V8a2 2 0 012-2h4l2-3h6l2 3h4a2 2 0 012 2z" /><circle cx="12" cy="13" r="4" /></svg>
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
                <Input :value="displayName" size="small" @update:value="v => displayName = v" @keyup.enter="saveDisplayName" @keyup.escape="cancelEditName" />
                <Button size="small" type="primary" :loading="nameLoading" @click="saveDisplayName">{{ t('dialog.ok') }}</Button>
                <Button size="small" @click="cancelEditName">{{ t('dialog.cancel') }}</Button>
              </div>
              <div v-else class="profile-name-display">
                <span class="profile-value">{{ displayName || '—' }}</span>
                <button class="profile-edit-btn" @click="editingName = true">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" /><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" /></svg>
                </button>
              </div>
            </div>

            <!-- Role -->
            <div class="profile-field">
              <span class="profile-label">{{ t('profile.role') }}</span>
              <Tag :type="auth.isRoot ? 'warning' : 'default'">{{ auth.user?.role }}</Tag>
            </div>

            <!-- Storage usage -->
            <div class="profile-field">
              <span class="profile-label">{{ t('profile.storage') }}</span>
              <span v-if="storageLoading" class="profile-value profile-value--muted">...</span>
              <span v-else class="profile-value">{{ formatSize(storageSize) }} · {{ storageCount }} {{ t('profile.files') }}</span>
            </div>
          </div>
        </TabPane>

        <TabPane name="security" :tab="t('account.security')">
          <div class="security-content">
            <!-- Password Change -->
            <Card :title="t('account.change_password')" size="small" style="margin-bottom: 16px">
              <Form @submit.prevent="changePassword">
                <FormItem :label="t('account.old_password')">
                  <Input :value="oldPwd" type="password" show-password-on="click" @update:value="v => oldPwd = v" />
                </FormItem>
                <FormItem :label="t('account.new_password')">
                  <Input :value="newPwd" type="password" show-password-on="click" @update:value="v => newPwd = v" />
                </FormItem>
                <Button type="primary" :loading="pwdLoading" attr-type="submit" block>
                  {{ t('account.change_password') }}
                </Button>
              </Form>
            </Card>

            <!-- Email Binding -->
            <Card :title="t('account.email_title')" size="small" style="margin-bottom: 16px">
              <template v-if="!status.smtp_enabled">
                <p style="color: var(--breeze-text-secondary)">{{ t('account.email_unavailable') }}</p>
              </template>
              <template v-else-if="status.has_email">
                <div style="display:flex;align-items:center;justify-content:space-between">
                  <span>{{ status.email }}</span>
                  <Button size="small" type="warning" @click="unbindEmail">{{ t('account.email_unbind') }}</Button>
                </div>
              </template>
              <template v-else>
                <Form @submit.prevent="emailStep === 'code_sent' ? verifyBindCode() : sendBindCode()">
                  <FormItem :label="t('account.email_input')">
                    <Input :value="bindEmail" :disabled="emailStep === 'code_sent'" @update:value="v => bindEmail = v" />
                  </FormItem>
                  <template v-if="emailStep === 'code_sent'">
                    <FormItem :label="t('account.email_code')">
                      <Input :value="bindCode" maxlength="6" @update:value="v => bindCode = v" />
                    </FormItem>
                    <Button type="primary" :loading="emailLoading" attr-type="submit" block>
                      {{ t('login.verify') }}
                    </Button>
                  </template>
                  <Button v-else type="primary" :loading="emailLoading" attr-type="submit" block>
                    {{ t('account.email_send_code') }}
                  </Button>
                </Form>
              </template>
            </Card>

            <!-- OTP Setup -->
            <Card :title="t('account.otp_title')" size="small">
              <template v-if="status.totp_enabled">
                <div style="display:flex;align-items:center;justify-content:space-between">
                  <Tag type="success">{{ t('account.otp_enabled') }}</Tag>
                  <Button size="small" type="warning" @click="disableOTP">{{ t('account.otp_disable') }}</Button>
                </div>
              </template>
              <template v-else-if="otpStep === 'setup'">
                <p style="color: var(--breeze-text-secondary); margin-bottom: 12px">{{ t('account.otp_scan_hint') }}</p>
                <div style="text-align: center; margin-bottom: 12px">
                  <img v-if="otpQR" :src="otpQR" alt="QR Code" style="border-radius: 4px" />
                </div>
                <FormItem :label="t('account.otp_manual_key')">
                  <Input :value="otpSecret" readonly />
                </FormItem>
                <Form @submit.prevent="enableOTP">
                  <FormItem :label="t('account.otp_verify_hint')">
                    <Input :value="otpCode" maxlength="6" @update:value="v => otpCode = v" />
                  </FormItem>
                  <Button type="primary" :loading="otpLoading" attr-type="submit" block>
                    {{ t('login.verify') }}
                  </Button>
                </Form>
              </template>
              <template v-else>
                <Button type="primary" :loading="otpLoading" block @click="setupOTP">
                  {{ t('account.otp_enable') }}
                </Button>
              </template>
            </Card>
          </div>
        </TabPane>
      </Tabs>
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

  &:active .profile-avatar-overlay {
    opacity: 1;
  }

  @include hover {
    .profile-avatar-overlay {
      opacity: 1;
    }
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

  &:active {
    background: $hover-white-medium;
    color: var(--breeze-accent);
  }

  @include hover {
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
