import type { UploadInput } from './engine.ts'

interface Job {
  signal: AbortSignal
  abort(): void
  resolve(hash: string | null): void
  reject(error: unknown): void
  url(part: number, signal: AbortSignal): Promise<string>
  progress(uploaded: number, total: number): void
  phase(phase: string): void
}

export class UploadClient {
  private worker: Worker | null = null
  private jobs = new Map<string, Job>()
  private requests = new Map<number, { id: string; control: AbortController }>()

  upload(input: UploadInput, callbacks: Pick<Job, 'url' | 'progress' | 'phase'>, signal: AbortSignal): Promise<string | null> {
    signal.throwIfAborted()
    if (this.jobs.has(input.id)) throw new Error('Duplicate upload task')
    if (!this.worker) {
      this.worker = new Worker(new URL('../workers/encrypt.worker.ts', import.meta.url), { type: 'module' })
      this.worker.onmessage = event => { void this.receive(event.data) }
      this.worker.onerror = event => {
        for (const id of [...this.jobs.keys()]) this.finish(id, undefined, new Error(event.message))
      }
      this.worker.onmessageerror = () => {
        for (const id of [...this.jobs.keys()]) this.finish(id, undefined, new Error('Upload worker message could not be decoded'))
      }
    }
    return new Promise((resolve, reject) => {
      const abort = () => this.worker?.postMessage({ type: 'cancel', id: input.id })
      this.jobs.set(input.id, { ...callbacks, signal, abort, resolve, reject })
      signal.addEventListener('abort', abort, { once: true })
      try { this.worker!.postMessage({ ...input, type: 'start' }, [input.dekRaw]) }
      catch (error) { this.finish(input.id, undefined, error instanceof Error ? error : new Error(String(error))) }
    })
  }

  pause(id: string): void { this.worker?.postMessage({ type: 'pause', id }) }
  resume(id: string): void { this.worker?.postMessage({ type: 'resume', id }) }

  private finish(id: string, hash?: string | null, error?: Error): void {
    const job = this.jobs.get(id)
    if (!job) return
    this.jobs.delete(id)
    for (const [request, pending] of this.requests) {
      if (pending.id === id) { pending.control.abort(); this.requests.delete(request) }
    }
    job.signal.removeEventListener('abort', job.abort)
    error ? job.reject(error) : job.resolve(hash ?? null)
    if (!this.jobs.size) { this.worker?.terminate(); this.worker = null }
  }

  private async receive(msg: { type: string; id: string; part: number; request: number; uploaded: number; total: number; phase: string; contentHash: string; message: string }): Promise<void> {
    if (msg.type === 'cancel-url') {
      const pending = this.requests.get(msg.request)
      if (pending?.id === msg.id) pending.control.abort()
      return
    }
    const job = this.jobs.get(msg.id)
    if (!job) return
    switch (msg.type) {
      case 'need-url': {
        const worker = this.worker
        const control = new AbortController()
        const abort = () => control.abort(job.signal.reason)
        job.signal.addEventListener('abort', abort, { once: true })
        if (job.signal.aborted) abort()
        const pending = { id: msg.id, control }
        this.requests.set(msg.request, pending)
        try {
          const url = await job.url(msg.part, control.signal)
          if (this.jobs.get(msg.id) === job) worker?.postMessage({ type: 'url', request: msg.request, url })
        } catch (error) {
          if (this.jobs.get(msg.id) === job) worker?.postMessage({ type: 'url', request: msg.request, error: error instanceof Error ? error.message : String(error) })
        } finally {
          job.signal.removeEventListener('abort', abort)
          if (this.requests.get(msg.request) === pending) this.requests.delete(msg.request)
        }
        break
      }
      case 'progress': if (!job.signal.aborted) job.progress(msg.uploaded, msg.total); break
      case 'phase': if (!job.signal.aborted) job.phase(msg.phase); break
      case 'done': this.finish(msg.id, msg.contentHash); break
      case 'cancelled': this.finish(msg.id, null); break
      case 'error': this.finish(msg.id, undefined, new Error(msg.message)); break
    }
  }
}
