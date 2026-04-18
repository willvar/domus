<script setup lang="ts">
import { computed } from 'vue'
import { getFileIcon } from '../../composables/useFileIcon'
import { useI18n } from '../../composables/useI18n'
import type { PropType } from 'vue'
import type { FileListItem } from '../../types'

const props = defineProps({
  file: { type: Object as PropType<FileListItem>, required: true },
  selected: { type: Boolean, default: false },
})

const emit = defineEmits(['open', 'more', 'longpress'])
const { t } = useI18n()
const icon = computed(() => getFileIcon(props.file.name, props.file.is_dir))
const subtitle = computed(() => props.file.is_dir ? t('mobile.files.folder') : props.file.content_type || t('mobile.files.file'))
</script>

<template>
  <div class="mobile-grid-item" :class="{ 'mobile-grid-item--selected': selected }" @click="emit('open')" @contextmenu.prevent="emit('more')">
    <div class="mobile-grid-item-media" @touchstart="emit('longpress')">
      <img v-if="file.thumbnail_url" :src="file.thumbnail_url" class="mobile-grid-item-thumb" />
      <component :is="icon" v-else width="34" height="34" class="mobile-grid-item-icon" />
    </div>
    <div class="mobile-grid-item-title">{{ file._originalName || file.name }}</div>
    <div class="mobile-grid-item-meta">{{ subtitle }}</div>
  </div>
</template>

<style lang="scss" scoped>
.mobile-grid-item {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
  transition: transform 0.16s ease, box-shadow 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.985);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.09);
  }

  &--selected {
    box-shadow: 0 0 0 1px rgba(61, 174, 233, 0.25);
    background: rgba(61, 174, 233, 0.12);
  }

  &-media {
    border-radius: 18px;
    aspect-ratio: 1;
    background: rgba(16, 32, 48, 0.06);
    @include flex-center;
    overflow: hidden;
  }

  &-thumb {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  &-icon {
    color: var(--breeze-accent);
  }

  &-title {
    font-size: 13px;
    font-weight: 700;
    color: #102030;
    line-height: 1.4;
    word-break: break-word;
  }

  &-meta {
    margin-top: -4px;
    font-size: 11px;
    color: rgba(16, 32, 48, 0.56);
    line-height: 1.4;
  }
}
</style>
