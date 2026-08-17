import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { Ref } from 'vue'
import type { FileListItem } from '../types'
import { readMigratedStorage } from '../utils/storageCompat'

export type MobileTabKey = 'files' | 'recent' | 'shared' | 'me'
export type MobileFileDisplayMode = 'list' | 'grid'
export type MobileSharedTab = 'withMe' | 'byMe'

interface RecentMobileItem {
  path: string
  name: string
  is_dir: boolean
  size: number
  created_at: string
  last_modified: string
  content_type?: string
  _shareId?: string
  _originalName?: string
  opened_at: string
}

type SheetState =
  | { kind: 'closed' }
  | { kind: 'create' }
  | { kind: 'file'; item: FileListItem }

const RECENT_KEY = 'domus_mobile_recent'
const LEGACY_RECENT_KEY = 'zephyr_mobile_recent'

function loadRecent(): RecentMobileItem[] {
  try {
    const raw = readMigratedStorage(RECENT_KEY, LEGACY_RECENT_KEY)
    return raw ? JSON.parse(raw) : []
  } catch {
    return []
  }
}

export const useMobileUiStore = defineStore('mobileUi', () => {
  const activeTab: Ref<MobileTabKey> = ref('files')
  const sheet: Ref<SheetState> = ref({ kind: 'closed' })
  const recentItems: Ref<RecentMobileItem[]> = ref(loadRecent())
  const fileDisplayMode: Ref<MobileFileDisplayMode> = ref('list')
  const sharedTab: Ref<MobileSharedTab> = ref('withMe')

  function saveRecent(): void {
    localStorage.setItem(RECENT_KEY, JSON.stringify(recentItems.value))
  }

  function setActiveTab(tab: MobileTabKey): void {
    activeTab.value = tab
  }

  function setFileDisplayMode(mode: MobileFileDisplayMode): void {
    fileDisplayMode.value = mode
  }

  function setSharedTab(tab: MobileSharedTab): void {
    sharedTab.value = tab
  }

  function openCreateSheet(): void {
    sheet.value = { kind: 'create' }
  }

  function openFileSheet(item: FileListItem): void {
    sheet.value = { kind: 'file', item }
  }

  function closeSheet(): void {
    sheet.value = { kind: 'closed' }
  }

  function trackRecent(item: FileListItem): void {
    const next: RecentMobileItem = {
      path: item.path,
      name: item.name,
      is_dir: item.is_dir,
      size: item.size,
      created_at: item.created_at,
      last_modified: item.last_modified,
      content_type: item.content_type,
      _shareId: item._shareId,
      _originalName: item._originalName,
      opened_at: new Date().toISOString(),
    }
    recentItems.value = [next, ...recentItems.value.filter(entry => !(entry.path === next.path && entry._shareId === next._shareId))].slice(0, 40)
    saveRecent()
  }

  return {
    activeTab,
    sheet,
    recentItems,
    fileDisplayMode,
    sharedTab,
    setActiveTab,
    setFileDisplayMode,
    setSharedTab,
    openCreateSheet,
    openFileSheet,
    closeSheet,
    trackRecent,
  }
})
