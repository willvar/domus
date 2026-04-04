import { shallowRef } from 'vue'
import type { ShallowRef } from 'vue'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view'
import type { KeyBinding, ViewUpdate } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import type { Extension } from '@codemirror/state'
import { oneDark } from '@codemirror/theme-one-dark'
import type { CodeMirrorCallbacks } from '../types'

type LanguageName = 'javascript' | 'typescript' | 'python' | 'json' | 'markdown' | 'html' | 'xml' | 'css' | 'java' | 'cpp' | 'c' | 'rust' | 'sql' | 'php' | 'go'

// Lazy language loaders — each returns a dynamic import, only fetched when needed
const langLoaders: Record<LanguageName, () => Promise<Extension>> = {
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

const cmTheme: Extension = EditorView.theme({
  '&': { height: '100%', fontSize: '14px' },
  '.cm-scroller': { fontFamily: "'Cascadia Code', 'Fira Code', 'JetBrains Mono', Consolas, monospace" },
})

/**
 * Creates a reusable CodeMirror editor manager.
 * Language extensions are loaded on demand — only the language actually
 * used gets fetched (each ~10-50 KB instead of all 13 upfront).
 * @returns {{ view, create, destroy }}
 */
export function useCodeMirror(): {
  view: ShallowRef<EditorView | null>
  create: (container: HTMLElement, content: string, language: string | null, readOnly: boolean, callbacks?: CodeMirrorCallbacks) => Promise<void>
  destroy: () => void
  replaceContent: (newContent: string) => void
} {
  const view: ShallowRef<EditorView | null> = shallowRef(null)

  function destroy(): void {
    if (view.value) {
      view.value.destroy()
      view.value = null
    }
  }

  function buildEditor(container: HTMLElement, content: string, langExt: Extension | null, readOnly: boolean, callbacks: CodeMirrorCallbacks): void {
    const extensions: Extension[] = [
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
      const keymaps: KeyBinding[] = []
      if (callbacks.onSave) {
        keymaps.push({ key: 'Mod-s', run(): boolean { callbacks.onSave!(); return true } })
      }
      if (keymaps.length) extensions.push(keymap.of(keymaps))
      if (callbacks.onChange) {
        extensions.push(
          EditorView.updateListener.of((update: ViewUpdate) => {
            if (update.docChanged) callbacks.onChange!(update.state.doc.toString())
          }),
        )
      }
    }

    view.value = new EditorView({
      parent: container,
      state: EditorState.create({ doc: content || '', extensions }),
    })
  }

  async function create(container: HTMLElement, content: string, language: string | null, readOnly: boolean, callbacks: CodeMirrorCallbacks = {}): Promise<void> {
    destroy()
    if (!container) return

    let langExt: Extension | null = null
    const loader: (() => Promise<Extension>) | null = language ? langLoaders[language as LanguageName] ?? null : null
    if (loader) {
      try { langExt = await loader() } catch { /* language not available */ }
    }

    buildEditor(container, content, langExt, readOnly, callbacks)
  }

  function replaceContent(newContent: string): void {
    if (!view.value) return
    view.value.dispatch({
      changes: { from: 0, to: view.value.state.doc.length, insert: newContent },
      effects: EditorView.scrollIntoView(0),
    })
  }

  return { view, create, destroy, replaceContent }
}
