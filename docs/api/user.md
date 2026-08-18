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
  "avatar_endpoint": "/user/avatar/alice"
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
获取公开头像的控制面访问描述。头像正文不会经过 Domus：浏览器使用 `url` 直接从
OSS 获取密文，再用公开头像专属的 `dek` 在本地解密为 `image/webp`。头像本身是公开
资料，因此该接口无需登录且其 DEK 不作为秘密；用户的 KEK 不会离开服务器。

鉴权：无需登录

成功响应：
```json
{
  "url": "https://oss.example.com/...presigned...",
  "dek": "64-char-hex",
  "size": 12345,
  "content_type": "image/webp",
  "chunk_size": 65536,
  "generation": 3
}
```

控制响应带 `Cache-Control: no-store`。失败响应：
```json
{"error": "no_avatar"}
```
