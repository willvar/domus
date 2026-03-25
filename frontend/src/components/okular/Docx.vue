<script setup>
import { ref, watch } from 'vue'
import mammoth from 'mammoth'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const renderedHtml = ref('')

watch(() => props.state.blob, async (blob) => {
  if (!blob) return
  try {
    const buf = await blob.arrayBuffer()
    const result = await mammoth.convertToHtml({ arrayBuffer: buf })
    renderedHtml.value = result.value
  } catch {
    renderedHtml.value = '<p style="color:red">Failed to render document.</p>'
  }
}, { immediate: true })
</script>

<template>
  <div class="viewer-toolbar">
    <span class="toolbar-label">{{ state.file.name }}</span>
  </div>
  <div class="viewer-body">
    <div class="docx-render-wrap">
      <div class="rendered-content" v-html="renderedHtml" />
    </div>
  </div>
</template>

<style scoped>
.viewer-toolbar {
  display: flex; align-items: center; height: 32px; padding: 0 12px;
  background: var(--breeze-bg-alt); border-bottom: 1px solid var(--breeze-border); flex-shrink: 0;
}
.toolbar-label { color: #bbb; font-size: 12px; }

.viewer-body { flex: 1; display: flex; min-height: 0; overflow: hidden; background: var(--breeze-bg); }
.docx-render-wrap { width: 100%; height: 100%; overflow-y: auto; background: #fff; }
.rendered-content {
  padding: 32px 40px; color: #1e1e1e; font-size: 15px; line-height: 1.7; max-width: 900px; margin: 0 auto;
}
.rendered-content :deep(h1) { font-size: 2em; margin: 1em 0 0.5em; }
.rendered-content :deep(h2) { font-size: 1.5em; margin: 1em 0 0.5em; }
.rendered-content :deep(h3) { font-size: 1.25em; margin: 1em 0 0.5em; }
.rendered-content :deep(p) { margin: 0.5em 0; }
.rendered-content :deep(table) { border-collapse: collapse; width: 100%; margin: 0.5em 0; }
.rendered-content :deep(th), .rendered-content :deep(td) { border: 1px solid #ddd; padding: 8px 12px; }
.rendered-content :deep(img) { max-width: 100%; }
.rendered-content :deep(ul), .rendered-content :deep(ol) { padding-left: 2em; }
</style>
