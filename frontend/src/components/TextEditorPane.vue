<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useCodeEditor } from '../composables/useCodeEditor'
import { useTheme } from '../composables/useTheme'

const props = defineProps<{
  modelValue: string
  language: string | null
}>()

const emit = defineEmits<{
  'update:modelValue': [content: string]
  'scroll-position': [position: {
    line: number
    totalLines: number
    scrollTop: number
    maxScroll: number
  }]
  save: []
}>()

const container = ref<HTMLElement | null>(null)
const editor = useCodeEditor()
const { isDark } = useTheme()
watch(isDark, (value) => editor.setDark(value), { immediate: false })
let scrollElement: HTMLElement | null = null
let scrollFrame = 0

function emitScrollPosition(): void {
  scrollFrame = 0
  const view = editor.view.value
  if (!view) return

  const scrollTop = view.scrollDOM.scrollTop
  const maxScroll = Math.max(0, view.scrollDOM.scrollHeight - view.scrollDOM.clientHeight)
  const block = view.lineBlockAtHeight(scrollTop + 8)
  emit('scroll-position', {
    line: view.state.doc.lineAt(block.from).number,
    totalLines: view.state.doc.lines,
    scrollTop,
    maxScroll,
  })
}

function handleScroll(): void {
  if (scrollFrame) return
  scrollFrame = requestAnimationFrame(emitScrollPosition)
}

function scrollToLine(line: number): void {
  const view = editor.view.value
  if (!view) return
  const targetLine = view.state.doc.line(Math.max(1, Math.min(view.state.doc.lines, Math.round(line))))
  view.scrollDOM.scrollTop = view.lineBlockAt(targetLine.from).top
}

function scrollToProgress(progress: number): void {
  const view = editor.view.value
  if (!view) return
  const maxScroll = Math.max(0, view.scrollDOM.scrollHeight - view.scrollDOM.clientHeight)
  view.scrollDOM.scrollTop = Math.max(0, Math.min(1, progress)) * maxScroll
}

defineExpose({ scrollToLine, scrollToProgress })

onMounted(async () => {
  if (!container.value) return
  editor.setDark(isDark.value)
  await editor.create(container.value, props.modelValue, props.language, {
    onChange: (content) => {
      emit('update:modelValue', content)
      handleScroll()
    },
    onSave: () => emit('save'),
  })
  if (!editor.view.value) return
  scrollElement = editor.view.value.scrollDOM
  scrollElement.addEventListener('scroll', handleScroll, { passive: true })
  handleScroll()
})

onBeforeUnmount(() => {
  scrollElement?.removeEventListener('scroll', handleScroll)
  if (scrollFrame) cancelAnimationFrame(scrollFrame)
  editor.destroy()
})
</script>

<template>
  <div ref="container" class="text-editor-pane" data-testid="text-editor" />
</template>

<style scoped>
.text-editor-pane {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--domus-surface);
  user-select: text;
}

.text-editor-pane :deep(.cm-editor) { height: 100%; }
</style>
