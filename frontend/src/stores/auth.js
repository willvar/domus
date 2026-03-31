import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useWindowManagerStore } from './windowManager'
import { useWorkspaceSync } from '../composables/useWorkspaceSync'
import { usePreferences } from '../composables/usePreferences'

export const useAuthStore = defineStore('auth', () => {
  const user = ref(null)
  const loading = ref(true)
  const needsSetup = ref(false)
  const ws = useWebSocket()

  const isLoggedIn = computed(() => !!user.value)
  const isRoot = computed(() => user.value?.role === 'root')
  const username = computed(() => user.value?.username || '')

  async function checkAuth() {
    loading.value = true
    try {
      const res = await api.get('/user')
      user.value = res.data
      useWindowManagerStore().setUser(res.data.id)
      // Connect WebSocket after confirming auth
      ws.connect()
    } catch {
      user.value = null
    } finally {
      loading.value = false
    }
  }

  // POST /auth/verify — returns {token, methods}
  async function verify(body) {
    const res = await api.post('/auth/verify', body)
    return res.data
  }

  // POST /auth/login — exchanges token for session
  async function login(body) {
    const res = await api.post('/auth', body)
    if (res.data.user) {
      user.value = res.data.user
      useWindowManagerStore().setUser(res.data.user.id)
      if (res.data.needs_setup) {
        needsSetup.value = true
      }
      // Connect WebSocket after login
      ws.connect()
    }
    return res.data
  }

  async function logout() {
    // Only clear workspace state when session isolation is ON (default mode).
    // When isolation is OFF (sync mode), other devices may still be active,
    // so we preserve the workspace for them.
    try {
      const { prefs } = usePreferences()
      if (prefs.sessionIsolation) {
        const workspace = useWorkspaceSync()
        await workspace.clear()
      }
    } catch { /* silent */ }
    ws.disconnect()
    try {
      await api.delete('/auth')
    } finally {
      useWindowManagerStore().clearUser()
      user.value = null
    }
  }

  function clearSession() {
    ws.disconnect()
    useWindowManagerStore().clearUser()
    user.value = null
  }

  // Listen for auth expiry events (from HTTP 401 interceptor)
  window.addEventListener('auth:expired', clearSession)

  // Listen for session.expired push from WebSocket
  ws.on('session.expired', clearSession)

  return {
    user,
    loading,
    needsSetup,
    isLoggedIn,
    isRoot,
    username,
    checkAuth,
    verify,
    login,
    logout,
  }
})
