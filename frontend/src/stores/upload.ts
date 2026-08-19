import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useI18n } from '../composables/useI18n'
import { showConfirm, showDuplicateDialog } from '../composables/useNativeDialog'
import { useActivityStore } from './activity'
import { useFileSystemStore } from './fileSystem'
import {
  generateDEK,
  generateThumbnail,
  encryptBlob,
} from '../composables/useCryptoUpload'
import type {
  UploadSession,
  ConflictInfo,
  TaskUpdateEvent,
  DuplicateDecision,
} from '../types'

// Presigned URL batch size — request this many at a time from the server
const PRESIGN_BATCH = 100

function getClientInstanceId(): string {
  return crypto?.randomUUID?.() || (Math.random().toString(36).slice(2) + Date.now().toString(36))
}

export const useUploadStore = defineStore('upload', () => {
  const { t } = useI18n()
  const ws = useWebSocket()
  const clientInstanceId = getClientInstanceId()
  const uploads: Ref<UploadSession[]> = ref([])
  const activityStore = useActivityStore()

  const activeUploads: ComputedRef<UploadSession[]> = computed(() => uploads.value.filter(u => u.status === 'uploading' || u.status === 'paused'))
  const hasActive: ComputedRef<boolean> = computed(() => activeUploads.value.length > 0)

  // Active encrypt workers keyed by upload id
  const workers = new Map<string, Worker>()

  // Once the server-side task reaches a terminal state, the persisted task
  // becomes the source of truth and the local upload session can disappear.
  ws.on('task.update', (data: TaskUpdateEvent) => {
    const entry = uploads.value.find(u => u.taskId === data.task_id)
    if (!entry) return
    if (data.status === 'completed' || data.status === 'failed' || data.status === 'cancelled') {
      removeUploadSession(entry.id)
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
    file_name: string
    oss_upload_id: string
    part_size: number
    total_parts: number
    dek: string
  }

  interface PresignResponse {
    parts: { part_number: number; presigned_url: string }[]
    expires_in: number
  }

  function encryptedFileSize(plainSize: number): number {
    if (plainSize <= 0) return 5
    const chunkPlain = 65536
    const overhead = 28
    const numChunks = Math.ceil(plainSize / chunkPlain)
    return 5 + plainSize + numChunks * overhead
  }

  function removeUploadSession(id: string): void {
    uploads.value = uploads.value.filter(u => u.id !== id)
  }

  function taskProgressFor(session: UploadSession, phase?: string): number {
    const ratio = Math.max(0, Math.min(1, session.progress / 100))
    switch (phase) {
      case 'generating':
        return 0.05
      case 'encrypting':
        return 0.1
      case 'uploading':
        return Math.max(0.1, Math.min(0.9, ratio * 0.9))
      case 'thumbnail':
        return 0.95
      case 'processing':
        return 0.99
      default:
        return Math.max(0, Math.min(1, ratio))
    }
  }

  async function syncTask(
    session: UploadSession,
    opts: { phase?: string; progress?: number; status?: 'failed' | 'cancelled'; force?: boolean } = {},
  ): Promise<void> {
    if (!session.taskId) return

    const phase = opts.phase ?? session.phase
    const progress = opts.progress ?? taskProgressFor(session, phase)
    const now = Date.now()
    const lastAt = session._lastTaskSyncAt || 0
    const lastProgress = session._lastTaskProgress ?? -1
    const lastPhase = session._lastTaskPhase || ''
    const status = opts.status
    const force = !!opts.force || !!status

    if (!force) {
      const progressDelta = Math.abs(progress - lastProgress)
      const phaseChanged = phase !== lastPhase
      if (!phaseChanged && progressDelta < 0.01 && now - lastAt < 300) {
        return
      }
    }

    session._lastTaskSyncAt = now
    session._lastTaskProgress = progress
    session._lastTaskPhase = phase

    const payload: Record<string, unknown> = {
      task_id: session.taskId,
      progress,
    }
    if (phase) payload.phase = phase
    if (status) payload.status = status

    try {
      await ws.request('task.report', payload)
    } catch {
      // Best-effort only; the local session remains authoritative while active.
    }
  }

  async function startUpload(file: File, targetPath: string, allowZeroByte = false, conflictStrategy: string | null = null): Promise<void> {
    const entry: UploadSession = {
      id: crypto?.randomUUID?.() || (Math.random().toString(36).slice(2) + Date.now().toString(36)),
      fileName: file.name,
      fileSize: file.size,
      progress: 0,
      speed: 0,
      status: 'uploading',
      phase: 'generating',
      uploadId: null,
      taskId: null,
      targetPath,
      partSize: undefined,
      totalParts: undefined,
      encryptedSize: encryptedFileSize(file.size),
      dekHex: null,
      startTime: Date.now(),
      startProgress: 0,
      bytesUploaded: 0,
      _lastTaskSyncAt: 0,
      _lastTaskProgress: -1,
      _lastTaskPhase: '',
      _resume: null,
      _file: file,
    }
    uploads.value.push(entry)
    try { activityStore.show() } catch { /* ignore */ }
    const upload = uploads.value[uploads.value.length - 1]

    try {
      if (!file.size && !allowZeroByte) {
        upload.status = 'failed'
        upload.error = t('upload.zero_byte_error')
        return
      }

      const dek = await generateDEK()
      upload.dekHex = dek.hex

      // 1. Init — reserve a DOFS generation and create an OSS multipart session.
      const initPayload: Record<string, unknown> = {
        path: targetPath,
        file_name: file.name,
        file_size: file.size,
        content_type: file.type || '',
        client_instance_id: clientInstanceId,
        dek: dek.hex,
      }
      if (conflictStrategy) initPayload.conflict_strategy = conflictStrategy

      const initRes = await api.post<InitResponse>('/file/upload', initPayload)
      const init = initRes.data
      const effectiveDekHex = init.dek || dek.hex
      const effectiveDekRaw = hexKey(effectiveDekHex)
      upload.dekHex = effectiveDekHex

      if (init.file_name && init.file_name !== file.name) {
        upload.fileName = init.file_name
      }
      upload.uploadId = init.upload_id
      upload.taskId = init.task_id || null
      upload.partSize = init.part_size
      upload.totalParts = init.total_parts
      await syncTask(upload, { phase: 'generating', force: true })

      // 2. Generate a local thumbnail. File plaintext is never included in a
      // Domus API request; ciphertext is sent directly to object storage.
      const thumbnail = await generateThumbnail(file)

      if ((upload.status as string) === 'cancelled') return

      // 3. Enter encrypting phase
      upload.phase = 'encrypting'
      await syncTask(upload, { phase: 'encrypting', force: true })

      // 4. Get first batch of presigned URLs
      const presignRes = await api.get<PresignResponse>('/file/upload/presign', {
        params: { upload_id: init.upload_id, start: 1, count: PRESIGN_BATCH },
      })
      const partUrls = presignRes.data.parts.map(p => p.presigned_url)

      if ((upload.status as string) === 'cancelled') return

      // 5. Encrypt + hash + upload (all in worker, main thread stays free)
      upload.phase = 'uploading'
      await syncTask(upload, { phase: 'uploading', force: true })
      const workerResult = await runEncryptWorker(
        upload,
        file,
        effectiveDekRaw,
        partUrls,
        init.part_size,
        init.total_parts,
        init.upload_id,
      )

      if (upload.status === 'cancelled' || !workerResult) return

      // 6. Upload thumbnail as a separate encrypted file
      let thumbnailUploadId: string | null = null
      if (thumbnail) {
        upload.phase = 'thumbnail'
        await syncTask(upload, { phase: 'thumbnail', force: true })
        thumbnailUploadId = await uploadThumbnail(upload, thumbnail, targetPath)
      }

      // 7. Complete
      upload.phase = 'processing'
      upload.status = 'processing'
      await syncTask(upload, { phase: 'processing', force: true })
      const completePayload: Record<string, unknown> = {
        upload_id: init.upload_id,
        content_hash: workerResult.contentHash,
        encrypted_size: upload.encryptedSize,
      }
      if (thumbnail) {
        completePayload.media_width = thumbnail.width
        completePayload.media_height = thumbnail.height
        if (thumbnail.duration) completePayload.media_duration = thumbnail.duration
      }
      if (thumbnailUploadId) completePayload.thumbnail_upload_id = thumbnailUploadId

      await api.post('/file/upload', completePayload, { timeout: 120000 })

      upload.progress = 100
      upload.speed = 0

      // Remove from uploads list after a short delay
      setTimeout(() => {
        removeUploadSession(upload.id)
      }, 2000)

      const fs = useFileSystemStore()
      fs.invalidateCache()
      fs.refresh()

    } catch (e: unknown) {
      if (upload.status !== 'cancelled') {
        upload.status = 'failed'
        upload.phase = undefined
        const err = e as { response?: { data?: { error?: string } }; message?: string }
        upload.error = err.response?.data?.error || err.message
        if (upload.uploadId) {
          try {
            await api.post('/file/upload/cancel', {
              upload_id: upload.uploadId,
              task_id: upload.taskId || '',
              reason: 'upload_failed',
              status: 'failed',
            })
          } catch {
            await syncTask(upload, { status: 'failed', force: true })
          }
        } else {
          await syncTask(upload, { status: 'failed', force: true })
        }
      }
      console.error('Upload failed:', e)
    }
  }

  // ── Encrypt worker ─────────────────────────────────────────────────────

  interface WorkerResult {
    contentHash: string
  }

  function runEncryptWorker(
    upload: UploadSession,
    file: File,
    dekRaw: Uint8Array,
    initialUrls: string[],
    partSize: number,
    totalParts: number,
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
            upload.phase = 'uploading'
            void syncTask(upload, { phase: 'uploading' })
            break
          }
          case 'part':
            break
          case 'need-urls':
            // Worker needs more presigned URLs
            try {
              if (!Number.isInteger(msg.from) || !Number.isInteger(msg.count) || msg.from < 1 || msg.count < 1) {
                throw new Error(`Invalid presign request: from=${msg.from} count=${msg.count}`)
              }
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
            resolve({ contentHash: msg.contentHash })
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
        totalParts,
      })
    })
  }

  // ── Thumbnail upload (as a separate encrypted file) ──────────────────

  async function uploadThumbnail(
    parentUpload: UploadSession,
    thumb: { blob: Blob; width: number; height: number; duration?: number },
    targetPath: string,
  ): Promise<string | null> {
    try {
      const thumbDek = await generateDEK()
      const encrypted = await encryptBlob(thumbDek.raw, await thumb.blob.arrayBuffer())

      // Init a mini upload for the thumbnail
      const initRes = await api.post<InitResponse>('/file/upload', {
        path: '/.user/thumbnails/',
        file_name: `thumb_${parentUpload.uploadId}.webp`,
        file_size: thumb.blob.size,
        content_type: 'image/webp',
        internal: true,
        dek: thumbDek.hex,
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

      // Complete thumbnail upload
      await api.post('/file/upload', {
        upload_id: thumbInit.upload_id,
        encrypted_size: encrypted.byteLength,
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
      upload.phase = 'uploading'
      upload.startTime = Date.now()
      upload.startProgress = upload.progress
      const worker = workers.get(id)
      if (worker) worker.postMessage({ type: 'resume' })
      void syncTask(upload, { phase: 'uploading', force: true })
    }
  }

  async function cancelUpload(id: string): Promise<void> {
    const upload = uploads.value.find(u => u.id === id)
    if (!upload) return

    upload.status = 'cancelled'
    upload.phase = undefined
    removeUploadSession(id)
    const worker = workers.get(id)
    if (worker) {
      worker.postMessage({ type: 'cancel' })
      worker.terminate()
      workers.delete(id)
    }
    if (upload.uploadId) {
      try {
        await api.post('/file/upload/cancel', {
          upload_id: upload.uploadId,
          task_id: upload.taskId || '',
          reason: 'user_cancel',
        })
      } catch {
        await syncTask(upload, { status: 'cancelled', force: true })
      }
    } else {
      await syncTask(upload, { status: 'cancelled', force: true })
    }
  }

  function retryUpload(id: string): void {
    const u = uploads.value.find(u => u.id === id)
    if (!u || !u._file) return
    const file: File = u._file
    const targetPath: string = u.targetPath
    removeUploadSession(id)
    startUpload(file, targetPath)
  }

  function abortUploads(reason: 'page_unload' | 'startup_reconcile'): void {
    const targets = uploads.value.filter(u =>
      !!u.uploadId && (u.status === 'uploading' || u.status === 'paused')
    )
    const baseURL = api.defaults.baseURL || window.location.origin
    for (const upload of targets) {
      const payload = JSON.stringify({
        upload_id: upload.uploadId,
        task_id: upload.taskId || '',
        reason,
      })
      let sent = false
      try {
        if (reason === 'page_unload' && typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function') {
          sent = navigator.sendBeacon(new URL('/file/upload/cancel', baseURL).toString(), new Blob([payload], { type: 'application/json' }))
        }
      } catch {
        sent = false
      }
      if (sent) continue
      try {
        fetch(new URL('/file/upload/cancel', baseURL).toString(), {
          method: 'POST',
          credentials: 'include',
          keepalive: true,
          headers: { 'Content-Type': 'application/json' },
          body: payload,
        }).catch(() => {})
      } catch {
        // ignore best-effort unload cleanup failures
      }
    }
  }

  async function reconcileStaleUploads(): Promise<void> {
    try {
      await api.post('/file/upload/cleanup', { client_instance_id: clientInstanceId })
    } catch {
      // ignore startup reconcile failures
    }
  }

  let heartbeatTimer: number | null = null

  async function sendHeartbeat(): Promise<void> {
    const targets = uploads.value.filter(u => !!u.uploadId && (u.status === 'uploading' || u.status === 'paused'))
    await Promise.all(targets.map((upload) =>
      api.post('/file/upload/heartbeat', { upload_id: upload.uploadId }).catch(() => {})
    ))
  }

  function bindPageUnloadCleanup(): void {
    const handler = () => abortUploads('page_unload')
    window.addEventListener('pagehide', handler)
    window.addEventListener('beforeunload', handler)
  }

  function startHeartbeat(): void {
    if (heartbeatTimer != null) return
    heartbeatTimer = window.setInterval(() => {
      void sendHeartbeat()
    }, 5000)
  }

  bindPageUnloadCleanup()
  startHeartbeat()
  void reconcileStaleUploads()

  function hexKey(value: string): Uint8Array {
    if (!/^[0-9a-fA-F]{64}$/.test(value)) throw new Error('Invalid upload encryption key')
    const result = new Uint8Array(32)
    for (let index = 0; index < result.length; index++) {
      result[index] = Number.parseInt(value.slice(index * 2, index * 2 + 2), 16)
    }
    return result
  }

  function removeCompleted(): void {
    uploads.value = uploads.value.filter(u => u.status === 'uploading' || u.status === 'paused')
  }

  return {
    clientInstanceId,
    uploads,
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
