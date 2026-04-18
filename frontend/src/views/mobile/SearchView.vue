<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconChevronLeft } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import MobileFileListItem from '../../components/mobile/MobileFileListItem.vue'
import MobileSearchInput from '../../components/mobile/MobileSearchInput.vue'
import MobileEmptyState from '../../components/mobile/MobileEmptyState.vue'
import MobileSelectionBar from '../../components/mobile/MobileSelectionBar.vue'
import { useI18n } from '../../composables/useI18n'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useMobileUiStore } from '../../stores/mobileUi'
import type { FileListItem } from '../../types'

const fs = useFileSystemStore()
const route = useRoute()
const router = useRouter()
const mobileUi = useMobileUiStore()
const { t } = useI18n()
const query = ref(fs.searchQuery)
const subtitle = computed(() => fs.selectMode ? t('mobile.selection.selected', { n: fs.selectedFiles.length }) : (fs.searchMode ? t('mobile.search.results', { n: fs.searchResults.length }) : t('mobile.search.subtitle')))
let timer: ReturnType<typeof setTimeout> | null = null

watch(query, (value) => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    if (value.trim().length >= 2) {
      fs.performSearch(value.trim())
    } else {
      fs.exitSearch()
    }
  }, 220)
})

onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
})

function handleBack(): void {
  if (fs.selectMode) {
    fs.exitSelectMode()
    return
  }
  fs.exitSearch()
  router.back()
}

function openItem(file: FileListItem): void {
  if (file.is_dir) {
    fs.exitSearch()
    router.push({ path: '/m/files', query: { path: file.path } })
    return
  }
  mobileUi.trackRecent(file)
  router.push({ path: '/m/preview', query: { path: file.path, shareId: file._shareId || '', name: file._originalName || file.name, from: route.fullPath } })
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.search.title')" :subtitle="subtitle" show-back @back="handleBack">
      <template #back><IconChevronLeft width="18" height="18" /></template>
    </MobileTopBar>

    <div class="mobile-page-body mobile-search-body">
      <MobileSearchInput v-model="query" :placeholder="t('mobile.search.placeholder')" @clear="query = ''; fs.exitSearch()" />

      <MobileEmptyState v-if="fs.searchLoading" :title="t('mobile.search.loading_title')" :body="t('mobile.search.loading_body')" />
      <MobileEmptyState v-else-if="!fs.searchMode && !query" :title="t('mobile.search.empty_prompt_title')" :body="t('mobile.search.empty_prompt_body')" />
      <MobileEmptyState v-else-if="fs.searchMode && fs.searchResults.length === 0" :title="t('mobile.search.no_results_title')" :body="t('mobile.search.no_results_body')" />
      <div v-else class="mobile-file-list">
        <MobileFileListItem
          v-for="file in fs.searchResults"
          :key="file.path"
          :file="file"
          :selected="fs.selectedFiles.includes(file.path)"
          :select-mode="fs.selectMode"
          :meta="file.parent || ''"
          @open="openItem(file as FileListItem)"
          @more="mobileUi.openFileSheet(file as FileListItem)"
          @longpress="fs.enterSelectMode(file.path)"
          @toggle-select="fs.toggleSelect(file.path)"
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
  padding: 16px 16px 188px;
}

.mobile-search-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.mobile-search-body :deep(.mobile-search-box) {
  position: sticky;
  top: 0;
  z-index: 8;
}

.mobile-search-body :deep(.mobile-search-box) {
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
}

.mobile-file-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.mobile-empty {
  padding: 40px 20px;
  text-align: center;
  color: rgba(16, 32, 48, 0.56);
}
</style>
