<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import {
  IconArrowLeft,
  IconClose,
  IconDeleteOutline,
  IconDownload,
  IconFolderOutline,
  IconInformationOutline,
} from '../barrels/icons'
import { registerFileDecrypt, unregisterFileDecrypt } from '../composables/useFileAccess'
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
      <button class="back-button" :aria-label="t('files.back_to_files')" @click="goBack"><IconArrowLeft /></button>
      <div class="preview-title">
        <strong>{{ name }}</strong>
        <span>{{ file ? formatSize(file.size) : t('files.preview') }}</span>
      </div>
      <div class="preview-actions">
        <button @click="showDetails = true"><IconInformationOutline /><span>{{ t('menu.details') }}</span></button>
        <button @click="download"><IconDownload /><span>{{ t('menu.download') }}</span></button>
        <button class="danger" @click="remove"><IconDeleteOutline /><span>{{ t('menu.delete') }}</span></button>
      </div>
    </header>

    <section class="preview-canvas" :class="`preview-canvas--${previewKind}`">
      <div v-if="loading" class="preview-state">
        <div class="loading-orbit" />
        <strong>{{ t('files.decrypting_preview') }}</strong>
        <span>{{ t('files.decrypting_local') }}</span>
      </div>

      <div v-else-if="error" class="preview-state preview-state--error">
        <IconInformationOutline />
        <strong>{{ t('files.preview_failed') }}</strong>
        <span>{{ error }}</span>
        <button @click="download"><IconDownload />{{ t('menu.download') }}</button>
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
          <input v-model="fontSample" :placeholder="t('files.font_sample')" />
          <div v-for="size in [18, 28, 42, 64]" :key="size" :style="{ fontFamily, fontSize: `${size}px` }">{{ fontSample }}</div>
        </div>
        <div v-else class="preview-state">
          <IconInformationOutline />
          <strong>{{ viewerType === 'archive' ? t('files.archive_too_large') : t('files.no_native_preview') }}</strong>
          <span>{{ t('files.download_to_open') }}</span>
          <button @click="download"><IconDownload />{{ t('menu.download') }}</button>
        </div>

        <div v-if="truncated" class="truncated-notice">{{ t('files.preview_truncated') }}</div>
      </template>
    </section>

    <aside v-if="showDetails && file" class="preview-details">
      <div class="details-heading"><div><span>{{ t('menu.details') }}</span><strong>{{ name }}</strong></div><button @click="showDetails = false"><IconClose /></button></div>
      <dl>
        <div><dt>{{ t('info.type') }}</dt><dd>{{ file.content_type || viewerType || t('info.file') }}</dd></div>
        <div><dt>{{ t('info.size') }}</dt><dd>{{ formatSize(file.size) }}</dd></div>
        <div><dt>{{ t('files.path') }}</dt><dd class="path-value">{{ file.path }}</dd></div>
        <div v-if="file.last_modified"><dt>{{ t('info.modified') }}</dt><dd>{{ dayjs(file.last_modified).format('YYYY-MM-DD HH:mm') }}</dd></div>
      </dl>
      <button class="folder-button" @click="openContainingFolder"><IconFolderOutline />{{ t('files.open_containing_folder') }}</button>
    </aside>
  </main>
</template>

<style lang="scss" scoped>
.preview-shell {
  --ink: #18243b;
  --muted: #7b8497;
  --line: #e5e8ef;
  --accent: #5568e8;
  min-height: 100dvh;
  color: var(--ink);
  background: #eff1f6;
  font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  user-select: none;
}
button, input { font: inherit; }
.preview-header {
  position: fixed;
  inset: 0 0 auto;
  z-index: 20;
  display: grid;
  height: 66px;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 13px;
  padding: 0 18px;
  border-bottom: 1px solid rgba(224, 227, 235, .9);
  background: rgba(255, 255, 255, .92);
  backdrop-filter: blur(22px);
}
.back-button, .preview-actions button, .details-heading button {
  display: inline-flex;
  min-height: 36px;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 0 10px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: #fff;
  cursor: pointer;
  font-size: 10px;
  font-weight: 700;
}
.back-button { width: 38px; padding: 0; }
.back-button svg, .preview-actions svg { width: 17px; height: 17px; }
.preview-title { min-width: 0; }
.preview-title strong, .preview-title span { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.preview-title strong { font-size: 13px; }
.preview-title span { margin-top: 3px; color: var(--muted); font-size: 9px; }
.preview-actions { display: flex; gap: 7px; }
.preview-actions button.danger { color: #c64754; border-color: #efd8db; background: #fff8f8; }

.preview-canvas { position: fixed; inset: 66px 0 0; display: grid; place-items: center; overflow: auto; padding: 26px; }
.preview-canvas--image, .preview-canvas--video { background: #171b24; }
.preview-image, .preview-media { display: block; max-width: 100%; max-height: calc(100dvh - 118px); object-fit: contain; box-shadow: 0 18px 55px rgba(0, 0, 0, .2); }
.preview-media { width: min(1120px, 100%); }
.preview-pdf { width: min(1100px, 100%); height: calc(100dvh - 112px); border: 0; border-radius: 8px; background: #fff; box-shadow: 0 16px 50px rgba(30, 38, 58, .14); }
.preview-state { display: flex; min-height: 280px; align-items: center; justify-content: center; flex-direction: column; gap: 9px; color: var(--muted); text-align: center; }
.preview-state > svg { width: 38px; height: 38px; color: #9da5b5; }
.preview-state strong { color: var(--ink); font-size: 14px; }
.preview-state span { max-width: 380px; font-size: 10px; line-height: 1.5; }
.preview-state button { display: inline-flex; min-height: 38px; align-items: center; gap: 7px; margin-top: 6px; padding: 0 14px; border: 1px solid var(--line); border-radius: 9px; color: var(--accent); background: #fff; cursor: pointer; font-size: 10px; font-weight: 700; }
.preview-state button svg { width: 16px; height: 16px; }
.loading-orbit { width: 35px; height: 35px; margin-bottom: 4px; border: 2px solid #dce0e9; border-top-color: var(--accent); border-radius: 50%; animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.document-preview, .text-preview, .table-preview, .notebook-preview, .archive-preview, .font-preview {
  width: min(1050px, 100%);
  min-height: calc(100dvh - 120px);
  margin: auto;
  border-radius: 12px;
  color: #202a3d;
  background: #fff;
  box-shadow: 0 15px 45px rgba(30, 38, 58, .1);
  user-select: text;
}
.document-preview { width: min(850px, 100%); padding: clamp(24px, 5vw, 70px); font-size: 14px; line-height: 1.75; }
.markdown-body :deep(h1), .markdown-body :deep(h2), .markdown-body :deep(h3) { margin: 1.2em 0 .5em; line-height: 1.2; }
.markdown-body :deep(h1:first-child), .markdown-body :deep(h2:first-child) { margin-top: 0; }
.markdown-body :deep(pre) { overflow: auto; padding: 14px; border-radius: 8px; background: #f4f5f8; }
.markdown-body :deep(code) { padding: .12em .3em; border-radius: 4px; background: #f0f1f5; }
.markdown-body :deep(img) { max-width: 100%; }
.markdown-body :deep(table) { width: 100%; border-collapse: collapse; }
.markdown-body :deep(th), .markdown-body :deep(td) { padding: 8px 10px; border: 1px solid var(--line); text-align: left; }
.text-preview { padding: 22px; overflow: auto; font-family: "SFMono-Regular", Consolas, monospace; font-size: 12px; line-height: 1.65; white-space: pre-wrap; word-break: break-word; }
.table-preview { overflow: auto; }
.table-preview table { width: 100%; min-width: 640px; border-collapse: collapse; font-size: 11px; }
.table-preview th, .table-preview td { padding: 10px 12px; border-right: 1px solid #edf0f4; border-bottom: 1px solid #edf0f4; text-align: left; }
.table-preview th { position: sticky; top: 0; z-index: 2; background: #f7f8fa; font-weight: 750; }
.notebook-preview { display: flex; flex-direction: column; gap: 8px; padding: 22px; }
.notebook-preview article { position: relative; padding: 14px; border-left: 3px solid #7a8ae9; border-radius: 8px; background: #f7f8fb; }
.notebook-preview article.cell-markdown { border-left-color: #54a880; background: #f8fbf9; }
.notebook-preview article > span { color: var(--muted); font-size: 8px; font-weight: 800; letter-spacing: .08em; text-transform: uppercase; }
.notebook-preview pre { margin: 8px 0 0; overflow: auto; font-size: 11px; line-height: 1.55; white-space: pre-wrap; }
.archive-preview { min-height: auto; padding: 10px; }
.archive-heading { display: flex; align-items: center; justify-content: space-between; padding: 12px; border-bottom: 1px solid var(--line); font-size: 11px; }
.archive-heading span { color: var(--muted); }
.archive-entry { display: grid; min-height: 38px; grid-template-columns: 20px minmax(0, 1fr); align-items: center; gap: 7px; padding: 0 10px; border-bottom: 1px solid #eff1f5; font-size: 10px; }
.archive-entry svg { width: 15px; height: 15px; color: var(--accent); }
.archive-file-dot { width: 6px; height: 6px; margin-left: 5px; border-radius: 50%; background: #a4abbb; }
.font-preview { display: flex; flex-direction: column; gap: 24px; padding: clamp(22px, 5vw, 60px); overflow: hidden; }
.font-preview input { padding: 10px 12px; border: 1px solid var(--line); border-radius: 8px; outline: 0; }
.font-preview > div { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.audio-card { display: flex; width: min(480px, 100%); align-items: center; flex-direction: column; gap: 18px; padding: 30px; border-radius: 18px; background: #fff; box-shadow: 0 16px 45px rgba(30, 38, 58, .12); }
.audio-art { display: grid; width: 170px; height: 170px; place-items: center; border-radius: 22px; color: #fff; background: linear-gradient(145deg, #697af0, #3d4dc0); font-size: 64px; }
.audio-card strong { max-width: 100%; overflow: hidden; font-size: 14px; text-overflow: ellipsis; white-space: nowrap; }
.audio-card audio { width: 100%; }
.truncated-notice { position: fixed; left: 50%; bottom: 16px; z-index: 10; padding: 8px 13px; transform: translateX(-50%); border-radius: 9px; color: #fff; background: rgba(31, 40, 60, .88); font-size: 9px; backdrop-filter: blur(10px); }

.preview-details { position: fixed; top: 78px; right: 12px; bottom: 12px; z-index: 30; display: flex; width: 310px; flex-direction: column; padding: 18px; border: 1px solid var(--line); border-radius: 14px; background: rgba(255, 255, 255, .98); box-shadow: 0 18px 55px rgba(29, 38, 61, .16); backdrop-filter: blur(22px); }
.details-heading { display: flex; align-items: start; justify-content: space-between; gap: 10px; }
.details-heading span, .details-heading strong { display: block; }
.details-heading span { color: var(--muted); font-size: 8px; font-weight: 800; letter-spacing: .1em; text-transform: uppercase; }
.details-heading strong { margin-top: 4px; max-width: 220px; overflow: hidden; font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.details-heading button { width: 30px; min-height: 30px; padding: 0; }
.preview-details dl { margin: 25px 0; }
.preview-details dl div { display: grid; grid-template-columns: 78px minmax(0, 1fr); gap: 9px; padding: 10px 0; border-bottom: 1px solid var(--line); font-size: 10px; }
.preview-details dt { color: var(--muted); }
.preview-details dd { margin: 0; overflow-wrap: anywhere; }
.path-value { font-family: ui-monospace, monospace; }
.folder-button { display: flex; min-height: 40px; align-items: center; justify-content: center; gap: 7px; margin-top: auto; border: 1px solid var(--line); border-radius: 9px; color: var(--accent); background: #fff; cursor: pointer; font-size: 10px; font-weight: 700; }
.folder-button svg { width: 16px; height: 16px; }

@media (max-width: 680px) {
  .preview-header { height: 60px; padding: max(8px, env(safe-area-inset-top)) 10px 8px; }
  .preview-actions button { width: 36px; min-height: 36px; padding: 0; }
  .preview-actions button span { display: none; }
  .preview-actions button.danger { display: none; }
  .preview-canvas { inset: 60px 0 0; padding: 10px; }
  .preview-image, .preview-media { max-height: calc(100dvh - 80px); }
  .preview-pdf { height: calc(100dvh - 80px); border-radius: 4px; }
  .document-preview, .text-preview, .table-preview, .notebook-preview, .archive-preview, .font-preview { min-height: calc(100dvh - 80px); border-radius: 8px; }
  .document-preview { padding: 22px 18px; font-size: 13px; }
  .text-preview { padding: 15px; font-size: 11px; }
  .notebook-preview { padding: 12px; }
  .audio-card { padding: 22px 16px; }
  .audio-art { width: 140px; height: 140px; }
  .preview-details { top: auto; right: 0; bottom: 0; left: 0; width: auto; max-height: 78dvh; padding-bottom: calc(18px + env(safe-area-inset-bottom)); border-width: 1px 0 0; border-radius: 20px 20px 0 0; }
}

@media (prefers-reduced-motion: reduce) {
  .loading-orbit { animation-duration: .01ms; }
}
</style>
