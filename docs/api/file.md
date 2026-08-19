# 文件接口 `/file`

返回总入口：[返回接口文档首页](./README.md)

所有接口都要求有效登录会话。Domus 的文件 HTTP 接口是控制面，不是文件正文代理：

```text
浏览器选取文件
  -> 浏览器生成 DEK，并按固定分块格式加密
  -> Domus 仅创建 DOFS generation 预留和 OSS multipart 会话
  -> 浏览器用 presigned URL 将密文分片直接 PUT 到 OSS
  -> Domus 从 OSS ListParts/HeadObject 取得权威结果并发布 generation
```

- 浏览器到 Domus 的文件写请求必须是 `application/json`，只允许各接口列出的字段；
- 文件明文和密文正文都不会进入 Domus HTTP 请求体；
- `content`、`plaintext`、`file`、`blob`、`bytes`、`data`、`parts`、
  `search_text`、`diff`、`ciphertext`、`base64` 等正文或旧协议字段会被拒绝；
- Domus HTTP 请求体上限为 1 MiB；
- 这是服务端托管的应用层加密，不是零知识加密：Domus 控制面会接收 DEK，并用用户
  KEK 包装保存；它不接收上传文件正文，但受信任的 DOFS backend 具备授权解密能力。

## 文件浏览与读取

### `GET /file/`

列出目录的直接子项。

查询参数：

- `path`：当前登录用户 namespace 内的绝对路径；`/` 是文件根目录，例如 `/Documents/`。

用户名不属于路径。服务端从会话中的不可变用户 ID 选择 DOFS namespace，因此两个用户
可以同时拥有 `/a.txt`，但会解析到彼此隔离的 inode、密钥和对象。`/.domus/` 是保留的
内部存储路径，公开接口返回 `403`；回收站使用虚拟路径 `/__trash__/`。

响应示例：

```json
{
  "files": [
    {
      "name": "a.txt",
      "path": "/a.txt",
      "is_dir": false,
      "size": 123,
      "created_at": "2026-08-18T10:00:00Z",
      "last_modified": "2026-08-18T10:00:00Z",
      "content_type": "text/plain",
      "status": "ready",
      "thumbnail_url": "https://object-storage.example/...",
      "thumbnail_dek": "hex-encoded-32-byte-key"
    }
  ]
}
```

上传中的条目还可能包含 `task_id`、`task_progress` 和 `task_phase`；媒体条目可能包含
`media_width`、`media_height` 和 `media_duration`。

### `GET /file/search`

按文件名搜索当前用户的 DOFS 命名空间。不会读取或索引文件正文。

查询参数：

- `query`：必填；
- `limit`：可选，默认 100。

响应示例：

```json
{
  "results": [
    {
      "path": "/a.txt",
      "parent": "/",
      "name": "a.txt",
      "is_dir": false,
      "size": 123,
      "content_type": "text/plain",
      "rank": 0.42
    }
  ]
}
```

### `GET /file/access`

返回当前不可变 generation 的临时 OSS 下载 URL 和浏览器本地解密所需元数据。
浏览器直接从 OSS 读取密文，Service Worker/前端在本地解密；Domus 不代理文件正文。

查询参数：

- `path`：必填；
- `optional=true`：文件不存在时返回 `204`，否则返回 `404`。

`internal=true` 和 `/.user/...` 仅供同源 Domus 前端读取产品私有加密文件，不是通用
文件浏览能力；它映射到 `/.domus/user/` 或 `/.domus/thumbnails/`。

响应示例：

```json
{
  "url": "https://object-storage.example/...",
  "size": 123,
  "name": "a.txt",
  "content_type": "text/plain",
  "chunk_size": 65536,
  "dek": "hex-encoded-32-byte-key",
  "content_hash": "optional-sha256-hex",
  "generation": 7
}
```

## 目录与文件命令

这些接口只接收路径和命令元数据。复制、移动和删除由 DOFS generation/命名空间
语义完成，不会把文件正文带入 HTTP 请求。

### `POST /file/mkdir`

```json
{"path": "/newdir/"}
```

### `POST /file/rename`

```json
{
  "old_path": "/a.txt",
  "new_path": "/b.txt",
  "is_dir": false
}
```

### `POST /file/copy`

```json
{
  "src_path": "/a.txt",
  "dst_path": "/b.txt",
  "is_dir": false
}
```

普通请求返回 `{"ok":true}`。请求头为 `Accept: text/event-stream` 时，目录复制可返回
SSE 进度流。

### `POST /file/move`

```json
{
  "src_path": "/a.txt",
  "dst_path": "/archive/a.txt",
  "is_dir": false
}
```

普通请求返回 `{"ok":true}`。请求头为 `Accept: text/event-stream` 时，目录移动可返回
SSE 进度流。

### `DELETE /file/delete`

查询参数：

- `path`：必填；
- `permanent=true|1`：跳过回收站并永久删除。

普通请求返回 `{"ok":true}`；部分目录操作可返回 SSE 进度流。删除上传中条目会中止
multipart 会话和 DOFS 预留。移入回收站仍计入用户用量；永久删除会在 DOFS 元数据
事务中立即释放逻辑用量，并把源文件、所有 generation 及关联缩略图交给持久化
reclaimer。OSS 暂时失败或 FUSE 挂载占用不会恢复目录项，后台会继续幂等重试。

## 浏览器直传 `/file/upload`

`POST /file/upload` 使用三套互斥的封闭 JSON schema 分派冲突检测、初始化和完成操作。
一个请求不得混用三组字段。

### 1. 冲突检测

请求：

```json
{
  "path": "/",
  "names": ["a.txt", "b.txt"]
}
```

响应：

```json
{
  "conflicts": [
    {
      "name": "a.txt",
      "size": 123,
      "is_dir": false,
      "last_modified": "2026-08-18T10:00:00Z"
    }
  ]
}
```

### 2. 初始化上传

浏览器先生成 32 字节随机 DEK，再发送控制消息：

```json
{
  "path": "/",
  "file_name": "a.txt",
  "file_size": 123,
  "content_type": "text/plain",
  "conflict_strategy": "replace",
  "client_instance_id": "browser-instance-id",
  "internal": false,
  "dek": "64-lower-or-upper-case-hex-characters",
  "expected_generation": 6
}
```

字段说明：

- `conflict_strategy`：可省略，或为 `replace`、`rename`；冲突且未指定时返回
  `409 file_already_exists`；
- `expected_generation`：可选的乐观并发条件；不匹配时返回 `409 generation_conflict`；
- `internal`：仅供 Domus 前端写入 `/.user/...` 产品私有虚拟路径时，请求按需创建
  `/.domus/` 下缺失的父目录；
  它不放宽路径权限、配额、加密直传或正文边界；
- `dek`：必填的 32 字节 DEK 十六进制值。

响应：

```json
{
  "upload_id": "uuid",
  "task_id": "uuid-or-empty",
  "file_name": "a.txt",
  "oss_upload_id": "provider-upload-id",
  "part_size": 8388608,
  "total_parts": 1,
  "dek": "effective-64-character-hex-key"
}
```

覆盖现有文件时，DOFS 可以保留目标文件的 DEK；因此客户端必须使用响应中的 `dek`
加密，而不能假定它等于请求中的新 DEK。响应不会暴露 OSS 对象键。

### 3. 获取分片 URL

`GET /file/upload/presign?upload_id=<uuid>&start=1&count=100`

响应：

```json
{
  "parts": [
    {"part_number": 1, "presigned_url": "https://object-storage.example/..."}
  ],
  "expires_in": 3600
}
```

浏览器按固定 AES-256-GCM 分块格式加密，在这些 URL 上直接执行 `PUT`。URL 过期前可
调用 heartbeat 延长 DOFS 上传预留；URL 本身过期后重新 presign 即可。

### 4. 查询断点状态

`GET /file/upload/?upload_id=<uuid>`

响应：

```json
{
  "upload_id": "uuid",
  "task_id": "uuid-or-empty",
  "oss_upload_id": "provider-upload-id",
  "file_name": "a.txt",
  "file_size": 123,
  "status": "active",
  "parts": [
    {"part_number": 1, "size": 156, "etag": "provider-etag"}
  ]
}
```

`parts` 来自服务端向 OSS 执行的 `ListParts`，不是客户端提交的数据。

### 5. 完成上传

所有密文分片直传 OSS 成功后，浏览器只发送完成控制消息：

```json
{
  "upload_id": "uuid",
  "content_hash": "optional-plaintext-sha256-hex",
  "encrypted_size": 156,
  "media_width": 0,
  "media_height": 0,
  "media_duration": 0,
  "thumbnail_upload_id": "optional-uuid"
}
```

Domus 会自行执行 `ListParts`/`HeadObject`，校验分片数量、总密文长度和对象状态，再用
OSS 的权威 ETag 完成 multipart，并原子发布 DOFS generation。客户端不能提交
`parts`、ETag、DEK 或任何文件正文。

成功响应：

```json
{"ok": true, "generation": 7}
```

完成操作可安全重试。缩略图生成或上传失败不会触发服务端补偿任务，原文件仍可正常
发布并以通用文件图标显示。

### 6. 心跳、取消和清理

延长一个活动上传预留：

```http
POST /file/upload/heartbeat
Content-Type: application/json

{"upload_id": "uuid"}
```

取消上传：

```http
POST /file/upload/cancel
Content-Type: application/json

{
  "upload_id": "uuid",
  "task_id": "optional-uuid",
  "reason": "optional-reason",
  "status": "cancelled"
}
```

`status` 只有 `failed` 会被保留，其他值统一按 `cancelled` 处理。

清理由同一用户其他浏览器实例遗留且已经失去心跳的上传：

```http
POST /file/upload/cleanup
Content-Type: application/json

{"client_instance_id": "current-browser-instance-id"}
```

响应为 `{"ok":true,"count":N}`。

## 浏览器缩略图

上传浏览器为支持的图片、视频和 PDF 生成 WebP 缩略图。缩略图通过同一加密直传协议
成为独立隐藏 DOFS 文件，并以源 inode/generation 指向缩略图 inode。重命名、移动保留
绑定；覆盖、永久删除会回收旧缩略图；复制不共享缩略图。

文件界面的“显示缩略图”是设备本地开关，默认关闭。它只控制列表和详情是否请求并
渲染已有缩略图，不控制上传时生成。服务端不运行 preview worker 或转码任务。

## 已移除接口

服务端正文读写和 diff 写入不属于当前架构，以下旧路由不存在：

- `PUT /file/content/diff`；
- `PUT /file/shared/:share_id/content/diff`；
- `POST /file/transcode`；
- `/file/share`、`/file/shares`、`/file/shared` 及其子路由；
- 任何把明文、密文或 base64 文件正文提交给 Domus HTTP 的文件接口。

分享的旧表和内部迁移数据可以继续存在，以保证升级和清理安全，但不再注册任何分享
路由；上传控制消息中的 `share_id` 也会被拒绝。
