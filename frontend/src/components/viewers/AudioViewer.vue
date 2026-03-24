<script setup>
import { ref, onMounted, onUnmounted } from 'vue'

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

onMounted(() => {
  if (audioEl.value) {
    audioEl.value.addEventListener('play', initAudio, { once: true })
  }
})

onUnmounted(() => {
  if (animFrameId) cancelAnimationFrame(animFrameId)
  if (audioCtx) audioCtx.close().catch(() => {})
})
</script>

<template>
  <div class="viewer-toolbar">
    <span class="toolbar-label">{{ state.file.name }}</span>
  </div>
  <div class="viewer-body audio-body">
    <canvas ref="canvasEl" class="audio-waveform" />
    <audio ref="audioEl" :src="state.url" controls autoplay class="audio-player" />
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
