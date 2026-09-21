import { reactive } from 'vue'
import { readEncryptedFile, writeEncryptedFile } from './useCryptoUpload'
import type { UserPreferences } from '../types'

const PREFS_PATH = '/.user/preferences.json'

const defaults: UserPreferences = {
  largeFileLimitMB: 10,
  alwaysCenter: false,
  defaultWidth: 800,
  defaultHeight: 600,
  sessionIsolation: true,
  wallpaperType: 'builtin',
  wallpaperBuiltinId: 0,
  wallpaperPath: '',
  wallpaperFit: 'cover',
  wallpaperFiles: [],
  playbackQuality: 'original',
}

const prefs: UserPreferences = reactive({ ...defaults })
let loaded: boolean = false
let saveTimer: ReturnType<typeof setTimeout> | null = null

async function load(): Promise<void> {
  try {
    const buf = await readEncryptedFile(PREFS_PATH, true)
    const json = JSON.parse(new TextDecoder().decode(buf))
    if (json && typeof json === 'object') {
      Object.assign(prefs, defaults, json)
    }
  } catch {
    Object.assign(prefs, defaults)
  }
  loaded = true
}

function save(): void {
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(async () => {
    try {
      const json = JSON.stringify({ ...prefs })
      const buf = new TextEncoder().encode(json).buffer as ArrayBuffer
      await writeEncryptedFile(PREFS_PATH, buf, 'application/json', { internal: true })
    } catch { /* silent */ }
  }, 500)
}

function update(partial: Partial<UserPreferences>): void {
  Object.assign(prefs, partial)
  save()
}

export function usePreferences(): {
  prefs: UserPreferences
  load: () => Promise<void>
  update: (partial: Partial<UserPreferences>) => void
  loaded: () => boolean
} {
  return { prefs, load, update, loaded: () => loaded }
}
