import type { Component } from 'vue'

// =============================================================================
// Domus Shared TypeScript Type Definitions
// =============================================================================

// -----------------------------------------------------------------------------
// 1. API / Data Types (source of truth: Go models)
// -----------------------------------------------------------------------------

/** User account. Mirrors model.User (json-exported fields only). */
export interface User {
  id: string
  username: string
  display_name: string
  role: 'root' | 'admin' | 'user'
  email: string
  totp_enabled: boolean
  created_at: string // ISO 8601
  /** Public control endpoint for resolving encrypted avatar access metadata. */
  avatar_endpoint?: string
  /** Browser-local blob URL populated after direct OSS fetch and decryption. */
  avatar_url?: string
}

/** Auth verify step request. */
export interface VerifyRequest {
  username: string
  password?: string
  [key: string]: unknown
}

/** Auth verify step response. */
export interface VerifyResponse {
  token: string
  methods: string[]
}

/** Auth login step request. */
export interface LoginRequest {
  token: string
  [key: string]: unknown
}

/** Auth login step response. */
export interface LoginResponse {
  user?: User
  needs_setup?: boolean
}

/**
 * FileInfo returned by file-related HTTP responses such as `GET /file/`.
 * Mirrors `store.FileInfo` in `internal/store/oss.go`.
 */
export interface FileInfo {
  inode?: number
  name: string
  path: string
  is_dir: boolean
  size: number
  created_at: string
  last_modified: string
  content_type?: string
  thumbnail_url?: string
  thumbnail_dek?: string
  media_codecs?: string
  media_meta?: string
  media_width?: number
  media_height?: number
  media_duration?: number
  status?: 'ready' | 'uploading' | 'processing' | 'deleted'
  // When a file is still backed by a task (for example an active upload),
  // the server includes task metadata so the list can render live progress.
  task_id?: string
  task_progress?: number
  task_phase?: string
}

/**
 * Extended file info used in the frontend file list.
 * Adds virtual-view metadata for shared and search items.
 */
export interface FileListItem extends FileInfo {
  /** Match score reserved for result ordering. */
  rank?: number
  // Shared virtual-view fields
  _shareId?: string
  _shareDbId?: number
  _sharedBy?: string
  _permission?: 'read' | 'write'
  _expiresAt?: string | null
  _originalName?: string
  trash_id?: string
  relative_path?: string
  original_path?: string
  deleted_at?: string
  /** Original OSS thumbnail URL, kept so the decrypt mapping can be re-registered after a Service Worker restart. */
  _thumbSource?: string
}

/** Share record. Mirrors model.Share. */
export interface Share {
  id: number
  share_id: string
  owner_id: string
  file_path: string
  file_name: string
  file_size: number
  content_type: string
  target_user_id: string
  target_username?: string
  permission: 'read' | 'write'
  expires_at?: string | null
  created_at: string
}

/** Share record enriched with owner username. Mirrors model.ShareFileView. */
export interface ShareFileView extends Share {
  owner_username: string
}

/** User-facing task. Mirrors model.Task (json-exported fields). */
export interface Task {
  task_id: string
  type: 'upload' | string
  status: TaskStatus
  progress: number
  phase: string
  name: string
  client_instance_id?: string
  created_at?: string
  updated_at?: string
}

/** Valid task statuses. */
export type TaskStatus = 'running' | 'pending' | 'completed' | 'failed' | 'cancelled'

/** Audit log entry. Mirrors model.AuditLog. */
export interface AuditLog {
  id: number
  user_id: string
  username: string
  ip: string
  action: string
  resource: string
  detail: string
  status: string
  duration: number
  created_at: string
}

/** Paginated audit log response from GET /admin/audit. */
export interface AuditLogResponse {
  total: number
  page: number
  size: number
  items: AuditLog[]
}

/** DB-backed session record. Mirrors model.DBSession (admin session listing). */
export interface DBSession {
  id: string
  user_id: string
  username: string
  role: string
  permissions: number
  created_at: string
  expires_at: string
}

// -----------------------------------------------------------------------------
// 2. Store Internal Types
// -----------------------------------------------------------------------------

// --- File System Store ---

/** A single tab in the Dolphin file manager. */
export interface FileTab {
  id: string
  path: string
  files: FileListItem[]
  selectedFiles: string[]
  lastSelectedIndex: number
  history: string[]
  historyIndex: number
  viewMode: 'icons' | 'list' | 'compact'
  sortBy: 'name' | 'size' | 'date'
  sortOrder: 'asc' | 'desc'
  searchQuery: string
  loading: boolean
  error: string | null
}

/** Serialized tab state for workspace persistence. */
export interface SerializedTab {
  path: string
  viewMode?: string
  sortBy?: string
  sortOrder?: string
}

/** Clipboard state for copy/cut-paste operations. */
export interface ClipboardState {
  items: ClipboardItem[]
  mode: 'copy' | 'cut' | null
}

/** A single item in the clipboard. */
export interface ClipboardItem {
  path: string
  is_dir: boolean
  name: string
}

/** Breadcrumb path segment. */
export interface PathSegment {
  name: string
  path: string
}

/** Directory listing cache entry. */
export interface CacheEntry {
  data: FileListItem[]
  timestamp: number
}

/** Result from chunked text loading (Range-based). */
export interface TextChunkResult {
  text: string
  byteLength: number
  nextByteStart: number
}

/** Byte offset entry for text page navigation. */
export interface PageMapEntry {
  byteStart: number
}

/** Filename search result. */
export interface SearchResult extends FileInfo {
  rank?: number
  /** Parent directory path (used in search result display). */
  parent?: string
}

// --- App Window (viewer) state ---

/** Supported viewer types for file preview windows. */
export type ViewerType =
  | 'image'
  | 'video'
  | 'audio'
  | 'text'
  | 'markdown'
  | 'csv'
  | 'pdf'
  | 'font'
  | 'archive'
  | 'notebook'

/** App window state for file viewers (image, video, text, etc.). */
export interface AppWindowState {
  windowId: string
  file: FileListItem
  url: string
  blob: Blob | null
  type: ViewerType
  content: string | null
  language: string | null
  editing: boolean
  dirty: boolean
  saving: boolean
  openedAt: number
  // Chunked text-loading fields
  chunked?: boolean
  page?: number
  pageByteStart?: number
  pageByteEnd?: number
  pageMap?: PageMapEntry[]
  totalPages?: number
  isFullyLoaded?: boolean
  totalSize?: number
  baseSize?: number
  _decryptUrl?: string
  _dek?: string
  _generation?: number
  _closing?: boolean
}

/** Viewer window reference for workspace restore. */
export interface ViewerWindowInfo {
  data?: {
    filePath?: string
  }
}

// --- Window Manager Store ---

/** Saved window geometry for position persistence. */
export interface WindowGeo {
  x: number
  y: number
  width: number
  height: number
  maximized?: boolean
}

/** Window state managed by the window-manager store. */
export interface WindowState {
  id: string
  title: string
  icon: string | Component
  type: 'files' | 'viewer' | 'generic' | string
  minimized: boolean
  maximized: boolean
  tiled: 'left' | 'right' | null
  x: number
  y: number
  width: number
  height: number
  zIndex: number
  data: Record<string, unknown>
  _restoreRect: WindowGeo | null
  _closing?: boolean
}

/** Options for opening a new window. */
export interface OpenWindowOptions {
  id: string
  title?: string
  icon?: string | Component
  type?: string
  data?: Record<string, unknown>
  maximized?: boolean
  width?: number
  height?: number
}

/** Flag indicating the action was triggered from a remote workspace sync. */
export interface RemoteFlag {
  remote?: boolean
}

// --- Upload Store ---

/** Upload status values. */
export type UploadStatus = 'uploading' | 'paused' | 'processing' | 'completed' | 'failed' | 'cancelled' | 'interrupted'

/** Frontend-owned upload session tracked in the upload store. */
export interface UploadSession {
  id: string
  fileName: string
  fileSize: number
  progress: number
  speed: number
  status: UploadStatus
  phase?: string
  uploadId: string | null
  taskId: string | null
  targetPath: string
  partSize?: number
  totalParts?: number
  encryptedSize?: number
  dekHex?: string | null
  startTime: number
  startProgress: number
  bytesUploaded: number
  error?: string
  _lastTaskSyncAt?: number
  _lastTaskProgress?: number
  _lastTaskPhase?: string
  _resume: (() => void) | null
  _file: File | null
}

/** Unified task item shown in the Activity panel. */
export interface TaskItem {
  id: string
  source: 'upload_session' | 'server_task'
  task_id?: string
  type: string
  status: TaskStatus | UploadStatus
  progress: number
  phase?: string
  name: string
  created_at?: string
  updated_at?: string
  speed?: number
  fileSize?: number
  bytesUploaded?: number
  error?: string
}

/** Conflict info returned during upload pre-check. */
export interface ConflictInfo {
  name: string
  size?: number
  is_dir?: boolean
}

// --- Pending Operations Store ---

/** Pending offline operation stored in IndexedDB. */
export interface PendingOp {
  id: string
  schemaVersion?: number
  createdAt: number
  lastAttempt: number | null
  lastError: string | null
  username?: string
  // HTTP retry fields
  apiUrl: string
  apiMethod: 'post' | 'put' | 'delete' | string
  apiData?: unknown
  type?: string
  description?: string
  _retrying?: boolean
  _completed?: boolean
  [key: string]: unknown
}

/** Input used when queueing a new pending HTTP operation. */
export type PendingOpInput = Omit<PendingOp, 'id' | 'createdAt' | 'lastAttempt' | 'lastError'>

// --- User Preferences ---

/** User preferences stored as an encrypted file at /.user/preferences.json. */
export interface UserPreferences {
  largeFileLimitMB: number
  alwaysCenter: boolean
  defaultWidth: number
  defaultHeight: number
  sessionIsolation: boolean
  wallpaperType: 'builtin' | 'custom' | string
  wallpaperBuiltinId: number
  wallpaperPath: string
  wallpaperFit: 'cover' | 'contain' | 'fill' | string
  wallpaperFiles: string[]
  playbackQuality: string
}

// -----------------------------------------------------------------------------
// 3. WebSocket Types
// -----------------------------------------------------------------------------

/** Outgoing WebSocket request message. */
export interface WSMessage {
  id: string
  action: string
  data: Record<string, unknown>
}

/** Incoming WebSocket response to a request. */
export interface WSResponse {
  id: string
  action?: string
  ok?: boolean
  data?: unknown
  error?: string
}

/** Incoming WebSocket push event (no `id`, has `event`). */
export interface WSPushEvent<T = unknown> {
  event: string
  data: T
}

/** Internal pending request entry in the WS composable. */
export interface PendingRequest {
  resolve: (value: unknown) => void
  reject: (reason: unknown) => void
  timeout: ReturnType<typeof setTimeout>
}

/**
 * Known push event types and their data shapes.
 */
export interface WSPushEventMap {
  'dir.changed': { path: string; change_type: string }
  'trash.changed': Record<string, never>
  'task.update': TaskUpdateEvent
  'session.expired': void
  'session.output': { session_id: string; data_base64: string }
  'session.exit': { session_id: string; reason?: string }
  'workspace.event': WorkspaceSyncEvent
}

/** Payload of a task.update push event. */
export interface TaskUpdateEvent {
  task_id: string
  type: string
  name: string
  status: string
  progress: number
  phase: string
}

/** Workspace sync event pushed via workspace.event. */
export interface WorkspaceSyncEvent {
  action: string
  [key: string]: unknown
}

/**
 * Known WS request actions that remain after moving query/command flows to HTTP.
 * Boundary: WS handles realtime subscriptions, terminal sessions, workspace
 * event relay, and client-side upload task reporting.
 */
export type WSAction =
  // Task operations
  | 'task.report'
  // Directory subscriptions
  | 'subscribe.directory'
  | 'unsubscribe.directory'
  // Terminal session
  | 'session.open'
  | 'session.input'
  | 'session.resize'
  | 'session.close'
  // Workspace sync
  | 'workspace.event'

// -----------------------------------------------------------------------------
// 4. Domain Dialog Types
// -----------------------------------------------------------------------------

/** Duplicate file conflict data passed to the Naive UI dialog renderer. */
export interface DuplicateDialogOptions {
  title: string
  incomingName: string
  incomingSize: number
  existingName: string
  existingSize: number
  existingIsDir: boolean
  allowMerge?: boolean
}

/** Result returned by the duplicate conflict dialog. */
export interface DuplicateDecision {
  action: 'skip' | 'replace' | 'rename' | 'merge'
  applyToAll?: boolean
}

// -----------------------------------------------------------------------------
// 5. File Access Response
// -----------------------------------------------------------------------------

/**
 * Response from GET /file/access (and GET /file/shared/:id).
 * Contains everything the Service Worker needs for client-side decryption.
 */
export interface FileAccessResponse {
  inode?: number
  url: string
  size: number
  name: string
  content_type: string
  chunk_size: number
  dek: string // hex-encoded Data Encryption Key
  generation: number
  content_hash?: string
  media_codecs?: string
  media_meta?: string
}

// -----------------------------------------------------------------------------
// 6. Service Worker Decrypt Metadata
// -----------------------------------------------------------------------------

/** Metadata passed to SW for registering a decrypt mapping. */
export interface DecryptMetadata {
  url: string
  size: number
  chunkSize: number
  contentType: string
  filename: string
  dek: string
  download?: boolean
  contentHash?: string
}

// -----------------------------------------------------------------------------
// 6b. Playback Renditions (server-side transcode)
// -----------------------------------------------------------------------------

/** One decryptable derived artifact (init or media segment). */
export interface RenditionArtifact {
  url: string
  dek: string
  size: number
  duration?: number
}

/** GET /file/renditions envelope with the source geometry for menu filtering. */
export interface RenditionsResponse {
  source_width?: number
  source_height?: number
  renditions: RenditionSummary[]
}

/** Quality rendition of a video file, from GET /file/renditions. */
export interface RenditionSummary {
  profile: string
  status: 'queued' | 'running' | 'cancelling' | 'ready' | 'failed' | 'cancelled'
  task_id: string
  codecs?: string
  width?: number
  height?: number
  progress?: number
  error?: string
  duration?: number
  init?: RenditionArtifact
  segments?: RenditionArtifact[]
}

// -----------------------------------------------------------------------------
// 7. Dialog Extras
// -----------------------------------------------------------------------------

/** Options for the confirm dialog (icon, button text). */
export interface ConfirmOptions {
  icon?: string
  positiveText?: string
  positiveType?: string
}

/** Result from the duplicate conflict dialog. Alias of DuplicateDecision. */
export type DuplicateDialogResult = DuplicateDecision

// -----------------------------------------------------------------------------
// 8. i18n Types
// -----------------------------------------------------------------------------

/** Supported locale codes. */
export type Locale = 'zh' | 'en'

/** A flat key-value map of translated messages. */
export type MessageMap = Record<string, string>

/** Top-level messages object keyed by locale. */
export type Messages = Record<Locale, MessageMap>

// -----------------------------------------------------------------------------
// 9. Workspace Sync Types
// -----------------------------------------------------------------------------

/** A serialized window for workspace persistence/sync. */
export interface SerializedWindow {
  id: string
  type: string
  title: string
  minimized: boolean
  maximized: boolean
  tiled: string | null
  data?: Record<string, unknown>
}

/** Full workspace snapshot persisted server-side. */
export interface WorkspaceSnapshot {
  version: number
  windows: SerializedWindow[]
  tabs: SerializedTab[]
  activeTabIndex: number
}

/** A workspace event emitted for real-time sync between devices. */
export interface WorkspaceEvent {
  action: string
  [key: string]: unknown
}

/** Callback used by viewer sync (play/pause/seek). */
export type ViewerCallbackFn = (action: string, currentTime?: number) => void

// -----------------------------------------------------------------------------
// 12. File Icon Types
// -----------------------------------------------------------------------------

/** Supported icon type keys for the file icon mapping. */
export type IconType =
  | 'folder'
  | 'file'
  | 'text'
  | 'code'
  | 'image'
  | 'video'
  | 'audio'
  | 'archive'
  | 'pdf'
  | 'spreadsheet'
  | 'document'
  | 'presentation'
  | 'config'
  | 'exec'
  | 'web'
  | 'database'
  | 'markdown'
  | 'python'
  | 'javascript'
  | 'css'
  | 'shell'
  | 'sql'
  | 'cpp'
