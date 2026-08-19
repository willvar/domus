# 审计与管理接口 `/audit` / `/admin`

返回总入口：[返回接口文档首页](./README.md)

## 审计与用户管理 `/audit`

### `GET /audit/`
分页查询审计日志。

鉴权：Root

查询参数：
- `page`
- `size`
- `user`
- `action`
- `from`（RFC3339）
- `to`（RFC3339）

响应：
```json
{
  "total": 100,
  "page": 1,
  "size": 50,
  "items": []
}
```

### `POST /audit/`
写入预览时长等审计预览数据。

鉴权：已登录

请求体：
```json
{"path": "/a.txt", "duration_ms": 1234, "type": "preview"}
```

响应：通常为 `204 No Content`。

### `GET /audit/user/`
列出用户。

鉴权：Root

响应：用户数组。

### `POST /audit/user/`
创建用户。

鉴权：Root

请求体：
```json
{"username": "alice", "password": "secret", "role": "user"}
```

响应：`201 Created`，返回用户对象。

用户名必须匹配 `[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}`，且不能以 `.` 或 `-` 开头；
否则返回 `400 invalid_username`。用户名用于登录和 namespace 展示标签，不进入文件
路径，也不再与容器或 Unix 账户创建相关。

### `PUT /audit/user/:id`
更新用户角色或密码。

鉴权：Root

请求体：
```json
{"role": "admin", "password": "newpass"}
```

响应：
```json
{"ok": true}
```

### `DELETE /audit/user/:id`
删除用户。

鉴权：Root

响应：
```json
{"ok": true}
```

### `DELETE /audit/user/:id/otp`
重置目标用户 OTP，并撤销其会话。

鉴权：Root

响应：
```json
{"ok": true}
```

### `DELETE /audit/user/:id/email`
清空目标用户邮箱，并撤销其会话。

鉴权：Root

响应：
```json
{"ok": true}
```

## 管理接口 `/admin`

### `GET /admin/oss/cors-check`
生成 OSS CORS 检测所需的临时 PUT / DELETE URL。

鉴权：Root

响应：
```json
{
  "put_url": "https://...",
  "delete_url": "https://..."
}
```
