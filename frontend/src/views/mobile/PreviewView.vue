<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconChevronLeft, IconMenu } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import MobileEmptyState from '../../components/mobile/MobileEmptyState.vue'
import { registerFileDecrypt, unregisterFileDecrypt } from '../../composables/useFileAccess'
import { useDevice } from '../../composables/useDevice'
import { useI18n } from '../../composables/useI18n'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useMobileUiStore } from '../../stores/mobileUi'
import type { FileListItem, ViewerType } from '../../types'

const route = useRoute()
const router = useRouter()
const fs = useFileSystemStore()
const mobileUi = useMobileUiStore()
const { isTouchInput } = useDevice()
const { t } = useI18n()

const file = ref<FileListItem | null>(null)
const decryptUrl = ref('')
const textContent = ref('')
const renderedHtml = ref('')
const csvRows = ref<string[][]>([])
const loading = ref(true)
const previewError = ref('')
const showChrome = ref(true)
const contentReady = computed(() => !loading.value && !previewError.value)
const previewSubtitle = computed(() => {
  if (viewerType.value === 'pdf') return t('mobile.preview.pdf_hint')
  if (viewerType.value === 'text' || viewerType.value === 'markdown' || viewerType.value === 'csv' || viewerType.value === 'notebook') {
    return t('mobile.preview.text_mode')
  }
  return viewerType.value || t('mobile.preview.subtitle')
})

const name = computed(() => file.value?._originalName || file.value?.name || (route.query.name as string) || t('mobile.preview.title'))
const viewerType = computed<ViewerType | null>(() => fs.getViewerType(name.value))

onMounted(async () => {
  const path = route.query.path
  if (typeof path !== 'string') {
    previewError.value = t('mobile.preview.missing_path')
    loading.value = false
    return
  }
  const shareId = typeof route.query.shareId === 'string' && route.query.shareId ? route.query.shareId : undefined
  file.value = {
    path,
    name: name.value,
    is_dir: false,
    size: 0,
    created_at: '',
    last_modified: '',
    _shareId: shareId,
    _originalName: name.value,
  }
  try {
    const { decryptUrl: url, access } = await registerFileDecrypt(file.value)
    decryptUrl.value = url
    file.value.size = access.size
    file.value.content_type = access.content_type
    if (viewerType.value === 'text' || viewerType.value === 'markdown' || viewerType.value === 'csv' || viewerType.value === 'notebook') {
      const res = await fetch(url)
      textContent.value = await res.text()
      if (viewerType.value === 'markdown') {
        const [{ default: MarkdownIt }, { default: DOMPurify }] = await Promise.all([
          import('markdown-it'),
          import('dompurify'),
        ])
        renderedHtml.value = DOMPurify.sanitize(new MarkdownIt({ linkify: true, breaks: true }).render(textContent.value))
      }
      if (viewerType.value === 'csv') {
        const { default: Papa } = await import('papaparse')
        const parsed = Papa.parse(textContent.value.trim(), { skipEmptyLines: true })
        csvRows.value = Array.isArray(parsed.data)
          ? parsed.data.slice(0, 40).filter(Array.isArray).map(row => row.map(cell => String(cell)))
          : []
      }
    }
  } catch (e: any) {
    previewError.value = e?.message || t('mobile.preview.unavailable')
  } finally {
    loading.value = false
  }
})

onBeforeUnmount(() => {
  unregisterFileDecrypt(decryptUrl.value)
})

function openMenu(): void {
  if (!file.value) return
  mobileUi.openFileSheet(file.value)
}

function handleBack(): void {
  const from = typeof route.query.from === 'string' ? route.query.from : ''
  if (from) {
    router.push(from)
    return
  }
  if (window.history.length > 1) router.back()
  else router.replace('/m/files')
}

function toggleChrome(): void {
  if (!contentReady.value) return
  showChrome.value = !showChrome.value
}

function handleMediaClick(): void {
  if (isTouchInput.value) toggleChrome()
}

function handleMediaDblClick(): void {
  if (!isTouchInput.value) toggleChrome()
}

function openDetails(): void {
  router.push({ path: '/m/details', query: { path: route.query.path, shareId: route.query.shareId || '', name: name.value, from: route.fullPath } })
}
</script>

<template>
  <section class="mobile-page preview-page">
    <MobileTopBar v-show="showChrome" :title="name" :subtitle="previewSubtitle" show-back @back="handleBack">
      <template #back><IconChevronLeft width="18" height="18" /></template>
      <template #actions>
        <button class="mobile-page-icon-btn" @click="openMenu"><IconMenu width="18" height="18" /></button>
      </template>
    </MobileTopBar>

    <div class="mobile-page-body preview-body" :class="{ 'preview-body--immersive': !showChrome }">
      <MobileEmptyState v-if="loading" :title="t('mobile.preview.title')" :body="t('mobile.preview.loading')" />
      <MobileEmptyState v-else-if="previewError" :title="t('mobile.preview.title')" :body="previewError" />
      <img v-else-if="viewerType === 'image'" :src="decryptUrl" class="preview-image" @click="toggleChrome" />
      <video v-else-if="viewerType === 'video'" :src="decryptUrl" class="preview-media" controls playsinline preload="metadata" @click="handleMediaClick" @dblclick.prevent="handleMediaDblClick" />
      <audio v-else-if="viewerType === 'audio'" :src="decryptUrl" class="preview-audio" controls />
      <iframe v-else-if="viewerType === 'pdf'" :src="decryptUrl" class="preview-frame" @click="toggleChrome" />
      <div v-else-if="viewerType === 'markdown' && renderedHtml" class="preview-markdown" v-html="renderedHtml" @click="toggleChrome" />
      <MobileEmptyState v-else-if="viewerType === 'markdown'" :title="t('mobile.preview.title')" :body="t('mobile.preview.markdown_empty')" />
      <div v-else-if="viewerType === 'csv' && csvRows.length" class="preview-table-wrap" @click="toggleChrome">
        <table class="preview-table">
          <tr v-for="(row, rowIndex) in csvRows" :key="rowIndex">
            <component :is="rowIndex === 0 ? 'th' : 'td'" v-for="(cell, cellIndex) in row" :key="cellIndex">{{ cell }}</component>
          </tr>
        </table>
      </div>
      <MobileEmptyState v-else-if="viewerType === 'csv'" :title="t('mobile.preview.title')" :body="t('mobile.preview.csv_empty')" />
      <pre v-else class="preview-text" @click="toggleChrome">{{ textContent }}</pre>
    </div>

    <div v-show="showChrome" class="preview-actions">
      <button @click="openDetails">{{ t('common.details') }}</button>
      <button @click="fs.downloadFile(file!._shareId ? file! : file!.path)">{{ t('common.download') }}</button>
      <button class="danger" @click="openMenu">{{ t('common.more') }}</button>
    </div>
  </section>
</template>

<style lang="scss" scoped>
.mobile-page {
  min-height: 100dvh;
}

.mobile-page-body {
  padding: 16px 16px 152px;
}

.preview-page {
  background: linear-gradient(180deg, #f0f4f8 0%, #e8eef5 100%);
}

.preview-body {
  display: flex;
  align-items: center;
  justify-content: center;
  transition: padding 0.2s ease;

  &--immersive {
    padding-top: 12px;
    padding-bottom: 84px;
  }
}

.preview-image,
.preview-media,
.preview-frame,
.preview-text,
.preview-audio,
.preview-markdown,
.preview-table-wrap {
  width: 100%;
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
}

.preview-image,
.preview-media {
  background: rgba(9, 16, 24, 0.92);
}

.preview-image,
.preview-media,
.preview-frame {
  min-height: 60vh;
  object-fit: contain;
  max-height: 72vh;
}

.preview-frame {
  background: #fff;
}

.preview-text {
  min-height: 60vh;
  padding: 20px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  color: #102030;
  font-size: 14px;
  line-height: 1.68;
}

.preview-markdown,
.preview-table-wrap {
  min-height: 60vh;
  padding: 20px;
  overflow: auto;
  color: #102030;
  line-height: 1.68;
  -webkit-overflow-scrolling: touch;
}

.preview-markdown {
  font-size: 15px;
}

.preview-markdown :deep(h1),
.preview-markdown :deep(h2),
.preview-markdown :deep(h3) {
  margin: 0 0 0.7em;
  line-height: 1.2;
}

.preview-markdown :deep(p),
.preview-markdown :deep(ul),
.preview-markdown :deep(ol),
.preview-markdown :deep(pre) {
  margin: 0 0 1em;
}

.preview-markdown :deep(code) {
  padding: 0.12em 0.35em;
  border-radius: 6px;
  background: rgba(16, 32, 48, 0.06);
}

.preview-markdown :deep(pre) {
  overflow: auto;
  padding: 14px;
  border-radius: 12px;
  background: rgba(16, 32, 48, 0.06);
}

.preview-table {
  width: 100%;
  border-collapse: collapse;
  min-width: 520px;

  th,
  td {
    padding: 10px 12px;
    border-bottom: 1px solid rgba(16, 32, 48, 0.08);
    text-align: left;
    font-size: 13px;
  }

  th {
    font-weight: 700;
    position: sticky;
    top: 0;
    background: rgba(255, 255, 255, 0.98);
    backdrop-filter: blur(8px);
  }
}

.preview-audio {
  padding: 14px;
}

.preview-audio :deep(audio) {
  width: 100%;
}

.preview-actions {
  position: fixed;
  left: 16px;
  right: 16px;
  bottom: calc(92px + env(safe-area-inset-bottom));
  z-index: 15;
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
  transition: transform 0.2s ease, opacity 0.2s ease;

  button {
    min-height: 48px;
    border: none;
    border-radius: 16px;
    background: rgba(255, 255, 255, 0.94);
    box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
    color: #102030;
    font-weight: 700;
    transition: transform 0.16s ease, background 0.16s ease;

    &:active {
      transform: scale(0.96);
      background: rgba(255, 255, 255, 1);
    }
  }

  .danger {
    color: #c23242;
  }
}

.mobile-page-icon-btn {
  width: 40px;
  height: 40px;
  border: none;
  border-radius: 12px;
  background: rgba(16, 32, 48, 0.06);
  @include inline-flex-center;
  transition: transform 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.94);
    background: rgba(16, 32, 48, 0.1);
  }
}

@media (max-width: 420px) {
  .preview-actions {
    grid-template-columns: 1fr;
  }

  .preview-image,
  .preview-media,
  .preview-frame,
  .preview-text,
  .preview-audio,
  .preview-markdown,
  .preview-table-wrap {
    border-radius: 18px;
  }
}

@media (min-width: 700px) {
  .preview-body {
    padding-left: 32px;
    padding-right: 32px;
  }

  .preview-actions {
    left: 50%;
    right: auto;
    width: min(460px, calc(100vw - 32px));
    transform: translateX(-50%);
  }
}
</style>
