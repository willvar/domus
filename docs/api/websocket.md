# WebSocket 协议

返回总入口：[返回接口文档首页](./README.md)

## 连接

- URL：`/ws`
- 鉴权：HTTP Upgrade 时要求有效 `domus_session` Cookie（升级兼容旧 `zephyr_session`）
- 非 WebSocket Upgrade 请求会被拒绝
- 未登录或会话失效时返回 `401`

前端通常会根据页面协议和 `VITE_API_BASE` 自动构造：
- `ws://host/ws`
- `wss://host/ws`

## 消息格式

### 客户端请求
```json
{
  "id": "req-1-1710000000000",
  "action": "session.open",
  "data": {}
}
```

### 服务端成功响应
```json
{
  "id": "req-1-1710000000000",
  "action": "session.open",
  "ok": true,
  "data": {}
}
```

### 服务端失败响应
```json
{
  "id": "req-1-1710000000000",
  "action": "session.open",
  "ok": false,
  "error": "invalid_request"
}
```

### 非法消息
```json
{
  "id": "",
  "ok": false,
  "error": "invalid_message"
}
```

### 服务端推送事件
```json
{
  "event": "task.update",
  "data": {}
}
```

## 客户端可调用动作

### `task.report`
上报上传类任务的进度与状态。

请求：
```json
{
  "id": "1",
  "action": "task.report",
  "data": {
    "task_id": "...",
    "progress": 0.5,
    "phase": "encrypting",
    "status": "running"
  }
}
```

成功响应：
```json
{"id": "1", "action": "task.report", "ok": true, "data": {"ok": true}}
```

常见错误：
- `invalid_request`
- `task_not_found`
- `access_denied`
- `task_not_reportable`

### `session.open`
打开一个终端/会话实例。

请求：
```json
{
  "id": "2",
  "action": "session.open",
  "data": {
    "cwd": "/home/alice/"
  }
}
```

成功响应：
```json
{
  "id": "2",
  "action": "session.open",
  "ok": true,
  "data": {
    "session_id": "...",
    "cwd": "/home/alice/",
    "user": "alice"
  }
}
```

后续 `session.input` 始终是原始终端字节，容器内 Shell 自己处理提示符、历史、
补全、管道、重定向和 Ctrl+C。工作区启动失败会返回
`workspace_unavailable`，不存在其他执行模式。

常见错误：
- `too_many_sessions`
- 以及底层会话层返回的其他错误

### `session.input`
向终端会话发送输入。

请求：
```json
{
  "id": "3",
  "action": "session.input",
  "data": {
    "session_id": "...",
    "data": "ls\n"
  }
}
```

成功响应：
```json
{"id": "3", "action": "session.input", "ok": true, "data": {}}
```

常见错误：
- `invalid_request`
- `session_not_found`
- `forbidden`

### `session.resize`
调整终端大小。

请求：
```json
{
  "id": "4",
  "action": "session.resize",
  "data": {
    "session_id": "...",
    "cols": 120,
    "rows": 30
  }
}
```

成功响应：
```json
{"id": "4", "action": "session.resize", "ok": true, "data": {}}
```

### `session.close`
关闭终端会话。

请求：
```json
{
  "id": "5",
  "action": "session.close",
  "data": {
    "session_id": "..."
  }
}
```

成功响应：
```json
{"id": "5", "action": "session.close", "ok": true, "data": {}}
```

### `workspace.event`
向同一用户的其他连接广播工作区事件。

请求：
```json
{
  "id": "7",
  "action": "workspace.event",
  "data": {
    "type": "window-focus",
    "payload": {"id": "win-1"}
  }
}
```

成功响应：
```json
{"id": "7", "action": "workspace.event", "ok": true, "data": null}
```

### `subscribe.directory`
订阅某个目录变化。

请求：
```json
{
  "id": "8",
  "action": "subscribe.directory",
  "data": {"path": "/home/alice/"}
}
```

成功响应：
```json
{"id": "8", "action": "subscribe.directory", "ok": true, "data": {"ok": true}}
```

### `unsubscribe.directory`
取消目录订阅。

请求：
```json
{
  "id": "9",
  "action": "unsubscribe.directory",
  "data": {"path": "/home/alice/"}
}
```

成功响应：
```json
{"id": "9", "action": "unsubscribe.directory", "ok": true, "data": {"ok": true}}
```

## 服务端推送事件

### `task.update`
任务状态/进度变更。

```json
{
  "event": "task.update",
  "data": {
    "task_id": "...",
    "type": "upload",
    "name": "a.txt",
    "status": "running",
    "progress": 0.5,
    "phase": "uploading"
  }
}
```

### `dir.changed`
目录内容变化通知。

```json
{
  "event": "dir.changed",
  "data": {
    "path": "/home/alice/",
    "change_type": "refresh"
  }
}
```

`change_type` 常见值：
- `created`
- `deleted`
- `modified`
- `refresh`

### `session.output`
终端输出数据。

工作区终端将容器的原始字节作为 Base64 发送，避免 UTF-8 解码破坏
交互式程序的输出：

```json
{
  "event": "session.output",
  "data": {
    "session_id": "...",
    "data_base64": "/wBB"
  }
}
```

客户端应将 `data_base64` 解码为字节后写入终端。

### `session.exit`
会话结束。

```json
{
  "event": "session.exit",
  "data": {
    "session_id": "...",
    "reason": "closed"
  }
}
```

### `workspace.event`
同用户其他连接广播来的工作区事件。

```json
{
  "event": "workspace.event",
  "data": {
    "type": "window-focus",
    "payload": {"id": "win-1"}
  }
}
```

### `session.expired`
当前用户会话失效。

```json
{
  "event": "session.expired",
  "data": {}
}
```

## 备注

1. `/file/upload/` 为“复用入口”，需根据请求体字段判断是冲突检查、初始化还是完成上传。
2. `/workspace/` 的 `state` 为透传 JSON，服务端不强约束结构。
3. WebSocket 错误码目前以字符串为主，存在不同 handler 返回风格略有差异的情况。
