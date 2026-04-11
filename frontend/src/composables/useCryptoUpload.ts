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

/**
 * Encrypt a Blob/ArrayBuffer using the same chunked AES-GCM wire format as
 * the server. Returns the encrypted bytes as Uint8Array.
 */
export async function encryptBlob(dekRaw: Uint8Array, data: ArrayBuffer): Promise<Uint8Array> {
  const key = await crypto.subtle.importKey('raw', dekRaw.buffer as ArrayBuffer, { name: 'AES-GCM' }, false, ['encrypt'])
  const plain = new Uint8Array(data)

  // Calculate output size
  const numChunks = Math.ceil(plain.length / CHUNK_SIZE) || 1
  const outputSize = 5 + plain.length + numChunks * (NONCE_SIZE + TAG_SIZE)
  const output = new Uint8Array(outputSize)

  // Header: [version][chunk_size BE]
  output[0] = CRYPTO_VERSION
  new DataView(output.buffer).setUint32(1, CHUNK_SIZE, false)

  let offset = 5
  for (let i = 0; i < numChunks; i++) {
    const start = i * CHUNK_SIZE
    const end = Math.min(start + CHUNK_SIZE, plain.length)
    const chunk = plain.subarray(start, end)

    const nonce = crypto.getRandomValues(new Uint8Array(NONCE_SIZE))
    const aad = uint64BE(i) as BufferSource
    const ciphertext = new Uint8Array(
      await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce, additionalData: aad }, key, chunk),
    )

    output.set(nonce, offset)
    offset += NONCE_SIZE
    output.set(ciphertext, offset)
    offset += ciphertext.length
  }

  return output.subarray(0, offset)
}

// ── Thumbnail generation (canvas API) ───────────────────────────────────────

export interface ThumbnailResult {
  blob: Blob
  width: number
  height: number
  duration?: number
}

const THUMB_MAX_WIDTH = 480

export async function generateThumbnail(file: File): Promise<ThumbnailResult | null> {
  if (file.type.startsWith('image/')) {
    return generateImageThumbnail(file)
  }
  if (file.type.startsWith('video/')) {
    return generateVideoThumbnail(file)
  }
  return null
}

async function generateImageThumbnail(file: File): Promise<ThumbnailResult> {
  const img = new Image()
  const url = URL.createObjectURL(file)
  try {
    img.src = url
    await img.decode()
    const scale = Math.min(1, THUMB_MAX_WIDTH / img.naturalWidth)
    const w = Math.round(img.naturalWidth * scale)
    const h = Math.round(img.naturalHeight * scale)
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    canvas.getContext('2d')!.drawImage(img, 0, 0, w, h)
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('toBlob failed'))), 'image/webp', 0.8)
    })
    return { blob, width: img.naturalWidth, height: img.naturalHeight }
  } finally {
    URL.revokeObjectURL(url)
  }
}

async function generateVideoThumbnail(file: File): Promise<ThumbnailResult> {
  const video = document.createElement('video')
  const url = URL.createObjectURL(file)
  try {
    video.src = url
    video.muted = true
    video.preload = 'metadata'
    await new Promise<void>((resolve, reject) => {
      video.onloadedmetadata = () => resolve()
      video.onerror = () => reject(new Error('video metadata load failed'))
    })
    // Seek to 1s or half duration, whichever is smaller
    video.currentTime = Math.min(1, video.duration / 2)
    await new Promise<void>((resolve) => {
      video.onseeked = () => resolve()
    })

    const scale = Math.min(1, THUMB_MAX_WIDTH / video.videoWidth)
    const w = Math.round(video.videoWidth * scale)
    const h = Math.round(video.videoHeight * scale)
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    canvas.getContext('2d')!.drawImage(video, 0, 0, w, h)
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('toBlob failed'))), 'image/webp', 0.8)
    })
    return { blob, width: video.videoWidth, height: video.videoHeight, duration: video.duration }
  } finally {
    URL.revokeObjectURL(url)
  }
}

// ── Text extraction ─────────────────────────────────────────────────────────

const TEXT_EXTENSIONS = new Set([
  '.txt', '.md', '.json', '.yaml', '.yml', '.toml', '.xml', '.csv', '.log',
  '.conf', '.ini', '.sh', '.bash', '.zsh', '.fish', '.go', '.py', '.js',
  '.ts', '.jsx', '.tsx', '.vue', '.html', '.htm', '.css', '.scss', '.less',
  '.sql', '.rs', '.c', '.cpp', '.h', '.hpp', '.java', '.kt', '.rb', '.php',
  '.pl', '.lua', '.r', '.swift', '.dart', '.ex', '.exs', '.env', '.gitignore',
  '.dockerignore', '.editorconfig', '.makefile', '.cmake', '.gradle', '.properties',
])

export function isTextFile(fileName: string): boolean {
  const dot = fileName.lastIndexOf('.')
  if (dot <= 0) return false
  return TEXT_EXTENSIONS.has(fileName.substring(dot).toLowerCase())
}

/** Extract the first `maxBytes` of a text file's content as a string. */
export async function extractSearchText(file: File, maxBytes = 100 * 1024): Promise<string | null> {
  if (!isTextFile(file.name)) return null
  const slice = file.slice(0, maxBytes)
  return slice.text()
}

// ── Decrypt blob (reverse of encryptBlob) ───────────────────────────────────

/**
 * Decrypt an encrypted blob using the same chunked AES-GCM wire format.
 * Returns plaintext as ArrayBuffer.
 */
export async function decryptBlob(dekRaw: Uint8Array, encrypted: ArrayBuffer): Promise<ArrayBuffer> {
  const data = new Uint8Array(encrypted)
  if (data.length < 5 || data[0] !== CRYPTO_VERSION) {
    throw new Error('Invalid encrypted data')
  }
  const chunkSize = new DataView(data.buffer).getUint32(1, false)
  if (chunkSize !== CHUNK_SIZE) {
    throw new Error(`Unexpected chunk size: expected ${CHUNK_SIZE}, got ${chunkSize}`)
  }
  const key = await crypto.subtle.importKey('raw', dekRaw.buffer as ArrayBuffer, { name: 'AES-GCM' }, false, ['decrypt'])

  const chunks: Uint8Array[] = []
  let offset = 5
  let idx = 0
  while (offset < data.length) {
    const nonce = data.subarray(offset, offset + NONCE_SIZE)
    offset += NONCE_SIZE
    // Remaining encrypted chunk: ciphertext + tag. Last chunk may be smaller.
    const encChunkMax = chunkSize + TAG_SIZE
    const remaining = data.length - offset
    const encChunkLen = Math.min(encChunkMax, remaining)
    const ciphertext = data.subarray(offset, offset + encChunkLen)
    offset += encChunkLen
    const aad = uint64BE(idx) as BufferSource
    const plainChunk = new Uint8Array(
      await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonce, additionalData: aad }, key, ciphertext.slice().buffer),
    )
    chunks.push(plainChunk)
    idx++
  }

  const totalLen = chunks.reduce((s, c) => s + c.length, 0)
  const result = new Uint8Array(totalLen)
  let pos = 0
  for (const c of chunks) {
    result.set(c, pos)
    pos += c.length
  }
  return result.buffer
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
    params: { path, ...(optional && { optional: 'true' }) },
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
export async function writeEncryptedFile(path: string, data: ArrayBuffer, contentType = 'application/octet-stream'): Promise<void> {
  const dir = path.substring(0, path.lastIndexOf('/') + 1) || '/'
  const name = path.substring(path.lastIndexOf('/') + 1)

  const dek = await generateDEK()
  const encrypted = await encryptBlob(dek.raw, data)

  const initRes: AxiosResponse<UploadInitResp> = await api.post('/file/upload', {
    path: dir,
    file_name: name,
    file_size: data.byteLength,
    content_type: contentType,
    conflict_strategy: 'replace',
  })
  const init = initRes.data

  const presignRes: AxiosResponse<PresignResp> = await api.get('/file/upload/presign', {
    params: { upload_id: init.upload_id, start: 1, count: 1 },
  })
  const presignedUrl = presignRes.data.parts[0].presigned_url

  const putResp = await fetch(presignedUrl, { method: 'PUT', body: encrypted.slice().buffer })
  if (!putResp.ok) throw new Error(`PUT to OSS failed: ${putResp.status}`)
  const etag = putResp.headers.get('ETag') || ''

  await api.post('/file/upload', {
    upload_id: init.upload_id,
    dek: dek.hex,
    parts: [{ part_number: 1, etag }],
  })
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

export function hexToBytes(hex: string): Uint8Array {
  const bytes = new Uint8Array(hex.length / 2)
  for (let i = 0; i < hex.length; i += 2) {
    bytes[i / 2] = parseInt(hex.substring(i, i + 2), 16)
  }
  return bytes
}

function uint64BE(n: number): Uint8Array {
  const buf = new Uint8Array(8)
  new DataView(buf.buffer).setBigUint64(0, BigInt(n), false)
  return buf
}

export { CHUNK_SIZE, NONCE_SIZE, TAG_SIZE, CRYPTO_VERSION }
