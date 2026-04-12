# 用户接口 `/user`

返回总入口：[返回接口文档首页](./README.md)

## `GET /user/`
获取当前用户信息。

鉴权：已登录

响应：
```json
{
  "id": "...",
  "username": "alice",
  "display_name": "Alice",
  "role": "user",
  "email": "alice@example.com",
  "totp_enabled": true,
  "avatar_url": "/user/avatar/alice"
}
```

## `GET /user/storage`
获取当前用户存储占用。

鉴权：已登录

响应：
```json
{"size": 123456, "count": 42}
```

## `GET /user/security`
获取当前用户安全配置摘要。

鉴权：已登录

响应：
```json
{
  "email": "alice@example.com",
  "has_email": true,
  "totp_enabled": true,
  "smtp_enabled": true
}
```

## `PUT /user/display-name`
更新显示名。

鉴权：已登录

请求体：
```json
{"display_name": "Alice"}
```

响应：
```json
{"ok": true}
```

## `PUT /user/security/password`
修改密码，并使当前会话之外的其他会话失效。

鉴权：已登录

请求体：
```json
{"old_password": "old", "new_password": "new"}
```

响应：
```json
{"ok": true}
```

## `POST /user/security/email/bind`
发送邮箱绑定验证码。

鉴权：已登录

请求体：
```json
{"email": "alice@example.com"}
```

响应：
```json
{"ok": true}
```

## `POST /user/security/email/verify`
确认邮箱绑定。

鉴权：已登录

请求体：
```json
{"email": "alice@example.com", "code": "123456"}
```

响应：
```json
{"ok": true}
```

## `DELETE /user/security/email`
解绑邮箱。

鉴权：已登录

响应：
```json
{"ok": true}
```

## `POST /user/security/otp/setup`
生成 TOTP Secret 与 otpauth URI。

鉴权：已登录

响应：
```json
{
  "secret": "BASE32SECRET",
  "uri": "otpauth://totp/..."
}
```

## `POST /user/security/otp/enable`
启用 TOTP。

鉴权：已登录

请求体：
```json
{"code": "123456"}
```

响应：
```json
{"ok": true}
```

## `DELETE /user/security/otp`
关闭 TOTP。

鉴权：已登录

响应：
```json
{"ok": true}
```

## `GET /user/avatar/:username`
公开读取用户头像。服务端会从 OSS 读取并解密后返回。

鉴权：无需登录

响应：
- 成功：`image/webp`
- 失败：
```json
{"error": "no_avatar"}
```
