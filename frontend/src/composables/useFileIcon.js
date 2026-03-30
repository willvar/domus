import IconFolder from '~icons/mdi/folder'
import IconFile from '~icons/mdi/file-outline'
import IconText from '~icons/mdi/file-document-outline'
import IconCode from '~icons/mdi/code-tags'
import IconImage from '~icons/mdi/file-image-outline'
import IconVideo from '~icons/mdi/file-video-outline'
import IconAudio from '~icons/mdi/file-music-outline'
import IconArchive from '~icons/mdi/zip-box-outline'
import IconPdf from '~icons/mdi/file-pdf-box'
import IconSpreadsheet from '~icons/mdi/file-table-outline'
import IconDocument from '~icons/mdi/file-word-outline'
import IconPresentation from '~icons/mdi/file-presentation-box'
import IconConfig from '~icons/mdi/code-json'
import IconExec from '~icons/mdi/application-cog-outline'
import IconWeb from '~icons/mdi/language-html5'
import IconDatabase from '~icons/mdi/database-outline'
import IconMarkdown from '~icons/mdi/language-markdown-outline'
import IconPython from '~icons/mdi/language-python'
import IconJs from '~icons/mdi/language-javascript'
import IconCss from '~icons/mdi/language-css3'
import IconShell from '~icons/mdi/console'
import IconSql from '~icons/mdi/database-search-outline'
import IconCpp from '~icons/mdi/language-cpp'

const ICONS = {
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
const EXT_MAP = {
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

export function getFileIcon(fileName, isDir) {
  if (isDir) return ICONS.folder
  const ext = fileName.split('.').pop()?.toLowerCase() || ''
  const iconType = EXT_MAP[ext] || 'file'
  return ICONS[iconType] || ICONS.file
}

export function getFileIconType(fileName, isDir) {
  if (isDir) return 'folder'
  const ext = fileName.split('.').pop()?.toLowerCase() || ''
  return EXT_MAP[ext] || 'file'
}

export { ICONS }
