import { useServiceWorker } from './useServiceWorker'
import { ResourcePool } from '../uploads/resources'
import type { FileListItem } from '../types'

const loads = new ResourcePool([0, 1, 2, 3])

export function thumbnailIdentity(file: FileListItem): string {
  if (!file.thumbnail_url) return ''
  const url = new URL(file.thumbnail_url, window.location.href)
  for (const key of [...url.searchParams.keys()]) {
    if (/^(x-amz-|x-oss-|awsaccesskeyid$|ossaccesskeyid$|signature$|expires$|security-token$)/i.test(key)) url.searchParams.delete(key)
  }
  return `${file.inode || file.path}:${file.generation || 0}:${url.href}:${file.thumbnail_dek || ''}`
}

function abortable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason)
    signal.addEventListener('abort', abort, { once: true })
    promise.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort))
  })
}

/** Called only for visible items. A mapping lives no longer than its read. */
export async function readThumbnail(file: FileListItem, signal: AbortSignal): Promise<Blob> {
  const lease = await loads.acquire(signal)
  const sw = useServiceWorker()
  try {
    for (let attempt = 0; attempt < 2; attempt++) {
      signal.throwIfAborted()
      const url = sw.registerDecrypt({ url: file.thumbnail_url!, dek: file.thumbnail_dek!,
        size: null, chunkSize: 0, contentType: 'image/webp', filename: '' })
      if (!url) throw new Error('Thumbnail decryption unavailable')
      try {
        await abortable(sw.flush(), signal)
        const response = await fetch(url, { signal })
        // A worker may have restarted between registration and the read.
        if (response.status === 404 && attempt === 0) continue
        if (!response.ok) throw new Error('Thumbnail read failed')
        const length = Number(response.headers.get('Content-Length'))
        if (!Number.isSafeInteger(length) || length <= 0 || length > 4 * 1024 * 1024) {
          await response.body?.cancel()
          throw new Error('Invalid thumbnail size')
        }
        const blob = await response.blob()
        signal.throwIfAborted()
        if (blob.size !== length) throw new Error('Incomplete thumbnail')
        return blob
      } finally { sw.unregisterDecrypt(url) }
    }
    throw new Error('Thumbnail decryption unavailable')
  } finally { loads.release(lease) }
}
