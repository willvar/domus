<script setup>
import { computed, ref, watch, onUnmounted } from 'vue'
import { useWindowManagerStore } from '../../stores/windowManager'
import { useAuthStore } from '../../stores/auth'
import { useJobsStore } from '../../stores/jobs'
import { useUploadStore } from '../../stores/upload'
import { usePendingOpsStore } from '../../stores/pendingOps'
import { useI18n } from '../../composables/useI18n'
import IconGrid from '~icons/mdi/view-grid'
import IconSync from '~icons/mdi/refresh'
import IconAccount from '~icons/mdi/account'
import IconCog from '~icons/mdi/cog-outline'

const wm = useWindowManagerStore()
const auth = useAuthStore()
const jobsStore = useJobsStore()
const uploadStore = useUploadStore()
const pendingOps = usePendingOpsStore()
const { t, locale, setLocale } = useI18n()

const props = defineProps({
  showPrefs: { type: Boolean, default: false },
})
const emit = defineEmits(['update:showPrefs'])

// Tray panel size is provided by PlasmaShell

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

// --- User menu dropdown ---
const showUserMenu = ref(false)
const userBtnRef = ref(null)
const userMenuRef = ref(null)
const userMenuStyle = ref({})

const userMenuItems = computed(() => [
  { label: auth.user?.display_name || auth.username, key: 'profile' },
  { type: 'divider' },
  { label: locale.value === 'zh' ? 'English' : '中文', key: 'lang' },
  { type: 'divider' },
  { label: t('titlebar.sign_out'), key: 'logout', danger: true },
])

function toggleUserMenu() {
  if (showUserMenu.value) {
    showUserMenu.value = false
    return
  }
  const btn = userBtnRef.value
  if (!btn) return
  const rect = btn.getBoundingClientRect()
  // Position above the button, aligned to right
  showUserMenu.value = true
  // Defer position calculation to next tick when menu is rendered
  requestAnimationFrame(() => {
    const menuEl = userMenuRef.value
    if (!menuEl) return
    const menuRect = menuEl.getBoundingClientRect()
    let left = rect.right - menuRect.width
    let top = rect.top - menuRect.height - 4
    if (left < 4) left = 4
    if (top < 4) top = rect.bottom + 4
    userMenuStyle.value = { left: `${left}px`, top: `${top}px` }
  })
}

function handleUserMenu(key) {
  showUserMenu.value = false
  if (key === 'profile') wm.openProfileApp()
  if (key === 'logout') auth.logout()
  if (key === 'lang') setLocale(locale.value === 'zh' ? 'en' : 'zh')
}

function onUserMenuClickOutside(e) {
  if (userMenuRef.value && !userMenuRef.value.contains(e.target) &&
      userBtnRef.value && !userBtnRef.value.contains(e.target)) {
    showUserMenu.value = false
  }
}

watch(showUserMenu, (val) => {
  if (val) {
    document.addEventListener('mousedown', onUserMenuClickOutside, true)
  } else {
    document.removeEventListener('mousedown', onUserMenuClickOutside, true)
  }
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onUserMenuClickOutside, true)
})

function toggleTasks() {
  const opening = !jobsStore.panelOpen
  jobsStore.panelOpen = opening
  if (opening) {
    pendingOps.showPanel = false
    emit('update:showPrefs', false)
  }
}

function togglePending() {
  const opening = !pendingOps.showPanel
  pendingOps.showPanel = opening
  if (opening) {
    jobsStore.panelOpen = false
    emit('update:showPrefs', false)
  }
}

function togglePrefs() {
  const opening = !props.showPrefs
  emit('update:showPrefs', opening)
  if (opening) {
    jobsStore.panelOpen = false
    pendingOps.showPanel = false
  }
}

const activeCount = computed(() => jobsStore.activeTasks.length + uploadStore.activeUploads.length)
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
        @click="toggleTasks"
      >
        <IconGrid width="22" height="22" />
        <span v-if="activeCount > 0" class="tray-badge">{{ activeCount }}</span>
      </button>

      <button
        class="taskbar-tray-btn"
        :class="{ 'taskbar-tray-btn--active': pendingOps.showPanel }"
        :title="t('pending.title')"
        @click="togglePending"
      >
        <IconSync width="22" height="22" />
        <span v-if="pendingOps.pendingCount > 0" class="tray-badge tray-badge--warning">{{ pendingOps.pendingCount }}</span>
      </button>

      <button
        class="taskbar-tray-btn"
        :class="{ 'taskbar-tray-btn--active': props.showPrefs }"
        :title="t('account.preferences')"
        @click="togglePrefs"
      >
        <IconCog width="22" height="22" />
      </button>

      <button
        ref="userBtnRef"
        class="taskbar-tray-btn"
        :class="{ 'taskbar-tray-btn--active': showUserMenu }"
        :title="auth.username"
        @click="toggleUserMenu"
      >
        <img v-if="auth.user?.avatar_url" :src="auth.user.avatar_url" class="tray-avatar" />
        <IconAccount v-else width="22" height="22" />
      </button>

      <Teleport to="body">
        <Transition name="ctx-menu">
          <div
            v-if="showUserMenu"
            ref="userMenuRef"
            class="plasma-context-menu"
            :style="userMenuStyle"
          >
            <template v-for="(item, i) in userMenuItems" :key="item.key || `div-${i}`">
              <div v-if="item.type === 'divider'" class="ctx-divider" />
              <button
                v-else
                class="ctx-item"
                :class="{ danger: item.danger, disabled: item.disabled }"
                :disabled="item.disabled"
                @click="!item.disabled && handleUserMenu(item.key)"
              >
                {{ item.label }}
              </button>
            </template>
          </div>
        </Transition>
      </Teleport>
    </div>
  </div>
</template>

<style scoped>
.taskbar {
  height: 64px;
  background: #141618;
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
  width: 40px;
  height: 40px;
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

.tray-avatar {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  object-fit: cover;
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

/* ─── Plasma context menu (user menu) ─── */
.plasma-context-menu {
  position: fixed;
  z-index: 10000;
  min-width: 160px;
  padding: 4px 0;
  background: var(--breeze-surface-raised, #31363b);
  border: 1px solid var(--breeze-border, #3b4045);
  border-radius: 4px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
}

.ctx-item {
  display: block;
  width: 100%;
  padding: 5px 16px;
  margin: 0;
  border: none;
  background: none;
  color: var(--breeze-text, #fcfcfc);
  font-size: 14px;
  line-height: 22px;
  text-align: left;
  cursor: default;
}
.ctx-item:hover:not(:disabled) {
  background: var(--breeze-accent, #3daee9);
  color: #fff;
}
.ctx-item.danger:hover:not(:disabled) {
  background: var(--breeze-danger, #da4453);
}
.ctx-item.disabled {
  color: var(--breeze-text-disabled, #505962);
  cursor: default;
}

.ctx-divider {
  height: 1px;
  margin: 4px 8px;
  background: var(--breeze-border, #3b4045);
}

.ctx-menu-enter-active {
  transition: opacity 0.12s ease, transform 0.12s ease;
}
.ctx-menu-leave-active {
  transition: opacity 0.08s ease;
}
.ctx-menu-enter-from {
  opacity: 0;
  transform: scale(0.96);
}
.ctx-menu-leave-to {
  opacity: 0;
}
</style>
