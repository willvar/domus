<script setup>
import { computed } from 'vue'
import IconHome from '~icons/mdi/home-outline'
import IconTrash from '~icons/mdi/delete-outline'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useAuthStore } from '../../stores/auth'
import { useI18n } from '../../composables/useI18n'

const fs = useFileSystemStore()
const auth = useAuthStore()
const { t } = useI18n()

const activeKey = computed(() => {
  if (fs.isTrash) return 'trash'
  const homePath = `/home/${auth.username}/`
  if (fs.currentPath === homePath || fs.currentPath === `home/${auth.username}/`) return 'home'
  return null
})

function handleSelect(key) {
  if (key === 'home') {
    fs.navigate(`/home/${auth.username}/`)
  } else if (key === 'trash') {
    fs.navigate('__trash__')
  }
}
</script>

<template>
  <div class="places-panel">
    <div class="section-header">{{ t('places.title') }}</div>
    <div class="places-list">
      <button
        class="place-item"
        :class="{ active: activeKey === 'home' }"
        @click="handleSelect('home')"
      >
        <IconHome class="place-icon" width="16" height="16" />
        <span class="place-label">{{ t('places.home') }}</span>
      </button>
      <button
        class="place-item"
        :class="{ active: activeKey === 'trash' }"
        @click="handleSelect('trash')"
      >
        <IconTrash class="place-icon" width="16" height="16" />
        <span class="place-label">{{ t('places.trash') }}</span>
      </button>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.places-panel {
  width: var(--sidebar-width);
  min-width: var(--sidebar-width);
  background: var(--sidebar-bg);
  border-right: 1px solid var(--breeze-border);
  overflow-y: auto;
  padding: var(--gap-xs) 0;
  flex-shrink: 0;

  @include mobile {
    position: fixed;
    left: 0;
    top: 0;
    bottom: 0;
    z-index: 100;
    box-shadow: 4px 0 16px rgba(0, 0, 0, 0.4);
    width: min(220px, 75vw);
  }
}

.section-header {
  padding: var(--gap-sm) var(--gap-md);
  font-size: var(--font-size-xs);
  color: var(--breeze-text-disabled);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 600;
}

.places-list {
  display: flex;
  flex-direction: column;
  padding: 0 6px;
}

.place-item {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 30px;
  padding: 0 8px;
  border: none;
  border-radius: 3px;
  background: none;
  color: var(--breeze-text-secondary);
  font-size: 14px;
  text-align: left;
  cursor: default;
  transition: background var(--transition-fast), color var(--transition-fast);

  &:hover {
    background: var(--breeze-hover);
    color: var(--breeze-text);
  }

  &.active {
    background: var(--breeze-active);
    color: var(--breeze-accent);

    .place-icon {
      opacity: 1;
    }
  }
}

.place-icon {
  flex-shrink: 0;
  opacity: 0.7;
}

.place-label {
  @include truncate;
}
</style>
