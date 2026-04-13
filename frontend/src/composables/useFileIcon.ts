import type { FunctionalComponent, SVGAttributes } from 'vue'
import type { IconType } from '../types'

import { IconFolder, IconFileOutline as IconFile, IconFileDocumentOutline as IconText, IconCodeTags as IconCode, IconFileImageOutline as IconImage, IconFileVideoOutline as IconVideo, IconFileMusicOutline as IconAudio, IconZipBoxOutline as IconArchive, IconFilePdfBox as IconPdf, IconFileTableOutline as IconSpreadsheet, IconFileWordOutline as IconDocument, IconFilePresentationBox as IconPresentation, IconCodeJson as IconConfig, IconApplicationCogOutline as IconExec, IconLanguageHtml5 as IconWeb, IconDatabaseOutline as IconDatabase, IconLanguageMarkdownOutline as IconMarkdown, IconLanguagePython as IconPython, IconLanguageJavascript as IconJs, IconLanguageCss3 as IconCss, IconConsole as IconShell, IconDatabaseSearchOutline as IconSql, IconLanguageCpp as IconCpp } from '../barrels/icons'

const ICONS: Record<IconType, FunctionalComponent<SVGAttributes>> = {
  folder: IconFolder,
  file: IconFile,
  text: IconText,
  code: IconCode,
  image: IconImage,
  video: IconVideo,
  audio: IconAudio,
  archive: IconArchive,
  pdf: IconPdf,
  spreadsheet: IconSpreadsheet,
  document: IconDocument,
  presentation: IconPresentation,
  config: IconConfig,
  exec: IconExec,
  web: IconWeb,
  database: IconDatabase,
  markdown: IconMarkdown,
  python: IconPython,
  javascript: IconJs,
  css: IconCss,
  shell: IconShell,
  sql: IconSql,
  cpp: IconCpp,
}

// Map file extensions to icon types
const EXT_MAP: Record<string, IconType> = {
  // Text
  txt: 'text', log: 'text', csv: 'text',
  // Markdown
  md: 'markdown', markdown: 'markdown', rst: 'markdown',
  // Code — specific languages
  js: 'javascript', ts: 'javascript', jsx: 'javascript', tsx: 'javascript',
  py: 'python',
  c: 'cpp', cpp: 'cpp', h: 'cpp', hpp: 'cpp',
  sh: 'shell', bash: 'shell', zsh: 'shell', fish: 'shell',
  sql: 'sql',
  css: 'css', scss: 'css', less: 'css', sass: 'css',
  // Code — generic
  go: 'code', rs: 'code', rb: 'code',
  java: 'code', kt: 'code',
  cs: 'code', php: 'code', swift: 'code',
  lua: 'code', r: 'code', pl: 'code', scala: 'code',
  vue: 'code', svelte: 'code',
  // Web
  html: 'web', htm: 'web',
  // Config
  json: 'config', yaml: 'config', yml: 'config', toml: 'config',
  xml: 'config', ini: 'config', conf: 'config', env: 'config',
  // Image
  jpg: 'image', jpeg: 'image', png: 'image', gif: 'image',
  svg: 'image', webp: 'image', bmp: 'image', ico: 'image',
  tiff: 'image', raw: 'image', psd: 'image', avif: 'image',
  // Video
  mp4: 'video', mkv: 'video', avi: 'video', mov: 'video',
  webm: 'video', flv: 'video', wmv: 'video',
  // Audio
  mp3: 'audio', wav: 'audio', flac: 'audio', ogg: 'audio',
  aac: 'audio', m4a: 'audio', wma: 'audio',
  // Archive
  zip: 'archive', tar: 'archive', gz: 'archive', bz2: 'archive',
  xz: 'archive', '7z': 'archive', rar: 'archive', zst: 'archive',
  // PDF
  pdf: 'pdf',
  // Spreadsheet
  ods: 'spreadsheet', xls: 'spreadsheet', xlsx: 'spreadsheet', xlsb: 'spreadsheet', xlsm: 'spreadsheet',
  // Document
  doc: 'document', docx: 'document', docm: 'document', dotm: 'document', dotx: 'document', odt: 'document', rtf: 'document',
  // Presentation
  ppt: 'presentation', pptx: 'presentation', ppsx: 'presentation', pps: 'presentation', pptm: 'presentation', potm: 'presentation', ppam: 'presentation', potx: 'presentation', ppsm: 'presentation', odp: 'presentation',
  // Database
  db: 'database', sqlite: 'database',
  // Executable
  exe: 'exec', bin: 'exec', AppImage: 'exec',
  // Go mod
  mod: 'config', sum: 'config',
  // Font
  ttf: 'file', otf: 'file', woff: 'file', woff2: 'file',
  // Notebook
  ipynb: 'code',
}

export function getFileIcon(fileName: string, isDir: boolean): FunctionalComponent<SVGAttributes> {
  if (isDir) return ICONS.folder
  const ext: string = fileName.split('.').pop()?.toLowerCase() || ''
  const iconType: IconType = EXT_MAP[ext] || 'file'
  return ICONS[iconType] || ICONS.file
}

export { ICONS }
