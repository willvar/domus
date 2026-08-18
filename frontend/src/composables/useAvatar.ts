import api from './useApi'
import { decryptBlob } from './useCryptoUpload'

const MAX_AVATAR_BYTES = 10 * 1024 * 1024
const CRYPTO_CHUNK_SIZE = 65536
const CRYPTO_HEADER_SIZE = 5
const NONCE_AND_TAG_SIZE = 12 + 16

interface PublicAvatarAccess {
  url: string
  dek: string
  size: number
  content_type: string
  chunk_size: number
  generation: number
}

function encryptedSize(plainSize: number): number {
  return CRYPTO_HEADER_SIZE + plainSize + Math.ceil(plainSize / CRYPTO_CHUNK_SIZE) * NONCE_AND_TAG_SIZE
}

function hexKey(value: string): Uint8Array {
  if (!/^[0-9a-fA-F]{64}$/.test(value)) throw new Error('Invalid avatar encryption key')
  const key = new Uint8Array(32)
  for (let index = 0; index < key.length; index++) {
    key[index] = Number.parseInt(value.slice(index * 2, index * 2 + 2), 16)
  }
  return key
}

/**
 * Resolve a public avatar without sending its object bytes through Domus.
 * Domus returns only a presigned OSS URL and the public avatar DEK; the browser
 * downloads ciphertext, decrypts locally, and exposes a short-lived blob URL.
 */
export async function loadPublicAvatar(username: string): Promise<string> {
  const normalized = username.trim()
  if (!normalized) throw new Error('Missing avatar username')
  const { data } = await api.get<PublicAvatarAccess>(`/user/avatar/${encodeURIComponent(normalized)}`)
  if (!Number.isSafeInteger(data.size) || data.size <= 0 || data.size > MAX_AVATAR_BYTES) {
    throw new Error('Invalid avatar size')
  }
  if (data.content_type !== 'image/webp' || data.chunk_size !== CRYPTO_CHUNK_SIZE) {
    throw new Error('Invalid avatar metadata')
  }
  const objectURL = new URL(data.url)
  if (objectURL.protocol !== 'https:' && objectURL.protocol !== 'http:') {
    throw new Error('Invalid avatar object URL')
  }
  const expectedEncryptedSize = encryptedSize(data.size)
  const response = await fetch(objectURL, { credentials: 'omit', cache: 'no-store' })
  if (!response.ok) throw new Error(`Avatar object fetch failed: ${response.status}`)
  const declaredLength = Number(response.headers.get('content-length'))
  if (Number.isFinite(declaredLength) && declaredLength > 0 && declaredLength !== expectedEncryptedSize) {
    throw new Error('Avatar object size mismatch')
  }
  const ciphertext = await response.arrayBuffer()
  if (ciphertext.byteLength !== expectedEncryptedSize) throw new Error('Avatar object size mismatch')
  const plaintext = await decryptBlob(hexKey(data.dek), ciphertext)
  if (plaintext.byteLength !== data.size) throw new Error('Avatar plaintext size mismatch')
  return URL.createObjectURL(new Blob([plaintext], { type: 'image/webp' }))
}

export function revokeAvatarURL(value: string | undefined): void {
  if (value?.startsWith('blob:')) URL.revokeObjectURL(value)
}
