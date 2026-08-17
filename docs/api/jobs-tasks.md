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
取消任务；上传任务会联动中止上传，`preview` / `transcode` 会取消容器内执行并
尽力清理部分输出。

鉴权：已登录（任务拥有者或 Root）

响应：
```json
{"ok": true}
```

当前服务端任务类型包括：

- `upload`；
- `preview`（工作区生成缩略图）；
- `transcode`（固定媒体配置转码）。

预览/转码常见阶段为 `generating`、`thumbnail`、`transcoding`、`finalizing`，
最终状态为 `completed`、`failed` 或 `cancelled`。
