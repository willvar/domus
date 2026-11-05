# OAuth 授权与只读 WebDAV

第三方应用通过 OAuth authorization code + PKCE `S256` 获得 `files:read`，再以 Bearer
令牌读取 `/dav/`。它不是登录/OIDC 平台，也不提供分享、写入、历史、OCR 或内容转换 API。
所有端点都运行在同一个 `net/http.Server` 和监听端口上。

## 启用与信任边界

```yaml
server:
  public_url: "https://files.example.com"
```

`public_url` 是浏览器打开 Domus 的规范 origin，不包含路径、查询或片段；本机开发可用
`http://127.0.0.1:8089`。省略时 OAuth/DAV 不启用，既有产品不受影响。生产必须使用 HTTPS。
前端与 API 建议同源，由反向代理将 `/oauth/`、`/dav`、`/dav/` 和
`/.well-known/oauth-authorization-server` 转发到现有 Domus HTTP 监听器；`/authorize` 和
`/applications` 则由静态前端的 SPA fallback 提供，不要将 `/authorize` 误代理到后端。

反向代理应保留 `Host`、`Authorization`、`Origin` 和条件/Range 请求头，禁用 DAV 的代理
缓存、响应压缩和整包缓冲；为静态前端设置 `Content-Security-Policy: frame-ancestors 'none'`
和 `X-Frame-Options: DENY`。授权页也拒绝在 iframe 内提交同意。代理应转发客户端 IP；
当前 HTTP 层不信任外部 `X-Forwarded-For`，只接受来自回环代理的 `X-Forwarded-Proto`。
不要记录令牌、客户端密钥或表单正文。长下载需要相应的代理流式超时预算。

授权固定涵盖当前及未来的所有普通可见文件（名称、目录、元数据、原始字节），不会获得：

- `/.domus/`、`/.user/` 等应用内部目录、缩略图、转码派生文件；
- 回收站、已删除/待上传文件、历史版本；
- 上传、删除、修改、分享或账号/管理员 API 权限。

`/__trash__/` 是普通用户自建目录名，不是产品回收站，仍可读取。每个令牌只选择其授权用户
的 DOFS namespace，路径中不要拼接用户名或其他用户 ID。

浏览器 `/file/access` 的短期密文 URL、DEK 描述与浏览器到 OSS 的加密直传完全不变。
只有 OAuth 授权的 `/dav/` 返回通过现有 DOFS backend 解密的原文；不会返回 OSS URL、密文
对象键或密钥。应用服务器仍是受信任的密钥托管方，此功能不改变现有加密格式和存储布局。

## 注册应用

文件界面用户菜单的「已连接应用」以弹窗打开，不离开当前目录。旧 `/applications` 地址
返回文件界面，不再提供独立管理页面。管理员可以注册/停用应用；普通用户只能查看和撤销
自己的授权。注册本身不授予文件权限。

以下管理接口使用现有登录 Cookie，所有修改还要求 `Origin` 精确等于 `server.public_url`：

| 接口 | 权限 | 内容 |
|---|---|---|
| `GET /oauth/clients` | root | 有效应用列表，不返回密钥 |
| `POST /oauth/clients` | root | 注册，客户端密钥仅在创建响应中返回一次 |
| `DELETE /oauth/clients/{client_id}` | root | 停用，对所有用户立即失效 |
| `GET /oauth/grants` | 当前用户 | 自己的有效授权列表 |
| `DELETE /oauth/grants/{id}` | 当前用户 | 撤销这一份授权及其全部令牌 |

注册请求：

```json
{
  "name": "Consumer application",
  "public": false,
  "redirect_uris": ["https://app.example.com/oauth/callback"]
}
```

回调地址为 1–10 个精确 URL，只允许 HTTPS 或回环 HTTP，无凭据、片段、通配符。
`public: false` 适用于能在服务端保管密钥的应用，创建响应含 `client_id`、`client_secret`；
后续只能查询 ID，不能取回密钥。公共客户端（例如本机或纯浏览器应用）使用 `public: true`，
没有客户端密钥。两者都必须使用 PKCE，不能把服务端客户端密钥放入前端代码。

## 标准授权流程

1. 应用生成随机 `state` 与 43–128 字符 PKCE `code_verifier`，保存到自身的登录事务中；
   `code_challenge = BASE64URL(SHA256(code_verifier))`，不含填充符。
2. 在顶层浏览器窗口打开：

   ```text
   GET /oauth/authorize?response_type=code&client_id=CLIENT_ID&redirect_uri=REGISTERED_URL&scope=files%3Aread&state=RANDOM_STATE&code_challenge=CHALLENGE&code_challenge_method=S256
   ```

3. Domus 校验请求后显示 `/authorize`；未登录时沿用现有登录页面并保留原路由。
   必须由用户明确同意。拒绝回调包含 `error=access_denied` 和原 `state`。
4. 应用收到 `code` 后先验证 `state`，然后以表单交换令牌：

   ```http
   POST /oauth/token
   Content-Type: application/x-www-form-urlencoded
   Authorization: Basic BASE64(CLIENT_ID:CLIENT_SECRET)

   grant_type=authorization_code&code=CODE&redirect_uri=REGISTERED_URL&code_verifier=VERIFIER
   ```

   公共客户端不发送 Basic，改为表单中的 `client_id=CLIENT_ID`。
   机密客户端也支持 `client_secret_post`，优先使用 Basic。

成功响应是标准 OAuth JSON：`access_token`、`token_type=bearer`、`expires_in=900`、
`refresh_token`、`scope=files:read`。响应禁止缓存。授权码有效期 5 分钟且只能消费一次；
不支持 implicit、password、client credentials、OIDC 或 JWT 客户端断言。

### 刷新和撤销

```http
POST /oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&refresh_token=REFRESH_TOKEN&client_id=PUBLIC_CLIENT_ID
```

机密客户端使用上述 Basic 认证。刷新令牌有效期 30 天，每次成功刷新都会轮换它、废止旧的
访问令牌并重新开始有效期；应用必须原子保存新令牌，不能并发刷新或重试旧令牌。
重用已消费的授权码或刷新令牌会撤销整份授权（包括最新令牌），需要重新取得用户同意。
存储使用 PostgreSQL 事务和行锁；失败的 PKCE 不会提前消耗授权码。

标准撤销接口是 `POST /oauth/revoke`，表单包含 `token`、可选 `token_type_hint`，以及同样的
客户端认证。撤销有效访问/刷新令牌会撤销其整份授权；无效令牌返回 200。也可在 Domus
「已连接应用」撤销某份授权，或由管理员停用整个应用。每次新 DAV 请求和刷新都检查
客户端、授权和用户是否仍存在/有效。已经开始的下载不做持续轮询撤销检查。

Cookie 登录会话与 OAuth 授权是分开的：退出登录不撤销已授权的应用，访问令牌不能调用
`/user`、`/file`、`/audit` 或 WebSocket。Cookie 也不能访问 DAV；URL/query token、刷新令牌
不能代替 DAV 的 Bearer 访问令牌。

发现文档：`GET /.well-known/oauth-authorization-server`。除标准 OAuth 元数据外，
`webdav_endpoint` 给出 `/dav/` 的规范地址。

## 同意页内部接口

- `POST /oauth/consent`：登录 Cookie + 同源 Origin，JSON `{"query":"原 OAuth 查询字符串"}`。
  返回经校验的应用名称、ID、回调、scope 和随机 `challenge`（10 分钟有效）。
- `POST /oauth/decision`：同一用户及同一登录会话的表单 `challenge=...&decision=allow|deny`。
  挑战只能消费一次；成功/拒绝由 Fosite 产生标准 303 回调。不能用另一登录会话批准。

这些接口只供 Domus 同意页使用；第三方应用应从 `/oauth/authorize` 开始，不应代替用户
调用同意接口。任意额外/重复授权参数、不精确的回调、非 `S256` 和其他 scope 都被拒绝。

## WebDAV

所有请求携带 `Authorization: Bearer ACCESS_TOKEN`，路径为 namespace 相对路径并进行正常
URL 编码，例如 `/dav/Documents/report.pdf`。不要通过 Basic 用户密码或 URL 参数传令牌。

| 方法 | 行为 |
|---|---|
| `OPTIONS` | `Allow: OPTIONS, PROPFIND, GET, HEAD`，`DAV: 1`；不宣告写锁 |
| `PROPFIND` | 标准 XML `207 Multi-Status`；`Depth` 必须为 `0` 或 `1` |
| `GET` | 原始文件明文字节，保留原始格式，不转换内容 |
| `HEAD` | 文件长度、类型、修改时间、ETag；不读取/解密文件正文 |
| 其他方法 | `405`；文件系统层也独立拒绝所有写操作 |

支持单段/多段/后缀 Range、`Content-Range`、强 ETag，以及 `If-Match`、`If-None-Match`、
`If-Modified-Since`、`If-Unmodified-Since`、`If-Range`。这些语义由现有 WebDAV 库与
`http.ServeContent` 处理。文件类型按扩展名给出，未知类型为 `application/octet-stream`；
下载设为 attachment，并使用 sandbox/nosniff，避免把用户 HTML 当作 Domus 页面运行。

正文使用固定 1 MiB 明文窗口的 DOFS Range 读取，整文件不入内存、不落明文临时文件，
适配层不另外启动传输协程。请求取消会传到对象存储；文件关闭会取消并等待正在进行的
读取（包括标准库多段 Range 的生产者），再释放明文窗口、对象响应体和 backend。
一个请求固定读取打开时的 generation，避免跨版本拼接；并发删除/回收可能中断正在进行
的下载，客户端可重新发现文件后重试。目录列表沿用 DOFS 的整目录元数据切片，内存为
O(单目录条目数)，不做全树缓存；无限深度 PROPFIND 返回 `403 propfind-finite-depth`。

## 持久化与运维

应用、授权、同意挑战及令牌签名保存在现有 PostgreSQL 的 `oauth_*` 表。客户端密钥只存
bcrypt，令牌只存签名和白名单会话字段，不保存原始令牌、浏览器会话、KEK 或 DEK。
已消费 code/refresh 记录在授权有效期间保留，用来检测重放；不要当作普通过期缓存清空。
`server.session_secret` 通过域分离 HMAC 派生 OAuth 签名密钥，更换它会使既有令牌失效。

数据库备份包含授权状态，必须与配置密钥一起按敏感数据保护。若恢复旧的数据库快照，
在重新开放服务前撤销恢复出的授权，避免旧令牌状态重新生效；不要只恢复某几张 OAuth 表。

可运行检查：`go test ./internal/oauth ./internal/dav ./internal/httpx ./internal/handler`。
OAuth 测试需要可连接的 PostgreSQL，使用 `TEST_DATABASE_DSN`，在独立随机 schema 中验证
真实事务和并发，测试结束清理自己的 schema；无法连接数据库时会显式跳过。

前端同意、原生表单提交、一次性密钥展示和桌面/移动端撤销交互可独立验收，不启动后端
或访问对象存储：在 `frontend/` 执行
`DOMUS_E2E_FRONTEND_ONLY=1 npx playwright test e2e/oauth-applications.spec.ts`。
