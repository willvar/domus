// Domus Service Worker — client-side AES-GCM decryption with bounded Range I/O.
const HEADER_SIZE = 5
const NONCE_SIZE = 12
const TAG_SIZE = 16
const CACHE_NAME = 'domus-decrypt-v2'
const STALE_CACHE_NAMES = ['domus-decrypt', 'zephyr-decrypt']
const CACHE_CLEANUP_DELAY = 5 * 60 * 1000
const MAX_CACHEABLE_BYTES = 64 * 1024 * 1024
const CIPHER_FETCH_BYTES = 4 * 1024 * 1024
const MEDIA_RANGE_BYTES = 4 * 1024 * 1024
const DECRYPT_WINDOW = 6

const BOOT_ID = crypto.randomUUID()
let kek = null
const registry = new Map()
const cleanupTimers = new Map()
const chunkAccum = new Map()

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', event => event.waitUntil(
  Promise.all([self.clients.claim(), ...STALE_CACHE_NAMES.map(name => caches.delete(name))])
))

self.addEventListener('message', async event => {
  const { type, id, key, metadata } = event.data
  switch (type) {
    case 'set-key':
      kek = await crypto.subtle.importKey('raw', hexToBytes(key), 'AES-GCM', false, ['decrypt'])
      break
    case 'clear-key':
      kek = null
      for (const entry of registry.values()) abortEntry(entry)
      registry.clear()
      chunkAccum.clear()
      for (const timer of cleanupTimers.values()) clearTimeout(timer)
      cleanupTimers.clear()
      await caches.delete(CACHE_NAME)
      break
    case 'register': {
      abortEntry(registry.get(id))
      const entry = { ...metadata, dekHex: metadata.dek || null, dek: null, requests: new Set(), closed: false }
      registry.set(id, entry)
      if (entry.contentHash && cleanupTimers.has(entry.contentHash)) {
        clearTimeout(cleanupTimers.get(entry.contentHash))
        cleanupTimers.delete(entry.contentHash)
      }
      break
    }
    case 'unregister': {
      const entry = registry.get(id)
      abortEntry(entry)
      registry.delete(id)
      if (entry?.contentHash && ![...registry.values()].some(item => item.contentHash === entry.contentHash)) {
        const hash = entry.contentHash
        chunkAccum.delete(hash)
        cleanupTimers.set(hash, setTimeout(async () => {
          cleanupTimers.delete(hash)
          const cache = await caches.open(CACHE_NAME)
          await cache.delete(cacheKey(hash))
        }, CACHE_CLEANUP_DELAY))
      }
      break
    }
    case 'flush': event.ports[0]?.postMessage(null); break
    case 'boot-id': event.ports[0]?.postMessage(BOOT_ID); break
  }
})

function abortEntry(entry) {
  if (!entry) return
  entry.closed = true
  for (const request of entry.requests) request.abort(new DOMException('Decryption closed', 'AbortError'))
}

function cacheKey(hash) { return new Request('/__cache__/' + hash) }

function cacheable(meta) {
  const type = meta.contentType || ''
  return meta.contentHash && meta.size > 0 && meta.size <= MAX_CACHEABLE_BYTES &&
    !type.startsWith('video/') && !type.startsWith('audio/')
}

function getAccum(meta) {
  if (!cacheable(meta)) return null
  let acc = chunkAccum.get(meta.contentHash)
  if (!acc) {
    acc = { chunks: new Map(), totalChunks: Math.ceil(meta.size / meta.chunkSize) }
    chunkAccum.set(meta.contentHash, acc)
  }
  return acc
}

function tryFlushAccum(meta) {
  const acc = chunkAccum.get(meta.contentHash)
  if (!acc || acc.chunks.size !== acc.totalChunks || meta.closed) return
  const parts = []
  for (let i = 0; i < acc.totalChunks; i++) {
    const part = acc.chunks.get(i)
    if (!part) return
    parts.push(part)
  }
  chunkAccum.delete(meta.contentHash)
  const response = new Response(new Blob(parts, { type: meta.contentType || 'application/octet-stream' }))
  void caches.open(CACHE_NAME).then(cache => cache.put(cacheKey(meta.contentHash), response)).catch(() => {})
}

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url)
  if (!url.pathname.startsWith('/__decrypt__/')) return
  const meta = registry.get(url.pathname.slice('/__decrypt__/'.length))
  event.respondWith(meta ? handleDecrypt(event.request, meta) : new Response('Not found', { status: 404 }))
})

async function handleDecrypt(request, meta) {
  if (meta.size === null) await discoverSize(meta, request.signal)
  if (!Number.isSafeInteger(meta.size) || meta.size < 0) return new Response('Invalid file size', { status: 400 })
  if (!meta.dek) {
    if (meta.dekHex) meta.dek = await crypto.subtle.importKey('raw', hexToBytes(meta.dekHex), 'AES-GCM', false, ['decrypt'])
    else if (meta.wrappedDek && kek) meta.dek = await unwrapDEK(kek, meta.wrappedDek)
    else return new Response('No decryption key', { status: 403 })
  }
  const range = request.headers.get('Range')
  const parsed = range ? parseRange(range, meta.size) : { start: 0, end: meta.size - 1 }
  if (parsed.start === null) return new Response('Invalid range', { status: 416, headers: { 'Content-Range': `bytes */${meta.size}` } })
  const mediaRequest = !meta.download && ['video', 'audio'].includes(request.destination)
  const start = parsed.start
  // Media consumers can ask for bytes=0- even for huge files. A truthful short
  // 206 response bounds the browser-side response too, not just our JS queue.
  const end = range && mediaRequest ? Math.min(parsed.end, start + MEDIA_RANGE_BYTES - 1) : parsed.end
  const headers = {
    'Accept-Ranges': 'bytes',
    'Content-Type': meta.contentType || 'application/octet-stream',
    'Content-Length': String(Math.max(0, end - start + 1)),
  }
  if (range) headers['Content-Range'] = `bytes ${start}-${end}/${meta.size}`
  if (meta.download && meta.filename) headers['Content-Disposition'] = `attachment; filename*=UTF-8''${encodeURIComponent(meta.filename)}`
  const init = { status: range ? 206 : 200, headers }
  if (request.method === 'HEAD') return new Response(null, init)
  if (cacheable(meta)) {
    const cached = await (await caches.open(CACHE_NAME)).match(cacheKey(meta.contentHash))
    if (cached) {
      const body = range ? (await cached.blob()).slice(start, end + 1) : cached.body
      return new Response(body, init)
    }
  }
  return new Response(decryptStream(meta, start, end, request.signal), init)
}

// Thumbnail descriptors contain a key and URL, but no plaintext size. Read
// only the encryption header, derive the size from the object's Content-Length,
// then cancel this probe. Content-Length is CORS-safelisted; Content-Range may
// be hidden. The body still goes through the same bounded Range decrypt path.
async function discoverSize(meta, requestSignal) {
  const control = new AbortController()
  const abortRequest = () => control.abort(requestSignal.reason)
  meta.requests.add(control)
  requestSignal.addEventListener('abort', abortRequest, { once: true })
  try {
    if (requestSignal.aborted) abortRequest()
    if (meta.closed) control.abort(new DOMException('Decryption closed', 'AbortError'))
    control.signal.throwIfAborted()
    const response = await fetch(meta.url, { signal: control.signal, cache: 'no-store' })
    if (response.status !== 200 || !response.body) throw new Error('Could not inspect encrypted object')
    const length = Number(response.headers.get('Content-Length'))
    if (!Number.isSafeInteger(length) || length < HEADER_SIZE) throw new Error('Invalid encrypted object size')
    const header = new Uint8Array(HEADER_SIZE)
    const reader = response.body.getReader()
    try {
      for (let offset = 0; offset < header.length;) {
        const { value, done } = await reader.read()
        control.signal.throwIfAborted()
        if (done) throw new Error('Truncated encryption header')
        const part = value.subarray(0, header.length - offset)
        header.set(part, offset)
        offset += part.length
      }
    } finally { await reader.cancel().catch(() => {}) }
    control.signal.throwIfAborted()
    const chunkSize = new DataView(header.buffer).getUint32(1, false)
    if (header[0] !== 1 || chunkSize <= 0 || chunkSize > 1024 * 1024) throw new Error('Invalid encryption header')
    const payload = length - HEADER_SIZE
    const chunks = Math.ceil(payload / (chunkSize + NONCE_SIZE + TAG_SIZE))
    const size = payload - chunks * (NONCE_SIZE + TAG_SIZE)
    if (size < 0 || Math.ceil(size / chunkSize) !== chunks) throw new Error('Invalid encrypted object size')
    meta.chunkSize = chunkSize
    meta.size = size
  } finally {
    meta.requests.delete(control)
    requestSignal.removeEventListener('abort', abortRequest)
    control.abort()
  }
}

// Each response owns at most one 4 MiB ciphertext batch, six decrypt results
// and two queued plaintext chunks. All reads are started from pull(), never
// from an async start() loop. Cancellation/unregister closes upstream fetches.
function decryptStream(meta, pStart, pEnd, requestSignal) {
  const control = new AbortController()
  let output
  let stopped = false
  let cipher = null
  let cipherOffset = 0
  let nextChunk
  let lastChunk
  let acc
  const ready = []
  const abortRequest = () => control.abort(requestSignal.reason)
  const aborted = () => finish(control.signal.reason, true)

  function finish(reason, failed = false, cancelled = false) {
    if (stopped) return
    stopped = true
    meta.requests.delete(control)
    requestSignal.removeEventListener('abort', abortRequest)
    control.signal.removeEventListener('abort', aborted)
    control.abort(reason)
    cipher = null
    ready.length = 0
    if (!cancelled) {
      if (failed) output.error(reason)
      else { output.close(); tryFlushAccum(meta) }
    }
  }

  async function fetchBytes(start, end) {
    control.signal.throwIfAborted()
    const response = await fetch(meta.url, {
      headers: { Range: `bytes=${start}-${end}` }, signal: control.signal, cache: 'no-store',
    })
    if (response.status !== 206 || !response.body) throw new Error('Object store did not honor byte range')
    const contentRange = response.headers.get('Content-Range')
    if (contentRange && !contentRange.startsWith(`bytes ${start}-${end}/`)) throw new Error('Object store returned the wrong byte range')
    const expected = end - start + 1
    const result = new Uint8Array(expected)
    const reader = response.body.getReader()
    let offset = 0
    try {
      while (offset < expected) {
        const { value, done } = await reader.read()
        control.signal.throwIfAborted()
        if (done) throw new Error('Truncated encrypted range')
        if (offset + value.length > expected) throw new Error('Oversized encrypted range')
        result.set(value, offset)
        offset += value.length
      }
      return result
    } finally { await reader.cancel().catch(() => {}) }
  }

  const stream = new ReadableStream({
    start(controller) { output = controller },
    async pull(controller) {
      try {
        if (stopped) return
        if (pEnd < pStart) { finish(); return }
        if (nextChunk === undefined) {
          if (!meta.chunkSize) {
            const header = await fetchBytes(0, HEADER_SIZE - 1)
            if (header[0] !== 1) throw new Error('Invalid encryption header')
            meta.chunkSize = new DataView(header.buffer).getUint32(1, false)
          }
          if (!Number.isSafeInteger(meta.chunkSize) || meta.chunkSize <= 0 || meta.chunkSize > 1024 * 1024) throw new Error('Invalid encryption chunk size')
          nextChunk = Math.floor(pStart / meta.chunkSize)
          lastChunk = Math.floor(pEnd / meta.chunkSize)
          acc = getAccum(meta)
        }
        if (!ready.length) {
          if (nextChunk > lastChunk) { finish(); return }
          const encChunk = meta.chunkSize + NONCE_SIZE + TAG_SIZE
          if (!cipher || cipherOffset === cipher.length) {
            const chunks = Math.min(Math.floor(CIPHER_FETCH_BYTES / encChunk), lastChunk - nextChunk + 1)
            const last = nextChunk + chunks - 1
            const encStart = HEADER_SIZE + nextChunk * encChunk
            const encEnd = HEADER_SIZE + last * encChunk + Math.min(meta.chunkSize, meta.size - last * meta.chunkSize) + NONCE_SIZE + TAG_SIZE - 1
            cipher = await fetchBytes(encStart, encEnd)
            cipherOffset = 0
          }
          control.signal.throwIfAborted()
          const pending = []
          while (pending.length < DECRYPT_WINDOW && cipherOffset < cipher.length && nextChunk <= lastChunk) {
            const index = nextChunk++
            const length = Math.min(meta.chunkSize, meta.size - index * meta.chunkSize) + NONCE_SIZE + TAG_SIZE
            const bytes = cipher.subarray(cipherOffset, cipherOffset + length)
            cipherOffset += length
            pending.push(crypto.subtle.decrypt(
              { name: 'AES-GCM', iv: bytes.subarray(0, NONCE_SIZE), additionalData: uint64BE(index) },
              meta.dek, bytes.subarray(NONCE_SIZE),
            ).then(value => ({ index, plain: new Uint8Array(value) })))
          }
          const results = await Promise.allSettled(pending)
          control.signal.throwIfAborted()
          for (const result of results) {
            if (result.status === 'rejected') throw new Error('Chunk authentication failed')
            const { index, plain } = result.value
            if (acc) acc.chunks.set(index, plain)
            const offset = index * meta.chunkSize
            ready.push(plain.subarray(Math.max(0, pStart - offset), Math.min(plain.length, pEnd - offset + 1)))
          }
          if (cipherOffset === cipher.length) cipher = null
        }
        controller.enqueue(ready.shift())
        if (!ready.length && nextChunk > lastChunk) finish()
      } catch (error) { if (!stopped) finish(error, true) }
    },
    cancel(reason) { finish(reason, false, true) },
  }, { highWaterMark: 2 })
  meta.requests.add(control)
  control.signal.addEventListener('abort', aborted, { once: true })
  requestSignal.addEventListener('abort', abortRequest, { once: true })
  if (requestSignal.aborted) abortRequest()
  if (meta.closed) control.abort(new DOMException('Decryption closed', 'AbortError'))
  return stream
}

async function unwrapDEK(key, wrappedHex) {
  const wrapped = hexToBytes(wrappedHex)
  const bytes = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: wrapped.subarray(0, NONCE_SIZE) }, key, wrapped.subarray(NONCE_SIZE),
  )
  return crypto.subtle.importKey('raw', bytes, 'AES-GCM', false, ['decrypt'])
}

function parseRange(header, totalSize) {
  const match = /^bytes=(\d*)-(\d*)$/.exec(header)
  if (!match || (!match[1] && !match[2])) return { start: null, end: null }
  let start, end
  if (!match[1]) {
    const count = Number(match[2])
    if (!Number.isSafeInteger(count) || count <= 0) return { start: null, end: null }
    start = Math.max(0, totalSize - count)
    end = totalSize - 1
  } else {
    start = Number(match[1])
    end = match[2] ? Number(match[2]) : totalSize - 1
  }
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start > end || start >= totalSize) return { start: null, end: null }
  return { start, end: Math.min(end, totalSize - 1) }
}

function hexToBytes(hex) {
  const bytes = new Uint8Array(hex.length / 2)
  for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16)
  return bytes
}

function uint64BE(n) {
  const bytes = new Uint8Array(8)
  new DataView(bytes.buffer).setBigUint64(0, BigInt(n), false)
  return bytes
}
