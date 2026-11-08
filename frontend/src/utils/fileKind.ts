import type { ViewerType } from '../types'

export function normalizedMime(value = ''): string {
  return value.split(';', 1)[0]!.trim().toLowerCase()
}

export function mimeViewer(value = ''): ViewerType | null {
  const mime = normalizedMime(value)
  if (mime.startsWith('image/')) return 'image'
  if (mime.startsWith('video/')) return 'video'
  if (mime.startsWith('audio/')) return 'audio'
  if (mime === 'application/pdf') return 'pdf'
  if (['text/markdown', 'text/x-markdown'].includes(mime)) return 'markdown'
  if (mime === 'text/csv') return 'csv'
  if (mime === 'application/x-ipynb+json') return 'notebook'
  if (['application/zip', 'application/x-zip-compressed'].includes(mime)) return 'archive'
  if (mime.startsWith('font/') || ['application/font-woff', 'application/vnd.ms-fontobject', 'application/x-font-ttf', 'application/x-font-opentype'].includes(mime)) return 'font'
  // Plain text is deliberately a fallback: .md/.csv/source filenames convey
  // more useful information than a browser's generic text/plain declaration.
  if ((mime.startsWith('text/') && mime !== 'text/plain') || /(?:json|xml)$/.test(mime)) return 'text'
  return null
}

export function supportsThumbnail(contentType = ''): boolean {
  const kind = mimeViewer(contentType)
  return kind === 'image' || kind === 'video' || kind === 'pdf'
}

export function splitExtension(name: string): { stem: string; suffix: string } {
  const compound = /\.(?:tar\.(?:gz|bz2|xz|zst))$/i.exec(name)
  const dot = compound?.index ?? name.lastIndexOf('.')
  return dot > 0 && dot < name.length - 1
    ? { stem: name.slice(0, dot), suffix: name.slice(dot) }
    : { stem: name, suffix: '' }
}

/** Bounded format hints before encryption, not a security validation scheme. */
export async function uploadContentType(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.slice(0, 4096).arrayBuffer())
  const ascii = new TextDecoder('latin1').decode(bytes)
  const starts = (...signature: number[]) => signature.every((value, index) => bytes[index] === value)
  if (starts(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)) return 'image/png'
  if (starts(0xff, 0xd8, 0xff)) return 'image/jpeg'
  if (/^GIF8[79]a/.test(ascii)) return 'image/gif'
  if (ascii.startsWith('RIFF') && ascii.slice(8, 12) === 'WEBP') return 'image/webp'
  if (ascii.startsWith('RIFF') && ascii.slice(8, 12) === 'WAVE') return 'audio/wav'
  if (ascii.startsWith('%PDF-')) return 'application/pdf'
  if (ascii.startsWith('fLaC')) return 'audio/flac'
  if (ascii.startsWith('OggS')) return ascii.includes('theora') ? 'video/ogg' : 'audio/ogg'
  if (ascii.startsWith('ID3')) return 'audio/mpeg'
  if (ascii.slice(4, 8) === 'ftyp') {
    const brand = ascii.slice(8, 12)
    if (['avif', 'avis'].includes(brand)) return 'image/avif'
    if (['heic', 'heix', 'hevc', 'hevx', 'mif1', 'msf1'].includes(brand)) return 'image/heif'
    if (brand === 'M4A ') return 'audio/mp4'
    return 'video/mp4'
  }
  if (starts(0x1a, 0x45, 0xdf, 0xa3)) return ascii.includes('webm') ? 'video/webm' : 'video/x-matroska'
  const declared = normalizedMime(file.type)
  if (declared && declared !== 'application/octet-stream') return declared
  if (starts(0x50, 0x4b, 0x03, 0x04) || starts(0x50, 0x4b, 0x05, 0x06)) return 'application/zip'
  // Keep extension inference on the server when the browser has no type.
  // Only small, complete UTF-8 documents can be classified here as text.
  if (bytes.length && file.size <= bytes.length && !bytes.some(value => value < 32 && ![9, 10, 13].includes(value))) {
    try {
      const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
      if (/^\s*[\[{]/.test(text)) {
        try { JSON.parse(text); return 'application/json' } catch { /* Not JSON. */ }
      }
      if (!splitExtension(file.name).suffix) return 'text/plain'
    } catch { /* Unknown binary content stays unknown. */ }
  }
  return ''
}
