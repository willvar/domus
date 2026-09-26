import { ref } from 'vue'
import api from './useApi'
import { useI18n } from './useI18n'
import { usePreferences } from './usePreferences'
import { useServiceWorker } from './useServiceWorker'
import { useAppMessage } from '../ui/feedback'
import type { RenditionArtifact, RenditionSummary, RenditionsResponse } from '../types'


/** Fixed playback-quality menu order, mirroring the worker profiles. */
export const RENDITION_PROFILES = ['2160p', '1440p', '1080p', '720p', '480p'] as const

/** Snapshot of playback internals for the "stats for nerds" overlay. */
export interface PlaybackStats {
  quality: string
  /** True while the MSE rendition pipeline is attached (false = raw source). */
  segmented: boolean
  renditionStatus?: string
  renditionProgress?: number
  codecs?: string
  videoWidth?: number
  videoHeight?: number
  currentTime?: number
  duration?: number
  bufferedAhead: number
  bufferedBehind: number
  bufferedRanges: number
  appendedSegments?: number
  totalSegments?: number
  droppedFrames?: number
  totalFrames?: number
}

/**
 * Server-side transcode playback: attaches a quality rendition to a video
 * element via MSE and streams the growing segment manifest while the worker
 * is still transcoding, falling back to the original (fully decrypted) source.
 */
export function useRenditions(options: {
  decryptUrl: () => string
  /** Source media codec string (from /file/access); shown for raw playback. */
  sourceCodecs?: () => string | undefined
}) {
  const renditions = ref<RenditionSummary[]>([])
  const sourceHeight = ref(0)
  const activeQuality = ref<string>('original')

  let video: HTMLVideoElement | null = null
  let filePath = ''
  let mediaSource: MediaSource | null = null
  let objectUrl = ''
  let sourceBuffer: SourceBuffer | null = null
  let initArtifact: RenditionArtifact | null = null
  let initAppended = false
  let segments: RenditionArtifact[] = []
  let appendedEnd = 0
  let appending = false
  let evicting = false
  let finalized = false
  let ended = false
  let restoreTime = 0
  let pendingSeek: number | null = null
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let pumpTimer: ReturnType<typeof setTimeout> | null = null
  let attachGeneration = 0
  let selectionGeneration = 0
  let refreshRequest = 0
  let attachedProfile: string | null = null
  let playbackIntent: boolean | null = null
  const mediaCleanup: Array<() => void> = []
  const artifactRequests = new Set<AbortController>()

  // View-facing readiness signals: resolve when a rendition's pipeline is
  // attached and appending, reject when the rendition fails or playback is
  // torn down. The preview view shows a loading overlay until these settle.
  const readyWaiters = new Map<string, Array<{ resolve: () => void; reject: (reason: Error) => void }>>()

  function waitPlayable(profile: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const list = readyWaiters.get(profile) || []
      list.push({ resolve, reject })
      readyWaiters.set(profile, list)
    })
  }

  function settleWaiters(profile: string, error?: Error): void {
    const list = readyWaiters.get(profile)
    if (!list) return
    readyWaiters.delete(profile)
    for (const waiter of list) error ? waiter.reject(error) : waiter.resolve()
  }

  function settleAllWaiters(error: Error): void {
    for (const profile of [...readyWaiters.keys()]) settleWaiters(profile, error)
  }

  const { t } = useI18n()
  const notify = useAppMessage()

  function findRendition(profile: string): RenditionSummary | undefined {
    return renditions.value.find(rendition => rendition.profile === profile)
  }

  async function refresh(): Promise<boolean> {
    if (!filePath) return false
    const request = ++refreshRequest
    const requestPath = filePath
    const generation = selectionGeneration
    const attachment = attachGeneration
    const response = await api.get<RenditionsResponse>('/file/renditions', { params: { path: filePath } })
    if (request !== refreshRequest || requestPath !== filePath || generation !== selectionGeneration || attachment !== attachGeneration) return false
    const payload = response.data
    renditions.value = Array.isArray(payload?.renditions) ? payload.renditions : []
    sourceHeight.value = payload?.source_height || 0
    return true
  }

  /** Fetches and decrypts one derived artifact through the service worker. */
  async function fetchArtifact(artifact: RenditionArtifact, generation: number): Promise<ArrayBuffer> {
    if (artifact.size > 64 * 1024 * 1024) throw new RangeError('Media segment exceeds the playback buffer budget')
    const control = new AbortController()
    artifactRequests.add(control)
    const sw = useServiceWorker()
    const registered = sw.registerDecrypt({
      url: artifact.url,
      size: artifact.size,
      chunkSize: 0,
      contentType: 'video/mp4',
      filename: '',
      dek: artifact.dek,
    })
    try {
      if (!registered) throw new Error('Service worker decryption unavailable')
      await sw.flush()
      control.signal.throwIfAborted()
      const response = await fetch(registered, { signal: control.signal })
      if (!response.ok) throw new Error(`artifact fetch failed: ${response.status}`)
      if (generation !== attachGeneration) throw new Error('stale artifact fetch')
      const buffer = await response.arrayBuffer()
      control.signal.throwIfAborted()
      return buffer
    } finally {
      artifactRequests.delete(control)
      if (registered) sw.unregisterDecrypt(registered)
    }
  }

  function mimeType(rendition: RenditionSummary): string {
    const codecs = rendition.codecs || (rendition.profile === 'original' ? '' : 'avc1.640028,mp4a.40.2')
    return `video/mp4; codecs="${codecs}"`
  }

  /**
   * iOS Safari (iPhone) exposes the MSE pipeline only as ManagedMediaSource;
   * the classic MediaSource global may be absent there. The two share the
   * source-buffer API, differing in the open signal ('startstreaming' vs
   * 'sourceopen') and in requiring disableRemotePlayback.
   */
  function mediaSourceCtor(): any | null {
    if (typeof MediaSource !== 'undefined') return MediaSource
    const managed = (globalThis as any).ManagedMediaSource
    return typeof managed === 'function' ? managed : null
  }

  /** Picks the first MIME type the platform's MSE actually accepts. */
  function resolveMime(rendition: RenditionSummary): string | null {
    const ctor = mediaSourceCtor()
    if (!ctor) return null
    if (rendition.profile === 'original') {
      if (!rendition.codecs) return null
      const mime = mimeType(rendition)
      return ctor.isTypeSupported(mime) ? mime : null
    }
    const candidates = [
      mimeType(rendition),
      // Renditions created before the worker's codec-string fix may carry an
      // unparseable level; the canonical High@4.0 hint still plays them.
      'video/mp4; codecs="avc1.640028,mp4a.40.2"',
    ]
    for (const candidate of candidates) {
      if (ctor.isTypeSupported(candidate)) return candidate
    }
    return null
  }

  function stopPolling(): void {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  function startPolling(): void {
    if (pollTimer) return
    pollTimer = setInterval(() => {
      void pollTick()
    }, 2000)
  }

  function teardownMedia(): void {
    attachGeneration++
    for (const control of artifactRequests) control.abort()
    artifactRequests.clear()
    for (const cleanup of mediaCleanup.splice(0)) cleanup()
    attachedProfile = null
    stopPolling()
    if (video) {
      video.removeEventListener('seeking', onSeeking)
      video.pause()
      video.removeAttribute('src')
      video.load()
    }
    sourceBuffer = null
    initArtifact = null
    initAppended = false
    segments = []
    appendedEnd = 0
    appending = false
    evicting = false
    finalized = false
    ended = false
    restoreTime = 0
    pendingSeek = null
    if (pumpTimer) {
      clearTimeout(pumpTimer)
      pumpTimer = null
    }
    if (mediaSource) {
      try { mediaSource.endOfStream() } catch { /* already detached */ }
      mediaSource = null
    }
    if (objectUrl) {
      URL.revokeObjectURL(objectUrl)
      objectUrl = ''
    }
  }

  function finalizeStream(): void {
    if (ended || !finalized || appendedEnd < segments.length) return
    ended = true
    if (mediaSource && mediaSource.readyState === 'open') {
      try {
        // Duration is already set (from the manifest); endOfStream lets the
        // browser snap it to the exact highest buffered timestamp.
        mediaSource.endOfStream()
      } catch { /* already ended */ }
    }
  }

  /** How much unplayed video to keep buffered ahead of the playhead. */
  const BUFFER_AHEAD_SECONDS = 45
  /** How much already-played video to keep behind the playhead. */
  const KEEP_BEHIND_SECONDS = 30

  // Time alone is not a memory budget for high-bitrate originals. Estimate
  // bytes from the manifest and stop at whichever bound is reached first.
  function bufferEdge(time: number, ahead: boolean, tight = false): number {
    let seconds = ahead ? BUFFER_AHEAD_SECONDS : tight ? 5 : KEEP_BEHIND_SECONDS
    let bytes = (ahead ? 32 : tight ? 2 : 16) * 1024 * 1024
    let start = 0
    const spans = segments.map(segment => {
      const span = { start, end: start + (segment.duration || 0), size: segment.size }
      start = span.end
      return span
    })
    let edge = time
    for (const span of ahead ? spans : spans.reverse()) {
      if (ahead ? span.end <= time : span.start >= time) continue
      const duration = span.end - span.start
      if (duration <= 0) continue
      const available = ahead ? span.end - Math.max(time, span.start) : Math.min(time, span.end) - span.start
      const length = Math.min(available, seconds, span.size > 0 ? bytes / span.size * duration : seconds)
      edge += ahead ? length : -length
      seconds -= length
      bytes -= length / duration * span.size
      if (length < available || seconds <= 0 || bytes <= 0) break
    }
    return Math.max(0, edge)
  }

  function totalDuration(): number {
    let total = 0
    for (const artifact of segments) total += artifact.duration || 0
    return total
  }

  function segmentIndexAt(time: number): number {
    let start = 0
    for (let index = 0; index < segments.length; index++) {
      start += segments[index].duration || 0
      if (time < start) return index
    }
    return Math.max(0, segments.length - 1)
  }

  function bufferedAhead(time: number): number {
    if (!sourceBuffer) return 0
    const buffered = sourceBuffer.buffered
    for (let i = 0; i < buffered.length; i++) {
      if (buffered.start(i) <= time && time < buffered.end(i)) return buffered.end(i) - time
    }
    return 0
  }

  function bufferedCovers(time: number): boolean {
    if (!sourceBuffer) return false
    const buffered = sourceBuffer.buffered
    for (let i = 0; i < buffered.length; i++) {
      if (buffered.start(i) <= time && time < buffered.end(i)) return true
    }
    return false
  }

  function schedulePump(delay = 500): void {
    if (pumpTimer) return
    pumpTimer = setTimeout(() => {
      pumpTimer = null
      pump()
    }, delay)
  }

  /**
   * Drops buffered data behind the playhead (each dropped segment stays
   * fetchable — it is its own object) so the SourceBuffer never fills up.
   * Returns true when a removal is in flight; its updateend re-pumps.
   */
  function evictBehind(time: number, tight = false): boolean {
    const target = sourceBuffer
    if (!target || target.updating || appending || evicting) return false
    const budgetEdge = bufferEdge(time, false, tight)
    const future = bufferEdge(time, true)
    let edge = 0
    let futureEnd = 0
    for (const segment of segments) {
      futureEnd += segment.duration || 0
      // remove() may also discard dependent frames up to the next keyframe.
      // Round down to an independent segment boundary, including on quota
      // recovery, so a tight byte budget cannot delete the current GOP.
      if (futureEnd <= budgetEdge) edge = futureEnd
      if (futureEnd >= future) break
    }
    const buffered = target.buffered
    let start = -1
    let end = -1
    for (let i = 0; i < buffered.length; i++) {
      if (buffered.start(i) < edge - 0.25) {
        start = buffered.start(i)
        end = Math.min(buffered.end(i), edge)
        break
      }
      // Trim future data even when it belongs to the same continuous range.
      // Keep a full boundary segment so removal cannot cut the next GOP.
      if (buffered.end(i) > futureEnd + 0.25 && futureEnd > time) {
        start = Math.max(buffered.start(i), futureEnd)
        end = buffered.end(i)
        appendedEnd = Math.min(appendedEnd, segmentIndexAt(futureEnd))
        ended = false
        break
      }
    }
    if (start < 0 || end <= start) return false
    evicting = true
    target.addEventListener('updateend', () => {
      if (sourceBuffer !== target) return
      evicting = false
      pump()
    }, { once: true })
    try {
      target.remove(start, end)
    } catch {
      evicting = false
      return false
    }
    return true
  }

  /** Moves the append window to a segment index after an out-of-region seek.
   *  fMP4 segments carry their own timestamps, so the target segment appends
   *  straight into a fresh buffered range while previously buffered data
   *  stays playable — evictBehind trims it as the playhead moves on. The
   *  old full-buffer wipe here is what made every seek stall. */
  function restartWindow(index: number): void {
    appendedEnd = Math.max(index, 0)
    pump()
  }

  /** Fetches + appends one artifact; onDone runs on the completion tick. */
  function appendArtifact(artifact: RenditionArtifact, generation: number, onDone: () => void): void {
    appending = true
    fetchArtifact(artifact, generation)
      .then(buffer => {
        const target = sourceBuffer
        if (!target || generation !== attachGeneration) return
        try {
          target.appendBuffer(buffer)
        } catch (reason) {
          console.warn('rendition append failed:', reason)
          appending = false
          // QuotaExceededError: drop buffered data behind the playhead,
          // then let the scheduled pump retry the same artifact.
          evictBehind(video?.currentTime || 0, true)
          schedulePump()
          return
        }
        target.addEventListener('updateend', () => {
          if (generation !== attachGeneration || sourceBuffer !== target) return
          appending = false
          onDone()
        }, { once: true })
      })
      .catch(reason => {
        if (generation !== attachGeneration) return
        appending = false
        if (reason instanceof RangeError) {
          settleAllWaiters(reason)
          void playOriginal()
          return
        }
        schedulePump()
      })
  }

  /**
   * Buffer controller: keeps a bounded window (BUFFER_AHEAD ahead of the
   * playhead, KEEP_BEHIND behind it) of independent per-segment objects
   * appended to MSE, evicting what falls out of the window. Segments are
   * only appended contiguously from the current window start; a seek
   * outside the buffer restarts the window at the target segment.
   */
  function pump(): void {
    if (!sourceBuffer || !video || appending || evicting) return
    if (pendingSeek !== null) {
      const target = pendingSeek
      pendingSeek = null
      if (!bufferedCovers(target)) {
        restartWindow(segmentIndexAt(target))
        return
      }
    }
    if (evictBehind(video.currentTime)) return
    if (bufferedAhead(video.currentTime) >= Math.max(0.1, bufferEdge(video.currentTime, true) - video.currentTime)) {
      schedulePump()
      return
    }
    if (!initAppended) {
      if (!initArtifact) return
      const generation = attachGeneration
      appendArtifact(initArtifact, generation, () => {
        initAppended = true
        if (restoreTime > 0 && video) {
          video.currentTime = restoreTime
          restoreTime = 0
        }
        pump()
      })
      return
    }
    if (appendedEnd >= segments.length) {
      if (finalized) finalizeStream()
      schedulePump()
      return
    }
    const generation = attachGeneration
    const artifact = segments[appendedEnd]
    appendArtifact(artifact, generation, () => {
      appendedEnd++
      if (finalized && appendedEnd >= segments.length) {
        finalizeStream()
        schedulePump()
        return
      }
      pump()
    })
  }

  function restoreAfterMetadata(time: number): void {
    const target = video
    if (!target || !time) return
    const generation = attachGeneration
    const cleanup = (): void => target.removeEventListener('loadedmetadata', onLoaded)
    const onLoaded = (): void => {
      cleanup()
      if (generation !== attachGeneration) return
      if (Number.isFinite(target.duration) && time < target.duration) target.currentTime = time
    }
    mediaCleanup.push(cleanup)
    target.addEventListener('loadedmetadata', onLoaded)
  }

  /** Resolves readiness waiters and resumes playback (if the source was
   *  playing before the switch) once the first frame of the new source is
   *  actually decodable. Auto-play can legitimately be rejected by the
   *  browser's gesture policy after a long transcode wait — then the video
   *  simply stays paused at the restored position. */
  function settleOnCanPlay(profile: string, wasPlaying: boolean): void {
    const target = video
    if (!target) return
    const generation = attachGeneration
    const selection = selectionGeneration
    const cleanup = (): void => target.removeEventListener('canplay', onCanPlay)
    const onCanPlay = (): void => {
      cleanup()
      if (generation !== attachGeneration) return
      if (selection === selectionGeneration) settleWaiters(profile)
      playbackIntent = null
      if (wasPlaying) target.play().catch(() => {})
    }
    mediaCleanup.push(cleanup)
    target.addEventListener('canplay', onCanPlay)
  }

  async function playOriginal(): Promise<void> {
    if (!video) return
    const keep = restoreTime || video.currentTime
    const wasPlaying = playbackIntent ?? !video.paused
    teardownMedia()
    playbackIntent = wasPlaying
    activeQuality.value = 'original'
    video.src = options.decryptUrl()
    restoreAfterMetadata(keep)
    settleOnCanPlay('original', wasPlaying)
  }

  /** Clamps seeks to the generated region while the rendition is still growing. */
  function onSeeking(): void {
    if (!video) return
    if (!finalized) {
      // During transcoding the player may not seek past the published tail.
      const limit = totalDuration()
      if (limit > 0 && video.currentTime > limit) {
        video.currentTime = Math.max(0, limit - 0.1)
      }
    }
    if (sourceBuffer && !bufferedCovers(video.currentTime)) {
      ended = false
      for (const control of artifactRequests) control.abort()
      // Out-of-buffer seek: steer the append window to the target segment.
      pendingSeek = video.currentTime
      pump()
    }
  }

  /** Attaches the MSE pipeline for one rendition, or polls until decodable. */
  async function playRendition(profile: string): Promise<void> {
    const rendition = findRendition(profile)
    if (!video || !rendition) return
    if (!rendition.init) {
      // Nothing decodable yet: keep the original playing and poll until the
      // init segment appears, then attach automatically.
      activeQuality.value = profile
      startPolling()
      return
    }
    const mime = resolveMime(rendition)
    if (!mime) {
      // Older iOS Safari has no MSE: the rendition pipeline cannot attach.
      settleWaiters(profile, new Error('MSE unsupported'))
      notify.info(t('quality.mse_unsupported'))
      await playOriginal()
      return
    }
    const keep = restoreTime || video.currentTime
    const wasPlaying = playbackIntent ?? !video.paused
    teardownMedia()
    const generation = attachGeneration
    playbackIntent = wasPlaying
    attachedProfile = profile
    activeQuality.value = profile
    restoreTime = keep
    const ctor = mediaSourceCtor()
    if (!ctor) {
      await playOriginal()
      return
    }
    const managed = typeof MediaSource === 'undefined'
    const source: MediaSource = new (ctor as any)()
    mediaSource = source
    objectUrl = URL.createObjectURL(source)
    if (managed) video.disableRemotePlayback = true
    video.src = objectUrl
    video.addEventListener('seeking', onSeeking)
    await onceSourceOpen(source, managed)
    if (generation !== attachGeneration || mediaSource !== source) return
    const current = findRendition(profile)
    if (!mediaSource || !video || activeQuality.value !== profile || !current?.init) {
      console.warn('rendition attach aborted', { hasSource: !!mediaSource, hasVideo: !!video, active: activeQuality.value, profile, hasInit: !!current?.init })
      settleWaiters(profile, new Error('rendition attach aborted'))
      await playOriginal()
      return
    }
    try {
      sourceBuffer = mediaSource.addSourceBuffer(mime)
    } catch (reason) {
      console.warn('addSourceBuffer failed:', reason)
      settleWaiters(profile, new Error('addSourceBuffer failed'))
      await playOriginal()
      return
    }
    initArtifact = current.init
    segments = current.segments || []
    initAppended = false
    appendedEnd = 0
    finalized = current.status === 'ready'
    // Surface the total duration immediately so the seek bar shows the full
    // length instead of waiting for every segment to append.
    if (current.duration && current.duration > (mediaSource.duration || 0)) {
      try { mediaSource.duration = current.duration } catch { /* will follow the appends */ }
    }
    pump()
    if (!finalized) startPolling()
    settleOnCanPlay(profile, wasPlaying)
  }

  function onceSourceOpen(source: MediaSource, managed: boolean): Promise<void> {
    return new Promise(resolve => {
      if (source.readyState === 'open') {
        resolve()
        return
      }
      // ManagedMediaSource (iOS) signals readiness with 'startstreaming'
      // instead of 'sourceopen'; listen for both once.
      const onOpen = (): void => {
        mediaCleanup.splice(mediaCleanup.indexOf(cancelSourceOpen), 1)
        source.removeEventListener('sourceopen', onOpen)
        source.removeEventListener('startstreaming', onOpen)
        resolve()
      }
      const cancelSourceOpen = (): void => {
        source.removeEventListener('sourceopen', onOpen)
        source.removeEventListener('startstreaming', onOpen)
        resolve()
      }
      mediaCleanup.push(cancelSourceOpen)
      source.addEventListener('sourceopen', onOpen)
      if (managed) source.addEventListener('startstreaming', onOpen)
    })
  }

  async function pollTick(): Promise<void> {
    if (!video) {
      stopPolling()
      return
    }
    const selection = selectionGeneration
    const attachment = attachGeneration
    try {
      if (!await refresh()) return
    } catch (reason) {
      console.warn('rendition refresh failed:', reason)
      return
    }
    if (selection !== selectionGeneration || attachment !== attachGeneration || !video) return
    // Judge after the refresh: the selected rendition may only appear in the
    // manifest after the queue request was accepted server-side.
    const rendition = findRendition(activeQuality.value)
    if (!rendition) {
      if (readyWaiters.has(activeQuality.value) || attachedProfile === activeQuality.value) {
        settleWaiters(activeQuality.value, new Error('rendition vanished'))
        await playOriginal()
      }
      stopPolling()
      return
    }
    if (rendition.status === 'failed' || rendition.status === 'cancelled') {
      settleWaiters(activeQuality.value, new Error(rendition.error || 'rendition failed'))
      stopPolling()
      // Recover to the raw original unless the raw original IS the current
      // source. A mounted MSE source (segmented original included) must be
      // torn down — a partial one would freeze on the last appended segment;
      // but a raw file playing while its background remux fails needs no
      // reload.
      if (mediaSource || activeQuality.value !== 'original') await playOriginal()
      return
    }
    const updated = findRendition(activeQuality.value)
    if (!updated) return
    if (updated.init && attachedProfile !== activeQuality.value) {
      // The init segment just appeared: attach the MSE pipeline now.
      await playRendition(activeQuality.value)
      return
    }
    if (!sourceBuffer || attachedProfile !== activeQuality.value) return
    const published = updated.segments || []
    if (published.length > segments.length) {
      segments = published
      pump()
    }
    if (updated.duration && mediaSource && updated.duration > (mediaSource.duration || 0)) {
      try { mediaSource.duration = updated.duration } catch { /* grows with the appends */ }
    }
    if (updated.status === 'ready') {
      segments = published
      finalized = true
      stopPolling()
      pump()
      finalizeStream()
    }
  }

  /** Switches playback quality. Manual picks here never overwrite the saved
   *  default preference (AccountMenu) — that preference only drives which
   *  quality attach() applies when a video is opened. */
  async function selectQuality(profile: string, opts: { silent?: boolean } = {}): Promise<void> {
    if (!video) return
    const rendition = findRendition(profile)
    const failed = rendition?.status === 'failed' || rendition?.status === 'cancelled'
    if (profile === activeQuality.value && !failed &&
      (attachedProfile === profile || readyWaiters.has(profile))) return

    const selection = ++selectionGeneration
    settleAllWaiters(new Error('playback selection superseded'))
    stopPolling()
    // Keep a fully attached source on its own segments while the new init
    // is pending; an unfinished attachment cannot keep playing.
    if (mediaSource && (!sourceBuffer || (failed && attachedProfile === profile) ||
      (profile === 'original' && (!rendition?.init || failed)))) {
      await playOriginal()
      if (selection !== selectionGeneration) return
    }
    activeQuality.value = profile
    if (rendition && !failed && (rendition.status === 'ready' || rendition.init)) {
      await playRendition(profile)
      return
    }

    const pending = waitPlayable(profile)
    // Original upgrades in the background. Other selections must settle
    // on supersession even while their queue POST is still in flight.
    if (profile === 'original') void pending.catch(() => {})
    const queue = async (): Promise<void> => {
      if (!rendition || failed) {
        try {
          await api.post('/file/transcode', { path: filePath, profile })
        } catch (reason: unknown) {
          if (!reason || typeof reason !== 'object' || !('response' in reason) ||
            !reason.response || typeof reason.response !== 'object' ||
            !('status' in reason.response) || reason.response.status !== 409) throw reason
          if (selection !== selectionGeneration) return
          await refresh()
          if (selection !== selectionGeneration) return
          const existing = findRendition(profile)
          if (!existing || existing.status === 'failed' || existing.status === 'cancelled') throw reason
          if (existing.status === 'ready' || existing.init) {
            await playRendition(profile)
            return
          }
        }
      }
      if (selection !== selectionGeneration) return
      // POST creates its row synchronously: missing on the next poll means
      // cancellation/replacement, not eventual queue visibility.
      startPolling()
      if (profile !== 'original' && !opts.silent) notify.info(t('quality.queued'))
    }
    void queue().catch(async (reason: unknown) => {
      if (selection !== selectionGeneration) return
      settleWaiters(profile, reason instanceof Error ? reason : new Error('rendition queue failed'))
      if (profile === 'original') notify.error(t('quality.failed'))
      await playOriginal()
    })
    if (profile !== 'original') await pending
  }

  /** Loads the rendition manifest for a file and applies the saved default. */
  async function attach(target: HTMLVideoElement, path: string): Promise<void> {
    detach()
    renditions.value = []
    sourceHeight.value = 0
    const selection = selectionGeneration
    video = target
    filePath = path
    attachGeneration++
    // Choose the manifest source first; do not start a redundant raw 8 GiB
    // fetch immediately before attaching an already available rendition.
    activeQuality.value = 'original'
    const { prefs } = usePreferences()
    try {
      await refresh()
    } catch (reason) {
      console.warn('rendition manifest unavailable:', reason)
      if (selection === selectionGeneration && video === target) await playOriginal()
      return
    }
    if (selection !== selectionGeneration || video !== target) return
    const preference = prefs.playbackQuality === 'auto' ? 'original' : prefs.playbackQuality
    // 'auto' was removed as a choice; stored values fall back to original.
    // The explicit 'original' preference only ever uses the remuxed original
    // or the raw source — it must not fall back to downscaled transcodes.
    const candidates = !preference || preference === 'original'
      ? ['original']
      : [preference]
    const wanted = candidates.find(profile => {
      const rendition = findRendition(profile)
      return rendition?.status === 'ready' || (rendition?.status === 'running' && rendition.init)
    })
    if (wanted && findRendition(wanted)) {
      await playRendition(wanted)
      return
    }
    if (preference && preference !== 'auto' && preference !== 'original') {
      // The remembered default has no rendition for this file yet: keep the
      // original playing and tell the user how to get it.
      notify.info(t('quality.preferred_missing', { profile: preference.toUpperCase() }))
    }
    await playOriginal()
  }

  /** Snapshot for the "stats for nerds" overlay. */
  function getStats(): PlaybackStats {
    let bufferedAhead = 0
    let bufferedBehind = 0
    let bufferedRanges = 0
    if (video) {
      const buffered = video.buffered
      bufferedRanges = buffered.length
      for (let index = 0; index < buffered.length; index++) {
        if (buffered.start(index) <= video.currentTime && video.currentTime < buffered.end(index)) {
          bufferedAhead = buffered.end(index) - video.currentTime
          bufferedBehind = video.currentTime - buffered.start(index)
          break
        }
      }
    }
    const rendition = findRendition(activeQuality.value)
    let droppedFrames: number | undefined
    let totalFrames: number | undefined
    if (video) {
      const playbackQuality = (video as any).getVideoPlaybackQuality?.()
      if (playbackQuality) {
        droppedFrames = playbackQuality.droppedVideoFrames
        totalFrames = playbackQuality.totalVideoFrames
      } else if ('webkitDroppedFrameCount' in video) {
        droppedFrames = (video as any).webkitDroppedFrameCount
        totalFrames = (video as any).webkitDecodedFrameCount
      }
    }
    return {
      quality: activeQuality.value,
      segmented: !!mediaSource,
      renditionStatus: rendition?.status,
      renditionProgress: rendition?.progress,
      codecs: mediaSource ? findRendition(attachedProfile || '')?.codecs : options.sourceCodecs?.() || undefined,
      videoWidth: video?.videoWidth || undefined,
      videoHeight: video?.videoHeight || undefined,
      currentTime: video?.currentTime,
      duration: video?.duration && Number.isFinite(video.duration) ? video.duration : undefined,
      bufferedAhead, bufferedBehind, bufferedRanges,
      appendedSegments: mediaSource ? appendedEnd : undefined,
      totalSegments: mediaSource ? segments.length : undefined,
      droppedFrames, totalFrames,
    }
  }

  function detach(): void {
    selectionGeneration++
    settleAllWaiters(new Error('playback detached'))
    teardownMedia()
    playbackIntent = null
    video = null
    filePath = ''
  }

  return { renditions, sourceHeight, activeQuality, attach, detach, selectQuality, refresh, getStats }
}
