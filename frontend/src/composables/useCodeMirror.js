import { shallowRef } from 'vue'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import { oneDark } from '@codemirror/theme-one-dark'

// Lazy language loaders — each returns a dynamic import, only fetched when needed
const langLoaders = {
  javascript: () => import('@codemirror/lang-javascript').then(m => m.javascript()),
  typescript: () => import('@codemirror/lang-javascript').then(m => m.javascript({ typescript: true })),
  python: () => import('@codemirror/lang-python').then(m => m.python()),
  json: () => import('@codemirror/lang-json').then(m => m.json()),
  markdown: () => import('@codemirror/lang-markdown').then(m => m.markdown()),
  html: () => import('@codemirror/lang-html').then(m => m.html()),
  xml: () => import('@codemirror/lang-xml').then(m => m.xml()),
  css: () => import('@codemirror/lang-css').then(m => m.css()),
  java: () => import('@codemirror/lang-java').then(m => m.java()),
  cpp: () => import('@codemirror/lang-cpp').then(m => m.cpp()),
  c: () => import('@codemirror/lang-cpp').then(m => m.cpp()),
  rust: () => import('@codemirror/lang-rust').then(m => m.rust()),
  sql: () => import('@codemirror/lang-sql').then(m => m.sql()),
  php: () => import('@codemirror/lang-php').then(m => m.php()),
  go: () => import('@codemirror/lang-go').then(m => m.go()),
}

const cmTheme = EditorView.theme({
  '&': { height: '100%', fontSize: '14px' },
  '.cm-scroller': { fontFamily: "'Cascadia Code', 'Fira Code', 'JetBrains Mono', Consolas, monospace" },
})

/**
 * Creates a reusable CodeMirror editor manager.
 * Language extensions are loaded on demand — only the language actually
 * used gets fetched (each ~10-50 KB instead of all 13 upfront).
 * @returns {{ view, create, destroy }}
 */
export function useCodeMirror() {
  const view = shallowRef(null)

  function destroy() {
    if (view.value) {
      view.value.destroy()
      view.value = null
    }
  }

  function buildEditor(container, content, langExt, readOnly, callbacks) {
    const extensions = [
      lineNumbers(),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      oneDark,
      cmTheme,
      EditorView.lineWrapping,
    ]
    if (langExt) extensions.push(langExt)

    if (readOnly) {
      extensions.push(EditorState.readOnly.of(true))
      extensions.push(EditorView.editable.of(false))
    } else {
      const keymaps = []
      if (callbacks.onSave) {
        keymaps.push({ key: 'Mod-s', run() { callbacks.onSave(); return true } })
      }
      if (keymaps.length) extensions.push(keymap.of(keymaps))
      if (callbacks.onChange) {
        extensions.push(
          EditorView.updateListener.of((update) => {
            if (update.docChanged) callbacks.onChange(update.state.doc.toString())
          }),
        )
      }
    }

    view.value = new EditorView({
      parent: container,
      state: EditorState.create({ doc: content || '', extensions }),
    })
  }

  /**
   * @param {HTMLElement} container
   * @param {string} content
   * @param {string|null} language
   * @param {boolean} readOnly
   * @param {{ onSave?: () => void, onChange?: (content: string) => void }} [callbacks]
   */
  async function create(container, content, language, readOnly, callbacks = {}) {
    destroy()
    if (!container) return

    let langExt = null
    const loader = language ? langLoaders[language] : null
    if (loader) {
      try { langExt = await loader() } catch { /* language not available */ }
    }

    buildEditor(container, content, langExt, readOnly, callbacks)
  }

  function replaceContent(newContent) {
    if (!view.value) return
    view.value.dispatch({
      changes: { from: 0, to: view.value.state.doc.length, insert: newContent },
      effects: EditorView.scrollIntoView(0),
    })
  }

  return { view, create, destroy, replaceContent }
}
