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

const CRYPTO_VERSION = 0x01
const CHUNK_SIZE = 65536 // 64 KB plaintext per encryption chunk
const NONCE_SIZE = 12
const TAG_SIZE = 16
const ENC_CHUNK_SIZE = NONCE_SIZE + CHUNK_SIZE + TAG_SIZE // 65564

let cancelled = false
let paused = false
let resumeResolve: (() => void) | null = null
let urlQueue: string[] = []

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

function uint64BE(n: number): Uint8Array {
  const buf = new Uint8Array(8)
  new DataView(buf.buffer).setBigUint64(0, BigInt(n), false)
  return buf
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

  const key = await crypto.subtle.importKey('raw', dekRaw as ArrayBuffer, { name: 'AES-GCM' }, false, ['encrypt'])

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
  let encChunkIdx = 0
  let uploaded = 0

  // Write 5-byte header into partBuffer
  partBuffer[0] = CRYPTO_VERSION
  new DataView(partBuffer.buffer).setUint32(1, CHUNK_SIZE, false)
  partOffset = 5

  // Stream-read the file
  const reader = file.stream().getReader()
  let leftover: Uint8Array<ArrayBuffer> = new Uint8Array(0)

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
    const resp = await fetch(url, { method: 'PUT', body: data })
    if (!resp.ok) {
      throw new Error(`Upload part ${partNumber} failed: ${resp.status}`)
    }
    completedPartCount++
    uploaded += data.length
    self.postMessage({ type: 'part', partNumber, size: data.length })
    self.postMessage({ type: 'progress', uploaded, total: encryptedSize })

    partNumber++
    partOffset = 0
  }

  for (;;) {
    if (cancelled) return

    const { done, value } = await reader.read()
    if (done) break

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

      // Encrypt this chunk
      const nonce = crypto.getRandomValues(new Uint8Array(NONCE_SIZE))
      const aad = uint64BE(encChunkIdx) as BufferSource
      const ciphertext = new Uint8Array(
        await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce, additionalData: aad }, key, chunk.slice().buffer),
      )
      encChunkIdx++

      // Write nonce + ciphertext to part buffer
      const encChunkLen = NONCE_SIZE + ciphertext.length
      if (partOffset + encChunkLen > partSize) {
        // Flush current part first
        await flushPart()
        partBuffer = new Uint8Array(partSize)
      }
      partBuffer.set(nonce, partOffset)
      partOffset += NONCE_SIZE
      partBuffer.set(ciphertext, partOffset)
      partOffset += ciphertext.length
    }
  }

  // Process any leftover data (last incomplete encryption chunk)
  if (leftover.length > 0 && !cancelled) {
    hasher.update(leftover)
    const nonce = crypto.getRandomValues(new Uint8Array(NONCE_SIZE))
    const aad = uint64BE(encChunkIdx) as BufferSource
    const ciphertext = new Uint8Array(
      await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce, additionalData: aad }, key, leftover.slice().buffer),
    )
    encChunkIdx++

    const encChunkLen = NONCE_SIZE + ciphertext.length
    if (partOffset + encChunkLen > partSize) {
      await flushPart()
      partBuffer = new Uint8Array(partSize)
    }
    partBuffer.set(nonce, partOffset)
    partOffset += NONCE_SIZE
    partBuffer.set(ciphertext, partOffset)
    partOffset += ciphertext.length
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
