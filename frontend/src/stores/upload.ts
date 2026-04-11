import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useI18n } from '../composables/useI18n'
import { showConfirm, showDuplicateDialog } from '../composables/useNativeDialog'
import { useFileSystemStore } from './fileSystem'
import { useJobsStore } from './jobs'
import { usePreferences } from '../composables/usePreferences'
import {
  generateDEK,
  generateThumbnail,
  extractSearchText,
  encryptBlob,
} from '../composables/useCryptoUpload'
import type {
  Upload,
  UploadPart,
  ConflictInfo,
  TaskUpdateEvent,
  DuplicateDecision,
} from '../types'

// Presigned URL batch size — request this many at a time from the server
const PRESIGN_BATCH = 100

export const useUploadStore = defineStore('upload', () => {
  const { t } = useI18n()
  const ws = useWebSocket()
  const uploads: Ref<Upload[]> = ref([])
  const showPanel: Ref<boolean> = ref(false)

  const activeUploads: ComputedRef<Upload[]> = computed(() => uploads.value.filter(u => u.status === 'uploading' || u.status === 'paused'))
  const hasActive: ComputedRef<boolean> = computed(() => activeUploads.value.length > 0)

  // Active encrypt workers keyed by upload id
  const workers = new Map<string, Worker>()

  // Listen for task updates — remove local upload entries once server confirms completion
  ws.on('task.update', (data: TaskUpdateEvent) => {
    const entry = uploads.value.find(u => u.taskId === data.task_id)
    if (!entry) return
    if (data.status === 'completed' || data.status === 'failed') {
      uploads.value = uploads.value.filter(u => u.taskId !== data.task_id)
      if (uploads.value.length === 0) showPanel.value = false
    }
  })

  // ── Conflict check + batch upload ────────────────────────────────────────

  async function uploadFiles(fileList: FileList, targetPath: string): Promise<void> {
    const allFiles: File[] = Array.from(fileList)
    if (allFiles.length === 0) return

    const names: string[] = allFiles.map(f => f.name)
    let conflictMap: Map<string, ConflictInfo> = new Map()
    try {
      const res = await api.post<{ conflicts?: ConflictInfo[] }>('/file/upload', { path: targetPath, names })
      for (const c of res.data.conflicts || []) {
        conflictMap.set(c.name, c)
      }
    } catch { /* proceed without conflict detection */ }

    const files: File[] = []
    const strategies: Map<string, string> = new Map()
    let duplicateDecision: DuplicateDecision | null = null

    for (const file of allFiles) {
      if (conflictMap.has(file.name)) {
        let action: DuplicateDecision['action'] | undefined = duplicateDecision?.action
        if (!action) {
          const existing = conflictMap.get(file.name)!
          const decision = await showDuplicateDialog({
            title: t('upload.duplicate_title'),
            incomingName: file.name,
            incomingSize: file.size,
            existingName: existing.name,
            existingSize: existing.size || 0,
            existingIsDir: existing.is_dir || false,
          })
          if (!decision) return
          action = decision.action
          if (decision.applyToAll) duplicateDecision = { action }
        }
        if (action === 'skip') continue
        strategies.set(file.name, action)
      }
      files.push(file)
    }

    if (files.length === 0) return

    const zeroByteFiles: File[] = files.filter(f => f.size === 0)
    if (zeroByteFiles.length > 0) {
      const fileListText: string = zeroByteFiles.map(f => `- ${f.name}`).join('\n')
      const shouldContinue = await showConfirm(
        t('upload.zero_byte_title'),
        t('upload.zero_byte_confirm', { files: fileListText }),
      )
      if (!shouldContinue) return
    }

    for (const file of files) {
      const strategy = strategies.get(file.name) || null
      startUpload(file, targetPath, zeroByteFiles.length > 0, strategy)
    }
  }

  // ── Main upload flow ─────────────────────────────────────────────────────

  interface InitResponse {
    upload_id: string
    task_id: string
    oss_key: string
    file_name: string
    oss_upload_id: string
    part_size: number
    total_parts: number
  }

  interface PresignResponse {
    parts: { part_number: number; presigned_url: string }[]
    expires_in: number
  }

  async function startUpload(file: File, targetPath: string, allowZeroByte = false, conflictStrategy: string | null = null): Promise<void> {
    const entry: Upload = {
      id: crypto?.randomUUID?.() || (Math.random().toString(36).slice(2) + Date.now().toString(36)),
      fileName: file.name,
      fileSize: file.size,
      progress: 0,
      speed: 0,
      status: 'uploading',
      uploadId: null,
      taskId: null,
      targetPath,
      completedParts: [],
      startTime: Date.now(),
      startProgress: 0,
      bytesUploaded: 0,
      _resume: null,
      _file: file,
    }
    uploads.value.push(entry)
    showPanel.value = true
    try { useJobsStore().panelOpen = true } catch { /* ignore */ }
    const upload = uploads.value[uploads.value.length - 1]

    try {
      if (!file.size && !allowZeroByte) {
        upload.status = 'failed'
        upload.error = t('upload.zero_byte_error')
        return
      }

      // 1. Init — create multipart upload on server
      const initPayload: Record<string, unknown> = {
        path: targetPath,
        file_name: file.name,
        file_size: file.size,
        content_type: file.type || '',
      }
      if (conflictStrategy) initPayload.conflict_strategy = conflictStrategy

      const initRes = await api.post<InitResponse>('/file/upload', initPayload)
      const init = initRes.data

      if (init.file_name && init.file_name !== file.name) {
        upload.fileName = init.file_name
      }
      upload.uploadId = init.upload_id
      upload.taskId = init.task_id || null

      // 2. Thumbnail + text extraction (fast — only reads a small portion)
      const { prefs } = usePreferences()
      const [thumbnail, searchText] = await Promise.all([
        generateThumbnail(file),
        prefs.indexContent ? extractSearchText(file) : Promise.resolve(null),
      ])

      if ((upload.status as string) === 'cancelled') return

      // 3. Generate DEK
      const dek = await generateDEK()

      // 4. Get first batch of presigned URLs
      const presignRes = await api.get<PresignResponse>('/file/upload/presign', {
        params: { upload_id: init.upload_id, start: 1, count: PRESIGN_BATCH },
      })
      const partUrls = presignRes.data.parts.map(p => p.presigned_url)

      if ((upload.status as string) === 'cancelled') return

      // 5. Encrypt + hash + upload (all in worker, main thread stays free)
      const workerResult = await runEncryptWorker(upload, file, dek.raw, partUrls, init.part_size, init.upload_id)

      if (upload.status === 'cancelled' || !workerResult) return

      // 6. Upload thumbnail as a separate encrypted file
      let thumbnailUploadId: string | null = null
      if (thumbnail) {
        thumbnailUploadId = await uploadThumbnail(upload, thumbnail, dek, targetPath)
      }

      // 7. Complete
      const completePayload: Record<string, unknown> = {
        upload_id: init.upload_id,
        dek: dek.hex,
        content_hash: workerResult.contentHash,
        parts: workerResult.parts,
      }
      if (searchText) completePayload.search_text = searchText
      if (thumbnail) {
        completePayload.media_width = thumbnail.width
        completePayload.media_height = thumbnail.height
        if (thumbnail.duration) completePayload.media_duration = thumbnail.duration
      }
      if (thumbnailUploadId) completePayload.thumbnail_upload_id = thumbnailUploadId

      await api.post('/file/upload', completePayload)

      upload.status = 'processing'
      upload.progress = 100
      upload.speed = 0

      // Remove from uploads list after a short delay
      setTimeout(() => {
        uploads.value = uploads.value.filter(u => u.id !== upload.id)
        if (uploads.value.length === 0) showPanel.value = false
      }, 2000)

      const fs = useFileSystemStore()
      fs.invalidateCache()
      fs.refresh()

    } catch (e: unknown) {
      if (upload.status !== 'cancelled') {
        upload.status = 'failed'
        const err = e as { response?: { data?: { error?: string } }; message?: string }
        upload.error = err.response?.data?.error || err.message
      }
      console.error('Upload failed:', e)
    }
  }

  // ── Encrypt worker ─────────────────────────────────────────────────────

  interface WorkerResult {
    parts: { part_number: number; etag: string }[]
    contentHash: string
  }

  function runEncryptWorker(
    upload: Upload,
    file: File,
    dekRaw: Uint8Array,
    initialUrls: string[],
    partSize: number,
    uploadId: string,
  ): Promise<WorkerResult | null> {
    return new Promise((resolve, reject) => {
      const worker = new Worker(
        new URL('../workers/encrypt.worker.ts', import.meta.url),
        { type: 'module' },
      )
      workers.set(upload.id, worker)

      worker.onmessage = async (e: MessageEvent) => {
        const msg = e.data
        switch (msg.type) {
          case 'progress': {
            const pct = msg.total > 0 ? Math.floor((msg.uploaded / msg.total) * 100) : 0
            upload.progress = pct
            upload.bytesUploaded = msg.uploaded
            // Report to server
            if (upload.taskId) {
              ws.request('upload.progress', {
                task_id: upload.taskId,
                progress: msg.uploaded / msg.total,
              }).catch(() => {})
            }
            break
          }
          case 'part':
            upload.completedParts.push({ partNumber: msg.partNumber })
            break
          case 'need-urls':
            // Worker needs more presigned URLs
            try {
              const res = await api.get<PresignResponse>('/file/upload/presign', {
                params: { upload_id: uploadId, start: msg.from, count: msg.count },
              })
              worker.postMessage({
                type: 'add-urls',
                partUrls: res.data.parts.map((p: { presigned_url: string }) => p.presigned_url),
              })
            } catch (err) {
              worker.postMessage({ type: 'cancel' })
              reject(err)
            }
            break
          case 'done':
            workers.delete(upload.id)
            worker.terminate()
            resolve({ parts: msg.parts, contentHash: msg.contentHash })
            break
          case 'error':
            workers.delete(upload.id)
            worker.terminate()
            reject(new Error(msg.message))
            break
        }
      }

      worker.onerror = (err) => {
        workers.delete(upload.id)
        worker.terminate()
        reject(new Error(err.message))
      }

      worker.postMessage({
        type: 'start',
        file,
        dekRaw: dekRaw.buffer,
        partUrls: initialUrls,
        partSize,
      })
    })
  }

  // ── Thumbnail upload (as a separate encrypted file) ──────────────────

  async function uploadThumbnail(
    parentUpload: Upload,
    thumb: { blob: Blob; width: number; height: number; duration?: number },
    parentDek: { key: CryptoKey; raw: Uint8Array; hex: string },
    targetPath: string,
  ): Promise<string | null> {
    try {
      const thumbDek = await generateDEK()
      const encrypted = await encryptBlob(thumbDek.raw, await thumb.blob.arrayBuffer())

      // Init a mini upload for the thumbnail
      const initRes = await api.post<InitResponse>('/file/upload', {
        path: targetPath.replace(/\/?$/, '/') + '.user/thumbnails',
        file_name: `${parentUpload.uploadId}.webp`,
        file_size: thumb.blob.size,
        content_type: 'image/webp',
      })
      const thumbInit = initRes.data

      // Get presigned URL for the single part
      const presignRes = await api.get<PresignResponse>('/file/upload/presign', {
        params: { upload_id: thumbInit.upload_id, start: 1, count: 1 },
      })
      const url = presignRes.data.parts[0].presigned_url

      // Upload encrypted thumbnail
      const resp = await fetch(url, { method: 'PUT', body: encrypted.slice().buffer })
      if (!resp.ok) throw new Error(`Thumbnail upload failed: ${resp.status}`)
      const etag = resp.headers.get('ETag') || ''

      // Complete thumbnail upload
      await api.post('/file/upload', {
        upload_id: thumbInit.upload_id,
        dek: thumbDek.hex,
        parts: [{ part_number: 1, etag }],
      })

      return thumbInit.upload_id
    } catch (e) {
      console.warn('Thumbnail upload failed:', e)
      return null
    }
  }

  // ── Pause / Resume / Cancel ──────────────────────────────────────────

  function pauseUpload(id: string): void {
    const upload = uploads.value.find(u => u.id === id)
    if (upload && upload.status === 'uploading') {
      upload.status = 'paused'
      const worker = workers.get(id)
      if (worker) worker.postMessage({ type: 'pause' })
    }
  }

  function resumeUpload(id: string): void {
    const upload = uploads.value.find(u => u.id === id)
    if (upload && upload.status === 'paused') {
      upload.status = 'uploading'
      upload.startTime = Date.now()
      upload.startProgress = upload.progress
      const worker = workers.get(id)
      if (worker) worker.postMessage({ type: 'resume' })
    }
  }

  async function cancelUpload(id: string): Promise<void> {
    const upload = uploads.value.find(u => u.id === id)
    if (!upload) return

    upload.status = 'cancelled'
    const worker = workers.get(id)
    if (worker) {
      worker.postMessage({ type: 'cancel' })
      worker.terminate()
      workers.delete(id)
    }
    if (upload.uploadId) {
      try {
        await api.delete('/file/upload', { params: { upload_id: upload.uploadId, task_id: upload.taskId || '' } })
      } catch { /* best-effort */ }
    }
  }

  function retryUpload(id: string): void {
    const u = uploads.value.find(u => u.id === id)
    if (!u || !u._file) return
    const file: File = u._file
    const targetPath: string = u.targetPath
    uploads.value = uploads.value.filter(x => x.id !== id)
    startUpload(file, targetPath)
  }

  function removeCompleted(): void {
    uploads.value = uploads.value.filter(u => u.status === 'uploading' || u.status === 'paused')
    if (uploads.value.length === 0) showPanel.value = false
  }

  return {
    uploads,
    showPanel,
    activeUploads,
    hasActive,
    uploadFiles,
    pauseUpload,
    resumeUpload,
    cancelUpload,
    retryUpload,
    removeCompleted,
  }
})
