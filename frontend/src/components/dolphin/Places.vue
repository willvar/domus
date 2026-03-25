<script setup>
import { ref, computed, onMounted, h } from 'vue'
import { NMenu } from 'naive-ui'
import IconHome from '~icons/mdi/home-outline'
import IconTrash from '~icons/mdi/delete-outline'
import IconFolderOutline from '~icons/mdi/folder-outline'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useAuthStore } from '../../stores/auth'
import { useI18n } from '../../composables/useI18n'
import { useWebSocket } from '../../composables/useWebSocket'

const fs = useFileSystemStore()
const auth = useAuthStore()
const ws = useWebSocket()
const { t } = useI18n()

const bookmarks = ref([])

onMounted(async () => {
  try {
    bookmarks.value = await ws.request('bookmark.list')
  } catch { /* bookmarks stay empty on error */ }
})

function icon(comp) {
  return () => h(comp, {
    width: 16,
    height: 16,
    style: 'margin-right:8px;opacity:0.7;',
  })
}

const menuOptions = computed(() => [
  {
    label: t('places.title'),
    key: 'places-header',
    type: 'group',
    children: [
      { label: t('places.home'), key: 'home', icon: icon(IconHome) },
      { label: t('places.trash'), key: 'trash', icon: icon(IconTrash) },
    ],
  },
])

function handleSelect(key) {
  if (key === 'home') {
    fs.navigate(`/home/${auth.username}/`)
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
        :options="bookmarks.map(b => ({ label: b.name, key: String(b.id), icon: icon(IconFolderOutline) }))"
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
