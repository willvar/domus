<script setup>
import { computed, ref, watch, onUnmounted } from 'vue'
import { useWindowManagerStore, FILES_ICON, PROFILE_ICON, KONSOLE_ICON } from '../../stores/windowManager'
import { useTouchHandlers } from '../../composables/useTouch'
import { useI18n } from '../../composables/useI18n'
import { openContextMenu } from '../../composables/useContextMenu'
import { usePreferences } from '../../composables/usePreferences'
import api from '../../composables/useApi'

const wm = useWindowManagerStore()
const { t } = useI18n()
const { prefs } = usePreferences()

function onDesktopContextMenu(e) {
  e.preventDefault()
  openContextMenu(e, null, 'desktop')
}

// --- Wallpaper rendering from preferences ---
const customWallpaperUrl = ref('')
let currentLoadedPath = ''

const wallpaperType = computed(() => prefs.wallpaperType || 'builtin')
const wallpaperFit = computed(() => prefs.wallpaperFit || 'cover')
const wallpaperBuiltinId = computed(() => prefs.wallpaperBuiltinId ?? 0)

const isVideo = computed(() => {
  if (wallpaperType.value !== 'custom') return false
  const ext = (prefs.wallpaperPath || '').split('.').pop()?.toLowerCase() || ''
  return ['mp4', 'webm', 'mov'].includes(ext)
})

const builtinColors = [
  ['#0d1117', '#151d28', '#0f1923', '#1a3a5c', '#1e4a6e', '#3daee9'],
]

const builtinGradients = computed(() => {
  const c = builtinColors[wallpaperBuiltinId.value] || builtinColors[0]
  return { bg: c.slice(0, 3), glow: c[3], spot: c[4], accent: c[5] }
})

// Load custom wallpaper blob
watch(() => prefs.wallpaperPath, async (path) => {
  if (!path || wallpaperType.value !== 'custom') {
    customWallpaperUrl.value = ''
    currentLoadedPath = ''
    return
  }
  if (path === currentLoadedPath) return
  try {
    const res = await api.get(`/user/store/${path}`, { responseType: 'blob' })
    if (res.data?.size > 0) {
      if (customWallpaperUrl.value) URL.revokeObjectURL(customWallpaperUrl.value)
      customWallpaperUrl.value = URL.createObjectURL(res.data)
      currentLoadedPath = path
    }
  } catch {
    customWallpaperUrl.value = ''
    currentLoadedPath = ''
  }
}, { immediate: true })

// Also reload when type changes to custom
watch(wallpaperType, (type) => {
  if (type === 'custom' && prefs.wallpaperPath && prefs.wallpaperPath !== currentLoadedPath) {
    // Trigger the path watcher
    const path = prefs.wallpaperPath
    currentLoadedPath = ''
    prefs.wallpaperPath = path
  }
})

onUnmounted(() => {
  if (customWallpaperUrl.value) URL.revokeObjectURL(customWallpaperUrl.value)
})

const apps = computed(() => [
  {
    id: 'profile',
    label: t('app.profile'),
    icon: PROFILE_ICON,
    action: () => wm.openProfileApp(),
  },
  {
    id: 'files',
    label: t('app.files'),
    icon: FILES_ICON,
    action: () => wm.openFilesApp(),
  },
  {
    id: 'konsole',
    label: t('app.terminal'),
    icon: KONSOLE_ICON,
    action: () => wm.openKonsoleApp(),
  },
])

const appTouchMap = new Map()
function appTouch(app) {
  if (!appTouchMap.has(app.id)) {
    appTouchMap.set(app.id, useTouchHandlers({ onDoubleTap: () => app.action() }))
  }
  return appTouchMap.get(app.id)
}

// Long-press on desktop background for mobile context menu
const desktopTouch = useTouchHandlers({
  onLongPress: (e) => openContextMenu(e, null, 'desktop'),
})
</script>

<template>
  <div
    class="desktop"
    @contextmenu="onDesktopContextMenu"
    @touchstart="desktopTouch.onTouchStart"
    @touchmove="desktopTouch.onTouchMove"
    @touchend="desktopTouch.onTouchEnd"
  >
    <!-- Custom image/video wallpaper -->
    <template v-if="wallpaperType === 'custom' && customWallpaperUrl">
      <video
        v-if="isVideo"
        :src="customWallpaperUrl"
        :style="{ objectFit: wallpaperFit }"
        class="wallpaper wallpaper-media"
        autoplay muted loop playsinline
      />
      <img
        v-else
        :src="customWallpaperUrl"
        :style="{ objectFit: wallpaperFit }"
        class="wallpaper wallpaper-media"
      />
    </template>

    <!-- Built-in SVG wallpaper -->
    <svg v-else class="wallpaper" viewBox="0 0 1920 1080" preserveAspectRatio="xMidYMid slice" xmlns="http://www.w3.org/2000/svg">
      <defs>
        <linearGradient id="bg" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" :stop-color="builtinGradients.bg[0]" />
          <stop offset="50%" :stop-color="builtinGradients.bg[1]" />
          <stop offset="100%" :stop-color="builtinGradients.bg[2]" />
        </linearGradient>
        <linearGradient id="glow1" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" :stop-color="builtinGradients.glow" stop-opacity="0.6" />
          <stop offset="100%" :stop-color="builtinGradients.bg[0]" stop-opacity="0" />
        </linearGradient>
        <radialGradient id="spot1" cx="25%" cy="35%" r="40%">
          <stop offset="0%" :stop-color="builtinGradients.spot" stop-opacity="0.35" />
          <stop offset="100%" stop-color="transparent" />
        </radialGradient>
      </defs>
      <rect width="1920" height="1080" fill="url(#bg)" />
      <rect width="1920" height="1080" fill="url(#spot1)" />
      <path d="M0 700 Q480 580 960 650 T1920 600 L1920 1080 L0 1080Z" fill="url(#glow1)" />
      <line x1="300" y1="0" x2="900" y2="1080" :stroke="builtinGradients.accent" stroke-opacity="0.04" stroke-width="1" />
      <line x1="800" y1="0" x2="1400" y2="1080" :stroke="builtinGradients.accent" stroke-opacity="0.03" stroke-width="1" />
      <line x1="1300" y1="0" x2="1900" y2="1080" :stroke="builtinGradients.accent" stroke-opacity="0.04" stroke-width="1" />
      <circle cx="350" cy="300" r="180" fill="none" :stroke="builtinGradients.accent" stroke-opacity="0.03" stroke-width="0.5" />
      <circle cx="1500" cy="750" r="250" fill="none" :stroke="builtinGradients.accent" stroke-opacity="0.025" stroke-width="0.5" />
    </svg>

    <!-- Desktop icons -->
    <div class="desktop-icons">
      <div
        v-for="app in apps"
        :key="app.id"
        class="desktop-icon"
        @dblclick="app.action()"
        @touchstart="appTouch(app).onTouchStart"
        @touchmove="appTouch(app).onTouchMove"
        @touchend="appTouch(app).onTouchEnd"
      >
        <div class="desktop-icon-img"><component :is="app.icon" width="40" height="40" /></div>
        <span class="desktop-icon-label">{{ app.label }}</span>
      </div>
    </div>

  </div>
</template>

<style scoped>
.desktop {
  position: absolute;
  inset: 0;
  z-index: 0;
  overflow: hidden;
}

.wallpaper {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}

.wallpaper-media {
  object-fit: cover;
}

.desktop-icons {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  flex-wrap: wrap;
  align-content: flex-start;
  gap: 8px;
  padding: 16px;
  height: 100%;
}

.desktop-icon {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  width: 72px;
  padding: 8px 4px;
  border-radius: 6px;
  user-select: none;
}
.desktop-icon:hover {
  background: rgba(255, 255, 255, 0.06);
}

.desktop-icon-img {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--breeze-accent);
}

.desktop-icon-label {
  font-size: 11px;
  color: #dde1e5;
  text-align: center;
  text-shadow: 0 1px 4px rgba(0, 0, 0, 0.8);
  word-break: break-all;
  line-height: 1.3;
}
</style>
