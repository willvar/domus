<script setup lang="ts">
import { ref, watch, nextTick, onUnmounted } from 'vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useI18n } from '../../composables/useI18n'

const fs = useFileSystemStore()
const { t } = useI18n()
const editing = ref(false)
const editPath = ref('')
const inputRef = ref<HTMLInputElement | null>(null)

// Subdirectory dropdown state
const subDirs = ref<{ name: string; path: string }[]>([])
const showSubMenu = ref(false)
const subMenuStyle = ref<Record<string, string>>({})
const subMenuRef = ref<HTMLDivElement | null>(null)
const activeSegIndex = ref(-1)

watch(() => fs.focusPathBar, (val) => {
  if (val) {
    startEdit()
    fs.focusPathBar = false
  }
})

function startEdit() {
  editing.value = true
  editPath.value = fs.currentPath?.startsWith('/') ? fs.currentPath : '/' + (fs.currentPath || '')
  nextTick(() => inputRef.value?.focus())
}

function commitEdit() {
  editing.value = false
  const p = editPath.value.trim() || '/'
  if (p !== fs.currentPath) {
    fs.navigate(p)
  }
}

function cancelEdit() {
  editing.value = false
}

async function loadSubDirs(parentPath: string) {
  try {
    const dirs = (await fs.listFilesAtPath(parentPath)).filter((f: any) => f.is_dir)
    subDirs.value = dirs.map((d: any) => ({ name: d.name, path: d.path }))
  } catch {
    subDirs.value = []
  }
}

async function toggleSubMenu(e: MouseEvent, segIndex: number) {
  e.stopPropagation()
  if (showSubMenu.value && activeSegIndex.value === segIndex) {
    showSubMenu.value = false
    return
  }
  const parentPath = segIndex === 0 ? '' : fs.pathSegments[segIndex - 1].path
  await loadSubDirs(parentPath)
  activeSegIndex.value = segIndex
  const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
  const left = rect.left
  const top = rect.bottom + 2
  subMenuStyle.value = { left: `${left}px`, top: `${top}px` }
  showSubMenu.value = true
}

function selectSubDir(path: string) {
  showSubMenu.value = false
  fs.navigate(path)
}

function onSubMenuClickOutside(e: MouseEvent) {
  if (subMenuRef.value && !subMenuRef.value.contains(e.target as Node)) {
    showSubMenu.value = false
  }
}

watch(showSubMenu, (val) => {
  if (val) {
    document.addEventListener('mousedown', onSubMenuClickOutside, true)
  } else {
    document.removeEventListener('mousedown', onSubMenuClickOutside, true)
  }
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onSubMenuClickOutside, true)
})
</script>

<template>
  <div class="breadcrumb-bar" @click="startEdit">
    <template v-if="fs.searchMode">
      <div class="breadcrumb-segments">
        <span class="breadcrumb-segment search-indicator">
          {{ t('search.results_title') }}: {{ fs.searchQuery }}
        </span>
      </div>
    </template>
    <template v-else-if="editing">
      <input
        ref="inputRef"
        v-model="editPath"
        class="path-input"
        :placeholder="t('breadcrumb.path_placeholder')"
        @keyup.enter="commitEdit"
        @keyup.escape="cancelEdit"
        @blur="commitEdit"
      />
    </template>
    <template v-else>
      <div class="breadcrumb-segments">
        <span class="breadcrumb-segment" @click.stop="fs.navigate('/')">
          /
        </span>
        <template v-for="(seg, i) in fs.pathSegments" :key="seg.path">
          <span
            class="breadcrumb-sep"
            :class="{ active: showSubMenu && activeSegIndex === i }"
            @click.stop="toggleSubMenu($event, i)"
          >›</span>
          <span
            class="breadcrumb-segment"
            :class="{ active: i === fs.pathSegments.length - 1 }"
            @click.stop="fs.navigate(seg.path)"
          >
            {{ seg.name }}
          </span>
        </template>
      </div>

      <Teleport to="body">
        <Transition name="ctx-menu">
          <div
            v-if="showSubMenu && subDirs.length > 0"
            ref="subMenuRef"
            class="plasma-dropdown breadcrumb-dropdown"
            :style="subMenuStyle"
          >
            <button
              v-for="dir in subDirs"
              :key="dir.path"
              class="dropdown-item"
              @click="selectSubDir(dir.path)"
            >
              {{ dir.name }}
            </button>
          </div>
        </Transition>
      </Teleport>
    </template>
  </div>
</template>

<style lang="scss" scoped>
.breadcrumb-bar {
  display: flex;
  align-items: center;
  height: 30px;
  padding: 0 var(--gap-sm);
  background: var(--breeze-surface);
  border-bottom: 1px solid var(--breeze-border);
  flex-shrink: 0;

  @include mobile {
    height: 34px;
  }
}

/* ─── Path editing input (Breeze-style) ─── */
.path-input {
  flex: 1;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--breeze-accent, #3daee9);
  border-radius: 3px;
  background: var(--breeze-bg-alt, #202326);
  color: var(--breeze-text, #fcfcfc);
  font-size: 14px;
  outline: none;

  &::placeholder {
    color: var(--breeze-text-disabled, #505962);
  }
}

/* ─── Breadcrumb segments ─── */
.breadcrumb-segments {
  display: flex;
  align-items: center;
  gap: 0;
  overflow: hidden;
  flex: 1;

  @include mobile {
    flex-direction: row;
    justify-content: flex-end;
  }
}

.breadcrumb-segment {
  padding: 2px 6px;
  border-radius: 3px;
  white-space: nowrap;
  font-size: var(--font-size-sm);
  color: var(--breeze-text-secondary);
  cursor: default;

  &:active {
    color: var(--breeze-text);
    background: var(--breeze-hover);
  }

  @include hover {
    color: var(--breeze-text);
    background: var(--breeze-hover);
  }

  &.active {
    color: var(--breeze-text);
    font-weight: 500;
  }

  &.search-indicator {
    color: var(--breeze-accent);
    font-weight: 500;
  }

  @include mobile {
    padding: 4px 8px;
    font-size: 13px;
  }
}

.breadcrumb-sep {
  color: var(--breeze-text-disabled);
  font-size: 14px;
  padding: 2px 1px;
  border-radius: 3px;
  cursor: default;

  &:active,
  &.active {
    color: var(--breeze-text-secondary);
    background: var(--breeze-hover);
  }

  @include hover {
    color: var(--breeze-text-secondary);
    background: var(--breeze-hover);
  }

  @include mobile {
    padding: 4px 2px;
  }
}

/* ─── Breadcrumb dropdown overrides ─── */
.plasma-dropdown.breadcrumb-dropdown {
  min-width: 140px;
  max-height: 320px;
  overflow-y: auto;

  .dropdown-item {
    white-space: nowrap;
  }
}

/* ─── Transition ─── */
@include ctx-menu-transition;
</style>
