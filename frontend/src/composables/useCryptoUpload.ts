/**
 * useCryptoUpload — client-side encryption utilities for direct OSS upload.
 *
 * Provides: streaming SHA-256 hash, DEK generation, thumbnail generation,
 * text extraction, and blob encryption.
 *
 * Encryption wire format is identical to the server-side Go implementation
 * (internal/auth/crypto.go) and compatible with sw.js decryption:
 *   [1 byte version: 0x01]
 *   [4 bytes chunk_size big-endian]
 *   [chunk 0: 12-byte nonce | ciphertext+16-byte tag]
 *   [chunk 1: …]
 */

import { createSHA256 } from 'hash-wasm'
import pdfWorkerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { decryptBlob as decryptWireBlob, encryptBlob as encryptWireBlob, keyFromHex, keyToHex } from '@willvar/escrow-vue'

const CRYPTO_VERSION = 0x01
const CHUNK_SIZE = 65536 // 64 KB — must match auth.DefaultChunkSize
const NONCE_SIZE = 12
const TAG_SIZE = 16

// ── SHA-256 streaming hash ──────────────────────────────────────────────────

/** Compute SHA-256 hex digest of a File using streaming (constant memory). */
export async function hashFile(file: File): Promise<string> {
  const hasher = await createSHA256()
  hasher.init()
  const reader = file.stream().getReader()
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    hasher.update(value)
  }
  return hasher.digest('hex')
}

/** Compute SHA-256 hex digest of the first `maxBytes` of a File. */
export async function hashFilePrefix(file: File, maxBytes = 4 * 1024 * 1024): Promise<string> {
  const hasher = await createSHA256()
  hasher.init()
  const chunk = new Uint8Array(await file.slice(0, Math.max(0, maxBytes)).arrayBuffer())
  hasher.update(chunk)
  return hasher.digest('hex')
}

// ── DEK generation ──────────────────────────────────────────────────────────

export interface DEKBundle {
  key: CryptoKey
  raw: Uint8Array
  hex: string
}

/** Generate a random 256-bit AES-GCM Data Encryption Key. */
export async function generateDEK(): Promise<DEKBundle> {
  const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, true, ['encrypt'])
  const raw = new Uint8Array(await crypto.subtle.exportKey('raw', key))
  const hex = bytesToHex(raw)
  return { key, raw, hex }
}

// ── Encrypt blob (small, in-memory — for thumbnails etc.) ───────────────────

/** Wire-format codec delegated to @willvar/escrow-vue (DOFS v1). */
export function encryptBlob(dekRaw: Uint8Array, data: ArrayBuffer): Promise<Uint8Array> {
  return encryptWireBlob(dekRaw, data)
}

// ── Thumbnail generation (canvas API) ───────────────────────────────────────

export interface ThumbnailResult {
  blob: Blob
  width: number
  height: number
  duration?: number
}

const THUMB_MAX_DIMENSION = 480
const PDF_THUMB_MAX_BYTES = 64 * 1024 * 1024
const VIDEO_EXTENSIONS = new Set(['mp4', 'webm', 'mov', 'm4v'])

function isVideoFile(file: File): boolean {
  if (file.type.startsWith('video/')) return true
  const dot = file.name.lastIndexOf('.')
  if (dot <= 0) return false
  return VIDEO_EXTENSIONS.has(file.name.substring(dot + 1).toLowerCase())
}

function isPDFFile(file: File): boolean {
  return file.type === 'application/pdf' || file.name.toLowerCase().endsWith('.pdf')
}

function thumbnailScale(width: number, height: number): number {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    throw new Error('Invalid media dimensions')
  }
  return Math.min(1, THUMB_MAX_DIMENSION / width, THUMB_MAX_DIMENSION / height)
}

let thumbnailQueue: Promise<unknown> = Promise.resolve()

const THUMB_TIMEOUT_MS = 15000

export function generateThumbnail(file: File, signal?: AbortSignal): Promise<ThumbnailResult | null> {
  const run = async (): Promise<ThumbnailResult | null> => {
    signal?.throwIfAborted()
    const control = new AbortController()
    const abort = () => control.abort(signal?.reason)
    signal?.addEventListener('abort', abort, { once: true })
    const timer = setTimeout(() => control.abort(new Error('Thumbnail timed out')), THUMB_TIMEOUT_MS)
    try { return await generateThumbnailNow(file, control.signal) }
    finally { clearTimeout(timer); signal?.removeEventListener('abort', abort) }
  }
  // A timed-out decoder must actually finish cleanup before the next starts.
  const result = thumbnailQueue.then(run, run)
  thumbnailQueue = result.catch(() => {})
  return result
}

async function generateThumbnailNow(file: File, signal: AbortSignal): Promise<ThumbnailResult | null> {
  try {
    if (file.type.startsWith('image/')) {
      return await generateImageThumbnail(file, signal)
    }
    if (isVideoFile(file)) {
      return await generateVideoThumbnail(file, signal)
    }
    if (isPDFFile(file) && file.size <= PDF_THUMB_MAX_BYTES) {
      return await generatePDFThumbnail(file, signal)
    }
  } catch (error) {
    // A malformed or browser-unsupported media file must never prevent the
    // original encrypted upload. The file list falls back to its type icon.
    console.warn('Browser thumbnail generation failed:', error)
  }
  return null
}

function mediaEvent(target: EventTarget, name: string, signal: AbortSignal, start: () => void): Promise<void> {
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      target.removeEventListener(name, done)
      target.removeEventListener('error', failed)
      signal.removeEventListener('abort', aborted)
    }
    const done = () => { cleanup(); resolve() }
    const failed = () => { cleanup(); reject(new Error('Media thumbnail decode failed')) }
    const aborted = () => { cleanup(); reject(signal.reason) }
    target.addEventListener(name, done, { once: true })
    target.addEventListener('error', failed, { once: true })
    signal.addEventListener('abort', aborted, { once: true })
    try { start() } catch (error) { cleanup(); reject(error) }
  })
}

async function generateImageThumbnail(file: File, signal: AbortSignal): Promise<ThumbnailResult> {
  const img = new Image()
  const url = URL.createObjectURL(file)
  const canvas = document.createElement('canvas')
  try {
    await mediaEvent(img, 'load', signal, () => { img.src = url })
    const scale = thumbnailScale(img.naturalWidth, img.naturalHeight)
    const w = Math.round(img.naturalWidth * scale)
    const h = Math.round(img.naturalHeight * scale)
    canvas.width = w
    canvas.height = h
    canvas.getContext('2d')!.drawImage(img, 0, 0, w, h)
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('toBlob failed'))), 'image/webp', 0.8)
    })
    return { blob, width: img.naturalWidth, height: img.naturalHeight }
  } finally {
    img.removeAttribute('src')
    canvas.width = canvas.height = 0
    URL.revokeObjectURL(url)
  }
}

async function generateVideoThumbnail(file: File, signal: AbortSignal): Promise<ThumbnailResult> {
  const video = document.createElement('video')
  const url = URL.createObjectURL(file)
  const canvas = document.createElement('canvas')
  try {
    video.muted = true
    video.preload = 'metadata'
    await mediaEvent(video, 'loadedmetadata', signal, () => { video.src = url })
    // Seek to 1s or half duration, whichever is smaller. Some iOS codecs never
    // fire `seeked` — bail out instead of hanging the pipeline forever.
    await mediaEvent(video, 'seeked', signal, () => { video.currentTime = Math.min(1, video.duration / 2) })

    const scale = thumbnailScale(video.videoWidth, video.videoHeight)
    const w = Math.round(video.videoWidth * scale)
    const h = Math.round(video.videoHeight * scale)
    canvas.width = w
    canvas.height = h
    canvas.getContext('2d')!.drawImage(video, 0, 0, w, h)
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('toBlob failed'))), 'image/webp', 0.8)
    })
    return { blob, width: video.videoWidth, height: video.videoHeight, duration: video.duration }
  } finally {
    video.pause()
    video.removeAttribute('src')
    video.load()
    canvas.width = canvas.height = 0
    URL.revokeObjectURL(url)
  }
}

async function generatePDFThumbnail(file: File, signal: AbortSignal): Promise<ThumbnailResult> {
  const pdfjs = await import('pdfjs-dist')
  signal.throwIfAborted()
  pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerURL
  const data = new Uint8Array(await file.arrayBuffer())
  signal.throwIfAborted()
  const loadingTask = pdfjs.getDocument({
    data,
  })
  let destroying: Promise<void> | undefined
  const destroy = () => (destroying ??= loadingTask.destroy())
  const abort = () => { void destroy().catch(() => {}) }
  signal.addEventListener('abort', abort, { once: true })
  const canvas = window.document.createElement('canvas')
  try {
    const pdfDocument = await loadingTask.promise
    const page = await pdfDocument.getPage(1)
    const naturalViewport = page.getViewport({ scale: 1 })
    const scale = thumbnailScale(naturalViewport.width, naturalViewport.height)
    const viewport = page.getViewport({ scale })
    canvas.width = Math.max(1, Math.round(viewport.width))
    canvas.height = Math.max(1, Math.round(viewport.height))
    const context = canvas.getContext('2d')
    if (!context) throw new Error('Canvas 2D context unavailable')
    await page.render({ canvas, canvasContext: context, viewport }).promise
    signal.throwIfAborted()
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((value) => (value ? resolve(value) : reject(new Error('toBlob failed'))), 'image/webp', 0.8)
    })
    return {
      blob,
      width: Math.round(naturalViewport.width),
      height: Math.round(naturalViewport.height),
    }
  } finally {
    signal.removeEventListener('abort', abort)
    canvas.width = canvas.height = 0
    await destroy()
  }
}

// ── Decrypt blob (reverse of encryptBlob) ───────────────────────────────────

/**
 * Decrypt an encrypted blob using the same chunked AES-GCM wire format.
 * Returns plaintext as ArrayBuffer.
 */
export async function decryptBlob(dekRaw: Uint8Array, encrypted: ArrayBuffer): Promise<ArrayBuffer> {
  const plain = await decryptWireBlob(dekRaw, new Uint8Array(encrypted))
  return plain.buffer as ArrayBuffer
}

// ── Small encrypted file read/write helpers ─────────────────────────────────

import api from './useApi'
import type { AxiosResponse } from 'axios'

interface FileAccessResponse {
  url: string
  dek: string
  size: number
  chunk_size: number
}

interface UploadInitResp {
  upload_id: string
  task_id: string
  oss_upload_id: string
  part_size: number
  total_parts: number
  file_name: string
  dek: string
}

interface PresignResp {
  parts: { part_number: number; presigned_url: string }[]
}

/**
 * Read a small encrypted file from OSS. Returns decrypted content as ArrayBuffer.
 * Throws if file does not exist. When optional is true, a missing file returns 204
 * instead of 404 to avoid browser console noise.
 */
export async function readEncryptedFile(path: string, optional = false): Promise<ArrayBuffer> {
  const res: AxiosResponse<FileAccessResponse> = await api.get('/file/access', {
    params: { path, ...(path.startsWith('/.user/') && { internal: 'true' }), ...(optional && { optional: 'true' }) },
  })
  if (res.status === 204) throw new Error('not found')
  const { url, dek } = res.data
  const resp = await fetch(url)
  if (!resp.ok) throw new Error(`fetch encrypted file failed: ${resp.status}`)
  const encrypted = await resp.arrayBuffer()
  return decryptBlob(hexToBytes(dek), encrypted)
}

/**
 * Write a small file to OSS via the encrypted upload pipeline.
 * Creates or replaces the file at the given path.
 */
export interface EncryptedWriteOptions {
  dek?: string
  expectedGeneration?: number
  shareId?: string
  /** Allow Domus to create missing parent directories for app-owned metadata. */
  internal?: boolean
}

export async function writeEncryptedFile(
  path: string,
  data: ArrayBuffer,
  contentType = 'application/octet-stream',
  options: EncryptedWriteOptions = {},
): Promise<number | undefined> {
  const dir = path.substring(0, path.lastIndexOf('/') + 1) || '/'
  const name = path.substring(path.lastIndexOf('/') + 1)
  const requestedDek = options.dek || (await generateDEK()).hex

  const initPayload: Record<string, unknown> = {
    path: dir,
    file_name: name,
    file_size: data.byteLength,
    content_type: contentType,
    conflict_strategy: 'replace',
    dek: requestedDek,
  }
  if (options.expectedGeneration) initPayload.expected_generation = options.expectedGeneration
  if (options.shareId) initPayload.share_id = options.shareId
  if (options.internal) initPayload.internal = true

  let uploadId = ''
  try {
    const initRes: AxiosResponse<UploadInitResp> = await api.post('/file/upload', initPayload)
    const init = initRes.data
    uploadId = init.upload_id
    const effectiveDek = init.dek || requestedDek
    const encrypted = await encryptBlob(hexToBytes(effectiveDek), data)

    const presignRes: AxiosResponse<PresignResp> = await api.get('/file/upload/presign', {
      params: { upload_id: init.upload_id, start: 1, count: 1 },
    })
    const presignedUrl = presignRes.data.parts[0]?.presigned_url
    if (!presignedUrl) throw new Error('Upload URL was not issued')

    const putResp = await fetch(presignedUrl, { method: 'PUT', body: encrypted.slice().buffer })
    if (!putResp.ok) throw new Error(`PUT to OSS failed: ${putResp.status}`)

    const completeRes = await api.post<{ generation?: number }>('/file/upload', {
      upload_id: init.upload_id,
      encrypted_size: encrypted.byteLength,
    })
    return completeRes.data.generation
  } catch (error) {
    if (uploadId) {
      await api.post('/file/upload/cancel', {
        upload_id: uploadId,
        reason: 'direct_write_failed',
        status: 'failed',
      }).catch(() => {})
    }
    throw error
  }
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function bytesToHex(bytes: Uint8Array): string {
  return keyToHex(bytes)
}

function hexToBytes(hex: string): Uint8Array {
  return keyFromHex(hex)
}
