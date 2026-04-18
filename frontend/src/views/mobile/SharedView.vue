<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconRefresh } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import MobileFileListItem from '../../components/mobile/MobileFileListItem.vue'
import MobileFilterChips from '../../components/mobile/MobileFilterChips.vue'
import MobileEmptyState from '../../components/mobile/MobileEmptyState.vue'
import api from '../../composables/useApi'
import { useI18n } from '../../composables/useI18n'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useMobileUiStore } from '../../stores/mobileUi'
import type { FileListItem, Share } from '../../types'

const fs = useFileSystemStore()
const route = useRoute()
const router = useRouter()
const mobileUi = useMobileUiStore()
const { t } = useI18n()
const loadingOwned = ref(false)
const ownedShares = ref<Array<Share & { target_username?: string }>>([])

const sharedTabs = [
  { key: 'withMe', label: t('mobile.shared.with_me') },
  { key: 'byMe', label: t('mobile.shared.by_me') },
]

const ownedShareFiles = computed<FileListItem[]>(() => ownedShares.value.map(share => ({
  name: share.file_name,
  path: share.file_path,
  is_dir: false,
  size: share.file_size,
  created_at: share.created_at,
  last_modified: share.created_at,
  content_type: share.content_type,
  _shareDbId: share.id,
  _permission: share.permission,
})))

async function loadShared(): Promise<void> {
  if (mobileUi.sharedTab === 'withMe') {
    if (fs.currentPath !== '__shared__/') {
      await fs.navigate('__shared__/')
    } else {
      await fs.refresh()
    }
    return
  }
  loadingOwned.value = true
  try {
    const { data } = await api.get<Array<Share & { target_username?: string }>>('/file/share/owned')
    ownedShares.value = Array.isArray(data) ? data : []
  } finally {
    loadingOwned.value = false
  }
}

onMounted(loadShared)

function openItem(file: FileListItem): void {
  if (!file._shareId && mobileUi.sharedTab === 'byMe') {
    router.push({ path: '/m/details', query: { path: file.path, name: file.name, from: route.fullPath } })
    return
  }
  mobileUi.trackRecent(file)
  router.push({ path: '/m/preview', query: { path: file.path, shareId: file._shareId || '', name: file._originalName || file.name, from: route.fullPath } })
}

function ownedRecipientLabel(file: FileListItem): string {
  const username = ownedShares.value.find(share => share.id === file._shareDbId)?.target_username
  return username ? t('mobile.shared.to', { name: username }) : ''
}

async function stopOwnedShare(file: FileListItem): Promise<void> {
  if (!file._shareDbId) return
  await api.delete('/file/share/' + file._shareDbId)
  await loadShared()
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.shared.title')" :subtitle="t('mobile.shared.subtitle')">
      <template #actions>
        <button class="mobile-page-icon-btn" @click="loadShared"><IconRefresh width="18" height="18" /></button>
      </template>
    </MobileTopBar>

    <div class="mobile-page-body">
      <MobileFilterChips :options="sharedTabs" :active="mobileUi.sharedTab" @select="mobileUi.setSharedTab($event); loadShared()" />
      <div class="mobile-shared-note">{{ t('mobile.shared.note') }}</div>
      <MobileEmptyState v-if="mobileUi.sharedTab === 'withMe' && fs.loading" :title="t('mobile.shared.loading_with_me_title')" :body="t('mobile.shared.loading_with_me_body')" />
      <MobileEmptyState v-else-if="mobileUi.sharedTab === 'byMe' && loadingOwned" :title="t('mobile.shared.loading_by_me_title')" :body="t('mobile.shared.loading_by_me_body')" />
      <MobileEmptyState v-else-if="mobileUi.sharedTab === 'withMe' && fs.sortedFiles.length === 0" :title="t('mobile.shared.empty_with_me_title')" :body="t('mobile.shared.empty_with_me_body')" />
      <MobileEmptyState v-else-if="mobileUi.sharedTab === 'byMe' && ownedShareFiles.length === 0" :title="t('mobile.shared.empty_by_me_title')" :body="t('mobile.shared.empty_by_me_body')" />
      <div v-else-if="mobileUi.sharedTab === 'withMe'" class="mobile-file-list">
        <MobileFileListItem
          v-for="file in fs.sortedFiles"
          :key="file.path"
          :file="file"
          :meta="file._sharedBy ? t('mobile.shared.from', { name: file._sharedBy }) : ''"
          @open="openItem(file)"
          @more="mobileUi.openFileSheet(file)"
          @longpress="mobileUi.openFileSheet(file)"
          @toggle-select="void 0"
        />
      </div>
      <div v-else class="mobile-file-list">
        <div v-for="file in ownedShareFiles" :key="`${file.path}-${file._shareDbId}`" class="owned-share-card">
          <MobileFileListItem
            :file="file"
            :meta="ownedRecipientLabel(file)"
            @open="openItem(file)"
            @more="mobileUi.openFileSheet(file)"
            @longpress="mobileUi.openFileSheet(file)"
            @toggle-select="void 0"
          />
          <button class="owned-share-stop" @click="stopOwnedShare(file)">{{ t('mobile.action.stop_sharing') }}</button>
        </div>
      </div>
    </div>
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

.mobile-empty,
.mobile-shared-note {
  padding: 20px;
  text-align: center;
  color: rgba(16, 32, 48, 0.56);
}

.mobile-shared-note {
  margin-bottom: 16px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.78);
  text-align: left;
  line-height: 1.6;
}

.mobile-page-icon-btn {
  width: 40px;
  height: 40px;
  border: none;
  border-radius: 12px;
  background: rgba(16, 32, 48, 0.06);
  @include inline-flex-center;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.94);
    background: rgba(16, 32, 48, 0.1);
  }
}

.owned-share-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: transform 0.16s ease, box-shadow 0.16s ease;

  &:active {
    transform: scale(0.995);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.08);
  }
}

.owned-share-stop {
  min-height: 42px;
  border: none;
  border-radius: 14px;
  background: rgba(218, 68, 83, 0.12);
  color: #c23242;
  font-size: 13px;
  font-weight: 700;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.98);
    background: rgba(218, 68, 83, 0.18);
  }
}
</style>
