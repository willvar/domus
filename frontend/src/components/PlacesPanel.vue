<script setup>
import { ref, computed, onMounted, h } from 'vue'
import { NMenu } from 'naive-ui'
import { useFileSystemStore } from '../stores/fileSystem'
import { useAuthStore } from '../stores/auth'
import { useI18n } from '../composables/useI18n'
import api from '../composables/useApi'

const fs = useFileSystemStore()
const auth = useAuthStore()
const { t } = useI18n()

const bookmarks = ref([])

onMounted(async () => {
  try {
    const res = await api.get('/bookmarks')
    bookmarks.value = res.data
  } catch { /* bookmarks stay empty on error */ }
})

function icon(svg) {
  return () => h('span', {
    innerHTML: svg,
    style: 'display:inline-flex;align-items:center;margin-right:8px;opacity:0.7;',
  })
}

const homeIcon = `<svg width="16" height="16" viewBox="0 0 16 16"><path d="M2 8l6-5 6 5v5.5a.5.5 0 01-.5.5h-3v-4H5.5v4h-3a.5.5 0 01-.5-.5V8z" fill="currentColor"/></svg>`
const trashIcon = `<svg width="16" height="16" viewBox="0 0 16 16"><path d="M5 2V1h6v1h3v1.5H2V2h3zM3 5h10l-.8 9H3.8L3 5z" fill="currentColor"/></svg>`
const folderIcon = `<svg width="16" height="16" viewBox="0 0 16 16"><path d="M2 4c0-.6.4-1 1-1h3l1.5 1.5H13c.6 0 1 .4 1 1V12c0 .6-.4 1-1 1H3c-.6 0-1-.4-1-1V4z" fill="#4d9de8"/></svg>`

const menuOptions = computed(() => [
  {
    label: t('places.title'),
    key: 'places-header',
    type: 'group',
    children: [
      { label: t('places.home'), key: 'home', icon: icon(homeIcon) },
      { label: t('places.trash'), key: 'trash', icon: icon(trashIcon) },
    ],
  },
])

function handleSelect(key) {
  if (key === 'home') {
    fs.navigate(auth.username + '/')
  } else if (key === 'trash') {
    fs.navigate('__trash__')
  } else {
    const bm = bookmarks.value.find(b => String(b.id) === key)
    if (bm) fs.navigate(bm.path)
  }
}
</script>

<template>
  <div class="places-panel">
    <NMenu
      :options="menuOptions"
      :root-indent="12"
      :indent="12"
      @update:value="handleSelect"
    />
    <template v-if="bookmarks.length > 0">
      <div class="section-header">{{ t('places.bookmarks') }}</div>
      <NMenu
        :options="bookmarks.map(b => ({ label: b.name, key: String(b.id), icon: icon(folderIcon) }))"
        :root-indent="12"
        @update:value="handleSelect"
      />
    </template>
  </div>
</template>

<style scoped>
.places-panel {
  width: var(--sidebar-width);
  min-width: var(--sidebar-width);
  background: var(--sidebar-bg);
  border-right: 1px solid var(--breeze-border);
  overflow-y: auto;
  padding: var(--gap-xs) 0;
  flex-shrink: 0;
}
.section-header {
  padding: var(--gap-sm) var(--gap-md);
  font-size: var(--font-size-xs);
  color: var(--breeze-text-disabled);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 600;
}

@media (max-width: 768px) {
  .places-panel {
    position: fixed;
    left: 0;
    top: 0;
    bottom: 0;
    z-index: 100;
    box-shadow: 4px 0 16px rgba(0,0,0,0.4);
    width: min(220px, 75vw);
  }
}
</style>
