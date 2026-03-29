<script setup>
import { ref, nextTick } from 'vue'
import IconClose from '~icons/mdi/close'
import IconConsole from '~icons/mdi/console'
import Terminal from './Terminal.vue'

const props = defineProps({
  initialCwd: { type: String, default: '' },
})

const emit = defineEmits(['exit'])

let tabCounter = 0

function makeTab() {
  return { id: ++tabCounter, label: `Shell ${tabCounter}` }
}

const tabs = ref([makeTab()])
const activeTabId = ref(tabs.value[0].id)
const terminalRefs = ref({})

function setTerminalRef(id, el) {
  if (el) terminalRefs.value[id] = el
  else delete terminalRefs.value[id]
}

function addTab() {
  const tab = makeTab()
  tabs.value.push(tab)
  activeTabId.value = tab.id
}

function closeTab(id, fromExit = false) {
  const idx = tabs.value.findIndex(t => t.id === id)
  if (idx < 0) return

  // Last tab: close button creates new tab, exit/ctrl+d closes the whole terminal
  if (tabs.value.length === 1) {
    if (fromExit) {
      tabs.value.splice(idx, 1)
      delete terminalRefs.value[id]
      emit('exit')
    } else {
      tabs.value.splice(idx, 1)
      delete terminalRefs.value[id]
      addTab()
    }
    return
  }

  if (activeTabId.value === id) {
    const newIdx = Math.min(idx, tabs.value.length - 2)
    activeTabId.value = tabs.value[newIdx === idx ? idx + 1 : newIdx].id
  }
  tabs.value.splice(idx, 1)
  delete terminalRefs.value[id]
}

function switchTab(id) {
  activeTabId.value = id
  nextTick(() => {
    const term = terminalRefs.value[id]
    if (term && term.refit) term.refit()
  })
}

function handleTabExit(tabId) {
  closeTab(tabId, true)
}

function handleWheel(e) {
  e.preventDefault()
  const idx = tabs.value.findIndex(t => t.id === activeTabId.value)
  if (e.deltaY > 0 && idx < tabs.value.length - 1) {
    switchTab(tabs.value[idx + 1].id)
  } else if (e.deltaY < 0 && idx > 0) {
    switchTab(tabs.value[idx - 1].id)
  }
}

function handleMiddleClick(e, tab) {
  if (e.button === 1) {
    e.preventDefault()
    closeTab(tab.id)
  }
}
</script>

<template>
  <div class="konsole-tabs-container">
    <div class="konsole-tab-bar">
      <div class="konsole-tab-list" @wheel="handleWheel">
        <div
          v-for="tab in tabs"
          :key="tab.id"
          class="konsole-tab"
          :class="{ active: tab.id === activeTabId }"
          @click="switchTab(tab.id)"
          @mousedown="handleMiddleClick($event, tab)"
        >
          <IconConsole class="konsole-tab-icon" width="14" height="14" />
          <span class="konsole-tab-label">{{ tab.label }}</span>
          <button
            class="konsole-tab-close"
            @click.stop="closeTab(tab.id)"
          ><IconClose width="14" height="14" /></button>
        </div>
      </div>
      <button class="konsole-tab-new" @click="addTab">+</button>
    </div>
    <div class="konsole-tab-content">
      <div
        v-for="tab in tabs"
        :key="tab.id"
        class="konsole-tab-pane"
        :class="{ active: tab.id === activeTabId }"
      >
        <Terminal
          :ref="(el) => setTerminalRef(tab.id, el)"
          :initial-cwd="initialCwd"
          @exit="handleTabExit(tab.id)"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.konsole-tabs-container {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 0;
}

.konsole-tab-bar {
  display: flex;
  align-items: stretch;
  background: #151718;
  border-bottom: 1px solid #2a2d30;
  height: 32px;
  flex-shrink: 0;
}

.konsole-tab-list {
  display: flex;
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
}
.konsole-tab-list::-webkit-scrollbar { display: none; }

.konsole-tab {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 0 10px;
  color: #8c9099;
  font-size: 13px;
  white-space: nowrap;
  max-width: 160px;
  flex-shrink: 0;
  border-right: 1px solid #2a2d30;
  cursor: default;
  transition: background 0.12s;
}

.konsole-tab:hover {
  background: rgba(255, 255, 255, 0.04);
  color: #c8ccd0;
}

.konsole-tab.active {
  background: #1b1e20;
  color: #e0e0e0;
  border-top: 2px solid var(--breeze-accent, #3daee9);
}

.konsole-tab-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  color: inherit;
}

.konsole-tab-label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.konsole-tab-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border: none;
  background: none;
  color: #8c9099;
  border-radius: 3px;
  padding: 0;
  flex-shrink: 0;
  opacity: 0;
  transition: opacity 0.12s, background 0.12s;
}

.konsole-tab:hover .konsole-tab-close { opacity: 1; }
.konsole-tab-close:hover {
  background: rgba(255, 255, 255, 0.12);
  color: #e0e0e0;
  opacity: 1;
}

.konsole-tab-new {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  border: none;
  background: none;
  color: #8c9099;
  font-size: 18px;
  flex-shrink: 0;
}
.konsole-tab-new:hover {
  background: rgba(255, 255, 255, 0.04);
  color: #e0e0e0;
}

.konsole-tab-content {
  flex: 1;
  min-height: 0;
  position: relative;
}

.konsole-tab-pane {
  position: absolute;
  inset: 0;
  visibility: hidden;
  pointer-events: none;
}

.konsole-tab-pane.active {
  visibility: visible;
  pointer-events: auto;
}

@media (max-width: 767px) {
  .konsole-tab { padding: 0 6px; }
  .konsole-tab-close { opacity: 0.6; width: 20px; height: 20px; }
  .konsole-tab-new { width: 36px; }
}
</style>
