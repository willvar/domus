<script setup>
import { shallowRef, ref, computed, watch } from 'vue'
import { NDataTable, NPagination } from 'naive-ui'
import SpreadsheetWorker from './spreadsheet.worker.js?worker'

const PAGE_SIZE = 50

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

// raw sheets: [{ name, headers, rowCount, rows (2D array) }]
const sheets = shallowRef([])
const activeSheet = ref('')
const loading = ref(false)
const page = ref(1)

watch(() => props.state.blob, async (blob) => {
  if (!blob) return
  loading.value = true
  try {
    const buf = await blob.arrayBuffer()
    const data = await parseInWorker(buf)
    sheets.value = data
    activeSheet.value = data[0]?.name || ''
    page.value = 1
  } catch { /* parse error */ }
  loading.value = false
}, { immediate: true })

function parseInWorker(buf) {
  return new Promise((resolve, reject) => {
    const worker = new SpreadsheetWorker()
    worker.onmessage = (e) => {
      worker.terminate()
      const data = JSON.parse(e.data)
      if (data.error) reject(new Error(data.error))
      else resolve(data)
    }
    worker.onerror = (err) => { worker.terminate(); reject(err) }
    worker.postMessage(buf, [buf])
  })
}

const sheetNames = computed(() => sheets.value.map(s => s.name))
const current = computed(() => sheets.value.find(s => s.name === activeSheet.value))

const columns = computed(() => {
  const s = current.value
  if (!s) return []
  return s.headers.map((h, i) => ({
    title: h,
    key: String(i),
    ellipsis: { tooltip: true },
    resizable: true,
  }))
})

// Only convert the current page slice to row objects
const pageRows = computed(() => {
  const s = current.value
  if (!s) return []
  const start = (page.value - 1) * PAGE_SIZE
  const slice = s.rows.slice(start, start + PAGE_SIZE)
  return slice.map((cells, ri) => {
    const obj = { _key: start + ri }
    for (let i = 0; i < cells.length; i++) obj[String(i)] = cells[i]
    return obj
  })
})

const totalRows = computed(() => current.value?.rowCount || 0)

function onSwitchSheet(name) {
  activeSheet.value = name
  page.value = 1
}
</script>

<template>
  <div class="viewer-toolbar">
    <button
      v-for="name in sheetNames" :key="name"
      class="viewer-btn mode-btn"
      :class="{ active: activeSheet === name }"
      @click="onSwitchSheet(name)"
    >
      {{ name }}
    </button>
    <div v-if="totalRows > PAGE_SIZE" class="toolbar-pagination">
      <NPagination
        :page="page"
        :page-size="PAGE_SIZE"
        :item-count="totalRows"
        :page-slot="5"
        size="small"
        @update:page="p => page = p"
      />
    </div>
  </div>
  <div class="viewer-body">
    <div v-if="loading" style="display:flex;align-items:center;justify-content:center;width:100%;height:100%;color:#999;">
      Loading...
    </div>
    <div v-else class="xlsx-table-wrap">
      <NDataTable
        v-if="columns.length"
        :columns="columns"
        :data="pageRows"
        :row-key="(r) => r._key"
        :max-height="9999"
        virtual-scroll
        size="small"
        striped
      />
    </div>
  </div>
</template>

<style scoped>
.viewer-toolbar {
  display: flex; align-items: center; height: 32px; padding: 0 8px;
  background: var(--breeze-bg-alt); border-bottom: 1px solid var(--breeze-border); gap: 4px; flex-shrink: 0;
  overflow-x: auto;
}
.toolbar-pagination { margin-left: auto; flex-shrink: 0; }
.viewer-btn { background: none; border: none; color: #bbb; padding: 4px 8px; border-radius: 4px; font-size: 12px; white-space: nowrap; }
.viewer-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }
@media (max-width: 767px) { .viewer-toolbar { height: 44px; } .viewer-btn { min-height: 44px; padding: 8px 12px; font-size: 14px; } }
.viewer-btn.mode-btn.active { background: rgba(61,174,233,0.18); color: #7cc7ff; }

.viewer-body { flex: 1; display: flex; min-height: 0; overflow: hidden; background: var(--breeze-bg); }
.xlsx-table-wrap { width: 100%; height: 100%; overflow: auto; }
</style>
