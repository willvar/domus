# 贡献指南

Domus 是对象存储原生、应用层加密、多租户隔离的浏览器文件管理器。桌面端与移动端
使用同一套响应式文件界面、路由和状态模型。

## 产品边界

当前能力包括目录浏览、文件名搜索、排序与多选，文件/目录的新建、复制、移动、
重命名、回收和恢复，浏览器加密直传、解密读取、文件预览、账户安全、管理与审计。

分享、媒体转码、浏览器终端、SSH、任意命令执行、Linux 桌面模拟和桌面窗口状态均不
属于当前产品。旧数据库表可能为升级清理而保留，但对应 HTTP/WS 路由不得重新暴露。

## 技术栈

| 层级 | 技术 |
| --- | --- |
| 后端 | Go 1.26、Fiber、GORM |
| 产品数据库 | PostgreSQL |
| 文件命名空间 | DOFS；单机默认 SQLite，多主机可选 PostgreSQL |
| 文件对象 | S3-compatible 对象存储（包括阿里云 OSS） |
| 前端 | Vue 3、Vue Router、Pinia、Vite、Breeze |
| 实时通信 | WebSocket（目录通知与上传任务进度） |
| 浏览器测试 | Playwright |

主要实现入口：

```text
cmd/                         CLI、开发进程和备份恢复
config/                      YAML 配置
internal/dofsbridge/         Domus 到独立 DOFS 模块的适配
internal/fileview/           inode/generation 的产品投影
internal/handler/            HTTP/WS 控制面
internal/model/              用户、任务、审计等产品数据
frontend/src/views/          FileShell、FilePreview、AdminView
frontend/src/stores/         认证、文件、上传和任务状态
frontend/e2e/                真实浏览器 E2E
deploy/systemd/              Web 与可选 DOFS FUSE unit
```

## 开发环境

需要 Go 1.26+、Node.js 22.13+、PostgreSQL 和一个专用 S3-compatible bucket。运行 Domus
文件管理产品不需要 Docker、FUSE 或 `user_allow_other`；只有测试/使用可选 Linux FUSE
挂载时才需要 FUSE 3。

```bash
cp config.example.yaml config.yaml
cd frontend && npm install && cd ..
make dev
```

在 `config.yaml` 中填写 `oss.*` 与 `database.*`。SMTP 只用于邮箱验证码。
`session_secret`、`encryption_secret` 首次启动可自动生成，生产环境必须持久备份。
root 初始密码优先通过 `server.root_bootstrap_password_file` 提供。

`make dev` 同时启动 Domus Web 和 Vite，默认入口为 `http://127.0.0.1:8089`，API 默认
为 `http://127.0.0.1:8088`。后端状态隔离在 `tmp/dev`，不构建镜像或启动容器。

## 验证

```bash
go test ./... -count=1 -timeout 120s
go vet ./...
cd frontend && npm run check && npm run build
make test-e2e
```

E2E 使用真实 PostgreSQL、OSS 和浏览器，覆盖加密直传/读回、文件生命周期、浏览器
生成 PDF 缩略图、默认关闭的缩略图显示开关、多租户隔离、响应式界面和重启持久化；
浏览器套件结束后还会创建并永久删除一个隔离密文 fixture，以 OSS Stat 验证物理回收。
默认读取 `tmp/dev/root-bootstrap-password`；也可设置 `DOMUS_E2E_PASSWORD`。

```bash
E2E_REUSE_SERVERS=1 make test-e2e
DOMUS_E2E_BROWSER_EXECUTABLE=/opt/google/chrome/chrome DOMUS_E2E_HEADED=1 make test-e2e
```

独立 DOFS 的真实 Linux FUSE 测试由 sibling `dofs` 仓库提供：

```bash
make test-dofs
```

## 数据路径与安全边界

文件正文不经过 Domus HTTP：

```text
File/Blob
  -> 浏览器 Web Worker 分块 AES-256-GCM
  -> presigned URL 直传 OSS 密文
  -> Domus 校验 OSS 权威状态
  -> DOFS generation CAS 发布
```

浏览器读取短期 OSS URL 中的密文并在本地解密。Domus 托管包装密钥，因此这是应用层
加密而非零知识加密；应用服务器具备授权解密能力，但正常上传/下载数据路径不代理
正文。

图片、视频和 PDF 的缩略图在上传浏览器中生成。缩略图有独立 DEK、独立 DOFS inode
和独立密文对象，源文件元数据以 `source inode + generation -> thumbnail inode` 关联：

- 重命名/移动保留关系；
- 覆盖内容会解绑并回收旧缩略图；
- 复制不共享源缩略图；
- 永久删除源文件会回收缩略图。

“显示缩略图”是设备本地偏好，默认关闭。关闭只禁止列表/详情加载和渲染缩略图，
不禁止上传时生成；这样已有缩略图可随时开启显示，同时默认不产生额外 OSS GET 流量。

HTTP 负责 query/command，WebSocket 只负责目录订阅、`task.update` 与 `task.report`。
不得恢复 `session.*` 或 `workspace.event`。

文件路径始终是当前认证用户的 namespace 相对绝对路径：`/` 对应 DOFS 根 inode，用户
名不得拼进路径，也不得重新创建 `/home/<username>`。`/.domus/` 是保留的应用内部目录，
公开 API 和文件列表必须隐藏；回收站公开路径固定为 `/__trash__/`。

## 代码规范

- Go 使用 `gofmt`；Handler 负责鉴权与封闭请求解析，文件语义由 DOFS/fileview 提供。
- 用户文件操作不得绕过 DOFS 直接读写对象正文。
- 上传控制字段使用 allowlist，不得加入正文、base64 或客户端自报 ETag 路径。
- Vue 使用 Composition API、Pinia 和现有 Breeze 组件。
- 桌面/移动端复用 `FileShell`；预览使用 `/preview` 路由。
- 文案同时更新英文和中文；交互变化同步更新 Playwright。

## 构建与部署

```bash
make build
make build-win
cd frontend && npm run build
```

Web/控制面可以跨平台构建。`domus dofs serve` 的 FUSE 挂载仍只支持 Linux，并且是
供外部普通 I/O 消费者使用的可选服务，不是 Domus Web 的启动依赖。生产说明见
`docs/dofs-production.md`。

`backup`、`restore --yes` 和 `reset --yes` 以整个配置 bucket 为边界；执行前必须停止
Web 以及任何共享该 DOFS 元数据的可选挂载服务。

## 提交规范

提交信息使用单行 `<类型>: <中文描述>`：`ADD:` 新能力、`OPT:` 优化/重构、`FIX:`
缺陷修复。一次提交只聚焦一个逻辑变更。
