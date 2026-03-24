<script setup>
import { ref, computed, watch, nextTick, onUnmounted } from 'vue'
import Papa from 'papaparse'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useCodeMirror } from '../../composables/useCodeMirror'
import { useI18n } from '../../composables/useI18n'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const fs = useFileSystemStore()
const { t } = useI18n()
const cm = useCodeMirror()
const cmContainer = ref(null)
const csvTableWrap = ref(null)
const editBuffer = ref('')
const mode = ref('table')
const sortKey = ref(null)
const sortAsc = ref(true)
const csvHeader = ref(null)
const canEdit = computed(() => !props.state.chunked || props.state.isFullyLoaded)

// Cache header row from first page
watch(() => props.state.content, (content) => {
  if (!content) return
  if (props.state.page === 0 || !csvHeader.value) {
    const firstLine = content.split('\n')[0]
    if (firstLine) {
      const result = Papa.parse(firstLine, { header: false })
      csvHeader.value = result.data[0] || []
    }
  }
}, { immediate: true })

const parsed = computed(() => {
  if (!props.state.content) return { headers: [], rows: [] }
  let text = props.state.content
  // For subsequent pages, prepend cached header so PapaParse labels columns
  if (props.state.chunked && props.state.page > 0 && csvHeader.value) {
    text = csvHeader.value.join(',') + '\n' + text
  }
  const result = Papa.parse(text, { header: true, skipEmptyLines: true })
  return { headers: result.meta.fields || [], rows: result.data }
})

const sortedRows = computed(() => {
  const { rows } = parsed.value
  if (!sortKey.value) return rows
  const key = sortKey.value
  const dir = sortAsc.value ? 1 : -1
  return [...rows].sort((a, b) => {
    const va = a[key] ?? '', vb = b[key] ?? ''
    const na = Number(va), nb = Number(vb)
    if (!isNaN(na) && !isNaN(nb)) return (na - nb) * dir
    return String(va).localeCompare(String(vb)) * dir
  })
})

function toggleSort(key) {
  if (sortKey.value === key) { sortAsc.value = !sortAsc.value }
  else { sortKey.value = key; sortAsc.value = true }
}

function createEditor(readOnly) {
  if (!cmContainer.value || !props.state) return
  const callbacks = readOnly ? {} : {
    onSave: () => { if (props.state.dirty) fs.saveViewer(props.windowId) },
    onChange: (content) => { props.state.content = content; props.state.dirty = true },
  }
  cm.create(cmContainer.value, props.state.content || '', null, readOnly, callbacks)
}

watch(() => mode.value === 'code' && props.state.content !== null, (show) => {
  if (show) nextTick(() => createEditor(!props.state.editing))
  else cm.destroy()
}, { immediate: true })

watch(() => props.state.editing, (editing) => {
  if (mode.value !== 'code') return
  if (editing) {
    editBuffer.value = props.state.content || ''
    nextTick(() => createEditor(false))
  } else {
    nextTick(() => createEditor(true))
  }
})

watch(() => props.state.page, () => {
  if (props.state.content !== null && !props.state.editing) {
    if (mode.value === 'code') {
      if (cm.view.value) cm.replaceContent(props.state.content)
      else nextTick(() => createEditor(true))
    }
    nextTick(() => { if (csvTableWrap.value) csvTableWrap.value.scrollTop = 0 })
  }
})

function startEdit() {
  mode.value = 'code'
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
      <button class="viewer-btn mode-btn" :class="{ active: mode === 'table' }" @click="mode = 'table'">
        <span>{{ t('preview.table') }}</span>
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
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="15 18 9 12 15 6"/></svg>
      </button>
      <span class="page-indicator">{{ state.page + 1 }} / ~{{ state.totalPages }}</span>
      <button class="viewer-btn" :disabled="state.page >= state.totalPages - 1" @click="fs.viewerNextPage(windowId)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
      </button>
    </template>
  </div>
  <div class="viewer-body">
    <div v-if="mode === 'code'" ref="cmContainer" class="viewer-cm-wrap" />
    <div v-else ref="csvTableWrap" class="csv-table-wrap">
      <table class="csv-table">
        <thead>
          <tr>
            <th v-for="h in parsed.headers" :key="h" @click="toggleSort(h)">
              {{ h }}
              <span v-if="sortKey === h" class="sort-arrow">{{ sortAsc ? '\u25B2' : '\u25BC' }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, i) in sortedRows" :key="i">
            <td v-for="h in parsed.headers" :key="h">{{ row[h] }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.viewer-toolbar {
  display: flex; align-items: center; height: 32px; padding: 0 8px;
  background: var(--breeze-bg-alt); border-bottom: 1px solid var(--breeze-border); gap: 6px; flex-shrink: 0;
}
.viewer-mode-group {
  display: flex; align-items: center; gap: 4px; margin-right: 8px; padding-right: 8px;
  border-right: 1px solid var(--breeze-border);
}
.viewer-btn { background: none; border: none; color: #bbb; padding: 4px 8px; border-radius: 4px; display: flex; align-items: center; gap: 4px; font-size: 12px; }
.viewer-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }
.viewer-btn:disabled { opacity: 0.4; }
.viewer-btn.edit-btn { color: #8cb4ff; }
@media (max-width: 767px) { .viewer-toolbar { height: 44px; } .viewer-btn { min-height: 44px; padding: 8px 12px; font-size: 14px; } }
.viewer-btn.save-btn { color: #5cb85c; }
.viewer-btn.mode-btn.active { background: rgba(61,174,233,0.18); color: #7cc7ff; }
.toolbar-sep { width: 1px; height: 16px; background: var(--breeze-border); margin: 0 4px; flex-shrink: 0; }
.page-indicator { font-size: 12px; color: #999; white-space: nowrap; padding: 0 4px; }

.viewer-body { flex: 1; display: flex; min-height: 0; overflow: hidden; background: var(--breeze-bg); }
.viewer-cm-wrap { width: 100%; height: 100%; overflow: hidden; }
.viewer-cm-wrap :deep(.cm-editor) { height: 100%; }
.viewer-cm-wrap :deep(.cm-editor.cm-focused) { outline: none; }

.csv-table-wrap { width: 100%; height: 100%; overflow: auto; }
.csv-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.csv-table th {
  text-align: left; padding: 6px 12px; color: #aaa; font-weight: 600; font-size: 12px;
  border-bottom: 1px solid var(--breeze-border); position: sticky; top: 0;
  background: var(--breeze-bg-alt); cursor: pointer; white-space: nowrap; user-select: none;
}
.csv-table th:hover { color: #fff; }
.sort-arrow { margin-left: 4px; font-size: 10px; }
.csv-table td {
  padding: 4px 12px; color: #ccc; border-bottom: 1px solid rgba(255,255,255,0.04);
  white-space: nowrap; max-width: 300px; overflow: hidden; text-overflow: ellipsis;
}
.csv-table tbody tr:hover td { background: rgba(255,255,255,0.03); }
.csv-table tbody tr:nth-child(even) td { background: rgba(255,255,255,0.015); }
.csv-table tbody tr:nth-child(even):hover td { background: rgba(255,255,255,0.04); }
</style>
