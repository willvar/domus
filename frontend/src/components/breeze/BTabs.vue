<script setup>
import { provide, ref, useSlots, computed } from 'vue'

const props = defineProps({
  value: { type: String, default: '' },
  animated: { type: Boolean, default: false },
})

const emit = defineEmits(['update:value'])

const slots = useSlots()

const tabs = computed(() => {
  const children = slots.default?.() || []
  return children
    .filter(c => c.props)
    .map(c => ({
      name: c.props.name,
      tab: c.props.tab || c.props.name,
      vnode: c,
    }))
})

function select(name) {
  emit('update:value', name)
}
</script>

<template>
  <div class="breeze-tabs">
    <div class="breeze-tabs__bar">
      <button
        v-for="t in tabs"
        :key="t.name"
        type="button"
        class="breeze-tabs__segment"
        :class="{ active: t.name === value }"
        @click="select(t.name)"
      >
        {{ t.tab }}
      </button>
    </div>
    <div class="breeze-tabs__content">
      <template v-for="t in tabs" :key="t.name">
        <div v-show="t.name === value" class="breeze-tab-pane">
          <component :is="t.vnode" />
        </div>
      </template>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.breeze-tabs {
  &__bar {
    display: flex;
    background: var(--breeze-bg-alt);
    border-radius: 4px;
    padding: 2px;
    gap: 2px;
  }

  &__segment {
    flex: 1;
    padding: 6px 16px;
    border: none;
    border-radius: 3px;
    background: transparent;
    color: var(--breeze-text-secondary);
    font-family: inherit;
    font-size: 14px;
    cursor: pointer;
    transition: all var(--transition-fast);
    white-space: nowrap;

    &:hover {
      color: var(--breeze-text);
    }

    &.active {
      background: var(--breeze-surface-raised);
      color: var(--breeze-text);
    }
  }

  &__content {
    margin-top: 0;
  }
}
</style>
