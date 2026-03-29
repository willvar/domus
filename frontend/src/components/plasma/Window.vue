<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useWindowManagerStore } from '../../stores/windowManager'
import { historyClose } from '../../composables/useWindowHistory'

const props = defineProps({
  windowId: { type: String, required: true },
  title: { type: String, default: 'Window' },
  icon: { type: [String, Object], default: '' },
})

const emit = defineEmits(['close'])

const wm = useWindowManagerStore()
const windowEl = ref(null)

const win = computed(() => wm.findWindow(props.windowId))

// Animation control — disable transition during drag/resize
const noTransition = ref(true)

// Drag state
let dragging = false
let dragOffsetX = 0
let dragOffsetY = 0

// Resize state
let resizing = false
let resizeEdge = ''
let resizeStartX = 0
let resizeStartY = 0
let resizeStartW = 0
let resizeStartH = 0
let resizeStartWinX = 0
let resizeStartWinY = 0

// Snap/tile preview
const snapPreview = ref(null) // 'left' | 'right' | 'maximize' | null

const SNAP_THRESHOLD = 10
const EDGE_SNAP_THRESHOLD = 8
const MIN_WIDTH = 300
const MIN_HEIGHT = 200

const windowStyle = computed(() => {
  const w = win.value
  if (!w) return { display: 'none' }
  if (w.minimized) return { display: 'none' }
  const transition = noTransition.value ? 'none' : 'left 0.2s ease, top 0.2s ease, width 0.2s ease, height 0.2s ease, border-radius 0.2s ease'
  if (w.maximized) {
    return {
      position: 'absolute',
      left: '0',
      top: '0',
      width: '100%',
      height: '100%',
      zIndex: w.zIndex,
      display: 'flex',
      borderRadius: '0',
      transition,
    }
  }
  return {
    position: 'absolute',
    left: w.x + 'px',
    top: w.y + 'px',
    width: w.width + 'px',
    height: w.height + 'px',
    zIndex: w.zIndex,
    display: 'flex',
    transition,
  }
})

const previewStyle = computed(() => {
  if (!snapPreview.value) return null
  const container = getContainer()
  if (!container) return null
  const rect = container.getBoundingClientRect()
  if (snapPreview.value === 'maximize') {
    return { left: '0', top: '0', width: rect.width + 'px', height: rect.height + 'px' }
  }
  if (snapPreview.value === 'left') {
    return { left: '0', top: '0', width: Math.floor(rect.width / 2) + 'px', height: rect.height + 'px' }
  }
  if (snapPreview.value === 'right') {
    const w = Math.floor(rect.width / 2)
    return { left: (rect.width - w) + 'px', top: '0', width: w + 'px', height: rect.height + 'px' }
  }
  return null
})

function handleMouseDown() {
  wm.bringToFront(props.windowId)
}

function getContainer() {
  return windowEl.value?.parentElement
}

// --- Drag ---
const DRAG_THRESHOLD = 5
let dragPending = false
let dragStartClientX = 0
let dragStartClientY = 0

function startDrag(e) {
  const w = win.value
  if (!w || e.button !== 0) return

  dragStartClientX = e.clientX
  dragStartClientY = e.clientY

  if (w.maximized || w.tiled) {
    // Don't unsnap yet — wait for actual movement
    dragPending = true
    dragging = false
  } else {
    dragOffsetX = e.clientX - w.x
    dragOffsetY = e.clientY - w.y
    dragging = true
    dragPending = false
    noTransition.value = true
  }

  window.addEventListener('mousemove', onDrag)
  window.addEventListener('mouseup', stopDrag)
  e.preventDefault()
}

function unsnapWindow(e) {
  const w = win.value
  const container = getContainer()
  if (!w || !container) return
  const rect = container.getBoundingClientRect()
  const restoreW = w._restoreRect?.width || 800
  const restoreH = w._restoreRect?.height || 600
  const ratioX = (e.clientX - rect.left) / rect.width
  const newX = e.clientX - rect.left - restoreW * ratioX
  const newY = e.clientY - rect.top

  w.maximized = false
  w.tiled = null
  w.x = Math.max(0, newX)
  w.y = Math.max(0, newY)
  w.width = restoreW
  w.height = restoreH
  w._restoreRect = null

  dragOffsetX = e.clientX - rect.left - w.x
  dragOffsetY = e.clientY - rect.top - w.y
  noTransition.value = true
  dragging = true
  dragPending = false
}

function onDrag(e) {
  // If pending, check if moved enough to start actual drag
  if (dragPending) {
    const dx = Math.abs(e.clientX - dragStartClientX)
    const dy = Math.abs(e.clientY - dragStartClientY)
    if (dx < DRAG_THRESHOLD && dy < DRAG_THRESHOLD) return
    unsnapWindow(e)
  }

  if (!dragging || !win.value) return
  const container = getContainer()
  if (!container) return
  const rect = container.getBoundingClientRect()
  const cx = e.clientX - rect.left
  const cy = e.clientY - rect.top

  let newX = cx - dragOffsetX
  let newY = cy - dragOffsetY

  // Edge snapping
  const w = win.value
  if (Math.abs(newX) < EDGE_SNAP_THRESHOLD) newX = 0
  if (Math.abs(newY) < EDGE_SNAP_THRESHOLD) newY = 0
  if (Math.abs(newX + w.width - rect.width) < EDGE_SNAP_THRESHOLD) newX = rect.width - w.width
  if (Math.abs(newY + w.height - rect.height) < EDGE_SNAP_THRESHOLD) newY = rect.height - w.height

  // Clamp so title bar stays accessible
  newX = Math.max(-w.width + 100, Math.min(newX, rect.width - 100))
  newY = Math.max(0, Math.min(newY, rect.height - 40))

  wm.updateWindow(props.windowId, { x: newX, y: newY })

  // Tile/maximize preview based on cursor position at screen edges
  if (cy <= SNAP_THRESHOLD) {
    snapPreview.value = 'maximize'
  } else if (cx <= SNAP_THRESHOLD) {
    snapPreview.value = 'left'
  } else if (cx >= rect.width - SNAP_THRESHOLD) {
    snapPreview.value = 'right'
  } else {
    snapPreview.value = null
  }
}

// --- Touch drag ---
function startDragTouch(e) {
  const w = win.value
  if (!w || e.touches.length !== 1) return
  const t = e.touches[0]

  dragStartClientX = t.clientX
  dragStartClientY = t.clientY

  if (w.maximized || w.tiled) {
    dragPending = true
    dragging = false
  } else {
    dragOffsetX = t.clientX - w.x
    dragOffsetY = t.clientY - w.y
    dragging = true
    dragPending = false
    noTransition.value = true
  }

  window.addEventListener('touchmove', onDragTouch, { passive: false })
  window.addEventListener('touchend', stopDragTouch)
}

function onDragTouch(e) {
  const t = e.touches[0]
  // Reuse onDrag logic with a fake mouse-like event
  onDrag({ clientX: t.clientX, clientY: t.clientY, preventDefault() {} })
  e.preventDefault()
}

function stopDragTouch() {
  window.removeEventListener('touchmove', onDragTouch)
  window.removeEventListener('touchend', stopDragTouch)
  stopDragCleanup()
}

function stopDragCleanup() {
  if (dragPending) {
    dragPending = false
    return
  }
  if (!dragging) return
  dragging = false

  // Apply snap action with animation
  if (snapPreview.value) {
    noTransition.value = false
    const container = getContainer()
    if (container) {
      const rect = container.getBoundingClientRect()
      if (snapPreview.value === 'maximize') {
        wm.maximizeWindow(props.windowId)
      } else {
        wm.tileWindow(props.windowId, snapPreview.value, rect.width, rect.height)
      }
    }
    snapPreview.value = null
  } else {
    noTransition.value = false
  }
}

function stopDrag() {
  window.removeEventListener('mousemove', onDrag)
  window.removeEventListener('mouseup', stopDrag)
  stopDragCleanup()
}

// --- Resize (all edges) ---
function startResize(edge, e) {
  if (win.value?.maximized) return
  if (e.button !== 0) return
  resizing = true
  noTransition.value = true
  resizeEdge = edge
  resizeStartX = e.clientX
  resizeStartY = e.clientY
  resizeStartW = win.value.width
  resizeStartH = win.value.height
  resizeStartWinX = win.value.x
  resizeStartWinY = win.value.y
  // Clear tiled state on manual resize
  if (win.value.tiled) {
    win.value.tiled = null
    win.value._restoreRect = null
  }
  window.addEventListener('mousemove', onResize)
  window.addEventListener('mouseup', stopResize)
  e.preventDefault()
}

function onResize(e) {
  if (!resizing || !win.value) return
  const dx = e.clientX - resizeStartX
  const dy = e.clientY - resizeStartY
  const edge = resizeEdge
  const updates = {}

  if (edge.includes('e')) {
    updates.width = Math.max(MIN_WIDTH, resizeStartW + dx)
  }
  if (edge.includes('w')) {
    const newW = Math.max(MIN_WIDTH, resizeStartW - dx)
    updates.width = newW
    updates.x = resizeStartWinX + (resizeStartW - newW)
  }
  if (edge.includes('s')) {
    updates.height = Math.max(MIN_HEIGHT, resizeStartH + dy)
  }
  if (edge.includes('n')) {
    const newH = Math.max(MIN_HEIGHT, resizeStartH - dy)
    updates.height = newH
    updates.y = resizeStartWinY + (resizeStartH - newH)
  }

  wm.updateWindow(props.windowId, updates)
}

function stopResize() {
  resizing = false
  noTransition.value = false
  window.removeEventListener('mousemove', onResize)
  window.removeEventListener('mouseup', stopResize)
}

// --- Actions ---
function minimize() {
  wm.minimizeWindow(props.windowId)
}

function toggleMaximize() {
  wm.toggleMaximize(props.windowId)
}

function close() {
  emit('close')
  historyClose(props.windowId)
}

function handleTitleDblClick() {
  toggleMaximize()
}

onMounted(() => {
  // Disable transition for initial positioning
  noTransition.value = true
  const w = win.value
  if (w) {
    const container = getContainer()
    if (container) {
      const rect = container.getBoundingClientRect()
      if (rect.width < 768) {
        // Mobile: always maximize
        w.maximized = true
      } else if (w.x === -1 && w.y === -1) {
        const width = Math.min(w.width, rect.width)
        const height = Math.min(w.height, rect.height)
        const x = Math.max(0, (rect.width - width) / 2)
        const y = Math.max(0, (rect.height - height) / 2)
        wm.updateWindow(props.windowId, { x, y, width, height })
      }
    }
  }
  // Re-enable transition after first frame
  requestAnimationFrame(() => {
    noTransition.value = false
  })
})

onUnmounted(() => {
  stopDrag()
  stopResize()
})
</script>

<template>
  <div
    v-if="win"
    ref="windowEl"
    class="plasma-window"
    :class="{ 'plasma-window--maximized': win.maximized }"
    :style="windowStyle"
    @mousedown="handleMouseDown"
  >
    <!-- Resize edges -->
    <template v-if="!win.maximized">
      <div class="resize-edge resize-n" @mousedown.stop="startResize('n', $event)" />
      <div class="resize-edge resize-s" @mousedown.stop="startResize('s', $event)" />
      <div class="resize-edge resize-w" @mousedown.stop="startResize('w', $event)" />
      <div class="resize-edge resize-e" @mousedown.stop="startResize('e', $event)" />
      <div class="resize-edge resize-nw" @mousedown.stop="startResize('nw', $event)" />
      <div class="resize-edge resize-ne" @mousedown.stop="startResize('ne', $event)" />
      <div class="resize-edge resize-sw" @mousedown.stop="startResize('sw', $event)" />
      <div class="resize-edge resize-se" @mousedown.stop="startResize('se', $event)" />
    </template>

    <!-- Title Bar -->
    <div
      class="plasma-titlebar"
      @mousedown="startDrag"
      @touchstart="startDragTouch"
      @dblclick="handleTitleDblClick"
    >
      <component :is="icon" v-if="icon" class="plasma-titlebar-icon" width="20" height="20" />
      <span class="plasma-titlebar-title">{{ title }}</span>
      <div class="plasma-titlebar-buttons">
        <button class="plasma-btn plasma-btn-minimize" title="最小化" @click.stop="minimize">
          <svg width="18" height="18" viewBox="0 0 18 18"><polyline points="4,7 9,12 14,7" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>
        </button>
        <button class="plasma-btn plasma-btn-maximize" :title="win.maximized ? '还原' : '最大化'" @click.stop="toggleMaximize">
          <svg v-if="!win.maximized" width="18" height="18" viewBox="0 0 18 18"><polyline points="4,11 9,6 14,11" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>
          <svg v-else width="18" height="18" viewBox="0 0 18 18"><rect x="5" y="5" width="8" height="8" rx="0.5" transform="rotate(45 9 9)" fill="none" stroke="currentColor" stroke-width="1.3" /></svg>
        </button>
        <button class="plasma-btn plasma-btn-close" title="关闭" @click.stop="close">
          <svg width="18" height="18" viewBox="0 0 18 18"><line x1="5" y1="5" x2="13" y2="13" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" /><line x1="13" y1="5" x2="5" y2="13" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" /></svg>
        </button>
      </div>
    </div>

    <!-- Content -->
    <div class="plasma-window-body">
      <slot />
    </div>
  </div>

  <!-- Snap/tile preview overlay (rendered as sibling, outside window) -->
  <Transition name="snap-fade">
    <div v-if="snapPreview && previewStyle" class="snap-preview" :style="previewStyle" />
  </Transition>
</template>

<style scoped>
.plasma-window {
  flex-direction: column;
  background: var(--breeze-surface);
  border: 1px solid var(--breeze-border);
  border-radius: 6px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5), 0 2px 8px rgba(0, 0, 0, 0.3);
}

.plasma-titlebar {
  display: flex;
  align-items: center;
  height: 40px;
  padding: 0 10px;
  background: var(--breeze-surface-raised);
  border-bottom: 1px solid var(--breeze-border);
  user-select: none;
  flex-shrink: 0;
  gap: 8px;
}

.plasma-titlebar-icon {
  display: flex;
  align-items: center;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
}

.plasma-titlebar-title {
  flex: 1;
  font-size: 13px;
  color: var(--breeze-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plasma-titlebar-buttons {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.plasma-btn {
  width: 26px;
  height: 26px;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: #fcfcfc;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  transition: background 0.15s, color 0.15s;
}
.plasma-btn:hover {
  background: #fcfcfc;
  color: var(--breeze-surface-raised);
}
.plasma-btn-close:hover {
  background: #da4453;
  color: var(--breeze-surface-raised);
}

.plasma-window-body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  border-radius: 0 0 6px 6px;
}
.plasma-window--maximized .plasma-window-body {
  border-radius: 0;
}

/* --- Resize edges --- */
.resize-edge {
  position: absolute;
  z-index: 10;
}
.resize-n { top: -4px; left: 8px; right: 8px; height: 8px; cursor: ns-resize; }
.resize-s { bottom: -4px; left: 8px; right: 8px; height: 8px; cursor: ns-resize; }
.resize-w { left: -4px; top: 8px; bottom: 8px; width: 8px; cursor: ew-resize; }
.resize-e { right: -4px; top: 8px; bottom: 8px; width: 8px; cursor: ew-resize; }
.resize-nw { top: -4px; left: -4px; width: 12px; height: 12px; cursor: nwse-resize; }
.resize-ne { top: -4px; right: -4px; width: 12px; height: 12px; cursor: nesw-resize; }
.resize-sw { bottom: -4px; left: -4px; width: 12px; height: 12px; cursor: nesw-resize; }
.resize-se { bottom: -4px; right: -4px; width: 12px; height: 12px; cursor: nwse-resize; }

/* --- Snap preview --- */
.snap-preview {
  position: absolute;
  z-index: 9999;
  background: rgba(61, 174, 233, 0.2);
  border: 2px solid var(--breeze-accent);
  border-radius: 6px;
  pointer-events: none;
}
.snap-fade-enter-active { transition: opacity 0.15s ease; }
.snap-fade-leave-active { transition: opacity 0.1s ease; }
.snap-fade-enter-from, .snap-fade-leave-to { opacity: 0; }
</style>
