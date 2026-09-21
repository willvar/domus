import { shallowRef } from 'vue'
import type { ShallowRef } from 'vue'
import { basicSetup } from 'codemirror'
import { Compartment, EditorState } from '@codemirror/state'
import type { Extension } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import type { KeyBinding, ViewUpdate } from '@codemirror/view'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags } from '@lezer/highlight'

interface CodeEditorCallbacks {
  onChange?: (content: string) => void
  onSave?: () => void
}

function legacyMode(
  importer: () => Promise<Record<string, unknown>>,
  parser: string,
): () => Promise<Extension> {
  return async () => {
    const [{ StreamLanguage }, module] = await Promise.all([
      import('@codemirror/language'),
      importer(),
    ])
    return StreamLanguage.define(module[parser] as Parameters<typeof StreamLanguage.define>[0])
  }
}

// Language support is loaded only after the user enters edit mode.
const languageLoaders: Record<string, () => Promise<Extension>> = {
  javascript: () => import('@codemirror/lang-javascript').then(module => module.javascript()),
  typescript: () => import('@codemirror/lang-javascript').then(module => module.javascript({ typescript: true })),
  python: () => import('@codemirror/lang-python').then(module => module.python()),
  json: () => import('@codemirror/lang-json').then(module => module.json()),
  markdown: () => import('@codemirror/lang-markdown').then(module => module.markdown()),
  html: () => import('@codemirror/lang-html').then(module => module.html()),
  xml: () => import('@codemirror/lang-xml').then(module => module.xml()),
  css: () => import('@codemirror/lang-css').then(module => module.css()),
  java: () => import('@codemirror/lang-java').then(module => module.java()),
  cpp: () => import('@codemirror/lang-cpp').then(module => module.cpp()),
  c: () => import('@codemirror/lang-cpp').then(module => module.cpp()),
  rust: () => import('@codemirror/lang-rust').then(module => module.rust()),
  sql: () => import('@codemirror/lang-sql').then(module => module.sql()),
  php: () => import('@codemirror/lang-php').then(module => module.php()),
  go: () => import('@codemirror/lang-go').then(module => module.go()),
  yaml: () => import('@codemirror/lang-yaml').then(module => module.yaml()),
  less: () => import('@codemirror/lang-less').then(module => module.less()),
  vue: () => import('@codemirror/lang-vue').then(module => module.vue()),
  liquid: () => import('@codemirror/lang-liquid').then(module => module.liquid()),
  wast: () => import('@codemirror/lang-wast').then(module => module.wast()),
  bash: legacyMode(() => import('@codemirror/legacy-modes/mode/shell'), 'shell'),
  scss: legacyMode(() => import('@codemirror/legacy-modes/mode/css'), 'sCSS'),
  kotlin: legacyMode(() => import('@codemirror/legacy-modes/mode/clike'), 'kotlin'),
  scala: legacyMode(() => import('@codemirror/legacy-modes/mode/clike'), 'scala'),
  swift: legacyMode(() => import('@codemirror/legacy-modes/mode/swift'), 'swift'),
  ini: legacyMode(() => import('@codemirror/legacy-modes/mode/toml'), 'toml'),
  protobuf: legacyMode(() => import('@codemirror/legacy-modes/mode/protobuf'), 'protobuf'),
  dockerfile: legacyMode(() => import('@codemirror/legacy-modes/mode/dockerfile'), 'dockerFile'),
  lua: legacyMode(() => import('@codemirror/legacy-modes/mode/lua'), 'lua'),
  r: legacyMode(() => import('@codemirror/legacy-modes/mode/r'), 'r'),
  ruby: legacyMode(() => import('@codemirror/legacy-modes/mode/ruby'), 'ruby'),
  clojure: legacyMode(() => import('@codemirror/legacy-modes/mode/clojure'), 'clojure'),
  cmake: legacyMode(() => import('@codemirror/legacy-modes/mode/cmake'), 'cmake'),
  coffeescript: legacyMode(() => import('@codemirror/legacy-modes/mode/coffeescript'), 'coffeeScript'),
  commonlisp: legacyMode(() => import('@codemirror/legacy-modes/mode/commonlisp'), 'commonLisp'),
  crystal: legacyMode(() => import('@codemirror/legacy-modes/mode/crystal'), 'crystal'),
  d: legacyMode(() => import('@codemirror/legacy-modes/mode/d'), 'd'),
  diff: legacyMode(() => import('@codemirror/legacy-modes/mode/diff'), 'diff'),
  elm: legacyMode(() => import('@codemirror/legacy-modes/mode/elm'), 'elm'),
  erlang: legacyMode(() => import('@codemirror/legacy-modes/mode/erlang'), 'erlang'),
  fortran: legacyMode(() => import('@codemirror/legacy-modes/mode/fortran'), 'fortran'),
  groovy: legacyMode(() => import('@codemirror/legacy-modes/mode/groovy'), 'groovy'),
  haskell: legacyMode(() => import('@codemirror/legacy-modes/mode/haskell'), 'haskell'),
  julia: legacyMode(() => import('@codemirror/legacy-modes/mode/julia'), 'julia'),
  mathematica: legacyMode(() => import('@codemirror/legacy-modes/mode/mathematica'), 'mathematica'),
  fsharp: legacyMode(() => import('@codemirror/legacy-modes/mode/mllike'), 'fSharp'),
  ocaml: legacyMode(() => import('@codemirror/legacy-modes/mode/mllike'), 'oCaml'),
  nginx: legacyMode(() => import('@codemirror/legacy-modes/mode/nginx'), 'nginx'),
  ntriples: legacyMode(() => import('@codemirror/legacy-modes/mode/ntriples'), 'ntriples'),
  octave: legacyMode(() => import('@codemirror/legacy-modes/mode/octave'), 'octave'),
  pascal: legacyMode(() => import('@codemirror/legacy-modes/mode/pascal'), 'pascal'),
  perl: legacyMode(() => import('@codemirror/legacy-modes/mode/perl'), 'perl'),
  powershell: legacyMode(() => import('@codemirror/legacy-modes/mode/powershell'), 'powerShell'),
  properties: legacyMode(() => import('@codemirror/legacy-modes/mode/properties'), 'properties'),
  pug: legacyMode(() => import('@codemirror/legacy-modes/mode/pug'), 'pug'),
  puppet: legacyMode(() => import('@codemirror/legacy-modes/mode/puppet'), 'puppet'),
  sass: legacyMode(() => import('@codemirror/legacy-modes/mode/sass'), 'sass'),
  scheme: legacyMode(() => import('@codemirror/legacy-modes/mode/scheme'), 'scheme'),
  sparql: legacyMode(() => import('@codemirror/legacy-modes/mode/sparql'), 'sparql'),
  stex: legacyMode(() => import('@codemirror/legacy-modes/mode/stex'), 'stex'),
  stylus: legacyMode(() => import('@codemirror/legacy-modes/mode/stylus'), 'stylus'),
  tcl: legacyMode(() => import('@codemirror/legacy-modes/mode/tcl'), 'tcl'),
  textile: legacyMode(() => import('@codemirror/legacy-modes/mode/textile'), 'textile'),
  toml: legacyMode(() => import('@codemirror/legacy-modes/mode/toml'), 'toml'),
  turtle: legacyMode(() => import('@codemirror/legacy-modes/mode/turtle'), 'turtle'),
  vb: legacyMode(() => import('@codemirror/legacy-modes/mode/vb'), 'vb'),
  verilog: legacyMode(() => import('@codemirror/legacy-modes/mode/verilog'), 'verilog'),
  vhdl: legacyMode(() => import('@codemirror/legacy-modes/mode/vhdl'), 'vhdl'),
  xquery: legacyMode(() => import('@codemirror/legacy-modes/mode/xquery'), 'xQuery'),
}

const editorTheme = EditorView.theme({
  '&': {
    height: '100%',
    color: '#202a3d',
    backgroundColor: '#fff',
    fontSize: '13px',
  },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': {
    fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", monospace',
    lineHeight: '1.65',
  },
  '.cm-content': { padding: '14px 0', caretColor: '#4f5fe7' },
  '.cm-gutters': {
    color: '#8a94a8',
    backgroundColor: '#f7f8fb',
    borderRight: '1px solid #e3e7ef',
  },
  '.cm-activeLine': { backgroundColor: '#f3f5ff' },
  '.cm-activeLineGutter': { color: '#4f5fe7', backgroundColor: '#ecefff' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': {
    backgroundColor: '#cfd5ff !important',
  },
})

const editorDarkTheme = EditorView.theme({
  '&': {
    height: '100%',
    color: '#e7ebf3',
    backgroundColor: '#1a1f2b',
    fontSize: '13px',
  },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': {
    fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", monospace',
    lineHeight: '1.65',
  },
  '.cm-content': { padding: '14px 0', caretColor: '#8b9bff' },
  '.cm-gutters': {
    color: '#7e8899',
    backgroundColor: '#161a24',
    borderRight: '1px solid #2b3243',
  },
  '.cm-activeLine': { backgroundColor: '#222836' },
  '.cm-activeLineGutter': { color: '#a9b6ff', backgroundColor: '#28304f' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': {
    backgroundColor: '#3a4680 !important',
  },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#8b9bff' },
}, { dark: true })

const darkHighlightStyle = HighlightStyle.define([
  { tag: tags.comment, color: '#7e8899', fontStyle: 'italic' },
  { tag: [tags.keyword, tags.operatorKeyword, tags.modifier], color: '#c792ea' },
  { tag: [tags.string, tags.special(tags.string), tags.regexp], color: '#a5d6a7' },
  { tag: [tags.number, tags.bool, tags.null, tags.atom], color: '#f5b880' },
  { tag: [tags.function(tags.variableName), tags.labelName], color: '#8bb9ff' },
  { tag: [tags.typeName, tags.className, tags.namespace], color: '#7fd6c2' },
  { tag: [tags.propertyName, tags.attributeName], color: '#e2c08d' },
  { tag: [tags.variableName, tags.definition(tags.variableName)], color: '#dfe6f2' },
  { tag: [tags.tagName, tags.angleBracket], color: '#f08d7f' },
  { tag: [tags.meta, tags.processingInstruction], color: '#9aa4b8' },
  { tag: tags.heading, color: '#8bb9ff', fontWeight: '700' },
  { tag: [tags.emphasis], fontStyle: 'italic' },
  { tag: [tags.strong], fontWeight: '700' },
  { tag: [tags.link, tags.url], color: '#7fd6c2', textDecoration: 'underline' },
  { tag: tags.invalid, color: '#e06a78' },
])

const themeCompartment = new Compartment()
let editorDark = false

function themeExtensions(): Extension {
  if (!editorDark) return editorTheme
  return [editorDarkTheme, syntaxHighlighting(darkHighlightStyle)]
}

export function useCodeEditor(): {
  view: ShallowRef<EditorView | null>
  create: (
    container: HTMLElement,
    content: string,
    language: string | null,
    callbacks?: CodeEditorCallbacks,
  ) => Promise<void>
  setDark: (value: boolean) => void
  destroy: () => void
} {
  const view = shallowRef<EditorView | null>(null)
  let revision = 0

  function destroyView(): void {
    view.value?.destroy()
    view.value = null
  }

  function destroy(): void {
    revision += 1
    destroyView()
  }

  function setDark(value: boolean): void {
    editorDark = value
    view.value?.dispatch({
      effects: themeCompartment.reconfigure(themeExtensions()),
    })
  }

  async function create(
    container: HTMLElement,
    content: string,
    language: string | null,
    callbacks: CodeEditorCallbacks = {},
  ): Promise<void> {
    const requestedRevision = ++revision
    destroyView()

    let languageExtension: Extension | null = null
    const loader = language ? languageLoaders[language] : undefined
    if (loader) {
      try {
        languageExtension = await loader()
      } catch (error) {
        console.warn(`Code editor language support could not be loaded for ${language}`, error)
      }
    }
    if (requestedRevision !== revision || !container.isConnected) return

    const bindings: KeyBinding[] = callbacks.onSave
      ? [{ key: 'Mod-s', run: () => { callbacks.onSave?.(); return true } }]
      : []
    const extensions: Extension[] = [
      basicSetup,
      themeCompartment.of(themeExtensions()),
      EditorView.lineWrapping,
      EditorState.tabSize.of(2),
      keymap.of(bindings),
    ]
    if (languageExtension) extensions.push(languageExtension)
    if (callbacks.onChange) {
      extensions.push(EditorView.updateListener.of((update: ViewUpdate) => {
        if (update.docChanged) callbacks.onChange?.(update.state.doc.toString())
      }))
    }

    view.value = new EditorView({
      parent: container,
      state: EditorState.create({ doc: content, extensions }),
    })
    view.value.focus()
  }

  return { view, create, setDark, destroy }
}
