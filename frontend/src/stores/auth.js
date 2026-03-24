import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../composables/useApi'
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

  const isLoggedIn = computed(() => !!user.value)
  const isAdmin = computed(() => user.value?.role === 'admin')
  const username = computed(() => user.value?.username || '')

  const canRead = computed(() => isAdmin.value || ((user.value?.permissions ?? 0) & PERM_READ) !== 0)
  const canUpload = computed(() => isAdmin.value || ((user.value?.permissions ?? 0) & PERM_UPLOAD) !== 0)
  const canEdit = computed(() => isAdmin.value || ((user.value?.permissions ?? 0) & PERM_EDIT) !== 0)
  const canDelete = computed(() => isAdmin.value || ((user.value?.permissions ?? 0) & PERM_DELETE) !== 0)
  const canWrite = computed(() => canUpload.value || canEdit.value || canDelete.value)

  async function checkAuth() {
    loading.value = true
    try {
      const res = await api.get('/user')
      user.value = res.data
      useWindowManagerStore().setUser(res.data.id)
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
    }
    return res.data
  }

  async function logout() {
    try {
      await api.delete('/auth')
    } finally {
      useWindowManagerStore().clearUser()
      user.value = null
    }
  }

  // Listen for auth expiry events
  window.addEventListener('auth:expired', () => {
    useWindowManagerStore().clearUser()
    user.value = null
  })

  return {
    user,
    loading,
    needsSetup,
    isLoggedIn,
    isAdmin,

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
