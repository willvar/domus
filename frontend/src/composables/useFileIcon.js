// Papirus icon theme (GPLv3) — imported as raw SVG strings
import iconFolder from '../assets/icons/folder.svg?raw'
import iconFile from '../assets/icons/text-x-generic.svg?raw'
import iconText from '../assets/icons/text-x-generic.svg?raw'
import iconCode from '../assets/icons/text-x-script.svg?raw'
import iconImage from '../assets/icons/image-x-generic.svg?raw'
import iconVideo from '../assets/icons/video-x-generic.svg?raw'
import iconAudio from '../assets/icons/audio-x-generic.svg?raw'
import iconArchive from '../assets/icons/application-zip.svg?raw'
import iconPdf from '../assets/icons/application-pdf.svg?raw'
import iconSpreadsheet from '../assets/icons/x-office-spreadsheet.svg?raw'
import iconDocument from '../assets/icons/x-office-document.svg?raw'
import iconPresentation from '../assets/icons/x-office-presentation.svg?raw'
import iconConfig from '../assets/icons/application-json.svg?raw'
import iconExec from '../assets/icons/application-x-executable.svg?raw'
import iconWeb from '../assets/icons/text-html.svg?raw'
import iconDatabase from '../assets/icons/application-database.svg?raw'
import iconMarkdown from '../assets/icons/text-markdown.svg?raw'
import iconPython from '../assets/icons/text-x-python.svg?raw'
import iconJs from '../assets/icons/application-javascript.svg?raw'
import iconCss from '../assets/icons/text-css.svg?raw'
import iconShell from '../assets/icons/application-x-shellscript.svg?raw'
import iconSql from '../assets/icons/application-sql.svg?raw'
import iconCpp from '../assets/icons/text-x-csrc.svg?raw'

const ICONS = {
  folder: iconFolder,
  file: iconFile,
  text: iconText,
  code: iconCode,
  image: iconImage,
  video: iconVideo,
  audio: iconAudio,
  archive: iconArchive,
  pdf: iconPdf,
  spreadsheet: iconSpreadsheet,
  document: iconDocument,
  presentation: iconPresentation,
  config: iconConfig,
  exec: iconExec,
  web: iconWeb,
  database: iconDatabase,
  markdown: iconMarkdown,
  python: iconPython,
  javascript: iconJs,
  css: iconCss,
  shell: iconShell,
  sql: iconSql,
  cpp: iconCpp,
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
  xls: 'spreadsheet', xlsx: 'spreadsheet', ods: 'spreadsheet',
  // Document
  doc: 'document', docx: 'document', odt: 'document', rtf: 'document',
  // Presentation
  ppt: 'presentation', pptx: 'presentation', odp: 'presentation',
  // Database
  db: 'database', sqlite: 'database',
  // Executable
  exe: 'exec', bin: 'exec', AppImage: 'exec',
  // Go mod
  mod: 'config', sum: 'config',
  // Font
  ttf: 'file', otf: 'file', woff: 'file', woff2: 'file',
  // Epub
  epub: 'document',
  // Notebook
  ipynb: 'code',
}

function sizeIcon(svgStr, size) {
  // Replace width/height in the SVG root tag
  return svgStr
    .replace(/width="[^"]*"/, `width="${size}"`)
    .replace(/height="[^"]*"/, `height="${size}"`)
}

export function getFileIconSvg(fileName, isDir, size = 48) {
  if (isDir) return sizeIcon(ICONS.folder, size)

  const ext = fileName.split('.').pop()?.toLowerCase() || ''
  const iconType = EXT_MAP[ext] || 'file'
  const icon = ICONS[iconType] || ICONS.file
  return sizeIcon(icon, size)
}

export function getFileIconType(fileName, isDir) {
  if (isDir) return 'folder'
  const ext = fileName.split('.').pop()?.toLowerCase() || ''
  return EXT_MAP[ext] || 'file'
}

// Export raw icons for use in stores/components
export { ICONS }
