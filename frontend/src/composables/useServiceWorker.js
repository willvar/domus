import { ref } from 'vue'

const swReady = ref(false)
let storedKeyHex = null

/**
 * Composable for managing the Service Worker that handles client-side
 * AES-GCM decryption of encrypted files from OSS/CDN.
 */
export function useServiceWorker() {
  async function register() {
    if (!('serviceWorker' in navigator)) return

    try {
      const registration = await navigator.serviceWorker.register('/sw.js', { scope: '/' })

      // Wait for the SW to be active
      const sw = registration.active || registration.installing || registration.waiting
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
    } catch (e) {
      console.warn('SW registration failed:', e)
    }
  }

  function sendKey(hex) {
    storedKeyHex = hex
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({ type: 'set-key', key: hex })
    }
  }

  function clearKey() {
    storedKeyHex = null
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({ type: 'clear-key' })
    }
  }

  /**
   * Register a file for decryption through the SW.
   * @param {object} metadata - { url, size, chunkSize, contentType, filename, wrappedDek, download? }
   * @returns {string} The decrypt URL: /__decrypt__/{id}
   */
  function registerDecrypt(metadata) {
    const id = crypto.randomUUID()
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({
        type: 'register', id, metadata,
      })
    }
    return '/__decrypt__/' + id
  }

  /**
   * Unregister a previously registered decrypt URL.
   * @param {string} url - The full /__decrypt__/{id} URL
   */
  function unregisterDecrypt(url) {
    if (!url || !url.startsWith('/__decrypt__/')) return
    const id = url.slice('/__decrypt__/'.length)
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({ type: 'unregister', id })
    }
  }

  return { swReady, register, sendKey, clearKey, registerDecrypt, unregisterDecrypt }
}
