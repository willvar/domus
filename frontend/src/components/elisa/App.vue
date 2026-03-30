<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useWorkspaceSync, registerViewerCallback, unregisterViewerCallback } from '../../composables/useWorkspaceSync'
import { usePreferences } from '../../composables/usePreferences'

const props = defineProps({
  state: { type: Object, required: true },
  windowId: { type: String, required: true },
})

const audioEl = ref(null)
const canvasEl = ref(null)
let audioCtx = null
let analyser = null
let animFrameId = null
let sourceConnected = false

const sync = useWorkspaceSync()
const { prefs } = usePreferences()
let _isRemotePlayback = false
let _timeSyncTimer = null

function draw() {
  if (!analyser || !canvasEl.value) return
  const canvas = canvasEl.value
  const ctx = canvas.getContext('2d')
  const bufferLength = analyser.frequencyBinCount
  const data = new Uint8Array(bufferLength)

  function render() {
    animFrameId = requestAnimationFrame(render)
    analyser.getByteFrequencyData(data)

    const w = canvas.width = canvas.clientWidth * (window.devicePixelRatio || 1)
    const h = canvas.height = canvas.clientHeight * (window.devicePixelRatio || 1)
    ctx.clearRect(0, 0, w, h)

    const barWidth = Math.max(1, (w / bufferLength) * 2.5)
    let x = 0
    for (let i = 0; i < bufferLength; i++) {
      const barHeight = (data[i] / 255) * h
      const r = 61 + (data[i] / 255) * 40
      const g = 174 - (data[i] / 255) * 40
      const b = 233
      ctx.fillStyle = `rgb(${r},${g},${b})`
      ctx.fillRect(x, h - barHeight, barWidth, barHeight)
      x += barWidth + 1
      if (x > w) break
    }
  }
  render()
}

function initAudio() {
  if (sourceConnected || !audioEl.value) return
  try {
    audioCtx = new (window.AudioContext || window.webkitAudioContext)()
    analyser = audioCtx.createAnalyser()
    analyser.fftSize = 256
    const source = audioCtx.createMediaElementSource(audioEl.value)
    source.connect(analyser)
    analyser.connect(audioCtx.destination)
    sourceConnected = true
    draw()
  } catch { /* AudioContext not available */ }
}

// ─── Playback sync ───

function onAudioPlay() {
  if (_isRemotePlayback || prefs.sessionIsolation) return
  sync.emitEvent({ action: 'viewer.play', windowId: props.windowId, currentTime: audioEl.value?.currentTime || 0 })
}

function onAudioPause() {
  if (_isRemotePlayback || prefs.sessionIsolation) return
  sync.emitEvent({ action: 'viewer.pause', windowId: props.windowId, currentTime: audioEl.value?.currentTime || 0 })
}

function onAudioSeeked() {
  if (_isRemotePlayback || prefs.sessionIsolation) return
  sync.emitEvent({ action: 'viewer.seek', windowId: props.windowId, currentTime: audioEl.value?.currentTime || 0 })
}

registerViewerCallback(props.windowId, (action, currentTime) => {
  const el = audioEl.value
  if (!el) return
  _isRemotePlayback = true
  try {
    switch (action) {
      case 'play':
        el.currentTime = currentTime
        el.play().catch(() => {})
        break
      case 'pause':
        el.pause()
        el.currentTime = currentTime
        break
      case 'seek':
        el.currentTime = currentTime
        break
      case 'timeSync':
        if (Math.abs(el.currentTime - currentTime) > 2) {
          el.currentTime = currentTime
        }
        break
    }
  } finally {
    setTimeout(() => { _isRemotePlayback = false }, 100)
  }
})

// TimeSync interval
watch(() => props.state?.url, (url) => {
  if (url) {
    _timeSyncTimer = setInterval(() => {
      const el = audioEl.value
      if (!el || el.paused || _isRemotePlayback || prefs.sessionIsolation) return
      sync.emitEvent({ action: 'viewer.timeSync', windowId: props.windowId, currentTime: el.currentTime })
    }, 3000)
  } else if (_timeSyncTimer) {
    clearInterval(_timeSyncTimer)
    _timeSyncTimer = null
  }
}, { immediate: true })

onMounted(() => {
  if (audioEl.value) {
    audioEl.value.addEventListener('play', initAudio, { once: true })
  }
})

onUnmounted(() => {
  if (animFrameId) cancelAnimationFrame(animFrameId)
  if (audioCtx) audioCtx.close().catch(() => {})
  unregisterViewerCallback(props.windowId)
  if (_timeSyncTimer) clearInterval(_timeSyncTimer)
})
</script>

<template>
  <div class="viewer-toolbar">
    <span class="toolbar-label">{{ state.file.name }}</span>
  </div>
  <div class="viewer-body audio-body">
    <canvas ref="canvasEl" class="audio-waveform" />
    <audio ref="audioEl" :src="state.url" controls autoplay class="audio-player" @play="onAudioPlay" @pause="onAudioPause" @seeked="onAudioSeeked" />
  </div>
</template>

<style scoped>
.viewer-toolbar {
  display: flex; align-items: center; height: 32px; padding: 0 12px;
  background: var(--breeze-bg-alt); border-bottom: 1px solid var(--breeze-border); flex-shrink: 0;
}
.toolbar-label { color: #bbb; font-size: 12px; }
.viewer-body.audio-body {
  flex: 1; display: flex; flex-direction: column; align-items: stretch;
  justify-content: flex-end; min-height: 0; background: var(--breeze-bg); padding: 0;
}
.audio-waveform {
  flex: 1; width: 100%; min-height: 0;
}
.audio-player {
  width: 100%; flex-shrink: 0;
  filter: invert(0.85) hue-rotate(180deg);
}
</style>
