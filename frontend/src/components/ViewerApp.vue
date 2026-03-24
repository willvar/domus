<script setup>
import { onMounted, onUnmounted, ref, watch, nextTick, computed, defineAsyncComponent } from 'vue'
import { useFileSystemStore } from '../stores/fileSystem'
import { useWindowManagerStore } from '../stores/windowManager'
import { useI18n } from '../composables/useI18n'
import { useCodeMirror } from '../composables/useCodeMirror'
import PlasmaWindow from './PlasmaWindow.vue'

// Lazy-loaded viewer sub-components
const viewerMap = {
  audio: defineAsyncComponent(() => import('./viewers/AudioViewer.vue')),
  markdown: defineAsyncComponent(() => import('./viewers/MarkdownViewer.vue')),
  csv: defineAsyncComponent(() => import('./viewers/CsvViewer.vue')),
  font: defineAsyncComponent(() => import('./viewers/FontViewer.vue')),
  xlsx: defineAsyncComponent(() => import('./viewers/SpreadsheetViewer.vue')),
  docx: defineAsyncComponent(() => import('./viewers/DocxViewer.vue')),
  epub: defineAsyncComponent(() => import('./viewers/EpubViewer.vue')),
  archive: defineAsyncComponent(() => import('./viewers/ArchiveViewer.vue')),
  notebook: defineAsyncComponent(() => import('./viewers/NotebookViewer.vue')),
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
const cmContainer = ref(null)
const cm = useCodeMirror()
const htmlPreviewMode = ref('split')

const windowOpen = computed(() => !!wm.findWindow(props.windowId))
const canEdit = computed(() => state.value && (!state.value.chunked || state.value.isFullyLoaded))
const isHtmlPreview = computed(() => {
  if (!state.value || state.value.type !== 'text') return false
  const name = state.value.file?.name?.toLowerCase() || ''
  return name.endsWith('.html') || name.endsWith('.htm')
})
const iframeSrcdoc = computed(() => state.value?.content || '')
const showCodePane = computed(() => !isHtmlPreview.value || htmlPreviewMode.value !== 'preview')
const showRenderedPane = computed(() => isHtmlPreview.value && htmlPreviewMode.value !== 'code')

// ─── CodeMirror ───

function createEditor(readOnly) {
  if (!cmContainer.value || !state.value) return
  const callbacks = readOnly ? {} : {
    onSave: () => { if (state.value?.dirty) fs.saveViewer(props.windowId) },
    onChange: (content) => { if (state.value) { state.value.content = content; state.value.dirty = true } },
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
  editBuffer.value = state.value.content
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
})
</script>

<template>
  <PlasmaWindow
    v-if="windowOpen && state"
    :window-id="windowId"
    :title="state.file.name"
    :icon="'<svg width=&quot;16&quot; height=&quot;16&quot; viewBox=&quot;0 0 24 24&quot; fill=&quot;none&quot; stroke=&quot;currentColor&quot; stroke-width=&quot;2&quot;><path d=&quot;M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z&quot;/><circle cx=&quot;12&quot; cy=&quot;12&quot; r=&quot;3&quot;/></svg>'"
    @close="handleClose"
  >
    <!-- Delegated viewers (audio, markdown, csv, font, xlsx, docx, epub, archive, notebook) -->
    <template v-if="delegatedViewer">
      <component :is="delegatedViewer" :state="state" :window-id="windowId" />
    </template>

    <!-- Inline viewers (image, video, pdf, text) -->
    <template v-else>
      <div class="viewer-toolbar">
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
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" />
                <polyline points="17 21 17 13 7 13 7 21" /><polyline points="7 3 7 8 15 8" />
              </svg>
              <span>{{ state.saving ? '...' : t('preview.save') }}</span>
            </button>
            <button class="viewer-btn" @click="cancelEdit">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
              </svg>
              <span>{{ t('preview.cancel') }}</span>
            </button>
          </template>
          <template v-else-if="canEdit">
            <button class="viewer-btn edit-btn" @click="startEdit">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
                <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
              </svg>
              <span>{{ t('preview.edit') }}</span>
            </button>
          </template>
          <template v-if="state.chunked && !state.isFullyLoaded && !state.editing">
            <span class="toolbar-sep" />
            <button class="viewer-btn" :disabled="state.page <= 0" @click="fs.viewerPrevPage(windowId)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="15 18 9 12 15 6"/></svg>
            </button>
            <span class="page-indicator">{{ state.page + 1 }} / ~{{ state.totalPages }}</span>
            <button class="viewer-btn" :disabled="state.page >= state.totalPages - 1" @click="fs.viewerNextPage(windowId)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
            </button>
          </template>
        </template>
      </div>

      <div class="viewer-body" :class="{ 'viewer-body-split': state.type === 'text' && isHtmlPreview && htmlPreviewMode === 'split' }">
        <img v-if="state.type === 'image' && state.url" :src="state.url" :alt="state.file.name" class="viewer-image" />
        <video v-else-if="state.type === 'video' && state.url" :src="state.url" controls autoplay playsinline class="viewer-video" />
        <iframe v-else-if="state.type === 'pdf' && state.url" :src="state.url" class="viewer-pdf" />
        <div v-else-if="state.type === 'text' && showCodePane" ref="cmContainer" class="viewer-cm-wrap" :class="{ 'viewer-pane': isHtmlPreview }" />
        <div v-if="state.type === 'text' && showRenderedPane" class="viewer-render-wrap" :class="{ 'viewer-pane': htmlPreviewMode === 'split' }">
          <iframe class="viewer-render-frame" :srcdoc="iframeSrcdoc" sandbox="allow-scripts allow-forms allow-modals" title="HTML Preview" />
        </div>
      </div>
    </template>
  </PlasmaWindow>
</template>

<style scoped>
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
}
.viewer-btn:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}
.viewer-btn:disabled {
  opacity: 0.4;
}
.viewer-btn.edit-btn {
  color: #8cb4ff;
}
.viewer-btn.save-btn {
  color: #5cb85c;
}
.viewer-btn.mode-btn.active {
  background: rgba(61, 174, 233, 0.18);
  color: #7cc7ff;
}

.toolbar-sep { width: 1px; height: 16px; background: var(--breeze-border); margin: 0 4px; flex-shrink: 0; }
.page-indicator { font-size: 12px; color: #999; white-space: nowrap; padding: 0 4px; }

.viewer-body {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 0;
  overflow: auto;
  background: var(--breeze-bg);
}

.viewer-body.viewer-body-split {
  align-items: stretch;
  justify-content: stretch;
  overflow: hidden;
}

.viewer-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  user-select: none;
}

.viewer-video {
  max-width: 100%;
  max-height: 100%;
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
}
.viewer-pane {
  flex: 1 1 50%;
  min-width: 0;
  min-height: 0;
}
.viewer-cm-wrap :deep(.cm-editor) {
  height: 100%;
}
.viewer-cm-wrap :deep(.cm-editor.cm-focused) {
  outline: none;
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

@media (max-width: 767px) {
  .viewer-toolbar { height: 44px; }
  .viewer-btn { min-height: 44px; padding: 8px 12px; font-size: 14px; }
}
</style>
