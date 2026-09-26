import { UploadEngine } from '../uploads/engine.ts'

// One worker owns the two shared multipart buffers for all file tasks.
let requestSequence = 0
const requests = new Map<number, { resolve: (url: string) => void; reject: (error: unknown) => void }>()
const engine = new UploadEngine({
  url(id, part, signal) {
    signal.throwIfAborted()
    return new Promise((resolve, reject) => {
      const request = ++requestSequence
      const abort = () => {
        requests.delete(request)
        self.postMessage({ type: 'cancel-url', id, request })
        reject(signal.reason)
      }
      signal.addEventListener('abort', abort, { once: true })
      requests.set(request, {
        resolve(url) { signal.removeEventListener('abort', abort); requests.delete(request); resolve(url) },
        reject(error) { signal.removeEventListener('abort', abort); requests.delete(request); reject(error) },
      })
      self.postMessage({ type: 'need-url', id, request, part })
    })
  },
  progress(id, uploaded, total) { self.postMessage({ type: 'progress', id, uploaded, total }) },
  phase(id, phase) { self.postMessage({ type: 'phase', id, phase }) },
})

self.onmessage = async (event: MessageEvent) => {
  const msg = event.data
  switch (msg.type) {
    case 'start':
      try {
        const contentHash = await engine.upload(msg)
        self.postMessage(contentHash === null ? { type: 'cancelled', id: msg.id } : { type: 'done', id: msg.id, contentHash })
      } catch (error) {
        self.postMessage({ type: 'error', id: msg.id, message: error instanceof Error ? error.message : String(error) })
      }
      break
    case 'url':
      if (msg.error) requests.get(msg.request)?.reject(new Error(msg.error))
      else requests.get(msg.request)?.resolve(msg.url)
      break
    case 'pause': engine.pause(msg.id); break
    case 'resume': engine.resume(msg.id); break
    case 'cancel': engine.cancel(msg.id); break
  }
}
