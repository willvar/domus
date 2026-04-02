<script setup>
import { ref, computed, watch, onMounted, onUnmounted, defineComponent, nextTick } from 'vue'

const props = defineProps({
  columns: { type: Array, default: () => [] },
  data: { type: Array, default: () => [] },
  rowKey: { type: Function, default: undefined },
  rowProps: { type: Function, default: undefined },
  size: { type: String, default: 'medium' },
  bordered: { type: Boolean, default: true },
  striped: { type: Boolean, default: false },
  virtualScroll: { type: Boolean, default: false },
  maxHeight: { type: String, default: undefined },
  loading: { type: Boolean, default: false },
})

// --- Sorting ---
const sortKey = ref(null)
const sortOrder = ref(null) // 'asc' | 'desc' | null

function toggleSort(col) {
  if (!col.sorter) return
  if (sortKey.value === col.key) {
    if (sortOrder.value === 'asc') sortOrder.value = 'desc'
    else if (sortOrder.value === 'desc') { sortKey.value = null; sortOrder.value = null }
    else sortOrder.value = 'asc'
  } else {
    sortKey.value = col.key
    sortOrder.value = 'asc'
  }
}

const sortedData = computed(() => {
  if (!sortKey.value || !sortOrder.value) return props.data
  const key = sortKey.value
  const dir = sortOrder.value === 'asc' ? 1 : -1
  return [...props.data].sort((a, b) => {
    const va = a[key], vb = b[key]
    if (va == null && vb == null) return 0
    if (va == null) return 1
    if (vb == null) return -1
    if (typeof va === 'string') return va.localeCompare(vb) * dir
    return (va - vb) * dir
  })
})

// --- Column resizing ---
const colWidths = ref({})

function onResizeStart(e, col) {
  e.preventDefault()
  const startX = e.clientX
  const startW = colWidths.value[col.key] || col.width || 120

  function onMove(ev) {
    const w = Math.max(50, startW + ev.clientX - startX)
    colWidths.value = { ...colWidths.value, [col.key]: w }
  }
  function onUp() {
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', onUp)
  }
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}

function colStyle(col) {
  const w = colWidths.value[col.key] || col.width
  const mw = col.minWidth
  const s = {}
  if (w) s.width = w + 'px'
  if (mw) s.minWidth = mw + 'px'
  return s
}

// --- Virtual scroll ---
const ROW_HEIGHT = computed(() => props.size === 'small' ? 36 : 40)
const BUFFER = 8
const scrollRef = ref(null)
const scrollTop = ref(0)
const containerHeight = ref(0)

const visibleRange = computed(() => {
  if (!props.virtualScroll) return { start: 0, end: sortedData.value.length }
  const start = Math.max(0, Math.floor(scrollTop.value / ROW_HEIGHT.value) - BUFFER)
  const visibleCount = Math.ceil(containerHeight.value / ROW_HEIGHT.value) + BUFFER * 2
  const end = Math.min(sortedData.value.length, start + visibleCount)
  return { start, end }
})

const visibleRows = computed(() => sortedData.value.slice(visibleRange.value.start, visibleRange.value.end))
const totalHeight = computed(() => sortedData.value.length * ROW_HEIGHT.value)
const offsetTop = computed(() => visibleRange.value.start * ROW_HEIGHT.value)

function onScroll(e) {
  scrollTop.value = e.target.scrollTop
}

function updateContainerHeight() {
  if (scrollRef.value) containerHeight.value = scrollRef.value.clientHeight
}

onMounted(() => {
  updateContainerHeight()
  window.addEventListener('resize', updateContainerHeight)
})
onUnmounted(() => window.removeEventListener('resize', updateContainerHeight))

watch(() => props.data.length, () => nextTick(updateContainerHeight))

// --- Render cell helper ---
const RenderCell = defineComponent({
  props: { render: Function, row: Object },
  render() { return this.render(this.row) },
})

function getKey(row, i) {
  if (props.rowKey) return props.rowKey(row)
  return i
}

function getRowAttrs(row) {
  if (!props.rowProps) return {}
  return props.rowProps(row)
}
</script>

<template>
  <div
    class="breeze-table"
    :class="[
      `breeze-table--${size}`,
      { 'breeze-table--bordered': bordered, 'breeze-table--striped': striped }
    ]"
  >
    <div
      ref="scrollRef"
      class="breeze-table__scroll"
      :style="maxHeight ? { maxHeight } : {}"
      @scroll="onScroll"
    >
      <table class="breeze-table__table">
        <thead class="breeze-table__head">
          <tr>
            <th
              v-for="col in columns"
              :key="col.key"
              class="breeze-table__th"
              :class="{ sortable: col.sorter, resizable: col.resizable }"
              :style="colStyle(col)"
              @click="toggleSort(col)"
            >
              <span class="breeze-table__th-text" :class="{ 'breeze-table__ellipsis': col.ellipsis }">{{ col.title }}</span>
              <span v-if="col.sorter" class="breeze-table__sort-icon">
                <template v-if="sortKey === col.key && sortOrder === 'asc'">&#9650;</template>
                <template v-else-if="sortKey === col.key && sortOrder === 'desc'">&#9660;</template>
                <template v-else>&#9652;</template>
              </span>
              <div
                v-if="col.resizable"
                class="breeze-table__resize-handle"
                @mousedown.stop="onResizeStart($event, col)"
              />
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-if="virtualScroll">
            <tr v-if="offsetTop > 0" :style="{ height: offsetTop + 'px' }" />
            <tr
              v-for="(row, i) in visibleRows"
              :key="getKey(row, visibleRange.start + i)"
              class="breeze-table__row"
              v-bind="getRowAttrs(row)"
              :style="{ height: ROW_HEIGHT + 'px' }"
            >
              <td
                v-for="col in columns"
                :key="col.key"
                class="breeze-table__td"
                :class="{ 'breeze-table__ellipsis': col.ellipsis }"
                :style="colStyle(col)"
              >
                <RenderCell v-if="col.render" :render="col.render" :row="row" />
                <template v-else>{{ row[col.key] }}</template>
              </td>
            </tr>
            <tr v-if="totalHeight - offsetTop - visibleRows.length * ROW_HEIGHT > 0" :style="{ height: (totalHeight - offsetTop - visibleRows.length * ROW_HEIGHT) + 'px' }" />
          </template>
          <template v-else>
            <tr
              v-for="(row, i) in sortedData"
              :key="getKey(row, i)"
              class="breeze-table__row"
              v-bind="getRowAttrs(row)"
            >
              <td
                v-for="col in columns"
                :key="col.key"
                class="breeze-table__td"
                :class="{ 'breeze-table__ellipsis': col.ellipsis }"
                :style="colStyle(col)"
              >
                <RenderCell v-if="col.render" :render="col.render" :row="row" />
                <template v-else>{{ row[col.key] }}</template>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
    <div v-if="loading" class="breeze-table__loading">
      <div class="breeze-table__spinner" />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.breeze-table {
  position: relative;
  width: 100%;

  &--bordered {
    border: 1px solid var(--breeze-border);
    border-radius: 4px;
    overflow: hidden;
  }

  &--small {
    .breeze-table__th {
      padding: 6px 10px;
      font-size: 12px;
    }

    .breeze-table__td {
      padding: 6px 10px;
      font-size: 13px;
    }
  }

  &--medium {
    .breeze-table__th {
      padding: 8px 12px;
      font-size: 13px;
    }

    .breeze-table__td {
      padding: 8px 12px;
      font-size: 14px;
    }
  }

  &--striped {
    .breeze-table__row:nth-child(odd) {
      background: var(--breeze-surface);

      &:hover {
        background: $hover-white-light;
      }
    }
  }

  &__scroll {
    overflow: auto;
    width: 100%;
  }

  &__table {
    width: 100%;
    border-collapse: collapse;
    table-layout: fixed;
  }

  &__head {
    position: sticky;
    top: 0;
    z-index: $z-sticky;
  }

  &__th {
    background: var(--breeze-surface);
    color: var(--breeze-text-secondary);
    font-weight: 500;
    text-align: left;
    border-bottom: 1px solid var(--breeze-border);
    white-space: nowrap;
    position: relative;
    user-select: none;

    &.sortable {
      cursor: pointer;

      &:hover {
        background: $hover-white-subtle;
      }
    }
  }

  &__th-text {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  &__sort-icon {
    font-size: 10px;
    color: var(--breeze-text-disabled);
    margin-left: 4px;
  }

  &__resize-handle {
    position: absolute;
    right: 0;
    top: 0;
    bottom: 0;
    width: 4px;
    cursor: col-resize;

    &:hover {
      background: var(--breeze-accent);
    }
  }

  &__row {
    transition: background var(--transition-fast);

    &:hover {
      background: $hover-white-subtle;
    }
  }

  &__td {
    color: var(--breeze-text);
    border-bottom: 1px solid var(--breeze-border);
  }

  &__ellipsis {
    @include truncate;
  }

  /* Loading overlay */
  &__loading {
    position: absolute;
    inset: 0;
    background: rgba(20, 22, 24, 0.4);
    @include flex-center;
    z-index: 3;
  }

  &__spinner {
    width: 24px;
    height: 24px;
    border: 2px solid var(--breeze-border);
    border-top-color: var(--breeze-accent);
    border-radius: 50%;
    animation: b-table-spin 0.7s linear infinite;
  }
}

@keyframes b-table-spin {
  to { transform: rotate(360deg); }
}
</style>
