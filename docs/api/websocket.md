# WebSocket 协议

返回总入口：[返回接口文档首页](./README.md)

## 连接

- URL：`/ws`
- HTTP Upgrade 时要求有效 `domus_session` Cookie；升级期间兼容旧
  `zephyr_session` 并自动迁移。
- 普通 HTTP 请求和未登录升级会被拒绝。

客户端请求、响应和服务端事件分别使用：

```json
{"id":"req-1","action":"subscribe.directory","data":{"path":"/"}}
```

```json
{"id":"req-1","action":"subscribe.directory","ok":true,"data":{"ok":true}}
```

```json
{"event":"dir.changed","data":{"path":"/","change_type":"refresh"}}
```

## 客户端动作

### `task.report`

浏览器上报自己负责的上传任务进度。`task_id` 必须属于当前用户且任务类型必须为
`upload`。

```json
{
  "id":"1",
  "action":"task.report",
  "data":{"task_id":"...","progress":0.5,"phase":"encrypting","status":"running"}
}
```

### `subscribe.directory`

订阅当前用户命名空间中的一个目录。`/` 是 namespace 根；订阅键在服务端与不可变
用户 ID 组合，即使多个租户都订阅 `/` 也不会串事件。

```json
{"id":"2","action":"subscribe.directory","data":{"path":"/"}}
```

### `unsubscribe.directory`

取消目录订阅，数据格式与订阅相同。

## 服务端事件

- `task.update`：上传任务的状态、进度与阶段；
- `dir.changed`：已订阅目录发生变化；
- `session.expired`：当前登录会话已失效。

## 已移除动作

浏览器终端和桌面窗口状态不属于当前文件管理产品。以下动作不再注册，请求会得到
`unknown_action`：

- `session.open`、`session.input`、`session.resize`、`session.close`；
- `workspace.event`。
