<script setup>
import { ref, computed, watch } from 'vue'
import { NModal, NForm, NFormItem, NSelect, NSwitch, NButton, NSpace, useMessage } from 'naive-ui'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { useJobsStore } from '../stores/jobs'

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
    const res = await api.post('/job', {
      type: 'transcode',
      path: filePath.value,
      preset: preset.value,
      output_format: outputFormat.value,
      replace: replace.value,
    })
    const jobId = res.data.job_id
    jobsStore.addJob({
      job_id: jobId,
      type: 'transcode',
      status: 'pending',
      progress: 0,
      phase: '',
      params: JSON.stringify({ original_name: fileName.value, output_format: outputFormat.value }),
    })
    message.success(t('transcode.started'))
    show.value = false
    if (resolvePromise) {
      resolvePromise(jobId)
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
  <NModal
    v-model:show="show"
    preset="dialog"
    :title="t('transcode.title')"
    style="width: 480px; max-width: 95vw"
    @close="close"
  >
    <div class="transcode-info">
      <span class="file-name">{{ fileName }}</span>
    </div>

    <NForm label-placement="left" label-width="auto" style="margin-top: 12px">
      <NFormItem :label="t('transcode.preset')">
        <NSelect v-model:value="preset" :options="presetOptions" />
      </NFormItem>
      <NFormItem :label="t('transcode.format')">
        <NSelect v-model:value="outputFormat" :options="formatOptions" />
      </NFormItem>
      <NFormItem :label="t('transcode.replace')">
        <NSwitch v-model:value="replace" />
      </NFormItem>
    </NForm>

    <template #action>
      <NSpace justify="end">
        <NButton @click="close">{{ t('dialog.cancel') }}</NButton>
        <NButton type="primary" :loading="loading" @click="startTranscode">
          {{ t('transcode.start') }}
        </NButton>
      </NSpace>
    </template>
  </NModal>
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
