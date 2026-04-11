/**
 * encrypt.worker.ts — Web Worker that encrypts a file and uploads parts
 * to OSS via presigned URLs.
 *
 * Communication protocol (postMessage):
 *
 * Main → Worker:
 *   { type: 'start', file: File, dekRaw: ArrayBuffer, partUrls: string[], partSize: number }
 *   { type: 'add-urls', partUrls: string[] }  // append more presigned URLs
 *   { type: 'pause' }
 *   { type: 'resume' }
 *   { type: 'cancel' }
 *
 * Worker → Main:
 *   { type: 'progress', uploaded: number, total: number }
 *   { type: 'part', partNumber: number, etag: string }
 *   { type: 'done', parts: { part_number: number, etag: string }[], contentHash: string }
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
        await encryptAndUpload(msg.file as File, msg.dekRaw as ArrayBuffer, msg.partSize as number)
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

async function encryptAndUpload(file: File, dekRaw: ArrayBuffer, partSize: number) {
  const { createSHA256 } = await import('hash-wasm')
  const hasher = await createSHA256()
  hasher.init()

  const key = await crypto.subtle.importKey('raw', dekRaw as ArrayBuffer, { name: 'AES-GCM' }, false, ['encrypt'])

  const totalPlain = file.size
  const numEncChunks = Math.ceil(totalPlain / CHUNK_SIZE) || 1
  const encryptedSize = 5 + totalPlain + numEncChunks * (NONCE_SIZE + TAG_SIZE)
  const totalParts = Math.ceil(encryptedSize / partSize) || 1

  const completedParts: { part_number: number; etag: string }[] = []
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
      self.postMessage({ type: 'need-urls', from: partNumber, count: Math.min(100, totalParts - partNumber + 1) })
      // Wait for add-urls message
      await new Promise<void>((resolve) => {
        const handler = (ev: MessageEvent) => {
          if (ev.data.type === 'add-urls') {
            urlQueue.push(...(ev.data.partUrls || []))
            self.removeEventListener('message', handler)
            resolve()
          } else if (ev.data.type === 'cancel') {
            cancelled = true
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
    const etag = resp.headers.get('ETag') || ''
    completedParts.push({ part_number: partNumber, etag })
    self.postMessage({ type: 'part', partNumber, etag })

    uploaded += data.length
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
    const contentHash = hasher.digest('hex')
    self.postMessage({ type: 'done', parts: completedParts, contentHash })
  }
}
