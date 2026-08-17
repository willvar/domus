<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue'
import type { Terminal as TerminalType } from '@xterm/xterm'
import type { FitAddon as FitAddonType } from '@xterm/addon-fit'
import { useWebSocket } from '../../composables/useWebSocket'

const props = defineProps({
  initialCwd: { type: String, default: '' },
})

const emit = defineEmits(['exit'])
const ws = useWebSocket()
const termRef = ref<HTMLDivElement | null>(null)

let terminal: TerminalType | null = null
let fitAddon: FitAddonType | null = null
let sessionId: string | null = null
let sessionEnded = false
let resizeObserver: ResizeObserver | null = null
let resizeFrame: number | null = null
let resizeInFlight = false
let resizeQueued = false
let lastSentCols = 0
let lastSentRows = 0

const THEME = {
  background: '#1b1e20',
  foreground: '#e0e0e0',
  cursor: '#3daee9',
  cursorAccent: '#1b1e20',
  selectionBackground: '#3daee940',
  black: '#1b1e20',
  red: '#ed1515',
  green: '#11d116',
  yellow: '#f5c211',
  blue: '#3daee9',
  magenta: '#c678dd',
  cyan: '#1abc9c',
  white: '#e0e0e0',
  brightBlack: '#7f8c8d',
  brightRed: '#c0392b',
  brightGreen: '#2ecc71',
  brightYellow: '#f39c12',
  brightBlue: '#3daee9',
  brightMagenta: '#8e44ad',
  brightCyan: '#16a085',
  brightWhite: '#ffffff',
}

function handleSessionOutput({ session_id, data_base64 }: { session_id: string; data_base64: string }) {
  if (session_id !== sessionId || !terminal || !data_base64) return
  try {
    const decoded = atob(data_base64)
    const bytes = Uint8Array.from(decoded, character => character.charCodeAt(0))
    terminal.write(bytes)
  } catch {
    terminal.writeln('\r\n\x1b[31mInvalid terminal byte stream\x1b[0m')
  }
}

function handleSessionExit({ session_id, reason }: { session_id: string; reason?: string }) {
  if (session_id !== sessionId) return
  sessionEnded = true
  sessionId = null
  terminal?.writeln(`\r\n\x1b[2mSession ended${reason && reason !== 'exit' ? ` (${reason})` : ''}.\x1b[0m`)
  emit('exit')
}

async function resizeSession(): Promise<void> {
  if (!sessionId || !terminal) return
  const cols = terminal.cols
  const rows = terminal.rows
  if (cols === lastSentCols && rows === lastSentRows) return

  if (resizeInFlight) {
    resizeQueued = true
    return
  }

  resizeInFlight = true
  lastSentCols = cols
  lastSentRows = rows
  try {
    await ws.request('session.resize', { session_id: sessionId, cols, rows })
  } catch {
    // A reconnect opens a new session and resets the last-sent dimensions.
  } finally {
    resizeInFlight = false
    if (resizeQueued) {
      resizeQueued = false
      scheduleRefit()
    }
  }
}

function scheduleRefit(): void {
  if (resizeFrame != null) return
  resizeFrame = window.requestAnimationFrame(() => {
    resizeFrame = null
    if (fitAddon && terminal) {
      try { fitAddon.fit() } catch { /* component may be unmounting */ }
    }
    void resizeSession()
  })
}

async function initSession() {
  sessionEnded = false
  try {
    const response = await ws.request('session.open', { cwd: props.initialCwd })
    sessionId = response.session_id
    lastSentCols = 0
    lastSentRows = 0
    await resizeSession()
  } catch (error: any) {
    sessionEnded = true
    terminal?.writeln(`\x1b[31mFailed to open shell session: ${error.error || error}\x1b[0m`)
  }
}

function handleData(data: string) {
  if (!sessionId || sessionEnded) return
  ws.request('session.input', { session_id: sessionId, data }).catch((error: any) => {
    terminal?.writeln(`\r\n\x1b[31mTerminal input failed: ${error.error || error}\x1b[0m`)
  })
}

function handleReconnect() {
  if (!terminal) return
  sessionId = null
  sessionEnded = false
  lastSentCols = 0
  lastSentRows = 0
  terminal.writeln('\r\n\x1b[33m[Reconnected - opening new session...]\x1b[0m')
  void initSession()
}

const isMobile = window.innerWidth < 768

function handleViewportResize() {
  if (window.visualViewport && termRef.value) {
    const viewportHeight = window.visualViewport.height
    const parent = termRef.value.parentElement
    if (parent) {
      parent.style.maxHeight = ''
      if (viewportHeight < window.innerHeight * 0.8) {
        parent.style.maxHeight = `${viewportHeight}px`
      }
    }
  }
  scheduleRefit()
}

onMounted(async () => {
  const { Terminal, FitAddon, WebLinksAddon } = await import('../../barrels/xterm')
  terminal = new Terminal({
    theme: THEME,
    fontSize: isMobile ? 12 : 14,
    fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, Monaco, 'Courier New', monospace",
    cursorBlink: true,
    cursorStyle: 'block',
    allowProposedApi: true,
    scrollback: 5000,
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.loadAddon(new WebLinksAddon())
  terminal.open(termRef.value!)
  await nextTick()
  fitAddon.fit()
  terminal.onData(handleData)

  resizeObserver = new ResizeObserver(() => {
    scheduleRefit()
  })
  resizeObserver.observe(termRef.value!)

  ws.on('session.output', handleSessionOutput)
  ws.on('session.exit', handleSessionExit)
  ws.onReconnect(handleReconnect)
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', handleViewportResize)
  }
  await initSession()
})

onBeforeUnmount(() => {
  ws.off('session.output', handleSessionOutput)
  ws.off('session.exit', handleSessionExit)
  ws.offReconnect(handleReconnect)
  if (window.visualViewport) {
    window.visualViewport.removeEventListener('resize', handleViewportResize)
  }
  resizeObserver?.disconnect()
  resizeObserver = null
  if (resizeFrame != null) {
    window.cancelAnimationFrame(resizeFrame)
    resizeFrame = null
  }
  if (sessionId) {
    ws.request('session.close', { session_id: sessionId }).catch(() => {})
    sessionId = null
  }
  terminal?.dispose()
  terminal = null
})

function refit() {
  scheduleRefit()
}

defineExpose({ refit })
</script>

<template>
  <div ref="termRef" class="konsole-terminal"></div>
</template>

<style lang="scss" scoped>
.konsole-terminal {
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: #1b1e20;

  :deep(.xterm) {
    height: 100%;
    padding: 4px 0 0 4px;
  }

  :deep(.xterm-viewport) {
    background-color: #1b1e20 !important;
    overflow-y: auto !important;
  }
}
</style>
