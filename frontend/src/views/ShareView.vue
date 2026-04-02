<template>
  <div class="share-view">
    <div v-if="loading" class="share-loading">Loading shared file...</div>
    <div v-else-if="error" class="share-error">{{ error }}</div>
    <template v-else>
      <div class="share-header">
        <h2>{{ meta.name }}</h2>
        <span class="share-size">{{ formatSize(meta.size) }}</span>
        <button class="share-download" @click="download">Download</button>
      </div>
      <div class="share-content">
        <!-- Image -->
        <img v-if="viewType === 'image'" :src="decryptUrl" class="share-image" />
        <!-- Video -->
        <video v-else-if="viewType === 'video'" :src="decryptUrl" controls class="share-video" />
        <!-- Audio -->
        <audio v-else-if="viewType === 'audio'" :src="decryptUrl" controls class="share-audio" />
        <!-- PDF -->
        <iframe v-else-if="viewType === 'pdf'" :src="decryptUrl" class="share-iframe" />
        <!-- Text -->
        <pre v-else-if="viewType === 'text'" class="share-text">{{ textContent }}</pre>
        <!-- Fallback -->
        <div v-else class="share-fallback">
          <p>Preview not available for this file type.</p>
          <p>Click Download to save the file.</p>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useServiceWorker } from '../composables/useServiceWorker'
import api from '../composables/useApi'

const route = useRoute()
const sw = useServiceWorker()

const loading = ref(true)
const error = ref('')
const meta = ref({})
const decryptUrl = ref('')
const textContent = ref('')

const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'avif', 'ico']
const videoExts = ['mp4', 'webm']
const audioExts = ['mp3', 'wav', 'ogg', 'aac', 'm4a', 'flac', 'opus']
const textExts = ['txt', 'md', 'json', 'js', 'ts', 'py', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'css', 'html', 'xml', 'yaml', 'yml', 'toml', 'ini', 'sh', 'bash', 'sql', 'log', 'csv']

const viewType = computed(() => {
  const ext = (meta.value.name || '').split('.').pop()?.toLowerCase() || ''
  if (imageExts.includes(ext)) return 'image'
  if (videoExts.includes(ext)) return 'video'
  if (audioExts.includes(ext)) return 'audio'
  if (ext === 'pdf') return 'pdf'
  if (textExts.includes(ext)) return 'text'
  return 'other'
})

function formatSize(bytes) {
  if (!bytes) return ''
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

onMounted(async () => {
  const shareId = route.params.shareId
  const shareKeyHex = window.location.hash.slice(1) // extract from #<key>

  if (!shareKeyHex) {
    error.value = 'Missing share key in URL'
    loading.value = false
    return
  }

  try {
    // Ensure SW is registered
    await sw.register()

    // Send share key as the KEK for this session
    sw.sendKey(shareKeyHex)

    // Fetch share info from public endpoint
    const res = await api.get(`/share/${shareId}/info`)
    meta.value = res.data

    // Register with SW for decryption
    const { url, size, content_type, chunk_size, wrapped_dek, name } = res.data
    decryptUrl.value = sw.registerDecrypt({
      url, size, chunkSize: chunk_size, contentType: content_type,
      filename: name, wrappedDek: wrapped_dek,
    })

    // For text files, fetch content
    if (viewType.value === 'text' && size < 2 * 1024 * 1024) {
      const textRes = await fetch(decryptUrl.value)
      textContent.value = await textRes.text()
    }
  } catch (e) {
    error.value = e.response?.data?.error === 'share_expired'
      ? 'This share link has expired.'
      : 'Failed to load shared file.'
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  if (decryptUrl.value) {
    sw.unregisterDecrypt(decryptUrl.value)
  }
  sw.clearKey()
})

function download() {
  if (!decryptUrl.value) return
  const a = document.createElement('a')
  a.href = decryptUrl.value
  a.download = meta.value.name || 'download'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}
</script>

<style lang="scss" scoped>
.share-view {
  max-width: 960px;
  margin: 0 auto;
  padding: 24px;
  font-family: system-ui, sans-serif;
}

.share-loading, .share-error {
  text-align: center;
  padding: 48px;
  color: #666;
}

.share-error { color: #c00; }

.share-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid #e0e0e0;

  h2 {
    margin: 0;
    font-size: 18px;
    flex: 1;
    @include truncate;
  }
}

.share-size { color: #888; font-size: 14px; }

.share-download {
  padding: 6px 16px;
  background: #1a73e8;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 14px;

  &:hover { background: #1557b0; }
}

.share-image { max-width: 100%; border-radius: 8px; }
.share-video, .share-audio { width: 100%; }
.share-iframe { width: 100%; height: 80vh; border: none; }

.share-text {
  background: #f5f5f5;
  padding: 16px;
  border-radius: 8px;
  overflow: auto;
  max-height: 80vh;
  font-size: 13px;
  line-height: 1.5;
}

.share-fallback {
  text-align: center;
  padding: 48px;
  color: #666;
}
</style>
