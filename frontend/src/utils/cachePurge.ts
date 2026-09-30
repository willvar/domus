/** Reset only Domus caches; recheck permission around asynchronous cleanup. */
export async function purgeClientState(canReload: () => boolean): Promise<boolean> {
  // Refresh the HTML entry before changing caches, so an offline failure does
  // not unregister a working worker. Hashed assets follow the current HTML.
  // This cannot purge a CDN or the browser's entire HTTP cache.
  const entry = await fetch(location.href, {
    cache: 'reload', credentials: 'same-origin', signal: AbortSignal.timeout(30000),
  })
  if (!entry.ok) throw new Error(`Reload entry returned HTTP ${entry.status}`)
  await entry.arrayBuffer()
  if (!canReload()) return false

  if ('serviceWorker' in navigator) {
    const registration = await navigator.serviceWorker.getRegistration('/')
    await registration?.unregister()
  }
  if (typeof caches !== 'undefined') {
    const names = await caches.keys()
    await Promise.all(names
      .filter(name => name.startsWith('domus-') || name === 'zephyr-decrypt')
      .map(name => caches.delete(name)))
  }
  if (!canReload()) return false
  location.reload()
  return true
}
