<script setup>
import { computed, inject, ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import BDrawer from '../breeze/BDrawer.vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useAuthStore } from '../../stores/auth'
import { useI18n } from '../../composables/useI18n'
import { useContextMenuState, closeContextMenu, suppressNextContextMenu } from '../../composables/useContextMenu'

const fs = useFileSystemStore()
const auth = useAuthStore()
const { t } = useI18n()

const openTranscodeDialog = inject('openTranscodeDialog', null)

const { show, x, y, targetFile, context } = useContextMenuState()

const isMobile = ref(window.innerWidth < 768)
function onResize() { isMobile.value = window.innerWidth < 768 }
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

const menuRef = ref(null)
const menuStyle = ref({})

const dangerKeys = new Set(['delete', 'empty_trash', 'permanent_delete'])

const pickWallpaper = inject('pickWallpaper', null)

const options = computed(() => {
  const items = []

  // Desktop context menu
  if (context.value === 'desktop') {
    items.push({ label: t('menu.change_wallpaper'), key: 'desktop_wallpaper' })
    return items
  }

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
      const vtype = fs.getViewerType(targetFile.value.name)
      if (vtype !== 'text') {
        items.push({ label: t('menu.open_as_text'), key: 'open_as_text' })
      }
    }

    if (!targetFile.value.is_dir) {
      const ext = (targetFile.value.name || '').split('.').pop()?.toLowerCase() || ''
      const mediaExts = ['mp4','mkv','webm','mov','avi','flv','wmv','m4v','ts','mp3','aac','flac','ogg','wav','wma','m4a','opus','jpg','jpeg','png','webp','bmp','gif','tiff','tif','heic','heif','avif']
      if (mediaExts.includes(ext)) {
        items.push({ label: t('menu.transcode'), key: 'transcode' })
      }
    }

    items.push({ type: 'divider' })
    items.push({ label: t('menu.copy'), key: 'copy' })
    items.push({ label: t('menu.cut'), key: 'cut' })
    items.push({ label: t('menu.rename'), key: 'rename' })
    items.push({ label: t('menu.delete'), key: 'delete' })
    items.push({ type: 'divider' })
    items.push({ label: t('menu.details'), key: 'details' })
  } else {
    items.push({ label: t('menu.new_folder'), key: 'mkdir' })
    items.push({ label: t('menu.upload'), key: 'upload' })
    if (fs.clipboard.items.length > 0) {
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

// Position menu within viewport bounds
watch(show, async (val) => {
  if (!val || isMobile.value) return
  menuStyle.value = { left: `${x.value}px`, top: `${y.value}px` }
  await nextTick()
  const el = menuRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const vw = window.innerWidth
  const vh = window.innerHeight
  let left = x.value
  let top = y.value
  if (left + rect.width > vw - 4) left = vw - rect.width - 4
  if (top + rect.height > vh - 4) top = vh - rect.height - 4
  if (left < 4) left = 4
  if (top < 4) top = 4
  menuStyle.value = { left: `${left}px`, top: `${top}px` }
})

// Click outside / scroll / resize to close
function onClickOutside(e) {
  if (menuRef.value && !menuRef.value.contains(e.target)) {
    if (e.button === 2) suppressNextContextMenu()
    closeContextMenu()
  }
}
function onDismiss() { closeContextMenu() }

watch(show, (val) => {
  if (val && !isMobile.value) {
    document.addEventListener('mousedown', onClickOutside, true)
    window.addEventListener('scroll', onDismiss, true)
    window.addEventListener('resize', onDismiss)
  } else {
    document.removeEventListener('mousedown', onClickOutside, true)
    window.removeEventListener('scroll', onDismiss, true)
    window.removeEventListener('resize', onDismiss)
  }
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onClickOutside, true)
  window.removeEventListener('scroll', onDismiss, true)
  window.removeEventListener('resize', onDismiss)
})

function handleSelect(key) {
  closeContextMenu()
  switch (key) {
    // Desktop actions
    case 'desktop_wallpaper': if (pickWallpaper) pickWallpaper(); break
    case 'open': fs.openSelected(); break
    case 'download':
      if (targetFile.value) fs.downloadFile(targetFile.value.path)
      break
    case 'open_as_text':
      if (targetFile.value) fs.openViewer(targetFile.value, { forceType: 'text' })
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
    case 'details':
      fs.showInfoPanel = !fs.showInfoPanel
      break
  }
}
</script>

<template>
  <!-- Desktop: Plasma-style context menu -->
  <Teleport to="body">
    <Transition name="ctx-menu">
      <div
        v-if="show && !isMobile"
        ref="menuRef"
        class="plasma-context-menu"
        :style="menuStyle"
      >
        <template v-for="(item, i) in options" :key="item.key || `div-${i}`">
          <div v-if="item.type === 'divider'" class="ctx-divider" />
          <button
            v-else
            class="ctx-item"
            :class="{ danger: dangerKeys.has(item.key) }"
            @click="handleSelect(item.key)"
          >
            {{ item.label }}
          </button>
        </template>
      </div>
    </Transition>
  </Teleport>

  <!-- Mobile: bottom action sheet -->
  <BDrawer
    v-if="isMobile"
    :show="show"
    placement="bottom"
    height="auto"
    @update:show="(v) => { if (!v) closeContextMenu() }"
  >
    <div class="action-sheet">
      <button
        v-for="item in actionItems"
        :key="item.key"
        class="action-sheet-item"
        :class="{ 'danger': dangerKeys.has(item.key) }"
        @click="handleSelect(item.key)"
      >
        {{ item.label }}
      </button>
      <div class="action-sheet-gap" />
      <button class="action-sheet-item cancel" @click="closeContextMenu">
        {{ t('preview.cancel') }}
      </button>
    </div>
  </BDrawer>
</template>

<style lang="scss" scoped>
@include ctx-menu-transition;

.action-sheet {
  display: flex;
  flex-direction: column;
  padding: 8px 0;
  padding-bottom: calc(8px + env(safe-area-inset-bottom));

  &-item {
    display: flex;
    align-items: center;
    min-height: 48px;
    padding: 12px 20px;
    border: none;
    background: none;
    color: var(--breeze-text);
    font-size: 16px;
    text-align: left;

    &:active {
      background: var(--toolbar-button-active);
    }

    &.danger {
      color: var(--breeze-danger);
    }

    &.cancel {
      color: var(--breeze-text-secondary);
      text-align: center;
      justify-content: center;
    }
  }

  &-gap {
    height: 8px;
    background: rgba(0, 0, 0, 0.2);
  }
}
</style>
