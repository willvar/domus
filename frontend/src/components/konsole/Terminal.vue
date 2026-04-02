<script setup>
import { ref, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { useWebSocket } from '../../composables/useWebSocket'
import { useAuthStore } from '../../stores/auth'
import '@xterm/xterm/css/xterm.css'

const props = defineProps({
  initialCwd: { type: String, default: '' },
})

const emit = defineEmits(['exit', 'cwd-change'])

const ws = useWebSocket()
const auth = useAuthStore()

const termRef = ref(null)
let terminal = null
let fitAddon = null
let sessionId = null
let inputBuffer = ''
let cursorPos = 0
let history = []
let historyIndex = -1
let tempInput = ''
let cwd = '/'
let isExecuting = false
let sessionEnded = false
let sshMode = false
let completing = false
let resizeObserver = null

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

function promptStr() {
  const user = auth.username || 'user'
  return `\x1b[1;32m${user}\x1b[0m@\x1b[1;34mzephyr\x1b[0m:\x1b[1;36m${cwd}\x1b[0m$ `
}

function writePrompt() {
  terminal.write(promptStr())
}

// --- Push event handlers ---

function handleSessionOutput({ session_id, data }) {
  if (session_id !== sessionId || !terminal) return
  // If loading spinner is active, clear it before writing output
  if (sshLoadingTimer) {
    clearInterval(sshLoadingTimer)
    sshLoadingTimer = null
    terminal.write('\r\x1b[K')
  }
  terminal.write(data)
}

function handleSessionDone({ session_id, cwd: newCwd }) {
  if (session_id !== sessionId) return
  isExecuting = false
  if (newCwd && newCwd !== cwd) {
    cwd = newCwd
    emit('cwd-change', cwd)
  }
  writePrompt()
}

function handleSessionExit({ session_id }) {
  if (session_id !== sessionId) return
  sessionEnded = true
  terminal.writeln('\r\n\x1b[2mSession ended.\x1b[0m')
  sessionId = null
  emit('exit')
}

let sshLoadingTimer = null

function handleSessionSSH({ session_id, status }) {
  if (session_id !== sessionId) return

  if (status === 'connecting') {
    // Show animated loading
    let dots = 0
    const frames = ['Connecting', 'Connecting.', 'Connecting..', 'Connecting...']
    terminal.write('\x1b[2m' + frames[0] + '\x1b[0m')
    sshLoadingTimer = setInterval(() => {
      dots = (dots + 1) % frames.length
      terminal.write('\r\x1b[K\x1b[2m' + frames[dots] + '\x1b[0m')
    }, 400)
  } else if (status === 'connected') {
    if (sshLoadingTimer) { clearInterval(sshLoadingTimer); sshLoadingTimer = null }
    terminal.write('\r\x1b[K')
    sshMode = true
  } else if (status === 'disconnected') {
    if (sshLoadingTimer) { clearInterval(sshLoadingTimer); sshLoadingTimer = null }
    terminal.write('\r\x1b[K')
    if (sshMode) {
      sshMode = false
      terminal.writeln('\r\n\x1b[33m[Connection closed]\x1b[0m')
    }
    isExecuting = false
    writePrompt()
  }
}


// --- Session init ---

async function initSession() {
  sessionEnded = false
  sshMode = false
  try {
    const res = await ws.request('session.open', { cwd: props.initialCwd })
    sessionId = res.session_id
    cwd = res.cwd || '/'
    if (res.history && Array.isArray(res.history)) {
      history = res.history.slice()
    }
    terminal.writeln(`\x1b[2m Zephyr Virtual Shell — type \x1b[0m\x1b[36mhelp\x1b[2m for commands\x1b[0m`)
    terminal.writeln('')
    writePrompt()
  } catch (e) {
    terminal.writeln(`\x1b[31mFailed to open shell session: ${e.error || e}\x1b[0m`)
  }
}

// --- Command execution (fire-and-forget) ---

function executeCommand(cmd) {
  if (!sessionId || sessionEnded) return
  isExecuting = true

  ws.request('session.input', { session_id: sessionId, data: cmd }).then(() => {
    // Request accepted — unlock input immediately.
    // Output arrives via session.output, prompt via session.done.
    isExecuting = false
  }).catch((e) => {
    terminal.writeln(`\x1b[31mError: ${e.error || e}\x1b[0m\r\n`)
    isExecuting = false
    writePrompt()
  })
}

// --- Tab completion (still request-response) ---

async function handleTab() {
  if (!sessionId || completing || sshMode) return
  completing = true
  try {
    const res = await ws.request('session.complete', {
      session_id: sessionId,
      line: inputBuffer,
    })
    const matches = res.matches || []
    const prefix = res.prefix || ''

    if (matches.length === 0) {
      // No matches
    } else if (matches.length === 1) {
      const suffix = matches[0].slice(prefix.length)
      const addSpace = !matches[0].endsWith('/') ? ' ' : ''
      const insert = suffix + addSpace
      const tail = inputBuffer.slice(cursorPos)
      inputBuffer = inputBuffer.slice(0, cursorPos) + insert + tail
      cursorPos += insert.length
      terminal.write(insert + tail)
      if (tail.length > 0) terminal.write(`\x1b[${tail.length}D`)
    } else {
      let common = matches[0]
      for (let i = 1; i < matches.length; i++) {
        while (!matches[i].startsWith(common)) {
          common = common.slice(0, -1)
        }
      }
      const extraCommon = common.slice(prefix.length)
      if (extraCommon.length > 0) {
        const tail = inputBuffer.slice(cursorPos)
        inputBuffer = inputBuffer.slice(0, cursorPos) + extraCommon + tail
        cursorPos += extraCommon.length
        terminal.write(extraCommon + tail)
        if (tail.length > 0) terminal.write(`\x1b[${tail.length}D`)
      } else {
        terminal.write('\r\n')
        const display = matches.map(m => {
          const name = m.split('/').filter(Boolean).pop() || m
          return m.endsWith('/') ? `\x1b[1;34m${name}/\x1b[0m` : name
        })
        terminal.writeln(display.join('  '))
        writePrompt()
        terminal.write(inputBuffer)
        const tailLen = inputBuffer.length - cursorPos
        if (tailLen > 0) terminal.write(`\x1b[${tailLen}D`)
      }
    }
  } catch {
    // Completion failed silently
  } finally {
    completing = false
  }
}

// --- Input handling ---

function handleData(data) {
  if (sessionEnded) return

  // SSH mode: forward all input directly, no local editing
  if (sshMode) {
    if (sessionId) {
      ws.request('session.input', { session_id: sessionId, data }).catch(() => {})
    }
    return
  }

  // vsh mode: local line editing
  if (isExecuting || completing) return

  for (let i = 0; i < data.length; i++) {
    const ch = data.charCodeAt(i)

    // Ctrl+D (EOF - exit if input is empty)
    if (ch === 4) {
      if (inputBuffer.length === 0) {
        terminal.write('\r\n')
        executeCommand('exit')
      }
      return
    }

    // Enter
    if (ch === 13) {
      terminal.write('\r\n')
      const cmd = inputBuffer.trim()
      if (cmd) {
        history.push(cmd)
        if (history.length > 200) history.shift()
      }
      const buf = inputBuffer
      inputBuffer = ''
      cursorPos = 0
      historyIndex = -1
      if (cmd) {
        executeCommand(buf)
      } else {
        writePrompt()
      }
      return
    }

    // Backspace
    if (ch === 127) {
      if (cursorPos > 0) {
        inputBuffer = inputBuffer.slice(0, cursorPos - 1) + inputBuffer.slice(cursorPos)
        cursorPos--
        terminal.write('\x1b[D')
        const tail = inputBuffer.slice(cursorPos)
        terminal.write(tail + ' ')
        terminal.write(`\x1b[${tail.length + 1}D`)
      }
      return
    }

    // Ctrl+C
    if (ch === 3) {
      terminal.write('^C\r\n')
      inputBuffer = ''
      cursorPos = 0
      historyIndex = -1
      // Notify backend to cancel any pending auth prompt
      if (sessionId) {
        ws.request('session.input', { session_id: sessionId, data: '\x03' }).catch(() => {})
      }
      writePrompt()
      return
    }

    // Ctrl+L (clear)
    if (ch === 12) {
      terminal.write('\x1b[2J\x1b[H')
      writePrompt()
      terminal.write(inputBuffer)
      const tail = inputBuffer.length - cursorPos
      if (tail > 0) terminal.write(`\x1b[${tail}D`)
      return
    }

    // Ctrl+A (home)
    if (ch === 1) {
      if (cursorPos > 0) {
        terminal.write(`\x1b[${cursorPos}D`)
        cursorPos = 0
      }
      return
    }

    // Ctrl+E (end)
    if (ch === 5) {
      const move = inputBuffer.length - cursorPos
      if (move > 0) {
        terminal.write(`\x1b[${move}C`)
        cursorPos = inputBuffer.length
      }
      return
    }

    // Ctrl+U (clear line)
    if (ch === 21) {
      if (cursorPos > 0) {
        terminal.write(`\x1b[${cursorPos}D`)
      }
      terminal.write('\x1b[K')
      inputBuffer = ''
      cursorPos = 0
      return
    }

    // Escape sequences (arrows, etc.)
    if (ch === 27 && i + 1 < data.length) {
      if (data[i + 1] === '[') {
        const code = data[i + 2]
        // Up arrow
        if (code === 'A') {
          if (history.length > 0) {
            if (historyIndex === -1) {
              tempInput = inputBuffer
              historyIndex = history.length - 1
            } else if (historyIndex > 0) {
              historyIndex--
            }
            if (cursorPos > 0) terminal.write(`\x1b[${cursorPos}D`)
            terminal.write('\x1b[K')
            inputBuffer = history[historyIndex]
            cursorPos = inputBuffer.length
            terminal.write(inputBuffer)
          }
          i += 2
          return
        }
        // Down arrow
        if (code === 'B') {
          if (historyIndex !== -1) {
            if (cursorPos > 0) terminal.write(`\x1b[${cursorPos}D`)
            terminal.write('\x1b[K')
            if (historyIndex < history.length - 1) {
              historyIndex++
              inputBuffer = history[historyIndex]
            } else {
              historyIndex = -1
              inputBuffer = tempInput
            }
            cursorPos = inputBuffer.length
            terminal.write(inputBuffer)
          }
          i += 2
          return
        }
        // Right arrow
        if (code === 'C') {
          if (cursorPos < inputBuffer.length) {
            terminal.write('\x1b[C')
            cursorPos++
          }
          i += 2
          return
        }
        // Left arrow
        if (code === 'D') {
          if (cursorPos > 0) {
            terminal.write('\x1b[D')
            cursorPos--
          }
          i += 2
          return
        }
        i += 2
        return
      }
      i += 1
      return
    }

    // Tab - completion
    if (ch === 9) {
      handleTab()
      return
    }

    // Normal printable character
    if (ch >= 32) {
      const char = data[i]
      if (cursorPos === inputBuffer.length) {
        inputBuffer += char
        cursorPos++
        terminal.write(char)
      } else {
        inputBuffer = inputBuffer.slice(0, cursorPos) + char + inputBuffer.slice(cursorPos)
        cursorPos++
        const tail = inputBuffer.slice(cursorPos)
        terminal.write(char + tail)
        if (tail.length > 0) terminal.write(`\x1b[${tail.length}D`)
      }
    }
  }
}

// --- Reconnect ---

function handleReconnect() {
  if (terminal) {
    sshMode = false
    terminal.writeln('\r\n\x1b[33m[Reconnected - opening new session...]\x1b[0m')
    inputBuffer = ''
    cursorPos = 0
    initSession()
  }
}

// --- Lifecycle ---

const isMobile = window.innerWidth < 768

function handleViewportResize() {
  // When soft keyboard opens/closes, adjust terminal container height
  if (window.visualViewport && termRef.value) {
    const vvHeight = window.visualViewport.height
    const parent = termRef.value.parentElement
    if (parent) {
      // Reset any override first to let CSS handle it
      parent.style.maxHeight = ''
      // If viewport is significantly smaller than window (keyboard open), constrain height
      if (vvHeight < window.innerHeight * 0.8) {
        parent.style.maxHeight = vvHeight + 'px'
      }
    }
    if (fitAddon) {
      try { fitAddon.fit() } catch { /* ignore */ }
    }
  }
}

onMounted(async () => {
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

  terminal.open(termRef.value)

  await nextTick()
  fitAddon.fit()

  terminal.onData(handleData)

  resizeObserver = new ResizeObserver(() => {
    if (fitAddon && terminal) {
      try {
        fitAddon.fit()
        // Notify backend of new dimensions (needed for SSH PTY resize)
        if (sessionId) {
          ws.request('session.resize', {
            session_id: sessionId,
            cols: terminal.cols,
            rows: terminal.rows,
          }).catch(() => {})
        }
      } catch { /* ignore */ }
    }
  })
  resizeObserver.observe(termRef.value)

  // Register push event listeners
  ws.on('session.output', handleSessionOutput)
  ws.on('session.done', handleSessionDone)
  ws.on('session.exit', handleSessionExit)
  ws.on('session.ssh', handleSessionSSH)

  // Handle mobile soft keyboard resize
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', handleViewportResize)
  }

  ws.onReconnect(handleReconnect)

  await initSession()

  // Send initial terminal dimensions to backend
  if (sessionId) {
    ws.request('session.resize', {
      session_id: sessionId,
      cols: terminal.cols,
      rows: terminal.rows,
    }).catch(() => {})
  }
})

onBeforeUnmount(() => {
  // Unregister push event listeners
  ws.off('session.output', handleSessionOutput)
  ws.off('session.done', handleSessionDone)
  ws.off('session.exit', handleSessionExit)
  ws.off('session.ssh', handleSessionSSH)

  ws.offReconnect(handleReconnect)
  if (window.visualViewport) {
    window.visualViewport.removeEventListener('resize', handleViewportResize)
  }
  if (sshLoadingTimer) { clearInterval(sshLoadingTimer); sshLoadingTimer = null }
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  if (sessionId) {
    ws.request('session.close', { session_id: sessionId }).catch(() => {})
    sessionId = null
  }
  if (terminal) {
    terminal.dispose()
    terminal = null
  }
})

function refit() {
  if (fitAddon && terminal) {
    try { fitAddon.fit() } catch { /* ignore */ }
  }
}

defineExpose({ refit })

watch(() => props.initialCwd, () => {
  // Don't auto-cd, just note the change
})
</script>

<template>
  <div class="konsole-terminal" ref="termRef"></div>
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
