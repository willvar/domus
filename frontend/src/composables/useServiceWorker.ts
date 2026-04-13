import { ref } from 'vue'
import type { Ref } from 'vue'
import type { DecryptMetadata } from '../types'

const swReady: Ref<boolean> = ref(false)
let storedKeyHex: string | null = null

/**
 * Composable for managing the Service Worker that handles client-side
 * AES-GCM decryption of encrypted files from OSS/CDN.
 */
export function useServiceWorker(): {
  swReady: Ref<boolean>
  register: () => Promise<void>
  sendKey: (hex: string) => void
  clearKey: () => void
  canDecrypt: () => boolean
  registerDecrypt: (metadata: DecryptMetadata) => string | null
  unregisterDecrypt: (url: string) => void
  flush: () => Promise<void>
} {
  async function register(): Promise<void> {
    if (!('serviceWorker' in navigator)) return

    try {
      const registration: ServiceWorkerRegistration = await navigator.serviceWorker.register('/sw.js', { scope: '/' })

      // Wait for the SW to be active
      const sw: ServiceWorker | null = registration.active || registration.installing || registration.waiting
      if (sw && sw.state === 'activated') {
        swReady.value = true
      } else if (sw) {
        sw.addEventListener('statechange', () => {
          if (sw.state === 'activated') swReady.value = true
        })
      }

      // Re-send key when a new SW takes control (e.g., after update)
      navigator.serviceWorker.addEventListener('controllerchange', () => {
        if (storedKeyHex && navigator.serviceWorker.controller) {
          navigator.serviceWorker.controller.postMessage({
            type: 'set-key', key: storedKeyHex,
          })
        }
      })
    } catch (e: unknown) {
      console.warn('SW registration failed:', e)
    }
  }

  function sendKey(hex: string): void {
    storedKeyHex = hex
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({ type: 'set-key', key: hex })
    }
  }

  function clearKey(): void {
    storedKeyHex = null
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({ type: 'clear-key' })
    }
  }

  function canDecrypt(): boolean {
    return !!navigator.serviceWorker?.controller
  }

  /**
   * Register a file for decryption through the SW.
   * @param metadata - { url, size, chunkSize, contentType, filename, wrappedDek, download? }
   * @returns The decrypt URL: /__decrypt__/{id}, or null if SW decryption is unavailable.
   */
  function registerDecrypt(metadata: DecryptMetadata): string | null {
    if (!canDecrypt()) return null
    const id: string = crypto.randomUUID()
    navigator.serviceWorker.controller!.postMessage({
      type: 'register', id, metadata,
    })
    return '/__decrypt__/' + id
  }

  /**
   * Unregister a previously registered decrypt URL.
   * @param url - The full /__decrypt__/{id} URL
   */
  function unregisterDecrypt(url: string): void {
    if (!url || !url.startsWith('/__decrypt__/')) return
    const id: string = url.slice('/__decrypt__/'.length)
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({ type: 'unregister', id })
    }
  }

  /** Wait until the SW has processed all prior postMessage calls (FIFO barrier). */
  function flush(): Promise<void> {
    if (!navigator.serviceWorker?.controller) return Promise.resolve()
    return new Promise(resolve => {
      const { port1, port2 } = new MessageChannel()
      port1.onmessage = () => resolve()
      navigator.serviceWorker.controller!.postMessage({ type: 'flush' }, [port2])
    })
  }

  return { swReady, register, sendKey, clearKey, canDecrypt, registerDecrypt, unregisterDecrypt, flush }
}
