/**
 * Development-only client-state purge: unregisters every service worker,
 * deletes every Cache API bucket (including possibly-poisoned decrypt
 * caches), clears session state and reloads the page. Exposed by the
 * development-only account-menu action.
 */
export async function purgeClientState(): Promise<void> {
  if ('serviceWorker' in navigator) {
    const registrations = await navigator.serviceWorker.getRegistrations()
    await Promise.all(registrations.map(registration => registration.unregister()))
  }
  if (typeof caches !== 'undefined') {
    const names = await caches.keys()
    await Promise.all(names.map(name => caches.delete(name)))
  }
  sessionStorage.clear()
  // In-memory SW state dies with the unregistration above; the reload then
  // boots a fresh SW and a fresh registry.
  location.reload()
}
