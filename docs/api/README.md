# Zephyr 接口文档

这里是项目当前的主接口文档入口，统一组织 HTTP 接口与 WebSocket 协议说明。

对应实现入口：
- HTTP 路由注册：`internal/handler/handler.go`
- WebSocket 动作注册：`internal/handler/ws_handlers.go`
- WebSocket 分发：`internal/ws/*.go`

## 目录

### 通用
- [通用约定](#通用约定)
- [SSE 约定](#sse-约定)

### HTTP API
- [认证 `/auth`](./auth.md)
- [当前用户 `/user`](./user.md)
- [审计与管理 `/audit` + `/admin`](./audit-admin.md)
- [文件 / 上传 / 分享 `/file`](./file.md)
- [作业与任务 `/job` + `/task`](./jobs-tasks.md)
- [工作区 `/workspace`](./workspace.md)

### WebSocket
- [WebSocket 协议](./websocket.md)

## 通用约定

### 基础
- HTTP 基础路径：无统一 `/api` 前缀，以下路径均直接挂在服务根路径下。
- 鉴权方式：登录成功后服务端设置会话 Cookie `zephyr_session`。
- 需要登录的接口：依赖该 Cookie。
- Root 管理接口：除登录外，还要求当前用户角色为 `root`。

### 常见响应
- 成功通常返回 JSON，例如：`{"ok": true}`。
- 失败通常返回 JSON，例如：`{"error": "invalid_request"}`。
- 某些接口返回流：
  - 文件/任务进度：`text/event-stream`
  - 头像：`image/webp`

## SSE 约定

以下接口在请求头 `Accept: text/event-stream` 时会返回 SSE 流：
- `POST /file/copy`
- `POST /file/move`
- `DELETE /file/delete`（部分目录场景）
- `GET /job/:id/status`

文件类 SSE 常见事件数据格式：
```json
{"done": 3, "total": 10, "current": "user/home/a.txt"}
```

完成时通常发送：
```json
{"done": true}
```

失败时通常发送：
```json
{"error": "..."}
```

## 维护说明

- 本目录是 canonical 文档位置。
- 根目录 `API.md` 仅保留为兼容入口。
- 若后端路由或 WebSocket 动作发生变化，请同步更新对应模块文件。
