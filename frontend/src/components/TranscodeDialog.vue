<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { useMessage } from '../composables/useMessage'
import { useActivityStore } from '../stores/activity'
import { useTasksStore } from '../stores/tasks'
import { Modal, Form, FormItem, Select, Switch, Button } from '../barrels/breeze'

const { t, te } = useI18n()
const message = useMessage()
const activityStore = useActivityStore()
const tasksStore = useTasksStore()

const show = ref(false)
const loading = ref(false)
const filePath = ref('')
const fileName = ref('')
const mediaType = ref('')

const preset = ref('medium')
const outputFormat = ref('')
const replace = ref(true)

let resolvePromise: ((value: string | null) => void) | null = null

const presetOptions = [
  { label: t('transcode.preset_low'), value: 'low' },
  { label: t('transcode.preset_medium'), value: 'medium' },
  { label: t('transcode.preset_high'), value: 'high' },
]

const formatOptions = computed(() => {
  switch (mediaType.value) {
    case 'video':
      return [
        { label: 'MP4', value: 'mp4' },
        { label: 'WebM', value: 'webm' },
        { label: 'MKV', value: 'mkv' },
        { label: 'MOV', value: 'mov' },
      ]
    case 'audio':
      return [
        { label: 'AAC', value: 'aac' },
        { label: 'MP3', value: 'mp3' },
        { label: 'FLAC', value: 'flac' },
        { label: 'OGG', value: 'ogg' },
      ]
    case 'image':
      return [
        { label: 'WebP', value: 'webp' },
        { label: 'JPEG', value: 'jpg' },
        { label: 'PNG', value: 'png' },
      ]
    default:
      return []
  }
})

// Set default format when media type changes
watch(mediaType, (type_) => {
  switch (type_) {
    case 'video': outputFormat.value = 'mp4'; break
    case 'audio': outputFormat.value = 'aac'; break
    case 'image': outputFormat.value = 'webp'; break
  }
})

function open(path: string, name: string, type_: string) {
  filePath.value = path
  fileName.value = name
  mediaType.value = type_
  preset.value = 'medium'
  replace.value = true
  loading.value = false
  show.value = true

  return new Promise((resolve) => {
    resolvePromise = resolve
  })
}

function close() {
  show.value = false
  if (resolvePromise) {
    resolvePromise(null)
    resolvePromise = null
  }
}

async function startTranscode() {
  loading.value = true
  try {
    const { data: res } = await api.post<{ task_id: string }>('/job/', {
      type: 'transcode',
      path: filePath.value,
      preset: preset.value,
      output_format: outputFormat.value,
      replace: replace.value,
    })
    const taskId = res.task_id
    tasksStore.upsertTask({
      task_id: taskId,
      type: 'transcode',
      status: 'running',
      progress: 0,
      phase: '',
      name: fileName.value,
    })
    activityStore.show()
    message.success(t('transcode.started'))
    show.value = false
    if (resolvePromise) {
      resolvePromise(taskId)
      resolvePromise = null
    }
  } catch (e: any) {
    message.error(te(e, 'transcode.failed'))
  } finally {
    loading.value = false
  }
}

defineExpose({ open })
</script>

<template>
  <Modal
    :show="show"
    preset="dialog"
    :title="t('transcode.title')"
    @close="close"
    @mask-click="close"
    @update:show="v => { if (!v) close() }"
  >
    <div class="transcode-info">
      <span class="file-name">{{ fileName }}</span>
    </div>

    <Form label-placement="left" label-width="auto" style="margin-top: 12px">
      <FormItem :label="t('transcode.preset')">
        <Select v-model:value="preset" :options="presetOptions" />
      </FormItem>
      <FormItem :label="t('transcode.format')">
        <Select v-model:value="outputFormat" :options="formatOptions" />
      </FormItem>
      <FormItem :label="t('transcode.replace')">
        <Switch v-model:value="replace" />
      </FormItem>
    </Form>

    <template #action>
      <div style="display:flex;justify-content:flex-end;gap:8px">
        <Button @click="close">{{ t('dialog.cancel') }}</Button>
        <Button type="primary" :loading="loading" @click="startTranscode">
          {{ t('transcode.start') }}
        </Button>
      </div>
    </template>
  </Modal>
</template>

<style lang="scss" scoped>
.transcode-info {
  padding: 8px 0;
}

.file-name {
  color: #3daee9;
  font-weight: 500;
}
</style>
