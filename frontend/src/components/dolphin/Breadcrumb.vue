<script setup>
import { ref, watch, nextTick, onUnmounted } from 'vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useWebSocket } from '../../composables/useWebSocket'
import { useI18n } from '../../composables/useI18n'

const fs = useFileSystemStore()
const ws = useWebSocket()
const { t } = useI18n()
const editing = ref(false)
const editPath = ref('')
const inputRef = ref(null)

// Subdirectory dropdown state
const subDirs = ref([])
const showSubMenu = ref(false)
const subMenuStyle = ref({})
const subMenuRef = ref(null)
const activeSegIndex = ref(-1)

watch(() => fs.focusPathBar, (val) => {
  if (val) {
    startEdit()
    fs.focusPathBar = false
  }
})

function startEdit() {
  editing.value = true
  editPath.value = '/' + (fs.currentPath || '')
  nextTick(() => inputRef.value?.focus())
}

function commitEdit() {
  editing.value = false
  let p = editPath.value.replace(/^\/+/, '')
  if (p !== fs.currentPath) {
    fs.navigate(p)
  }
}

function cancelEdit() {
  editing.value = false
}

async function loadSubDirs(parentPath) {
  try {
    const res = await ws.request('file.list', { path: parentPath })
    const dirs = (res.files || []).filter(f => f.is_dir)
    subDirs.value = dirs.map(d => ({ name: d.name, path: d.path }))
  } catch {
    subDirs.value = []
  }
}

async function toggleSubMenu(e, segIndex) {
  e.stopPropagation()
  if (showSubMenu.value && activeSegIndex.value === segIndex) {
    showSubMenu.value = false
    return
  }
  const parentPath = segIndex === 0 ? '' : fs.pathSegments[segIndex - 1].path
  await loadSubDirs(parentPath)
  activeSegIndex.value = segIndex
  const rect = e.currentTarget.getBoundingClientRect()
  const left = rect.left
  const top = rect.bottom + 2
  subMenuStyle.value = { left: `${left}px`, top: `${top}px` }
  showSubMenu.value = true
}

function selectSubDir(path) {
  showSubMenu.value = false
  fs.navigate(path)
}

function onSubMenuClickOutside(e) {
  if (subMenuRef.value && !subMenuRef.value.contains(e.target)) {
    showSubMenu.value = false
  }
}

watch(showSubMenu, (val) => {
  if (val) {
    document.addEventListener('mousedown', onSubMenuClickOutside, true)
  } else {
    document.removeEventListener('mousedown', onSubMenuClickOutside, true)
  }
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onSubMenuClickOutside, true)
})
</script>

<template>
  <div class="breadcrumb-bar" @click="startEdit">
    <template v-if="fs.searchMode">
      <div class="breadcrumb-segments">
        <span class="breadcrumb-segment search-indicator">
          {{ t('search.results_title') }}: {{ fs.searchQuery }}
        </span>
      </div>
    </template>
    <template v-else-if="editing">
      <input
        ref="inputRef"
        v-model="editPath"
        class="path-input"
        :placeholder="t('breadcrumb.path_placeholder')"
        @keyup.enter="commitEdit"
        @keyup.escape="cancelEdit"
        @blur="commitEdit"
      />
    </template>
    <template v-else-if="fs.isTrash">
      <div class="breadcrumb-segments">
        <span class="breadcrumb-segment" @click.stop="fs.navigate('/')">
          /
        </span>
        <span class="breadcrumb-sep">›</span>
        <span class="breadcrumb-segment active">
          {{ t('places.trash') }}
        </span>
      </div>
    </template>
    <template v-else>
      <div class="breadcrumb-segments">
        <span class="breadcrumb-segment" @click.stop="fs.navigate('/')">
          /
        </span>
        <template v-for="(seg, i) in fs.pathSegments" :key="seg.path">
          <span
            class="breadcrumb-sep"
            :class="{ active: showSubMenu && activeSegIndex === i }"
            @click.stop="toggleSubMenu($event, i)"
          >›</span>
          <span
            class="breadcrumb-segment"
            :class="{ active: i === fs.pathSegments.length - 1 }"
            @click.stop="fs.navigate(seg.path)"
          >
            {{ seg.name }}
          </span>
        </template>
      </div>

      <Teleport to="body">
        <Transition name="ctx-menu">
          <div
            v-if="showSubMenu && subDirs.length > 0"
            ref="subMenuRef"
            class="plasma-dropdown"
            :style="subMenuStyle"
          >
            <button
              v-for="dir in subDirs"
              :key="dir.path"
              class="dropdown-item"
              @click="selectSubDir(dir.path)"
            >
              {{ dir.name }}
            </button>
          </div>
        </Transition>
      </Teleport>
    </template>
  </div>
</template>

<style scoped>
.breadcrumb-bar {
  display: flex;
  align-items: center;
  height: 30px;
  padding: 0 var(--gap-sm);
  background: var(--breeze-surface);
  border-bottom: 1px solid var(--breeze-border);
  flex-shrink: 0;
}

/* ─── Path editing input (Breeze-style) ─── */
.path-input {
  flex: 1;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--breeze-accent, #3daee9);
  border-radius: 3px;
  background: var(--breeze-bg-alt, #202326);
  color: var(--breeze-text, #fcfcfc);
  font-size: 14px;
  outline: none;
}
.path-input::placeholder {
  color: var(--breeze-text-disabled, #505962);
}

/* ─── Breadcrumb segments ─── */
.breadcrumb-segments {
  display: flex;
  align-items: center;
  gap: 0;
  overflow: hidden;
  flex: 1;
}
.breadcrumb-segment {
  padding: 2px 6px;
  border-radius: 3px;
  white-space: nowrap;
  font-size: var(--font-size-sm);
  color: var(--breeze-text-secondary);
  cursor: default;
}
.breadcrumb-segment:hover {
  color: var(--breeze-text);
  background: var(--breeze-hover);
}
.breadcrumb-segment.active {
  color: var(--breeze-text);
  font-weight: 500;
}
.breadcrumb-segment.search-indicator {
  color: var(--breeze-accent);
  font-weight: 500;
}
.breadcrumb-sep {
  color: var(--breeze-text-disabled);
  font-size: 14px;
  padding: 2px 1px;
  border-radius: 3px;
  cursor: default;
}
.breadcrumb-sep:hover,
.breadcrumb-sep.active {
  color: var(--breeze-text-secondary);
  background: var(--breeze-hover);
}

/* ─── Subdirectory dropdown (Plasma style) ─── */
.plasma-dropdown {
  position: fixed;
  z-index: 10000;
  min-width: 140px;
  max-height: 320px;
  overflow-y: auto;
  padding: 4px 0;
  background: var(--breeze-surface-raised, #31363b);
  border: 1px solid var(--breeze-border, #3b4045);
  border-radius: 4px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
}
.dropdown-item {
  display: block;
  width: 100%;
  padding: 5px 16px;
  border: none;
  background: none;
  color: var(--breeze-text, #fcfcfc);
  font-size: 14px;
  line-height: 22px;
  text-align: left;
  cursor: default;
  white-space: nowrap;
}
.dropdown-item:hover {
  background: var(--breeze-accent, #3daee9);
  color: #fff;
}

/* ─── Transition ─── */
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

@media (max-width: 767px) {
  .breadcrumb-bar { height: 44px; }
  .breadcrumb-segment { padding: 8px 12px; font-size: 14px; }
  .breadcrumb-sep { padding: 8px 4px; }
}
</style>
