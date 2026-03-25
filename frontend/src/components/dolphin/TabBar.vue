<script setup>
import { ref, watch, nextTick } from 'vue'
import { NIcon } from 'naive-ui'
import { useFileSystemStore } from '../../stores/fileSystem'
import IconFolder from '~icons/mdi/folder'
import IconClose from '~icons/mdi/close'

const fs = useFileSystemStore()
const tabListRef = ref(null)

function tabLabel(tab) {
  if (!tab.path) return 'Home'
  const parts = tab.path.replace(/\/$/, '').split('/')
  return parts[parts.length - 1] || 'Home'
}

function handleMouseDown(e, tab) {
  if (e.button === 1) {
    e.preventDefault()
    fs.closeTab(tab.id)
  }
}

function handleWheel(e) {
  e.preventDefault()
  if (e.deltaY > 0) {
    fs.nextTab()
  } else if (e.deltaY < 0) {
    fs.prevTab()
  }
}

function scrollActiveTabIntoView() {
  nextTick(() => {
    const el = tabListRef.value?.querySelector('.tab.active')
    if (el) el.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' })
  })
}

watch(() => fs.activeTabId, scrollActiveTabIntoView)
watch(() => fs.tabs.length, scrollActiveTabIntoView)

// --- Drag to reorder ---
const draggingId = ref(null)
const targetSlot = ref(-1)  // visual slot index the dragged tab should occupy
let dragIndex = -1           // original index of dragged tab
let dragStartX = 0
let dragStarted = false
let tabWidths = []           // widths captured at drag start
let tabLefts = []            // left positions captured at drag start
const dragTranslateX = ref(0)
const DRAG_THRESHOLD = 5

function onTabDragStart(e, tab) {
  if (e.button !== 0) return
  dragIndex = fs.tabs.findIndex(t => t.id === tab.id)
  dragStartX = e.clientX
  dragStarted = false

  const tabEls = tabListRef.value?.querySelectorAll('.tab')
  if (tabEls) {
    tabWidths = Array.from(tabEls).map(el => el.getBoundingClientRect().width)
    tabLefts = Array.from(tabEls).map(el => el.getBoundingClientRect().left)
  }

  window.addEventListener('mousemove', onTabDragMove)
  window.addEventListener('mouseup', onTabDragEnd)
}

function onTabDragMove(e) {
  if (dragIndex < 0) return
  const dx = e.clientX - dragStartX

  if (!dragStarted) {
    if (Math.abs(dx) < DRAG_THRESHOLD) return
    dragStarted = true
    draggingId.value = fs.tabs[dragIndex].id
    targetSlot.value = dragIndex
  }

  dragTranslateX.value = dx

  // Determine which slot the dragged tab's center is closest to
  const draggedCenter = tabLefts[dragIndex] + tabWidths[dragIndex] / 2 + dx
  let slot = dragIndex
  for (let i = 0; i < tabWidths.length; i++) {
    const slotCenter = tabLefts[i] + tabWidths[i] / 2
    if (i < dragIndex && draggedCenter < slotCenter) {
      slot = i
      break
    }
    if (i > dragIndex && draggedCenter > slotCenter) {
      slot = i
    }
  }
  targetSlot.value = slot
}

function onTabDragEnd() {
  window.removeEventListener('mousemove', onTabDragMove)
  window.removeEventListener('mouseup', onTabDragEnd)

  if (dragStarted && targetSlot.value !== dragIndex && targetSlot.value >= 0) {
    fs.moveTab(dragIndex, targetSlot.value)
  }

  draggingId.value = null
  dragTranslateX.value = 0
  targetSlot.value = -1
  dragIndex = -1
  dragStarted = false
  tabWidths = []
  tabLefts = []
}

function tabStyle(tab) {
  if (!draggingId.value) return {}

  const i = fs.tabs.findIndex(t => t.id === tab.id)

  // The dragged tab follows cursor
  if (tab.id === draggingId.value) {
    return {
      transform: `translateX(${dragTranslateX.value}px)`,
      zIndex: 10,
      position: 'relative',
    }
  }

  // Other tabs shift to make room
  const slot = targetSlot.value
  const src = dragIndex
  const dragW = tabWidths[src]

  let shift = 0
  if (slot < src && i >= slot && i < src) {
    // Dragged left: tabs between [slot, src) shift right by dragged tab width
    shift = dragW
  } else if (slot > src && i > src && i <= slot) {
    // Dragged right: tabs between (src, slot] shift left by dragged tab width
    shift = -dragW
  }

  return {
    transform: shift ? `translateX(${shift}px)` : '',
    transition: 'transform 0.15s ease',
  }
}
</script>

<template>
  <div class="tab-bar">
    <div ref="tabListRef" class="tab-list" @wheel="handleWheel">
      <div
        v-for="tab in fs.tabs"
        :key="tab.id"
        class="tab"
        :class="{
          active: tab.id === fs.activeTabId,
          dragging: draggingId === tab.id,
        }"
        :style="tabStyle(tab)"
        @click="fs.switchTab(tab.id)"
        @mousedown="handleMouseDown($event, tab); onTabDragStart($event, tab)"
      >
        <IconFolder class="tab-icon" width="16" height="16" />
        <span class="tab-label">{{ tabLabel(tab) }}</span>
        <button
          class="tab-close"
          @click.stop="fs.closeTab(tab.id)"
        ><NIcon :size="14"><IconClose /></NIcon></button>
      </div>
    </div>
    <button class="tab-new" @click="fs.createTab()">+</button>
  </div>
</template>

<style scoped>
.tab-bar {
  display: flex;
  align-items: stretch;
  background: var(--breeze-surface);
  border-bottom: 1px solid var(--breeze-border);
  height: 34px;
  padding: 0;
  gap: 0;
  flex-shrink: 0;
}

.tab-list {
  display: flex;
  flex: 1;
  min-width: 0;
  gap: 0;
  overflow-x: auto;
  scrollbar-width: none;
}

.tab-list::-webkit-scrollbar {
  display: none;
}

.tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 10px;
  color: var(--breeze-text-secondary);
  font-size: 12px;
  white-space: nowrap;
  max-width: 180px;
  flex-shrink: 0;
  border-top: 2px solid transparent;
  border-right: 1px solid var(--breeze-border);
  position: relative;
  cursor: default;
}

.tab:hover {
  background: rgba(255, 255, 255, 0.04);
  color: var(--breeze-text);
}

.tab.active {
  background: rgba(255, 255, 255, 0.03);
  color: var(--breeze-text);
  border-top-color: var(--breeze-accent);
}

.tab.dragging {
  opacity: 0.85;
  background: var(--breeze-surface-raised);
}

.tab-icon {
  width: 14px;
  height: 14px;
  fill: var(--icon-folder);
  flex-shrink: 0;
}

.tab-label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.tab-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border: none;
  background: none;
  color: var(--breeze-text-secondary);
  border-radius: 3px;
  font-size: 14px;
  line-height: 1;
  padding: 0;
  flex-shrink: 0;
  opacity: 0.6;
  transition: opacity 0.15s, background 0.15s;
}

.tab:hover .tab-close {
  opacity: 1;
}

.tab-close:hover {
  background: rgba(255, 255, 255, 0.12);
  color: var(--breeze-text);
  opacity: 1;
}

.tab-new {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  border: none;
  background: none;
  color: var(--breeze-text-secondary);
  font-size: 16px;
  flex-shrink: 0;
  border-right: 1px solid var(--breeze-border);
}

.tab-new:hover {
  background: rgba(255, 255, 255, 0.04);
  color: var(--breeze-text);
}

@media (max-width: 767px) {
  .tab-close { width: 44px; height: 44px; padding: 12px; opacity: 1; }
  .tab-new { width: 44px; }
}
</style>
