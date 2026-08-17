// Domus Service Worker — Client-side AES-GCM decryption with Range support.
//
// Architecture:
//   Main thread sends KEK (Key Encryption Key) via postMessage.
//   For each file, main thread registers metadata (including DEK + contentHash).
//   SW intercepts /__decrypt__/{id} requests:
//     - Initial load: streams decrypted content progressively (边下边播)
//     - Range requests: fetches only the required encrypted chunks from OSS
//     - Cache hit: serves from Cache API

const HEADER_SIZE = 5 // [1 byte version][4 bytes chunk_size]
const NONCE_SIZE = 12
const TAG_SIZE = 16
const CACHE_NAME = 'domus-decrypt'
const LEGACY_CACHE_NAME = 'zephyr-decrypt'
const CACHE_CLEANUP_DELAY = 5 * 60 * 1000 // 5 minutes
const MAX_CACHEABLE_BYTES = 64 * 1024 * 1024


// --- State ---
let kek = null // CryptoKey (AES-GCM) — user's KEK for unwrapping DEKs
const registry = new Map() // id → { url, size, chunkSize, contentType, filename, download, wrappedDek, dek?, contentHash? }
const cleanupTimers = new Map() // contentHash → timer id
const chunkAccum = new Map() // contentHash → { chunks: Map<idx, Uint8Array>, totalChunks: number }

// --- Lifecycle ---
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(
  Promise.all([self.clients.claim(), caches.delete(LEGACY_CACHE_NAME)])
))

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
      chunkAccum.clear()
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
          chunkAccum.delete(hash) // release in-memory chunks
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
    case 'flush':
      if (event.ports[0]) event.ports[0].postMessage(null)
      break
  }
})

function cacheKey(contentHash) {
  return new Request('/__cache__/' + contentHash)
}

// --- Chunk accumulator: collects decrypted chunks across multiple Range requests ---
function getAccum(meta) {
  if (!meta.contentHash || !meta.chunkSize || !meta.size || meta.size > MAX_CACHEABLE_BYTES) return null
  let acc = chunkAccum.get(meta.contentHash)
  if (!acc) {
    acc = { chunks: new Map(), totalChunks: Math.ceil(meta.size / meta.chunkSize) }
    chunkAccum.set(meta.contentHash, acc)
  }
  return acc
}

function tryFlushAccum(meta) {
  const acc = chunkAccum.get(meta.contentHash)
  if (!acc || acc.chunks.size < acc.totalChunks) return
  const parts = []
  for (let i = 0; i < acc.totalChunks; i++) parts.push(acc.chunks.get(i))
  chunkAccum.delete(meta.contentHash)
  const full = concatAll(parts)
  const blob = new Blob([full], { type: meta.contentType || 'application/octet-stream' })
  const cacheResp = new Response(blob, {
    headers: { 'Content-Type': meta.contentType || 'application/octet-stream' },
  })
  caches.open(CACHE_NAME).then(c => c.put(cacheKey(meta.contentHash), cacheResp))
}

// --- Encryption geometry helpers ---
function encChunkSize(chunkSize) {
  return NONCE_SIZE + chunkSize + TAG_SIZE
}

// Map plaintext byte range [pStart, pEnd] to encrypted byte range.
function plaintextToEncRange(pStart, pEnd, chunkSize) {
  const encChunk = encChunkSize(chunkSize)
  const startChunk = Math.floor(pStart / chunkSize)
  const endChunk = Math.floor(pEnd / chunkSize)
  const encStart = HEADER_SIZE + startChunk * encChunk
  const encEnd = HEADER_SIZE + (endChunk + 1) * encChunk - 1
  return { startChunk, endChunk, encStart, encEnd }
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

  // Range request without cache — stream-decrypt only the needed encrypted chunks
  if (rangeHeader && meta.size > 0 && meta.chunkSize > 0) {
    const { start, end } = parseRange(rangeHeader, meta.size)
    if (start === null) {
      return new Response('Invalid range', {
        status: 416, headers: { 'Content-Range': `bytes */${meta.size}` }
      })
    }
    return streamDecryptRange(meta, start, end)
  }

  // Full request — stream decrypt (边下边播) and cache in background
  return streamDecryptResponse(meta)
}

// --- Stream decrypt: progressive decryption returned as ReadableStream ---
function streamDecryptResponse(meta) {
  let collectForCache = Boolean(
    meta.contentHash && (!meta.size || meta.size <= MAX_CACHEABLE_BYTES)
  )
  const plainParts = []
  let totalPlain = 0

  const stream = new ReadableStream({
    async start(controller) {
      try {
        const response = await fetch(meta.url)
        if (!response.ok) {
          controller.error(new Error('fetch failed'))
          return
        }

        const reader = response.body.getReader()

        // Read header
        let residual = new Uint8Array(0)
        while (residual.length < HEADER_SIZE) {
          const { value, done } = await reader.read()
          if (done) break
          residual = concat(residual, value)
        }
        if (residual.length < HEADER_SIZE || residual[0] !== 0x01) {
          controller.error(new Error('invalid header'))
          return
        }

        const chunkSize = new DataView(residual.buffer, residual.byteOffset).getUint32(1, false)
        const encChunk = encChunkSize(chunkSize)
        residual = residual.slice(HEADER_SIZE)

        let chunkIdx = 0

        while (true) {
          // Accumulate enough data for one encrypted chunk
          while (residual.length < encChunk) {
            const { value, done } = await reader.read()
            if (done) break
            residual = concat(residual, value)
          }
          if (residual.length === 0) break

          const chunkLen = Math.min(residual.length, encChunk)
          const encData = residual.slice(0, chunkLen)
          residual = residual.slice(chunkLen)

          const nonce = encData.slice(0, NONCE_SIZE)
          const ciphertext = encData.slice(NONCE_SIZE)
          const aad = uint64BE(chunkIdx)

          try {
            const plaintext = new Uint8Array(await crypto.subtle.decrypt(
              { name: 'AES-GCM', iv: nonce, additionalData: aad },
              meta.dek, ciphertext
            ))
            controller.enqueue(plaintext)
            totalPlain += plaintext.length
            if (collectForCache) {
              if (totalPlain <= MAX_CACHEABLE_BYTES) {
                plainParts.push(plaintext)
              } else {
                // Unknown/malformed sizes must not turn the browser process
                // into an unbounded plaintext cache.
                collectForCache = false
                plainParts.length = 0
              }
            }
            chunkIdx++
          } catch {
            controller.error(new Error('decrypt failed'))
            return
          }

          if (chunkLen < encChunk && residual.length === 0) break
        }

        controller.close()

        // Cache the full plaintext in background
        if (collectForCache && plainParts.length > 0) {
          const full = concatAll(plainParts)
          const blob = new Blob([full], { type: meta.contentType || 'application/octet-stream' })
          const cacheResp = new Response(blob, {
            headers: { 'Content-Type': meta.contentType || 'application/octet-stream' }
          })
          caches.open(CACHE_NAME).then(c => c.put(cacheKey(meta.contentHash), cacheResp))
        }
      } catch (err) {
        try { controller.error(err) } catch { /* already closed */ }
      }
    }
  })

  const headers = {
    'Accept-Ranges': 'bytes',
    'Content-Type': meta.contentType || 'application/octet-stream',
  }
  if (meta.size > 0) headers['Content-Length'] = String(meta.size)
  if (meta.download && meta.filename) {
    headers['Content-Disposition'] = `attachment; filename*=UTF-8''${encodeURIComponent(meta.filename)}`
  }

  return new Response(stream, { status: 200, headers })
}

// --- Stream-decrypt a specific plaintext range, fetching only the required encrypted chunks ---
function streamDecryptRange(meta, pStart, pEnd) {
  const chunkSize = meta.chunkSize
  const encChunk = encChunkSize(chunkSize)
  const { startChunk, encStart, encEnd } = plaintextToEncRange(pStart, pEnd, chunkSize)
  const totalPlain = pEnd - pStart + 1
  const acc = getAccum(meta)

  const ac = new AbortController()

  const stream = new ReadableStream({
    async start(controller) {
      try {
        const resp = await fetch(meta.url, {
          headers: { 'Range': `bytes=${encStart}-${encEnd}` },
          signal: ac.signal,
        })
        if (!resp.ok && resp.status !== 206) {
          controller.error(new Error('fetch failed'))
          return
        }

        const reader = resp.body.getReader()
        let residual = new Uint8Array(0)
        let chunkIdx = startChunk
        let emitted = 0

        while (emitted < totalPlain) {
          // If this chunk is already in the accumulator, serve from memory
          const cached = acc?.chunks.get(chunkIdx)
          if (cached) {
            let sliceStart = 0, sliceEnd = cached.length
            if (chunkIdx === startChunk) sliceStart = pStart - startChunk * chunkSize
            const remaining = totalPlain - emitted
            if (sliceEnd - sliceStart > remaining) sliceEnd = sliceStart + remaining
            controller.enqueue(cached.subarray(sliceStart, sliceEnd))
            emitted += sliceEnd - sliceStart
            chunkIdx++

            // Skip the corresponding encrypted chunk in the S3 stream
            let toSkip = encChunk
            while (toSkip > 0) {
              if (residual.length >= toSkip) { residual = residual.slice(toSkip); break }
              toSkip -= residual.length
              residual = new Uint8Array(0)
              const { value, done } = await reader.read()
              if (done) break
              residual = value
            }
            continue
          }

          while (residual.length < encChunk) {
            const { value, done } = await reader.read()
            if (done) break
            residual = concat(residual, value)
          }
          if (residual.length === 0) break

          const chunkLen = Math.min(residual.length, encChunk)
          const encData = residual.slice(0, chunkLen)
          residual = residual.slice(chunkLen)

          const nonce = encData.slice(0, NONCE_SIZE)
          const ciphertext = encData.slice(NONCE_SIZE)
          const aad = uint64BE(chunkIdx)

          const plain = new Uint8Array(await crypto.subtle.decrypt(
            { name: 'AES-GCM', iv: nonce, additionalData: aad },
            meta.dek, ciphertext,
          ))

          // Store full decrypted chunk in accumulator
          if (acc) acc.chunks.set(chunkIdx, plain)

          let sliceStart = 0, sliceEnd = plain.length
          if (chunkIdx === startChunk) sliceStart = pStart - startChunk * chunkSize
          const remaining = totalPlain - emitted
          if (sliceEnd - sliceStart > remaining) sliceEnd = sliceStart + remaining

          controller.enqueue(plain.subarray(sliceStart, sliceEnd))
          emitted += sliceEnd - sliceStart
          chunkIdx++

          if (chunkLen < encChunk && residual.length === 0) break
        }

        controller.close()

        // Try to assemble full plaintext from accumulated chunks → Cache API
        if (acc) tryFlushAccum(meta)
      } catch (err) {
        if (err.name !== 'AbortError') {
          try { controller.error(err) } catch { /* already closed */ }
        }
      }
    },
    cancel() {
      ac.abort()
    },
  })

  const total = meta.size
  const contentType = meta.contentType || 'application/octet-stream'

  // Full-file range → 200 (ensures <video> initialises its media source correctly)
  if (pStart === 0 && pEnd >= meta.size - 1) {
    return new Response(stream, {
      status: 200,
      headers: {
        'Content-Type': contentType,
        'Content-Length': String(total),
        'Accept-Ranges': 'bytes',
      },
    })
  }

  // True partial range → 206
  return new Response(stream, {
    status: 206,
    headers: {
      'Content-Range': `bytes ${pStart}-${pEnd}/${total}`,
      'Content-Length': String(totalPlain),
      'Content-Type': contentType,
      'Accept-Ranges': 'bytes',
    },
  })
}

// --- Serve a Range slice ---
function handleRangeFromCache(meta, rangeHeader, cached) {
  return cached.blob().then(blob => {
    const { start, end } = parseRange(rangeHeader, blob.size)
    if (start === null) {
      return new Response('Invalid range', {
        status: 416, headers: { 'Content-Range': `bytes */${blob.size}` }
      })
    }
    const slice = blob.slice(start, end + 1, meta.contentType || blob.type)
    return serveRange(meta, slice, start, end, blob.size)
  })
}

function serveRange(meta, data, start, end, cachedSize) {
  const total = meta.size || cachedSize
  const contentLength = end - start + 1

  // If the range covers the entire file, return 200 instead of 206.
  // This ensures <video> elements properly initialise their media source.
  if (start === 0 && contentLength >= total) {
    return new Response(data, {
      status: 200,
      headers: {
        'Content-Type': meta.contentType || 'application/octet-stream',
        'Content-Length': String(total),
        'Accept-Ranges': 'bytes',
      }
    })
  }

  return new Response(data, {
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
  view.setBigUint64(0, BigInt(n), false)
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
