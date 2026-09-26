import { createSHA256 } from 'hash-wasm'
import type { IHasher } from 'hash-wasm'
import { WireEncoder, uint64BE } from '@willvar/escrow-vue'
import { ResourcePool } from './resources.ts'
import { putPart } from './transport.ts'
import type { UploadBody } from './transport.ts'
import { createPartSpool } from './staging.ts'
import type { PartSpool } from './staging.ts'

export const PART_BUFFER_BYTES = 8 * 1024 * 1024
export const PART_BUFFER_COUNT = 2
const CHUNK = 65536
const ENC_CHUNK = CHUNK + 28
const READ_BYTES = 6 * CHUNK

export interface UploadInput {
  id: string
  file: File
  dekRaw: ArrayBuffer
  partSize: number
  totalParts: number
  paused?: boolean
}
interface Checkpoint {
  offset: number
  part: number
  uploaded: number
  hash?: Uint8Array
  digest?: string
}
interface Task {
  input: UploadInput
  committed: Checkpoint
  paused: boolean
  cancelled: boolean
  attempt?: AbortController
  wake?: () => void
}
interface Slot { bytes?: Uint8Array<ArrayBuffer>; hasher?: IHasher }
interface Prepared {
  body: UploadBody
  next: Checkpoint
  dispose(): Promise<void>
}
export interface EngineIO {
  url(id: string, part: number, signal: AbortSignal): Promise<string>
  progress(id: string, uploaded: number, total: number): void
  phase?(id: string, phase: string): void
  put?: typeof putPart
  spool?: () => Promise<PartSpool>
}

export class UploadEngine {
  private slots = new ResourcePool<Slot>(Array.from({ length: PART_BUFFER_COUNT }, () => ({})))
  private tasks = new Map<string, Task>()
  private io: EngineIO

  constructor(io: EngineIO) { this.io = io }

  pause(id: string): void {
    const task = this.tasks.get(id)
    if (!task) return
    task.paused = true
    task.attempt?.abort(new DOMException('Upload paused', 'AbortError'))
  }

  resume(id: string): void {
    const task = this.tasks.get(id)
    if (!task) return
    task.paused = false
    task.wake?.()
  }

  cancel(id: string): void {
    const task = this.tasks.get(id)
    if (!task) return
    task.cancelled = true
    task.attempt?.abort(new DOMException('Upload cancelled', 'AbortError'))
    task.wake?.()
  }

  async upload(input: UploadInput): Promise<string | null> {
    if (this.tasks.has(input.id)) throw new Error('Duplicate upload task')
    if (!Number.isSafeInteger(input.file.size) || input.file.size < 0 ||
        !Number.isSafeInteger(input.partSize) || input.partSize < ENC_CHUNK + 5 || input.partSize > 5 * 1024 ** 3 ||
        !Number.isSafeInteger(input.totalParts) || input.totalParts < 1 || input.totalParts > 10000) {
      throw new Error('Invalid multipart geometry')
    }
    const task: Task = { input, committed: { offset: 0, part: 1, uploaded: 0 }, paused: !!input.paused, cancelled: false }
    this.tasks.set(input.id, task)
    try {
      while (!task.cancelled && task.committed.part <= input.totalParts) {
        while (task.paused && !task.cancelled) await new Promise<void>(resolve => { task.wake = resolve })
        task.wake = undefined
        if (task.cancelled) break
        const attempt = new AbortController()
        task.attempt = attempt
        let checkpoint = task.committed
        // Always observe the sending promise immediately, even while filling a
        // different slot. A failed sink aborts the producer and pool waiters.
        let sending: Promise<{ error?: unknown }> = Promise.resolve({})
        try {
          while (checkpoint.part <= input.totalParts) {
            this.io.phase?.(input.id, 'queued')
            const slot = await this.slots.acquire(attempt.signal)
            let prepared: Prepared | undefined
            let handedOff = false
            try {
              attempt.signal.throwIfAborted()
              this.io.phase?.(input.id, 'encrypting')
              prepared = await this.prepare(input, checkpoint, slot, attempt.signal)
              const previous = await sending
              if (previous.error) throw previous.error
              attempt.signal.throwIfAborted()
              const part = checkpoint.part
              checkpoint = prepared.next
              const body = prepared
              sending = (async () => {
                try {
                  const url = await this.io.url(input.id, part, attempt.signal)
                  attempt.signal.throwIfAborted()
                  this.io.phase?.(input.id, 'uploading')
                  await (this.io.put || putPart)(url, body.body, attempt.signal)
                  // Only acknowledged parts form a restart checkpoint.
                  task.committed = body.next
                  this.io.progress(input.id, body.next.uploaded, 5 + input.file.size + Math.ceil(input.file.size / CHUNK) * 28)
                  return {}
                } catch (error) {
                  attempt.abort(error)
                  return { error }
                } finally {
                  try { await body.dispose() } finally { this.slots.release(slot) }
                }
              })().catch(error => { attempt.abort(error); return { error } })
              handedOff = true
            } finally {
              if (!handedOff) {
                try { await prepared?.dispose() } finally { this.slots.release(slot) }
              }
            }
          }
          const result = await sending
          if (result.error) throw result.error
        } catch (error) {
          const interrupted = attempt.signal.aborted && (attempt.signal.reason as Error)?.name === 'AbortError'
          attempt.abort(error)
          await sending
          if (!task.cancelled && !interrupted) throw error
        } finally { task.attempt = undefined }
      }
      if (task.cancelled) return null
      if (task.committed.offset !== input.file.size || !task.committed.digest) throw new Error('Multipart geometry mismatch')
      return task.committed.digest
    } finally {
      this.tasks.delete(input.id)
      new Uint8Array(input.dekRaw).fill(0)
    }
  }

  private async prepare(input: UploadInput, start: Checkpoint, slot: Slot, signal: AbortSignal): Promise<Prepared> {
    slot.bytes ??= new Uint8Array(PART_BUFFER_BYTES)
    slot.hasher ??= await createSHA256()
    const hash = slot.hasher
    start.hash ? hash.load(start.hash) : hash.init()
    const key = await crypto.subtle.importKey('raw', input.dekRaw, 'AES-GCM', false, ['encrypt'])
    signal.throwIfAborted()
    let spool: PartSpool | undefined
    let used = 0
    let length = 0
    let offset = start.offset
    const append = (bytes: Uint8Array<ArrayBuffer>) => {
      if (used + bytes.length > slot.bytes!.length) {
        if (!spool) throw new Error('Part exceeded its memory lease')
        spool.write(slot.bytes!.subarray(0, used))
        used = 0
      }
      slot.bytes!.set(bytes, used)
      used += bytes.length
      length += bytes.length
    }
    try {
      // File-backed requests avoid retained native copies of ArrayBuffer/Blob
      // upload bodies in Chromium. Keep both disk and memory under the lease.
      spool = await (this.io.spool || createPartSpool)()
      signal.throwIfAborted()
      if (start.part === 1) append(WireEncoder.header())
      while (offset < input.file.size) {
        signal.throwIfAborted()
        const room = input.partSize - length
        const remaining = input.file.size - offset
        let readBytes = Math.min(READ_BYTES, Math.floor(room / ENC_CHUNK) * CHUNK, Math.floor(remaining / CHUNK) * CHUNK)
        if (readBytes === 0) {
          if (remaining < CHUNK && remaining + 28 <= room) readBytes = remaining
          else break
        }
        const plain = new Uint8Array(await input.file.slice(offset, offset + readBytes).arrayBuffer())
        signal.throwIfAborted()
        if (plain.length !== readBytes) throw new Error('Upload source changed while reading')
        hash.update(plain)
        const pending: Promise<Uint8Array<ArrayBuffer>>[] = []
        for (let pos = 0; pos < plain.length; pos += CHUNK) {
          const nonce = crypto.getRandomValues(new Uint8Array(12))
          const ordinal = (offset + pos) / CHUNK
          pending.push(crypto.subtle.encrypt(
            { name: 'AES-GCM', iv: nonce, additionalData: uint64BE(ordinal) }, key, plain.subarray(pos, pos + CHUNK),
          ).then(cipher => {
            const segment = new Uint8Array(12 + cipher.byteLength)
            segment.set(nonce)
            segment.set(new Uint8Array(cipher), 12)
            return segment
          }))
        }
        // WebCrypto itself is not abortable; finish all six operations before
        // returning the slot, including when one operation rejects early.
        const results = await Promise.allSettled(pending)
        signal.throwIfAborted()
        for (const result of results) {
          if (result.status === 'rejected') throw result.reason
          append(result.value)
        }
        offset += plain.length
      }
      const savedHash = hash.save()
      const digest = offset === input.file.size ? hash.digest('hex') : undefined
      if ((offset === input.file.size) !== (start.part === input.totalParts)) throw new Error('Multipart geometry mismatch')
      spool.write(slot.bytes.subarray(0, used))
      const body = await spool.finish()
      signal.throwIfAborted()
      return {
        body,
        next: { part: start.part + 1, offset, uploaded: start.uploaded + length, hash: savedHash, digest },
        async dispose() { await spool?.dispose() },
      }
    } catch (error) { await spool?.dispose(); throw error }
  }
}
