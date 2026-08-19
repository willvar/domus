# 任务接口 `/task`

返回总入口：[返回接口文档首页](./README.md)

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
取消任务；上传任务会联动中止 multipart 上传和 DOFS reservation。

鉴权：已登录（任务拥有者或 Root）

响应：
```json
{"ok": true}
```

当前唯一用户可见任务类型是 `upload`。浏览器可能上报 `generating`、`encrypting`、
`uploading`、`thumbnail` 和 `processing` 阶段；其中 `thumbnail` 表示浏览器正在加密直传
缩略图，不是服务端预览任务。最终状态为 `completed`、`failed` 或 `cancelled`。
