<script setup>
import { computed, inject, ref, onMounted, onUnmounted } from 'vue'
import { NDropdown, NDrawer, NDrawerContent } from 'naive-ui'
import { useFileSystemStore } from '../stores/fileSystem'
import { useAuthStore } from '../stores/auth'
import { useI18n } from '../composables/useI18n'
import { useContextMenuState, closeContextMenu } from '../composables/useContextMenu'

const fs = useFileSystemStore()
const auth = useAuthStore()
const { t } = useI18n()

const openTranscodeDialog = inject('openTranscodeDialog', null)

const { show, x, y, targetFile } = useContextMenuState()

const isMobile = ref(window.innerWidth < 768)
function onResize() { isMobile.value = window.innerWidth < 768 }
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

const options = computed(() => {
  const items = []

  if (fs.isTrash) {
    if (targetFile.value) {
      items.push({ label: t('menu.restore'), key: 'restore' })
      items.push({ label: t('menu.permanent_delete'), key: 'delete' })
    } else {
      items.push({ label: t('menu.empty_trash'), key: 'empty_trash' })
      items.push({ type: 'divider' })
      items.push({ label: t('menu.refresh'), key: 'refresh' })
    }
    return items
  }

  if (targetFile.value) {
    items.push({ label: t('menu.open'), key: 'open' })
    if (!targetFile.value.is_dir) {
      items.push({ label: t('menu.download'), key: 'download' })
    }

    if (!targetFile.value.is_dir && auth.canEdit) {
      const ext = (targetFile.value.name || '').split('.').pop()?.toLowerCase() || ''
      const mediaExts = ['mp4','mkv','webm','mov','avi','flv','wmv','m4v','ts','mp3','aac','flac','ogg','wav','wma','m4a','opus','jpg','jpeg','png','webp','bmp','gif','tiff','tif','heic','heif','avif']
      if (mediaExts.includes(ext)) {
        items.push({ label: t('menu.transcode'), key: 'transcode' })
      }
    }

    if (auth.canEdit || auth.canDelete) {
      items.push({ type: 'divider' })
    }
    if (auth.canEdit) {
      items.push({ label: t('menu.copy'), key: 'copy' })
      items.push({ label: t('menu.cut'), key: 'cut' })
      items.push({ label: t('menu.rename'), key: 'rename' })
    }
    if (auth.canDelete) {
      items.push({ label: t('menu.delete'), key: 'delete' })
    }
  } else {
    if (auth.canUpload) {
      items.push({ label: t('menu.new_folder'), key: 'mkdir' })
      items.push({ label: t('menu.upload'), key: 'upload' })
    }
    if (auth.canEdit && fs.clipboard.items.length > 0) {
      if (items.length > 0) items.push({ type: 'divider' })
      items.push({ label: t('menu.paste'), key: 'paste' })
    }
    if (items.length > 0) items.push({ type: 'divider' })
    items.push({ label: t('menu.refresh'), key: 'refresh' })
    items.push({ label: t('menu.select_all'), key: 'selectall' })
  }

  return items
})

// Flat action items (no dividers) for mobile sheet
const actionItems = computed(() => options.value.filter(o => o.type !== 'divider'))

function handleSelect(key) {
  closeContextMenu()
  switch (key) {
    case 'open': fs.openSelected(); break
    case 'download':
      if (targetFile.value) fs.downloadFile(targetFile.value.path)
      break
    case 'copy': fs.copySelected(); break
    case 'cut': fs.cutSelected(); break
    case 'rename': fs.startRename(); break
    case 'delete': fs.deleteSelected(); break
    case 'mkdir': fs.createFolder(); break
    case 'paste': fs.paste(); break
    case 'refresh': fs.refresh(); break
    case 'selectall': fs.selectAll(); break
    case 'restore': fs.restoreSelected(); break
    case 'empty_trash': fs.emptyTrash(); break
    case 'upload':
      document.querySelector('input[type="file"]')?.click()
      break
    case 'transcode':
      if (targetFile.value && openTranscodeDialog) {
        const name = targetFile.value.name || ''
        const ext = name.split('.').pop()?.toLowerCase() || ''
        const videoExts = ['mp4','mkv','webm','mov','avi','flv','wmv','m4v','ts']
        const audioExts = ['mp3','aac','flac','ogg','wav','wma','m4a','opus']
        let mtype = 'image'
        if (videoExts.includes(ext)) mtype = 'video'
        else if (audioExts.includes(ext)) mtype = 'audio'
        openTranscodeDialog(targetFile.value.path, name, mtype)
      }
      break
  }
}
</script>

<template>
  <!-- Desktop: dropdown at x,y -->
  <NDropdown
    v-if="!isMobile"
    :show="show"
    :options="options"
    :x="x"
    :y="y"
    trigger="manual"
    placement="bottom-start"
    @select="handleSelect"
    @clickoutside="closeContextMenu"
  />

  <!-- Mobile: bottom action sheet -->
  <NDrawer
    v-else
    :show="show"
    placement="bottom"
    :height="'auto'"
    :trap-focus="false"
    @update:show="(v) => { if (!v) closeContextMenu() }"
  >
    <NDrawerContent body-content-style="padding: 0">
      <div class="action-sheet">
        <button
          v-for="item in actionItems"
          :key="item.key"
          class="action-sheet-item"
          :class="{ 'danger': item.key === 'delete' || item.key === 'empty_trash' }"
          @click="handleSelect(item.key)"
        >
          {{ item.label }}
        </button>
        <div class="action-sheet-gap" />
        <button class="action-sheet-item cancel" @click="closeContextMenu">
          {{ t('preview.cancel') }}
        </button>
      </div>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.action-sheet {
  display: flex;
  flex-direction: column;
  padding: 8px 0;
  padding-bottom: calc(8px + env(safe-area-inset-bottom));
}
.action-sheet-item {
  display: flex;
  align-items: center;
  min-height: 48px;
  padding: 12px 20px;
  border: none;
  background: none;
  color: var(--breeze-text);
  font-size: 16px;
  text-align: left;
}
.action-sheet-item:active {
  background: var(--toolbar-button-active);
}
.action-sheet-item.danger {
  color: var(--breeze-danger);
}
.action-sheet-item.cancel {
  color: var(--breeze-text-secondary);
  text-align: center;
  justify-content: center;
}
.action-sheet-gap {
  height: 8px;
  background: rgba(0,0,0,0.2);
}
</style>
