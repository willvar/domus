<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NButtonGroup,
  NDescriptions,
  NDescriptionsItem,
  NDrawer,
  NDrawerContent,
  NDropdown,
  NInput,
  NResult,
  NSpin,
} from 'naive-ui'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import exifr from 'exifr'
import {
  IconArrowLeft,
  IconClose,
  IconContentSave,
  IconDeleteOutline,
  IconDownload,
  IconFolderOutline,
  IconInformationOutline,
  IconPencilOutline,
  IconTune,
} from '../barrels/icons'
import { writeEncryptedFile } from '../composables/useCryptoUpload'
import { registerFileDecrypt, unregisterFileDecrypt } from '../composables/useFileAccess'
import pdfWorkerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { mediaMetaItems, parseMediaMeta } from '../utils/mediaMeta'
import type { MetaItem } from '../utils/mediaMeta'
import { useDevice } from '../composables/useDevice'
import { useI18n } from '../composables/useI18n'
import { showConfirm } from '../composables/useNativeDialog'
import { RENDITION_PROFILES, useRenditions } from '../composables/useRenditions'
import type { PlaybackStats } from '../composables/useRenditions'
import api from '../composables/useApi'
import { useFileSystemStore } from '../stores/fileSystem'
import { useAppMessage } from '../ui/feedback'
import type { FileListItem, ViewerType } from '../types'
import {
  isTrashLocation,
  parseTrashLocation,
  trashLocationRouteQuery,
  trashParentLocation,
} from '../utils/trashLocation'

const TEXT_PREVIEW_LIMIT = 2 * 1024 * 1024
const ARCHIVE_PREVIEW_LIMIT = 128 * 1024 * 1024

const route = useRoute()
const router = useRouter()
const fs = useFileSystemStore()
const { t, te } = useI18n()
const { isMobile } = useDevice()
const message = useAppMessage()
const TextEditorPane = defineAsyncComponent(() => import('../components/TextEditorPane.vue'))

interface EditorScrollPosition {
  line: number
  totalLines: number
  scrollTop: number
  maxScroll: number
}

interface TextEditorPaneExpose {
  scrollToLine: (line: number) => void
  scrollToProgress: (progress: number) => void
}

interface MarkdownScrollAnchor {
  line: number
  top: number
}

const file = ref<FileListItem | null>(null)
const decryptUrl = ref('')
const loading = ref(true)
const error = ref('')
const videoEl = ref<HTMLVideoElement | null>(null)
const textContent = ref('')
const renderedHTML = ref('')
const csvRows = ref<string[][]>([])
const notebookCells = ref<Array<{ type: string; source: string }>>([])
const archiveEntries = ref<Array<{ name: string; dir: boolean; size?: number }>>([])
const sourceTruncated = ref(false)
const viewTruncated = ref(false)
const showDetails = ref(false)
const fontFamily = ref('sans-serif')
const fontSample = ref(t('files.font_sample_text'))
const editing = ref(false)
const dirty = ref(false)
const saving = ref(false)
const preparingEdit = ref(false)
const originalContent = ref('')
const fileDEK = ref('')
const fileGeneration = ref(0)
const editorMode = ref<'source' | 'preview' | 'split' | 'table'>('source')
const editorPane = ref<TextEditorPaneExpose | null>(null)
const markdownPreview = ref<HTMLElement | null>(null)
const openedAt = Date.now()
let loadedFont: FontFace | null = null
let markdownRenderer: ((source: string) => string) | null = null
let csvParser: ((source: string) => string[][]) | null = null
let lastEditorScroll: EditorScrollPosition | null = null
let scrollSyncOrigin: 'source' | 'preview' | null = null
let scrollSyncReleaseFrame = 0

const name = computed(() => file.value?.name || (typeof route.query.name === 'string' ? route.query.name : t('files.preview')))
const viewerType = computed<ViewerType | null>(() => fs.getViewerType(name.value))
const previewKind = computed(() => viewerType.value || 'unsupported')
const inTrash = computed(() => isTrashLocation(file.value?.path || ''))
const isVideoFile = computed(() => viewerType.value === 'video')
const quality = useRenditions({
  decryptUrl: () => decryptUrl.value,
  sourceCodecs: () => file.value?.media_codecs,
})
const switchingQuality = ref(false)
let qualitySelection = 0
function qualityLabelOf(profile: string): string {
  return profile === 'original' ? t('quality.original') : profile.toUpperCase()
}
const qualityOptions = computed(() => {
  const options: Array<{ label: string; key: string }> = [{ label: t('quality.original'), key: 'original' }]
  // Profiles above the source height would be pointless upscales, but the
  // same-height profile stays: a CRF re-encode of the original size is a
  // useful fallback when the original codec refuses to play.
  const cap = quality.sourceHeight.value
  for (const profile of RENDITION_PROFILES) {
    if (cap > 0 && parseInt(profile, 10) > cap) continue
    options.push({ label: qualityLabelOf(profile), key: profile })
  }
  return options
})
const qualityLabel = computed(() => quality.activeQuality.value === 'original'
  ? t('quality.original')
  : quality.activeQuality.value.toUpperCase())

// File metadata ("元数据"): best-effort extras per preview kind, parsed
// client-side from the decrypted bytes the SW serves via decryptUrl, plus the
// worker-probed media_meta for audio/video.
const metaItems = ref<MetaItem[]>([])
const archiveCounts = ref<{ entries: number; files: number; uncompressed: number } | null>(null)

function formatShutter(seconds?: number): string | undefined {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) return undefined
  if (seconds >= 1) return `${+seconds.toFixed(1)}s`
  return `1/${Math.round(1 / seconds)}s`
}

function formatExposureBias(bias?: number): string | undefined {
  if (typeof bias !== 'number' || !Number.isFinite(bias) || bias === 0) return undefined
  const rounded = Math.round(bias * 10) / 10
  return `${rounded > 0 ? '+' : ''}${rounded} EV`
}

function formatExifDate(value?: Date | string | number): string | undefined {
  if (value === undefined || value === null || value === '') return undefined
  if (value instanceof Date) return dayjs(value).format('YYYY-MM-DD HH:mm')
  if (typeof value !== 'string') return undefined
  const parsed = dayjs(value.replace(/^(\d{4}):(\d{2}):(\d{2})/, '$1-$2-$3'))
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm') : undefined
}

function formatExifDimensions(e: Record<string, unknown>): string | undefined {
  const e2 = e as Record<string, any>
  const width = e2.ExifImageWidth ?? e2.ImageWidth ?? e2.PixelXDimension
  const height = e2.ExifImageHeight ?? e2.ImageHeight ?? e2.PixelYDimension
  if (!width || !height) return undefined
  return `${width} × ${height}`
}

// PDF dates arrive as "D:20240102103456+08'00'".
function formatPdfDate(value?: string): string | undefined {
  if (!value) return undefined
  const match = String(value).replace(/^D:/, '').match(/^(\d{4})(\d{2})(\d{2})(\d{2})?(\d{2})?/)
  if (!match) return undefined
  return `${match[1]}-${match[2]}-${match[3]} ${match[4] && match[5] ? `${match[4]}:${match[5]}` : ''}`.trim()
}

async function loadMetadata(): Promise<void> {
  try {
    if (viewerType.value === 'image') await loadImageMeta()
    else if (viewerType.value === 'video' || viewerType.value === 'audio') loadMediaMeta()
    else if (viewerType.value === 'pdf') await loadPdfMeta()
    else if (viewerType.value === 'archive') loadArchiveMeta()
    else if (viewerType.value && ['text', 'markdown', 'csv', 'notebook'].includes(viewerType.value)) loadTextMeta()
  } catch {
    // Metadata is optional; never let it break the preview.
  }
}

function pushMeta(items: MetaItem[], label: string, value?: string | number | null, href?: string): void {
  if (value === undefined || value === null || value === '') return
  items.push({ label, value: String(value), href })
}

function loadMediaMeta(): void {
  const meta = parseMediaMeta(file.value?.media_meta)
  if (meta) metaItems.value = mediaMetaItems(meta)
}

async function loadImageMeta(): Promise<void> {
  if (!decryptUrl.value) return
  const response = await fetch(decryptUrl.value)
  if (!response.ok) return
  const buffer = await response.arrayBuffer()
  const parsed = await exifr.parse(buffer, { tiff: true, exif: true, ifd0: {}, xmp: {} })
  const e = (parsed && Object.keys(parsed).length ? parsed : null) as Record<string, any> | null
  const items: MetaItem[] = []
  if (e) {
    pushMeta(items, t('info.meta_camera'), [e.Make, e.Model].filter(Boolean).join(' '))
    pushMeta(items, t('info.meta_lens'), e.LensModel || e.LensMake)
    pushMeta(items, t('info.meta_taken'), formatExifDate(e.DateTimeOriginal ?? e.CreateDate))
    pushMeta(items, t('info.meta_dimensions'), formatExifDimensions(e))
    pushMeta(items, t('info.meta_keywords'), e.Keywords ?? e.Subject)
    pushMeta(items, t('info.meta_shutter'), formatShutter(e.ExposureTime))
    pushMeta(items, t('info.meta_aperture'), typeof e.FNumber === 'number' ? `f/${e.FNumber}` : undefined)
    pushMeta(items, t('info.meta_iso'), e.ISO)
    pushMeta(items, t('info.meta_focal'), typeof e.FocalLength === 'number' ? `${Math.round(e.FocalLength)}mm` : undefined)
    pushMeta(items, t('info.meta_focal_35mm'), e.FocalLengthIn35mmFilm ? `${e.FocalLengthIn35mmFilm}mm` : undefined)
    pushMeta(items, t('info.meta_bias'), formatExposureBias(e.ExposureCompensation ?? e.ExposureBiasValue))
    pushMeta(items, t('info.meta_metering'), e.MeteringMode)
    pushMeta(items, t('info.meta_white_balance'), e.WhiteBalance)
    pushMeta(items, t('info.meta_flash'), e.Flash)
    pushMeta(items, t('info.meta_software'), e.Software)
  }
  // Resolution fallback: some images (screenshots, edited exports) have no
  // EXIF dimensions; read the decoder frame geometry instead.
  if (!items.some(item => item.label === t('info.meta_dimensions'))) {
    const geometry = await imageGeometry(decryptUrl.value).catch(() => null)
    if (geometry) {
      pushMeta(items, t('info.meta_dimensions'), `${geometry.width} × ${geometry.height}`)
    }
  }
  const gps = await exifr.gps(new Uint8Array(buffer)).catch(() => null)
  if (gps && Number.isFinite(gps.latitude) && Number.isFinite(gps.longitude)) {
    pushMeta(
      items,
      t('info.meta_gps'),
      `${gps.latitude.toFixed(6)}, ${gps.longitude.toFixed(6)}`,
      `https://www.openstreetmap.org/?mlat=${gps.latitude}&mlon=${gps.longitude}#map=17/${gps.latitude}/${gps.longitude}`,
    )
  }
  metaItems.value = items
}

function imageGeometry(url: string): Promise<{ width: number; height: number }> {
  return new Promise((resolve, reject) => {
    const image = new Image()
    image.onload = () => resolve({ width: image.naturalWidth, height: image.naturalHeight })
    image.onerror = reject
    image.src = url
  })
}

async function loadPdfMeta(): Promise<void> {
  if (!decryptUrl.value) return
  const pdfjs = await import('pdfjs-dist')
  pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerURL
  const doc = await pdfjs.getDocument({ url: decryptUrl.value }).promise
  const { info } = await doc.getMetadata()
  const fields = info as Record<string, unknown>
  const items: MetaItem[] = []
  pushMeta(items, t('info.meta_doc_title'), typeof fields.Title === 'string' ? fields.Title : undefined)
  pushMeta(items, t('info.meta_author'), typeof fields.Author === 'string' ? fields.Author : undefined)
  pushMeta(items, t('info.meta_subject'), typeof fields.Subject === 'string' ? fields.Subject : undefined)
  pushMeta(items, t('info.meta_keywords'), typeof fields.Keywords === 'string' ? fields.Keywords : undefined)
  pushMeta(items, t('info.meta_pages'), doc.numPages)
  pushMeta(items, t('info.meta_created'), formatPdfDate(fields.CreationDate as string | undefined))
  pushMeta(items, t('info.meta_producer'), typeof fields.Producer === 'string' ? fields.Producer : undefined)
  pushMeta(items, t('info.meta_version'), fields.PDFFormatVersion as string | undefined)
  metaItems.value = items
}

function loadArchiveMeta(): void {
  const counts = archiveCounts.value
  if (!counts || !counts.entries) return
  const items: MetaItem[] = []
  pushMeta(items, t('info.meta_entries'), counts.entries)
  pushMeta(items, t('info.meta_files'), counts.files)
  pushMeta(items, t('info.meta_uncompressed'), formatSize(counts.uncompressed))
  metaItems.value = items
}

function loadTextMeta(): void {
  const content = textContent.value
  if (!content) return
  const items: MetaItem[] = []
  pushMeta(items, t('info.meta_lines'), content.split('\n').length + (sourceTruncated.value ? '+' : ''))
  const crlf = (content.match(/\r\n/g) || []).length
  const lineFeeds = (content.match(/\n/g) || []).length - crlf
  const loneCarriage = (content.match(/\r/g) || []).length - crlf
  pushMeta(items, t('info.meta_eol'),
    crlf && !lineFeeds && !loneCarriage ? 'CRLF'
      : lineFeeds && !crlf && !loneCarriage ? 'LF'
        : crlf || (lineFeeds && loneCarriage) ? t('info.meta_eol_mixed') : undefined)
  pushMeta(items, t('info.meta_encoding'), content.startsWith('\uFEFF')
    ? 'UTF-8 (BOM)'
    : content.includes('\uFFFD') ? t('info.meta_encoding_other') : 'UTF-8')
  metaItems.value = items
}

// "Stats for nerds": right-click the video and toggle the diagnostics
// overlay; it samples playback internals every 500ms while visible.
const statsVisible = ref(false)
const statsMenu = ref(false)
const statsMenuX = ref(0)
const statsMenuY = ref(0)
const stats = ref<PlaybackStats | null>(null)
let statsTimer: ReturnType<typeof setInterval> | null = null
const statsMenuOptions = [{ label: t('quality.stats_menu'), key: 'stats' }]

function openVideoMenu(event: MouseEvent): void {
  statsMenuX.value = event.clientX
  statsMenuY.value = event.clientY
  statsMenu.value = true
}

function toggleStats(): void {
  statsMenu.value = false
  statsVisible.value = !statsVisible.value
  if (statsVisible.value) {
    stats.value = quality.getStats()
    statsTimer = setInterval(() => {
      stats.value = quality.getStats()
    }, 500)
  } else if (statsTimer) {
    clearInterval(statsTimer)
    statsTimer = null
  }
}
const truncated = computed(() => sourceTruncated.value || viewTruncated.value)
const isHtmlFile = computed(() => /\.html?$/i.test(name.value))
const editableText = computed(() => ['text', 'markdown', 'csv'].includes(viewerType.value || ''))
const canEdit = computed(() => editableText.value && !inTrash.value)
const editorLanguage = computed(() => {
  if (viewerType.value === 'markdown') return 'markdown'
  return fs.getLanguage(name.value)
})
const editorSourceVisible = computed(() => {
  if (!editing.value) return false
  if (viewerType.value === 'text' && !isHtmlFile.value) return true
  return editorMode.value === 'source' || editorMode.value === 'split'
})
const editorPreviewVisible = computed(() => {
  if (!editing.value) return false
  if (viewerType.value === 'csv') return editorMode.value === 'table'
  if (viewerType.value === 'markdown' || isHtmlFile.value) {
    return editorMode.value === 'preview' || editorMode.value === 'split'
  }
  return false
})
const parentPath = computed(() => {
  const path = file.value?.path || (typeof route.query.path === 'string' ? route.query.path : '/')
  if (isTrashLocation(path)) return trashParentLocation(path)
  const trimmed = path.replace(/\/$/, '')
  const index = trimmed.lastIndexOf('/')
  return index <= 0 ? '/' : `${trimmed.slice(0, index)}/`
})

onMounted(async () => {
  window.addEventListener('beforeunload', preventDirtyUnload)
  const path = typeof route.query.path === 'string' ? route.query.path : ''
  if (!path) {
    error.value = t('files.preview_missing')
    loading.value = false
    return
  }

  file.value = {
    path,
    name: typeof route.query.name === 'string' ? route.query.name : path.split('/').pop() || '',
    inode: typeof route.query.inode === 'string' && Number.isSafeInteger(Number(route.query.inode))
      ? Number(route.query.inode)
      : undefined,
    is_dir: false,
    size: 0,
    created_at: '',
    last_modified: '',
  }

  try {
    const registered = await registerFileDecrypt(file.value)
    decryptUrl.value = registered.decryptUrl
    fileDEK.value = registered.access.dek
    fileGeneration.value = registered.access.generation
    file.value = {
      ...file.value,
      name: registered.access.name || file.value.name,
      inode: registered.access.inode || file.value.inode,
      size: registered.access.size,
      content_type: registered.access.content_type,
      media_codecs: registered.access.media_codecs,
      media_meta: registered.access.media_meta,
    }
    await loadRichPreview(previewKind.value, registered.access.size)
    void loadMetadata()
  } catch (reason: any) {
    console.error('Preview failed:', reason)
    error.value = t('files.preview_retry_hint')
  } finally {
    loading.value = false
    if (isVideoFile.value && decryptUrl.value && !error.value) {
      await nextTick()
      if (videoEl.value && file.value) void quality.attach(videoEl.value, file.value.path)
    }
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', preventDirtyUnload)
  qualitySelection++
  quality.detach()
  if (scrollSyncReleaseFrame) cancelAnimationFrame(scrollSyncReleaseFrame)
  unregisterFileDecrypt(decryptUrl.value)
  if (loadedFont) document.fonts.delete(loadedFont)
  if (statsTimer) {
    clearInterval(statsTimer)
    statsTimer = null
  }
  if (file.value) {
    void api.post('/audit/', {
      path: file.value.path,
      duration_ms: Date.now() - openedAt,
      type: previewKind.value,
    }).catch(() => {})
  }
})

onBeforeRouteLeave(async () => {
  if (!dirty.value) return true
  return showConfirm(t('preview.discard_title'), t('preview.discard_body'), {
    positiveText: t('preview.discard'),
  })
})

async function loadRichPreview(kind: string, size: number): Promise<void> {
  if (['text', 'markdown', 'csv', 'notebook'].includes(kind)) {
    const headers = size > TEXT_PREVIEW_LIMIT ? { Range: `bytes=0-${TEXT_PREVIEW_LIMIT - 1}` } : undefined
    const response = await fetch(decryptUrl.value, { headers })
    if (!response.ok && response.status !== 206) throw new Error(`Preview fetch failed: ${response.status}`)
    textContent.value = await response.text()
    sourceTruncated.value = size > TEXT_PREVIEW_LIMIT
    viewTruncated.value = false

    if (kind === 'markdown') {
      const [{ default: MarkdownIt }, { default: DOMPurify }] = await Promise.all([
        import('markdown-it'),
        import('dompurify'),
      ])
      const markdown = new MarkdownIt({ linkify: true, breaks: true })
      markdown.core.ruler.push('domus_source_line_anchors', (state) => {
        for (const token of state.tokens) {
          if (!token.block || !token.map || token.nesting === -1 || token.type === 'inline') continue
          token.attrSet('data-source-line', String(token.map[0] + 1))
        }
      })
      markdownRenderer = source => DOMPurify.sanitize(markdown.render(source))
    } else if (kind === 'csv') {
      const { default: Papa } = await import('papaparse')
      csvParser = source => {
        const parsed = Papa.parse(source, { skipEmptyLines: true })
        return Array.isArray(parsed.data)
          ? parsed.data.filter(Array.isArray).map(row => row.map(value => String(value)))
          : []
      }
    }
    refreshDerivedPreview()
    return
  }

  if (kind === 'archive' && size <= ARCHIVE_PREVIEW_LIMIT) {
    const [{ default: JSZip }, response] = await Promise.all([
      import('jszip'),
      fetch(decryptUrl.value),
    ])
    const zip = await JSZip.loadAsync(await response.arrayBuffer())
    const allEntries = Object.values(zip.files)
    let uncompressed = 0
    let fileCount = 0
    for (const entry of allEntries) {
      if (entry.dir) continue
      const data = (entry as unknown as { _data?: { uncompressedSize?: number } })._data
      uncompressed += data?.uncompressedSize || 0
      fileCount++
    }
    archiveCounts.value = { entries: allEntries.length, files: fileCount, uncompressed }
    archiveEntries.value = allEntries.slice(0, 500).map(entry => ({
      name: entry.name,
      dir: entry.dir,
    }))
    viewTruncated.value = allEntries.length > 500
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

function refreshDerivedPreview(): void {
  viewTruncated.value = false
  if (viewerType.value === 'markdown') {
    renderedHTML.value = markdownRenderer?.(textContent.value) || ''
    return
  }
  if (viewerType.value === 'csv') {
    const rows = csvParser?.(textContent.value) || []
    csvRows.value = rows.slice(0, 200)
    viewTruncated.value = rows.length > 200
    return
  }
  if (viewerType.value === 'notebook') {
    const notebook = JSON.parse(textContent.value) as { cells?: Array<{ cell_type?: string; source?: string | string[] }> }
    const cells = notebook.cells || []
    notebookCells.value = cells.slice(0, 200).map(cell => ({
      type: cell.cell_type || 'raw',
      source: Array.isArray(cell.source) ? cell.source.join('') : cell.source || '',
    }))
    viewTruncated.value = cells.length > 200
  }
}

function preventDirtyUnload(event: BeforeUnloadEvent): void {
  if (!dirty.value) return
  event.preventDefault()
  event.returnValue = ''
}

function updateEditorContent(content: string): void {
  textContent.value = content
  dirty.value = content !== originalContent.value
  refreshDerivedPreview()
  void nextTick(() => {
    if (lastEditorScroll) syncPreviewToEditor(lastEditorScroll)
  })
}

function markdownScrollAnchors(totalLines: number): MarkdownScrollAnchor[] {
  const preview = markdownPreview.value
  if (!preview) return []

  const maxScroll = Math.max(0, preview.scrollHeight - preview.clientHeight)
  const previewTop = preview.getBoundingClientRect().top
  const byLine = new Map<number, number>([[1, 0]])
  for (const element of preview.querySelectorAll<HTMLElement>('[data-source-line]')) {
    const sourceLine = Number(element.dataset.sourceLine)
    if (!Number.isSafeInteger(sourceLine) || sourceLine < 1 || sourceLine > totalLines) continue
    const top = Math.max(0, Math.min(
      maxScroll,
      element.getBoundingClientRect().top - previewTop + preview.scrollTop,
    ))
    const previous = byLine.get(sourceLine)
    if (previous === undefined || top < previous) byLine.set(sourceLine, top)
  }
  if (totalLines > 1) byLine.set(totalLines, maxScroll)

  let previousTop = 0
  return [...byLine.entries()]
    .sort(([lineA], [lineB]) => lineA - lineB)
    .map(([line, top]) => {
      previousTop = Math.max(previousTop, top)
      return { line, top: previousTop }
    })
}

function previewTopForLine(line: number, anchors: MarkdownScrollAnchor[]): number {
  if (!anchors.length || line <= anchors[0].line) return anchors[0]?.top || 0
  for (let index = 1; index < anchors.length; index += 1) {
    const previous = anchors[index - 1]
    const next = anchors[index]
    if (line > next.line) continue
    const span = next.line - previous.line
    if (span <= 0) return next.top
    return previous.top + ((line - previous.line) / span) * (next.top - previous.top)
  }
  return anchors[anchors.length - 1]?.top || 0
}

function sourceLineForPreviewTop(top: number, anchors: MarkdownScrollAnchor[]): number {
  if (!anchors.length || top <= anchors[0].top) return anchors[0]?.line || 1
  let previous = anchors[0]
  for (let index = 1; index < anchors.length; index += 1) {
    const next = anchors[index]
    if (next.top <= previous.top) continue
    if (top <= next.top) {
      return Math.round(previous.line + ((top - previous.top) / (next.top - previous.top)) * (next.line - previous.line))
    }
    previous = next
  }
  return anchors[anchors.length - 1]?.line || 1
}

function holdScrollSync(origin: 'source' | 'preview'): void {
  scrollSyncOrigin = origin
  if (scrollSyncReleaseFrame) cancelAnimationFrame(scrollSyncReleaseFrame)
  scrollSyncReleaseFrame = requestAnimationFrame(() => {
    scrollSyncReleaseFrame = requestAnimationFrame(() => {
      scrollSyncOrigin = null
      scrollSyncReleaseFrame = 0
    })
  })
}

function syncPreviewToEditor(position: EditorScrollPosition): void {
  const preview = markdownPreview.value
  if (!preview || viewerType.value !== 'markdown' || editorMode.value !== 'split') return

  const maxScroll = Math.max(0, preview.scrollHeight - preview.clientHeight)
  const atEnd = position.maxScroll > 0 && position.scrollTop >= position.maxScroll - 1
  const target = position.totalLines <= 1 && position.maxScroll > 0
    ? (position.scrollTop / position.maxScroll) * maxScroll
    : atEnd
      ? maxScroll
      : previewTopForLine(position.line, markdownScrollAnchors(position.totalLines))
  if (Math.abs(preview.scrollTop - target) < 1) return
  holdScrollSync('source')
  preview.scrollTop = target
}

function handleEditorScroll(position: EditorScrollPosition): void {
  lastEditorScroll = position
  if (scrollSyncOrigin === 'preview') return
  syncPreviewToEditor(position)
}

function handleMarkdownPreviewScroll(): void {
  const preview = markdownPreview.value
  if (!preview || !editorPane.value || editorMode.value !== 'split' || scrollSyncOrigin === 'source') return

  const totalLines = lastEditorScroll?.totalLines || textContent.value.split('\n').length
  const maxScroll = Math.max(0, preview.scrollHeight - preview.clientHeight)
  if (maxScroll <= 0) return
  if (totalLines <= 1) {
    holdScrollSync('preview')
    editorPane.value.scrollToProgress(preview.scrollTop / maxScroll)
    return
  }
  const line = preview.scrollTop >= maxScroll - 1
    ? totalLines
    : sourceLineForPreviewTop(preview.scrollTop, markdownScrollAnchors(totalLines))
  holdScrollSync('preview')
  editorPane.value.scrollToLine(line)
}

watch(editorMode, (mode) => {
  if (mode !== 'split') return
  void nextTick(() => {
    if (lastEditorScroll) syncPreviewToEditor(lastEditorScroll)
  })
})

async function startEdit(): Promise<void> {
  if (!canEdit.value || preparingEdit.value) return
  preparingEdit.value = true
  try {
    if (sourceTruncated.value) {
      const confirmed = await showConfirm(
        t('preview.edit_large_title'),
        t('preview.edit_large_body', { size: formatSize(file.value?.size || 0) }),
        { positiveText: t('preview.load_and_edit') },
      )
      if (!confirmed) return
      const response = await fetch(decryptUrl.value)
      if (!response.ok) throw new Error(`Full text fetch failed: ${response.status}`)
      textContent.value = await response.text()
      sourceTruncated.value = false
      refreshDerivedPreview()
    }

    originalContent.value = textContent.value
    dirty.value = false
    if (viewerType.value === 'markdown' || isHtmlFile.value) {
      editorMode.value = isMobile.value ? 'source' : 'split'
    } else {
      editorMode.value = 'source'
    }
    editing.value = true
  } catch (reason: any) {
    console.error('Preparing editor failed:', reason)
    message.error(te(reason, 'preview.edit_failed'))
  } finally {
    preparingEdit.value = false
  }
}

function cancelEdit(): void {
  textContent.value = originalContent.value
  refreshDerivedPreview()
  dirty.value = false
  editing.value = false
}

async function saveEdit(): Promise<void> {
  if (!file.value || !editing.value || saving.value || !dirty.value) return
  saving.value = true
  try {
    const bytes = new TextEncoder().encode(textContent.value)
    const generation = await writeEncryptedFile(
      file.value.path,
      bytes.slice().buffer,
      file.value.content_type || 'text/plain',
      {
        dek: fileDEK.value,
        expectedGeneration: fileGeneration.value,
      },
    )
    if (generation) fileGeneration.value = generation
    file.value.size = bytes.byteLength
    originalContent.value = textContent.value
    dirty.value = false
    editing.value = false
    fs.invalidateCache()
    message.success(t('preview.saved'))
  } catch (reason: any) {
    console.error('Saving file failed:', reason)
    message.error(te(reason, 'preview.save_failed'))
  } finally {
    saving.value = false
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
  if (isTrashLocation(parentPath.value)) {
    void router.replace({ path: '/files', query: trashLocationRouteQuery(parentPath.value) })
    return
  }
  void router.replace({ path: '/files', query: parentPath.value === '/' ? {} : { path: parentPath.value } })
}

async function download(): Promise<void> {
  if (file.value) await fs.downloadFile(file.value.path)
}

async function remove(): Promise<void> {
  if (!file.value?.inode) return
  const ok = await showConfirm(
    inTrash.value ? t('dialog.permanent_delete_title') : t('dialog.delete_title'),
    inTrash.value ? t('dialog.confirm_permanent_delete', { n: 1 }) : t('dialog.confirm_delete', { n: 1 }), {
    icon: 'warning',
    positiveType: 'error',
  })
  if (!ok) return
  const trash = parseTrashLocation(file.value.path)
  if (trash?.id) {
    const response = await api.delete<{ entry_removed?: boolean }>(`/trash/${encodeURIComponent(trash.id)}`, {
      params: { path: trash.relativePath, expected_inode: file.value.inode },
    })
    if (response.data.entry_removed) {
      void router.replace({ path: '/files', query: { place: 'trash' } })
      return
    }
  } else {
    await api.post('/trash/', { path: file.value.path, expected_inode: file.value.inode })
  }
  goBack()
}

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / (1024 ** index)
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`
}

async function onQualitySelect(key: string | number): Promise<void> {
  const previousSelection = qualitySelection
  const profile = String(key)
  const rendition = quality.renditions.value.find(item => item.profile === profile)
  const playable = !!rendition && (rendition.status === 'ready' || (rendition.status === 'running' && rendition.init))
  // 原画 always plays the raw source directly — its background remux never
  // prompts. Other profiles without a usable rendition need a transcode.
  if (profile !== 'original' && !playable) {
    const confirmed = await showConfirm(
      t('quality.transcode_title'),
      t('quality.transcode_body', { profile: qualityLabelOf(profile) }),
      { positiveText: t('quality.transcode_confirm') },
    )
    if (!confirmed || previousSelection !== qualitySelection) return
  }
  const selection = ++qualitySelection
  switchingQuality.value = profile !== 'original' && !playable
  try {
    await quality.selectQuality(profile)
  } catch {
    if (selection === qualitySelection) message.error(t('quality.failed'))
  } finally {
    if (selection === qualitySelection) switchingQuality.value = false
  }
}
</script>

<template>
  <main class="preview-shell" :class="{ 'preview-shell--bar': isMobile && !editing }">
    <header class="preview-header">
      <NButton circle class="back-button" :aria-label="t('files.back_to_files')" @click="goBack">
        <template #icon><IconArrowLeft /></template>
      </NButton>
      <div class="preview-title">
        <strong>{{ name }}</strong>
        <span>
          {{ file ? formatSize(file.size) : t('files.preview') }}
          <template v-if="dirty"> · {{ t('preview.unsaved') }}</template>
        </span>
      </div>
      <div class="preview-actions">
        <template v-if="editing">
          <NButton :disabled="saving" @click="cancelEdit">
            <template #icon><IconClose /></template><span>{{ t('preview.cancel') }}</span>
          </NButton>
          <NButton
            type="primary"
            :loading="saving"
            :disabled="!dirty"
            data-testid="preview-save"
            @click="saveEdit"
          >
            <template #icon><IconContentSave /></template><span>{{ t('preview.save') }}</span>
          </NButton>
        </template>
        <template v-else-if="isMobile">
          <NDropdown
            v-if="isVideoFile && !inTrash"
            trigger="click"
            placement="bottom-end"
            :options="qualityOptions"
            data-testid="quality-dropdown"
            @select="onQualitySelect"
            @update:show="show => show && void quality.refresh()"
          >
            <NButton class="quality-menu" data-testid="quality-menu">
              <template #icon><IconTune /></template><span>{{ qualityLabel }}</span>
            </NButton>
          </NDropdown>
        </template>
        <template v-else>
          <NDropdown
            v-if="isVideoFile && !inTrash"
            trigger="click"
            placement="bottom-end"
            :options="qualityOptions"
            data-testid="quality-dropdown"
            @select="onQualitySelect"
            @update:show="show => show && void quality.refresh()"
          >
            <NButton data-testid="quality-menu">
              <template #icon><IconTune /></template><span>{{ qualityLabel }}</span>
            </NButton>
          </NDropdown>
          <NButton v-if="canEdit" :loading="preparingEdit" data-testid="preview-edit" @click="startEdit">
            <template #icon><IconPencilOutline /></template><span>{{ t('preview.edit') }}</span>
          </NButton>
          <NButton @click="showDetails = true">
            <template #icon><IconInformationOutline /></template><span>{{ t('menu.details') }}</span>
          </NButton>
          <NButton @click="download">
            <template #icon><IconDownload /></template><span>{{ t('menu.download') }}</span>
          </NButton>
          <NButton type="error" secondary class="danger" :disabled="!file?.inode" @click="remove">
            <template #icon><IconDeleteOutline /></template><span>{{ inTrash ? t('menu.permanent_delete') : t('menu.delete') }}</span>
          </NButton>
        </template>
      </div>
    </header>

    <section
      class="preview-canvas"
      :class="[`preview-canvas--${previewKind}`, { 'preview-canvas--editing': editing }]"
    >
      <div v-if="loading" class="preview-state">
        <NSpin size="large" />
        <strong>{{ t('files.decrypting_preview') }}</strong>
      </div>

      <div v-else-if="error" class="preview-state preview-state--error">
        <NResult status="error" :title="t('files.preview_failed')" :description="error">
          <template #footer>
            <NButton @click="download"><template #icon><IconDownload /></template>{{ t('menu.download') }}</NButton>
          </template>
        </NResult>
      </div>

      <template v-else>
        <div v-if="editing" class="edit-workspace">
          <div v-if="viewerType === 'markdown' || viewerType === 'csv' || isHtmlFile" class="edit-toolbar">
            <NButtonGroup size="small">
              <NButton
                :type="editorMode === 'source' ? 'primary' : 'default'"
                :secondary="editorMode === 'source'"
                @click="editorMode = 'source'"
              >{{ t('preview.code') }}</NButton>
              <NButton
                v-if="viewerType === 'csv'"
                :type="editorMode === 'table' ? 'primary' : 'default'"
                :secondary="editorMode === 'table'"
                @click="editorMode = 'table'"
              >{{ t('preview.table') }}</NButton>
              <NButton
                v-else
                :type="editorMode === 'preview' ? 'primary' : 'default'"
                :secondary="editorMode === 'preview'"
                @click="editorMode = 'preview'"
              >{{ t('preview.rendered') }}</NButton>
              <NButton
                v-if="viewerType !== 'csv' && !isMobile"
                :type="editorMode === 'split' ? 'primary' : 'default'"
                :secondary="editorMode === 'split'"
                @click="editorMode = 'split'"
              >{{ t('preview.split') }}</NButton>
            </NButtonGroup>
            <span class="edit-shortcut">{{ t('preview.save_shortcut') }}</span>
          </div>
          <div class="edit-panes" :class="{ 'edit-panes--split': editorMode === 'split' }">
            <TextEditorPane
              v-if="editorSourceVisible"
              ref="editorPane"
              class="edit-source"
              :model-value="textContent"
              :language="editorLanguage"
              @update:model-value="updateEditorContent"
              @scroll-position="handleEditorScroll"
              @save="saveEdit"
            />
            <article
              v-if="editorPreviewVisible && viewerType === 'markdown'"
              ref="markdownPreview"
              class="edit-render markdown-body"
              data-testid="markdown-editor-preview"
              @scroll="handleMarkdownPreviewScroll"
              v-html="renderedHTML"
            />
            <div v-else-if="editorPreviewVisible && viewerType === 'csv'" class="edit-table table-preview">
              <table>
                <tr v-for="(row, rowIndex) in csvRows" :key="rowIndex">
                  <component
                    :is="rowIndex === 0 ? 'th' : 'td'"
                    v-for="(cell, cellIndex) in row"
                    :key="cellIndex"
                  >{{ cell }}</component>
                </tr>
              </table>
            </div>
            <iframe
              v-else-if="editorPreviewVisible && isHtmlFile"
              class="edit-html-preview"
              :srcdoc="textContent"
              sandbox=""
              :title="t('preview.rendered')"
            />
          </div>
        </div>
        <img v-else-if="viewerType === 'image'" :src="decryptUrl" class="preview-image" :alt="name" />
        <div v-else-if="isVideoFile" class="video-shell" @contextmenu.prevent="openVideoMenu">
          <video ref="videoEl" class="preview-media" controls playsinline preload="metadata" />
          <div v-if="switchingQuality" class="video-loading">
            <NSpin size="large" />
            <strong>{{ t('quality.preparing', { profile: qualityLabelOf(quality.activeQuality.value) }) }}</strong>
          </div>
          <NDropdown
            trigger="manual"
            :show="statsMenu"
            :x="statsMenuX"
            :y="statsMenuY"
            placement="bottom-start"
            :options="statsMenuOptions"
            @select="toggleStats"
            @clickoutside="statsMenu = false"
          />
        </div>
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

        <div v-if="isVideoFile && statsVisible && stats" class="video-stats">
          <div>{{ t('quality.stats_quality') }}: {{ qualityLabelOf(stats.quality) }}<template v-if="stats.segmented && stats.renditionStatus && stats.renditionStatus !== 'ready'"> · {{ stats.renditionStatus }}</template></div>
          <div v-if="stats.videoWidth">{{ t('quality.stats_resolution') }}: {{ stats.videoWidth }}×{{ stats.videoHeight }}</div>
          <div v-if="stats.codecs">codecs: {{ stats.codecs }}</div>
          <div>{{ t('quality.stats_buffer') }}: +{{ stats.bufferedAhead.toFixed(1) }}s / −{{ stats.bufferedBehind.toFixed(1) }}s · {{ stats.bufferedRanges }}</div>
          <div v-if="stats.segmented">{{ t('quality.stats_segments') }}: {{ stats.appendedSegments }}/{{ stats.totalSegments }}<template v-if="stats.renditionStatus === 'running' && stats.renditionProgress"> · {{ Math.round(stats.renditionProgress * 100) }}%</template></div>
          <div v-if="stats.droppedFrames !== undefined">{{ t('quality.stats_dropped') }}: {{ stats.droppedFrames }} / {{ stats.totalFrames }}</div>
        </div>

        <NAlert v-if="truncated" class="truncated-notice" type="warning" :show-icon="false">
          {{ t('files.preview_truncated') }}
        </NAlert>
      </template>
    </section>

    <Transition name="select-bar">
      <nav v-if="isMobile && !editing" class="preview-mobile-bar">
        <NButton v-if="canEdit" quaternary @click="startEdit">
          <IconPencilOutline /><span>{{ t('preview.edit') }}</span>
        </NButton>
        <NButton quaternary @click="showDetails = true">
          <IconInformationOutline /><span>{{ t('menu.details') }}</span>
        </NButton>
        <NButton quaternary @click="download">
          <IconDownload /><span>{{ t('menu.download') }}</span>
        </NButton>
        <NButton quaternary type="error" class="mobile-delete" :disabled="!file?.inode" @click="remove">
          <IconDeleteOutline /><span>{{ inTrash ? t('menu.permanent_delete') : t('menu.delete') }}</span>
        </NButton>
      </nav>
    </Transition>

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
        <template v-if="metaItems.length">
          <div class="exif-heading">{{ t('info.meta_title') }}</div>
          <NDescriptions :column="1" label-placement="top" bordered size="small">
            <NDescriptionsItem v-for="item in metaItems" :key="item.label" :label="item.label">
              <a v-if="item.href" :href="item.href" target="_blank" rel="noopener">{{ item.value }}</a>
              <template v-else>{{ item.value }}</template>
            </NDescriptionsItem>
          </NDescriptions>
        </template>
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
  --canvas: #eef1f6;
  --surface: #ffffff;
  --header-bg: rgba(255, 255, 255, .92);
  min-height: 100dvh;
  color: var(--ink);
  background: var(--canvas);
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
  border-bottom: 1px solid var(--line);
  background: var(--header-bg);
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
.preview-canvas--editing { display: block; overflow: hidden; padding: 16px; }
.preview-canvas--image, .preview-canvas--video { background: #151a24; }
.preview-image, .preview-media { display: block; max-width: 100%; max-height: calc(100dvh - 128px); object-fit: contain; box-shadow: 0 24px 70px rgb(0 0 0 / 24%); }
.preview-media { width: auto; height: auto; }
.video-shell {
  position: relative;
  display: grid;
  place-items: center;
  width: 100%;
}
.video-shell .preview-media { grid-area: 1 / 1; }
.video-loading {
  grid-area: 1 / 1;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 18px;
  border-radius: 999px;
  color: #fff;
  background: rgb(21 26 36 / 78%);
  font-size: 13px;
}
.video-stats {
  position: fixed;
  top: 84px;
  left: 12px;
  right: 12px;
  z-index: 30;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 22px;
  padding: 10px 16px;
  border-radius: 10px;
  color: #eee;
  background: rgb(21 26 36 / 82%);
  font-family: ui-monospace, monospace;
  font-size: 11px;
  line-height: 1.8;
  user-select: text;
}
.preview-pdf { width: min(1120px, 100%); height: calc(100dvh - 128px); border: 0; border-radius: 12px; background: var(--domus-surface); box-shadow: 0 18px 58px rgb(30 38 58 / 15%); }
.preview-state { display: flex; min-height: 280px; align-items: center; justify-content: center; flex-direction: column; gap: 9px; color: var(--muted); text-align: center; }
.preview-state > svg { width: 38px; height: 38px; color: var(--muted); }
.preview-state strong { color: var(--ink); font-size: 16px; }
.preview-state span { max-width: 390px; font-size: 13px; line-height: 1.55; }

.edit-workspace {
  display: flex;
  width: min(1480px, 100%);
  height: 100%;
  margin: 0 auto;
  overflow: hidden;
  flex-direction: column;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--domus-surface);
  box-shadow: var(--domus-shadow-md);
}
.edit-toolbar {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 7px 10px;
  border-bottom: 1px solid var(--line);
  background: var(--domus-surface-2);
}
.edit-shortcut { color: var(--muted); font-size: 11px; }
.edit-panes {
  display: grid;
  min-height: 0;
  flex: 1;
  grid-template-columns: minmax(0, 1fr);
}
.edit-panes--split { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.edit-source,
.edit-render,
.edit-table,
.edit-html-preview {
  min-width: 0;
  min-height: 0;
  overflow: auto;
  border: 0;
  background: var(--domus-surface);
  user-select: text;
}
.edit-render { padding: clamp(24px, 4vw, 58px); font-size: 14px; line-height: 1.78; }
.edit-table { width: 100%; border-radius: 0; box-shadow: none; }
.edit-html-preview { width: 100%; height: 100%; border-left: 1px solid var(--line); }
.edit-panes--split .edit-render { border-left: 1px solid var(--line); }

.document-preview, .text-preview, .table-preview, .notebook-preview, .archive-preview, .font-preview {
  width: min(1050px, 100%);
  min-height: calc(100dvh - 136px);
  margin: auto;
  border-radius: 16px;
  color: var(--ink);
  background: var(--domus-surface);
  box-shadow: var(--domus-shadow-md);
  user-select: text;
}
.document-preview { width: min(860px, 100%); padding: clamp(28px, 5vw, 72px); font-size: 15px; line-height: 1.78; }
.markdown-body :deep(h1), .markdown-body :deep(h2), .markdown-body :deep(h3) { margin: 1.2em 0 .5em; line-height: 1.2; }
.markdown-body :deep(h1:first-child), .markdown-body :deep(h2:first-child) { margin-top: 0; }
.markdown-body :deep(pre) { overflow: auto; padding: 14px; border-radius: 8px; background: var(--domus-surface-2); }
.markdown-body :deep(code) { padding: .12em .3em; border-radius: 4px; background: var(--domus-surface-2); }
.markdown-body :deep(img) { max-width: 100%; }
.markdown-body :deep(table) { width: 100%; border-collapse: collapse; }
.markdown-body :deep(th), .markdown-body :deep(td) { padding: 8px 10px; border: 1px solid var(--line); text-align: left; }
.text-preview { padding: 26px; overflow: auto; font-family: "SFMono-Regular", Consolas, monospace; font-size: 13px; line-height: 1.68; white-space: pre-wrap; word-break: break-word; }
.table-preview { overflow: auto; }
.table-preview table { width: 100%; min-width: 640px; border-collapse: collapse; font-size: 12px; }
.table-preview th, .table-preview td { padding: 12px 14px; border-right: 1px solid var(--line); border-bottom: 1px solid var(--line); text-align: left; }
.table-preview th { position: sticky; top: 0; z-index: 2; background: var(--domus-surface-2); font-weight: 750; }
.notebook-preview { display: flex; flex-direction: column; gap: 8px; padding: 22px; }
.notebook-preview article { position: relative; padding: 14px; border-left: 3px solid #7a8ae9; border-radius: 8px; background: var(--domus-surface-2); }
.notebook-preview article.cell-markdown { border-left-color: #54a880; background: var(--domus-surface-2); }
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
.audio-card { display: flex; width: min(500px, 100%); align-items: center; flex-direction: column; gap: 20px; padding: 36px; border-radius: 22px; background: var(--domus-surface); box-shadow: 0 20px 58px rgb(30 38 58 / 14%); }
.audio-art { display: grid; width: 170px; height: 170px; place-items: center; border-radius: 22px; color: #fff; background: linear-gradient(145deg, #697af0, #3d4dc0); font-size: 64px; }
.audio-card strong { max-width: 100%; overflow: hidden; font-size: 16px; text-overflow: ellipsis; white-space: nowrap; }
.audio-card audio { width: 100%; }
.truncated-notice { position: fixed; left: 50%; bottom: 16px; z-index: 10; width: max-content; max-width: calc(100vw - 32px); transform: translateX(-50%); }

.path-value { font-family: ui-monospace, monospace; }
.folder-button svg { width: 16px; height: 16px; }

.exif-heading {
  margin: 18px 0 10px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 800;
  letter-spacing: .08em;
  text-transform: uppercase;
}
.exif-heading + .n-descriptions { margin-bottom: 4px; }

@media (max-width: 680px) {
  .preview-header { height: 64px; padding: max(8px, env(safe-area-inset-top)) 12px 8px; }
  .preview-actions button { width: 42px; height: 42px; padding: 0; }
  .preview-actions button span { display: none; }
  /* Quality keeps its label in the header so the current quality is visible. */
  .preview-actions .quality-menu { width: auto; min-width: 42px; padding: 0 10px; }
  .preview-actions .quality-menu span { display: inline; }
  .preview-actions .quality-menu :deep(.n-button__icon) { margin-right: 4px; }
  .preview-canvas { inset: 64px 0 0; padding: 10px; }
  .preview-canvas--editing { padding: 8px; }
  .preview-image, .preview-media { max-height: calc(100dvh - 84px); }
  .preview-pdf { height: calc(100dvh - 84px); border-radius: 6px; }
  /* Keep media above the fixed bottom bar. */
  .preview-shell--bar .preview-image, .preview-shell--bar .preview-media { max-height: calc(100dvh - 148px); }
  .preview-shell--bar .preview-pdf { height: calc(100dvh - 148px); }
  .preview-shell--bar .document-preview, .preview-shell--bar .text-preview,
  .preview-shell--bar .table-preview, .preview-shell--bar .notebook-preview,
  .preview-shell--bar .archive-preview, .preview-shell--bar .font-preview {
    min-height: calc(100dvh - 148px);
    border-radius: 10px;
  }
  .preview-mobile-bar {
    --bar-bg: rgba(255, 255, 255, .94);
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 40;
    display: flex;
    height: calc(64px + env(safe-area-inset-bottom));
    padding-bottom: env(safe-area-inset-bottom);
    align-items: center;
    border: 0;
    border-top: 1px solid var(--nav-border, rgba(224, 227, 236, .9));
    border-radius: 18px 18px 0 0;
    background: var(--nav-bg, var(--bar-bg));
    backdrop-filter: blur(20px);
  }
  .preview-mobile-bar button {
    display: flex;
    min-width: 0;
    height: 100%;
    flex: 1;
    align-items: center;
    justify-content: center;
    border: 0;
    padding: 0 3px;
    color: var(--ink-2);
    background: transparent;
    font-size: 10px;
    font-weight: 700;
    touch-action: manipulation;
  }
  .preview-mobile-bar button :deep(.n-button__content) { flex-direction: column; gap: 4px; }
  .preview-mobile-bar button svg { width: 21px; height: 21px; }
  .preview-mobile-bar .mobile-delete { color: var(--domus-danger); }
  /* Touch keeps :hover applied after a tap, so suppress sticky highlights. */
  @media (hover: none) {
    .preview-mobile-bar :deep(.n-button:not(.n-button--disabled):hover),
    .preview-mobile-bar :deep(.n-button:not(.n-button--disabled):focus) {
      background-color: transparent;
    }
    .preview-mobile-bar :deep(.n-button:not(.n-button--disabled):hover .n-button__state-border),
    .preview-mobile-bar :deep(.n-button:not(.n-button--disabled):focus .n-button__state-border) {
      border-color: transparent;
    }
  }
  .select-bar-enter-active, .select-bar-leave-active { transition: transform .24s cubic-bezier(.2,.8,.3,1), opacity .24s ease; }
  .select-bar-enter-from, .select-bar-leave-to { transform: translateY(100%); opacity: 0; }
  .preview-actions button { width: 42px; height: 42px; padding: 0; }
  .preview-actions button span { display: none; }
  .preview-actions button :deep(.n-button__icon) { margin-right: 0; }
  .preview-actions button.danger { display: none; }
  .preview-canvas { inset: 64px 0 0; padding: 10px; }
  .preview-canvas--editing { padding: 8px; }
  .preview-image, .preview-media { max-height: calc(100dvh - 84px); }
  .preview-pdf { height: calc(100dvh - 84px); border-radius: 6px; }
  .document-preview, .text-preview, .table-preview, .notebook-preview, .archive-preview, .font-preview { min-height: calc(100dvh - 84px); border-radius: 10px; }
  .document-preview { padding: 22px 18px; font-size: 13px; }
  .text-preview { padding: 15px; font-size: 11px; }
  .notebook-preview { padding: 12px; }
  .audio-card { padding: 22px 16px; }
  .audio-art { width: 140px; height: 140px; }
  .edit-workspace { border-radius: 10px; }
  .edit-toolbar { min-height: 46px; padding: 6px 8px; }
  .edit-shortcut { display: none; }
  .edit-render { padding: 20px 16px; font-size: 13px; }
}
</style>
