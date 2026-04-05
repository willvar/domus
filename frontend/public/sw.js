// Zephyr Service Worker — Client-side AES-GCM decryption with Cache API.
//
// Architecture:
//   Main thread sends KEK (Key Encryption Key) via postMessage.
//   For each file, main thread registers metadata (including DEK + contentHash).
//   SW intercepts /__decrypt__/{id} requests, checks cache by contentHash,
//   decrypts on miss, caches the result, returns decrypted content.

const HEADER_SIZE = 5 // [1 byte version][4 bytes chunk_size]
const NONCE_SIZE = 12
const TAG_SIZE = 16
const CACHE_NAME = 'zephyr-decrypt'
const CACHE_CLEANUP_DELAY = 5 * 60 * 1000 // 5 minutes

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
const registry = new Map() // id → { url, size, chunkSize, contentType, filename, download, wrappedDek, dek?, contentHash? }
const cleanupTimers = new Map() // contentHash → timer id
const inflight = new Map() // contentHash → Promise<Uint8Array> — deduplicates concurrent decrypts

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
      inflight.clear()
      // Clear all cleanup timers and purge cache immediately
      for (const timer of cleanupTimers.values()) clearTimeout(timer)
      cleanupTimers.clear()
      caches.delete(CACHE_NAME)
      break
    case 'register': {
      // metadata.dek is a hex string from the server; rename to dekHex
      // to avoid collision with the cached CryptoKey stored as .dek
      const entry = { ...metadata, dekHex: metadata.dek || null, dek: null }
      registry.set(id, entry)
      // Cancel pending cleanup for this contentHash (file re-opened)
      if (entry.contentHash && cleanupTimers.has(entry.contentHash)) {
        clearTimeout(cleanupTimers.get(entry.contentHash))
        cleanupTimers.delete(entry.contentHash)
      }
      break
    }
    case 'unregister': {
      const entry = registry.get(id)
      registry.delete(id)
      // Schedule delayed cache cleanup
      if (entry?.contentHash) {
        // Only schedule cleanup if no other registry entry uses this contentHash
        let stillUsed = false
        for (const e of registry.values()) {
          if (e.contentHash === entry.contentHash) { stillUsed = true; break }
        }
        if (!stillUsed) {
          const hash = entry.contentHash
          const timer = setTimeout(async () => {
            cleanupTimers.delete(hash)
            const cache = await caches.open(CACHE_NAME)
            await cache.delete(cacheKey(hash))
          }, CACHE_CLEANUP_DELAY)
          cleanupTimers.set(hash, timer)
        }
      }
      break
    }
  }
})

function cacheKey(contentHash) {
  return new Request('/__cache__/' + contentHash)
}

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
  // Resolve DEK
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

  // Try cache first
  if (meta.contentHash) {
    const cache = await caches.open(CACHE_NAME)
    const cached = await cache.match(cacheKey(meta.contentHash))
    if (cached) {
      if (rangeHeader && meta.size > 0) {
        return handleRangeFromCache(meta, rangeHeader, cached)
      }
      const headers = { 'Accept-Ranges': 'bytes' }
      if (meta.contentType) headers['Content-Type'] = meta.contentType
      if (meta.size > 0) headers['Content-Length'] = String(meta.size)
      if (meta.download && meta.filename) {
        headers['Content-Disposition'] = `attachment; filename*=UTF-8''${encodeURIComponent(meta.filename)}`
      }
      return new Response(cached.body, { status: 200, headers })
    }
  }

  // Decrypt — deduplicate concurrent requests for the same contentHash
  const fullData = await deduplicatedDecrypt(meta)
  if (!fullData) return new Response('Decrypt failed', { status: 500 })

  if (rangeHeader && meta.size > 0) {
    return serveRange(meta, rangeHeader, fullData)
  }

  // Full response
  const headers = { 'Accept-Ranges': 'bytes' }
  if (meta.contentType) headers['Content-Type'] = meta.contentType
  if (meta.size > 0) headers['Content-Length'] = String(fullData.byteLength)
  if (meta.download && meta.filename) {
    headers['Content-Disposition'] = `attachment; filename*=UTF-8''${encodeURIComponent(meta.filename)}`
  }
  return new Response(fullData, { status: 200, headers })
}

// --- Deduplicated decrypt: prevents double download when <video> sends rapid GET + Range ---
async function deduplicatedDecrypt(meta) {
  const hash = meta.contentHash
  if (hash && inflight.has(hash)) {
    return inflight.get(hash)
  }

  const promise = decryptFull(meta).then(data => {
    // Write to Cache API before resolving so subsequent requests hit cache
    if (data && hash) {
      const blob = new Blob([data], { type: meta.contentType || 'application/octet-stream' })
      const cacheResp = new Response(blob, {
        headers: { 'Content-Type': meta.contentType || 'application/octet-stream' }
      })
      return caches.open(CACHE_NAME)
        .then(c => c.put(cacheKey(hash), cacheResp))
        .then(() => data)
    }
    return data
  }).finally(() => {
    if (hash) inflight.delete(hash)
  })

  if (hash) inflight.set(hash, promise)
  return promise
}

// --- Decrypt full file into a Uint8Array ---
async function decryptFull(meta) {
  const response = await fetch(resolveFileUrl(meta.url))
  if (!response.ok) return null

  const reader = response.body.getReader()

  // Read header into residual so no bytes from the first network chunk are lost
  let residual = new Uint8Array(0)
  while (residual.length < HEADER_SIZE) {
    const { value, done } = await reader.read()
    if (done) break
    residual = concat(residual, value)
  }
  if (residual.length < HEADER_SIZE || residual[0] !== 0x01) return null

  const chunkSize = new DataView(residual.buffer, residual.byteOffset).getUint32(1)
  const encChunkSize = NONCE_SIZE + chunkSize + TAG_SIZE
  residual = residual.slice(HEADER_SIZE)

  const parts = []
  let chunkIdx = 0

  while (true) {
    while (residual.length < encChunkSize) {
      const { value, done } = await reader.read()
      if (done) break
      residual = concat(residual, value)
    }
    if (residual.length === 0) break

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
      parts.push(new Uint8Array(plaintext))
      chunkIdx++
    } catch {
      return null
    }

    if (chunkLen < encChunkSize && residual.length === 0) break
  }

  return concatAll(parts)
}

// --- Serve a Range slice from cached/decrypted data ---
function handleRangeFromCache(meta, rangeHeader, cached) {
  return cached.arrayBuffer().then(buf => serveRange(meta, rangeHeader, new Uint8Array(buf)))
}

function serveRange(meta, rangeHeader, fullData) {
  const total = fullData.byteLength
  const { start, end } = parseRange(rangeHeader, total)
  if (start === null) {
    return new Response('Invalid range', {
      status: 416, headers: { 'Content-Range': `bytes */${total}` }
    })
  }
  const contentLength = end - start + 1

  // If the range covers the entire file, return 200 instead of 206.
  // This ensures <video> elements properly initialise their media source.
  if (start === 0 && contentLength >= total) {
    return new Response(fullData, {
      status: 200,
      headers: {
        'Content-Type': meta.contentType || 'application/octet-stream',
        'Content-Length': String(total),
        'Accept-Ranges': 'bytes',
      }
    })
  }

  return new Response(fullData.slice(start, start + contentLength), {
    status: 206,
    headers: {
      'Content-Range': `bytes ${start}-${end}/${total}`,
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
  if (spec.includes(',')) return { start: null, end: null }

  const parts = spec.split('-')
  let start, end

  if (parts[0] === '') {
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
