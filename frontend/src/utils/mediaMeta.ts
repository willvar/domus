import dayjs from 'dayjs'
import { useI18n } from '../composables/useI18n'

/**
 * Parsed shape of the JSON the worker stores in `media_meta`
 * (see MediaMeta in internal/worker/media_meta.go).
 */
export interface MediaMetaParsed {
  duration?: number
  width?: number
  height?: number
  fps?: number
  bit_rate?: number
  rotation?: number
  audio_channels?: number
  audio_sample_rate?: number
  creation_time?: string
  make?: string
  model?: string
  title?: string
  artist?: string
  album?: string
  genre?: string
  date?: string
  gps?: string
}

export function parseMediaMeta(json?: string | null): MediaMetaParsed | null {
  if (!json) return null
  try {
    const parsed = JSON.parse(json) as MediaMetaParsed
    return parsed && typeof parsed === 'object' && Object.keys(parsed).length ? parsed : null
  } catch {
    return null
  }
}

export function parseIso6709(value?: string): { latitude: number; longitude: number } | null {
  if (!value) return null
  const match = value.trim().match(/^([+-]?\d+(?:\.\d+)?)([+-]\d+(?:\.\d+)?)/)
  if (!match) return null
  const latitude = Number(match[1])
  const longitude = Number(match[2])
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) return null
  return { latitude, longitude }
}

export function formatMediaClock(seconds?: number): string | undefined {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) return undefined
  const total = Math.round(seconds)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const secondsPart = String(total % 60).padStart(2, '0')
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, '0')}:${secondsPart}` : `${minutes}:${secondsPart}`
}

export function formatMediaBitRate(bitRate?: number): string | undefined {
  if (typeof bitRate !== 'number' || !Number.isFinite(bitRate) || bitRate <= 0) return undefined
  if (bitRate >= 1_000_000) return `${(bitRate / 1_000_000).toFixed(1)} Mbps`
  return `${Math.round(bitRate / 1000)} kbps`
}

function formatSampleRate(rate?: number): string | undefined {
  if (typeof rate !== 'number' || rate <= 0) return undefined
  if (rate >= 1000 && rate % 1000 === 0) return `${rate / 1000}kHz`
  return `${(rate / 1000).toFixed(1)}kHz`
}

/** One labelled metadata row rendered in the details UIs. */
export interface MetaItem { label: string; value: string; href?: string }

/** Builds labelled metadata rows from a parsed media meta object. */
export function mediaMetaItems(meta: MediaMetaParsed): MetaItem[] {
  const { t } = useI18n()
  const items: MetaItem[] = []
  const push = (label: string, value?: string | number | null, href?: string) => {
    if (value === undefined || value === null || value === '') return
    items.push({ label, value: String(value), href })
  }

  push(t('info.meta_doc_title'), meta.title)
  push(t('info.meta_artist'), meta.artist)
  push(t('info.meta_album'), meta.album)
  push(t('info.meta_genre'), meta.genre)
  push(t('info.meta_camera'), [meta.make, meta.model].filter(Boolean).join(' '))
  push(t('info.meta_taken'), formatMetaTimestamp(meta.creation_time ?? meta.date))
  push(t('info.meta_duration'), formatMediaClock(meta.duration))
  if (meta.width && meta.height) {
    push(t('info.meta_dimensions'), `${meta.width} × ${meta.height}`)
  }
  push(t('info.meta_fps'), typeof meta.fps === 'number' && meta.fps > 0 ? meta.fps : undefined)
  push(t('info.meta_bitrate'), formatMediaBitRate(meta.bit_rate))
  push(t('info.meta_rotation'), typeof meta.rotation === 'number' && meta.rotation !== 0 ? `${Math.abs(Math.round(meta.rotation))}°` : undefined)
  push(t('info.meta_audio'), [
    meta.audio_channels ? `${meta.audio_channels}ch` : '',
    formatSampleRate(meta.audio_sample_rate),
  ].filter(Boolean).join(' · '))
  const gps = parseIso6709(meta.gps)
  if (gps) {
    push(
      t('info.meta_gps'),
      `${gps.latitude.toFixed(6)}, ${gps.longitude.toFixed(6)}`,
      `https://www.openstreetmap.org/?mlat=${gps.latitude}&mlon=${gps.longitude}#map=17/${gps.latitude}/${gps.longitude}`,
    )
  }
  return items
}

function formatMetaTimestamp(value?: string): string | undefined {
  if (!value) return undefined
  const parsed = dayjs(value)
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm') : value
}
