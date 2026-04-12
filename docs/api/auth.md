# 认证接口 `/auth`

返回总入口：[返回接口文档首页](./README.md)

## `GET /auth`
获取认证相关配置。

鉴权：无需登录

响应：
```json
{"smtp_enabled": true}
```

## `POST /auth/verify`
第一阶段认证校验。支持密码、OTP 或邮箱验证码发起。

鉴权：无需登录

请求体三选一：

### 1. 密码登录预校验
```json
{"username": "alice", "password": "secret"}
```

### 2. OTP 预校验
```json
{"username": "alice", "otp": "123456"}
```

### 3. 邮箱验证码发起
```json
{"username": "alice", "method": "email"}
```

成功响应：
```json
{"token": "challenge-token", "methods": ["email", "otp"]}
```

说明：
- 若用户无需二次验证，`methods` 为空数组。
- 邮箱方式存在反枚举处理，不保证用户名不存在时返回错误。
- 登录尝试过多可能返回：
```json
{"error": "too_many_attempts", "retry_after": 300}
```

## `POST /auth`
第二阶段登录，验证 challenge 并设置 `zephyr_session` Cookie。

鉴权：无需登录

请求体：
```json
{"token": "challenge-token", "code": "123456", "method": "email"}
```

若 challenge 不要求二次验证，可仅传：
```json
{"token": "challenge-token"}
```

成功响应：
```json
{
  "user": {
    "id": "...",
    "username": "alice",
    "role": "user",
    "email": "alice@example.com",
    "totp_enabled": true
  },
  "needs_setup": false
}
```

## `DELETE /auth`
注销当前会话并清除 Cookie。

鉴权：已登录

响应：
```json
{"ok": true}
```
