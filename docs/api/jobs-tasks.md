# 任务接口 `/task`

返回总入口：[返回接口文档首页](./README.md)

## 用户任务 `/task`

### `GET /task/`
列出近期用户任务。

鉴权：已登录

响应：最多 50 条任务，活动任务优先、转码优先，避免批量上传记录挤掉仍在运行的转码。
类型为 `upload` 或 `transcode`。转码记录还包含 `profile`、显示用的
`source_path` 及失败时的脱敏 `error`。此接口用于首次打开、连接恢复和用户操作后的
状态快照，不作定时轮询。独立 worker 通过本机 `net/rpc` 提示 Web 读取最新任务并推送
`task.update`，推送包含同样的画质、显示路径和脱敏错误；断线时保留上次状态。

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

### `POST /task/:id/retry`

仅任务拥有者可重试已失败或取消的转码任务。服务器按源 inode 解析当前文件并创建新
任务，不使用可能过时的显示路径，也不会更改原件。正在处理或已完成的任务不可重试。

上传阶段包括 `generating`、`encrypting`、`uploading` 和 `processing`；新浏览器不再
上报缩略图生成。转码状态包括 `queued/running/cancelling/completed/failed/cancelled`，
阶段包括 `probing/transcoding/finalizing`。进度基于已发布时长，不伪造预计完成时间；
“已有可播放分段”与“全部完成”是不同状态。失败记录保留到用户清除。
