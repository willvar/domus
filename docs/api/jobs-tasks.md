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
取消任务；若任务是上传任务，也会尝试联动取消与清理。

鉴权：已登录（任务拥有者或 Root）

响应：
```json
{"ok": true}
```
