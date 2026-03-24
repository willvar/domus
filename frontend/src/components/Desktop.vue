<script setup>
import { useWindowManagerStore, FILES_ICON } from '../stores/windowManager'
import { useTouchHandlers } from '../composables/useTouch'

const wm = useWindowManagerStore()

const apps = [
  {
    id: 'files',
    label: '文件',
    icon: FILES_ICON,
    action: () => wm.openFilesApp(),
  },
]

const appTouchMap = new Map()
function appTouch(app) {
  if (!appTouchMap.has(app.id)) {
    appTouchMap.set(app.id, useTouchHandlers({ onDoubleTap: () => app.action() }))
  }
  return appTouchMap.get(app.id)
}
</script>

<template>
  <div class="desktop">
    <!-- SVG Wallpaper -->
    <svg class="wallpaper" viewBox="0 0 1920 1080" preserveAspectRatio="xMidYMid slice" xmlns="http://www.w3.org/2000/svg">
      <defs>
        <linearGradient id="bg" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stop-color="#0d1117" />
          <stop offset="50%" stop-color="#151d28" />
          <stop offset="100%" stop-color="#0f1923" />
        </linearGradient>
        <linearGradient id="glow1" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stop-color="#1a3a5c" stop-opacity="0.6" />
          <stop offset="100%" stop-color="#0d1f33" stop-opacity="0" />
        </linearGradient>
        <linearGradient id="glow2" x1="100%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="#1e4a6e" stop-opacity="0.4" />
          <stop offset="100%" stop-color="#0a1520" stop-opacity="0" />
        </linearGradient>
        <radialGradient id="spot1" cx="25%" cy="35%" r="40%">
          <stop offset="0%" stop-color="#1a3f5f" stop-opacity="0.35" />
          <stop offset="100%" stop-color="transparent" />
        </radialGradient>
        <radialGradient id="spot2" cx="75%" cy="65%" r="35%">
          <stop offset="0%" stop-color="#163050" stop-opacity="0.3" />
          <stop offset="100%" stop-color="transparent" />
        </radialGradient>
      </defs>
      <rect width="1920" height="1080" fill="url(#bg)" />
      <rect width="1920" height="1080" fill="url(#spot1)" />
      <rect width="1920" height="1080" fill="url(#spot2)" />
      <!-- Subtle geometric shapes -->
      <path d="M0 700 Q480 580 960 650 T1920 600 L1920 1080 L0 1080Z" fill="url(#glow1)" />
      <path d="M1920 200 Q1440 350 960 280 T0 350 L0 0 L1920 0Z" fill="url(#glow2)" />
      <!-- Faint accent lines -->
      <line x1="300" y1="0" x2="900" y2="1080" stroke="#3daee9" stroke-opacity="0.04" stroke-width="1" />
      <line x1="800" y1="0" x2="1400" y2="1080" stroke="#3daee9" stroke-opacity="0.03" stroke-width="1" />
      <line x1="1300" y1="0" x2="1900" y2="1080" stroke="#3daee9" stroke-opacity="0.04" stroke-width="1" />
      <!-- Subtle circles -->
      <circle cx="350" cy="300" r="180" fill="none" stroke="#3daee9" stroke-opacity="0.03" stroke-width="0.5" />
      <circle cx="1500" cy="750" r="250" fill="none" stroke="#3daee9" stroke-opacity="0.025" stroke-width="0.5" />
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
