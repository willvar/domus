<script setup>
import { ref, watch, nextTick } from 'vue'
import { NInput, NDropdown } from 'naive-ui'
import { useFileSystemStore } from '../stores/fileSystem'
import { useAuthStore } from '../stores/auth'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'

const fs = useFileSystemStore()
const auth = useAuthStore()
const { t } = useI18n()
const editing = ref(false)
const editPath = ref('')
const inputRef = ref(null)

const subDirs = ref([])

watch(() => fs.focusPathBar, (val) => {
  if (val) {
    startEdit()
    fs.focusPathBar = false
  }
})

function startEdit() {
  editing.value = true
  editPath.value = '/' + (fs.currentPath || '')
  nextTick(() => inputRef.value?.focus())
}

function commitEdit() {
  editing.value = false
  let p = editPath.value.replace(/^\/+/, '')
  if (p !== fs.currentPath) {
    fs.navigate(p)
  }
}

function cancelEdit() {
  editing.value = false
}

async function loadSubDirs(parentPath) {
  try {
    const res = await api.get('/file', { params: { path: parentPath } })
    const dirs = (res.data.files || []).filter(f => f.is_dir)
    subDirs.value = dirs.map(d => ({ label: d.name, key: d.path }))
  } catch {
    subDirs.value = []
  }
}

function handleSubDirSelect(path) {
  fs.navigate(path)
}
</script>

<template>
  <div class="breadcrumb-bar" @click="startEdit">
    <template v-if="editing">
      <NInput
        ref="inputRef"
        v-model:value="editPath"
        size="small"
        class="path-input"
        :placeholder="t('breadcrumb.path_placeholder')"
        @keyup.enter="commitEdit"
        @keyup.escape="cancelEdit"
        @blur="commitEdit"
      />
    </template>
    <template v-else-if="fs.isTrash">
      <div class="breadcrumb-segments">
        <span class="breadcrumb-segment" @click.stop="fs.navigate(auth.username + '/')">
          /
        </span>
        <span class="breadcrumb-sep">›</span>
        <span class="breadcrumb-segment active">
          {{ t('places.trash') }}
        </span>
      </div>
    </template>
    <template v-else>
      <div class="breadcrumb-segments">
        <span class="breadcrumb-segment" @click.stop="fs.navigate(auth.username + '/')">
          /
        </span>
        <template v-for="(seg, i) in fs.pathSegments" :key="seg.path">
          <NDropdown
            trigger="click"
            :options="subDirs"
            @select="handleSubDirSelect"
            @update:show="(show) => show && loadSubDirs(i === 0 ? '' : fs.pathSegments[i-1].path)"
          >
            <span class="breadcrumb-sep" @click.stop>›</span>
          </NDropdown>
          <span
            class="breadcrumb-segment"
            :class="{ active: i === fs.pathSegments.length - 1 }"
            @click.stop="fs.navigate(seg.path)"
          >
            {{ seg.name }}
          </span>
        </template>
      </div>
    </template>
  </div>
</template>

<style scoped>
.breadcrumb-bar {
  display: flex;
  align-items: center;
  height: 30px;
  padding: 0 var(--gap-sm);
  background: var(--breeze-surface);
  border-bottom: 1px solid var(--breeze-border);
  flex-shrink: 0;
}
.path-input { flex: 1; }
.breadcrumb-segments {
  display: flex;
  align-items: center;
  gap: 0;
  overflow: hidden;
  flex: 1;
}
.breadcrumb-segment {
  padding: 2px 6px;
  border-radius: 3px;
  white-space: nowrap;
  font-size: var(--font-size-sm);
  color: var(--breeze-text-secondary);
}
.breadcrumb-segment:hover {
  color: var(--breeze-text);
  background: var(--breeze-hover);
}
.breadcrumb-segment.active {
  color: var(--breeze-text);
  font-weight: 500;
}
.breadcrumb-sep {
  color: var(--breeze-text-disabled);
  font-size: 14px;
  padding: 2px 1px;
  border-radius: 3px;
}
.breadcrumb-sep:hover {
  color: var(--breeze-text-secondary);
  background: var(--breeze-hover);
}

@media (max-width: 767px) {
  .breadcrumb-bar { height: 44px; }
  .breadcrumb-segment { padding: 8px 12px; font-size: 14px; }
  .breadcrumb-sep { padding: 8px 4px; }
}
</style>
