<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import api from '../../composables/useApi'
import { useAuthStore } from '../../stores/auth'
import { useI18n } from '../../composables/useI18n'

const auth = useAuthStore()
const router = useRouter()
const { locale, setLocale, t } = useI18n()

const displayName = ref('')
const storageSummary = ref('')
const emailSummary = ref(t('mobile.profile.unavailable'))
const securitySummary = ref(t('mobile.profile.security_hint'))

onMounted(async () => {
  displayName.value = auth.user?.display_name || auth.username
  try {
    const { data } = await api.get('/user/storage')
    storageSummary.value = `${formatSize(data.size)} · ${data.count} ${t('profile.files')}`
  } catch {
    storageSummary.value = t('mobile.profile.unavailable')
  }
  try {
    const { data } = await api.get('/user/security')
    emailSummary.value = data.has_email ? data.email : t('mobile.profile.not_linked')
    securitySummary.value = data.totp_enabled ? t('mobile.security.otp_enabled') : t('mobile.security.not_enabled')
  } catch {
    emailSummary.value = t('mobile.profile.unavailable')
  }
})

const avatarInitial = computed(() => (auth.username || '?')[0].toUpperCase())

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(1)} ${units[i]}`
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.profile.title')" :subtitle="t('mobile.profile.subtitle')" />

    <div class="mobile-page-body profile-body">
      <section class="profile-hero">
        <div class="profile-avatar">{{ avatarInitial }}</div>
        <div>
          <div class="profile-name">{{ displayName || auth.username }}</div>
          <div class="profile-handle">@{{ auth.username }}</div>
        </div>
      </section>

      <section class="profile-card">
        <div class="profile-card-title">{{ t('mobile.profile.storage') }}</div>
        <div class="profile-card-value">{{ storageSummary }}</div>
      </section>

      <section class="profile-card profile-card--compact">
        <div class="profile-card-title">{{ t('mobile.profile.security') }}</div>
        <div class="profile-card-value">{{ securitySummary }}</div>
        <div class="profile-card-meta">{{ emailSummary }}</div>
      </section>

      <section class="profile-grid">
        <button class="profile-action" @click="router.push('/m/activity')">{{ t('mobile.profile.activity') }}</button>
        <button class="profile-action" @click="router.push('/m/terminal')">{{ t('app.terminal') }}</button>
        <button class="profile-action" @click="router.push('/m/preferences')">{{ t('mobile.profile.preferences') }}</button>
        <button class="profile-action" @click="router.push('/m/security')">{{ t('mobile.profile.security') }}</button>
        <button class="profile-action" @click="setLocale(locale === 'zh' ? 'en' : 'zh')">{{ t('mobile.profile.language', { locale }) }}</button>
        <button class="profile-action" @click="router.push('/desktop')">{{ t('mobile.profile.desktop') }}</button>
      </section>

      <button class="profile-logout" @click="auth.logout()">{{ t('mobile.profile.sign_out') }}</button>
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

.profile-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.profile-hero,
.profile-card,
.profile-action,
.profile-logout {
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
  transition: box-shadow 0.16s ease, transform 0.16s ease;
}

.profile-hero {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 18px;
  border-radius: 24px;

  &:active {
    transform: scale(0.995);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.08);
  }
}

.profile-avatar {
  width: 56px;
  height: 56px;
  border-radius: 18px;
  background: linear-gradient(135deg, #1576b8, #3daee9);
  color: #fff;
  font-size: 24px;
  font-weight: 700;
  @include flex-center;
}

.profile-name {
  font-size: 18px;
  font-weight: 700;
  color: #102030;
}

.profile-handle {
  margin-top: 4px;
  font-size: 13px;
  color: rgba(16, 32, 48, 0.56);
}

.profile-card {
  padding: 18px;
  border-radius: 22px;

  &:active {
    transform: scale(0.996);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.08);
  }

  &--compact {
    padding-bottom: 16px;
  }
}

.profile-card-title {
  font-size: 13px;
  font-weight: 700;
  text-transform: uppercase;
  color: rgba(16, 32, 48, 0.48);
}

.profile-card-value {
  margin-top: 8px;
  font-size: 16px;
  font-weight: 700;
  color: #102030;
}

.profile-card-meta {
  margin-top: 6px;
  font-size: 13px;
  color: rgba(16, 32, 48, 0.56);
}

.profile-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

@media (max-width: 360px) {
  .profile-grid {
    grid-template-columns: 1fr;
  }
}

.profile-action,
.profile-logout {
  min-height: 88px;
  border: none;
  border-radius: 20px;
  padding: 16px;
  text-align: left;
  font-size: 15px;
  font-weight: 700;
  color: #102030;
  transition: transform 0.16s ease, box-shadow 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.985);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.09);
  }
}

.profile-logout {
  min-height: 52px;
  color: #c23242;
  background: rgba(255, 255, 255, 0.94);
}
</style>
