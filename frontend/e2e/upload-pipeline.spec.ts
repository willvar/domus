import { createDecipheriv, createHash } from 'node:crypto'
import { expect, test, type WebSocketRoute } from '@playwright/test'

test('shared worker sends exact disk parts, retries immutable ciphertext and reclaims unlocked leftovers', async ({ page }) => {
  const bodies = new Map<string, Map<number, Buffer>>()
  let retryBody: Buffer | undefined
  let retried = false
  await page.route('**/__upload_pipeline_test__/**', async route => {
    const request = route.request()
    const [id, number] = new URL(request.url()).pathname.split('/').slice(-2)
    const body = request.postDataBuffer()!
    expect(body).not.toBeNull()
    if (id === 'small' && number === '1' && !retried) {
      retryBody = body
      retried = true
      await route.fulfill({ status: 503, body: '' })
      return
    }
    if (id === 'small' && number === '1') expect(body.equals(retryBody!)).toBeTruthy()
    const parts = bodies.get(id) || new Map<number, Buffer>()
    parts.set(Number(number), body)
    bodies.set(id, parts)
    await new Promise(resolve => setTimeout(resolve, 15))
    await route.fulfill({ status: 200, body: '' })
  })
  await page.goto('/')
  await page.evaluate(async () => {
    const directory = await (await navigator.storage.getDirectory()).getDirectoryHandle('domus-upload-parts', { create: true })
    await directory.getFileHandle('orphan.cipher', { create: true })
    await new Promise<void>(resolve => {
      void navigator.locks.request('domus-upload-part:live.cipher', async () => {
        await directory.getFileHandle('live.cipher', { create: true })
        return new Promise<void>(release => { (window as any).__releaseSpool = release; resolve() })
      })
    })
  })
  const result = await page.evaluate(async () => {
    const moduleURL = '/src/uploads/client.ts'
    const { UploadClient } = await import(moduleURL)
    const uploader = new UploadClient()
    const inputs = [
      { id: 'small', size: 9 * 1024 * 1024 + 73, partSize: 8 * 1024 * 1024, byte: 11 },
      { id: 'large', size: 13 * 1024 * 1024 + 37, partSize: 12 * 1024 * 1024, byte: 23 },
    ]
    return Promise.all(inputs.map(async input => {
      const file = new File([new Uint8Array(input.size).fill(input.byte)], `${input.id}.bin`)
      const hash = await uploader.upload({
        id: input.id, file, dekRaw: new Uint8Array(32).fill(input.byte).buffer,
        partSize: input.partSize, totalParts: 2,
      }, {
        url: async (part: number) => `${location.origin}/__upload_pipeline_test__/${input.id}/${part}`,
        progress() {}, phase() {},
      }, new AbortController().signal)
      return { ...input, hash }
    }))
  })
  expect(retried).toBeTruthy()
  for (const input of result) {
    const parts = bodies.get(input.id)!
    expect(parts.size).toBe(2)
    const wire = Buffer.concat([...parts].sort(([a], [b]) => a - b).map(([, body]) => body))
    expect(wire.length).toBe(5 + input.size + Math.ceil(input.size / 65536) * 28)
    expect(wire.readUInt32BE(1)).toBe(65536)
    const hash = createHash('sha256')
    let ordinal = 0
    for (let pos = 5; pos < wire.length; pos += 65564) {
      const segment = wire.subarray(pos, Math.min(pos + 65564, wire.length))
      const aad = Buffer.alloc(8)
      aad.writeBigUInt64BE(BigInt(ordinal++))
      const decipher = createDecipheriv('aes-256-gcm', Buffer.alloc(32, input.byte), segment.subarray(0, 12))
      decipher.setAAD(aad)
      decipher.setAuthTag(segment.subarray(-16))
      const plain = Buffer.concat([decipher.update(segment.subarray(12, -16)), decipher.final()])
      expect(plain.every(byte => byte === input.byte)).toBeTruthy()
      hash.update(plain)
    }
    expect(hash.digest('hex')).toBe(input.hash)
  }
  const stagedFiles = await page.evaluate(async () => {
    const root = await navigator.storage.getDirectory()
    const directory = await root.getDirectoryHandle('domus-upload-parts')
    const names = []
    for await (const [name] of (directory as any)) names.push(name)
    return names
  })
  expect(stagedFiles).toEqual(['live.cipher'])
  await page.evaluate(async () => {
    ;(window as any).__releaseSpool()
    const directory = await (await navigator.storage.getDirectory()).getDirectoryHandle('domus-upload-parts')
    await directory.removeEntry('live.cipher')
    delete (window as any).__releaseSpool
  })
})

test('upload store coordinates multiple files, pause, cancellation and thumbnail completion', async ({ page }) => {
  const uploads = new Map<string, { name: string; size: number; dek: string; parts: Map<number, Buffer> }>()
  const completed: string[] = []
  const cancelled: string[] = []
  let id = 0
  await page.route('**/file/upload', async route => {
    const body = route.request().postDataJSON()
    if (body.names) { await route.fulfill({ json: { conflicts: [] } }); return }
    if (body.upload_id) {
      const upload = uploads.get(body.upload_id)!
      const wire = Buffer.concat([...upload.parts].sort(([a], [b]) => a - b).map(([, part]) => part))
      expect(wire.length).toBe(body.encrypted_size)
      expect(wire.length).toBe(5 + upload.size + Math.ceil(upload.size / 65536) * 28)
      const hash = createHash('sha256')
      let ordinal = 0
      for (let pos = 5; pos < wire.length; pos += 65564) {
        const segment = wire.subarray(pos, Math.min(pos + 65564, wire.length))
        const aad = Buffer.alloc(8)
        aad.writeBigUInt64BE(BigInt(ordinal++))
        const decipher = createDecipheriv('aes-256-gcm', Buffer.from(upload.dek, 'hex'), segment.subarray(0, 12))
        decipher.setAAD(aad)
        decipher.setAuthTag(segment.subarray(-16))
        hash.update(decipher.update(segment.subarray(12, -16)))
        hash.update(decipher.final())
      }
      if (body.content_hash) expect(hash.digest('hex')).toBe(body.content_hash)
      completed.push(upload.name)
      await route.fulfill({ json: { generation: 1 } })
      return
    }
    const uploadID = `test-${++id}`
    uploads.set(uploadID, { name: body.file_name, size: body.file_size, dek: body.dek, parts: new Map() })
    await route.fulfill({ json: {
      upload_id: uploadID, task_id: '', dek: body.dek, part_size: 8388608,
      total_parts: body.file_size > 127 * 65536 ? 2 : 1,
    } })
  })
  await page.route('**/file/upload/presign?*', async route => {
    const url = new URL(route.request().url())
    const uploadID = url.searchParams.get('upload_id')!
    const start = Number(url.searchParams.get('start'))
    const upload = uploads.get(uploadID)!
    const count = upload.size > 127 * 65536 ? 2 : 1
    await route.fulfill({ json: { expires_in: 3600, parts: Array.from({ length: count - start + 1 }, (_, i) => ({
      part_number: start + i, presigned_url: `${url.origin}/__upload_store_test__/${uploadID}/${start + i}`,
    })) } })
  })
  await page.route('**/__upload_store_test__/**', async route => {
    const [uploadID, part] = new URL(route.request().url()).pathname.split('/').slice(-2)
    await new Promise(resolve => setTimeout(resolve, 10))
    uploads.get(uploadID)!.parts.set(Number(part), route.request().postDataBuffer()!)
    await route.fulfill({ status: 200, body: '' })
  })
  await page.route('**/file/upload/cancel', async route => {
    const upload = uploads.get(route.request().postDataJSON().upload_id)
    if (upload) cancelled.push(upload.name)
    await route.fulfill({ json: { ok: true } })
  })
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/file/upload/heartbeat', route => route.fulfill({ json: {} }))
  await page.goto('/')
  await page.locator('.login-card').waitFor()
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/upload.ts'
    const { useUploadStore } = await import(moduleURL)
    const store = useUploadStore()
    const files = new DataTransfer()
    for (const name of ['run.bin', 'pause.bin', 'cancel.bin']) {
      files.items.add(new File([new Uint8Array(9 * 1024 * 1024 + 73).fill(9)], name))
    }
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 32
    canvas.getContext('2d')!.fillRect(0, 0, 32, 32)
    const image = await new Promise<Blob>(resolve => canvas.toBlob(blob => resolve(blob!)))
    files.items.add(new File([image], 'picture.png', { type: 'image/png' }))
    await store.uploadFiles(files.files, '/')
    const paused = store.uploads.find((entry: { fileName: string }) => entry.fileName === 'pause.bin')!
    const removed = store.uploads.find((entry: { fileName: string }) => entry.fileName === 'cancel.bin')!
    store.pauseUpload(paused.id)
    await store.cancelUpload(removed.id)
  })
  await expect.poll(() => completed.includes('run.bin') && completed.includes('picture.png')).toBeTruthy()
  expect(completed.some(name => name.startsWith('thumb_'))).toBeTruthy()
  expect(completed).not.toContain('pause.bin')
  expect(completed).not.toContain('cancel.bin')
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/upload.ts'
    const { useUploadStore } = await import(moduleURL)
    const store = useUploadStore()
    const paused = store.uploads.find((entry: { fileName: string }) => entry.fileName === 'pause.bin')!
    if (paused.status !== 'paused') throw new Error('Paused state was not preserved')
    store.resumeUpload(paused.id)
  })
  await expect.poll(() => completed.includes('pause.bin')).toBeTruthy()
  await expect.poll(async () => page.evaluate(async () => {
    const moduleURL = '/src/stores/upload.ts'
    const { useUploadStore } = await import(moduleURL)
    return useUploadStore().uploads.length
  })).toBe(0)
})

test('cancelling during Axios retry backoff settles the worker and permits another upload', async ({ page }) => {
  let attempts = 0
  await page.route('**/__upload_retry_test__', async route => {
    attempts++
    await route.fulfill({ status: 503, body: '' })
  })
  await page.route('**/__upload_after_cancel__', route => route.fulfill({ status: 200, body: '' }))
  await page.goto('/')
  const failedResponse = page.waitForResponse(response => response.url().endsWith('/__upload_retry_test__'))
  await page.evaluate(async () => {
    const moduleURL = '/src/uploads/client.ts'
    const { UploadClient } = await import(moduleURL)
    const client = new UploadClient()
    const control = new AbortController()
    const run = client.upload({
      id: 'cancel', file: new File([new Uint8Array(1)], 'cancel.bin'),
      dekRaw: new Uint8Array(32).buffer, partSize: 8388608, totalParts: 1,
    }, {
      url: async () => `${location.origin}/__upload_retry_test__`, progress() {}, phase() {},
    }, control.signal)
    ;(window as any).__uploadRetryTest = { client, control, run }
  })
  await failedResponse
  const result = await page.evaluate(async () => {
    const { client, control, run } = (window as any).__uploadRetryTest
    const start = performance.now()
    control.abort()
    const cancelled = await run
    const elapsed = performance.now() - start
    const next = await client.upload({
      id: 'next', file: new File([new Uint8Array(1)], 'next.bin'),
      dekRaw: new Uint8Array(32).buffer, partSize: 8388608, totalParts: 1,
    }, {
      url: async () => `${location.origin}/__upload_after_cancel__`, progress() {}, phase() {},
    }, new AbortController().signal)
    delete (window as any).__uploadRetryTest
    return { cancelled, next, elapsed }
  })
  expect(result.cancelled).toBeNull()
  expect(result.elapsed).toBeLessThan(1500)
  expect(result.next).toBe(createHash('sha256').update(Buffer.alloc(1)).digest('hex'))
  expect(attempts).toBe(1)
})

test('cancelling while init is in flight cleans up the late reservation without reading a body', async ({ page }) => {
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  let reserved = false
  let cancelled = false
  let presigned = false
  await page.route('**/file/upload', async route => {
    const body = route.request().postDataJSON()
    if (body.names) { await route.fulfill({ json: { conflicts: [] } }); return }
    reserved = true
    await gate
    await route.fulfill({ json: { upload_id: 'late-init', task_id: '', dek: body.dek, part_size: 8388608, total_parts: 1 } })
  })
  await page.route('**/file/upload/cancel', async route => {
    expect(route.request().postDataJSON().upload_id).toBe('late-init')
    cancelled = true
    await route.fulfill({ json: {} })
  })
  await page.route('**/file/upload/presign?*', async route => { presigned = true; await route.abort() })
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.goto('/')
  await page.locator('.login-card').waitFor()
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/upload.ts'
    const { useUploadStore } = await import(moduleURL)
    const files = new DataTransfer()
    files.items.add(new File(['cancel before init response'], 'late.bin'))
    await useUploadStore().uploadFiles(files.files, '/')
  })
  await expect.poll(() => reserved).toBeTruthy()
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/upload.ts'
    const { useUploadStore } = await import(moduleURL)
    const store = useUploadStore()
    await store.cancelUpload(store.uploads[0].id)
  })
  release()
  await expect.poll(() => cancelled).toBeTruthy()
  expect(presigned).toBeFalsy()
})

test('aborted thumbnail decoding unloads media before the next decoder starts', async ({ page }) => {
  await page.goto('/')
  const result = await page.evaluate(async () => {
    const moduleURL = '/src/composables/useCryptoUpload.ts'
    const { generateThumbnail } = await import(moduleURL)
    const control = new AbortController()
    const createElement = document.createElement.bind(document)
    const revoke = URL.revokeObjectURL.bind(URL)
    let media: HTMLVideoElement | undefined
    let revoked = 0
    document.createElement = ((tag: string, options?: ElementCreationOptions) => {
      const element = createElement(tag, options)
      if (tag === 'video') {
        media = element as HTMLVideoElement
        queueMicrotask(() => control.abort())
      }
      return element
    }) as typeof document.createElement
    URL.revokeObjectURL = url => { revoked++; revoke(url) }
    try {
      await generateThumbnail(new File(['invalid'], 'cancel.mp4', { type: 'video/mp4' }), control.signal)
      const cleaned = !!media && !media.getAttribute('src') && media.readyState === HTMLMediaElement.HAVE_NOTHING
      const canvas = createElement('canvas')
      canvas.width = canvas.height = 24
      const blob = await new Promise<Blob>(resolve => canvas.toBlob(value => resolve(value!)))
      const next = await generateThumbnail(new File([blob], 'next.png', { type: 'image/png' }))
      return { cleaned, revoked, width: next?.width }
    } finally {
      document.createElement = createElement
      URL.revokeObjectURL = revoke
    }
  })
  expect(result).toEqual({ cleaned: true, revoked: 2, width: 24 })
})

for (const eventOrder of ['before-response', 'after-response']) {
  test(`completion failure preserves its HTTP error with a failed task event ${eventOrder}`, async ({ page }) => {
    let socket: WebSocketRoute | undefined
    let cancelCalls = 0
    await page.routeWebSocket('**/ws', ws => {
      socket = ws
      ws.onMessage(message => {
        const request = JSON.parse(String(message))
        if (request.id) ws.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
      })
    })
    await page.route('**/file/upload', async route => {
      const body = route.request().postDataJSON()
      if (body.names) { await route.fulfill({ json: { conflicts: [] } }); return }
      if (body.upload_id) {
        if (eventOrder === 'before-response') {
          socket!.send(JSON.stringify({ event: 'task.update', data: { task_id: 'failed-task', type: 'upload', status: 'failed' } }))
          await expect.poll(() => page.evaluate(() => (window as any).__failureEventSeen === true)).toBeTruthy()
        }
        await route.fulfill({ status: 400, json: { error: 'invalid_parts' } })
        return
      }
      await route.fulfill({ json: { upload_id: 'failed-completion', task_id: 'failed-task', dek: body.dek, part_size: 8388608, total_parts: 1 } })
    })
    await page.route('**/file/upload/presign?*', route => route.fulfill({ json: {
      expires_in: 3600, parts: [{ part_number: 1, presigned_url: `${new URL(route.request().url()).origin}/__failed_completion_body__` }],
    } }))
    await page.route('**/__failed_completion_body__', route => route.fulfill({ status: 200, body: '' }))
    await page.route('**/file/upload/cancel', async route => {
      cancelCalls++
      socket!.send(JSON.stringify({ event: 'task.update', data: {
        task_id: 'failed-task', type: 'upload', status: 'failed', name: 'failure.bin', progress: 0.99, phase: 'processing',
      } }))
      await route.fulfill({ json: {} })
    })
    await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
    await page.goto('/')
    await page.locator('.login-card').waitFor()
    await page.evaluate(async () => {
      const url = '/src/composables/useWebSocket.ts'
      const { useWebSocket } = await import(url)
      useWebSocket().on('task.update', (event: { task_id: string; status: string }) => {
        if (event.task_id === 'failed-task' && event.status === 'failed') (window as any).__failureEventSeen = true
      })
      useWebSocket().connect()
    })
    await expect.poll(() => !!socket).toBeTruthy()
    await page.evaluate(async () => {
      const url = '/src/stores/upload.ts'
      const { useUploadStore } = await import(url)
      const files = new DataTransfer()
      files.items.add(new File(['fixture'], 'failure.bin'))
      await useUploadStore().uploadFiles(files.files, '/')
    })
    await expect.poll(() => cancelCalls).toBe(1)
    await expect.poll(async () => page.evaluate(async () => {
      const url = '/src/stores/upload.ts'
      const { useUploadStore } = await import(url)
      return useUploadStore().uploads.map((entry: { status: string; error?: string }) => ({ status: entry.status, error: entry.error }))
    })).toEqual([{ status: 'failed', error: 'invalid_parts' }])
    expect(cancelCalls).toBe(1)
  })
}

test('cancelling an initialized upload sends one cleanup request after worker teardown', async ({ page }) => {
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  let signing = false
  let cancelCalls = 0
  await page.route('**/file/upload', route => {
    const body = route.request().postDataJSON()
    return route.fulfill({ json: body.names ? { conflicts: [] } : {
      upload_id: 'cancel-once', task_id: '', dek: body.dek, part_size: 8388608, total_parts: 1,
    } })
  })
  await page.route('**/file/upload/presign?*', async route => {
    signing = true
    await gate
    await route.fulfill({ json: { expires_in: 3600, parts: [] } }).catch(() => {})
  })
  await page.route('**/file/upload/cancel', async route => {
    expect(route.request().postDataJSON().upload_id).toBe('cancel-once')
    cancelCalls++
    await route.fulfill({ json: {} })
  })
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.goto('/')
  await page.locator('.login-card').waitFor()
  await page.evaluate(async () => {
    const url = '/src/stores/upload.ts'
    const { useUploadStore } = await import(url)
    const files = new DataTransfer()
    files.items.add(new File(['cancel while signing'], 'cancel.bin'))
    await useUploadStore().uploadFiles(files.files, '/')
  })
  await expect.poll(() => signing).toBeTruthy()
  await page.evaluate(async () => {
    const url = '/src/stores/upload.ts'
    const store = (await import(url)).useUploadStore()
    store.cancelUpload(store.uploads[0].id)
    if (store.uploads.length !== 0) throw new Error('Cancelled task remained visible')
  })
  release()
  await expect.poll(() => cancelCalls).toBe(1)
  await expect.poll(async () => page.evaluate(async () => (await navigator.locks.query()).held.length)).toBe(0)
  await page.waitForTimeout(250)
  expect(cancelCalls).toBe(1)
})
