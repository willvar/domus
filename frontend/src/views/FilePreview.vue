<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NDescriptions,
  NDescriptionsItem,
  NDrawer,
  NDrawerContent,
  NInput,
  NResult,
  NSpin,
} from 'naive-ui'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import {
  IconArrowLeft,
  IconDeleteOutline,
  IconDownload,
  IconFolderOutline,
  IconInformationOutline,
} from '../barrels/icons'
import { registerFileDecrypt, unregisterFileDecrypt } from '../composables/useFileAccess'
import { useDevice } from '../composables/useDevice'
import { useI18n } from '../composables/useI18n'
import { showConfirm } from '../composables/useNativeDialog'
import api from '../composables/useApi'
import { useFileSystemStore } from '../stores/fileSystem'
import type { FileListItem, ViewerType } from '../types'

const TEXT_PREVIEW_LIMIT = 2 * 1024 * 1024
const ARCHIVE_PREVIEW_LIMIT = 128 * 1024 * 1024

const route = useRoute()
const router = useRouter()
const fs = useFileSystemStore()
const { t } = useI18n()
const { isMobile } = useDevice()

const file = ref<FileListItem | null>(null)
const decryptUrl = ref('')
const loading = ref(true)
const error = ref('')
const textContent = ref('')
const renderedHTML = ref('')
const csvRows = ref<string[][]>([])
const notebookCells = ref<Array<{ type: string; source: string }>>([])
const archiveEntries = ref<Array<{ name: string; dir: boolean; size?: number }>>([])
const truncated = ref(false)
const showDetails = ref(false)
const fontFamily = ref('sans-serif')
const fontSample = ref('Domus · 对象存储原生的私人文件空间')
const openedAt = Date.now()
let loadedFont: FontFace | null = null

const name = computed(() => file.value?.name || (typeof route.query.name === 'string' ? route.query.name : t('files.preview')))
const viewerType = computed<ViewerType | null>(() => fs.getViewerType(name.value))
const previewKind = computed(() => viewerType.value || 'unsupported')
const parentPath = computed(() => {
  const path = file.value?.path || (typeof route.query.path === 'string' ? route.query.path : '/')
  const trimmed = path.replace(/\/$/, '')
  const index = trimmed.lastIndexOf('/')
  return index <= 0 ? '/' : `${trimmed.slice(0, index)}/`
})

onMounted(async () => {
  const path = typeof route.query.path === 'string' ? route.query.path : ''
  if (!path) {
    error.value = t('files.preview_missing')
    loading.value = false
    return
  }

  file.value = {
    path,
    name: typeof route.query.name === 'string' ? route.query.name : path.split('/').pop() || '',
    is_dir: false,
    size: 0,
    created_at: '',
    last_modified: '',
  }

  try {
    const registered = await registerFileDecrypt(file.value)
    decryptUrl.value = registered.decryptUrl
    file.value = {
      ...file.value,
      name: registered.access.name || file.value.name,
      size: registered.access.size,
      content_type: registered.access.content_type,
    }
    await loadRichPreview(previewKind.value, registered.access.size)
  } catch (reason: any) {
    error.value = reason?.message || t('files.preview_failed')
  } finally {
    loading.value = false
  }
})

onBeforeUnmount(() => {
  unregisterFileDecrypt(decryptUrl.value)
  if (loadedFont) document.fonts.delete(loadedFont)
  if (file.value) {
    void api.post('/audit/', {
      path: file.value.path,
      duration_ms: Date.now() - openedAt,
      type: previewKind.value,
    }).catch(() => {})
  }
})

async function loadRichPreview(kind: string, size: number): Promise<void> {
  if (['text', 'markdown', 'csv', 'notebook'].includes(kind)) {
    const headers = size > TEXT_PREVIEW_LIMIT ? { Range: `bytes=0-${TEXT_PREVIEW_LIMIT - 1}` } : undefined
    const response = await fetch(decryptUrl.value, { headers })
    if (!response.ok && response.status !== 206) throw new Error(`Preview fetch failed: ${response.status}`)
    textContent.value = await response.text()
    truncated.value = size > TEXT_PREVIEW_LIMIT

    if (kind === 'markdown') {
      const [{ default: MarkdownIt }, { default: DOMPurify }] = await Promise.all([
        import('markdown-it'),
        import('dompurify'),
      ])
      renderedHTML.value = DOMPurify.sanitize(new MarkdownIt({ linkify: true, breaks: true }).render(textContent.value))
    } else if (kind === 'csv') {
      const { default: Papa } = await import('papaparse')
      const parsed = Papa.parse(textContent.value, { skipEmptyLines: true })
      csvRows.value = Array.isArray(parsed.data)
        ? parsed.data.slice(0, 200).filter(Array.isArray).map(row => row.map(value => String(value)))
        : []
      if (Array.isArray(parsed.data) && parsed.data.length > 200) truncated.value = true
    } else if (kind === 'notebook') {
      const notebook = JSON.parse(textContent.value) as { cells?: Array<{ cell_type?: string; source?: string | string[] }> }
      notebookCells.value = (notebook.cells || []).slice(0, 200).map(cell => ({
        type: cell.cell_type || 'raw',
        source: Array.isArray(cell.source) ? cell.source.join('') : cell.source || '',
      }))
    }
    return
  }

  if (kind === 'archive' && size <= ARCHIVE_PREVIEW_LIMIT) {
    const [{ default: JSZip }, response] = await Promise.all([
      import('jszip'),
      fetch(decryptUrl.value),
    ])
    const zip = await JSZip.loadAsync(await response.arrayBuffer())
    archiveEntries.value = Object.values(zip.files).slice(0, 500).map(entry => ({
      name: entry.name,
      dir: entry.dir,
    }))
    truncated.value = Object.keys(zip.files).length > 500
    return
  }

  if (kind === 'font') {
    const response = await fetch(decryptUrl.value)
    const family = `domus-preview-${Date.now()}`
    loadedFont = new FontFace(family, await response.arrayBuffer())
    await loadedFont.load()
    document.fonts.add(loadedFont)
    fontFamily.value = `'${family}'`
  }
}

function goBack(): void {
  const from = typeof route.query.from === 'string' ? route.query.from : ''
  if (from.startsWith('/files')) {
    void router.replace(from)
    return
  }
  void router.replace({ path: '/files', query: parentPath.value === '/' ? {} : { path: parentPath.value } })
}

function openContainingFolder(): void {
  void router.replace({ path: '/files', query: { path: parentPath.value } })
}

async function download(): Promise<void> {
  if (file.value) await fs.downloadFile(file.value.path)
}

async function remove(): Promise<void> {
  if (!file.value) return
  const ok = await showConfirm(t('dialog.delete_title'), t('dialog.confirm_delete', { n: 1 }), {
    icon: 'warning',
    positiveType: 'error',
  })
  if (!ok) return
  await api.delete('/file/delete', { params: { path: file.value.path } })
  openContainingFolder()
}

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / (1024 ** index)
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`
}
</script>

<template>
  <main class="preview-shell">
    <header class="preview-header">
      <NButton circle class="back-button" :aria-label="t('files.back_to_files')" @click="goBack">
        <template #icon><IconArrowLeft /></template>
      </NButton>
      <div class="preview-title">
        <strong>{{ name }}</strong>
        <span>{{ file ? formatSize(file.size) : t('files.preview') }}</span>
      </div>
      <div class="preview-actions">
        <NButton @click="showDetails = true">
          <template #icon><IconInformationOutline /></template><span>{{ t('menu.details') }}</span>
        </NButton>
        <NButton @click="download">
          <template #icon><IconDownload /></template><span>{{ t('menu.download') }}</span>
        </NButton>
        <NButton type="error" secondary class="danger" @click="remove">
          <template #icon><IconDeleteOutline /></template><span>{{ t('menu.delete') }}</span>
        </NButton>
      </div>
    </header>

    <section class="preview-canvas" :class="`preview-canvas--${previewKind}`">
      <div v-if="loading" class="preview-state">
        <NSpin size="large" />
        <strong>{{ t('files.decrypting_preview') }}</strong>
        <span>{{ t('files.decrypting_local') }}</span>
      </div>

      <div v-else-if="error" class="preview-state preview-state--error">
        <NResult status="error" :title="t('files.preview_failed')" :description="error">
          <template #footer>
            <NButton @click="download"><template #icon><IconDownload /></template>{{ t('menu.download') }}</NButton>
          </template>
        </NResult>
      </div>

      <template v-else>
        <img v-if="viewerType === 'image'" :src="decryptUrl" class="preview-image" :alt="name" />
        <video v-else-if="viewerType === 'video'" :src="decryptUrl" class="preview-media" controls playsinline preload="metadata" />
        <div v-else-if="viewerType === 'audio'" class="audio-card">
          <div class="audio-art"><span>♪</span></div>
          <strong>{{ name }}</strong>
          <audio :src="decryptUrl" controls preload="metadata" />
        </div>
        <iframe v-else-if="viewerType === 'pdf'" :src="decryptUrl" class="preview-pdf" :title="name" />
        <article v-else-if="viewerType === 'markdown'" class="document-preview markdown-body" v-html="renderedHTML" />
        <div v-else-if="viewerType === 'csv'" class="table-preview">
          <table>
            <tr v-for="(row, rowIndex) in csvRows" :key="rowIndex">
              <component :is="rowIndex === 0 ? 'th' : 'td'" v-for="(cell, cellIndex) in row" :key="cellIndex">{{ cell }}</component>
            </tr>
          </table>
        </div>
        <div v-else-if="viewerType === 'notebook'" class="notebook-preview">
          <article v-for="(cell, index) in notebookCells" :key="index" :class="`cell-${cell.type}`">
            <span>{{ cell.type }}</span>
            <pre>{{ cell.source }}</pre>
          </article>
        </div>
        <pre v-else-if="viewerType === 'text'" class="text-preview">{{ textContent }}</pre>
        <div v-else-if="viewerType === 'archive' && archiveEntries.length" class="archive-preview">
          <div class="archive-heading"><strong>{{ t('files.archive_contents') }}</strong><span>{{ archiveEntries.length }}</span></div>
          <div v-for="entry in archiveEntries" :key="entry.name" class="archive-entry">
            <IconFolderOutline v-if="entry.dir" />
            <span v-else class="archive-file-dot" />
            <span>{{ entry.name }}</span>
          </div>
        </div>
        <div v-else-if="viewerType === 'font'" class="font-preview">
          <NInput v-model:value="fontSample" :placeholder="t('files.font_sample')" />
          <div v-for="size in [18, 28, 42, 64]" :key="size" :style="{ fontFamily, fontSize: `${size}px` }">{{ fontSample }}</div>
        </div>
        <div v-else class="preview-state">
          <NResult
            status="info"
            :title="viewerType === 'archive' ? t('files.archive_too_large') : t('files.no_native_preview')"
            :description="t('files.download_to_open')"
          >
            <template #footer>
              <NButton @click="download"><template #icon><IconDownload /></template>{{ t('menu.download') }}</NButton>
            </template>
          </NResult>
        </div>

        <NAlert v-if="truncated" class="truncated-notice" type="warning" :show-icon="false">
          {{ t('files.preview_truncated') }}
        </NAlert>
      </template>
    </section>

    <NDrawer
      :show="showDetails && Boolean(file)"
      :placement="isMobile ? 'bottom' : 'right'"
      :width="isMobile ? undefined : 390"
      :height="isMobile ? '72vh' : undefined"
      @update:show="showDetails = $event"
    >
      <NDrawerContent v-if="file" class="preview-details" :title="name" closable>
        <NDescriptions :column="1" label-placement="top" bordered size="small">
          <NDescriptionsItem :label="t('info.type')">{{ file.content_type || viewerType || t('info.file') }}</NDescriptionsItem>
          <NDescriptionsItem :label="t('info.size')">{{ formatSize(file.size) }}</NDescriptionsItem>
          <NDescriptionsItem :label="t('files.path')"><span class="path-value">{{ file.path }}</span></NDescriptionsItem>
          <NDescriptionsItem v-if="file.last_modified" :label="t('info.modified')">{{ dayjs(file.last_modified).format('YYYY-MM-DD HH:mm') }}</NDescriptionsItem>
        </NDescriptions>
        <template #footer>
          <NButton type="primary" block class="folder-button" @click="openContainingFolder">
            <template #icon><IconFolderOutline /></template>{{ t('files.open_containing_folder') }}
          </NButton>
        </template>
      </NDrawerContent>
    </NDrawer>
  </main>
</template>

<style lang="scss" scoped>
.preview-shell {
  --ink: #172033;
  --muted: #626d82;
  --line: #e3e7ef;
  --accent: #4f5fe7;
  min-height: 100dvh;
  color: var(--ink);
  background: #eef1f6;
  font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  user-select: none;
}
.preview-header {
  position: fixed;
  inset: 0 0 auto;
  z-index: 20;
  display: grid;
  height: 72px;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 14px;
  padding: 0 22px;
  border-bottom: 1px solid rgba(224, 227, 235, .9);
  background: rgba(255, 255, 255, .92);
  backdrop-filter: blur(22px);
}
.back-button { width: 42px; height: 42px; }
.back-button svg, .preview-actions svg { width: 18px; height: 18px; }
.preview-title { min-width: 0; }
.preview-title strong, .preview-title span { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.preview-title strong { font-size: 14px; font-weight: 680; }
.preview-title span { margin-top: 3px; color: var(--muted); font-size: 11px; }
.preview-actions { display: flex; gap: 8px; }

.preview-canvas { position: fixed; inset: 72px 0 0; display: grid; place-items: center; overflow: auto; padding: 30px; }
.preview-canvas--image, .preview-canvas--video { background: #151a24; }
.preview-image, .preview-media { display: block; max-width: 100%; max-height: calc(100dvh - 128px); object-fit: contain; box-shadow: 0 24px 70px rgb(0 0 0 / 24%); }
.preview-media { width: min(1120px, 100%); }
.preview-pdf { width: min(1120px, 100%); height: calc(100dvh - 128px); border: 0; border-radius: 12px; background: #fff; box-shadow: 0 18px 58px rgb(30 38 58 / 15%); }
.preview-state { display: flex; min-height: 280px; align-items: center; justify-content: center; flex-direction: column; gap: 9px; color: var(--muted); text-align: center; }
.preview-state > svg { width: 38px; height: 38px; color: var(--muted); }
.preview-state strong { color: var(--ink); font-size: 16px; }
.preview-state span { max-width: 390px; font-size: 13px; line-height: 1.55; }

.document-preview, .text-preview, .table-preview, .notebook-preview, .archive-preview, .font-preview {
  width: min(1050px, 100%);
  min-height: calc(100dvh - 136px);
  margin: auto;
  border-radius: 16px;
  color: #202a3d;
  background: #fff;
  box-shadow: 0 15px 45px rgba(30, 38, 58, .1);
  user-select: text;
}
.document-preview { width: min(860px, 100%); padding: clamp(28px, 5vw, 72px); font-size: 15px; line-height: 1.78; }
.markdown-body :deep(h1), .markdown-body :deep(h2), .markdown-body :deep(h3) { margin: 1.2em 0 .5em; line-height: 1.2; }
.markdown-body :deep(h1:first-child), .markdown-body :deep(h2:first-child) { margin-top: 0; }
.markdown-body :deep(pre) { overflow: auto; padding: 14px; border-radius: 8px; background: #f4f5f8; }
.markdown-body :deep(code) { padding: .12em .3em; border-radius: 4px; background: #f0f1f5; }
.markdown-body :deep(img) { max-width: 100%; }
.markdown-body :deep(table) { width: 100%; border-collapse: collapse; }
.markdown-body :deep(th), .markdown-body :deep(td) { padding: 8px 10px; border: 1px solid var(--line); text-align: left; }
.text-preview { padding: 26px; overflow: auto; font-family: "SFMono-Regular", Consolas, monospace; font-size: 13px; line-height: 1.68; white-space: pre-wrap; word-break: break-word; }
.table-preview { overflow: auto; }
.table-preview table { width: 100%; min-width: 640px; border-collapse: collapse; font-size: 12px; }
.table-preview th, .table-preview td { padding: 12px 14px; border-right: 1px solid #edf0f4; border-bottom: 1px solid #edf0f4; text-align: left; }
.table-preview th { position: sticky; top: 0; z-index: 2; background: #f7f8fa; font-weight: 750; }
.notebook-preview { display: flex; flex-direction: column; gap: 8px; padding: 22px; }
.notebook-preview article { position: relative; padding: 14px; border-left: 3px solid #7a8ae9; border-radius: 8px; background: #f7f8fb; }
.notebook-preview article.cell-markdown { border-left-color: #54a880; background: #f8fbf9; }
.notebook-preview article > span { color: var(--muted); font-size: 10px; font-weight: 800; letter-spacing: .08em; text-transform: uppercase; }
.notebook-preview pre { margin: 8px 0 0; overflow: auto; font-size: 12px; line-height: 1.6; white-space: pre-wrap; }
.archive-preview { min-height: auto; padding: 10px; }
.archive-heading { display: flex; align-items: center; justify-content: space-between; padding: 14px; border-bottom: 1px solid var(--line); font-size: 13px; }
.archive-heading span { color: var(--muted); }
.archive-entry { display: grid; min-height: 44px; grid-template-columns: 22px minmax(0, 1fr); align-items: center; gap: 8px; padding: 0 12px; border-bottom: 1px solid #eff1f5; font-size: 12px; }
.archive-entry svg { width: 15px; height: 15px; color: var(--accent); }
.archive-file-dot { width: 6px; height: 6px; margin-left: 5px; border-radius: 50%; background: #a4abbb; }
.font-preview { display: flex; flex-direction: column; gap: 24px; padding: clamp(22px, 5vw, 60px); overflow: hidden; }
.font-preview > div { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.audio-card { display: flex; width: min(500px, 100%); align-items: center; flex-direction: column; gap: 20px; padding: 36px; border-radius: 22px; background: #fff; box-shadow: 0 20px 58px rgb(30 38 58 / 14%); }
.audio-art { display: grid; width: 170px; height: 170px; place-items: center; border-radius: 22px; color: #fff; background: linear-gradient(145deg, #697af0, #3d4dc0); font-size: 64px; }
.audio-card strong { max-width: 100%; overflow: hidden; font-size: 16px; text-overflow: ellipsis; white-space: nowrap; }
.audio-card audio { width: 100%; }
.truncated-notice { position: fixed; left: 50%; bottom: 16px; z-index: 10; width: max-content; max-width: calc(100vw - 32px); transform: translateX(-50%); }

.path-value { font-family: ui-monospace, monospace; }
.folder-button svg { width: 16px; height: 16px; }

@media (max-width: 680px) {
  .preview-header { height: 64px; padding: max(8px, env(safe-area-inset-top)) 12px 8px; }
  .preview-actions button { width: 42px; height: 42px; padding: 0; }
  .preview-actions button span { display: none; }
  .preview-actions button.danger { display: none; }
  .preview-canvas { inset: 64px 0 0; padding: 10px; }
  .preview-image, .preview-media { max-height: calc(100dvh - 84px); }
  .preview-pdf { height: calc(100dvh - 84px); border-radius: 6px; }
  .document-preview, .text-preview, .table-preview, .notebook-preview, .archive-preview, .font-preview { min-height: calc(100dvh - 84px); border-radius: 10px; }
  .document-preview { padding: 22px 18px; font-size: 13px; }
  .text-preview { padding: 15px; font-size: 11px; }
  .notebook-preview { padding: 12px; }
  .audio-card { padding: 22px 16px; }
  .audio-art { width: 140px; height: 140px; }
}
</style>
