<script setup>
import { ref, watch, nextTick } from 'vue'
import { NButton, NButtonGroup, NInput, NTooltip, NDropdown } from 'naive-ui'
import IconChevronLeft from '~icons/mdi/chevron-left'
import IconChevronRight from '~icons/mdi/chevron-right'
import IconViewGrid from '~icons/mdi/view-grid-outline'
import IconViewList from '~icons/mdi/view-list-outline'
import IconUpload from '~icons/mdi/upload'
import IconSearch from '~icons/mdi/magnify'
import { useFileSystemStore } from '../stores/fileSystem'
import { useUploadStore } from '../stores/upload'
import { useAuthStore } from '../stores/auth'
import { useI18n } from '../composables/useI18n'

const fs = useFileSystemStore()
const upload = useUploadStore()
const auth = useAuthStore()
const { t } = useI18n()

const searchRef = ref(null)
const fileInputRef = ref(null)

const viewOptions = [
  { label: () => t('toolbar.view_icons'), key: 'icons' },
  { label: () => t('toolbar.view_compact'), key: 'compact' },
  { label: () => t('toolbar.view_details'), key: 'details' },
]


watch(() => fs.focusSearch, (val) => {
  if (val) {
    nextTick(() => {
      searchRef.value?.focus()
      fs.focusSearch = false
    })
  }
})

function handleViewSelect(key) { fs.viewMode = key }

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
</script>

<template>
  <div class="toolbar">
    <div class="toolbar-left">
      <NButtonGroup size="small">
        <NTooltip>
          <template #trigger>
            <NButton :disabled="!fs.canGoBack" quaternary class="nav-btn" @click="fs.goBack">
              <IconChevronLeft width="16" height="16" />
            </NButton>
          </template>
          {{ t('toolbar.back') }}
        </NTooltip>
        <NTooltip>
          <template #trigger>
            <NButton :disabled="!fs.canGoForward" quaternary class="nav-btn" @click="fs.goForward">
              <IconChevronRight width="16" height="16" />
            </NButton>
          </template>
          {{ t('toolbar.forward') }}
        </NTooltip>
      </NButtonGroup>

      <NDropdown :options="viewOptions" trigger="click" @select="handleViewSelect">
        <NButton size="small" quaternary class="nav-btn">
          <component :is="fs.viewMode === 'icons' ? IconViewGrid : IconViewList" width="16" height="16" />
        </NButton>
      </NDropdown>

      <span class="toolbar-sep" />

      <template v-if="auth.canUpload">
        <NTooltip>
          <template #trigger>
            <NButton size="small" quaternary class="nav-btn" @click="triggerUpload">
              <IconUpload width="16" height="16" />
            </NButton>
          </template>
          {{ t('toolbar.upload') }}
        </NTooltip>
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
      <NInput
        ref="searchRef"
        v-model:value="fs.searchQuery"
        :placeholder="t('toolbar.search')"
        size="small"
        clearable
        class="search-input"
      >
        <template #prefix>
          <IconSearch width="14" height="14" />
        </template>
      </NInput>
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
.nav-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--breeze-text-secondary) !important;
}
.nav-btn:hover { color: var(--breeze-text) !important; }
.toolbar-sep {
  width: 1px;
  height: 18px;
  background: var(--breeze-border);
  margin: 0 4px;
}
.search-input { width: 180px; }

@media (max-width: 767px) {
  .search-input { width: 120px; }
  .nav-btn { min-width: 44px; min-height: 44px; }
}
</style>
