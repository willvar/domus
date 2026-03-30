<script setup>
import { ref, computed, watch, nextTick, onUnmounted } from 'vue'
import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'
import { useCodeMirror } from '../../composables/useCodeMirror'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const md = new MarkdownIt({ html: false, linkify: true })
const notebook = ref(null)
const codeContainers = ref({})

const cells = computed(() => notebook.value?.cells || [])
const cellCount = computed(() => cells.value.length)
const language = computed(() => {
  const info = notebook.value?.metadata?.kernelspec?.language
    || notebook.value?.metadata?.language_info?.name
    || 'python'
  return info.toLowerCase()
})

watch(() => props.state.content, (content) => {
  if (!content) return
  try { notebook.value = JSON.parse(content) } catch { notebook.value = null }
}, { immediate: true })

// CodeMirror instances for code cells
const cmInstances = []

function setCodeRef(i, el) {
  codeContainers.value[i] = el
}

watch(cells, async () => {
  // Cleanup old instances
  cmInstances.forEach(cm => cm.destroy())
  cmInstances.length = 0

  await nextTick()

  cells.value.forEach((cell, i) => {
    if (cell.cell_type !== 'code') return
    const container = codeContainers.value[i]
    if (!container) return
    const cm = useCodeMirror()
    const source = Array.isArray(cell.source) ? cell.source.join('') : (cell.source || '')
    cm.create(container, source, language.value, true)
    cmInstances.push(cm)
  })
}, { immediate: false })

// Trigger after mount
watch(() => notebook.value, async () => {
  await nextTick()
  await nextTick()
  cmInstances.forEach(cm => cm.destroy())
  cmInstances.length = 0
  cells.value.forEach((cell, i) => {
    if (cell.cell_type !== 'code') return
    const container = codeContainers.value[i]
    if (!container) return
    const cm = useCodeMirror()
    const source = Array.isArray(cell.source) ? cell.source.join('') : (cell.source || '')
    cm.create(container, source, language.value, true)
    cmInstances.push(cm)
  })
})

function renderMarkdown(source) {
  const text = Array.isArray(source) ? source.join('') : (source || '')
  return DOMPurify.sanitize(md.render(text))
}

function getOutputHtml(output) {
  if (output.output_type === 'stream') {
    const text = Array.isArray(output.text) ? output.text.join('') : (output.text || '')
    return `<pre class="nb-stream">${escapeHtml(text)}</pre>`
  }
  if (output.output_type === 'error') {
    const tb = (output.traceback || []).join('\n')
    return `<pre class="nb-error">${escapeHtml(tb)}</pre>`
  }
  const data = output.data || {}
  if (data['text/html']) {
    const html = Array.isArray(data['text/html']) ? data['text/html'].join('') : data['text/html']
    return `<div class="nb-html-output">${DOMPurify.sanitize(html)}</div>`
  }
  if (data['image/png']) {
    return `<img src="data:image/png;base64,${data['image/png']}" class="nb-img" />`
  }
  if (data['image/jpeg']) {
    return `<img src="data:image/jpeg;base64,${data['image/jpeg']}" class="nb-img" />`
  }
  if (data['text/plain']) {
    const text = Array.isArray(data['text/plain']) ? data['text/plain'].join('') : data['text/plain']
    return `<pre class="nb-stream">${escapeHtml(text)}</pre>`
  }
  return ''
}

function escapeHtml(str) {
  return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

onUnmounted(() => {
  cmInstances.forEach(cm => cm.destroy())
  cmInstances.length = 0
})
</script>

<template>
  <div class="viewer-toolbar">
    <span class="toolbar-label">{{ cellCount }} cells</span>
  </div>
  <div class="viewer-body nb-body">
    <div v-for="(cell, i) in cells" :key="i" class="nb-cell" :class="'nb-cell-' + cell.cell_type">
      <!-- Markdown cell -->
      <div v-if="cell.cell_type === 'markdown'" class="nb-markdown" v-html="renderMarkdown(cell.source)" />
      <!-- Code cell -->
      <template v-else-if="cell.cell_type === 'code'">
        <div :ref="(el) => setCodeRef(i, el)" class="nb-code-input" />
        <div v-for="(output, j) in (cell.outputs || [])" :key="j" class="nb-output" v-html="getOutputHtml(output)" />
      </template>
      <!-- Raw cell -->
      <pre v-else class="nb-raw">{{ Array.isArray(cell.source) ? cell.source.join('') : cell.source }}</pre>
    </div>
  </div>
</template>

<style scoped>
.viewer-toolbar {
  display: flex; align-items: center; height: 32px; padding: 0 12px;
  background: var(--breeze-bg-alt); border-bottom: 1px solid var(--breeze-border); flex-shrink: 0;
}
.toolbar-label { color: #bbb; font-size: 12px; }

.viewer-body.nb-body {
  flex: 1; display: flex; flex-direction: column; min-height: 0; overflow-y: auto;
  background: var(--breeze-bg); padding: 16px; gap: 4px;
}

.nb-cell {
  border-left: 3px solid transparent; border-radius: 4px;
  background: rgba(255,255,255,0.02);
}
.nb-cell-code { border-left-color: #3daee9; }
.nb-cell-markdown { border-left-color: #27ae60; }
.nb-cell-raw { border-left-color: #888; }

.nb-markdown { padding: 12px 16px; color: #ccc; line-height: 1.6; }
.nb-markdown :deep(h1) { font-size: 1.6em; color: #eee; margin: 0.5em 0 0.3em; }
.nb-markdown :deep(h2) { font-size: 1.3em; color: #eee; margin: 0.5em 0 0.3em; }
.nb-markdown :deep(h3) { font-size: 1.1em; color: #eee; margin: 0.5em 0 0.3em; }
.nb-markdown :deep(p) { margin: 0.4em 0; }
.nb-markdown :deep(code) { background: rgba(255,255,255,0.08); padding: 2px 5px; border-radius: 3px; font-size: 0.9em; }
.nb-markdown :deep(pre) { background: #282c34; padding: 12px; border-radius: 4px; overflow-x: auto; }
.nb-markdown :deep(pre code) { background: none; padding: 0; }
.nb-markdown :deep(a) { color: #3daee9; }
.nb-markdown :deep(table) { border-collapse: collapse; }
.nb-markdown :deep(th), .nb-markdown :deep(td) { border: 1px solid #555; padding: 6px 10px; }
.nb-markdown :deep(img) { max-width: 100%; }

.nb-code-input { overflow: hidden; }
.nb-code-input :deep(.cm-editor) { border-radius: 0; }
.nb-code-input :deep(.cm-editor.cm-focused) { outline: none; }

.nb-output { padding: 8px 16px; }
.nb-output :deep(.nb-stream) { color: #bbb; font-size: 13px; margin: 0; white-space: pre-wrap; word-break: break-all; }
.nb-output :deep(.nb-error) { color: #e06c75; font-size: 13px; margin: 0; white-space: pre-wrap; }
.nb-output :deep(.nb-img) { max-width: 100%; margin: 4px 0; }
.nb-output :deep(.nb-html-output) { color: #ccc; overflow-x: auto; }

.nb-raw { color: #999; padding: 12px 16px; margin: 0; font-size: 13px; }
</style>
