<script setup lang="ts">
import { ref, shallowRef, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import type MarkdownIt from 'markdown-it'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useCodeMirror } from '../../composables/useCodeMirror'
import { IconChevronLeft as IconPrev, IconChevronRight as IconNext } from '../../barrels/icons'
import { useI18n } from '../../composables/useI18n'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const fs = useFileSystemStore()
const { t } = useI18n()
const cm = useCodeMirror()
const cmContainer = ref<HTMLDivElement | null>(null)
const mdWrap = ref<HTMLDivElement | null>(null)
const editBuffer = ref('')
const mode = ref(window.innerWidth < 768 ? 'preview' : 'split')

const md = shallowRef<MarkdownIt | null>(null)
onMounted(async () => {
  const { default: MarkdownIt } = await import('markdown-it')
  md.value = new MarkdownIt({ html: false, linkify: true, typographer: true })
})

const renderedHtml = computed(() => {
  if (!props.state.content || !md.value) return ''
  return md.value.render(props.state.content)
})

const showCode = computed(() => mode.value !== 'preview')
const showPreview = computed(() => mode.value !== 'code')

function createEditor(readOnly: boolean) {
  if (!cmContainer.value || !props.state) return
  const callbacks = readOnly ? {} : {
    onSave: () => { if (props.state.dirty) fs.saveViewer(props.windowId) },
    onChange: (content: string) => { props.state.content = content; props.state.dirty = true },
  }
  cm.create(cmContainer.value, props.state.content || '', 'markdown', readOnly, callbacks)
}

const canEdit = computed(() => {
  if (props.state.file?.path?.startsWith('/__trash__/')) return false
  if (props.state.file?._shareId && props.state.file?._permission !== 'write') return false
  return !props.state.chunked || props.state.isFullyLoaded
})

watch(() => props.state.content !== null, (ready) => {
  if (ready && !props.state.editing) nextTick(() => { if (!cm.view.value) createEditor(true) })
}, { immediate: true })

watch(() => props.state.page, () => {
  if (props.state.content !== null && !props.state.editing) {
    if (cm.view.value) cm.replaceContent(props.state.content)
    else if (showCode.value) nextTick(() => createEditor(true))
    nextTick(() => { if (mdWrap.value) mdWrap.value.scrollTop = 0 })
  }
})

watch(() => props.state.editing, (editing) => {
  if (editing) {
    editBuffer.value = props.state.content || ''
    nextTick(() => createEditor(false))
  } else {
    nextTick(() => createEditor(true))
  }
})

watch(showCode, (visible) => {
  if (!visible) { cm.destroy(); return }
  if (props.state.content !== null) nextTick(() => createEditor(!props.state.editing))
}, { immediate: true })

function startEdit() {
  editBuffer.value = props.state.content
  props.state.editing = true
  props.state.dirty = false
}

function cancelEdit() {
  props.state.content = editBuffer.value
  props.state.editing = false
  props.state.dirty = false
}

onUnmounted(() => cm.destroy())
</script>

<template>
  <div class="viewer-toolbar">
    <div class="viewer-mode-group">
      <button class="viewer-btn mode-btn" :class="{ active: mode === 'code' }" @click="mode = 'code'">
        <span>{{ t('preview.code') }}</span>
      </button>
      <button class="viewer-btn mode-btn" :class="{ active: mode === 'preview' }" @click="mode = 'preview'">
        <span>{{ t('preview.rendered') }}</span>
      </button>
      <button class="viewer-btn mode-btn" :class="{ active: mode === 'split' }" @click="mode = 'split'">
        <span>{{ t('preview.split') }}</span>
      </button>
    </div>
    <template v-if="state.editing">
      <button class="viewer-btn save-btn" :disabled="state.saving || !state.dirty" @click="fs.saveViewer(windowId)">
        <span>{{ state.saving ? '...' : t('preview.save') }}</span>
      </button>
      <button class="viewer-btn" @click="cancelEdit"><span>{{ t('preview.cancel') }}</span></button>
    </template>
    <template v-else-if="canEdit">
      <button class="viewer-btn edit-btn" @click="startEdit"><span>{{ t('preview.edit') }}</span></button>
    </template>
    <template v-if="state.chunked && !state.isFullyLoaded && !state.editing">
      <span class="toolbar-sep" />
      <button class="viewer-btn" :disabled="state.page <= 0" @click="fs.viewerPrevPage(windowId)">
        <IconPrev width="14" height="14" />
      </button>
      <span class="page-indicator">{{ state.page + 1 }} / ~{{ state.totalPages }}</span>
      <button class="viewer-btn" :disabled="state.page >= state.totalPages - 1" @click="fs.viewerNextPage(windowId)">
        <IconNext width="14" height="14" />
      </button>
    </template>
  </div>
  <div class="viewer-body" :class="{ 'viewer-body-split': mode === 'split' }">
    <div v-if="showCode" ref="cmContainer" class="viewer-cm-wrap" :class="{ 'viewer-pane': mode === 'split' }" />
    <div v-if="showPreview" ref="mdWrap" class="md-render-wrap" :class="{ 'viewer-pane': mode === 'split' }">
      <div class="markdown-body" v-html="renderedHtml" />
    </div>
  </div>
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

  &:active { background: $hover-white-strong; color: #fff; }
  @include hover { background: $hover-white-strong; color: #fff; }
  &:disabled { opacity: 0.4; }
  &.edit-btn { color: #8cb4ff; }
  &.save-btn { color: #5cb85c; }
  &.mode-btn.active { background: rgba(61, 174, 233, 0.18); color: #7cc7ff; }
}

.toolbar-sep { width: 1px; height: 16px; background: var(--breeze-border); margin: 0 4px; flex-shrink: 0; }
.page-indicator { font-size: 12px; color: #999; white-space: nowrap; padding: 0 4px; }

.viewer-body {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: auto;
  background: var(--breeze-bg);

  &.viewer-body-split { overflow: hidden; }
}

.viewer-pane { flex: 1 1 50%; min-width: 0; min-height: 0; }

.viewer-cm-wrap {
  width: 100%;
  height: 100%;
  overflow: hidden;

  :deep(.cm-editor) {
    height: 100%;

    &.cm-focused { outline: none; }
  }
}

.md-render-wrap {
  width: 100%;
  height: 100%;
  overflow-y: auto;
  background: #fff;
}

.markdown-body {
  padding: 24px 32px;
  color: #1e1e1e;
  font-size: 15px;
  line-height: 1.7;
  max-width: 900px;
  margin: 0 auto;

  :deep(h1) { font-size: 2em; border-bottom: 1px solid #eee; padding-bottom: 0.3em; margin: 1em 0 0.5em; }
  :deep(h2) { font-size: 1.5em; border-bottom: 1px solid #eee; padding-bottom: 0.3em; margin: 1em 0 0.5em; }
  :deep(h3) { font-size: 1.25em; margin: 1em 0 0.5em; }
  :deep(p) { margin: 0.5em 0; }
  :deep(code) { background: #f0f0f0; padding: 2px 6px; border-radius: 3px; font-size: 0.9em; }
  :deep(pre) { background: #282c34; color: #abb2bf; padding: 16px; border-radius: 6px; overflow-x: auto; }
  :deep(pre code) { background: none; padding: 0; color: inherit; }
  :deep(blockquote) { border-left: 4px solid #3daee9; padding: 0.5em 1em; margin: 0.5em 0; color: #555; background: #f8f8f8; }
  :deep(table) { border-collapse: collapse; width: 100%; margin: 0.5em 0; }
  :deep(th), :deep(td) { border: 1px solid #ddd; padding: 8px 12px; text-align: left; }
  :deep(th) { background: #f0f0f0; font-weight: 600; }
  :deep(img) { max-width: 100%; }
  :deep(a) { color: #3daee9; }
  :deep(ul), :deep(ol) { padding-left: 2em; }
  :deep(hr) { border: none; border-top: 1px solid #eee; margin: 1.5em 0; }
}

@include mobile {
  .viewer-toolbar { height: 44px; }
  .viewer-btn { min-height: 44px; padding: 8px 12px; font-size: 14px; }
  .viewer-body.viewer-body-split { flex-direction: column; }
  .viewer-pane { flex: 1 1 50%; min-height: 0; }
  .markdown-body { padding: 16px; }
}
</style>
