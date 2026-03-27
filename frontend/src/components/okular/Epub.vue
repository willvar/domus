<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import ePub from 'epubjs'
import { useI18n } from '../../composables/useI18n'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const { t } = useI18n()
const epubContainer = ref(null)
const toc = ref([])
const currentChapter = ref('')
let book = null
let rendition = null

onMounted(async () => {
  await nextTick()
  if (!props.state.url || !epubContainer.value) return
  try {
    book = ePub(props.state.url)
    rendition = book.renderTo(epubContainer.value, {
      width: '100%',
      height: '100%',
      spread: 'none',
    })

    rendition.themes.default({
      body: { background: '#202326 !important', color: '#fcfcfc !important', 'font-family': "'Noto Sans', sans-serif !important" },
      'a, a:link, a:visited': { color: '#3daee9 !important' },
      'h1, h2, h3, h4, h5, h6': { color: '#fcfcfc !important' },
      'p, li, td, th, span, div': { color: '#fcfcfc !important' },
    })

    rendition.display()

    const nav = await book.loaded.navigation
    toc.value = nav.toc || []
    if (toc.value.length) currentChapter.value = toc.value[0].href
  } catch { /* epub load failed */ }
})

function prev() { if (rendition) rendition.prev() }
function next() { if (rendition) rendition.next() }
function goToChapter() { if (rendition && currentChapter.value) rendition.display(currentChapter.value) }

onUnmounted(() => {
  if (book) { book.destroy(); book = null }
})
</script>

<template>
  <div class="viewer-toolbar">
    <button class="viewer-btn" @click="prev">{{ t('preview.prev') }}</button>
    <select v-if="toc.length" v-model="currentChapter" class="epub-toc-select" @change="goToChapter">
      <option v-for="item in toc" :key="item.href" :value="item.href">{{ item.label.trim() }}</option>
    </select>
    <button class="viewer-btn" @click="next">{{ t('preview.next') }}</button>
  </div>
  <div class="viewer-body epub-body">
    <div ref="epubContainer" class="epub-container" />
  </div>
</template>

<style scoped>
.viewer-toolbar {
  display: flex; align-items: center; height: 32px; padding: 0 8px;
  background: var(--breeze-bg-alt); border-bottom: 1px solid var(--breeze-border); gap: 6px; flex-shrink: 0;
}
.viewer-btn { background: none; border: none; color: #bbb; padding: 4px 8px; border-radius: 4px; font-size: 12px; }
.viewer-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }
@media (max-width: 767px) { .viewer-toolbar { height: 44px; } .viewer-btn { min-height: 44px; padding: 8px 12px; font-size: 14px; } .epub-toc-select { min-height: 44px; font-size: 14px; } }

.epub-toc-select {
  flex: 1; background: rgba(255,255,255,0.06); border: 1px solid var(--breeze-border);
  color: #ccc; padding: 3px 8px; border-radius: 4px; font-size: 12px; outline: none;
  max-width: 400px;
}
.epub-toc-select:focus { border-color: #3daee9; }

.viewer-body.epub-body {
  flex: 1; display: flex; min-height: 0; overflow: hidden; background: #202326;
}
.epub-container { width: 100%; height: 100%; }
</style>
