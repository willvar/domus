/**
 * useCryptoUpload — client-side encryption utilities for direct OSS upload.
 *
 * Provides streaming SHA-256, DEK generation, and blob encryption.
 *
 * Encryption wire format is identical to the server-side Go implementation
 * (internal/auth/crypto.go) and compatible with sw.js decryption:
 *   [1 byte version: 0x01]
 *   [4 bytes chunk_size big-endian]
 *   [chunk 0: 12-byte nonce | ciphertext+16-byte tag]
 *   [chunk 1: …]
 */

import { createSHA256 } from 'hash-wasm'
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
