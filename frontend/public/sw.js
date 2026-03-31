// Zephyr Service Worker — Client-side AES-GCM decryption for CDN-cached encrypted files.
//
// Architecture:
//   Main thread sends KEK (Key Encryption Key) via postMessage.
//   For each file, main thread registers metadata (including wrapped DEK) via postMessage.
//   SW intercepts /__decrypt__/{id} requests, unwraps DEK with KEK, decrypts file content.

const HEADER_SIZE = 5 // [1 byte version][4 bytes chunk_size]
const NONCE_SIZE = 12
const TAG_SIZE = 16

// In development (localhost), rewrite cross-origin URLs to go through Vite proxy to avoid CORS.
// In production, use the original URL directly (CDN handles CORS).
function resolveFileUrl(url) {
  if (self.location.hostname === 'localhost' || self.location.hostname === '127.0.0.1') {
    try {
      const parsed = new URL(url)
      if (parsed.origin !== self.location.origin) {
        return '/oss-proxy' + parsed.pathname + parsed.search
      }
    } catch { /* not a valid URL, return as-is */ }
  }
  return url
}

// --- State ---
let kek = null // CryptoKey (AES-GCM) — user's KEK for unwrapping DEKs
const registry = new Map() // id → { url, size, chunkSize, contentType, filename, download, wrappedDek, dek? }

// --- Lifecycle ---
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

// --- Message handling ---
self.addEventListener('message', async (event) => {
  const { type, id, key, metadata } = event.data
  switch (type) {
    case 'set-key':
      kek = await crypto.subtle.importKey(
        'raw', hexToBytes(key), { name: 'AES-GCM' }, false, ['decrypt']
      )
      break
    case 'clear-key':
      kek = null
      registry.clear()
      break
    case 'register':
      // metadata.dek is a hex string from the server; rename to dekHex
      // to avoid collision with the cached CryptoKey stored as .dek
      registry.set(id, { ...metadata, dekHex: metadata.dek || null, dek: null })
      break
    case 'unregister':
      registry.delete(id)
      break
  }
})

// --- Fetch interception ---
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url)
  if (!url.pathname.startsWith('/__decrypt__/')) return

  const id = url.pathname.slice('/__decrypt__/'.length)
  const meta = registry.get(id)
  if (!meta) {
    event.respondWith(new Response('Not found', { status: 404 }))
    return
  }

  event.respondWith(handleDecrypt(event.request, meta))
})

async function handleDecrypt(request, meta) {
  // Resolve DEK: either import directly (server-unwrapped) or unwrap with KEK (link shares)
  if (!meta.dek) {
    if (meta.dekHex) {
      meta.dek = await crypto.subtle.importKey(
        'raw', hexToBytes(meta.dekHex), { name: 'AES-GCM' }, false, ['decrypt']
      )
    } else if (meta.wrappedDek && kek) {
      meta.dek = await unwrapDEK(kek, meta.wrappedDek)
    } else {
      return new Response('No decryption key', { status: 403 })
    }
  }

  const rangeHeader = request.headers.get('Range')
  if (rangeHeader && meta.size > 0) {
    return handleRange(meta, rangeHeader)
  }
  return handleFull(meta)
}

// --- Full file response ---
async function handleFull(meta) {
  const response = await fetch(resolveFileUrl(meta.url))
  if (!response.ok) {
    return new Response('Upstream fetch failed', { status: 502 })
  }

  const reader = response.body.getReader()

  // Read and parse header
  const headerBuf = await readExact(reader, HEADER_SIZE)
  if (!headerBuf || headerBuf[0] !== 0x01) {
    return new Response('Invalid encrypted file', { status: 500 })
  }
  const chunkSize = new DataView(headerBuf.buffer, headerBuf.byteOffset).getUint32(1)
  const encChunkSize = NONCE_SIZE + chunkSize + TAG_SIZE

  let chunkIdx = 0
  let residual = new Uint8Array(0) // leftover bytes from previous read

  const decryptedStream = new ReadableStream({
    async pull(controller) {
      while (true) {
        // Accumulate bytes until we have a full encrypted chunk (or stream ends)
        while (residual.length < encChunkSize) {
          const { value, done } = await reader.read()
          if (done) break
          residual = concat(residual, value)
        }

        if (residual.length === 0) {
          controller.close()
          return
        }

        // Determine chunk boundaries (last chunk may be shorter)
        const chunkLen = Math.min(residual.length, encChunkSize)
        const encChunk = residual.slice(0, chunkLen)
        residual = residual.slice(chunkLen)

        const nonce = encChunk.slice(0, NONCE_SIZE)
        const ciphertext = encChunk.slice(NONCE_SIZE)
        const aad = uint64BE(chunkIdx)

        try {
          const plaintext = await crypto.subtle.decrypt(
            { name: 'AES-GCM', iv: nonce, additionalData: aad },
            meta.dek, ciphertext
          )
          controller.enqueue(new Uint8Array(plaintext))
          chunkIdx++
        } catch (e) {
          controller.error(new Error(`Decrypt chunk ${chunkIdx} failed: ${e.message}`))
          return
        }

        // If residual is empty and we got a short chunk, stream is done
        if (chunkLen < encChunkSize && residual.length === 0) {
          controller.close()
          return
        }
      }
    }
  })

  const headers = { 'Accept-Ranges': 'bytes' }
  if (meta.contentType) headers['Content-Type'] = meta.contentType
  if (meta.size > 0) headers['Content-Length'] = String(meta.size)
  if (meta.download && meta.filename) {
    headers['Content-Disposition'] = `attachment; filename*=UTF-8''${encodeURIComponent(meta.filename)}`
  }

  return new Response(decryptedStream, { status: 200, headers })
}

// --- Range request response ---
async function handleRange(meta, rangeHeader) {
  const { start, end } = parseRange(rangeHeader, meta.size)
  if (start === null) {
    return new Response('Invalid range', {
      status: 416,
      headers: { 'Content-Range': `bytes */${meta.size}` }
    })
  }

  const chunkSize = meta.chunkSize || 65536
  const encChunkSize = NONCE_SIZE + chunkSize + TAG_SIZE

  const startChunk = Math.floor(start / chunkSize)
  const endChunk = Math.floor(end / chunkSize)

  let cipherStart = HEADER_SIZE + startChunk * encChunkSize
  let cipherEnd = HEADER_SIZE + (endChunk + 1) * encChunkSize - 1

  // Clamp to actual encrypted file size
  const totalChunks = Math.ceil(meta.size / chunkSize)
  const lastChunkPlain = meta.size % chunkSize || (meta.size > 0 ? chunkSize : 0)
  const cipherTotal = HEADER_SIZE + (totalChunks - 1) * encChunkSize + NONCE_SIZE + lastChunkPlain + TAG_SIZE
  if (cipherEnd >= cipherTotal) cipherEnd = cipherTotal - 1

  const response = await fetch(resolveFileUrl(meta.url), {
    headers: { 'Range': `bytes=${cipherStart}-${cipherEnd}` }
  })
  if (!response.ok && response.status !== 206) {
    return new Response('Upstream range fetch failed', { status: 502 })
  }

  // Read all fetched encrypted data
  const encData = new Uint8Array(await response.arrayBuffer())

  // Decrypt chunks and collect plaintext
  const plaintextParts = []
  let offset = 0
  let chunkIdx = startChunk

  while (offset < encData.length) {
    const remaining = encData.length - offset
    const thisChunkEnc = Math.min(remaining, encChunkSize)
    const chunk = encData.slice(offset, offset + thisChunkEnc)
    offset += thisChunkEnc

    const nonce = chunk.slice(0, NONCE_SIZE)
    const ciphertext = chunk.slice(NONCE_SIZE)
    const aad = uint64BE(chunkIdx)

    const plaintext = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: nonce, additionalData: aad },
      meta.dek, ciphertext
    )
    plaintextParts.push(new Uint8Array(plaintext))
    chunkIdx++
  }

  // Concatenate all plaintext
  const fullPlaintext = concatAll(plaintextParts)

  // Trim to the exact requested range
  const trimStart = start - startChunk * chunkSize
  const contentLength = end - start + 1
  const trimmed = fullPlaintext.slice(trimStart, trimStart + contentLength)

  return new Response(trimmed, {
    status: 206,
    headers: {
      'Content-Range': `bytes ${start}-${end}/${meta.size}`,
      'Content-Length': String(contentLength),
      'Content-Type': meta.contentType || 'application/octet-stream',
      'Accept-Ranges': 'bytes',
    }
  })
}

// --- DEK unwrapping ---
async function unwrapDEK(kek, wrappedDekHex) {
  const wrapped = hexToBytes(wrappedDekHex)
  const nonce = wrapped.slice(0, NONCE_SIZE)
  const ciphertext = wrapped.slice(NONCE_SIZE)

  const dekBytes = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: nonce }, kek, ciphertext
  )
  return crypto.subtle.importKey(
    'raw', dekBytes, { name: 'AES-GCM' }, false, ['decrypt']
  )
}

// --- Range header parsing ---
function parseRange(header, totalSize) {
  if (!header.startsWith('bytes=')) return { start: null, end: null }
  const spec = header.slice(6)
  if (spec.includes(',')) return { start: null, end: null } // no multi-range

  const parts = spec.split('-')
  let start, end

  if (parts[0] === '') {
    // Suffix: bytes=-N
    const n = parseInt(parts[1], 10)
    if (isNaN(n) || n <= 0) return { start: null, end: null }
    start = Math.max(0, totalSize - n)
    end = totalSize - 1
  } else {
    start = parseInt(parts[0], 10)
    if (isNaN(start) || start < 0) return { start: null, end: null }
    end = parts[1] === '' ? totalSize - 1 : parseInt(parts[1], 10)
    if (isNaN(end)) return { start: null, end: null }
  }

  if (start > end || start >= totalSize) return { start: null, end: null }
  if (end >= totalSize) end = totalSize - 1
  return { start, end }
}

// --- Utility functions ---
function hexToBytes(hex) {
  const bytes = new Uint8Array(hex.length / 2)
  for (let i = 0; i < hex.length; i += 2) {
    bytes[i / 2] = parseInt(hex.substr(i, 2), 16)
  }
  return bytes
}

function uint64BE(n) {
  const buf = new ArrayBuffer(8)
  const view = new DataView(buf)
  // n fits in 32 bits for chunk indices
  view.setUint32(0, 0)
  view.setUint32(4, n)
  return new Uint8Array(buf)
}

function concat(a, b) {
  const result = new Uint8Array(a.length + b.length)
  result.set(a, 0)
  result.set(b, a.length)
  return result
}

function concatAll(parts) {
  let totalLen = 0
  for (const p of parts) totalLen += p.length
  const result = new Uint8Array(totalLen)
  let offset = 0
  for (const p of parts) {
    result.set(p, offset)
    offset += p.length
  }
  return result
}

async function readExact(reader, n) {
  const buf = new Uint8Array(n)
  let filled = 0
  while (filled < n) {
    const { value, done } = await reader.read()
    if (done) return filled > 0 ? buf.slice(0, filled) : null
    const take = Math.min(value.length, n - filled)
    buf.set(value.slice(0, take), filled)
    filled += take
    // If we got more than needed, we'd lose data. But for a 5-byte header this won't happen.
  }
  return buf
}
