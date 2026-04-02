<script setup>
import { ref, reactive, computed, h, onUnmounted } from 'vue'
import BSpin from '../breeze/BSpin.vue'
import BDataTable from '../breeze/BDataTable.vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useUploadStore } from '../../stores/upload'
import { useAuthStore } from '../../stores/auth'
import { useI18n } from '../../composables/useI18n'
import { getFileIcon } from '../../composables/useFileIcon'
import IconCopy from '~icons/mdi/content-copy'
import IconCut from '~icons/mdi/content-cut'
import IconDelete from '~icons/mdi/delete-outline'
import IconClose from '~icons/mdi/close'
import { openContextMenu } from '../../composables/useContextMenu'
import { useTouchHandlers } from '../../composables/useTouch'
import DesktopEntry from '../plasma/DesktopEntry.vue'
import InlineRename from './InlineRename.vue'
import dayjs from 'dayjs'

const fs = useFileSystemStore()
const upload = useUploadStore()
const auth = useAuthStore()
const { t } = useI18n()

const dragOver = ref(false)
const fileViewRef = ref(null)

// --- Rubber-band selection ---
const rubberBand = reactive({
  active: false,
  startX: 0,
  startY: 0,
  currentX: 0,
  currentY: 0,
})

const rubberBandStyle = computed(() => {
  const x1 = Math.min(rubberBand.startX, rubberBand.currentX)
  const y1 = Math.min(rubberBand.startY, rubberBand.currentY)
  const x2 = Math.max(rubberBand.startX, rubberBand.currentX)
  const y2 = Math.max(rubberBand.startY, rubberBand.currentY)
  return {
    left: x1 + 'px',
    top: y1 + 'px',
    width: (x2 - x1) + 'px',
    height: (y2 - y1) + 'px',
  }
})

function handleRubberBandStart(e) {
  // Only left button, only in icons/compact view, not on a file item
  if (e.button !== 0) return
  if (fs.viewMode === 'details') return
  if (e.target.closest('.file-item')) return

  const container = fileViewRef.value
  if (!container) return

  const rect = container.getBoundingClientRect()
  rubberBand.startX = e.clientX - rect.left + container.scrollLeft
  rubberBand.startY = e.clientY - rect.top + container.scrollTop
  rubberBand.currentX = rubberBand.startX
  rubberBand.currentY = rubberBand.startY
  rubberBand.active = true

  document.body.style.userSelect = 'none'
  window.addEventListener('mousemove', handleRubberBandMove)
  window.addEventListener('mouseup', handleRubberBandEnd)
}

function handleRubberBandMove(e) {
  const container = fileViewRef.value
  if (!container) return

  const rect = container.getBoundingClientRect()
  rubberBand.currentX = e.clientX - rect.left + container.scrollLeft
  rubberBand.currentY = e.clientY - rect.top + container.scrollTop

  // AABB collision detection
  const bandX1 = Math.min(rubberBand.startX, rubberBand.currentX)
  const bandY1 = Math.min(rubberBand.startY, rubberBand.currentY)
  const bandX2 = Math.max(rubberBand.startX, rubberBand.currentX)
  const bandY2 = Math.max(rubberBand.startY, rubberBand.currentY)

  const hitPaths = []
  const items = container.querySelectorAll('.file-item[data-path]')
  for (const item of items) {
    const itemRect = item.getBoundingClientRect()
    // Convert item rect to container-relative coordinates
    const ix1 = itemRect.left - rect.left + container.scrollLeft
    const iy1 = itemRect.top - rect.top + container.scrollTop
    const ix2 = ix1 + itemRect.width
    const iy2 = iy1 + itemRect.height

    // AABB intersection
    if (bandX1 < ix2 && bandX2 > ix1 && bandY1 < iy2 && bandY2 > iy1) {
      hitPaths.push(item.dataset.path)
    }
  }

  if (e.ctrlKey || e.metaKey) {
    // Additive: merge with pre-drag selection
    const merged = new Set(hitPaths)
    fs.selectedFiles = [...merged]
  } else {
    fs.selectedFiles = hitPaths
  }
}

let rubberBandUsed = false

function handleRubberBandEnd() {
  // Mark that rubber-band was used so the subsequent click doesn't clear selection
  if (rubberBand.startX !== rubberBand.currentX || rubberBand.startY !== rubberBand.currentY) {
    rubberBandUsed = true
  }
  rubberBand.active = false
  document.body.style.userSelect = ''
  window.removeEventListener('mousemove', handleRubberBandMove)
  window.removeEventListener('mouseup', handleRubberBandEnd)
}

onUnmounted(() => {
  // Clean up in case component unmounts during drag
  window.removeEventListener('mousemove', handleRubberBandMove)
  window.removeEventListener('mouseup', handleRubberBandEnd)
  document.body.style.userSelect = ''
})

// --- End rubber-band ---

function formatSize(bytes) {
  if (!bytes) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let size = bytes / 1024
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return `${size.toFixed(1)} ${units[i]}`
}

// Details view columns
const columns = computed(() => [
  {
    title: t('fileview.col_name'),
    key: 'name',
    sorter: true,
    render(row) {
      const iconComp = getFileIcon(row.name, row.is_dir)
      const isRenaming = fs.renamingFile === row.path
      const status = row.status || 'ready'
      const children = [
        h(iconComp, { width: fs.iconSize, height: fs.iconSize, class: 'detail-icon' }),
        isRenaming
          ? h(InlineRename, { file: row })
          : h('span', { class: 'truncate' }, row.name),
      ]
      if (status === 'processing' && row.job_phase) {
        const pct = row.job_progress != null ? Math.round(row.job_progress * 100) : 0
        const phaseText = t(`jobs.phase_${row.job_phase}`) || row.job_phase
        children.push(h('span', { class: 'file-status-badge badge-processing' }, `${phaseText} ${pct}%`))
      } else if (status !== 'ready') {
        children.push(h('span', { class: `file-status-badge badge-${status}` }, t(`status.badge_${status}`)))
      }
      return h('div', { class: ['detail-name', status !== 'ready' ? 'detail-not-ready' : '', status === 'uploading' ? 'detail-uploading' : ''] }, children)
    },
  },
  {
    title: t('fileview.col_size'),
    key: 'size',
    width: 100,
    sorter: true,
    render: (row) => row.is_dir ? '—' : formatSize(row.size),
  },
  {
    title: t('fileview.col_modified'),
    key: 'last_modified',
    width: 160,
    sorter: true,
    render: (row) => row.last_modified ? dayjs(row.last_modified).format('YYYY-MM-DD HH:mm') : '—',
  },
])

const rowTouchCache = new Map()
function getRowTouch(row) {
  if (!rowTouchCache.has(row.path)) {
    rowTouchCache.set(row.path, useTouchHandlers({
      onDoubleTap: () => {
        if (fs.isTrash) return
        if (row.is_dir) fs.navigate(row.path)
        else if (fs.getViewerType(row.name)) fs.openViewer(row)
        else fs.downloadFile(row.path)
      },
      onLongPress: (e) => {
        const t = e.touches[0]
        openContextMenu({ clientX: t.clientX, clientY: t.clientY, preventDefault() {} }, row)
      },
    }))
  }
  return rowTouchCache.get(row.path)
}

const rowProps = (row) => {
  const touch = getRowTouch(row)
  return {
    class: fs.selectedFiles.includes(row.path) ? 'row-selected' : '',
    onClick: (e) => fs.selectFile(row.path, e),
    onDblclick: () => {
      if (fs.isTrash) return
      if (row.is_dir) fs.navigate(row.path)
      else if (fs.getViewerType(row.name)) fs.openViewer(row)
      else fs.downloadFile(row.path)
    },
    onContextmenu: (e) => {
      e.stopPropagation()
      openContextMenu(e, row)
    },
    onTouchstart: touch.onTouchStart,
    onTouchmove: touch.onTouchMove,
    onTouchend: touch.onTouchEnd,
  }
}

function handleDragOver(e) {
  e.preventDefault()
  dragOver.value = true
}

function handleDragLeave() {
  dragOver.value = false
}

function handleDrop(e) {
  e.preventDefault()
  dragOver.value = false
  if (fs.isTrash) return
  const files = e.dataTransfer?.files
  if (files?.length) {
    upload.uploadFiles(files, fs.currentPath)
  }
}

function handleBackgroundClick(e) {
  if (e.target.closest('.file-item')) return
  // Don't clear selection if rubber-band was just used
  if (rubberBandUsed) {
    rubberBandUsed = false
    return
  }
  fs.clearSelection()
}

function handleContextMenu(e) {
  openContextMenu(e, null)
}

function handleSearchResultClick(item) {
  // Navigate to parent directory
  if (item.is_dir) {
    fs.exitSearch()
    fs.navigate(item.path)
  } else {
    fs.exitSearch()
    fs.navigate(item.parent || '/')
  }
}

function handleSearchResultDblClick(item) {
  fs.exitSearch()
  if (item.is_dir) {
    fs.navigate(item.path)
  } else if (fs.getViewerType(item.name)) {
    fs.openViewer(item)
  } else {
    fs.downloadFile(item.path)
  }
}
</script>

<template>
  <div
    ref="fileViewRef"
    class="file-view"
    :class="{ 'drag-over': dragOver }"
    @dragover="handleDragOver"
    @dragleave="handleDragLeave"
    @drop="handleDrop"
    @click="handleBackgroundClick"
    @contextmenu="handleContextMenu"
    @mousedown="handleRubberBandStart"
  >
    <!-- Search results view -->
    <template v-if="fs.searchMode">
      <BSpin :show="fs.searchLoading" class="file-view-spin">
        <div v-if="fs.searchResults.length === 0 && !fs.searchLoading" class="empty-state">
          <div class="empty-text">{{ t('search.no_results') }}</div>
        </div>
        <div v-else class="search-results">
          <div
            v-for="item in fs.searchResults"
            :key="item.path"
            class="search-result-item"
            @click="handleSearchResultClick(item)"
            @dblclick="handleSearchResultDblClick(item)"
          >
            <component :is="getFileIcon(item.name, item.is_dir)" class="search-result-icon" width="24" height="24" />
            <div class="search-result-info">
              <div class="search-result-name">{{ item.name }}</div>
              <div class="search-result-path">{{ item.parent || '/' }}</div>
            </div>
            <div class="search-result-rank">{{ Math.round(item.rank * 100) }}%</div>
          </div>
        </div>
      </BSpin>
    </template>

    <!-- Normal directory view -->
    <BSpin v-else :show="fs.loading" class="file-view-spin">
      <template v-if="fs.sortedFiles.length === 0 && !fs.loading">
        <div class="empty-state"><div class="empty-text">{{ t('fileview.empty') }}</div></div>
      </template>

      <template v-else-if="fs.viewMode === 'details'">
        <BDataTable
          :columns="columns"
          :data="fs.sortedFiles"
          :row-key="(row) => row.path"
          :row-props="rowProps"
          size="small"
          :bordered="false"
          class="details-table"
          virtual-scroll
          max-height="calc(100vh - 160px)"
        />
      </template>

      <template v-else>
        <div
          class="file-grid"
          :class="{ 'compact-grid': fs.viewMode === 'compact' }"
        >
          <DesktopEntry
            v-for="file in fs.sortedFiles"
            :key="file.path"
            :file="file"
            :compact="fs.viewMode === 'compact'"
          />
        </div>
      </template>
    </BSpin>

    <!-- Rubber-band selection rectangle -->
    <div
      v-if="rubberBand.active"
      class="rubber-band"
      :style="rubberBandStyle"
    />

    <div v-if="dragOver" class="drop-overlay">
      <div class="drop-message">{{ t('fileview.drop') }}</div>
    </div>

    <!-- Mobile select mode action bar -->
    <div v-if="fs.selectMode" class="select-action-bar">
      <span class="select-count">{{ fs.selectedFiles.length }}</span>
      <div class="select-actions">
        <button class="select-action-btn" @click="fs.copySelected(); fs.exitSelectMode()">
          <IconCopy width="20" height="20" />
          <span>{{ t('menu.copy') }}</span>
        </button>
        <button class="select-action-btn" @click="fs.cutSelected(); fs.exitSelectMode()">
          <IconCut width="20" height="20" />
          <span>{{ t('menu.cut') }}</span>
        </button>
        <button class="select-action-btn danger" @click="fs.deleteSelected(); fs.exitSelectMode()">
          <IconDelete width="20" height="20" />
          <span>{{ t('menu.delete') }}</span>
        </button>
        <button class="select-action-btn" @click="fs.exitSelectMode()">
          <IconClose width="20" height="20" />
        </button>
      </div>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.file-view {
  flex: 1;
  overflow: auto;
  position: relative;
  background: var(--breeze-bg-alt);
  min-height: 0;

  &-spin {
    min-height: 200px;
    height: 100%;
  }
}

.file-grid {
  display: flex;
  flex-wrap: wrap;
  align-content: flex-start;
  padding: var(--gap-md);
  gap: var(--file-grid-gap);
  min-height: 100%;

  &.compact-grid {
    flex-direction: column;
    flex-wrap: nowrap;
    gap: 6px;
  }
}

/* Search results */
.search-results {
  padding: 4px 0;
}

.search-result {
  &-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 12px;
    cursor: default;
    transition: background var(--transition-fast);

    &:hover {
      background: $hover-white-subtle;
    }
  }

  &-icon {
    flex-shrink: 0;
  }

  &-info {
    flex: 1;
    min-width: 0;
  }

  &-name {
    font-size: 13px;
    color: var(--breeze-text);
    @include truncate;
  }

  &-path {
    font-size: 11px;
    color: var(--breeze-text-disabled);
    @include truncate;
  }

  &-rank {
    font-size: 11px;
    color: var(--breeze-text-secondary);
    flex-shrink: 0;
  }
}

.empty-state {
  padding: 60px 20px;
  text-align: center;
}

.empty-text {
  color: var(--breeze-text-disabled);
  font-size: 14px;
}

.details-table {
  height: 100%;
}

.drag-over {
  outline: 2px dashed var(--breeze-accent);
  outline-offset: -4px;
}

.drop-overlay {
  position: absolute;
  inset: 0;
  background: rgba(61, 174, 233, 0.06);
  @include flex-center;
  pointer-events: none;
}

.drop-message {
  padding: 16px 32px;
  background: var(--breeze-surface);
  border-radius: 8px;
  font-size: var(--font-size-lg);
  color: var(--breeze-accent);
  border: 2px dashed var(--breeze-accent);
}

.rubber-band {
  position: absolute;
  background: rgba(61, 174, 233, 0.15);
  border: 1px solid rgba(61, 174, 233, 0.6);
  pointer-events: none;
  z-index: $z-resize;
}

:deep(.row-selected) {
  background: var(--breeze-active) !important;
}

:deep(.detail-name) {
  display: flex;
  align-items: center;
  gap: 8px;
}

:deep(.detail-icon) {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  line-height: 0;
}

:deep(.detail-not-ready) {
  opacity: 0.55;
}

:deep(.detail-uploading) {
  animation: pulse-uploading 1.8s ease-in-out infinite;
}

@keyframes pulse-uploading {
  0%, 100% { opacity: 0.45; }
  50% { opacity: 0.75; }
}

:deep(.file-status-badge) {
  font-size: 10px;
  line-height: 1;
  padding: 2px 6px;
  border-radius: 3px;
  white-space: nowrap;
  flex-shrink: 0;
}

:deep(.badge-uploading) {
  color: #3daee9;
  background: rgba(61, 174, 233, 0.15);
}

:deep(.badge-processing) {
  color: #f67400;
  background: rgba(246, 116, 0, 0.15);
}

:deep(.badge-failed) {
  color: #da4453;
  background: rgba(218, 68, 83, 0.15);
}

.select-action {
  &-bar {
    position: sticky;
    bottom: 0;
    left: 0;
    right: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 16px;
    padding-bottom: calc(8px + env(safe-area-inset-bottom));
    background: var(--breeze-surface-raised);
    border-top: 1px solid var(--breeze-border);
    z-index: 20;
  }

  &-btn {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    min-width: 52px;
    min-height: 48px;
    padding: 6px 8px;
    border: none;
    background: none;
    color: var(--breeze-text);
    font-size: 11px;
    border-radius: 6px;

    &:active {
      background: var(--toolbar-button-active);
    }

    &.danger {
      color: var(--breeze-danger);
    }
  }
}

.select-count {
  color: var(--breeze-accent);
  font-weight: 600;
  font-size: 16px;
  min-width: 32px;
}

.select-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: auto;
}
</style>
