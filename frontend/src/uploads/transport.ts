import axios, { AxiosError } from 'axios'
import type { AxiosAdapter } from 'axios'
import axiosRetry from 'axios-retry'

export type UploadBody = Blob

const IDLE_TIMEOUT_MS = 120000

// XHR's timeout limits total elapsed time, even while bytes are still moving.
// Give each retry its own no-progress deadline and keep the caller's signal on
// the retry config: the attempt-local signal is aborted when a stall expires.
const putWithIdleTimeout: AxiosAdapter = async config => {
  const attempt = new AbortController()
  const abort = () => attempt.abort()
  config.signal?.addEventListener?.('abort', abort, { once: true })
  if (config.signal?.aborted) abort()
  let stalled = false
  let loaded = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  const arm = () => {
    clearTimeout(timer)
    timer = setTimeout(() => { stalled = true; attempt.abort() }, IDLE_TIMEOUT_MS)
  }
  arm()
  try {
    const response = await axios.getAdapter('xhr')({
      ...config, timeout: 0, signal: attempt.signal,
      onUploadProgress(progress) {
        if (progress.loaded > loaded) { loaded = progress.loaded; arm() }
        config.onUploadProgress?.(progress)
      },
    })
    response.config = config
    return response
  } catch (error) {
    if (stalled && !config.signal?.aborted) {
      throw new AxiosError('Upload made no progress for 120 seconds', AxiosError.ETIMEDOUT, config)
    }
    if (axios.isAxiosError(error)) error.config = config
    throw error
  } finally {
    clearTimeout(timer)
    config.signal?.removeEventListener?.('abort', abort)
  }
}

// A separate client sends only presigned ciphertext requests, with no Domus
// cookies, authentication interceptors or response JSON transformations.
const transport = axios.create({
  adapter: putWithIdleTimeout,
  timeout: 0,
  withCredentials: false,
  headers: { 'Content-Type': false },
  // Keep disk-backed Files intact through dispatch and retries.
  transformRequest: [data => data],
  transformResponse: [data => data],
})
axiosRetry(transport, {
  retries: 4,
  shouldResetTimeout: true,
  retryDelay: (count, error) => axiosRetry.exponentialDelay(count, error, 1000),
  retryCondition: error => !axios.isCancel(error) && (
    error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT' || axiosRetry.isNetworkOrIdempotentRequestError(error)
  ),
})

// The caller retains its capacity lease until this promise settles, including
// all retries and AbortSignal-driven XHR teardown.
export async function putPart(url: string, body: UploadBody, signal: AbortSignal): Promise<void> {
  signal.throwIfAborted()
  await transport.put(url, body, { signal })
}
