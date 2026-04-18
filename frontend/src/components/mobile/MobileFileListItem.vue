<script setup lang="ts">
import { computed } from 'vue'
import dayjs from 'dayjs'
import { IconMenu } from '../../barrels/icons'
import { getFileIcon } from '../../composables/useFileIcon'
import { useI18n } from '../../composables/useI18n'
import { useTouchHandlers } from '../../composables/useTouch'
import type { PropType } from 'vue'
import type { FileListItem } from '../../types'

const props = defineProps({
  file: { type: Object as PropType<FileListItem>, required: true },
  selected: { type: Boolean, default: false },
  selectMode: { type: Boolean, default: false },
  meta: { type: String, default: '' },
})

const emit = defineEmits(['open', 'more', 'longpress', 'toggle-select'])
const { t } = useI18n()

const icon = computed(() => getFileIcon(props.file.name, props.file.is_dir))
const subtitle = computed(() => {
  if (props.meta) return props.meta
  const parts = []
  if (!props.file.is_dir && props.file.size) parts.push(formatSize(props.file.size))
  if (props.file.last_modified) parts.push(dayjs(props.file.last_modified).format('YYYY-MM-DD HH:mm'))
  return parts.join(' · ')
})

const touch = useTouchHandlers({
  onLongPress: () => emit('longpress'),
})

function formatSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
  return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB'
}

function handleClick(): void {
  if (props.selectMode) {
    emit('toggle-select')
    return
  }
  emit('open')
}
</script>

<template>
  <div
    class="mobile-file-item"
    :class="{ 'mobile-file-item--selected': selected }"
    @click="handleClick"
    @touchstart="touch.onTouchStart"
    @touchmove="touch.onTouchMove"
    @touchend="touch.onTouchEnd"
  >
    <div class="mobile-file-item-icon-wrap">
      <component :is="icon" class="mobile-file-item-icon" width="24" height="24" />
      <div v-if="selectMode" class="mobile-file-item-check" :class="{ 'mobile-file-item-check--selected': selected }" />
    </div>
    <div class="mobile-file-item-copy">
      <div class="mobile-file-item-title">{{ file._originalName || file.name }}</div>
      <div class="mobile-file-item-meta">{{ subtitle || (file.is_dir ? t('mobile.files.folder') : t('mobile.files.file')) }}</div>
    </div>
    <button class="mobile-file-item-more" @click.stop="emit('more')">
      <IconMenu width="18" height="18" />
    </button>
  </div>
</template>

<style lang="scss" scoped>
.mobile-file-item {
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 14px;
  min-height: 72px;
  padding: 14px 16px;
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 10px 28px rgba(11, 24, 36, 0.06);
  transition: transform 0.16s ease, box-shadow 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.992);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.08);
  }

  &--selected {
    background: rgba(61, 174, 233, 0.12);
    box-shadow: 0 0 0 1px rgba(61, 174, 233, 0.25);
  }

  &-icon-wrap {
    position: relative;
    width: 42px;
    height: 42px;
    border-radius: 14px;
    background: rgba(16, 32, 48, 0.06);
    @include flex-center;
  }

  &-icon {
    color: var(--breeze-accent);
  }

  &-copy {
    min-width: 0;
  }

  &-title {
    font-size: 15px;
    font-weight: 700;
    color: #102030;
    @include truncate;
  }

  &-meta {
    margin-top: 4px;
    font-size: 12px;
    color: rgba(16, 32, 48, 0.6);
    @include truncate;
  }

  &-more {
    width: 38px;
    height: 38px;
    border: none;
    border-radius: 12px;
    background: rgba(16, 32, 48, 0.06);
    color: rgba(16, 32, 48, 0.72);
    transition: transform 0.16s ease, background 0.16s ease;

    &:active {
      transform: scale(0.94);
      background: rgba(16, 32, 48, 0.1);
    }
  }

  &-check {
    position: absolute;
    right: -2px;
    bottom: -2px;
    width: 14px;
    height: 14px;
    border-radius: 999px;
    border: 2px solid rgba(16, 32, 48, 0.24);
    background: #fff;

    &--selected {
      border-color: #3daee9;
      background: #3daee9;
    }
  }
}
</style>
