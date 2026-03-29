import { reactive } from 'vue'
import api from './useApi'

const defaults = {
  largeFileLimitMB: 10,
  alwaysCenter: false,
  defaultWidth: 800,
  defaultHeight: 600,
  indexContent: false,
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
