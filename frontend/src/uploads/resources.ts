// Capacity is acquired before reading plaintext and held until the HTTP body
// is no longer in use. Paused/cancelled waiters own no resource.
export class ResourcePool<T> {
  private free: T[]
  private waiters: Array<{ resolve: (value: T) => void; reject: (error: unknown) => void; signal: AbortSignal; abort: () => void }> = []

  constructor(resources: T[]) { this.free = [...resources] }

  acquire(signal: AbortSignal): Promise<T> {
    signal.throwIfAborted()
    const resource = this.free.shift()
    if (resource !== undefined) return Promise.resolve(resource)
    return new Promise((resolve, reject) => {
      const waiter = { resolve, reject, signal, abort: () => {
        this.waiters = this.waiters.filter(item => item !== waiter)
        reject(signal.reason)
      } }
      this.waiters.push(waiter)
      signal.addEventListener('abort', waiter.abort, { once: true })
    })
  }

  release(resource: T): void {
    const waiter = this.waiters.shift()
    if (waiter) {
      waiter.signal.removeEventListener('abort', waiter.abort)
      waiter.resolve(resource)
    } else this.free.push(resource)
  }
}
