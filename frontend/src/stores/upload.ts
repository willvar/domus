import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useI18n } from '../composables/useI18n'
import { showConfirm, showDuplicateDialog } from '../composables/useNativeDialog'
import { useActivityStore } from './activity'
import { useFileSystemStore } from './fileSystem'
import { UploadClient } from '../uploads/client'
import { ResourcePool } from '../uploads/resources'
import {
  generateDEK,
  generateThumbnail,
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

  const uploader = new UploadClient()
  const initCapacity = new ResourcePool([0, 1, 2, 3])
  const thumbnailCapacity = new ResourcePool([true])
  const controls = new Map<string, AbortController>()
  const thumbnailControls = new Map<string, AbortController>()

  // Keep failures visible with the original response error and File for retry.
  ws.on('task.update', (data: TaskUpdateEvent) => {
    const entry = uploads.value.find(u => u.taskId === data.task_id)
    if (!entry) return
    if (data.status === 'failed') {
      // Publication already has an authoritative HTTP result in flight. A
      // generic task event must not abort it or replace its response error.
      if (entry.status === 'processing' && controls.has(entry.id)) return
      const alreadyFailed = entry.status === 'failed'
      entry.status = 'failed'
      entry.phase = undefined
      entry.error ||= t('upload.status_failed')
      if (!alreadyFailed) controls.get(entry.id)?.abort()
    } else if (data.status === 'completed' || data.status === 'cancelled') {
      if (data.status === 'cancelled') {
        entry.status = 'cancelled'
        controls.get(entry.id)?.abort()
      }
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
      const entry = createSession(file, targetPath)
      void startUpload(entry, zeroByteFiles.length > 0, strategy)
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
      case 'queued':
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
      if (now - lastAt < 300) return
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

  function createSession(file: File, targetPath: string): UploadSession {
    const entry: UploadSession = {
      id: crypto?.randomUUID?.() || (Math.random().toString(36).slice(2) + Date.now().toString(36)),
      fileName: file.name,
      fileSize: file.size,
      progress: 0,
      speed: 0,
      status: 'uploading',
      phase: 'queued',
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
    return uploads.value.find(item => item.id === entry.id)!
  }

  async function startUpload(entry: UploadSession, allowZeroByte = false, conflictStrategy: string | null = null): Promise<void> {
    if (entry.status === 'cancelled') return
    const file = entry._file as File
    const targetPath = entry.targetPath
    const upload = entry
    const control = new AbortController()
    const signal = control.signal
    controls.set(upload.id, control)
    upload.phase = 'generating'
    upload.startTime = Date.now()
    let completed = false

    try {
      if (!file.size && !allowZeroByte) {
        upload.status = 'failed'
        upload.error = t('upload.zero_byte_error')
        return
      }

      const dek = await generateDEK()
      upload.dekHex = dek.hex
      signal.throwIfAborted()
      await waitUntilResumed(upload, signal)

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

      const init = await initializeUpload(upload, initPayload, signal)

      if (init.file_name && init.file_name !== file.name) {
        upload.fileName = init.file_name
      }
      upload.uploadId = init.upload_id
      upload.taskId = init.task_id || null
      upload.partSize = init.part_size
      upload.totalParts = init.total_parts
      signal.throwIfAborted()
      const effectiveDekHex = init.dek || dek.hex
      const effectiveDekRaw = hexKey(effectiveDekHex)
      upload.dekHex = effectiveDekHex
      await syncTask(upload, { phase: 'generating', force: true })
      await waitUntilResumed(upload, signal)

      // 2. Acquire capacity in the shared worker before reading or signing.
      if (upload.status !== 'paused') upload.phase = 'queued'
      const workerResult = await runEncryptWorker(
        upload,
        file,
        effectiveDekRaw,
        init.part_size,
        init.total_parts,
        init.upload_id,
        signal,
      )

      if (upload.status === 'cancelled' || !workerResult) return

      // 3. Upload thumbnail as a separate encrypted file
      await waitUntilResumed(upload, signal)
      upload.phase = 'thumbnail'
      let thumbnail: { width: number; height: number; duration?: number } | null = null
      let thumbnailUploadId: string | null = null
      for (;;) {
        await waitUntilResumed(upload, signal)
        const stage = new AbortController()
        const abort = () => stage.abort(signal.reason)
        signal.addEventListener('abort', abort, { once: true })
        thumbnailControls.set(upload.id, stage)
        let lease: boolean | undefined
        try {
          lease = await thumbnailCapacity.acquire(stage.signal)
          stage.signal.throwIfAborted()
          const generated = await generateThumbnail(file, stage.signal)
          stage.signal.throwIfAborted()
          if (generated) {
            upload.phase = 'thumbnail'
            await syncTask(upload, { phase: 'thumbnail', force: true })
            thumbnailUploadId = await uploadThumbnail(upload, generated, stage.signal)
            thumbnail = { width: generated.width, height: generated.height, duration: generated.duration }
          }
          break
        } catch (error) {
          signal.throwIfAborted()
          if (!stage.signal.aborted) throw error
          thumbnail = null
        } finally {
          if (lease !== undefined) thumbnailCapacity.release(lease)
          signal.removeEventListener('abort', abort)
          thumbnailControls.delete(upload.id)
        }
      }
      signal.throwIfAborted()

      // 4. Complete. Once publication starts, await its authoritative result.
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

      signal.throwIfAborted()
      await api.post('/file/upload', completePayload, { timeout: 120000 })
      completed = true

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
      if (upload.status !== 'cancelled' && !signal.aborted) {
        upload.status = 'failed'
        upload.phase = undefined
        const err = e as { response?: { data?: { error?: string } }; message?: string }
        upload.error = err.response?.data?.error || err.message
      }
      if (!signal.aborted) console.error('Upload failed:', e)
    } finally {
      // The running lifecycle owns remote cleanup, including late init
      // responses. UI cancellation only signals it, so cleanup runs once.
      const failedLocally = upload.status === 'failed' && !signal.aborted
      const cancelled = signal.aborted && upload.status !== 'failed'
      if (!completed && (failedLocally || cancelled)) {
        const status = failedLocally ? 'failed' : 'cancelled'
        try {
          if (upload.uploadId) {
            await api.post('/file/upload/cancel', {
              upload_id: upload.uploadId, task_id: upload.taskId || '',
              reason: failedLocally ? 'upload_failed' : 'user_cancel', status,
            })
          } else {
            await syncTask(upload, { status, force: true })
          }
        } catch {
          await syncTask(upload, { status, force: true })
        }
      }
      controls.delete(upload.id)
      upload._resume = null
      upload.dekHex = null
    }
  }

  async function initializeUpload(upload: UploadSession, payload: Record<string, unknown>, signal: AbortSignal): Promise<InitResponse> {
    for (;;) {
      await waitUntilResumed(upload, signal)
      const lease = await initCapacity.acquire(signal)
      try {
        signal.throwIfAborted()
        if (upload.status === 'paused') continue
        // Do not discard an in-flight response: cancellation needs its upload ID
        // to release the remote reservation. Queued init requests are abortable.
        const response = await api.post<InitResponse>('/file/upload', payload, { 'axios-retry': { retries: 0 } })
        return response.data
      } finally { initCapacity.release(lease) }
    }
  }

  async function waitUntilResumed(upload: UploadSession, signal: AbortSignal): Promise<void> {
    signal.throwIfAborted()
    while (upload.status === 'paused') await new Promise<void>((resolve, reject) => {
      const abort = () => { upload._resume = null; reject(signal.reason) }
      upload._resume = () => { signal.removeEventListener('abort', abort); upload._resume = null; resolve() }
      signal.addEventListener('abort', abort, { once: true })
    })
    signal.throwIfAborted()
  }

  // ── Encrypt worker ─────────────────────────────────────────────────────

  interface WorkerResult {
    contentHash: string
  }

  function runEncryptWorker(
    upload: UploadSession,
    file: File,
    dekRaw: Uint8Array,
    partSize: number,
    totalParts: number,
    uploadId: string,
    signal: AbortSignal,
  ): Promise<WorkerResult | null> {
    let firstPart = 1
    let urls: string[] = []
    let expiresAt = 0
    return uploader.upload({ id: upload.id, file, dekRaw: dekRaw.buffer as ArrayBuffer, partSize, totalParts, paused: upload.status === 'paused' }, {
      async url(part, requestSignal) {
        if (!Number.isInteger(part) || part < 1 || part > totalParts) throw new Error('Invalid presign part')
        if (part < firstPart || part >= firstPart + urls.length || Date.now() >= expiresAt) {
          const res = await api.get<PresignResponse>('/file/upload/presign', {
            params: { upload_id: uploadId, start: part, count: PRESIGN_BATCH }, signal: requestSignal,
          })
          firstPart = part
          urls = res.data.parts.map(item => item.presigned_url)
          expiresAt = Date.now() + Math.max(0, res.data.expires_in - 60) * 1000
        }
        const url = urls[part - firstPart]
        if (!url) throw new Error('Upload URL was not issued')
        return url
      },
      progress(uploaded, total) {
        upload.progress = total > 0 ? Math.floor(uploaded / total * 100) : 0
        upload.bytesUploaded = uploaded
        if (upload.status !== 'paused') upload.phase = 'uploading'
        void syncTask(upload)
      },
      phase(phase) {
        if (upload.status !== 'paused') upload.phase = phase
        void syncTask(upload)
      },
    }, signal).then(contentHash => contentHash === null ? null : { contentHash })
  }

  // ── Thumbnail upload (as a separate encrypted file) ──────────────────

  async function uploadThumbnail(
    parentUpload: UploadSession,
    thumb: { blob: Blob; width: number; height: number; duration?: number },
    signal: AbortSignal,
  ): Promise<string | null> {
    let thumbnailID = ''
    try {
      signal.throwIfAborted()
      const thumbDek = await generateDEK()

      // Init a mini upload for the thumbnail
      const initRes = await api.post<InitResponse>('/file/upload', {
        path: '/.user/thumbnails/',
        file_name: `thumb_${parentUpload.uploadId}.webp`,
        file_size: thumb.blob.size,
        content_type: 'image/webp',
        internal: true,
        dek: thumbDek.hex,
        conflict_strategy: 'replace',
      })
      const thumbInit = initRes.data
      thumbnailID = thumbInit.upload_id
      signal.throwIfAborted()
      // Thumbnails use the same encryption, disk leases and retry path.
      const contentHash = await uploader.upload({
        id: `thumbnail:${thumbInit.upload_id}`,
        file: new File([thumb.blob], 'thumbnail.webp', { type: 'image/webp' }),
        dekRaw: hexKey(thumbInit.dek || thumbDek.hex).buffer as ArrayBuffer,
        partSize: thumbInit.part_size, totalParts: thumbInit.total_parts,
      }, {
        async url(part, requestSignal) {
          const res = await api.get<PresignResponse>('/file/upload/presign', {
            params: { upload_id: thumbInit.upload_id, start: part, count: 1 }, signal: requestSignal,
          })
          const url = res.data.parts[0]?.presigned_url
          if (!url) throw new Error('Thumbnail upload URL was not issued')
          return url
        },
        progress() {}, phase() {},
      }, signal)
      signal.throwIfAborted()
      if (!contentHash) throw new Error('Thumbnail upload cancelled')

      // Complete thumbnail upload
      await api.post('/file/upload', {
        upload_id: thumbInit.upload_id,
        encrypted_size: encryptedFileSize(thumb.blob.size),
        content_hash: contentHash,
      }, { signal })

      return thumbInit.upload_id
    } catch (e) {
      if (thumbnailID) await api.post('/file/upload/cancel', { upload_id: thumbnailID, reason: 'thumbnail_failed' }).catch(() => {})
      signal.throwIfAborted()
      console.warn('Thumbnail upload failed:', e)
      return null
    }
  }

  // ── Pause / Resume / Cancel ──────────────────────────────────────────

  function pauseUpload(id: string): void {
    const upload = uploads.value.find(u => u.id === id)
    if (upload && upload.status === 'uploading') {
      upload.status = 'paused'
      upload.phase = 'paused'
      uploader.pause(id)
      thumbnailControls.get(id)?.abort(new DOMException('Upload paused', 'AbortError'))
    }
  }

  function resumeUpload(id: string): void {
    const upload = uploads.value.find(u => u.id === id)
    if (upload && upload.status === 'paused') {
      upload.status = 'uploading'
      upload.phase = 'uploading'
      upload.startTime = Date.now()
      upload.startProgress = upload.progress
      upload._resume?.()
      uploader.resume(id)
      void syncTask(upload, { phase: 'uploading', force: true })
    }
  }

  function cancelUpload(id: string): void {
    const upload = uploads.value.find(u => u.id === id)
    if (!upload || upload.status === 'processing') return

    upload.status = 'cancelled'
    upload.phase = undefined
    removeUploadSession(id)
    controls.get(id)?.abort()
  }

  function retryUpload(id: string): void {
    const u = uploads.value.find(u => u.id === id)
    if (!u || !u._file || controls.has(id) || !['failed', 'interrupted'].includes(u.status)) return
    const file: File = u._file
    const targetPath: string = u.targetPath
    removeUploadSession(id)
    const entry = createSession(file, targetPath)
    void startUpload(entry, file.size === 0)
  }

  function abortUploads(reason: 'page_unload' | 'startup_reconcile'): void {
    for (const control of controls.values()) control.abort()
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
