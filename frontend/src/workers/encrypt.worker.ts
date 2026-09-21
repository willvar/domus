/**
 * encrypt.worker.ts — Web Worker that encrypts a file and uploads parts
 * to OSS via presigned URLs.
 *
 * Communication protocol (postMessage):
 *
 * Main → Worker:
 *   { type: 'start', file: File, dekRaw: ArrayBuffer, partUrls: string[], partSize: number, totalParts: number }
 *   { type: 'add-urls', partUrls: string[] }  // append more presigned URLs
 *   { type: 'pause' }
 *   { type: 'resume' }
 *   { type: 'cancel' }
 *
 * Worker → Main:
 *   { type: 'progress', uploaded: number, total: number }
 *   { type: 'part', partNumber: number, size: number }
 *   { type: 'done', contentHash: string }
 *   { type: 'error', message: string }
 *   { type: 'need-urls', from: number, count: number }  // request more presigned URLs
 */

import { WIRE_HEADER_SIZE, WireEncoder } from '@willvar/escrow-vue'

const CHUNK_SIZE = 65536 // 64 KB plaintext per encryption chunk
const NONCE_SIZE = 12
const TAG_SIZE = 16
const ENC_CHUNK_SIZE = NONCE_SIZE + CHUNK_SIZE + TAG_SIZE // 65564

let cancelled = false
let paused = false
let resumeResolve: (() => void) | null = null
let urlQueue: string[] = []
let preferXHR = false

const PUT_TIMEOUT_MS = 120000
const PUT_MAX_ATTEMPTS = 5
const PUT_RETRY_BACKOFF_MS = 3000

async function putPart(url: string, data: Uint8Array<ArrayBuffer>, onProgress?: (loaded: number) => void): Promise<void> {
  if (!preferXHR) {
    try {
      const resp = await fetch(url, { method: 'PUT', body: data })
      if (!resp.ok) {
        throw new Error(`Upload part failed: ${resp.status}`)
      }
      onProgress?.(data.length)
      return
    } catch (err) {
      // fetch() rejecting at the network layer (TypeError on iOS: "Load
      // failed") falls back to XHR; HTTP status errors are real failures.
      if (!(err instanceof TypeError)) throw err
      preferXHR = true
    }
  }
  await xhrPutWithRetry(url, data, onProgress)
}

function xhrPut(url: string, data: Uint8Array<ArrayBuffer>, onProgress?: (loaded: number) => void): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', url, true)
    xhr.timeout = PUT_TIMEOUT_MS
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve()
      else reject(new HttpPartError(`Upload part failed: ${xhr.status}`))
    }
    xhr.onerror = () => reject(new Error('Upload part failed: network error'))
    xhr.ontimeout = () => reject(new Error(`Upload part timed out after ${PUT_TIMEOUT_MS / 1000}s`))
    if (onProgress) {
      xhr.upload.onprogress = (e) => onProgress(e.loaded)
    }
    xhr.send(data)
  })
}

async function xhrPutWithRetry(url: string, data: Uint8Array<ArrayBuffer>, onProgress?: (loaded: number) => void): Promise<void> {
  let lastError: Error = new Error('Upload part failed')
  for (let attempt = 1; attempt <= PUT_MAX_ATTEMPTS; attempt++) {
    try {
      await xhrPut(url, data, onProgress)
      return
    } catch (err) {
      lastError = err instanceof Error ? err : new Error(String(err))
      if (err instanceof HttpPartError) throw lastError
      if (attempt < PUT_MAX_ATTEMPTS && !cancelled) {
        await new Promise((resolve) => setTimeout(resolve, PUT_RETRY_BACKOFF_MS * attempt))
      }
    }
  }
  throw lastError
}

class HttpPartError extends Error {}

self.onmessage = async (e: MessageEvent) => {
  const msg = e.data
  switch (msg.type) {
    case 'start':
      cancelled = false
      paused = false
      urlQueue = msg.partUrls || []
      try {
        await encryptAndUpload(
          msg.file as File,
          msg.dekRaw as ArrayBuffer,
          msg.partSize as number,
          msg.totalParts as number,
        )
      } catch (err: any) {
        if (!cancelled) {
          self.postMessage({ type: 'error', message: err.message || String(err) })
        }
      }
      break
    case 'add-urls':
      urlQueue.push(...(msg.partUrls || []))
      break
    case 'pause':
      paused = true
      break
    case 'resume':
      paused = false
      if (resumeResolve) {
        resumeResolve()
        resumeResolve = null
      }
      break
    case 'cancel':
      cancelled = true
      paused = false
      if (resumeResolve) {
        resumeResolve()
        resumeResolve = null
      }
      break
  }
}

async function waitIfPaused(): Promise<void> {
  if (!paused) return
  return new Promise((resolve) => {
    resumeResolve = resolve
  })
}

async function encryptAndUpload(
  file: File,
  dekRaw: ArrayBuffer,
  partSize: number,
  totalParts: number,
) {
  const { createSHA256 } = await import('hash-wasm')
  const hasher = await createSHA256()
  hasher.init()

  const encoder = await WireEncoder.create(new Uint8Array(dekRaw as ArrayBuffer))

  const totalPlain = file.size
  const numEncChunks = Math.ceil(totalPlain / CHUNK_SIZE)
  const encryptedSize = 5 + totalPlain + numEncChunks * (NONCE_SIZE + TAG_SIZE)
  if (!Number.isSafeInteger(totalParts) || totalParts < 1) {
    throw new Error(`Invalid multipart geometry: ${totalParts}`)
  }

  let completedPartCount = 0
  let partNumber = 1
  let partBuffer = new Uint8Array(partSize)
  let partOffset = 0
  let uploaded = 0

  // Write 5-byte header into partBuffer
  partBuffer.set(WireEncoder.header(), 0)
  partOffset = WIRE_HEADER_SIZE

  // Stream-read the file
  const reader = file.stream().getReader()
  let leftover: Uint8Array<ArrayBuffer> = new Uint8Array(0)

  // Windowed encryption: WebCrypto encrypts off-thread, so keeping a window of
  // in-flight encryptChunk calls overlaps crypto with reading/hashing. The
  // escrow WireEncoder reserves its AAD ordinal synchronously, and segments
  // are written in ordinal order below.
  const ENCRYPT_WINDOW = 6
  const inflight = new Map<number, Promise<Uint8Array>>()
  let nextEncryptIdx = 0
  let nextWriteIdx = 0
  let sourceDone = false

  async function writeSegment(segment: Uint8Array): Promise<void> {
    const encChunkLen = segment.length
    if (partOffset + encChunkLen > partSize) {
      // Flush current part first
      await flushPart()
      partBuffer = new Uint8Array(partSize)
    }
    partBuffer.set(segment, partOffset)
    partOffset += encChunkLen
  }

  async function flushPart() {
    if (cancelled) return
    await waitIfPaused()
    if (cancelled) return

    const data = partBuffer.slice(0, partOffset)

    // Get presigned URL — request more if needed
    while (urlQueue.length === 0 && !cancelled) {
      const count = Math.min(100, totalParts - partNumber + 1)
      if (count <= 0) {
        throw new Error(`No presigned URLs available for part ${partNumber}`)
      }
      self.postMessage({ type: 'need-urls', from: partNumber, count })
      await new Promise<void>((resolve) => {
        const handler = (ev: MessageEvent) => {
          if (ev.data.type === 'add-urls' || ev.data.type === 'cancel') {
            if (ev.data.type === 'cancel') {
              cancelled = true
            }
            self.removeEventListener('message', handler)
            resolve()
          }
        }
        self.addEventListener('message', handler)
      })
    }
    if (cancelled) return

    const url = urlQueue.shift()!
    await putPart(url, data, (loaded) => {
      self.postMessage({ type: 'progress', uploaded: uploaded + loaded, total: encryptedSize })
    })
    completedPartCount++
    uploaded += data.length
    self.postMessage({ type: 'part', partNumber, size: data.length })

    partNumber++
    partOffset = 0
  }

  while (!cancelled) {
    if (!sourceDone) {
      const { done, value } = await reader.read()
      if (done) {
        sourceDone = true
      } else {
        // Combine leftover with new data
        let plain: Uint8Array
        if (leftover.length > 0) {
          plain = new Uint8Array(leftover.length + value.length)
          plain.set(leftover)
          plain.set(value, leftover.length)
          leftover = new Uint8Array(0) as Uint8Array<ArrayBuffer>
        } else {
          plain = value
        }

        let pos = 0
        while (pos < plain.length) {
          if (cancelled) return

          const chunkEnd = Math.min(pos + CHUNK_SIZE, plain.length)
          const remaining = plain.length - pos

          // If this is not the last file read and we don't have a full chunk, save as leftover
          if (remaining < CHUNK_SIZE && chunkEnd === plain.length) {
            leftover = plain.subarray(pos) as Uint8Array<ArrayBuffer>
            break
          }

          const chunk = plain.subarray(pos, chunkEnd)
          pos = chunkEnd

          hasher.update(chunk)

          // Schedule this chunk's encryption; ordinals are reserved by the
          // encoder in call order, segments are written in ordinal order.
          inflight.set(nextEncryptIdx, encoder.encryptChunk(chunk))
          nextEncryptIdx++
        }
      }
    }

    // Drain one encrypted segment in ordinal order (bounded by the window
    // once the window fills, and drains fully at EOF).
    if (inflight.size >= ENCRYPT_WINDOW || (sourceDone && inflight.size > 0)) {
      const pending = inflight.get(nextWriteIdx)
      inflight.delete(nextWriteIdx)
      nextWriteIdx++
      if (pending) await writeSegment(await pending)
    }

    if (sourceDone && inflight.size === 0) break
  }

  // Process any leftover data (last incomplete encryption chunk)
  if (leftover.length > 0 && !cancelled) {
    hasher.update(leftover)
    const segment = await encoder.encryptChunk(leftover)
    await writeSegment(segment)
  }

  // Flush remaining data as last part
  if (partOffset > 0 && !cancelled) {
    await flushPart()
  }

  if (!cancelled) {
    if (completedPartCount !== totalParts) {
      throw new Error(`Multipart geometry mismatch: wrote ${completedPartCount}, expected ${totalParts}`)
    }
    const contentHash = hasher.digest('hex')
    self.postMessage({ type: 'done', contentHash })
  }
}
