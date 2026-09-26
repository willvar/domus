import assert from 'node:assert/strict'
import { test } from 'node:test'
import { setImmediate as flush } from 'node:timers/promises'

// Exercise the real Axios XHR adapter and axios-retry with a controllable
// network and clock, including native total-timeout behavior.
const requests = []
class FakeXHR extends EventTarget {
  upload = new EventTarget()
  onloadend = null
  status = 200
  statusText = 'OK'
  responseText = ''
  aborted = false
  open() {}
  setRequestHeader() {}
  getAllResponseHeaders() { return '' }
  send(body) {
    this.body = body
    requests.push(this)
    if (this.timeout) this.timer = setTimeout(() => this.ontimeout(), this.timeout)
  }
  progress(loaded) {
    this.upload.dispatchEvent(Object.assign(new Event('progress'), { loaded, total: 100, lengthComputable: true }))
  }
  finish() {
    clearTimeout(this.timer)
    this.upload.dispatchEvent(new Event('loadend'))
    this.onloadend()
  }
  abort() {
    this.aborted = true
    clearTimeout(this.timer)
    this.onabort?.()
  }
}
globalThis.XMLHttpRequest = FakeXHR
const { putPart } = await import('../src/uploads/transport.ts')

function setup(t) {
  requests.length = 0
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: Date.now() })
  const control = new AbortController()
  t.after(async () => { control.abort(); await flush() })
  const body = new Blob(['immutable ciphertext'])
  const result = { settled: false, error: undefined }
  const done = putPart('https://upload.test/part', body, control.signal).then(
    () => { result.settled = true },
    error => { result.settled = true; result.error = error },
  )
  const tick = async ms => { t.mock.timers.tick(ms); await flush() }
  return { control, body, result, done, tick }
}

test('a progressing PUT can exceed two minutes and waits for its acknowledgement', async t => {
  const { result, done, tick } = setup(t)
  await flush()
  for (let loaded = 10; loaded <= 100; loaded += 10) {
    await tick(30000)
    requests[0].progress(loaded)
    await flush()
    assert.equal(result.settled, false)
    assert.equal(requests[0].aborted, false)
    assert.equal(requests.length, 1)
  }
  await tick(119999)
  assert.equal(result.settled, false)
  requests[0].finish()
  await done
  assert.equal(result.error, undefined)
  await tick(120001)
  assert.equal(requests[0].aborted, false)
  assert.equal(requests.length, 1)
})

for (const phase of ['connect', 'upload', 'acknowledgement']) {
  test(`a stalled ${phase} aborts and retries the same body with a fresh deadline`, async t => {
    const { body, result, done, tick } = setup(t)
    await flush()
    if (phase !== 'connect') requests[0].progress(phase === 'upload' ? 10 : 100)
    await flush()
    await tick(60000)
    // Duplicate progress notifications cannot postpone a real stall.
    if (phase === 'upload') requests[0].progress(10)
    await flush()
    await tick(60000)
    assert.equal(requests[0].aborted, true)
    assert.equal(result.settled, false)
    await tick(2500)
    assert.equal(requests.length, 2)
    assert.equal(requests[1].body, body)
    assert.equal(requests[1].aborted, false)
    requests[1].progress(100)
    requests[1].finish()
    await done
    assert.equal(result.error, undefined)
    await tick(120001)
    assert.equal(requests.length, 2)
    assert.equal(requests[1].aborted, false)
  })
}

for (const phase of ['sending', 'backoff']) {
  test(`user cancellation during ${phase} clears the deadline and prevents retries`, async t => {
    const { control, result, done, tick } = setup(t)
    await flush()
    if (phase === 'backoff') await tick(120000)
    control.abort()
    await done
    assert.equal(result.error?.code, 'ERR_CANCELED')
    assert.equal(requests[0].aborted, true)
    await tick(240000)
    assert.equal(requests.length, 1)
  })
}
