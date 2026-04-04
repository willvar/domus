import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useI18n } from '../composables/useI18n'
import { showConfirm, showDuplicateDialog } from '../composables/useNativeDialog'
import { useFileSystemStore } from './fileSystem'
import { useJobsStore } from './jobs'
import type {
  Upload,
  UploadPart,
  InterruptedUpload,
  UploadInitResponse,
  UploadPartResponse,
  UploadStatusResponse,
  ConflictInfo,
  TaskUpdateEvent,
  DuplicateDecision,
} from '../types'

const INTERRUPTED_KEY = 'zephyr_interrupted_uploads'

function saveInterruptedState(uploads: Upload[]): void {
  const active = uploads.filter(u => u.status === 'uploading' || u.status === 'paused')
  if (active.length === 0) {
    localStorage.removeItem(INTERRUPTED_KEY)
    return
  }
  const data: InterruptedUpload[] = active.map(u => ({
    fileName: u.fileName,
    fileSize: u.fileSize,
    uploadId: u.uploadId!,
    taskId: u.taskId || null,
    progress: u.progress,
    targetPath: u.targetPath,
    etags: u.etags,
  }))
  localStorage.setItem(INTERRUPTED_KEY, JSON.stringify(data))
}

function loadInterruptedState(): InterruptedUpload[] {
  const raw = localStorage.getItem(INTERRUPTED_KEY)
  if (!raw) return []
  try { return JSON.parse(raw) } catch { return [] }
}

export const useUploadStore = defineStore('upload', () => {
  const { t } = useI18n()
  const ws = useWebSocket()
  const uploads: Ref<Upload[]> = ref([])
  const showPanel: Ref<boolean> = ref(false)

  const activeUploads: ComputedRef<Upload[]> = computed(() => uploads.value.filter(u => u.status === 'uploading' || u.status === 'paused'))
  const hasActive: ComputedRef<boolean> = computed(() => activeUploads.value.length > 0)

  // Listen for task updates — remove local upload entries once server takes over
  ws.on('task.update', (data: TaskUpdateEvent) => {
    const entry = uploads.value.find(u => u.taskId === data.task_id)
    if (!entry || entry.status !== 'processing') return
    // Server is now tracking this task; remove from local upload list
    uploads.value = uploads.value.filter(u => u.taskId !== data.task_id)
    if (uploads.value.length === 0) showPanel.value = false
  })

  function checkInterrupted(): void {
    const interrupted = loadInterruptedState()
    if (interrupted.length === 0) return
    localStorage.removeItem(INTERRUPTED_KEY)
    for (const item of interrupted) {
      uploads.value.push({
        id: crypto?.randomUUID?.() || (Math.random().toString(36).slice(2) + Date.now().toString(36)),
        fileName: item.fileName,
        fileSize: item.fileSize,
        progress: item.progress,
        speed: 0,
        status: 'interrupted',
        uploadId: item.uploadId,
        taskId: item.taskId || null,
        targetPath: item.targetPath,
        parts: [],
        etags: item.etags || [],
        startTime: 0,
        startProgress: 0,
        bytesUploaded: 0,
        _resume: null,
        _file: null,
      })
    }
    showPanel.value = true
  }

  async function uploadFiles(fileList: FileList, targetPath: string): Promise<void> {
    const allFiles: File[] = Array.from(fileList)
    if (allFiles.length === 0) return

    // Check conflicts via backend
    const names: string[] = allFiles.map(f => f.name)
    let conflictMap: Map<string, ConflictInfo> = new Map()
    try {
      const res = await api.post<{ conflicts?: ConflictInfo[] }>('/file/upload', { path: targetPath, names })
      for (const c of res.data.conflicts || []) {
        conflictMap.set(c.name, c)
      }
    } catch {
      // If check fails, proceed without conflict detection (fallback to backend 409)
    }

    const files: File[] = []
    const strategies: Map<string, string> = new Map() // file name -> conflict_strategy
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
        strategies.set(file.name, action) // 'replace' or 'rename'
      }

      files.push(file)
    }

    if (files.length === 0) return

    const zeroByteFiles: File[] = files.filter(file => file.size === 0)

    if (zeroByteFiles.length > 0) {
      const fileListText: string = zeroByteFiles.map(file => `- ${file.name}`).join('\n')
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

  async function startUpload(file: File, targetPath: string, allowZeroByte: boolean = false, conflictStrategy: string | null = null): Promise<void> {
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
      parts: [],
      etags: [],
      startTime: Date.now(),
      startProgress: 0,
      bytesUploaded: 0,
      _resume: null,
      _file: file,
    }
    uploads.value.push(entry)
    showPanel.value = true
    // Also open the jobs panel since uploads are shown there
    try {
      useJobsStore().panelOpen = true
    } catch { /* ignore */ }
    const upload = uploads.value[uploads.value.length - 1]

    try {
      if (!file.size && !allowZeroByte) {
        upload.status = 'failed'
        upload.error = t('upload.zero_byte_error')
        return
      }

      const initPayload: Record<string, unknown> = {
        path: targetPath,
        file_name: file.name,
        file_size: file.size,
      }
      if (conflictStrategy) {
        initPayload.conflict_strategy = conflictStrategy
      }

      const initRes = await api.post<UploadInitResponse>('/file/upload', initPayload)

      // Backend may have renamed the file (conflict_strategy = 'rename')
      if (initRes.data.file_name && initRes.data.file_name !== file.name) {
        upload.fileName = initRes.data.file_name
      }

      upload.uploadId = initRes.data.upload_id
      upload.taskId = initRes.data.task_id || null
      saveInterruptedState(uploads.value)
      const chunkSize: number = initRes.data.chunk_size
      const totalParts: number = initRes.data.total_parts

      const storageKey = await runUploadLoop(upload, file, upload.uploadId, chunkSize, totalParts)

      if (upload.status === 'cancelled') return

      await completeUpload(upload, storageKey)

    } catch (e: unknown) {
      if (upload.status !== 'cancelled') {
        upload.status = 'failed'
        const err = e as { response?: { data?: { error?: string } }; message?: string }
        upload.error = err.response?.data?.error || err.message
        saveInterruptedState(uploads.value)
      }
      console.error('Upload failed:', e)
    }
  }

  async function runUploadLoop(upload: Upload, file: File, uploadId: string | null, chunkSize: number, totalParts: number, existingEtags: UploadPart[] = []): Promise<string> {
    const storageKey = `upload_${file.name}_${file.size}_${uploadId}`

    const partQueue: number[] = []
    for (let i = 1; i <= totalParts; i++) {
      if (existingEtags.find(p => p.partNumber === i)) continue
      partQueue.push(i)
    }

    upload.etags = [...existingEtags]

    // Accurate bytesUploaded: account for last chunk being smaller
    let bytesUploaded: number = 0
    for (const etag of existingEtags) {
      const start = (etag.partNumber - 1) * chunkSize
      const end = Math.min(start + chunkSize, file.size)
      bytesUploaded += (end - start)
    }
    upload.bytesUploaded = bytesUploaded
    upload.progress = totalParts > 0 ? Math.floor((upload.etags.length / totalParts) * 100) : 0
    // Anchor for ETA: only measure rate from progress gained in this session
    upload.startProgress = upload.progress
    upload.startTime = Date.now()

    for (const partNumber of partQueue) {
      // Handle pause: wait for resume signal
      if (upload.status === 'paused') {
        await new Promise<void>(resolve => { upload._resume = resolve })
        if ((upload.status as string) === 'cancelled') break
      }

      if ((upload.status as string) === 'cancelled') break

      const start: number = (partNumber - 1) * chunkSize
      const end: number = Math.min(start + chunkSize, file.size)
      const chunk: Blob = file.slice(start, end)

      const formData = new FormData()
      formData.append('upload_id', uploadId!)
      formData.append('part_number', String(partNumber))
      formData.append('chunk', chunk)

      const partStart: number = Date.now()
      const res = await api.put<UploadPartResponse>('/file/upload/part', formData, {
        headers: { 'Content-Type': 'multipart/form-data' },
        timeout: 300000,
      })

      const elapsed: number = (Date.now() - partStart) / 1000
      const chunkLen: number = end - start

      const partResult: UploadPart = { partNumber: res.data.part_number, etag: res.data.etag }
      upload.etags.push(partResult)

      // Skip UI updates if user paused while this chunk was in-flight
      if (upload.status !== 'paused') {
        upload.speed = Math.floor(chunkLen / elapsed)
        upload.bytesUploaded += chunkLen
        upload.progress = Math.floor((upload.etags.length / totalParts) * 100)

        // Report progress to server via WS
        if (upload.taskId) {
          ws.request('upload.progress', {
            task_id: upload.taskId,
            progress: upload.etags.length / totalParts,
          }).catch(() => {})
        }
      }

      localStorage.setItem(storageKey, JSON.stringify(upload.etags))
      saveInterruptedState(uploads.value)
    }

    return storageKey
  }

  async function completeUpload(upload: Upload, storageKey: string): Promise<void> {
    upload.etags.sort((a, b) => a.partNumber - b.partNumber)
    await api.post('/file/upload', {
      upload_id: upload.uploadId,
      task_id: upload.taskId || '',
      parts: upload.etags.map(p => ({ part_number: p.partNumber, etag: p.etag })),
    })

    // Chunks delivered — server will continue processing via the same job
    // The job.update WS push will drive status changes from here
    upload.status = 'processing'
    upload.progress = 100
    upload.speed = 0
    localStorage.removeItem(storageKey)
    saveInterruptedState(uploads.value)

    const fs = useFileSystemStore()
    fs.invalidateCache()
    fs.refresh()
  }

  function pauseUpload(id: string): void {
    const upload = uploads.value.find(u => u.id === id)
    if (upload && upload.status === 'uploading') upload.status = 'paused'
  }

  function resumeUpload(id: string): void {
    const upload = uploads.value.find(u => u.id === id)
    if (upload && upload.status === 'paused') {
      upload.status = 'uploading'
      // Reset ETA anchor so paused time doesn't skew the estimate
      upload.startTime = Date.now()
      upload.startProgress = upload.progress
      if (upload._resume) {
        upload._resume()
        upload._resume = null
      }
    }
  }

  async function cancelUpload(id: string): Promise<void> {
    const upload = uploads.value.find(u => u.id === id)
    if (!upload) return

    upload.status = 'cancelled'
    // Wake up paused loop so it can exit
    if (upload._resume) {
      upload._resume()
      upload._resume = null
    }
    if (upload.uploadId) {
      try {
        await api.delete('/file/upload', { params: { upload_id: upload.uploadId, task_id: upload.taskId || '' } })
      } catch { /* best-effort abort */ }
    }
  }

  async function resumeInterrupted(id: string, file?: File): Promise<void> {
    const u = uploads.value.find(u => u.id === id)
    if (!u || u.status !== 'interrupted') return

    // Use stored File object if available (same session), otherwise use the provided one
    const resumeFile: File | null = u._file || file || null
    if (!resumeFile) return

    // Validate: file must match the original upload
    if (resumeFile.name !== u.fileName || resumeFile.size !== u.fileSize) {
      u.error = t('upload.file_mismatch', { name: u.fileName })
      return
    }

    try {
      const res = await api.get<UploadStatusResponse>('/file/upload', { params: { upload_id: u.uploadId } })
      const { chunk_size, status, parts } = res.data

      const serverStatus: string = status === 'active' ? 'uploading' : status
      if (serverStatus === 'processing' || serverStatus === 'ready') {
        uploads.value = uploads.value.filter(x => x.id !== id)
        if (uploads.value.length === 0) showPanel.value = false
        saveInterruptedState(uploads.value)
        return
      }

      if (serverStatus !== 'uploading') {
        const targetPath = u.targetPath
        uploads.value = uploads.value.filter(x => x.id !== id)
        startUpload(resumeFile, targetPath)
        return
      }

      const totalParts: number = Math.ceil(resumeFile.size / chunk_size)
      const existingEtags: UploadPart[] = (parts || []).map(p => ({ partNumber: p.part_number, etag: p.etag }))

      u.status = 'uploading'
      u.startTime = Date.now()
      u._file = resumeFile
      u._resume = null
      u.error = undefined

      const storageKey = await runUploadLoop(u, resumeFile, u.uploadId, chunk_size, totalParts, existingEtags)

      if ((u.status as string) === 'cancelled') return

      await completeUpload(u, storageKey)

    } catch (e: unknown) {
      const err = e as { response?: { status?: number; data?: { error?: string } }; message?: string }
      if (err.response?.status === 404) {
        const targetPath = u.targetPath
        uploads.value = uploads.value.filter(x => x.id !== id)
        startUpload(resumeFile, targetPath)
      } else if ((u.status as string) !== 'cancelled') {
        u.status = 'failed'
        u.error = err.response?.data?.error || err.message
        saveInterruptedState(uploads.value)
      }
    }
  }

  function dismissInterrupted(id: string): void {
    const u = uploads.value.find(u => u.id === id)
    if (!u || u.status !== 'interrupted') return
    if (u.uploadId) {
      api.delete('/file/upload', { params: { upload_id: u.uploadId, task_id: u.taskId || '' } }).catch(() => { /* best-effort */ })
    }
    uploads.value = uploads.value.filter(x => x.id !== id)
    if (uploads.value.length === 0) showPanel.value = false
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
    checkInterrupted,
    resumeInterrupted,
    dismissInterrupted,
  }
})
