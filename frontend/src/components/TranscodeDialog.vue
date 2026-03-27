<script setup>
import { ref, computed, watch } from 'vue'
import { useWebSocket } from '../composables/useWebSocket'
import { useI18n } from '../composables/useI18n'
import { useMessage } from '../composables/useMessage'
import { useJobsStore } from '../stores/jobs'
import BModal from './breeze/BModal.vue'
import BForm from './breeze/BForm.vue'
import BFormItem from './breeze/BFormItem.vue'
import BSelect from './breeze/BSelect.vue'
import BSwitch from './breeze/BSwitch.vue'
import BButton from './breeze/BButton.vue'

const ws = useWebSocket()
const { t, te } = useI18n()
const message = useMessage()
const jobsStore = useJobsStore()

const show = ref(false)
const loading = ref(false)
const filePath = ref('')
const fileName = ref('')
const mediaType = ref('')

const preset = ref('medium')
const outputFormat = ref('')
const replace = ref(true)

let resolvePromise = null

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

function open(path, name, type_) {
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
    const res = await ws.request('task.create', {
      type: 'transcode',
      path: filePath.value,
      preset: preset.value,
      output_format: outputFormat.value,
      replace: replace.value,
    })
    const taskId = res.task_id
    jobsStore.addTask({
      task_id: taskId,
      type: 'transcode',
      status: 'running',
      progress: 0,
      phase: '',
      name: fileName.value,
    })
    message.success(t('transcode.started'))
    show.value = false
    if (resolvePromise) {
      resolvePromise(taskId)
      resolvePromise = null
    }
  } catch (e) {
    message.error(te(e, 'transcode.failed'))
  } finally {
    loading.value = false
  }
}

defineExpose({ open })
</script>

<template>
  <BModal
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

    <BForm label-placement="left" label-width="auto" style="margin-top: 12px">
      <BFormItem :label="t('transcode.preset')">
        <BSelect v-model:value="preset" :options="presetOptions" />
      </BFormItem>
      <BFormItem :label="t('transcode.format')">
        <BSelect v-model:value="outputFormat" :options="formatOptions" />
      </BFormItem>
      <BFormItem :label="t('transcode.replace')">
        <BSwitch v-model:value="replace" />
      </BFormItem>
    </BForm>

    <template #action>
      <div style="display:flex;justify-content:flex-end;gap:8px">
        <BButton @click="close">{{ t('dialog.cancel') }}</BButton>
        <BButton type="primary" :loading="loading" @click="startTranscode">
          {{ t('transcode.start') }}
        </BButton>
      </div>
    </template>
  </BModal>
</template>

<style scoped>
.transcode-info {
  padding: 8px 0;
}
.file-name {
  color: #3daee9;
  font-weight: 500;
}
</style>
