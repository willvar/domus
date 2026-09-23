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
const CACHE_NAME = 'domus-decrypt-v2'
// Pre-v2 entries may be truncated: the SW once dropped tail chunks while
// caching streamed plaintext. Activation deletes the poisoned generations.
const STALE_CACHE_NAMES = ['domus-decrypt', 'zephyr-decrypt']
const CACHE_CLEANUP_DELAY = 5 * 60 * 1000 // 5 minutes
const MAX_CACHEABLE_BYTES = 64 * 1024 * 1024


// --- State ---
const BOOT_ID = crypto.randomUUID() // changes on every SW (re)start
let kek = null // CryptoKey (AES-GCM) — user's KEK for unwrapping DEKs
const registry = new Map() // id → { url, size, chunkSize, contentType, filename, download, wrappedDek, dek?, contentHash? }
const cleanupTimers = new Map() // contentHash → timer id
const chunkAccum = new Map() // contentHash → { chunks: Map<idx, Uint8Array>, totalChunks: number }

// --- Lifecycle ---
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(
  Promise.all([self.clients.claim(), ...STALE_CACHE_NAMES.map(name => caches.delete(name))])
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
    case 'boot-id':
      // Lets the main thread detect SW restarts: the in-memory registry is
      // empty after one, so previously issued decrypt URLs are all dead.
      if (event.ports[0]) event.ports[0].postMessage(BOOT_ID)
      break
  }
})

function cacheKey(contentHash) {
  return new Request('/__cache__/' + contentHash)
}

// --- Chunk accumulator: collects decrypted chunks across multiple Range requests ---
// Media content types are excluded: they stream, so accumulating their
// plaintext only balloons the SW process until the browser kills it.
function isStreamableMedia(meta) {
  const type = meta.contentType || ''
  return type.startsWith('video/') || type.startsWith('audio/')
}

function getAccum(meta) {
  if (!meta.contentHash || !meta.chunkSize || !meta.size || meta.size > MAX_CACHEABLE_BYTES) return null
  if (isStreamableMedia(meta)) return null
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
// WebCrypto decrypts are async and off-thread: with a strictly sequential
// await loop the pipeline is serialized at ~50 MB/s. A small window of
// in-flight decryptions (emitted strictly in order) hides the per-chunk
// latency behind the ciphertext stream.
const DECRYPT_WINDOW = 6

async function readUntil(reader, residual, want) {
  while (residual.length < want) {
    const { value, done } = await reader.read()
    if (done) break
    residual = concat(residual, value)
  }
  return residual
}

// Pull one encrypted chunk from the reader. Returns the raw encrypted chunk
// (nonce + ciphertext + tag), or null at end of stream.
async function pullEncChunk(reader, residual, encChunk) {
  residual.value = await readUntil(reader, residual.value ?? new Uint8Array(0), encChunk)
  if (residual.value.length === 0) return null
  const chunkLen = Math.min(residual.value.length, encChunk)
  const encData = residual.value.slice(0, chunkLen)
  residual.value = residual.value.slice(chunkLen)
  return encData
}

// Windowed decrypt: keeps DECRYPT_WINDOW WebCrypto decryptions in flight and
// calls back with plaintext chunks strictly in index order. onChunk(idx,
// plaintext, chunkLen) returning false stops the pipeline early. Returns when
// the stream is drained or the caller stops. `residual` must be an object
// with a .value field (ciphertext accumulator across calls).
async function decryptStreamWindowed({ reader, residual, dek, encChunk, startChunk, onChunk }) {
  const inflight = new Map() // idx -> Promise<Uint8Array>
  let nextIdx = startChunk
  let readerDone = false
  try {
    while (true) {
      while (!readerDone && inflight.size < DECRYPT_WINDOW) {
        const encData = await pullEncChunk(reader, residual, encChunk)
        if (encData === null) {
          readerDone = true
          break
        }
        const idx = nextIdx++
        inflight.set(idx, crypto.subtle.decrypt(
          { name: 'AES-GCM', iv: encData.slice(0, NONCE_SIZE), additionalData: uint64BE(idx) },
          dek, encData.slice(NONCE_SIZE),
        ).then(pt => new Uint8Array(pt)))
      }
      if (inflight.size === 0) return
      const idx = startChunk
      let plain
      try {
        plain = await inflight.get(idx)
      } catch {
        throw new Error('decrypt failed')
      }
      inflight.delete(idx)
      startChunk++
      if (!onChunk(idx, plain)) {
        for (const pending of inflight.values()) {
          pending.catch(() => {}) // avoid unhandled rejections while unwinding
        }
        return
      }
    }
  } catch (err) {
    for (const pending of inflight.values()) {
      pending.catch(() => {}) // avoid unhandled rejections while unwinding
    }
    throw err
  }
}

function streamDecryptResponse(meta) {
  let collectForCache = Boolean(
    meta.contentHash && !isStreamableMedia(meta) && meta.size > 0 && meta.size <= MAX_CACHEABLE_BYTES
  )
  const plainParts = []
  let totalPlain = 0

  // Pull-based pipeline state: the browser pulls (desiredSize > 0) and only
  // then does the SW decrypt, so playback memory stays bounded instead of
  // decrypting the whole file as fast as the network allows. WebCrypto runs
  // off-thread; a small in-flight window hides the per-chunk latency while
  // chunks are still emitted strictly in index order.
  const state = {
    started: false,
    reader: null,
    residual: new Uint8Array(0),
    chunkSize: 0,
    encChunk: 0,
    nextIdx: 0,
    emitIdx: 0,
    readerDone: false,
    inflight: new Map(),
  }

  async function startPipeline() {
    const response = await fetch(meta.url)
    if (!response.ok || !response.body) throw new Error('fetch failed')
    state.reader = response.body.getReader()
    while (state.residual.length < HEADER_SIZE) {
      const { value, done } = await state.reader.read()
      if (done) break
      state.residual = concat(state.residual, value)
    }
    if (state.residual.length < HEADER_SIZE || state.residual[0] !== 0x01) {
      throw new Error('invalid header')
    }
    state.chunkSize = new DataView(state.residual.buffer, state.residual.byteOffset).getUint32(1, false)
    state.encChunk = encChunkSize(state.chunkSize)
    state.residual = state.residual.slice(HEADER_SIZE)
    state.started = true
  }

  async function pullEncChunk() {
    while (state.residual.length < state.encChunk) {
      const { value, done } = await state.reader.read()
      if (done) break
      state.residual = concat(state.residual, value)
    }
    // A tail shorter than one chunk (or a small single-chunk object) is
    // still a full encrypted chunk and must be emitted, not dropped.
    if (state.residual.length === 0) return undefined
    const chunkLen = Math.min(state.residual.length, state.encChunk)
    const encData = state.residual.slice(0, chunkLen)
    state.residual = state.residual.slice(chunkLen)
    return encData
  }

  const stream = new ReadableStream({
    async pull(controller) {
      try {
        if (!state.started) await startPipeline()
        // Top up the in-flight decrypt window.
        while (!state.readerDone && state.inflight.size < DECRYPT_WINDOW) {
          const encData = await pullEncChunk()
          if (encData === undefined) {
            state.readerDone = true
            break
          }
          const idx = state.nextIdx++
          state.inflight.set(idx, crypto.subtle.decrypt(
            { name: 'AES-GCM', iv: encData.slice(0, NONCE_SIZE), additionalData: uint64BE(idx) },
            meta.dek, encData.slice(NONCE_SIZE),
          ).then(pt => new Uint8Array(pt)))
        }
        if (state.inflight.size === 0) {
          controller.close()
          if (collectForCache && plainParts.length > 0) {
            const full = concatAll(plainParts)
            const blob = new Blob([full], { type: meta.contentType || 'application/octet-stream' })
            const cacheResp = new Response(blob, {
              headers: { 'Content-Type': meta.contentType || 'application/octet-stream' }
            })
            caches.open(CACHE_NAME).then(c => c.put(cacheKey(meta.contentHash), cacheResp))
          }
          return
        }
        const plain = await state.inflight.get(state.emitIdx)
        state.inflight.delete(state.emitIdx)
        state.emitIdx++
        controller.enqueue(plain)
        totalPlain += plain.length
        if (collectForCache) {
          if (totalPlain <= MAX_CACHEABLE_BYTES) {
            plainParts.push(plain)
          } else {
            // Unknown/malformed sizes must not turn the browser process
            // into an unbounded plaintext cache.
            collectForCache = false
            plainParts.length = 0
          }
        }
      } catch (err) {
        try { controller.error(err) } catch { /* already closed */ }
      }
    },
    cancel() {
      if (state.reader) {
        state.reader.cancel().catch(() => {})
      }
    },
  }, { highWaterMark: 16 })

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
        const residualBox = { value: new Uint8Array(0) }
        let emitted = 0

        await decryptStreamWindowed({
          reader, residual: residualBox, dek: meta.dek, encChunk, startChunk,
          onChunk: (idx, plain, chunkLen) => {
            // Store full decrypted chunk in accumulator
            if (acc) acc.chunks.set(idx, plain)

            let sliceStart = 0, sliceEnd = plain.length
            if (idx === startChunk) sliceStart = pStart - startChunk * chunkSize
            const remaining = totalPlain - emitted
            if (sliceEnd - sliceStart > remaining) sliceEnd = sliceStart + remaining
            if (sliceEnd > sliceStart) {
              controller.enqueue(plain.subarray(sliceStart, sliceEnd))
              emitted += sliceEnd - sliceStart
            }
            return emitted < totalPlain
          },
        })

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
