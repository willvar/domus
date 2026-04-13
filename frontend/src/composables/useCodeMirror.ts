import { shallowRef } from 'vue'
import type { ShallowRef } from 'vue'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view'
import type { KeyBinding, ViewUpdate } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import type { Extension } from '@codemirror/state'
import { oneDark } from '@codemirror/theme-one-dark'
import type { CodeMirrorCallbacks } from '../types'

// Helper: wrap a legacy StreamLanguage parser with lazy import
function sl(imp: () => Promise<Record<string, unknown>>, parser: string): () => Promise<Extension> {
  return async () => {
    const [{ StreamLanguage }, m] = await Promise.all([import('@codemirror/language'), imp()])
    return (StreamLanguage as { define: (p: unknown) => Extension }).define(m[parser])
  }
}

// Lazy language loaders — each returns a dynamic import, only fetched when needed
const langLoaders: Record<string, () => Promise<Extension>> = {
  // First-party @codemirror/lang-* packages
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
  yaml: () => import('@codemirror/lang-yaml').then(m => m.yaml()),
  less: () => import('@codemirror/lang-less').then(m => m.less()),
  vue: () => import('@codemirror/lang-vue').then(m => m.vue()),
  liquid: () => import('@codemirror/lang-liquid').then(m => m.liquid()),
  wast: () => import('@codemirror/lang-wast').then(m => m.wast()),
  // Legacy modes — all static import paths for Vite/Rolldown
  bash: sl(() => import('@codemirror/legacy-modes/mode/shell'), 'shell'),
  scss: sl(() => import('@codemirror/legacy-modes/mode/css'), 'sCSS'),
  kotlin: sl(() => import('@codemirror/legacy-modes/mode/clike'), 'kotlin'),
  scala: sl(() => import('@codemirror/legacy-modes/mode/clike'), 'scala'),
  swift: sl(() => import('@codemirror/legacy-modes/mode/swift'), 'swift'),
  ini: sl(() => import('@codemirror/legacy-modes/mode/toml'), 'toml'),
  protobuf: sl(() => import('@codemirror/legacy-modes/mode/protobuf'), 'protobuf'),
  dockerfile: sl(() => import('@codemirror/legacy-modes/mode/dockerfile'), 'dockerFile'),
  lua: sl(() => import('@codemirror/legacy-modes/mode/lua'), 'lua'),
  r: sl(() => import('@codemirror/legacy-modes/mode/r'), 'r'),
  ruby: sl(() => import('@codemirror/legacy-modes/mode/ruby'), 'ruby'),
  apl: sl(() => import('@codemirror/legacy-modes/mode/apl'), 'apl'),
  asciiarmor: sl(() => import('@codemirror/legacy-modes/mode/asciiarmor'), 'asciiArmor'),
  asterisk: sl(() => import('@codemirror/legacy-modes/mode/asterisk'), 'asterisk'),
  brainfuck: sl(() => import('@codemirror/legacy-modes/mode/brainfuck'), 'brainfuck'),
  clojure: sl(() => import('@codemirror/legacy-modes/mode/clojure'), 'clojure'),
  cmake: sl(() => import('@codemirror/legacy-modes/mode/cmake'), 'cmake'),
  cobol: sl(() => import('@codemirror/legacy-modes/mode/cobol'), 'cobol'),
  coffeescript: sl(() => import('@codemirror/legacy-modes/mode/coffeescript'), 'coffeeScript'),
  commonlisp: sl(() => import('@codemirror/legacy-modes/mode/commonlisp'), 'commonLisp'),
  crystal: sl(() => import('@codemirror/legacy-modes/mode/crystal'), 'crystal'),
  cypher: sl(() => import('@codemirror/legacy-modes/mode/cypher'), 'cypher'),
  d: sl(() => import('@codemirror/legacy-modes/mode/d'), 'd'),
  diff: sl(() => import('@codemirror/legacy-modes/mode/diff'), 'diff'),
  dtd: sl(() => import('@codemirror/legacy-modes/mode/dtd'), 'dtd'),
  dylan: sl(() => import('@codemirror/legacy-modes/mode/dylan'), 'dylan'),
  ebnf: sl(() => import('@codemirror/legacy-modes/mode/ebnf'), 'ebnf'),
  ecl: sl(() => import('@codemirror/legacy-modes/mode/ecl'), 'ecl'),
  eiffel: sl(() => import('@codemirror/legacy-modes/mode/eiffel'), 'eiffel'),
  elm: sl(() => import('@codemirror/legacy-modes/mode/elm'), 'elm'),
  erlang: sl(() => import('@codemirror/legacy-modes/mode/erlang'), 'erlang'),
  factor: sl(() => import('@codemirror/legacy-modes/mode/factor'), 'factor'),
  fcl: sl(() => import('@codemirror/legacy-modes/mode/fcl'), 'fcl'),
  forth: sl(() => import('@codemirror/legacy-modes/mode/forth'), 'forth'),
  fortran: sl(() => import('@codemirror/legacy-modes/mode/fortran'), 'fortran'),
  gas: sl(() => import('@codemirror/legacy-modes/mode/gas'), 'gas'),
  gherkin: sl(() => import('@codemirror/legacy-modes/mode/gherkin'), 'gherkin'),
  groovy: sl(() => import('@codemirror/legacy-modes/mode/groovy'), 'groovy'),
  haskell: sl(() => import('@codemirror/legacy-modes/mode/haskell'), 'haskell'),
  haxe: sl(() => import('@codemirror/legacy-modes/mode/haxe'), 'haxe'),
  http: sl(() => import('@codemirror/legacy-modes/mode/http'), 'http'),
  idl: sl(() => import('@codemirror/legacy-modes/mode/idl'), 'idl'),
  jinja2: sl(() => import('@codemirror/legacy-modes/mode/jinja2'), 'jinja2'),
  julia: sl(() => import('@codemirror/legacy-modes/mode/julia'), 'julia'),
  livescript: sl(() => import('@codemirror/legacy-modes/mode/livescript'), 'liveScript'),
  mathematica: sl(() => import('@codemirror/legacy-modes/mode/mathematica'), 'mathematica'),
  mirc: sl(() => import('@codemirror/legacy-modes/mode/mirc'), 'mirc'),
  fsharp: sl(() => import('@codemirror/legacy-modes/mode/mllike'), 'fSharp'),
  ocaml: sl(() => import('@codemirror/legacy-modes/mode/mllike'), 'oCaml'),
  sml: sl(() => import('@codemirror/legacy-modes/mode/mllike'), 'sml'),
  modelica: sl(() => import('@codemirror/legacy-modes/mode/modelica'), 'modelica'),
  nginx: sl(() => import('@codemirror/legacy-modes/mode/nginx'), 'nginx'),
  nsis: sl(() => import('@codemirror/legacy-modes/mode/nsis'), 'nsis'),
  ntriples: sl(() => import('@codemirror/legacy-modes/mode/ntriples'), 'ntriples'),
  octave: sl(() => import('@codemirror/legacy-modes/mode/octave'), 'octave'),
  oz: sl(() => import('@codemirror/legacy-modes/mode/oz'), 'oz'),
  pascal: sl(() => import('@codemirror/legacy-modes/mode/pascal'), 'pascal'),
  perl: sl(() => import('@codemirror/legacy-modes/mode/perl'), 'perl'),
  pig: sl(() => import('@codemirror/legacy-modes/mode/pig'), 'pig'),
  powershell: sl(() => import('@codemirror/legacy-modes/mode/powershell'), 'powerShell'),
  properties: sl(() => import('@codemirror/legacy-modes/mode/properties'), 'properties'),
  pug: sl(() => import('@codemirror/legacy-modes/mode/pug'), 'pug'),
  puppet: sl(() => import('@codemirror/legacy-modes/mode/puppet'), 'puppet'),
  q: sl(() => import('@codemirror/legacy-modes/mode/q'), 'q'),
  sas: sl(() => import('@codemirror/legacy-modes/mode/sas'), 'sas'),
  sass: sl(() => import('@codemirror/legacy-modes/mode/sass'), 'sass'),
  scheme: sl(() => import('@codemirror/legacy-modes/mode/scheme'), 'scheme'),
  sieve: sl(() => import('@codemirror/legacy-modes/mode/sieve'), 'sieve'),
  smalltalk: sl(() => import('@codemirror/legacy-modes/mode/smalltalk'), 'smalltalk'),
  solr: sl(() => import('@codemirror/legacy-modes/mode/solr'), 'solr'),
  sparql: sl(() => import('@codemirror/legacy-modes/mode/sparql'), 'sparql'),
  stex: sl(() => import('@codemirror/legacy-modes/mode/stex'), 'stex'),
  stylus: sl(() => import('@codemirror/legacy-modes/mode/stylus'), 'stylus'),
  tcl: sl(() => import('@codemirror/legacy-modes/mode/tcl'), 'tcl'),
  textile: sl(() => import('@codemirror/legacy-modes/mode/textile'), 'textile'),
  tiddlywiki: sl(() => import('@codemirror/legacy-modes/mode/tiddlywiki'), 'tiddlyWiki'),
  toml: sl(() => import('@codemirror/legacy-modes/mode/toml'), 'toml'),
  troff: sl(() => import('@codemirror/legacy-modes/mode/troff'), 'troff'),
  ttcn: sl(() => import('@codemirror/legacy-modes/mode/ttcn'), 'ttcn'),
  turtle: sl(() => import('@codemirror/legacy-modes/mode/turtle'), 'turtle'),
  vb: sl(() => import('@codemirror/legacy-modes/mode/vb'), 'vb'),
  vbscript: sl(() => import('@codemirror/legacy-modes/mode/vbscript'), 'vbScript'),
  velocity: sl(() => import('@codemirror/legacy-modes/mode/velocity'), 'velocity'),
  verilog: sl(() => import('@codemirror/legacy-modes/mode/verilog'), 'verilog'),
  vhdl: sl(() => import('@codemirror/legacy-modes/mode/vhdl'), 'vhdl'),
  webidl: sl(() => import('@codemirror/legacy-modes/mode/webidl'), 'webIDL'),
  xquery: sl(() => import('@codemirror/legacy-modes/mode/xquery'), 'xQuery'),
  yacas: sl(() => import('@codemirror/legacy-modes/mode/yacas'), 'yacas'),
  z80: sl(() => import('@codemirror/legacy-modes/mode/z80'), 'z80'),
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
    const loader: (() => Promise<Extension>) | null = language ? langLoaders[language] ?? null : null
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
