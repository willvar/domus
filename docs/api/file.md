# 文件接口 `/file`

返回总入口：[返回接口文档首页](./README.md)

## 文件浏览与检索

### `GET /file/`
列出目录直接子项。

鉴权：已登录

查询参数：
- `path`：应用层路径，如 `/home/alice/`

响应：
```json
{
  "files": [
    {
      "name": "a.txt",
      "path": "/home/alice/a.txt",
      "is_dir": false,
      "size": 123,
      "created_at": "2026-04-11T10:00:00Z",
      "last_modified": "2026-04-11T10:00:00Z",
      "content_type": "text/plain",
      "status": "ready",
      "task_id": "...",
      "task_progress": 0.5,
      "task_phase": "uploading",
      "thumbnail_url": "https://...",
      "thumbnail_dek": "..."
    }
  ]
}
```

### `GET /file/search`
全文/文件名搜索。

鉴权：已登录

查询参数：
- `query`：必填
- `limit`：可选，默认 100

响应：
```json
{
  "results": [
    {
      "path": "/home/alice/a.txt",
      "parent": "/home/alice/",
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
获取文件的临时下载 URL 与解密信息，供前端/Service Worker 解密读取。

鉴权：已登录

查询参数：
- `path`：必填
- `optional=true`：若文件不存在则返回 `204`

响应：
```json
{
  "url": "https://...",
  "size": 123,
  "name": "a.txt",
  "content_type": "text/plain",
  "chunk_size": 65536,
  "dek": "hex-encoded-32-byte-key",
  "content_hash": "..."
}
```

## 文件内容写入

### `PUT /file/content/diff`
按 diff/edit 列表修改文件内容。

鉴权：已登录

请求体：
```json
{
  "path": "/home/alice/a.txt",
  "base_size": 100,
  "edits": [
    {"offset": 0, "delete": 0, "insert": "hello"}
  ]
}
```

响应：
```json
{"ok": true, "new_size": 105}
```

### `PUT /file/shared/:share_id/content/diff`
修改共享文件内容，仅写权限共享可用。

鉴权：已登录（且必须是 share 目标用户）

请求体：
```json
{
  "base_size": 100,
  "edits": [
    {"offset": 0, "delete": 0, "insert": "hello"}
  ]
}
```

响应：
```json
{"ok": true, "new_size": 105}
```

## 目录 / 复制 / 移动 / 删除

### `POST /file/mkdir`
创建目录。

鉴权：已登录

请求体：
```json
{"path": "/home/alice/newdir/"}
```

响应：
```json
{"ok": true}
```

### `POST /file/rename`
重命名文件或目录。

鉴权：已登录

请求体：
```json
{"old_path": "/home/alice/a.txt", "new_path": "/home/alice/b.txt", "is_dir": false}
```

响应：
```json
{"ok": true}
```

### `POST /file/copy`
复制文件或目录。

鉴权：已登录

请求体：
```json
{"src_path": "/home/alice/a.txt", "dst_path": "/home/alice/b.txt", "is_dir": false}
```

普通响应：
```json
{"ok": true}
```

若请求头为 `Accept: text/event-stream`，则返回 SSE 进度流。

### `POST /file/move`
移动文件或目录。

鉴权：已登录

请求体：
```json
{"src_path": "/home/alice/a.txt", "dst_path": "/home/alice/archive/a.txt", "is_dir": false}
```

普通响应：
```json
{"ok": true}
```

若请求头为 `Accept: text/event-stream`，则返回 SSE 进度流。

### `DELETE /file/delete`
删除文件或目录；默认会进入回收站，某些场景可永久删除。

鉴权：已登录

查询参数：
- `path`：必填
- `permanent=true|1`：永久删除

普通响应：
```json
{"ok": true}
```

说明：
- 非 ready 文件会直接中止上传并清理对象/记录。
- 目录删除与某些移动路径下支持 SSE 进度流。

## 上传 `/file/upload`

### `GET /file/upload/`
查询上传状态与已完成分片。

鉴权：已登录

查询参数：
- `upload_id`

响应：
```json
{
  "upload_id": "...",
  "task_id": "...",
  "oss_upload_id": "...",
  "file_name": "a.txt",
  "file_size": 123,
  "status": "active",
  "parts": [
    {"part_number": 1, "size": 5242880, "etag": "..."}
  ]
}
```

### `POST /file/upload/`
复用入口，根据请求体自动分派为三种操作：

#### A. 冲突检测
请求体：
```json
{"path": "/home/alice/", "names": ["a.txt", "b.txt"]}
```

响应：
```json
{
  "conflicts": [
    {
      "name": "a.txt",
      "size": 123,
      "is_dir": false,
      "last_modified": "2026-04-11T10:00:00Z"
    }
  ]
}
```

#### B. 初始化上传
请求体：
```json
{
  "path": "/home/alice/",
  "file_name": "a.txt",
  "file_size": 123,
  "content_type": "text/plain",
  "conflict_strategy": "replace",
  "internal": false
}
```

响应：
```json
{
  "upload_id": "...",
  "task_id": "...",
  "oss_key": "alice/home/alice/a.txt",
  "file_name": "a.txt",
  "oss_upload_id": "...",
  "part_size": 5242880,
  "total_parts": 1
}
```

`conflict_strategy` 常见值：
- `replace`
- `rename`
- 不传时如冲突会返回 `409 file_already_exists`

#### C. 完成上传
请求体：
```json
{
  "upload_id": "...",
  "dek": "hex...",
  "content_hash": "...",
  "encrypted_size": 456,
  "parts": [
    {"part_number": 1, "etag": "..."}
  ],
  "search_text": "全文索引文本",
  "media_width": 1920,
  "media_height": 1080,
  "media_duration": 12.34,
  "thumbnail_upload_id": "..."
}
```

响应：
```json
{"ok": true}
```

### `GET /file/upload/presign`
批量获取上传分片 presigned URL。

鉴权：已登录

查询参数：
- `upload_id`
- `start`：起始分片号，默认 1
- `count`：数量，默认 100，上限受配置约束

响应：
```json
{
  "parts": [
    {"part_number": 1, "presigned_url": "https://..."}
  ],
  "expires_in": 3600
}
```

### `DELETE /file/upload/`
中止上传并清理记录。

鉴权：已登录

查询参数：
- `upload_id`
- `task_id`：可选，不传时使用上传记录中的任务 ID
- `status`：`cancelled` 或 `failed`

响应：
```json
{"ok": true}
```

## 分享

### `POST /file/share`
创建用户到用户的文件分享。

鉴权：已登录

请求体：
```json
{
  "path": "/home/alice/a.txt",
  "target_username": "bob",
  "permission": "read",
  "expires_in": 3600
}
```

响应：
```json
{"share_id": "uuid"}
```

### `GET /file/shares`
列出某个文件的分享记录。

鉴权：已登录

查询参数：
- `path`

响应：分享记录数组。

### `DELETE /file/share/:id`
撤销分享。Owner 与目标用户均可删除。

鉴权：已登录

响应：
```json
{"ok": true}
```

### `GET /file/shared`
列出“分享给我”的文件视图。

鉴权：已登录

响应：`ShareFileView[]`

### `GET /file/shared/:share_id`
获取共享文件的读取信息与解密参数。

鉴权：已登录（必须是 share 目标用户）

响应：
```json
{
  "url": "https://...",
  "size": 123,
  "name": "a.txt",
  "content_type": "text/plain",
  "chunk_size": 65536,
  "permission": "read",
  "dek": "hex..."
}
```
