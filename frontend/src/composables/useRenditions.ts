import { ref } from 'vue'
import api from './useApi'
import { useI18n } from './useI18n'
import { usePreferences } from './usePreferences'
import { useServiceWorker } from './useServiceWorker'
import { useAppMessage } from '../ui/feedback'
import type { RenditionArtifact, RenditionSummary, RenditionsResponse } from '../types'


/** Fixed playback-quality menu order, mirroring the worker profiles. */
export const RENDITION_PROFILES = ['2160p', '1440p', '1080p', '720p', '480p'] as const

/**
 * Server-side transcode playback: attaches a quality rendition to a video
 * element via MSE and streams the growing segment manifest while the worker
 * is still transcoding, falling back to the original (fully decrypted) source.
 */
export function useRenditions(options: { decryptUrl: () => string }) {
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

  const { t } = useI18n()
  const notify = useAppMessage()

  function findRendition(profile: string): RenditionSummary | undefined {
    return renditions.value.find(rendition => rendition.profile === profile)
  }

  async function refresh(): Promise<void> {
    if (!filePath) return
    const response = await api.get<RenditionsResponse>('/file/renditions', { params: { path: filePath } })
    const payload = response.data
    renditions.value = Array.isArray(payload?.renditions) ? payload.renditions : []
    sourceHeight.value = payload?.source_height || 0
  }

  /** Fetches and decrypts one derived artifact through the service worker. */
  async function fetchArtifact(artifact: RenditionArtifact, generation: number): Promise<ArrayBuffer> {
    const sw = useServiceWorker()
    const registered = sw.registerDecrypt({
      url: artifact.url,
      size: artifact.size,
      chunkSize: 0,
      contentType: 'video/mp4',
      filename: '',
      dek: artifact.dek,
    })
    if (!registered) throw new Error('Service worker decryption unavailable')
    await sw.flush()
    try {
      const response = await fetch(registered)
      if (!response.ok) throw new Error(`artifact fetch failed: ${response.status}`)
      if (generation !== attachGeneration) throw new Error('stale artifact fetch')
      return await response.arrayBuffer()
    } finally {
      sw.unregisterDecrypt(registered)
    }
  }

  function mimeType(rendition: RenditionSummary): string {
    const codecs = rendition.codecs || 'avc1.640028,mp4a.40.2'
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
    stopPolling()
    if (video) video.removeEventListener('seeking', onSeeking)
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
    const keep = tight ? 5 : KEEP_BEHIND_SECONDS
    const edge = Math.max(0, time - keep)
    const buffered = target.buffered
    let start = -1
    let end = -1
    for (let i = 0; i < buffered.length; i++) {
      if (buffered.end(i) <= edge) {
        if (start < 0) start = buffered.start(i)
        end = buffered.end(i)
      }
    }
    if (start < 0 || end <= start) return false
    evicting = true
    target.addEventListener('updateend', () => {
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

  /** Restarts the append window at a segment index after an out-of-region seek. */
  function restartWindow(index: number): void {
    appendedEnd = index
    const target = sourceBuffer
    if (!target || target.updating) {
      schedulePump(100)
      return
    }
    const buffered = target.buffered
    if (buffered.length === 0) {
      pump()
      return
    }
    evicting = true
    target.addEventListener('updateend', () => {
      evicting = false
      pump()
    }, { once: true })
    try {
      target.remove(0, buffered.end(buffered.length - 1))
    } catch {
      evicting = false
      pump()
    }
  }

  /** Fetches + appends one artifact; onDone runs on the completion tick. */
  function appendArtifact(artifact: RenditionArtifact, generation: number, onDone: () => void): void {
    appending = true
    fetchArtifact(artifact, generation)
      .then(buffer => {
        const target = sourceBuffer
        if (!target || generation !== attachGeneration) {
          appending = false
          return
        }
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
          appending = false
          onDone()
        }, { once: true })
      })
      .catch(() => {
        appending = false
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
    if (!sourceBuffer || !video || appending || evicting || ended) return
    if (pendingSeek !== null) {
      const target = pendingSeek
      pendingSeek = null
      if (!bufferedCovers(target)) {
        restartWindow(segmentIndexAt(target))
        return
      }
    }
    if (bufferedAhead(video.currentTime) >= BUFFER_AHEAD_SECONDS) {
      schedulePump()
      return
    }
    if (evictBehind(video.currentTime)) return
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
      return
    }
    const generation = attachGeneration
    const artifact = segments[appendedEnd]
    appendArtifact(artifact, generation, () => {
      appendedEnd++
      if (finalized && appendedEnd >= segments.length) {
        finalizeStream()
        return
      }
      pump()
    })
  }

  function restoreAfterMetadata(time: number): void {
    if (!video || !time) return
    const onLoaded = (): void => {
      video?.removeEventListener('loadedmetadata', onLoaded)
      if (video && Number.isFinite(video.duration) && time < video.duration) video.currentTime = time
    }
    video.addEventListener('loadedmetadata', onLoaded)
  }

  async function playOriginal(): Promise<void> {
    if (!video) return
    const keep = video.currentTime
    teardownMedia()
    attachGeneration++
    activeQuality.value = 'original'
    video.src = options.decryptUrl()
    restoreAfterMetadata(keep)
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
      notify.info(t('quality.mse_unsupported'))
      return
    }
    const keep = video.currentTime
    teardownMedia()
    attachGeneration++
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
    const current = findRendition(profile)
    if (!mediaSource || !video || activeQuality.value !== profile || !current?.init) {
      console.warn('rendition attach aborted', { hasSource: !!mediaSource, hasVideo: !!video, active: activeQuality.value, profile, hasInit: !!current?.init })
      await playOriginal()
      return
    }
    try {
      sourceBuffer = mediaSource.addSourceBuffer(mime)
    } catch (reason) {
      console.warn('addSourceBuffer failed:', reason)
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
        source.removeEventListener('sourceopen', onOpen)
        source.removeEventListener('startstreaming', onOpen)
        resolve()
      }
      source.addEventListener('sourceopen', onOpen)
      if (managed) source.addEventListener('startstreaming', onOpen)
    })
  }

  async function pollTick(): Promise<void> {
    if (!video) {
      stopPolling()
      return
    }
    try {
      await refresh()
    } catch (reason) {
      console.warn('rendition refresh failed:', reason)
      return
    }
    // Judge after the refresh: the selected rendition may only appear in the
    // manifest after the queue request was accepted server-side.
    const rendition = findRendition(activeQuality.value)
    if (activeQuality.value === 'original' || !rendition) {
      stopPolling()
      return
    }
    if (rendition.status === 'failed' || rendition.status === 'cancelled') {
      stopPolling()
      await playOriginal()
      return
    }
    const updated = findRendition(activeQuality.value)
    if (!updated) return
    if (updated.init && !sourceBuffer) {
      // The init segment just appeared: attach the MSE pipeline now.
      await playRendition(activeQuality.value)
      return
    }
    if (!sourceBuffer) return
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

  /** Switches playback quality. Saves the preference when user-initiated. */
  async function selectQuality(profile: string, opts: { silent?: boolean; savePreference?: boolean } = {}): Promise<void> {
    if (!video) return
    if (opts.savePreference) {
      const { update } = usePreferences()
      update({ playbackQuality: profile })
    }
    if (profile === 'original') {
      await playOriginal()
      return
    }
    if (profile === activeQuality.value) return
    const rendition = findRendition(profile)
    if (rendition && (rendition.status === 'ready' || (rendition.status === 'running' && rendition.init))) {
      await playRendition(profile)
      return
    }
    if (rendition && (rendition.status === 'queued' || rendition.status === 'running')) {
      // Selected but not decodable yet: keep the original and poll.
      activeQuality.value = profile
      finalized = false
      startPolling()
      if (!opts.silent) notify.info(t('quality.queued'))
      return
    }
    try {
      await api.post('/file/transcode', { path: filePath, profile })
      activeQuality.value = profile
      finalized = false
      startPolling()
      if (!opts.silent) notify.info(t('quality.queued'))
    } catch (reason: any) {
      if (reason?.response?.status === 409) {
        await refresh()
        const existing = findRendition(profile)
        if (existing && existing.status !== 'failed' && existing.status !== 'cancelled') {
          if (existing.status === 'ready' || existing.init) await playRendition(profile)
          else {
            activeQuality.value = profile
            startPolling()
          }
          return
        }
      }
      notify.error(t('quality.failed'))
    }
  }

  /** Loads the rendition manifest for a file and applies the saved default. */
  async function attach(target: HTMLVideoElement, path: string): Promise<void> {
    video = target
    filePath = path
    attachGeneration++
    // The original always starts playing immediately; upgrades below switch
    // the source once the manifest is known.
    activeQuality.value = 'original'
    video.src = options.decryptUrl()
    const { prefs } = usePreferences()
    try {
      await refresh()
    } catch (reason) {
      console.warn('rendition manifest unavailable:', reason)
      return
    }
    const preference = prefs.playbackQuality
    const wanted = preference === 'auto'
      ? RENDITION_PROFILES.find(profile => {
          const rendition = findRendition(profile)
          return rendition?.status === 'ready' || (rendition?.status === 'running' && rendition.init)
        })
      : preference && preference !== 'original' ? preference : undefined
    if (wanted && findRendition(wanted)) {
      await playRendition(wanted)
      return
    }
    if (wanted) {
      // The remembered default has no rendition for this file yet: keep the
      // original playing and tell the user how to get it.
      notify.info(t('quality.preferred_missing', { profile: wanted.toUpperCase() }))
    }
  }

  function detach(): void {
    teardownMedia()
    attachGeneration++
    video = null
    filePath = ''
  }

  return { renditions, sourceHeight, activeQuality, attach, detach, selectQuality, refresh }
}
