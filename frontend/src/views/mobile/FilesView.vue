<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconChevronLeft, IconMagnify, IconMenu, IconRefresh } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import MobileFileListItem from '../../components/mobile/MobileFileListItem.vue'
import MobileFileGridItem from '../../components/mobile/MobileFileGridItem.vue'
import MobileSelectionBar from '../../components/mobile/MobileSelectionBar.vue'
import MobileFilterChips from '../../components/mobile/MobileFilterChips.vue'
import MobileEmptyState from '../../components/mobile/MobileEmptyState.vue'
import { useI18n } from '../../composables/useI18n'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useMobileUiStore } from '../../stores/mobileUi'
import type { FileListItem } from '../../types'

const fs = useFileSystemStore()
const mobileUi = useMobileUiStore()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const title = computed(() => {
  if (fs.isShared) return t('mobile.files.shared_title')
  if (fs.currentPath === '/' || !fs.currentPath) return t('mobile.nav.files')
  const trimmed = fs.currentPath.replace(/\/$/, '')
  return trimmed.split('/').pop() || t('mobile.nav.files')
})

const subtitle = computed(() => fs.currentPath || '/')
const selectSummary = computed(() => fs.selectMode ? t('mobile.selection.selected', { n: fs.selectedFiles.length }) : '')
const viewOptions = [
  { key: 'list', label: t('mobile.files.list') },
  { key: 'grid', label: t('mobile.files.grid') },
]
const sortOptions = [
  { key: 'name', label: t('mobile.files.sort_name') },
  { key: 'date', label: t('mobile.files.sort_recent') },
  { key: 'size', label: t('mobile.files.sort_size') },
]

onMounted(() => {
  if (fs.tabs.length === 0) {
    fs.createTab()
  }
})

watch(() => route.query.path, (path) => {
  if (typeof path === 'string' && path && path !== fs.currentPath) {
    fs.navigate(path)
  }
}, { immediate: true })

function navigateToPath(path: string, replace = false): void {
  const query = { path }
  if (route.path === '/m/files' && route.query.path === path) {
    if (path !== fs.currentPath) fs.navigate(path)
    return
  }
  router[replace ? 'replace' : 'push']({ path: '/m/files', query })
}

function getParentPath(path: string): string {
  const normalized = path.endsWith('/') ? path.slice(0, -1) : path
  const parent = normalized.slice(0, normalized.lastIndexOf('/')) || '/'
  return parent.endsWith('/') ? parent : parent + '/'
}

function openItem(file: FileListItem): void {
  if (file.is_dir) {
    navigateToPath(file.path)
    return
  }
  mobileUi.trackRecent(file)
  router.push({ path: '/m/preview', query: { path: file.path, shareId: file._shareId || '', name: file._originalName || file.name, from: route.fullPath } })
}

function openMore(file: FileListItem): void {
  fs.selectedFiles = [file.path]
  mobileUi.openFileSheet(file)
}

function openDetails(): void {
  router.push({ path: '/m/path-picker', query: { from: route.fullPath } })
}

function handleBack(): void {
  if (!fs.canGoUp) return
  navigateToPath(getParentPath(fs.currentPath))
}

async function refresh(): Promise<void> {
  await fs.refresh()
}

function cycleSort(next: string): void {
  fs.sortBy = next as typeof fs.sortBy
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="title" :subtitle="selectSummary || subtitle" :show-back="fs.canGoUp" @back="handleBack">
      <template #back>
        <IconChevronLeft width="18" height="18" />
      </template>
      <template #actions>
        <button class="mobile-page-icon-btn" @click="router.push('/m/search')"><IconMagnify width="18" height="18" /></button>
        <button class="mobile-page-icon-btn" @click="refresh"><IconRefresh width="18" height="18" /></button>
        <button class="mobile-page-icon-btn" @click="openDetails"><IconMenu width="18" height="18" /></button>
      </template>
    </MobileTopBar>

    <div class="mobile-page-body">
      <div class="mobile-toolbar-stack">
        <MobileFilterChips :options="viewOptions" :active="mobileUi.fileDisplayMode" @select="mobileUi.setFileDisplayMode" />
        <MobileFilterChips :options="sortOptions" :active="fs.sortBy" @select="cycleSort" />
      </div>

      <MobileEmptyState v-if="fs.loading" :title="t('mobile.files.loading_title')" :body="t('mobile.files.loading_body')" />
      <MobileEmptyState v-else-if="fs.sortedFiles.length === 0" :title="t('mobile.files.empty_title')" :body="t('mobile.files.empty_body')" />
      <div v-else-if="mobileUi.fileDisplayMode === 'list'" class="mobile-file-list">
        <MobileFileListItem
          v-for="file in fs.sortedFiles"
          :key="file.path"
          :file="file"
          :selected="fs.selectedFiles.includes(file.path)"
          :select-mode="fs.selectMode"
          @open="openItem(file)"
          @more="openMore(file)"
          @longpress="fs.enterSelectMode(file.path)"
          @toggle-select="fs.toggleSelect(file.path)"
        />
      </div>
      <div v-else class="mobile-file-grid">
        <MobileFileGridItem
          v-for="file in fs.sortedFiles"
          :key="file.path"
          :file="file"
          :selected="fs.selectedFiles.includes(file.path)"
          @open="openItem(file)"
          @more="openMore(file)"
          @longpress="fs.enterSelectMode(file.path)"
        />
      </div>
    </div>

    <MobileSelectionBar
      v-if="fs.selectMode"
      :count="fs.selectedFiles.length"
      @copy="fs.copySelected()"
      @cut="fs.cutSelected()"
      @delete="fs.deleteSelected()"
      @cancel="fs.exitSelectMode()"
    />
  </section>
</template>

<style lang="scss" scoped>
.mobile-page {
  min-height: 100dvh;
}

.mobile-page-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px 16px 188px;
}

.mobile-file-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.mobile-file-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.mobile-empty {
  padding: 48px 20px;
  text-align: center;
  color: rgba(16, 32, 48, 0.56);
}

.mobile-toolbar-stack {
  position: sticky;
  top: 0;
  z-index: 8;
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: -4px -4px 0;
  padding: 4px;
  background: linear-gradient(180deg, rgba(238, 243, 247, 0.96), rgba(238, 243, 247, 0.82), transparent);
  backdrop-filter: blur(10px);
  border-radius: 18px;
}

.mobile-page-icon-btn {
  width: 40px;
  height: 40px;
  border: none;
  border-radius: 12px;
  background: rgba(16, 32, 48, 0.06);
  color: #102030;
  @include inline-flex-center;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.94);
    background: rgba(16, 32, 48, 0.1);
  }
}

@media (min-width: 560px) {
  .mobile-file-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
</style>
