<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps({
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 10 },
  itemCount: { type: Number, default: 0 },
  pageSlot: { type: Number, default: 5 },
  size: { type: String, default: 'medium' },
})

const emit = defineEmits(['update:page'])

const totalPages = computed(() => Math.max(1, Math.ceil(props.itemCount / props.pageSize)))

const pages = computed(() => {
  const total = totalPages.value
  const current = props.page
  const slot = props.pageSlot
  const result: (number | string)[] = []

  if (total <= slot) {
    for (let i = 1; i <= total; i++) result.push(i)
  } else {
    const half = Math.floor(slot / 2)
    let start = Math.max(1, current - half)
    let end = start + slot - 1
    if (end > total) {
      end = total
      start = Math.max(1, end - slot + 1)
    }
    if (start > 1) { result.push(1); if (start > 2) result.push('...') }
    for (let i = start; i <= end; i++) result.push(i)
    if (end < total) { if (end < total - 1) result.push('...'); result.push(total) }
  }
  return result
})

function go(p: number | string) {
  if (typeof p !== 'number') return
  if (p < 1 || p > totalPages.value || p === props.page) return
  emit('update:page', p)
}
</script>

<template>
  <div class="breeze-pagination" :class="`breeze-pagination--${size}`">
    <button class="breeze-pagination__btn" :disabled="page <= 1" @click="go(page - 1)">&lt;</button>
    <template v-for="(p, i) in pages" :key="i">
      <span v-if="p === '...'" class="breeze-pagination__ellipsis">...</span>
      <button
        v-else
        class="breeze-pagination__btn"
        :class="{ active: p === page }"
        @click="go(p)"
      >{{ p }}</button>
    </template>
    <button class="breeze-pagination__btn" :disabled="page >= totalPages" @click="go(page + 1)">&gt;</button>
  </div>
</template>

<style lang="scss" scoped>
.breeze-pagination {
  display: inline-flex;
  align-items: center;
  gap: 2px;

  &--small &__btn {
    min-width: 24px;
    height: 24px;
    font-size: 12px;
  }

  &__btn {
    min-width: 28px;
    height: 28px;
    padding: 0 6px;
    border: none;
    border-radius: 3px;
    background: none;
    color: var(--breeze-text-secondary);
    font-family: inherit;
    font-size: 13px;
    cursor: pointer;
    @include inline-flex-center;
    transition: all var(--transition-fast);

    &:hover:not(:disabled) {
      background: $hover-white-light;
      color: var(--breeze-text);
    }

    &.active {
      background: var(--breeze-accent);
      color: #fff;
    }

    &:disabled {
      opacity: 0.35;
      cursor: not-allowed;
    }
  }

  &__ellipsis {
    min-width: 28px;
    text-align: center;
    color: var(--breeze-text-disabled);
    font-size: 12px;
  }
}
</style>
