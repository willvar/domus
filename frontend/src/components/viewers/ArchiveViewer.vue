<script setup>
import { ref, watch, computed } from 'vue'
import JSZip from 'jszip'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const entries = ref([])
const error = ref('')

function formatSize(bytes) {
  if (bytes === 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
}

function formatDate(d) {
  if (!d) return '-'
  return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

watch(() => props.state.blob, async (blob) => {
  if (!blob) return
  try {
    const zip = await JSZip.loadAsync(blob)
    const list = []
    zip.forEach((path, file) => {
      const depth = path.split('/').filter(Boolean).length - 1
      list.push({
        path,
        name: path,
        isDir: file.dir,
        size: file._data ? (file._data.uncompressedSize || 0) : 0,
        date: file.date,
        depth: Math.max(0, depth),
      })
    })
    list.sort((a, b) => a.path.localeCompare(b.path))
    entries.value = list
  } catch {
    error.value = 'Failed to read archive'
  }
}, { immediate: true })

const summary = computed(() => {
  const files = entries.value.filter(e => !e.isDir)
  const total = files.reduce((s, e) => s + e.size, 0)
  return `${files.length} files, ${formatSize(total)}`
})
</script>

<template>
  <div class="viewer-toolbar">
    <span class="toolbar-label">{{ summary }}</span>
  </div>
  <div class="viewer-body archive-body">
    <div v-if="error" class="archive-error">{{ error }}</div>
    <table v-else class="archive-table">
      <thead>
        <tr><th>Name</th><th>Size</th><th>Modified</th></tr>
      </thead>
      <tbody>
        <tr v-for="entry in entries" :key="entry.path" :class="{ 'is-dir': entry.isDir }">
          <td :style="{ paddingLeft: (entry.depth * 16 + 12) + 'px' }">
            <span class="entry-icon">{{ entry.isDir ? '\uD83D\uDCC1' : '\uD83D\uDCC4' }}</span>
            {{ entry.name }}
          </td>
          <td class="size-col">{{ entry.isDir ? '-' : formatSize(entry.size) }}</td>
          <td class="date-col">{{ formatDate(entry.date) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.viewer-toolbar {
  display: flex; align-items: center; height: 32px; padding: 0 12px;
  background: var(--breeze-bg-alt); border-bottom: 1px solid var(--breeze-border); flex-shrink: 0;
}
.toolbar-label { color: #bbb; font-size: 12px; }

.viewer-body.archive-body {
  flex: 1; display: flex; flex-direction: column; min-height: 0; overflow-y: auto; background: var(--breeze-bg);
}
.archive-error { padding: 24px; color: #da4453; text-align: center; }
.archive-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.archive-table th {
  text-align: left; padding: 6px 12px; color: #888; font-weight: 600; font-size: 11px; text-transform: uppercase;
  border-bottom: 1px solid var(--breeze-border); position: sticky; top: 0; background: var(--breeze-bg-alt);
}
.archive-table td { padding: 4px 12px; color: #ccc; border-bottom: 1px solid rgba(255,255,255,0.04); }
.archive-table tr:hover td { background: rgba(255,255,255,0.03); }
.archive-table tr.is-dir td { color: #7cc7ff; }
.entry-icon { margin-right: 6px; }
.size-col { white-space: nowrap; color: #888; width: 90px; }
.date-col { white-space: nowrap; color: #888; width: 160px; }
</style>
