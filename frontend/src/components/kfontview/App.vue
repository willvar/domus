<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const sampleText = ref('')
const defaultSample = 'The quick brown fox jumps over the lazy dog'
const sizes = [12, 18, 24, 36, 48, 72]
const fontFamily = ref('sans-serif')
let loadedFace: FontFace | null = null

onMounted(async () => {
  if (!props.state.blob) return
  try {
    const name = `preview-font-${props.windowId.replace(/[^a-zA-Z0-9]/g, '')}`
    const buf = await props.state.blob.arrayBuffer()
    loadedFace = new FontFace(name, buf)
    await loadedFace.load()
    document.fonts.add(loadedFace)
    fontFamily.value = `'${name}'`
  } catch { /* font load failed */ }
})

onUnmounted(() => {
  if (loadedFace) {
    document.fonts.delete(loadedFace)
    loadedFace = null
  }
})
</script>

<template>
  <div class="viewer-toolbar">
    <input v-model="sampleText" class="font-sample-input" placeholder="Type to preview..." />
  </div>
  <div class="viewer-body font-body">
    <div class="font-samples">
      <div v-for="size in sizes" :key="size" class="font-sample-row">
        <span class="font-size-label">{{ size }}px</span>
        <span class="font-sample-text" :style="{ fontFamily, fontSize: size + 'px' }">
          {{ sampleText || defaultSample }}
        </span>
      </div>
      <div class="font-charset-section">
        <div class="font-charset" :style="{ fontFamily }">
          <div>ABCDEFGHIJKLMNOPQRSTUVWXYZ</div>
          <div>abcdefghijklmnopqrstuvwxyz</div>
          <div>0123456789</div>
          <div>!@#$%^&amp;*()_+-=[]{}|;':&quot;,.&lt;&gt;?</div>
        </div>
      </div>
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
  flex-shrink: 0;
}

.font-sample-input {
  flex: 1;
  background: $hover-white-light;
  border: 1px solid var(--breeze-border);
  color: #ccc;
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 13px;
  outline: none;

  &:focus { border-color: #3daee9; }
}

.viewer-body.font-body {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
  background: var(--breeze-bg);
  padding: 24px;
}

.font-samples { display: flex; flex-direction: column; gap: 20px; }
.font-sample-row { display: flex; align-items: baseline; gap: 16px; }
.font-size-label { color: #888; font-size: 12px; min-width: 44px; text-align: right; flex-shrink: 0; }
.font-sample-text { color: #ddd; word-break: break-word; }
.font-charset-section { margin-top: 16px; padding-top: 16px; border-top: 1px solid var(--breeze-border); }
.font-charset { color: #aaa; font-size: 24px; line-height: 1.8; letter-spacing: 2px; }
</style>
