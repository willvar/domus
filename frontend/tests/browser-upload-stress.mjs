// Optional real-browser stress test. Uses a sparse disk file and a streaming
// loopback receiver; no application account or object-store credentials.
import assert from 'node:assert/strict'
import { createDecipheriv, createHash } from 'node:crypto'
import { createServer } from 'node:http'
import { mkdir, mkdtemp, open, readFile, rm } from 'node:fs/promises'
import { homedir } from 'node:os'
import { chromium } from '@playwright/test'
import { spawn } from 'node:child_process'
import { once } from 'node:events'

const size = Number(process.env.UPLOAD_TEST_BYTES || 64 * 1024 * 1024)
const count = Number(process.env.UPLOAD_TEST_FILES || 2)
assert.ok(Number.isSafeInteger(size) && size > 0)
assert.ok(Number.isSafeInteger(count) && count > 0)
const states = Array.from({ length: count }, (_, id) => ({
  key: Buffer.alloc(32, id + 1), hash: createHash('sha256'), offset: 0, ordinal: 0,
  header: false, residual: Buffer.alloc(0),
}))
let receivedError
const server = createServer((request, response) => {
  response.setHeader('Access-Control-Allow-Origin', '*')
  response.setHeader('Access-Control-Allow-Methods', 'PUT, OPTIONS')
  response.setHeader('Access-Control-Allow-Headers', '*')
  if (request.method === 'OPTIONS') { response.writeHead(204).end(); return }
  const state = states[Number(request.url.split('/')[1])]
  if (!state || request.method !== 'PUT') { response.writeHead(404).end(); return }
  request.on('data', value => {
    if (receivedError) return
    try {
      state.residual = Buffer.concat([state.residual, value])
      if (!state.header) {
        if (state.residual.length < 5) return
        assert.equal(state.residual[0], 1)
        assert.equal(state.residual.readUInt32BE(1), 65536)
        state.residual = state.residual.subarray(5)
        state.header = true
      }
      while (state.offset < size) {
        const length = Math.min(65536, size - state.offset)
        if (state.residual.length < length + 28) break
        const segment = state.residual.subarray(0, length + 28)
        const aad = Buffer.alloc(8)
        aad.writeBigUInt64BE(BigInt(state.ordinal++))
        const decipher = createDecipheriv('aes-256-gcm', state.key, segment.subarray(0, 12))
        decipher.setAAD(aad)
        decipher.setAuthTag(segment.subarray(-16))
        state.hash.update(decipher.update(segment.subarray(12, -16)))
        state.hash.update(decipher.final())
        state.offset += length
        state.residual = state.residual.subarray(length + 28)
      }
    } catch (error) { receivedError = error }
  })
  request.on('end', () => response.writeHead(receivedError ? 400 : 200).end())
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const diskRoot = `${homedir()}/tmp`
await mkdir(diskRoot, { recursive: true })
const directory = await mkdtemp(`${diskRoot}/domus-browser-upload-`)
let browser, socket, timer
let peakPSS = 0, samples = 0, sampling = false, memoryError
let samplePromise = Promise.resolve()
const memorySamples = []
try {
  const path = `${directory}/source.bin`
  const file = await open(path, 'wx', 0o600)
  await file.truncate(size)
  await file.close()
  // Do not enable CDP Network recording: retaining upload request bodies in the
  // inspector would measure the test recorder instead of normal browser use.
  browser = spawn(chromium.executablePath(), [
    '--headless', '--no-sandbox', '--remote-debugging-port=0',
    `--user-data-dir=${directory}/profile`, '--no-first-run', 'about:blank',
  ], { env: { ...process.env, TMPDIR: directory }, stdio: ['ignore', 'ignore', 'pipe'] })
  const endpoint = await new Promise((resolve, reject) => {
    let stderr = ''
    browser.stderr.on('data', chunk => {
      stderr += chunk
      const match = stderr.match(/DevTools listening on (ws:\/\/\S+)/)
      if (match) resolve(match[1])
    })
    browser.once('error', reject)
    browser.once('exit', () => reject(new Error('Chromium exited before opening CDP')))
  })
  socket = new WebSocket(endpoint)
  await once(socket, 'open')
  let sequence = 0
  const pending = new Map()
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data)
    const call = pending.get(message.id)
    if (!call) return
    pending.delete(message.id)
    message.error ? call.reject(new Error(message.error.message)) : call.resolve(message.result)
  })
  socket.addEventListener('close', () => {
    for (const call of pending.values()) call.reject(new Error('Chromium CDP closed'))
    pending.clear()
  })
  const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++sequence
    pending.set(id, { resolve, reject })
    socket.send(JSON.stringify({ id, method, params, sessionId }))
  })
  const sample = async () => {
    if (sampling) return
    sampling = true
    try {
      const { processInfo } = await send('SystemInfo.getProcessInfo')
      let pss = 0
      const processes = []
      for (const process of processInfo) {
        const status = await readFile(`/proc/${process.id}/smaps_rollup`, 'utf8').catch(() => '')
        const bytes = Number(status.match(/^Pss:\s+(\d+)/m)?.[1] || 0) * 1024
        pss += bytes
        processes.push({ type: process.type, bytes })
      }
      peakPSS = Math.max(peakPSS, pss)
      samples++
      if (samples === 1 || samples % 10 === 0 || pss > 1024 ** 3) memorySamples.push({ pss, received: states.reduce((sum, state) => sum + state.offset, 0), processes })
      if (pss > 1024 ** 3) {
        memoryError = new Error('Chromium exceeded the 1 GiB stress-test memory ceiling')
        await send('Browser.close')
      }
    } catch { /* browser may be closing */ }
    finally { sampling = false }
  }
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
  const evaluate = async expression => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, sessionId)
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text)
    return result.result.value
  }
  await send('Page.navigate', { url: process.env.DOMUS_E2E_BASE_URL || 'http://127.0.0.1:8089/' }, sessionId)
  await evaluate(`new Promise(resolve => document.readyState === 'complete' ? resolve() : window.addEventListener('load', resolve, { once: true }))`)
  await evaluate(`(${(() => {
    const input = document.createElement('input')
    input.id = 'stress-source'
    input.type = 'file'
    document.body.appendChild(input)
  }).toString()})()`)
  const { root } = await send('DOM.getDocument', {}, sessionId)
  const { nodeId } = await send('DOM.querySelector', { nodeId: root.nodeId, selector: '#stress-source' }, sessionId)
  await send('DOM.setFileInputFiles', { nodeId, files: [path] }, sessionId)
  await sample()
  timer = setInterval(() => { if (!sampling) samplePromise = sample() }, 1000)
  const started = performance.now()
  const upload = async ({ count, port }) => {
    const { UploadClient } = await import('/src/uploads/client.ts')
    const client = new UploadClient()
    const file = document.querySelector('#stress-source').files[0]
    let totalParts = 1, used = 5
    for (let offset = 0; offset < file.size; offset += 65536) {
      const length = Math.min(65536, file.size - offset) + 28
      if (used + length > 8388608) { totalParts++; used = 0 }
      used += length
    }
    return Promise.all(Array.from({ length: count }, (_, index) => client.upload({
      id: String(index), file, dekRaw: new Uint8Array(32).fill(index + 1).buffer,
      partSize: 8388608, totalParts,
    }, {
      url: async part => `http://127.0.0.1:${port}/${index}/${part}`,
      progress() {}, phase() {},
    }, new AbortController().signal)))
  }
  const hashes = await evaluate(`(${upload.toString()})(${JSON.stringify({ count, port: server.address().port })})`)
  if (memoryError) throw memoryError
  if (receivedError) throw receivedError
  for (let i = 0; i < count; i++) {
    assert.equal(states[i].offset, size)
    assert.equal(states[i].residual.length, 0)
    assert.equal(states[i].hash.digest('hex'), hashes[i])
  }
  clearInterval(timer)
  await samplePromise
  await sample()
  if (memoryError) throw memoryError
  console.log(JSON.stringify({ files: count, bytesPerFile: size, seconds: (performance.now() - started) / 1000, chromiumPeakPSS: peakPSS, samples, memorySamples, authenticatedRoundtrip: true }))
} catch (error) {
  console.error(JSON.stringify({ chromiumPeakPSS: peakPSS, samples, memorySamples }))
  throw memoryError || receivedError || error
} finally {
  clearInterval(timer)
  socket?.close()
  if (browser && browser.exitCode === null) {
    const exited = once(browser, 'exit')
    browser.kill('SIGTERM')
    await exited
  }
  server.closeAllConnections()
  await new Promise(resolve => server.close(resolve))
  await rm(directory, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
}
