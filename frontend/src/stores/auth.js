import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import { useWindowManagerStore } from './windowManager'

// Permission bitmask constants
export const PERM_READ = 1
export const PERM_UPLOAD = 2
export const PERM_EDIT = 4
export const PERM_DELETE = 8

export const useAuthStore = defineStore('auth', () => {
  const user = ref(null)
  const loading = ref(true)
  const needsSetup = ref(false)
  const ws = useWebSocket()

  const isLoggedIn = computed(() => !!user.value)
  const isRoot = computed(() => user.value?.role === 'root')
  const username = computed(() => user.value?.username || '')

  const canRead = computed(() => isRoot.value || ((user.value?.permissions ?? 0) & PERM_READ) !== 0)
  const canUpload = computed(() => isRoot.value || ((user.value?.permissions ?? 0) & PERM_UPLOAD) !== 0)
  const canEdit = computed(() => isRoot.value || ((user.value?.permissions ?? 0) & PERM_EDIT) !== 0)
  const canDelete = computed(() => isRoot.value || ((user.value?.permissions ?? 0) & PERM_DELETE) !== 0)
  const canWrite = computed(() => canUpload.value || canEdit.value || canDelete.value)

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
    ws.disconnect()
    try {
      await api.delete('/auth')
    } finally {
      useWindowManagerStore().clearUser()
      user.value = null
    }
  }

  // Listen for auth expiry events (from HTTP 401 interceptor)
  window.addEventListener('auth:expired', () => {
    ws.disconnect()
    useWindowManagerStore().clearUser()
    user.value = null
  })

  // Listen for session.expired push from WebSocket
  ws.on('session.expired', () => {
    ws.disconnect()
    useWindowManagerStore().clearUser()
    user.value = null
  })

  return {
    user,
    loading,
    needsSetup,
    isLoggedIn,
    isRoot,

    canRead,
    canUpload,
    canEdit,
    canDelete,
    canWrite,
    username,
    checkAuth,
    verify,
    login,
    logout,
  }
})
