import assert from 'node:assert/strict'
import { createCipheriv } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import vm from 'node:vm'

const source = await readFile(new URL('../public/sw.js', import.meta.url), 'utf8')
const CHUNK = 65536
const ENC = CHUNK + 28
const key = Buffer.alloc(32, 7)

function plaintext(index, length) {
  const bytes = Buffer.alloc(length)
  for (let i = 0; i < length; i++) bytes[i] = (index * 17 + i * 31) & 255
  return bytes
}

async function harness(size, options = {}) {
  const listeners = new Map()
  const cache = new Map()
  const ranges = []
  let readBytes = 0, offline = false, aborted = 0
  let cached
  const cacheStored = new Promise(resolve => { cached = resolve })
  const context = vm.createContext({
    crypto, Request, Response, Blob, URL, AbortController, DOMException,
    Uint8Array, DataView, ReadableStream, console, setTimeout: () => 0, clearTimeout() {},
    self: { addEventListener(type, callback) { listeners.set(type, callback) } },
    caches: { async open() { return {
      async match(request) { return cache.get(request.url)?.clone() },
      async put(request, response) { cache.set(request.url, new Response(await response.arrayBuffer())); cached() },
      async delete(request) { return cache.delete(request.url) },
    } }, async delete() { cache.clear() } },
    async fetch(_url, { headers, signal }) {
      if (offline) throw new Error('Offline')
      const encryptedSize = 5 + size + Math.ceil(size / CHUNK) * 28
      const range = headers?.Range ? /^bytes=(\d+)-(\d+)$/.exec(headers.Range) : null
      const start = range ? Number(range[1]) : 0, end = range ? Number(range[2]) : encryptedSize - 1
      ranges.push([start, end])
      signal.addEventListener('abort', () => { aborted++ }, { once: true })
      if (options.ignoreRange) return new Response('ignored', { status: 200 })
      let pos = start
      const body = new ReadableStream({
        pull(controller) {
          signal.throwIfAborted()
          if (pos > end) { controller.close(); return }
          if (options.readLimit && readBytes >= options.readLimit) throw new Error('Read-ahead exceeded test ceiling')
          let bytes
          if (pos === 0) bytes = Buffer.from([1, 0, 1, 0, 0])
          else {
            const index = (pos - 5) / ENC
            assert.ok(Number.isInteger(index))
            const iv = Buffer.alloc(12)
            iv.writeBigUInt64BE(BigInt(index), 4)
            const aad = Buffer.alloc(8)
            aad.writeBigUInt64BE(BigInt(index))
            const cipher = createCipheriv('aes-256-gcm', key, iv)
            cipher.setAAD(aad)
            bytes = Buffer.concat([iv, cipher.update(plaintext(index, Math.min(CHUNK, size - index * CHUNK))), cipher.final(), cipher.getAuthTag()])
            if (options.corrupt) bytes[bytes.length - 1] ^= 1
          }
          bytes = bytes.subarray(0, end - pos + 1)
          pos += bytes.length
          readBytes += bytes.length
          controller.enqueue(bytes)
        },
      }, { highWaterMark: 0 })
      return new Response(body, { status: range ? 206 : 200, headers: {
        ...(!options.missingLength ? { 'Content-Length': String(options.invalidLength ?? end - start + 1) } : {}),
        ...(range ? { 'Content-Range': `bytes ${start}-${end}/${encryptedSize}` } : {}),
      } })
    },
  })
  // Resolve the cache key against the test origin, like the browser does.
  context.Request = class extends Request { constructor(url, init) { super(new URL(url, 'https://test.local'), init) } }
  vm.runInContext(source, context)
  await listeners.get('message')({ data: { type: 'register', id: 'file', metadata: {
    url: 'https://object.local/file', size: options.unknownSize ? null : size, chunkSize: options.header ? 0 : CHUNK,
    contentType: options.cache ? 'application/pdf' : 'video/mp4',
    contentHash: options.cache ? 'fixture' : '', dek: key.toString('hex'),
  } } })
  return {
    ranges,
    cacheStored,
    get readBytes() { return readBytes },
    get aborted() { return aborted },
    set offline(value) { offline = value },
    async request(range, destination = '') {
      let response
      const request = new Request('https://test.local/__decrypt__/file', { headers: range ? { Range: range } : {} })
      Object.defineProperty(request, 'destination', { value: destination })
      listeners.get('fetch')({ request, respondWith(value) { response = value } })
      return response
    },
    unregister() { return listeners.get('message')({ data: { type: 'unregister', id: 'file' } }) },
  }
}

for (const range of [undefined, 'bytes=0-']) test(`8 GiB slow consumer applies backpressure (${range || 'full'})`, async () => {
  const h = await harness(8 * 1024 ** 3, { readLimit: 16 * 1024 ** 2 })
  const response = await h.request(range)
  assert.equal(response.status, range ? 206 : 200)
  const reader = response.body.getReader()
  assert.equal((await reader.read()).value.length, CHUNK)
  await delay(60)
  const stoppedAt = h.readBytes
  assert.ok(stoppedAt <= 4 * 1024 ** 2, `read ahead ${stoppedAt}`)
  await delay(40)
  assert.equal(h.readBytes, stoppedAt)
  await reader.cancel()
  assert.ok(h.aborted > 0)
})

test('media open-ended response is finite and suffix seeks preserve full-file ordinals', async () => {
  const size = 8 * 1024 ** 3 + 137
  const h = await harness(size)
  const response = await h.request('bytes=0-', 'video')
  assert.equal(response.status, 206)
  assert.equal(response.headers.get('Content-Range'), `bytes 0-4194303/${size}`)
  await response.body.cancel()
  const tail = await h.request('bytes=-93')
  const bytes = Buffer.from(await tail.arrayBuffer())
  assert.deepEqual(bytes, plaintext(Math.floor(size / CHUNK), 137).subarray(44))
  assert.ok(h.ranges.every(([a, b]) => b - a + 1 <= 4 * 1024 ** 2))
})

test('full/range reads cross batches, preserve tails, and reuse small-file cache offline', async () => {
  const size = 9 * 1024 ** 2 + 137
  const h = await harness(size, { header: true, cache: true })
  const response = await h.request()
  const bytes = Buffer.from(await response.arrayBuffer())
  const expected = Buffer.concat(Array.from({ length: Math.ceil(size / CHUNK) }, (_, i) => plaintext(i, Math.min(CHUNK, size - i * CHUNK))))
  assert.deepEqual(bytes, expected)
  await h.cacheStored
  h.offline = true
  const range = await h.request('bytes=65520-65600')
  assert.deepEqual(Buffer.from(await range.arrayBuffer()), expected.subarray(65520, 65601))
  assert.equal((await h.request('bytes=0-')).status, 206)
  assert.equal((await h.request('bytes=garbage-')).status, 416)
})

test('unregister errors an outstanding response and aborts upstream', async () => {
  const h = await harness(8 * 1024 ** 3)
  const response = await h.request('bytes=0-')
  const reader = response.body.getReader()
  await reader.read()
  await h.unregister()
  await assert.rejects(reader.read(), { name: 'AbortError' })
  assert.ok(h.aborted > 0)
})

test('ignored ranges and corrupt ciphertext fail closed', async () => {
  for (const options of [{ ignoreRange: true }, { corrupt: true }]) {
    const h = await harness(CHUNK, options)
    const response = await h.request('bytes=0-')
    await assert.rejects(response.arrayBuffer())
    assert.ok(h.aborted > 0)
  }
})

test('unknown-size thumbnails discover plaintext geometry, including full and partial tail chunks', async () => {
  for (const size of [137, CHUNK, 2 * CHUNK + 93]) {
    const h = await harness(size, { unknownSize: true, header: true })
    const response = await h.request()
    assert.equal(response.headers.get('Content-Length'), String(size))
    const expected = Buffer.concat(Array.from({ length: Math.ceil(size / CHUNK) }, (_, i) => plaintext(i, Math.min(CHUNK, size - i * CHUNK))))
    assert.deepEqual(Buffer.from(await response.arrayBuffer()), expected)
    assert.equal(h.readBytes, 5 + size + Math.ceil(size / CHUNK) * 28)
    assert.deepEqual(Buffer.from(await (await h.request('bytes=-20')).arrayBuffer()), expected.subarray(-20))
  }
})

test('unknown-size discovery cancels its probe without draining a large object', async () => {
  const h = await harness(8 * 1024 ** 3, { unknownSize: true, header: true, readLimit: 8 * 1024 ** 2 })
  const response = await h.request('bytes=-93')
  assert.deepEqual(Buffer.from(await response.arrayBuffer()), plaintext(8 * 1024 ** 3 / CHUNK - 1, CHUNK).subarray(-93))
  assert.equal(h.readBytes, 5 + ENC)
})

test('empty files stay empty and invalid unknown-size descriptors fail without draining their body', async () => {
  for (const unknownSize of [false, true]) {
    const h = await harness(0, { unknownSize, header: true })
    const response = await h.request()
    assert.equal(response.headers.get('Content-Length'), '0')
    assert.equal((await response.arrayBuffer()).byteLength, 0)
    assert.equal(h.readBytes, unknownSize ? 5 : 0)
  }
  for (const options of [{ missingLength: true }, { invalidLength: 6 }]) {
    const h = await harness(CHUNK, { ...options, unknownSize: true, header: true })
    await assert.rejects(h.request(), /Invalid encrypted object size/)
    assert.ok(h.readBytes <= 5)
    assert.ok(h.aborted > 0)
  }
})
