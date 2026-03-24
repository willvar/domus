const DB_NAME = 'zephyr'
const DB_VERSION = 1
const STORE_NAME = 'pending_ops'

let db = null
let dbUnavailable = false

function openDB() {
  if (db) return Promise.resolve(db)
  if (dbUnavailable) return Promise.reject(new Error('IndexedDB unavailable'))

  return new Promise((resolve, reject) => {
    let request
    try {
      request = indexedDB.open(DB_NAME, DB_VERSION)
    } catch {
      dbUnavailable = true
      console.warn('[IndexedDB] Not available in this environment')
      return reject(new Error('IndexedDB unavailable'))
    }

    request.onupgradeneeded = (e) => {
      const database = e.target.result
      if (!database.objectStoreNames.contains(STORE_NAME)) {
        const store = database.createObjectStore(STORE_NAME, { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt', { unique: false })
      }
    }

    request.onsuccess = (e) => {
      db = e.target.result
      resolve(db)
    }

    request.onerror = (e) => {
      dbUnavailable = true
      console.warn('[IndexedDB] Failed to open:', e.target.error)
      reject(e.target.error)
    }
  })
}

export async function addOp(op) {
  const database = await openDB()
  return new Promise((resolve, reject) => {
    const tx = database.transaction(STORE_NAME, 'readwrite')
    const store = tx.objectStore(STORE_NAME)
    const request = store.put(op)
    request.onsuccess = () => resolve()
    request.onerror = (e) => reject(e.target.error)
  })
}

export async function getAllOps() {
  const database = await openDB()
  return new Promise((resolve, reject) => {
    const tx = database.transaction(STORE_NAME, 'readonly')
    const store = tx.objectStore(STORE_NAME)
    const index = store.index('createdAt')
    const request = index.getAll()
    request.onsuccess = (e) => resolve(e.target.result || [])
    request.onerror = (e) => reject(e.target.error)
  })
}

export async function deleteOp(id) {
  const database = await openDB()
  return new Promise((resolve, reject) => {
    const tx = database.transaction(STORE_NAME, 'readwrite')
    const store = tx.objectStore(STORE_NAME)
    const request = store.delete(id)
    request.onsuccess = () => resolve()
    request.onerror = (e) => reject(e.target.error)
  })
}

export async function clearOps() {
  const database = await openDB()
  return new Promise((resolve, reject) => {
    const tx = database.transaction(STORE_NAME, 'readwrite')
    const store = tx.objectStore(STORE_NAME)
    const request = store.clear()
    request.onsuccess = () => resolve()
    request.onerror = (e) => reject(e.target.error)
  })
}

export function isDBAvailable() {
  return !dbUnavailable
}
