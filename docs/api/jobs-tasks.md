# 作业与任务接口 `/job` / `/task`

返回总入口：[返回接口文档首页](./README.md)

## 作业 `/job`

### `GET /job/`
获取近期作业。

鉴权：已登录

响应：
```json
[
  {
    "job_id": "...",
    "type": "transcode",
    "status": "running",
    "progress": 0.5,
    "phase": "transcoding",
    "created_at": "2026-04-11T10:00:00Z"
  }
]
```

### `POST /job/`
创建作业。当前仅支持转码。

鉴权：已登录

请求体：
```json
{
  "type": "transcode",
  "path": "/home/alice/video.mov",
  "preset": "medium",
  "output_format": "mp4",
  "replace": false
}
```

响应：
```json
{"task_id": "..."}
```

### `DELETE /job/done`
清理已完成作业。

鉴权：已登录

响应：
```json
{"ok": true}
```

### `GET /job/:id/status`
以 SSE 方式轮询输出作业进度。

鉴权：已登录（作业拥有者或 Root）

SSE 数据示例：
```json
{
  "job_id": "...",
  "type": "transcode",
  "status": "running",
  "progress": 0.5,
  "phase": "transcoding"
}
```

失败时可能追加：
```json
{"error": "job_failed"}
```

### `DELETE /job/:id`
取消作业。

鉴权：已登录（作业拥有者或 Root）

响应：
```json
{"ok": true}
```

## 用户任务 `/task`

### `GET /task/`
列出近期用户任务。

鉴权：已登录

响应：任务数组。

### `DELETE /task/done`
删除已完成任务。

鉴权：已登录

响应：
```json
{"ok": true}
```

### `DELETE /task/:id`
取消任务；若任务关联后台作业或上传，也会尝试联动取消与清理。

鉴权：已登录（任务拥有者或 Root）

响应：
```json
{"ok": true}
```
