<script setup lang="ts">
import { computed, ref, onBeforeUnmount } from 'vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { getFileIcon } from '../../composables/useFileIcon'
import { openContextMenu } from '../../composables/useContextMenu'
import { useTouchHandlers } from '../../composables/useTouch'
import { useI18n } from '../../composables/useI18n'
import InlineRename from '../dolphin/InlineRename.vue'
import { IconCheck } from '../../barrels/icons'

const { t } = useI18n()

import type { FileListItem } from '../../types'

const props = defineProps({
  file: { type: Object as () => FileListItem, required: true },
  compact: { type: Boolean, default: false },
})

const fs = useFileSystemStore()

const isSelected = computed(() => fs.selectedFiles.includes(props.file.path))
const isRenaming = computed(() => fs.renamingFile === props.file.path)
const isCut = computed(() =>
  fs.clipboard.mode === 'cut' && fs.clipboard.items.some(i => i.path === props.file.path)
)
const fileStatus = computed(() => props.file.status || 'ready')
const isNotReady = computed(() => fileStatus.value !== 'ready')
const badgeLabel = computed(() => {
  const s = fileStatus.value
  if (s === 'ready') return ''
  const key = `status.badge_${s}`
  return t(key)
})

const iconName = computed(() => getFileIcon(props.file.name, props.file.is_dir))

function formatSize(bytes: number) {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let size = bytes / 1024
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return `${size.toFixed(1)} ${units[i]}`
}

function formatDate(d: string) {
  if (!d) return ''
  const date = new Date(d)
  if (isNaN(date.getTime())) return ''
  const Y = date.getFullYear()
  const M = String(date.getMonth() + 1).padStart(2, '0')
  const D = String(date.getDate()).padStart(2, '0')
  const h = String(date.getHours()).padStart(2, '0')
  const m = String(date.getMinutes()).padStart(2, '0')
  const s = String(date.getSeconds()).padStart(2, '0')
  return `${Y}.${M}.${D} ${h}:${m}:${s}`
}

function formatDuration(seconds: number) {
  if (!seconds) return ''
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}

function fileType(file: any) {
  if (file.is_dir) return t('info.directory')
  const ct = file.content_type
  if (!ct) return t('info.file')
  // Extract format label from MIME: "video/mp4" → "MP4", "image/jpeg" → "JPEG"
  const sub = ct.split('/').pop()?.replace(/^x-/, '').toUpperCase()
  const category = ct.split('/')[0]
  const categoryMap: Record<string, string> = { video: 'info.cat_video', audio: 'info.cat_audio', image: 'info.cat_image' }
  const categoryKey = categoryMap[category]
  if (categoryKey) return t(categoryKey, { fmt: sub })
  return sub || t('info.file')
}

function handleClick(e: MouseEvent) {
  e.stopPropagation()
  if (fs.selectMode) {
    fs.toggleSelect(props.file.path)
    return
  }
  fs.selectFile(props.file.path, e)
}

function handleDblClick() {
  if (fs.selectMode) return
  if (fs.isTrash) return
  if (isNotReady.value) return
  if (props.file.is_dir) {
    fs.navigate(props.file.path)
  } else if (fs.getViewerType(props.file.name)) {
    fs.openViewer(props.file)
  } else {
    fs.downloadFile(props.file.path)
  }
}

// Hover tooltip
const showTooltip = ref(false)
const tooltipStyle = ref<Record<string, string>>({})
let hoverTimer: ReturnType<typeof setTimeout> | null = null

function onMouseEnter(e: MouseEvent) {
  const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
  hoverTimer = setTimeout(() => {
    tooltipStyle.value = {
      left: rect.right + 8 + 'px',
      top: rect.top + 'px',
    }
    showTooltip.value = true
  }, 500)
}

function onMouseLeave() {
  clearTimeout(hoverTimer!)
  hoverTimer = setTimeout(() => {
    showTooltip.value = false
  }, 200)
}

function cancelHideTimer() {
  clearTimeout(hoverTimer!)
}

onBeforeUnmount(() => {
  clearTimeout(hoverTimer!)
})

const touch = useTouchHandlers({
  onDoubleTap: () => handleDblClick(),
  onLongPress: (e) => {
    if (!fs.selectMode) {
      fs.enterSelectMode(props.file.path)
    } else {
      const t = e.touches[0]
      openContextMenu({ clientX: t.clientX, clientY: t.clientY, preventDefault() {} } as any, props.file)
    }
  },
})
</script>

<template>
  <div
    class="file-item"
    :data-path="file.path"
    :class="[
      {
        selected: isSelected,
        compact: compact,
        cut: isCut,
      },
      isNotReady ? `status-${fileStatus}` : '',
    ]"
    @click="handleClick"
    @dblclick="handleDblClick"
    @contextmenu.prevent.stop="openContextMenu($event, file)"
    @touchstart="touch.onTouchStart"
    @touchmove="touch.onTouchMove"
    @touchend="touch.onTouchEnd"
    @mouseenter="onMouseEnter"
    @mouseleave="onMouseLeave"
  >
    <div class="file-icon">
      <div v-if="fs.selectMode" class="select-check" :class="{ checked: isSelected }">
        <IconCheck v-if="isSelected" width="18" height="18" color="#fff" />
      </div>
      <img
        v-if="file.thumbnail_url"
        :src="file.thumbnail_url"
        class="file-thumbnail"
        :style="{ height: fs.iconSize + 'px' }"
        loading="lazy"
        draggable="false"
      />
      <component :is="iconName" v-else :width="fs.iconSize" :height="fs.iconSize" />
    </div>
    <div class="file-label">
      <InlineRename
        v-if="isRenaming"
        :file="file"
      />
      <span v-else class="file-name">{{ file.name }}</span>
      <span v-if="isNotReady" class="file-status-badge" :class="'badge-' + fileStatus">
        {{ badgeLabel }}
      </span>
      <span v-else class="file-size">{{ file.is_dir ? '' : formatSize(file.size) }}</span>
    </div>

    <Teleport to="body">
      <div v-if="showTooltip" class="file-tooltip" :style="tooltipStyle"
        @mouseenter="cancelHideTimer"
        @mouseleave="showTooltip = false"
      >
        <img
          v-if="file.thumbnail_url"
          :src="file.thumbnail_url"
          class="tooltip-thumbnail"
        />
        <div class="tooltip-info">
          <div class="tooltip-name">{{ file.name }}</div>
          <div v-if="!file.is_dir" class="tooltip-row">
            <span class="tooltip-label">{{ t('info.type') }}:</span>
            <span>{{ fileType(file) }}</span>
          </div>
          <div v-if="!file.is_dir" class="tooltip-row">
            <span class="tooltip-label">{{ t('info.size') }}:</span>
            <span>{{ formatSize(file.size) }}</span>
          </div>
          <div v-if="file.media_width && file.media_height" class="tooltip-row">
            <span class="tooltip-label">{{ t('info.dimensions') }}:</span>
            <span>{{ file.media_width }} x {{ file.media_height }}</span>
          </div>
          <div v-if="file.media_duration" class="tooltip-row">
            <span class="tooltip-label">{{ t('info.duration') }}:</span>
            <span>{{ formatDuration(file.media_duration) }}</span>
          </div>
          <div v-if="file.created_at" class="tooltip-row">
            <span class="tooltip-label">{{ t('info.created') }}:</span>
            <span>{{ formatDate(file.created_at) }}</span>
          </div>
          <div v-if="file.last_modified" class="tooltip-row">
            <span class="tooltip-label">{{ t('info.modified') }}:</span>
            <span>{{ formatDate(file.last_modified) }}</span>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style lang="scss" scoped>
.file-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: var(--file-item-padding);
  border-radius: var(--file-item-radius);
  width: 110px;
  gap: 4px;
  transition: background var(--transition-fast);
  user-select: none;

  &:hover {
    background: var(--breeze-hover);
  }

  &.selected {
    background: var(--breeze-active);
    outline: 1px solid rgba(61, 174, 233, 0.5);
    border-radius: var(--file-item-radius);
  }

  &.cut {
    opacity: 0.45;
  }

  &.compact {
    flex-direction: row;
    width: 100%;
    gap: var(--gap-sm);
    padding: 3px var(--gap-sm);
  }

  &.status-uploading {
    animation: pulse-busy 1.8s ease-in-out infinite;
  }

  &.status-processing {
    opacity: 0.55;
  }

  &.status-failed {
    opacity: 0.55;
  }
}

.file-icon {
  flex-shrink: 0;
  line-height: 0;
  @include flex-center;
  position: relative;
}

.select-check {
  position: absolute;
  top: -4px;
  left: -4px;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  border: 2px solid #888;
  background: rgba(0, 0, 0, 0.4);
  z-index: $z-sticky;
  @include flex-center;

  &.checked {
    border-color: #3daee9;
    background: #3daee9;
  }
}

.file-thumbnail {
  object-fit: contain;
  border-radius: 4px;
  max-width: 100%;
}

.compact {
  .file-thumbnail {
    width: 32px;
    height: 32px;
    max-height: 32px;
  }

  .file-label {
    flex-direction: row;
    align-items: center;
    gap: var(--gap-sm);
  }

  .file-name {
    text-align: left;
    -webkit-line-clamp: 1;
  }
}

.file-label {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-width: 0;
  width: 100%;
  gap: 1px;
}

.file-name {
  font-size: var(--font-size-sm);
  color: var(--breeze-text);
  text-align: center;
  max-width: 100%;
  word-break: break-all;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  line-height: 1.3;
}

.file-size {
  font-size: var(--font-size-xs);
  color: var(--breeze-text-secondary);
  line-height: 1.2;
}

@keyframes pulse-busy {
  0%, 100% { opacity: 0.45; }
  50% { opacity: 0.75; }
}

.file-status-badge {
  font-size: 10px;
  line-height: 1;
  padding: 2px 6px;
  border-radius: 3px;
  white-space: nowrap;
}

.badge-uploading {
  color: #3daee9;
  background: rgba(61, 174, 233, 0.15);
}

.badge-processing {
  color: #f67400;
  background: rgba(246, 116, 0, 0.15);
}

.badge-failed {
  color: #da4453;
  background: rgba(218, 68, 83, 0.15);
}
</style>

<style lang="scss">
.file-tooltip {
  position: fixed;
  z-index: $z-popup;
  background: var(--breeze-surface-raised, #31363b);
  border: 1px solid var(--breeze-border, #3b4045);
  border-radius: 6px;
  padding: 10px;
  max-width: 280px;
  box-shadow: $shadow-dropdown;
  animation: tooltip-fade-in 0.15s ease;
}

@keyframes tooltip-fade-in {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}

.tooltip {
  &-thumbnail {
    width: 100%;
    max-height: 160px;
    object-fit: contain;
    border-radius: 4px;
    margin-bottom: 8px;
  }

  &-info {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  &-name {
    font-size: 13px;
    font-weight: 500;
    color: var(--breeze-text, #fcfcfc);
    margin-bottom: 4px;
    word-break: break-all;
  }

  &-row {
    font-size: 12px;
    color: var(--breeze-text-secondary, #6e7a86);
    display: flex;
    gap: 6px;
  }

  &-label {
    color: var(--breeze-text-disabled, #505962);
    flex-shrink: 0;
  }
}
</style>
