<script setup lang="ts">
import { ref, watch, nextTick, onUnmounted } from 'vue'
import { IconChevronLeft, IconChevronRight, IconViewGridOutline as IconViewGrid, IconViewListOutline as IconViewList, IconUpload, IconMagnify as IconSearch, IconClose, IconShareVariantOutline as IconShare } from '../../barrels/icons'
import { inject } from 'vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useUploadStore } from '../../stores/upload'
import { useAuthStore } from '../../stores/auth'
import { useI18n } from '../../composables/useI18n'

const fs = useFileSystemStore()
const upload = useUploadStore()
const auth = useAuthStore()
const { t } = useI18n()

const openShareDialog = inject<((path: string) => void) | null>('openShareDialog', null)

const searchRef = ref<HTMLInputElement | null>(null)
const fileInputRef = ref<HTMLInputElement | null>(null)

const canShare = () => {
  if (fs.isTrash || fs.isShared) return false
  if (fs.selectedFiles.length !== 1) return false
  const file = fs.selectedFile
  return file && !file.is_dir
}

function triggerShare() {
  const file = fs.selectedFile
  if (file && openShareDialog) openShareDialog(file.path)
}

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
const viewBtnRef = ref<HTMLButtonElement | null>(null)
const viewMenuRef = ref<HTMLDivElement | null>(null)
const viewMenuStyle = ref<Record<string, string>>({})

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
  let left = rect.left
  // Prevent overflow on mobile
  const menuWidth = 180
  if (left + menuWidth > window.innerWidth) {
    left = window.innerWidth - menuWidth - 8
  }
  viewMenuStyle.value = {
    left: `${Math.max(4, left)}px`,
    top: `${rect.bottom + 2}px`,
  }
  showViewMenu.value = true
}

function selectView(key: string) {
  fs.viewMode = key
  showViewMenu.value = false
}

function onViewClickOutside(e: MouseEvent) {
  if (viewMenuRef.value && !viewMenuRef.value.contains(e.target as Node) &&
      viewBtnRef.value && !viewBtnRef.value.contains(e.target as Node)) {
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

function handleFileSelect(e: Event) {
  const input = e.target as HTMLInputElement
  const files = input.files
  if (files && files.length > 0) {
    upload.uploadFiles(files, fs.currentPath)
  }
  input.value = ''
}

function clearSearch() {
  fs.exitSearch()
  searchRef.value?.focus()
}

function handleSearchKeydown(e: KeyboardEvent) {
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
            <div class="dropdown-divider" />
            <button
              class="dropdown-item"
              :class="{ selected: fs.showHidden }"
              @click="fs.toggleHidden()"
            >
              {{ t('toolbar.show_hidden') }}
            </button>
          </div>
        </Transition>
      </Teleport>

      <span class="toolbar-sep" />

      <template v-if="auth.isLoggedIn">
        <button
          class="nav-btn"
          :title="t('toolbar.upload')"
          @click="triggerUpload"
        >
          <IconUpload width="16" height="16" />
        </button>
        <button
          v-if="canShare()"
          class="nav-btn"
          :title="t('share.title')"
          @click="triggerShare"
        >
          <IconShare width="16" height="16" />
        </button>
        <input
          ref="fileInputRef"
          type="file"
          multiple
          accept="*/*"
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

<style lang="scss" scoped>
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

  &-left,
  &-right {
    display: flex;
    align-items: center;
    gap: 2px;
  }

  &-sep {
    width: 1px;
    height: 18px;
    background: var(--breeze-border);
    margin: 0 4px;
  }
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
  @include flex-center;
  border: none;
  border-radius: 3px;
  background: none;
  color: var(--breeze-text-secondary);
  cursor: default;
  transition: background var(--transition-fast), color var(--transition-fast);

  &:hover:not(:disabled) {
    background: var(--toolbar-button-hover);
    color: var(--breeze-text);
  }

  &:active:not(:disabled),
  &.active {
    background: var(--toolbar-button-active);
    color: var(--breeze-text);
  }

  &:disabled {
    opacity: 0.35;
    cursor: default;
  }

  @include mobile {
    min-width: 44px;
    min-height: 44px;
  }
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

  &:focus-within {
    border-color: var(--breeze-accent, #3daee9);
  }

  @include mobile {
    width: 120px;
  }
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

  &::placeholder {
    color: var(--breeze-text-disabled, #505962);
  }
}

.search-clear {
  flex-shrink: 0;
  @include flex-center;
  width: 18px;
  height: 18px;
  margin-right: 3px;
  border: none;
  border-radius: 50%;
  background: none;
  color: var(--breeze-text-secondary);
  cursor: default;

  &:hover {
    background: var(--toolbar-button-hover);
    color: var(--breeze-text);
  }
}

/* ─── Dropdown transition (shared with context menu) ─── */
@include ctx-menu-transition;
</style>
