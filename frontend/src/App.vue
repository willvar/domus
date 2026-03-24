<script setup>
import { onMounted } from 'vue'
import {
  NConfigProvider,
  NMessageProvider,
  NNotificationProvider,
  NSpin,
  darkTheme,
} from 'naive-ui'
import { useAuthStore } from './stores/auth'
import LoginPage from './components/LoginPage.vue'

const auth = useAuthStore()

// Breeze Dark theme overrides for Naive UI
const themeOverrides = {
  common: {
    primaryColor: '#3daee9',
    primaryColorHover: '#4dbcf5',
    primaryColorPressed: '#2980b9',
    primaryColorSuppl: '#3daee9',
    bodyColor: '#1b1e20',
    cardColor: '#2a2e32',
    modalColor: '#2a2e32',
    popoverColor: '#31363b',
    tableColor: '#1e2226',
    inputColor: '#1e2226',
    actionColor: '#2a2e32',
    tagColor: '#31363b',
    hoverColor: 'rgba(255,255,255,0.06)',
    borderColor: '#3b4045',
    dividerColor: '#3b4045',
    textColorBase: '#bfc5ca',
    textColor1: '#bfc5ca',
    textColor2: '#9aa0a6',
    textColor3: '#6e7a86',
    textColorDisabled: '#505962',
    placeholderColor: '#505962',
    iconColor: '#6e7a86',
    borderRadius: '4px',
    fontSize: '16px',
    fontFamily: "'Noto Sans', 'Segoe UI', system-ui, -apple-system, sans-serif",
  },
  Button: {
    textColor: '#bfc5ca',
    colorQuaternary: 'transparent',
    colorQuaternaryHover: 'rgba(255,255,255,0.08)',
    colorQuaternaryPressed: 'rgba(255,255,255,0.12)',
    textColorQuaternary: '#bfc5ca',
    textColorQuaternaryHover: '#bfc5ca',
    textColorQuaternaryPressed: '#bfc5ca',
    borderQuaternary: 'none',
  },
  Input: {
    color: '#1e2226',
    colorFocus: '#1e2226',
    border: '1px solid #3b4045',
    borderHover: '1px solid #3daee9',
    borderFocus: '1px solid #3daee9',
    textColor: '#bfc5ca',
    placeholderColor: '#5c6670',
    caretColor: '#3daee9',
  },
  DataTable: {
    thColor: '#2a2e32',
    tdColor: '#1e2226',
    tdColorHover: 'rgba(255,255,255,0.04)',
    borderColor: '#3b4045',
    thTextColor: '#9aa0a6',
    tdTextColor: '#bfc5ca',
  },
  Menu: {
    color: 'transparent',
    itemTextColor: '#9aa0a6',
    itemTextColorHover: '#bfc5ca',
    itemTextColorActive: '#3daee9',
    itemColorHover: 'rgba(255,255,255,0.05)',
    itemColorActive: 'rgba(61,174,233,0.15)',
    groupHeaderTextColor: '#7f8c8d',
  },
  Dropdown: {
    color: '#31363b',
    optionTextColor: '#bfc5ca',
    optionTextColorHover: '#bfc5ca',
    optionColorHover: 'rgba(255,255,255,0.08)',
    dividerColor: '#3b4045',
  },
  Card: {
    color: '#2a2e32',
    borderColor: '#3b4045',
    titleTextColor: '#bfc5ca',
  },
  Modal: {
    color: '#2a2e32',
    textColor: '#bfc5ca',
  },
  Form: {
    labelTextColor: '#9aa0a6',
  },
  Descriptions: {
    thColor: '#2a2e32',
    tdColor: 'transparent',
    borderColor: '#3b4045',
    thTextColor: '#7f8c8d',
    tdTextColor: '#bfc5ca',
  },
  Slider: {
    fillColor: '#3daee9',
    fillColorHover: '#4dbcf5',
    railColor: '#3b4045',
    dotBorderActive: '2px solid #3daee9',
  },
  Progress: {
    railColor: '#3b4045',
  },
  Empty: {
    textColor: '#5c6670',
  },
  Spin: {
    color: '#3daee9',
  },
}

onMounted(async () => {
  document.addEventListener('contextmenu', (e) => e.preventDefault())
  await auth.checkAuth()
})
</script>

<template>
  <NConfigProvider :theme="darkTheme" :theme-overrides="themeOverrides">
    <NMessageProvider>
      <NNotificationProvider>
        <div v-if="auth.loading" class="loading-screen">
          <NSpin size="large" />
        </div>

        <LoginPage v-else-if="!auth.isLoggedIn" />

        <router-view v-else />
      </NNotificationProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>

<style scoped>
.loading-screen {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  background: var(--breeze-bg);
}
</style>
