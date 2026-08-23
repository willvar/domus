import api from './useApi'
import { useServiceWorker } from './useServiceWorker'
import type { FileAccessResponse, FileListItem } from '../types'
import { parseTrashLocation } from '../utils/trashLocation'

type AccessTarget = Pick<FileListItem, 'path' | '_shareId'>

export async function registerFileDecrypt(target: AccessTarget): Promise<{ decryptUrl: string; access: FileAccessResponse }> {
  const sw = useServiceWorker()
  const trash = parseTrashLocation(target.path)
  const res = target._shareId
    ? await api.get<FileAccessResponse>('/file/shared/' + target._shareId)
    : trash?.id
      ? await api.get<FileAccessResponse>(`/trash/${encodeURIComponent(trash.id)}/access`, {
          params: { path: trash.relativePath },
        })
    : await api.get<FileAccessResponse>('/file/access', { params: { path: target.path } })
  const access = res.data
  const decryptUrl = sw.registerDecrypt({
    url: access.url,
    size: access.size,
    chunkSize: access.chunk_size,
    contentType: access.content_type,
    filename: access.name,
    dek: access.dek,
    contentHash: access.content_hash,
  })
  if (!decryptUrl) {
    throw new Error('Service worker decryption unavailable')
  }
  await sw.flush()
  return { decryptUrl, access }
}

export function unregisterFileDecrypt(url: string | null | undefined): void {
  if (!url) return
  useServiceWorker().unregisterDecrypt(url)
}
