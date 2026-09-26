export interface PartSpool {
  write(bytes: Uint8Array<ArrayBuffer>): void
  finish(): Promise<File>
  dispose(): Promise<void>
}

interface SyncFile {
  write(bytes: Uint8Array<ArrayBuffer>): number
  close(): void
}

// S3 parts are ciphertext-only disk files. A Web Lock covers their whole
// lifetime, so a later worker can remove crash leftovers without touching files
// still in use by another tab.
export async function createPartSpool(): Promise<PartSpool> {
  if (!globalThis.navigator?.storage?.getDirectory || !navigator.locks) {
    throw new Error('Encrypted uploads require browser disk staging support (OPFS and Web Locks)')
  }
  const directory = await (await navigator.storage.getDirectory()).getDirectoryHandle('domus-upload-parts', { create: true })
  for await (const [name] of (directory as FileSystemDirectoryHandle & AsyncIterable<[string, FileSystemHandle]>)) {
    if (!name.endsWith('.cipher')) continue
    await navigator.locks.request(`domus-upload-part:${name}`, { ifAvailable: true }, async lock => {
      if (lock) await directory.removeEntry(name).catch(() => {})
    })
  }
  const name = `${crypto.randomUUID()}.cipher`
  let unlock!: () => void
  let ready!: () => void
  let failed!: (error: unknown) => void
  const locked = new Promise<void>((resolve, reject) => { ready = resolve; failed = reject })
  const held = navigator.locks.request(`domus-upload-part:${name}`, () => {
    ready()
    return new Promise<void>(resolve => { unlock = resolve })
  })
  void held.catch(failed)
  await locked
  let access: SyncFile | undefined
  try {
    const file = await directory.getFileHandle(name, { create: true })
    access = await (file as FileSystemFileHandle & { createSyncAccessHandle(): Promise<SyncFile> }).createSyncAccessHandle()
    return {
      write(bytes) {
        if (!access || access.write(bytes) !== bytes.length) throw new Error('Short write staging encrypted part')
      },
      async finish() {
        access?.close()
        access = undefined
        return file.getFile()
      },
      async dispose() {
        try { access?.close(); access = undefined; await directory.removeEntry(name) }
        finally { unlock(); await held }
      },
    }
  } catch (error) {
    access?.close()
    await directory.removeEntry(name).catch(() => {})
    unlock()
    await held
    throw error
  }
}
