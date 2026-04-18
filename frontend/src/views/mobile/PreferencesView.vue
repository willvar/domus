<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { IconChevronLeft } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import { useI18n } from '../../composables/useI18n'
import { usePreferences } from '../../composables/usePreferences'

const router = useRouter()
const { t } = useI18n()
const prefsApi = usePreferences()
const prefs = prefsApi.prefs

const largeFileLabel = computed(() => `${prefs.largeFileLimitMB} MB`)

function toggleBool(key: 'indexContent' | 'sessionIsolation' | 'alwaysCenter'): void {
  prefsApi.update({ [key]: !prefs[key] })
}

function adjustLimit(delta: number): void {
  const next = Math.max(4, Math.min(512, prefs.largeFileLimitMB + delta))
  prefsApi.update({ largeFileLimitMB: next })
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.preferences.title')" :subtitle="t('mobile.preferences.subtitle')" show-back @back="router.back()">
      <template #back><IconChevronLeft width="18" height="18" /></template>
    </MobileTopBar>
    <div class="mobile-page-body prefs-body">
      <section class="prefs-card">
        <div class="prefs-row">
          <div>
            <strong>{{ t('mobile.preferences.large_preview') }}</strong>
            <span>{{ largeFileLabel }}</span>
          </div>
          <div class="prefs-stepper">
            <button @click="adjustLimit(-16)">-</button>
            <button @click="adjustLimit(16)">+</button>
          </div>
        </div>
        <div class="prefs-row">
          <div><strong>{{ t('mobile.preferences.index_content') }}</strong><span>{{ t('mobile.preferences.index_content_hint') }}</span></div>
          <button class="prefs-toggle" :class="{ on: prefs.indexContent }" @click="toggleBool('indexContent')">{{ prefs.indexContent ? t('common.enabled') : t('common.disabled') }}</button>
        </div>
        <div class="prefs-row">
          <div><strong>{{ t('mobile.preferences.session_isolation') }}</strong><span>{{ t('mobile.preferences.session_isolation_hint') }}</span></div>
          <button class="prefs-toggle" :class="{ on: prefs.sessionIsolation }" @click="toggleBool('sessionIsolation')">{{ prefs.sessionIsolation ? t('common.enabled') : t('common.disabled') }}</button>
        </div>
        <div class="prefs-row">
          <div><strong>{{ t('mobile.preferences.center_windows') }}</strong><span>{{ t('mobile.preferences.center_windows_hint') }}</span></div>
          <button class="prefs-toggle" :class="{ on: prefs.alwaysCenter }" @click="toggleBool('alwaysCenter')">{{ prefs.alwaysCenter ? t('common.enabled') : t('common.disabled') }}</button>
        </div>
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

.prefs-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.prefs-card {
  padding: 18px;
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
}

.prefs-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 0;
  border-top: 1px solid rgba(16, 32, 48, 0.08);

  &:first-child {
    padding-top: 0;
    border-top: none;
  }

  strong {
    display: block;
    color: #102030;
  }

  span {
    display: block;
    margin-top: 4px;
    font-size: 12px;
    color: rgba(16, 32, 48, 0.56);
  }
}

.prefs-toggle,
.prefs-stepper button {
  min-width: 56px;
  height: 38px;
  border: none;
  border-radius: 12px;
  background: rgba(16, 32, 48, 0.08);
  color: #102030;
  font-weight: 700;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.95);
    background: rgba(16, 32, 48, 0.12);
  }
}

.prefs-toggle.on {
  background: rgba(61, 174, 233, 0.16);
  color: #1576b8;
}

.prefs-stepper {
  display: flex;
  gap: 8px;
}
</style>
