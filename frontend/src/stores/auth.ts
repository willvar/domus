import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useWindowManagerStore } from './windowManager'
import { useWorkspaceSync } from '../composables/useWorkspaceSync'
import { usePreferences } from '../composables/usePreferences'
import type { User, VerifyRequest, VerifyResponse, LoginRequest, LoginResponse } from '../types'

export const useAuthStore = defineStore('auth', () => {
  const user: Ref<User | null> = ref(null)
  const loading: Ref<boolean> = ref(true)
  const needsSetup: Ref<boolean> = ref(false)
  const initialized: Ref<boolean> = ref(false)
  const ws = useWebSocket()
  let authInitPromise: Promise<void> | null = null

  const isLoggedIn: ComputedRef<boolean> = computed(() => !!user.value)
  const isRoot: ComputedRef<boolean> = computed(() => user.value?.role === 'root')
  const username: ComputedRef<string> = computed(() => user.value?.username || '')

  async function checkAuth(): Promise<void> {
    loading.value = true
    try {
      const res = await api.get<User>('/user')
      user.value = res.data
      useWindowManagerStore().setUser(res.data.id)
      // Connect WebSocket after confirming auth
      ws.connect()
    } catch {
      user.value = null
    } finally {
      loading.value = false
      initialized.value = true
    }
  }

  async function ensureAuthInitialized(): Promise<void> {
    if (initialized.value && !loading.value) return
    if (!authInitPromise) {
      authInitPromise = checkAuth().finally(() => {
        authInitPromise = null
      })
    }
    await authInitPromise
  }

  // POST /auth/verify — returns {token, methods}
  async function verify(body: VerifyRequest): Promise<VerifyResponse> {
    const res = await api.post<VerifyResponse>('/auth/verify', body)
    return res.data
  }

  // POST /auth/login — exchanges token for session
  async function login(body: LoginRequest): Promise<LoginResponse> {
    const res = await api.post<LoginResponse>('/auth', body)
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

  async function logout(): Promise<void> {
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

  function clearSession(): void {
    ws.disconnect()
    useWindowManagerStore().clearUser()
    user.value = null
    loading.value = false
    initialized.value = true
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
    ensureAuthInitialized,
    verify,
    login,
    logout,
  }
})
