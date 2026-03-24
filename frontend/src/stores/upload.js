import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { showConfirm, showDuplicateDialog } from '../composables/useNativeDialog'
import { useFileSystemStore } from './fileSystem'

const INTERRUPTED_KEY = 'zephyr_interrupted_uploads'

function saveInterruptedState(uploads) {
  const active = uploads.filter(u => u.status === 'uploading' || u.status === 'paused')
  if (active.length === 0) {
    localStorage.removeItem(INTERRUPTED_KEY)
    return
  }
  const data = active.map(u => ({
    fileName: u.fileName,
    fileSize: u.fileSize,
    uploadId: u.uploadId,
    progress: u.progress,
    targetPath: u.targetPath,
    etags: u.etags,
  }))
  localStorage.setItem(INTERRUPTED_KEY, JSON.stringify(data))
}

function loadInterruptedState() {
  const raw = localStorage.getItem(INTERRUPTED_KEY)
  if (!raw) return []
  try { return JSON.parse(raw) } catch { return [] }
}

export const useUploadStore = defineStore('upload', () => {
  const { t } = useI18n()
  const uploads = ref([])
  const showPanel = ref(false)

  const activeUploads = computed(() => uploads.value.filter(u => u.status === 'uploading' || u.status === 'paused'))
  const hasActive = computed(() => activeUploads.value.length > 0)

  function checkInterrupted() {
    const interrupted = loadInterruptedState()
    if (interrupted.length === 0) return
    localStorage.removeItem(INTERRUPTED_KEY)
    for (const item of interrupted) {
      uploads.value.push({
        id: crypto.randomUUID(),
        fileName: item.fileName,
        fileSize: item.fileSize,
        progress: item.progress,
        speed: 0,
        status: 'interrupted',
        uploadId: item.uploadId,
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

  async function uploadFiles(fileList, targetPath) {
    const allFiles = Array.from(fileList)
    if (allFiles.length === 0) return

    // Check conflicts via backend
    const names = allFiles.map(f => f.name)
    let conflictMap = new Map()
    try {
      const res = await api.post('/upload/check-conflicts', { path: targetPath, names })
      for (const c of res.data.conflicts || []) {
        conflictMap.set(c.name, c)
      }
    } catch {
      // If check fails, proceed without conflict detection (fallback to backend 409)
    }

    const files = []
    const strategies = new Map() // file name -> conflict_strategy
    let duplicateDecision = null

    for (const file of allFiles) {
      if (conflictMap.has(file.name)) {
        let action = duplicateDecision?.action
        if (!action) {
          const existing = conflictMap.get(file.name)
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

    const zeroByteFiles = files.filter(file => file.size === 0)

    if (zeroByteFiles.length > 0) {
      const fileListText = zeroByteFiles.map(file => `- ${file.name}`).join('\n')
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

  async function startUpload(file, targetPath, allowZeroByte = false, conflictStrategy = null) {
    const entry = {
      id: crypto.randomUUID(),
      fileName: file.name,
      fileSize: file.size,
      progress: 0,
      speed: 0,
      status: 'uploading',
      uploadId: null,
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
    const upload = uploads.value[uploads.value.length - 1]

    try {
      if (!file.size && !allowZeroByte) {
        upload.status = 'failed'
        upload.error = t('upload.zero_byte_error')
        return
      }

      const initPayload = {
        path: targetPath,
        file_name: file.name,
        file_size: file.size,
      }
      if (conflictStrategy) {
        initPayload.conflict_strategy = conflictStrategy
      }

      const initRes = await api.post('/upload/init', initPayload)

      // Backend may have renamed the file (conflict_strategy = 'rename')
      if (initRes.data.file_name && initRes.data.file_name !== file.name) {
        upload.fileName = initRes.data.file_name
      }

      upload.uploadId = initRes.data.upload_id
      saveInterruptedState(uploads.value)
      const chunkSize = initRes.data.chunk_size
      const totalParts = initRes.data.total_parts

      const storageKey = await runUploadLoop(upload, file, upload.uploadId, chunkSize, totalParts)

      if (upload.status === 'cancelled') return

      await completeUpload(upload, storageKey)

    } catch (e) {
      if (upload.status !== 'cancelled') {
        upload.status = 'failed'
        upload.error = e.response?.data?.error || e.message
        saveInterruptedState(uploads.value)
      }
      console.error('Upload failed:', e)
    }
  }

  async function runUploadLoop(upload, file, uploadId, chunkSize, totalParts, existingEtags = []) {
    const storageKey = `upload_${file.name}_${file.size}_${uploadId}`

    const partQueue = []
    for (let i = 1; i <= totalParts; i++) {
      if (existingEtags.find(p => p.partNumber === i)) continue
      partQueue.push(i)
    }

    upload.etags = [...existingEtags]

    // Accurate bytesUploaded: account for last chunk being smaller
    let bytesUploaded = 0
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
        await new Promise(resolve => { upload._resume = resolve })
        if (upload.status === 'cancelled') break
      }

      if (upload.status === 'cancelled') break

      const start = (partNumber - 1) * chunkSize
      const end = Math.min(start + chunkSize, file.size)
      const chunk = file.slice(start, end)

      const formData = new FormData()
      formData.append('upload_id', uploadId)
      formData.append('part_number', String(partNumber))
      formData.append('chunk', chunk)

      const partStart = Date.now()
      const res = await api.put('/upload/part', formData, {
        headers: { 'Content-Type': 'multipart/form-data' },
        timeout: 300000,
      })

      const elapsed = (Date.now() - partStart) / 1000
      const chunkLen = end - start

      const partResult = { partNumber: res.data.part_number, etag: res.data.etag }
      upload.etags.push(partResult)

      // Skip UI updates if user paused while this chunk was in-flight
      if (upload.status !== 'paused') {
        upload.speed = Math.floor(chunkLen / elapsed)
        upload.bytesUploaded += chunkLen
        upload.progress = Math.floor((upload.etags.length / totalParts) * 100)
      }

      localStorage.setItem(storageKey, JSON.stringify(upload.etags))
      saveInterruptedState(uploads.value)
    }

    return storageKey
  }

  async function completeUpload(upload, storageKey) {
    upload.etags.sort((a, b) => a.partNumber - b.partNumber)
    await api.post('/upload/complete', {
      upload_id: upload.uploadId,
      parts: upload.etags.map(p => ({ part_number: p.partNumber, etag: p.etag })),
    })

    upload.status = 'completed'
    upload.progress = 100
    localStorage.removeItem(storageKey)
    saveInterruptedState(uploads.value)

    const fs = useFileSystemStore()
    fs.invalidateCache()
    fs.refresh()

    setTimeout(() => {
      if (!hasActive.value) showPanel.value = false
    }, 3000)
  }

  function pauseUpload(id) {
    const upload = uploads.value.find(u => u.id === id)
    if (upload && upload.status === 'uploading') upload.status = 'paused'
  }

  function resumeUpload(id) {
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

  async function cancelUpload(id) {
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
        await api.delete('/upload/abort', { params: { upload_id: upload.uploadId } })
      } catch { /* best-effort abort */ }
    }
  }

  async function resumeInterrupted(id, file) {
    const u = uploads.value.find(u => u.id === id)
    if (!u || u.status !== 'interrupted') return

    // Use stored File object if available (same session), otherwise use the provided one
    const resumeFile = u._file || file
    if (!resumeFile) return

    // Validate: file must match the original upload
    if (resumeFile.name !== u.fileName || resumeFile.size !== u.fileSize) {
      u.error = t('upload.file_mismatch', { name: u.fileName })
      return
    }

    try {
      const res = await api.get('/upload/status', { params: { upload_id: u.uploadId } })
      const { chunk_size, status, parts } = res.data

      if (status !== 'active') {
        const targetPath = u.targetPath
        uploads.value = uploads.value.filter(x => x.id !== id)
        startUpload(resumeFile, targetPath)
        return
      }

      const totalParts = Math.ceil(resumeFile.size / chunk_size)
      const existingEtags = (parts || []).map(p => ({ partNumber: p.part_number, etag: p.etag }))

      u.status = 'uploading'
      u.startTime = Date.now()
      u._file = resumeFile
      u._resume = null
      u.error = null

      const storageKey = await runUploadLoop(u, resumeFile, u.uploadId, chunk_size, totalParts, existingEtags)

      if (u.status === 'cancelled') return

      await completeUpload(u, storageKey)

    } catch (e) {
      if (e.response?.status === 404) {
        const targetPath = u.targetPath
        uploads.value = uploads.value.filter(x => x.id !== id)
        startUpload(resumeFile, targetPath)
      } else if (u.status !== 'cancelled') {
        u.status = 'failed'
        u.error = e.response?.data?.error || e.message
        saveInterruptedState(uploads.value)
      }
    }
  }

  function dismissInterrupted(id) {
    const u = uploads.value.find(u => u.id === id)
    if (!u || u.status !== 'interrupted') return
    if (u.uploadId) {
      api.delete('/upload/abort', { params: { upload_id: u.uploadId } }).catch(() => { /* best-effort */ })
    }
    uploads.value = uploads.value.filter(x => x.id !== id)
    if (uploads.value.length === 0) showPanel.value = false
  }

  function retryUpload(id) {
    const u = uploads.value.find(u => u.id === id)
    if (!u || !u._file) return
    const file = u._file
    const targetPath = u.targetPath
    uploads.value = uploads.value.filter(x => x.id !== id)
    startUpload(file, targetPath)
  }

  function removeCompleted() {
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
