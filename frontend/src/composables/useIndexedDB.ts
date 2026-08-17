import type { PendingOp } from '../types'

const DB_NAME: string = 'domus'
const LEGACY_DB_NAME: string = 'zephyr'
const MIGRATION_MARKER: string = 'domus_indexeddb_migrated_v1'
const DB_VERSION: number = 1
const PENDING_OPS_STORE: string = 'pending_ops'

let db: IDBDatabase | null = null
let dbUnavailable: boolean = false

function openDB(): Promise<IDBDatabase> {
  if (db) return Promise.resolve(db)
  if (dbUnavailable) return Promise.reject(new Error('IndexedDB unavailable'))

  return new Promise((resolve, reject) => {
    let request: IDBOpenDBRequest
    try {
      request = indexedDB.open(DB_NAME, DB_VERSION)
    } catch {
      dbUnavailable = true
      console.warn('[IndexedDB] Not available in this environment')
      return reject(new Error('IndexedDB unavailable'))
    }

    request.onupgradeneeded = (e: IDBVersionChangeEvent): void => {
      const database: IDBDatabase = (e.target as IDBOpenDBRequest).result
      if (!database.objectStoreNames.contains(PENDING_OPS_STORE)) {
        const store: IDBObjectStore = database.createObjectStore(PENDING_OPS_STORE, { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt', { unique: false })
      }
    }

    request.onsuccess = (e: Event): void => {
      const opened = (e.target as IDBOpenDBRequest).result
      migrateLegacyPendingOps(opened).finally(() => {
        db = opened
        resolve(opened)
      })
    }

    request.onerror = (e: Event): void => {
      dbUnavailable = true
      console.warn('[IndexedDB] Failed to open:', (e.target as IDBOpenDBRequest).error)
      reject((e.target as IDBOpenDBRequest).error)
    }
  })
}

// Copy pending offline operations once. The old database is deliberately kept
// so rolling back a deployment cannot destroy queued user work.
async function migrateLegacyPendingOps(target: IDBDatabase): Promise<void> {
  if (localStorage.getItem(MIGRATION_MARKER) === '1') return
  const migrated = await new Promise<boolean>((resolve) => {
    let created = false
    let request: IDBOpenDBRequest
    try {
      request = indexedDB.open(LEGACY_DB_NAME, DB_VERSION)
    } catch {
      resolve(false)
      return
    }
    request.onupgradeneeded = (): void => { created = true }
    request.onerror = (): void => resolve(false)
    request.onsuccess = (): void => {
      const legacy = request.result
      if (created || !legacy.objectStoreNames.contains(PENDING_OPS_STORE)) {
        legacy.close()
        if (created) indexedDB.deleteDatabase(LEGACY_DB_NAME)
        resolve(true)
        return
      }
      const readTx = legacy.transaction(PENDING_OPS_STORE, 'readonly')
      const getAll = readTx.objectStore(PENDING_OPS_STORE).getAll()
      getAll.onerror = (): void => { legacy.close(); resolve(false) }
      getAll.onsuccess = (): void => {
        const operations = (getAll.result || []) as PendingOp[]
        if (operations.length === 0) {
          legacy.close()
          resolve(true)
          return
        }
        const writeTx = target.transaction(PENDING_OPS_STORE, 'readwrite')
        const store = writeTx.objectStore(PENDING_OPS_STORE)
        for (const operation of operations) store.put(operation)
        writeTx.oncomplete = (): void => { legacy.close(); resolve(true) }
        writeTx.onerror = (): void => { legacy.close(); resolve(false) }
        writeTx.onabort = (): void => { legacy.close(); resolve(false) }
      }
    }
  })
  if (migrated) localStorage.setItem(MIGRATION_MARKER, '1')
}

export async function addOp(op: PendingOp): Promise<void> {
  const database: IDBDatabase = await openDB()
  return new Promise((resolve, reject) => {
    const tx: IDBTransaction = database.transaction(PENDING_OPS_STORE, 'readwrite')
    const store: IDBObjectStore = tx.objectStore(PENDING_OPS_STORE)
    const request: IDBRequest = store.put(op)
    request.onsuccess = (): void => resolve()
    request.onerror = (e: Event): void => reject((e.target as IDBRequest).error)
  })
}

export async function getAllOps(): Promise<PendingOp[]> {
  const database: IDBDatabase = await openDB()
  return new Promise((resolve, reject) => {
    const tx: IDBTransaction = database.transaction(PENDING_OPS_STORE, 'readonly')
    const store: IDBObjectStore = tx.objectStore(PENDING_OPS_STORE)
    const index: IDBIndex = store.index('createdAt')
    const request: IDBRequest<PendingOp[]> = index.getAll()
    request.onsuccess = (e: Event): void => resolve((e.target as IDBRequest<PendingOp[]>).result || [])
    request.onerror = (e: Event): void => reject((e.target as IDBRequest).error)
  })
}

export async function deleteOp(id: string): Promise<void> {
  const database: IDBDatabase = await openDB()
  return new Promise((resolve, reject) => {
    const tx: IDBTransaction = database.transaction(PENDING_OPS_STORE, 'readwrite')
    const store: IDBObjectStore = tx.objectStore(PENDING_OPS_STORE)
    const request: IDBRequest = store.delete(id)
    request.onsuccess = (): void => resolve()
    request.onerror = (e: Event): void => reject((e.target as IDBRequest).error)
  })
}

export async function clearOps(): Promise<void> {
  const database: IDBDatabase = await openDB()
  return new Promise((resolve, reject) => {
    const tx: IDBTransaction = database.transaction(PENDING_OPS_STORE, 'readwrite')
    const store: IDBObjectStore = tx.objectStore(PENDING_OPS_STORE)
    const request: IDBRequest = store.clear()
    request.onsuccess = (): void => resolve()
    request.onerror = (e: Event): void => reject((e.target as IDBRequest).error)
  })
}

export function isDBAvailable(): boolean {
  return !dbUnavailable
}
