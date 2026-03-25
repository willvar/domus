<script setup>
import { computed } from 'vue'
import { NDropdown } from 'naive-ui'
import { useWindowManagerStore } from '../stores/windowManager'
import { useAuthStore } from '../stores/auth'
import { useJobsStore } from '../stores/jobs'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useI18n } from '../composables/useI18n'
import IconGrid from '~icons/mdi/view-grid'
import IconSync from '~icons/mdi/refresh'
import IconAccount from '~icons/mdi/account'

const wm = useWindowManagerStore()
const auth = useAuthStore()
const jobsStore = useJobsStore()
const pendingOps = usePendingOpsStore()
const { t, locale, setLocale } = useI18n()

const emit = defineEmits(['show-account'])

function handleWheel(e) {
  e.preventDefault()
  const wins = wm.windows
  if (wins.length === 0) return

  const activeIdx = wins.findIndex(w => w.id === wm.activeWindowId)
  let nextIdx
  if (e.deltaY > 0) {
    nextIdx = (activeIdx + 1) % wins.length
  } else {
    nextIdx = (activeIdx - 1 + wins.length) % wins.length
  }
  const target = wins[nextIdx]
  if (target.minimized) {
    wm.restoreWindow(target.id)
  } else {
    wm.bringToFront(target.id)
  }
}

function handleClick(win) {
  if (win.minimized) {
    wm.restoreWindow(win.id)
  } else if (wm.activeWindowId === win.id) {
    wm.minimizeWindow(win.id)
  } else {
    wm.bringToFront(win.id)
  }
}

const userMenuOptions = computed(() => {
  const items = [
    { label: `${auth.username}`, key: 'user', disabled: true },
    { type: 'divider' },
    { label: t('account.security'), key: 'account' },
    {
      label: locale.value === 'zh' ? 'English' : '中文',
      key: 'lang',
    },
    { type: 'divider' },
    { label: t('titlebar.sign_out'), key: 'logout' },
  ]
  return items
})

function handleUserMenu(key) {
  if (key === 'logout') auth.logout()
  if (key === 'account') emit('show-account')
  if (key === 'lang') setLocale(locale.value === 'zh' ? 'en' : 'zh')
}

const activeCount = computed(() => jobsStore.activeJobs.length)
</script>

<template>
  <div class="taskbar" @wheel.prevent="handleWheel">
    <div class="taskbar-left">
      <button
        v-for="win in wm.windows"
        :key="win.id"
        class="taskbar-item"
        :class="{
          'taskbar-item--active': !win.minimized && wm.activeWindowId === win.id,
        }"
        :title="win.title"
        @click="handleClick(win)"
      >
        <component :is="win.icon" v-if="win.icon" class="taskbar-item-icon" width="48" height="48" />
      </button>
    </div>

    <div class="taskbar-right">
      <button
        class="taskbar-tray-btn"
        :class="{ 'taskbar-tray-btn--active': jobsStore.panelOpen }"
        :title="t('jobs.title')"
        @click="jobsStore.togglePanel()"
      >
        <IconGrid width="18" height="18" />
        <span v-if="activeCount > 0" class="tray-badge">{{ activeCount }}</span>
      </button>

      <button
        class="taskbar-tray-btn"
        :class="{ 'taskbar-tray-btn--active': pendingOps.showPanel }"
        :title="t('pending.title')"
        @click="pendingOps.showPanel = !pendingOps.showPanel"
      >
        <IconSync width="18" height="18" />
        <span v-if="pendingOps.pendingCount > 0" class="tray-badge tray-badge--warning">{{ pendingOps.pendingCount }}</span>
      </button>

      <NDropdown :options="userMenuOptions" trigger="click" placement="top-end" @select="handleUserMenu">
        <button class="taskbar-tray-btn" :title="auth.username">
          <IconAccount width="18" height="18" />
        </button>
      </NDropdown>
    </div>
  </div>
</template>

<style scoped>
.taskbar {
  height: 64px;
  background: #1b1e20;
  border-top: none;
  display: flex;
  align-items: center;
  padding: 4px 4px;
  padding-bottom: calc(4px + env(safe-area-inset-bottom));
  flex-shrink: 0;
}

.taskbar-left {
  display: flex;
  align-items: center;
  gap: 3px;
}

.taskbar-right {
  margin-left: auto;
  display: flex;
  align-items: center;
  padding-right: 6px;
  gap: 2px;
}

.taskbar-item {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 100%;
  padding: 4px 8px;
  border: none;
  border-radius: 0;
  background: #272b30;
  border-top: 2px solid #3b4248;
  color: var(--breeze-text);
}
.taskbar-item:hover {
  background: #333840;
}

.taskbar-item--active {
  background: #2a7aab;
  border-top-color: #4db8d9;
}
.taskbar-item--active:hover {
  background: #3291c4;
  border-top-color: #5cc8e8;
}

.taskbar-item-icon {
  display: flex;
  align-items: center;
  justify-content: center;
}

.taskbar-tray-btn {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: 0;
  background: transparent;
  color: var(--breeze-text-secondary);
}
.taskbar-tray-btn:hover {
  color: var(--breeze-text);
}
.taskbar-tray-btn--active {
  color: #3daee9;
}

.tray-badge {
  position: absolute;
  top: 2px;
  right: 2px;
  min-width: 14px;
  height: 14px;
  padding: 0 3px;
  border-radius: 7px;
  background: #3daee9;
  color: #fff;
  font-size: 10px;
  font-weight: 600;
  line-height: 14px;
  text-align: center;
}
.tray-badge--warning {
  background: #e6a23c;
}
</style>
