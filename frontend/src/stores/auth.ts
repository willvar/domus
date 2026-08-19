import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { loadPublicAvatar, revokeAvatarURL } from '../composables/useAvatar'
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

  function replaceUser(next: User | null): void {
    const previousAvatar = user.value?.avatar_url
    user.value = next
    if (previousAvatar && previousAvatar !== next?.avatar_url) revokeAvatarURL(previousAvatar)
  }

  async function hydrateAvatar(source: User): Promise<User> {
    const hydrated = { ...source, avatar_url: '' }
    if (!source.avatar_endpoint) return hydrated
    try {
      hydrated.avatar_url = await loadPublicAvatar(source.username)
    } catch {
      hydrated.avatar_url = ''
    }
    return hydrated
  }

  async function checkAuth(): Promise<void> {
    loading.value = true
    try {
      const res = await api.get<User>('/user')
      replaceUser(await hydrateAvatar(res.data))
      // Connect WebSocket after confirming auth
      ws.connect()
    } catch {
      replaceUser(null)
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
      replaceUser(await hydrateAvatar(res.data.user))
      if (res.data.needs_setup) {
        needsSetup.value = true
      }
      // Connect WebSocket after login
      ws.connect()
    }
    return res.data
  }

  async function refreshAvatar(): Promise<string> {
    if (!user.value) return ''
    try {
      const nextAvatar = await loadPublicAvatar(user.value.username)
      const previousAvatar = user.value.avatar_url
      user.value.avatar_endpoint = `/user/avatar/${encodeURIComponent(user.value.username)}`
      user.value.avatar_url = nextAvatar
      if (previousAvatar && previousAvatar !== nextAvatar) revokeAvatarURL(previousAvatar)
      return nextAvatar
    } catch {
      return user.value.avatar_url || ''
    }
  }

  async function logout(): Promise<void> {
    ws.disconnect()
    try {
      await api.delete('/auth')
    } finally {
      replaceUser(null)
    }
  }

  function clearSession(): void {
    ws.disconnect()
    replaceUser(null)
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
    refreshAvatar,
    logout,
  }
})
