import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { UploadEngine, PART_BUFFER_BYTES, PART_BUFFER_COUNT } from '../src/uploads/engine.ts'

const CHUNK = 65536
const ENC_CHUNK = CHUNK + 28
function partCount(size, partSize = PART_BUFFER_BYTES) {
  let parts = 1, used = 5
  for (let offset = 0; offset < size; offset += CHUNK) {
    const length = Math.min(CHUNK, size - offset) + 28
    if (used + length > partSize) { parts++; used = 0 }
    used += length
  }
  return parts
}
function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function untilAborted(signal) {
  return new Promise((_, reject) => {
    if (signal.aborted) reject(signal.reason)
    else signal.addEventListener('abort', () => reject(signal.reason), { once: true })
  })
}
async function harness(options = {}) {
  const tasks = new Map()
  let sequence = 0
  const buffers = new Set()
  let maxAhead = 0
  const engine = new UploadEngine({
    async url(id, part, signal) {
      await options.beforeURL?.(tasks.get(id), signal)
      return `${id}/${part}`
    },
    progress(id, uploaded) { tasks.get(id).progress = uploaded },
    spool: options.spool || (async () => {
      // Only the test sink materializes ciphertext in RAM; production uses OPFS.
      const chunks = []
      return {
        write(bytes) { buffers.add(bytes.buffer); chunks.push(new Blob([bytes])) },
        async finish() { return new Blob(chunks) },
        async dispose() { chunks.length = 0 },
      }
    }),
    async put(url, body, signal) {
      const [id, part] = url.split('/')
      const state = tasks.get(id)
      await options.beforePut?.(state, signal, Number(part))
      signal.throwIfAborted()
      // Materialization is confined to the test sink, never the upload engine.
      const data = new Uint8Array(await body.arrayBuffer())
      let pos = 0
      if (Number(part) === 1) {
        assert.equal(data[0], 1)
        assert.equal(new DataView(data.buffer, data.byteOffset).getUint32(1), CHUNK)
        pos = 5
      }
      assert.equal(Number(part), state.parts + 1)
      while (pos < data.length) {
        const end = Math.min(pos + ENC_CHUNK, data.length)
        const aad = new Uint8Array(8)
        new DataView(aad.buffer).setBigUint64(0, BigInt(state.ordinal++))
        const plain = new Uint8Array(await crypto.subtle.decrypt(
          { name: 'AES-GCM', iv: data.subarray(pos, pos + 12), additionalData: aad },
          state.key, data.subarray(pos + 12, end),
        ))
        state.resultHash.update(plain)
        state.uploadedPlain += plain.length
        pos = end
      }
      state.parts++
      state.uploadedCipher += data.length
    },
  })
  return {
    engine, tasks, buffers,
    get maxAhead() { return maxAhead },
    async start(size, { partSize = PART_BUFFER_BYTES, paused = false, onRead } = {}) {
      const id = String(++sequence)
      const dek = crypto.getRandomValues(new Uint8Array(32))
      const state = {
        id, size, key: await crypto.subtle.importKey('raw', dek, 'AES-GCM', false, ['decrypt']),
        sourceHash: createHash('sha256'), resultHash: createHash('sha256'),
        readBytes: 0, reads: 0, maxRead: 0, parts: 0, ordinal: 0, uploadedPlain: 0, uploadedCipher: 0,
      }
      const file = {
        size,
        slice(start, end) { return { async arrayBuffer() {
          assert.ok(end - start <= 6 * CHUNK)
          const bytes = new Uint8Array(end - start)
          for (let i = 0; i < bytes.length; i++) {
            const offset = start + i
            bytes[i] = (offset * 31 + Math.floor(offset / CHUNK) * 17) & 255
          }
          // Replayed reads after pause must not duplicate the reference hash.
          if (end > state.readBytes) state.sourceHash.update(bytes.subarray(Math.max(0, state.readBytes - start)))
          state.readBytes = Math.max(state.readBytes, end)
          state.reads++
          state.maxRead = Math.max(state.maxRead, bytes.length)
          const ahead = [...tasks.values()].reduce((sum, task) => sum + task.readBytes - task.uploadedPlain, 0)
          maxAhead = Math.max(maxAhead, ahead)
          onRead?.(state)
          return bytes.buffer
        } } },
      }
      state.assertComplete = hash => {
        assert.equal(hash, state.sourceHash.digest('hex'))
        assert.equal(state.resultHash.digest('hex'), hash)
        assert.equal(state.uploadedPlain, size)
        assert.equal(state.parts, partCount(size, partSize))
        assert.equal(state.uploadedCipher, 5 + size + Math.ceil(size / CHUNK) * 28)
      }
      tasks.set(id, state)
      state.run = engine.upload({ id, file, dekRaw: dek.buffer, partSize, totalParts: partCount(size, partSize), paused })
      // Tests may intentionally hold a rejection while exercising another task.
      state.run.catch(() => {})
      return state
    },
  }
}

test('wire framing, ordinal authentication, hash and part boundaries', async () => {
  const h = await harness()
  for (const size of [0, 1, CHUNK - 1, CHUNK, CHUNK + 1, 6 * CHUNK, 6 * CHUNK + 1, 127 * CHUNK, 127 * CHUNK + 1]) {
    const task = await h.start(size)
    task.assertComplete(await task.run)
  }
  assert.ok(h.buffers.size <= PART_BUFFER_COUNT, 'multipart buffers must be reused')
})

test('many files share two buffers and overlap filling with sending without unbounded reads', async (t) => {
  const gate = deferred()
  const sending = deferred()
  const h = await harness({ beforePut: async () => { sending.resolve(); await gate.promise } })
  const tasks = await Promise.all(Array.from({ length: 8 }, () => h.start(24 * 1024 * 1024 + 12345)))
  await sending.promise
  await delay(100)
  const readBytes = tasks.reduce((n, task) => n + task.readBytes, 0)
  assert.ok(readBytes <= PART_BUFFER_COUNT * PART_BUFFER_BYTES)
  const reads = tasks.reduce((n, task) => n + task.reads, 0)
  await delay(40)
  assert.equal(tasks.reduce((n, task) => n + task.reads, 0), reads, 'full capacity stops all producers')
  gate.resolve()
  for (const task of tasks) task.assertComplete(await task.run)
  assert.equal(h.buffers.size, PART_BUFFER_COUNT)
  assert.ok(h.maxAhead <= PART_BUFFER_COUNT * PART_BUFFER_BYTES)
  t.diagnostic(`8 files: aggregate max read-ahead=${h.maxAhead}; backing buffers=${h.buffers.size}`)
})

test('pause aborts PUT, releases capacity, and resumes from acknowledged hash/ordinal checkpoint', async () => {
  const blocked = deferred()
  let stop = true
  const h = await harness({ beforePut: async (task, signal, part) => {
    if (task.id === '1' && part === 2 && stop) { blocked.resolve(); await untilAborted(signal) }
  } })
  const first = await h.start(24 * 1024 * 1024)
  await blocked.promise
  h.engine.pause(first.id)
  const second = await h.start(20 * 1024 * 1024)
  second.assertComplete(await second.run)
  const reads = first.reads
  await delay(10)
  assert.equal(first.reads, reads)
  assert.equal(first.parts, 1)
  stop = false
  h.engine.resume(first.id)
  first.assertComplete(await first.run)
})

test('cancel during URL wait, pool wait and initial pause releases every promise', async () => {
  const blocked = deferred()
  const h = await harness({ beforeURL: async (task, signal) => {
    if (task.id === '1') { blocked.resolve(); await untilAborted(signal) }
  } })
  const first = await h.start(24 * 1024 * 1024)
  await blocked.promise
  const second = await h.start(24 * 1024 * 1024)
  const paused = await h.start(24 * 1024 * 1024, { paused: true })
  for (const task of [first, second, paused]) h.engine.cancel(task.id)
  assert.deepEqual(await Promise.all([first.run, second.run, paused.run]), [null, null, null])
  assert.equal(paused.reads, 0)
  const next = await h.start(12 * 1024 * 1024)
  next.assertComplete(await next.run)
})

test('source/PUT failure cannot strand another file or leak capacity', async () => {
  const h = await harness({ beforePut: task => { if (task.id === '1') throw new Error('PUT failed') } })
  const bad = await h.start(24 * 1024 * 1024)
  const sourceError = await h.start(24 * 1024 * 1024, { onRead: () => { throw new Error('source failed') } })
  const good = await h.start(20 * 1024 * 1024)
  await assert.rejects(bad.run, /PUT failed/)
  await assert.rejects(sourceError.run, /source failed/)
  good.assertComplete(await good.run)
})

test('rapid resume then pause remains paused and all paused tasks yield capacity to a new file', async () => {
  const h = await harness({ beforePut: async (task, signal) => {
    if (['1', '2'].includes(task.id)) await untilAborted(signal)
  } })
  const paused = await h.start(CHUNK, { paused: true })
  h.engine.resume(paused.id)
  h.engine.pause(paused.id)
  await delay(20)
  assert.equal(paused.reads, 0)
  const other = await h.start(24 * 1024 * 1024)
  await delay(40)
  h.engine.pause(other.id)
  const next = await h.start(16 * 1024 * 1024)
  next.assertComplete(await next.run)
  h.engine.cancel(paused.id)
  h.engine.cancel(other.id)
  assert.deepEqual(await Promise.all([paused.run, other.run]), [null, null])
})

test('disk quota failures dispose the partial spool and release its capacity', async () => {
  let live = 0
  let fail = true
  const h = await harness({ spool: async () => {
    live++
    const chunks = []
    return {
      write(bytes) {
        if (fail) { fail = false; throw new DOMException('Disk full', 'QuotaExceededError') }
        chunks.push(new Blob([bytes]))
      },
      async finish() { return new Blob(chunks) },
      async dispose() { chunks.length = 0; live-- },
    }
  } })
  const bad = await h.start(20 * 1024 * 1024, { partSize: 12 * 1024 * 1024 })
  await assert.rejects(bad.run, { name: 'QuotaExceededError' })
  assert.equal(live, 0)
  const next = await h.start(12 * 1024 * 1024)
  next.assertComplete(await next.run)
})

test('oversized S3 parts stage ciphertext while retaining fixed-size memory buffers', async () => {
  // A disk-backed test spool exercises the production large-part path.
  const { mkdtemp, mkdir, rm } = await import('node:fs/promises')
  const { openSync, writeSync, closeSync, openAsBlob } = await import('node:fs')
  const { homedir } = await import('node:os')
  const root = `${homedir()}/tmp`
  await mkdir(root, { recursive: true })
  const directory = await mkdtemp(`${root}/domus-upload-test-`)
  let serial = 0, live = 0
  try {
    const h = await harness({ spool: async () => {
      const path = `${directory}/${++serial}.cipher`
      let fd = openSync(path, 'wx', 0o600)
      live++
      return {
        write(bytes) { assert.ok(bytes.length <= PART_BUFFER_BYTES); assert.equal(writeSync(fd, bytes), bytes.length) },
        async finish() { closeSync(fd); fd = undefined; return openAsBlob(path) },
        async dispose() { if (fd !== undefined) closeSync(fd); await rm(path); live-- },
      }
    } })
    const task = await h.start(30 * 1024 * 1024 + 123, { partSize: 12 * 1024 * 1024 })
    task.assertComplete(await task.run)
    assert.equal(live, 0)
    assert.ok(serial > 1)
  } finally { await rm(directory, { recursive: true, force: true }) }
})

test('large-file streaming roundtrip', { skip: !process.env.UPLOAD_TEST_BYTES }, async (t) => {
  let peakRSS = 0
  const h = await harness({ beforePut: async () => { peakRSS = Math.max(peakRSS, process.memoryUsage().rss); await delay(1) } })
  const count = Number(process.env.UPLOAD_TEST_FILES || 1)
  const tasks = await Promise.all(Array.from({ length: count }, () => h.start(Number(process.env.UPLOAD_TEST_BYTES))))
  for (const task of tasks) task.assertComplete(await task.run)
  assert.ok(h.maxAhead <= PART_BUFFER_COUNT * PART_BUFFER_BYTES)
  t.diagnostic(`files=${count}; max read-ahead=${h.maxAhead}; buffers=${h.buffers.size}; sampled RSS=${peakRSS}`)
})
