<script setup>
import { ref, watch, nextTick, onUnmounted } from 'vue'
import IconChevronLeft from '~icons/mdi/chevron-left'
import IconChevronRight from '~icons/mdi/chevron-right'
import IconViewGrid from '~icons/mdi/view-grid-outline'
import IconViewList from '~icons/mdi/view-list-outline'
import IconUpload from '~icons/mdi/upload'
import IconSearch from '~icons/mdi/magnify'
import IconClose from '~icons/mdi/close'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useUploadStore } from '../../stores/upload'
import { useAuthStore } from '../../stores/auth'
import { useI18n } from '../../composables/useI18n'

const fs = useFileSystemStore()
const upload = useUploadStore()
const auth = useAuthStore()
const { t } = useI18n()

const searchRef = ref(null)
const fileInputRef = ref(null)

watch(() => fs.focusSearch, (val) => {
  if (val) {
    nextTick(() => {
      searchRef.value?.focus()
      fs.focusSearch = false
    })
  }
})

// View mode dropdown
const showViewMenu = ref(false)
const viewBtnRef = ref(null)
const viewMenuRef = ref(null)
const viewMenuStyle = ref({})

const viewOptions = [
  { key: 'icons', labelKey: 'toolbar.view_icons' },
  { key: 'compact', labelKey: 'toolbar.view_compact' },
  { key: 'details', labelKey: 'toolbar.view_details' },
]

function toggleViewMenu() {
  if (showViewMenu.value) {
    showViewMenu.value = false
    return
  }
  const btn = viewBtnRef.value
  if (!btn) return
  const rect = btn.getBoundingClientRect()
  viewMenuStyle.value = {
    left: `${rect.left}px`,
    top: `${rect.bottom + 2}px`,
  }
  showViewMenu.value = true
}

function selectView(key) {
  fs.viewMode = key
  showViewMenu.value = false
}

function onViewClickOutside(e) {
  if (viewMenuRef.value && !viewMenuRef.value.contains(e.target) &&
      viewBtnRef.value && !viewBtnRef.value.contains(e.target)) {
    showViewMenu.value = false
  }
}

watch(showViewMenu, (val) => {
  if (val) {
    document.addEventListener('mousedown', onViewClickOutside, true)
  } else {
    document.removeEventListener('mousedown', onViewClickOutside, true)
  }
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onViewClickOutside, true)
})

function triggerUpload() {
  fileInputRef.value?.click()
}

function handleFileSelect(e) {
  const files = Array.from(e.target.files)
  e.target.value = ''
  if (files.length > 0) {
    upload.uploadFiles(files, fs.currentPath)
  }
}

function clearSearch() {
  fs.exitSearch()
  searchRef.value?.focus()
}

function handleSearchKeydown(e) {
  if (e.key === 'Enter' && fs.searchQuery.trim().length >= 2) {
    fs.performSearch(fs.searchQuery.trim())
  }
  if (e.key === 'Escape') {
    fs.exitSearch()
  }
}
</script>

<template>
  <div class="toolbar">
    <div class="toolbar-left">
      <div class="btn-group">
        <button
          class="nav-btn"
          :disabled="!fs.canGoBack"
          :title="t('toolbar.back')"
          @click="fs.goBack"
        >
          <IconChevronLeft width="16" height="16" />
        </button>
        <button
          class="nav-btn"
          :disabled="!fs.canGoForward"
          :title="t('toolbar.forward')"
          @click="fs.goForward"
        >
          <IconChevronRight width="16" height="16" />
        </button>
      </div>

      <button
        ref="viewBtnRef"
        class="nav-btn"
        :class="{ active: showViewMenu }"
        :title="t(`toolbar.view_${fs.viewMode}`)"
        @click="toggleViewMenu"
      >
        <component :is="fs.viewMode === 'icons' ? IconViewGrid : IconViewList" width="16" height="16" />
      </button>

      <Teleport to="body">
        <Transition name="ctx-menu">
          <div
            v-if="showViewMenu"
            ref="viewMenuRef"
            class="plasma-dropdown"
            :style="viewMenuStyle"
          >
            <button
              v-for="opt in viewOptions"
              :key="opt.key"
              class="dropdown-item"
              :class="{ selected: fs.viewMode === opt.key }"
              @click="selectView(opt.key)"
            >
              {{ t(opt.labelKey) }}
            </button>
          </div>
        </Transition>
      </Teleport>

      <span class="toolbar-sep" />

      <template v-if="auth.canUpload">
        <button
          class="nav-btn"
          :title="t('toolbar.upload')"
          @click="triggerUpload"
        >
          <IconUpload width="16" height="16" />
        </button>
        <input
          ref="fileInputRef"
          type="file"
          multiple
          style="display: none"
          @change="handleFileSelect"
        />
      </template>
    </div>

    <div class="toolbar-right">
      <div class="search-box">
        <IconSearch class="search-icon" width="14" height="14" />
        <input
          ref="searchRef"
          v-model="fs.searchQuery"
          :placeholder="t('toolbar.search')"
          class="search-input"
          type="text"
          @keydown="handleSearchKeydown"
        />
        <button
          v-if="fs.searchQuery"
          class="search-clear"
          @click="clearSearch"
        >
          <IconClose width="12" height="12" />
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 36px;
  padding: 0 var(--gap-sm);
  background: var(--toolbar-bg);
  border-bottom: 1px solid var(--breeze-border);
  gap: var(--gap-xs);
  flex-shrink: 0;
}
.toolbar-left, .toolbar-right {
  display: flex;
  align-items: center;
  gap: 2px;
}

/* ─── Button group ─── */
.btn-group {
  display: flex;
  align-items: center;
  gap: 0;
}

/* ─── Toolbar button (Dolphin-style flat icon button) ─── */
.nav-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 3px;
  background: none;
  color: var(--breeze-text-secondary);
  cursor: default;
  transition: background var(--transition-fast), color var(--transition-fast);
}
.nav-btn:hover:not(:disabled) {
  background: var(--toolbar-button-hover);
  color: var(--breeze-text);
}
.nav-btn:active:not(:disabled),
.nav-btn.active {
  background: var(--toolbar-button-active);
  color: var(--breeze-text);
}
.nav-btn:disabled {
  opacity: 0.35;
  cursor: default;
}

/* ─── Separator ─── */
.toolbar-sep {
  width: 1px;
  height: 18px;
  background: var(--breeze-border);
  margin: 0 4px;
}

/* ─── View mode dropdown (Plasma style) ─── */
.plasma-dropdown {
  position: fixed;
  z-index: 10000;
  min-width: 120px;
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
}
.dropdown-item:hover {
  background: var(--breeze-accent, #3daee9);
  color: #fff;
}
.dropdown-item.selected {
  color: var(--breeze-accent, #3daee9);
}
.dropdown-item.selected:hover {
  color: #fff;
}

/* ─── Search box (Breeze-style input) ─── */
.search-box {
  position: relative;
  display: flex;
  align-items: center;
  width: 180px;
  height: 26px;
  background: var(--breeze-bg-alt, #202326);
  border: 1px solid var(--breeze-border, #3b4045);
  border-radius: 3px;
  transition: border-color var(--transition-fast);
}
.search-box:focus-within {
  border-color: var(--breeze-accent, #3daee9);
}
.search-icon {
  flex-shrink: 0;
  margin-left: 6px;
  color: var(--breeze-text-secondary);
  pointer-events: none;
}
.search-input {
  flex: 1;
  width: 100%;
  height: 100%;
  padding: 0 4px;
  border: none;
  background: none;
  color: var(--breeze-text, #fcfcfc);
  font-size: 13px;
  outline: none;
}
.search-input::placeholder {
  color: var(--breeze-text-disabled, #505962);
}
.search-clear {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  margin-right: 3px;
  border: none;
  border-radius: 50%;
  background: none;
  color: var(--breeze-text-secondary);
  cursor: default;
}
.search-clear:hover {
  background: var(--toolbar-button-hover);
  color: var(--breeze-text);
}

/* ─── Dropdown transition (shared with context menu) ─── */
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
  .search-box { width: 120px; }
  .nav-btn { min-width: 44px; min-height: 44px; }
}
</style>
