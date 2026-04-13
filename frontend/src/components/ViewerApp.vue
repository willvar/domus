<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch, nextTick, computed, defineAsyncComponent } from 'vue'
import { useFileSystemStore } from '../stores/fileSystem'
import { useWindowManagerStore } from '../stores/windowManager'
import { useI18n } from '../composables/useI18n'
import { useCodeMirror } from '../composables/useCodeMirror'
import { useWorkspaceSync, registerViewerCallback, unregisterViewerCallback } from '../composables/useWorkspaceSync'
import { usePreferences } from '../composables/usePreferences'
import PlasmaWindow from './plasma/Window.vue'
import { IconEyeOutline as IconEye, IconContentSave as IconSave, IconClose, IconPencil as IconEdit, IconChevronLeft as IconPrev, IconChevronRight as IconNext, IconMagnifyPlusOutline as IconZoomIn, IconMagnifyMinusOutline as IconZoomOut } from '../barrels/icons'

// Lazy-loaded viewer sub-components
const viewers = () => import('../barrels/viewers')
const viewerMap: Record<string, ReturnType<typeof defineAsyncComponent>> = {
  audio: defineAsyncComponent(() => viewers().then(m => m.AudioViewer)),
  markdown: defineAsyncComponent(() => viewers().then(m => m.MarkdownViewer)),
  csv: defineAsyncComponent(() => viewers().then(m => m.CsvViewer)),
  font: defineAsyncComponent(() => viewers().then(m => m.FontViewer)),
  archive: defineAsyncComponent(() => viewers().then(m => m.ArchiveViewer)),
  notebook: defineAsyncComponent(() => viewers().then(m => m.NotebookViewer)),
}

const props = defineProps({
  windowId: { type: String, required: true },
})

const fs = useFileSystemStore()
const wm = useWindowManagerStore()
const { t } = useI18n()

const state = computed(() => fs.findApp(props.windowId))
const delegatedViewer = computed(() => state.value ? viewerMap[state.value.type] : null)

const editBuffer = ref('')
const cmContainer = ref<HTMLDivElement | null>(null)
const cm = useCodeMirror()
const htmlPreviewMode = ref('split')

const windowOpen = computed(() => !!wm.findWindow(props.windowId))
const canEdit = computed(() => {
  if (!state.value) return false
  if (state.value.chunked && !state.value.isFullyLoaded) return false
  if (state.value.file?.path?.startsWith('/__trash__/')) return false
  // Shared files: only editable with write permission
  if (state.value.file?._shareId && state.value.file?._permission !== 'write') return false
  return true
})
const isHtmlPreview = computed(() => {
  if (!state.value || state.value.type !== 'text') return false
  const name = state.value.file?.name?.toLowerCase() || ''
  return name.endsWith('.html') || name.endsWith('.htm')
})
const iframeSrcdoc = computed(() => state.value?.content || '')
const showCodePane = computed(() => !isHtmlPreview.value || htmlPreviewMode.value !== 'preview')
const showRenderedPane = computed(() => isHtmlPreview.value && htmlPreviewMode.value !== 'code')

// ─── CodeMirror ───

function createEditor(readOnly: boolean) {
  if (!cmContainer.value || !state.value) return
  const callbacks = readOnly ? {} : {
    onSave: () => { if (state.value?.dirty) fs.saveViewer(props.windowId) },
    onChange: (content: string) => { if (state.value) { state.value.content = content; state.value.dirty = true } },
  }
  cm.create(cmContainer.value, state.value.content || '', state.value.language, readOnly, callbacks)
}

watch(
  () => state.value && state.value.type === 'text' && state.value.file && state.value.content !== null && windowOpen.value,
  (ready) => {
    if (ready && !state.value?.editing) {
      nextTick(() => { if (!cm.view.value) createEditor(true) })
    }
  },
  { immediate: true }
)

watch(() => state.value?.editing, (editing) => {
  if (editing) {
    editBuffer.value = state.value?.content || ''
    nextTick(() => createEditor(false))
  } else {
    nextTick(() => createEditor(true))
  }
})

watch(isHtmlPreview, (enabled) => {
  htmlPreviewMode.value = enabled ? 'split' : 'code'
}, { immediate: true })

watch(showCodePane, (visible) => {
  if (!visible) { cm.destroy(); return }
  if (state.value?.type === 'text' && state.value.content !== null) {
    nextTick(() => createEditor(!state.value?.editing))
  }
}, { immediate: true })

// Update CodeMirror content on page change (chunked text)
watch(() => state.value?.page, () => {
  if (state.value?.type === 'text' && state.value.content !== null && !state.value.editing) {
    if (cm.view.value) cm.replaceContent(state.value.content)
    else nextTick(() => createEditor(true))
  }
})

// ─── actions ───

function startEdit() {
  if (!state.value) return
  editBuffer.value = state.value.content || ''
  state.value.editing = true
  state.value.dirty = false
}

function cancelEdit() {
  if (!state.value) return
  state.value.content = editBuffer.value
  state.value.editing = false
  state.value.dirty = false
}

function handleClose() {
  fs.closeViewer(props.windowId)
}

onMounted(() => {
  if (state.value?.type === 'text' && state.value.file && state.value.content && !state.value.editing && showCodePane.value) {
    nextTick(() => createEditor(true))
  }
})

onUnmounted(() => {
  cm.destroy()
  unregisterViewerCallback(props.windowId)
  if (_timeSyncTimer) clearInterval(_timeSyncTimer)
  _destroyPanzoom()
})

// ─── Image zoom / pan (powered by @panzoom/panzoom) ───
//
// panzoom applies `scale(s) translate(x, y)` with transform-origin 50% 50%.
// A pixel at (px, py) ends up at:
//   visX = s*(px + x) + (natW/2)*(1-s)
// So to center the image: x = (cw - natW) / (2*s), y = (ch - natH) / (2*s)

type PanzoomObject = import('@panzoom/panzoom').PanzoomObject
const imgScale = ref(1)
const imgFitMode = ref(true)
const imgContainerEl = ref<HTMLDivElement | null>(null)
const imgEl = ref<HTMLImageElement | null>(null)
let _pz: PanzoomObject | null = null
let _imgResizeOb: ResizeObserver | null = null
let _imgNatW = 0
let _imgNatH = 0
let _wheelHandler: ((e: WheelEvent) => void) | null = null
let _changeHandler: (() => void) | null = null

const imgZoomPct = computed(() => Math.round(imgScale.value * 100))

function _syncScale() {
  imgScale.value = _pz?.getScale() ?? 1
}

/** Fit scale and centered pan values for the current container/image */
function _fitParams() {
  const c = imgContainerEl.value
  if (!c || !_imgNatW) return null
  const cw = c.clientWidth, ch = c.clientHeight
  const s = Math.min(cw / _imgNatW, ch / _imgNatH, 1)
  // pan values are in pre-scale space (panzoom uses `scale(s) translate(x,y)`)
  return { s, x: (cw - _imgNatW) / (2 * s), y: (ch - _imgNatH) / (2 * s) }
}

function imgFitToWindow() {
  const p = _fitParams()
  if (!p || !_pz) return
  _pz.setOptions({ startScale: p.s, startX: p.x, startY: p.y })
  _pz.reset({ animate: false })
  imgFitMode.value = true
  _syncScale()
}

function imgActualSize() {
  const c = imgContainerEl.value
  if (!c || !_imgNatW || !_pz) return
  _pz.zoom(1, { animate: false, force: true })
  _pz.pan((c.clientWidth - _imgNatW) / 2, (c.clientHeight - _imgNatH) / 2, { animate: false, force: true })
  imgFitMode.value = false
}

/** Keep current scale, re-center in the (possibly resized) container */
function imgRecenter() {
  const c = imgContainerEl.value
  if (!c || !_imgNatW || !_pz) return
  const s = _pz.getScale()
  _pz.pan((c.clientWidth - _imgNatW) / (2 * s), (c.clientHeight - _imgNatH) / (2 * s), { animate: false, force: true })
}

function imgZoomIn() {
  _pz?.zoomIn({ animate: false })
}

function imgZoomOut() {
  _pz?.zoomOut({ animate: false })
}

function imgOnDblClick() {
  if (imgFitMode.value && imgScale.value < 1) imgActualSize()
  else imgFitToWindow()
}

async function _initPanzoom() {
  const el = imgEl.value
  const container = imgContainerEl.value
  if (!el || !container || _pz) return
  const { default: Panzoom } = await import('@panzoom/panzoom')
  if (!imgEl.value) return

  const p = _fitParams()
  const EDGE = 48 // minimum visible pixels per axis
  _pz = Panzoom(el, {
    canvas: true,
    panOnlyWhenZoomed: true,
    roundPixels: true,
    minScale: 0.02,
    maxScale: 80,
    step: 0.3,
    cursor: 'grab',
    startScale: p?.s ?? 1,
    startX: p?.x ?? 0,
    startY: p?.y ?? 0,
    // Custom setTransform: clamp so the image can't fully leave the viewport
    setTransform(elem, { scale: s, x, y }) {
      const c = imgContainerEl.value
      if (c && _imgNatW) {
        const ox = _imgNatW / 2, oy = _imgNatH / 2
        // Image rect in container pixel space
        let left = s * x + ox * (1 - s)
        let top  = s * y + oy * (1 - s)
        const rw = s * _imgNatW, rh = s * _imgNatH
        const cw = c.clientWidth, ch = c.clientHeight
        // At least EDGE px visible in each axis
        left = Math.max(EDGE - rw, Math.min(cw - EDGE, left))
        top  = Math.max(EDGE - rh, Math.min(ch - EDGE, top))
        // Convert clamped pixel position back to panzoom translate
        x = (left - ox * (1 - s)) / s
        y = (top  - oy * (1 - s)) / s
      }
      elem.style.transform = `scale(${s}) translate(${x}px, ${y}px)`
    },
  })

  _wheelHandler = (e: WheelEvent) => {
    _pz!.zoomWithWheel(e)
    imgFitMode.value = false
  }
  container.addEventListener('wheel', _wheelHandler, { passive: false })

  // Sync scale display; detect user gestures (pinch etc.) via fit-scale comparison
  _changeHandler = () => {
    _syncScale()
    const p = _fitParams()
    if (p) imgFitMode.value = Math.abs(imgScale.value - p.s) < 0.001
  }
  el.addEventListener('panzoomchange', _changeHandler)
}

function imgOnLoad() {
  const el = imgEl.value
  if (!el) return
  _imgNatW = el.naturalWidth
  _imgNatH = el.naturalHeight
  if (!_pz) {
    _initPanzoom().then(() => imgFitToWindow())
  } else {
    imgFitToWindow()
  }
}

function _destroyPanzoom() {
  if (_wheelHandler && imgContainerEl.value) {
    imgContainerEl.value.removeEventListener('wheel', _wheelHandler)
    _wheelHandler = null
  }
  if (_changeHandler && imgEl.value) {
    imgEl.value.removeEventListener('panzoomchange', _changeHandler)
    _changeHandler = null
  }
  _pz?.destroy()
  _pz = null
  _imgResizeOb?.disconnect()
  _imgResizeOb = null
}

watch(() => state.value?.type === 'image' && state.value?.url, (isImage) => {
  if (isImage) {
    nextTick(() => {
      if (imgContainerEl.value) {
        _imgResizeOb = new ResizeObserver(() => {
          if (imgFitMode.value) {
            imgFitToWindow()
          } else {
            imgRecenter()
          }
        })
        _imgResizeOb.observe(imgContainerEl.value)
      }
    })
  } else {
    _destroyPanzoom()
  }
}, { immediate: true })

// ─── Video playback sync ───
const videoEl = ref<HTMLVideoElement | null>(null)
const sync = useWorkspaceSync()
const { prefs } = usePreferences()
let _isRemotePlayback = false
let _timeSyncTimer: ReturnType<typeof setInterval> | null = null

function onVideoPlay() {
  if (_isRemotePlayback || prefs.sessionIsolation) return
  sync.emitEvent({ action: 'viewer.play', windowId: props.windowId, currentTime: videoEl.value?.currentTime || 0 })
}

function onVideoPause() {
  if (_isRemotePlayback || prefs.sessionIsolation) return
  sync.emitEvent({ action: 'viewer.pause', windowId: props.windowId, currentTime: videoEl.value?.currentTime || 0 })
}

function onVideoSeeked() {
  if (_isRemotePlayback || prefs.sessionIsolation) return
  sync.emitEvent({ action: 'viewer.seek', windowId: props.windowId, currentTime: videoEl.value?.currentTime || 0 })
}

// Register callback for receiving remote playback events
registerViewerCallback(props.windowId, (action: string, currentTime?: number) => {
  const el = videoEl.value
  if (!el) return
  const t = currentTime ?? 0
  _isRemotePlayback = true
  try {
    switch (action) {
      case 'play':
        el.currentTime = t
        el.play().catch(() => {})
        break
      case 'pause':
        el.pause()
        el.currentTime = t
        break
      case 'seek':
        el.currentTime = t
        break
      case 'timeSync':
        if (Math.abs(el.currentTime - t) > 2) {
          el.currentTime = t
        }
        break
    }
  } finally {
    // Delay clearing to avoid re-triggering events from the programmatic changes
    setTimeout(() => { _isRemotePlayback = false }, 100)
  }
})

// Start timeSync interval when video is playing
watch(() => state.value?.type === 'video' && state.value?.url, (ready) => {
  if (ready) {
    _timeSyncTimer = setInterval(() => {
      const el = videoEl.value
      if (!el || el.paused || _isRemotePlayback || prefs.sessionIsolation) return
      sync.emitEvent({ action: 'viewer.timeSync', windowId: props.windowId, currentTime: el.currentTime })
    }, 3000)
  } else if (_timeSyncTimer) {
    clearInterval(_timeSyncTimer!)
    _timeSyncTimer = null
  }
}, { immediate: true })

function onVideoError() {
  const el = videoEl.value
  if (!el) return
  const err = el.error
  console.error('[Viewer] video error:', err?.code, err?.message)
}
</script>

<template>
  <PlasmaWindow
    v-if="windowOpen && state"
    :window-id="windowId"
    :title="state.file.name"
    :icon="IconEye"
    @close="handleClose"
  >
    <!-- Delegated viewers (audio, markdown, csv, font, archive, notebook) -->
    <template v-if="delegatedViewer">
      <component :is="delegatedViewer" :state="state" :window-id="windowId" />
    </template>

    <!-- Inline viewers (image, video, pdf, text) -->
    <template v-else>
      <div class="viewer-toolbar">
        <template v-if="state.type === 'image'">
          <button class="viewer-btn" :title="t('preview.fit')" :class="{ active: imgFitMode }" @click="imgFitToWindow">
            <span>{{ t('preview.fit') }}</span>
          </button>
          <button class="viewer-btn" :title="t('preview.actualSize')" @click="imgActualSize">
            <span>1:1</span>
          </button>
          <span class="toolbar-sep" />
          <button class="viewer-btn" @click="imgZoomOut"><IconZoomOut width="16" height="16" /></button>
          <span class="zoom-indicator">{{ imgZoomPct }}%</span>
          <button class="viewer-btn" @click="imgZoomIn"><IconZoomIn width="16" height="16" /></button>
        </template>
        <template v-if="state.type === 'text'">
          <template v-if="isHtmlPreview">
            <div class="viewer-mode-group">
              <button class="viewer-btn mode-btn" :class="{ active: htmlPreviewMode === 'code' }" @click="htmlPreviewMode = 'code'">
                <span>{{ t('preview.code') }}</span>
              </button>
              <button class="viewer-btn mode-btn" :class="{ active: htmlPreviewMode === 'preview' }" @click="htmlPreviewMode = 'preview'">
                <span>{{ t('preview.rendered') }}</span>
              </button>
              <button class="viewer-btn mode-btn" :class="{ active: htmlPreviewMode === 'split' }" @click="htmlPreviewMode = 'split'">
                <span>{{ t('preview.split') }}</span>
              </button>
            </div>
          </template>
          <template v-if="state.editing">
            <button class="viewer-btn save-btn" :disabled="state.saving || !state.dirty" @click="fs.saveViewer(windowId)">
              <IconSave width="16" height="16" />
              <span>{{ state.saving ? '...' : t('preview.save') }}</span>
            </button>
            <button class="viewer-btn" @click="cancelEdit">
              <IconClose width="16" height="16" />
              <span>{{ t('preview.cancel') }}</span>
            </button>
          </template>
          <template v-else-if="canEdit">
            <button class="viewer-btn edit-btn" @click="startEdit">
              <IconEdit width="16" height="16" />
              <span>{{ t('preview.edit') }}</span>
            </button>
          </template>
          <template v-if="state.chunked && !state.isFullyLoaded && !state.editing">
            <span class="toolbar-sep" />
            <button class="viewer-btn" :disabled="(state.page ?? 0) <= 0" @click="fs.viewerPrevPage(windowId)">
              <IconPrev width="14" height="14" />
            </button>
            <span class="page-indicator">{{ (state.page ?? 0) + 1 }} / ~{{ state.totalPages }}</span>
            <button class="viewer-btn" :disabled="(state.page ?? 0) >= (state.totalPages ?? 1) - 1" @click="fs.viewerNextPage(windowId)">
              <IconNext width="14" height="14" />
            </button>
          </template>
        </template>
      </div>

      <div class="viewer-body" :class="{ 'viewer-body-split': state.type === 'text' && isHtmlPreview && htmlPreviewMode === 'split' }">
        <div v-if="state.type === 'image' && state.url" ref="imgContainerEl" class="viewer-image-container" @dblclick="imgOnDblClick">
          <img ref="imgEl" :src="state.url" :alt="state.file.name" class="viewer-image" draggable="false" @load="imgOnLoad" />
        </div>
        <video v-else-if="state.type === 'video' && state.url" ref="videoEl" :src="state.url" controls autoplay playsinline class="viewer-video" @play="onVideoPlay" @pause="onVideoPause" @seeked="onVideoSeeked" @error="onVideoError" />
        <iframe v-else-if="state.type === 'pdf' && state.url" :src="state.url" class="viewer-pdf" />
        <iframe v-else-if="state.type === 'office' && state.url" :src="state.url" class="viewer-pdf" allowfullscreen />
        <div v-else-if="state.type === 'text' && showCodePane" ref="cmContainer" class="viewer-cm-wrap" :class="{ 'viewer-pane': isHtmlPreview }" />
        <div v-if="state.type === 'text' && showRenderedPane" class="viewer-render-wrap" :class="{ 'viewer-pane': htmlPreviewMode === 'split' }">
          <iframe class="viewer-render-frame" :srcdoc="iframeSrcdoc" sandbox="" title="HTML Preview" />
        </div>
      </div>
    </template>
  </PlasmaWindow>
</template>

<style lang="scss" scoped>
.viewer-toolbar {
  display: flex;
  align-items: center;
  height: 32px;
  padding: 0 8px;
  background: var(--breeze-bg-alt);
  border-bottom: 1px solid var(--breeze-border);
  gap: 6px;
  flex-shrink: 0;
}

.viewer-mode-group {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-right: 8px;
  padding-right: 8px;
  border-right: 1px solid var(--breeze-border);
}

.viewer-btn {
  background: none;
  border: none;
  color: #bbb;
  padding: 4px 8px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;

  &:hover {
    background: $hover-white-strong;
    color: #fff;
  }

  &:disabled {
    opacity: 0.4;
  }

  &.edit-btn {
    color: #8cb4ff;
  }

  &.save-btn {
    color: #5cb85c;
  }

  &.active, &.mode-btn.active {
    background: rgba(61, 174, 233, 0.18);
    color: #7cc7ff;
  }
}

.toolbar-sep { width: 1px; height: 16px; background: var(--breeze-border); margin: 0 4px; flex-shrink: 0; }
.page-indicator { font-size: 12px; color: #999; white-space: nowrap; padding: 0 4px; }

.viewer-body {
  flex: 1;
  @include flex-center;
  min-height: 0;
  overflow: auto;
  background: var(--breeze-bg);

  &.viewer-body-split {
    align-items: stretch;
    justify-content: stretch;
    overflow: hidden;
  }
}

.viewer-image-container {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.viewer-image {
  user-select: none;
}

.zoom-indicator {
  font-size: 12px;
  color: #999;
  min-width: 42px;
  text-align: center;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.viewer-video {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.viewer-pdf {
  width: 100%;
  height: 100%;
  border: 0;
}

.viewer-cm-wrap {
  width: 100%;
  height: 100%;
  overflow: hidden;

  :deep(.cm-editor) {
    height: 100%;

    &.cm-focused {
      outline: none;
    }
  }
}

.viewer-pane {
  flex: 1 1 50%;
  min-width: 0;
  min-height: 0;
}

.viewer-render-wrap {
  width: 100%;
  height: 100%;
  background: #fff;
}

.viewer-render-frame {
  width: 100%;
  height: 100%;
  border: 0;
  background: #fff;
}

@include mobile {
  .viewer-toolbar { height: 44px; }
  .viewer-btn { min-height: 44px; padding: 8px 12px; font-size: 14px; }
}
</style>
