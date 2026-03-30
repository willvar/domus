import { reactive } from 'vue'
import api from './useApi'
import { useWebSocket } from './useWebSocket'

const defaults = {
  largeFileLimitMB: 10,
  alwaysCenter: false,
  defaultWidth: 800,
  defaultHeight: 600,
  indexContent: false,
  sessionIsolation: true,
  wallpaperType: 'builtin',
  wallpaperBuiltinId: 0,
  wallpaperPath: '',
  wallpaperFit: 'cover',
  wallpaperFiles: [],
}

const prefs = reactive({ ...defaults })
let loaded = false
let saveTimer = null

async function load() {
  try {
    const res = await api.get('/user/store/preferences.json')
    if (res.data && typeof res.data === 'object') {
      Object.assign(prefs, defaults, res.data)
    }
  } catch {
    Object.assign(prefs, defaults)
  }
  loaded = true
}

function save() {
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(async () => {
    try {
      await api.put('/user/store/preferences.json', JSON.stringify({ ...prefs }), {
        headers: { 'Content-Type': 'application/json' },
      })
      // Push preference changes to other devices via workspace event
      const ws = useWebSocket()
      ws.request('workspace.event', { action: 'prefs.changed', data: { ...prefs } }).catch(() => {})
    } catch { /* silent */ }
  }, 500)
}

function update(partial) {
  Object.assign(prefs, partial)
  save()
}

export function usePreferences() {
  return { prefs, load, update, loaded: () => loaded }
}
