# 贡献指南

感谢你对 Domus 项目的关注！本文档将帮助你快速了解项目结构、搭建开发环境并参与贡献。

## 目录

- [项目概述](#项目概述)
- [技术栈](#技术栈)
- [项目结构](#项目结构)
- [开发环境搭建](#开发环境搭建)
- [开发工作流](#开发工作流)
- [接口文档](#接口文档)
- [代码规范](#代码规范)
- [提交规范](#提交规范)
- [架构指南](#架构指南)

---

## 项目概述

Domus 是一个全栈云文件管理平台，提供 Plasma 桌面风格的 Web UI。核心功能包括：

- 用户认证（密码 + TOTP 两步验证 + 邮箱验证）
- 文件管理（上传、下载、搜索、回收站）
- 媒体转码与媒体缩略图
- HTTP API + WebSocket 实时同步
- 全文搜索与文件索引
- 管理员面板与审计日志

## 技术栈

| 层级 | 技术 |
|------|------|
| 后端语言 | Go 1.26+ |
| Web 框架 | Fiber v2 |
| 数据库 | PostgreSQL + GORM |
| 文件存储 | 阿里云 OSS |
| 实时通信 | WebSocket (fasthttp/websocket) |
| 前端框架 | Vue 3 + Vue Router + Pinia |
| 构建工具 | Vite 8 |
| UI 组件 | Breeze（项目自建组件库） |
| 代码编辑器 | CodeMirror 6 |
| 图标 | Material Design Icons (unplugin-icons) |
| Lint | golangci-lint (Go) + ESLint (JS/Vue) |

## 项目结构

```
domus/
├── cmd/                        # CLI 入口
│   └── domus/main.go          # 程序主入口
│   └── root.go                 # CLI 命令定义 (start/stop/restart/status)
├── config/                     # 配置解析
│   └── config.go               # YAML 配置加载与默认值
├── internal/                   # 核心业务代码（不对外暴露）
│   ├── auth/                   # 认证模块
│   │   ├── cookie.go           #   会话 Cookie 管理
│   │   ├── crypto.go           #   加密/解密工具
│   │   ├── challenge.go        #   多因素认证挑战
│   │   └── totp.go             #   TOTP 两步验证
│   ├── handler/                # HTTP 路由与 WebSocket 实时入口
│   │   ├── handler.go          #   路由注册与依赖注入
│   │   ├── auth.go             #   登录/登出/验证
│   │   ├── account.go          #   用户资料与安全设置
│   │   ├── files.go            #   文件列表 / 搜索 / 文件命令
│   │   ├── upload.go           #   分片上传
│   │   ├── content.go          #   文本编辑、原始内容
│   │   ├── share.go            #   分享关系与共享访问
│   │   ├── jobs.go             #   后台作业查询与控制
│   │   ├── task.go             #   用户任务查询与控制
│   │   ├── workspace.go        #   工作区快照保存 / 读取
│   │   ├── indexer.go          #   全文搜索索引
│   │   ├── terminal.go         #   浏览器原始 TTY 会话
│   │   ├── admin.go            #   管理员用户管理
│   │   └── ws_handlers.go      #   仅实时订阅 / 会话类 WS 动作
│   ├── middleware/             # 中间件
│   │   └── middleware.go       #   认证校验、权限检查、WebSocket 升级
│   ├── model/                  # 数据模型与数据库
│   │   ├── db.go               #   数据库初始化与表结构管理
│   │   ├── user.go             #   用户模型（bcrypt 密码哈希）
│   │   ├── file.go             #   文件记录模型（全文检索向量）
│   │   ├── session.go          #   会话管理
│   │   ├── job.go              #   后台作业
│   │   ├── task.go             #   用户任务追踪
│   │   ├── workspace.go        #   工作区快照
│   │   ├── share.go            #   分享关系
│   │   └── audit.go            #   审计日志
│   ├── service/                # 业务逻辑服务
│   │   ├── dispatcher.go       #   作业队列分发器
│   │   └── email.go            #   邮件服务 (SMTP)
│   ├── store/                  # 外部存储抽象
│   │   └── oss.go              #   阿里云 OSS 客户端
│   ├── dofs/                   # OSS 加密文件的宿主机 FUSE 数据面
│   ├── terminal/               # 浏览器 TTY 会话管理
│   ├── workspace/              # Docker 工作区控制面与运行时
│   └── ws/                     # WebSocket 基础设施
│       ├── hub.go              #   连接中心、目录订阅
│       ├── conn.go             #   单连接处理
│       ├── router.go           #   消息路由
│       └── push.go             #   推送通知
├── shared/                     # 可复用的通用工具包
│   ├── bootstrap/              #   PID 文件与进程管理
│   ├── daemon/                 #   Unix 守护进程操作
│   ├── logger/                 #   结构化日志
│   ├── stats/                  #   服务器性能统计
│   └── version/                #   版本管理
├── frontend/                   # Vue 3 前端
│   ├── src/
│   │   ├── main.ts             #   前端入口
│   │   ├── router.ts           #   路由定义 (/ → PlasmaShell, /admin → AdminView)
│   │   ├── App.vue             #   根组件
│   │   ├── i18n/               #   中英文翻译字典
│   │   ├── components/
│   │   │   ├── breeze/         #   Breeze 自建 UI 组件库
│   │   │   ├── plasma/         #   Plasma 桌面环境组件 (Desktop, Window, Panel...)
│   │   │   ├── dolphin/        #   文件管理器
│   │   │   ├── kate/           #   文本/代码编辑器
│   │   │   ├── elisa/          #   音频播放器
│   │   │   ├── ark/            #   压缩包管理器
│   │   │   ├── kfontview/      #   字体预览器
│   │   │   ├── konsole/        #   终端模拟器
│   │   │   ├── notebook/       #   笔记应用
│   │   │   ├── LoginPage.vue   #   登录页
│   │   │   ├── ViewerApp.vue   #   文件内容查看器
│   │   │   ├── GlobalDialog.vue      # 全局对话框
│   │   │   └── TranscodeDialog.vue   # 转码对话框
│   │   ├── composables/        #   Vue 组合式函数
│   │   │   ├── useApi.ts       #     Axios 实例与拦截器
│   │   │   ├── useWebSocket.ts #     WebSocket 连接管理
│   │   │   ├── useI18n.ts      #     国际化 (中/英)
│   │   │   ├── useKeyboard.ts  #     键盘快捷键
│   │   │   ├── useCodeMirror.ts#     代码编辑器集成
│   │   │   ├── useFileIcon.ts  #     文件类型图标映射
│   │   │   └── ...             #     其他 composables
│   │   ├── stores/             #   Pinia 状态管理
│   │   │   ├── auth.ts         #     用户认证状态
│   │   │   ├── fileSystem.ts   #     目录状态与文件命令
│   │   │   ├── upload.ts       #     上传会话与进度
│   │   │   ├── tasks.ts        #     任务面板聚合视图
│   │   │   ├── pendingOps.ts   #     离线重试队列
│   │   │   └── windowManager.ts#     窗口管理器
│   │   └── views/
│   │       ├── PlasmaShell.vue #   主桌面视图
│   │       └── AdminView.vue   #   管理员面板
│   ├── eslint.config.js
│   ├── vite.config.js
│   └── package.json
├── config.example.yaml         # 配置文件模板
├── Makefile                    # 构建脚本
├── VERSION                     # 版本号 (当前 0.1.0)
├── .golangci.yml               # Go lint 配置
└── .gitignore
```

## 开发环境搭建

### 前置依赖

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Go | 1.26+ | 后端编译 |
| Node.js | 20+ | 前端构建与 Playwright E2E |
| PostgreSQL | 14+ | 数据库 |
| FUSE 3 | - | DOFS 挂载，需 `/dev/fuse` 与 `fusermount3` |
| Docker Engine | - | 每用户可复用 Linux 工作区 |
| 阿里云 OSS | - | 文件存储（需配置 Access Key） |
| golangci-lint | - | Go 代码检查（可选） |

### 步骤

1. **克隆仓库**

```bash
git clone <repo-url> && cd domus
```

2. **配置数据库**

确保 PostgreSQL 运行中，Domus 会在首次启动时自动创建数据库。

3. **创建配置文件**

```bash
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，填写必要的配置项：
- `oss.*` — 阿里云 OSS 凭证与 Bucket
- `database.*` — PostgreSQL 连接信息
- `smtp.*` — 邮箱验证（可选）

可选：开发环境可额外创建 `config.dev.yaml`，只填写需要覆盖的字段（例如本机数据库名、端口等）。默认启动时会先读取 `config.yaml`，再用 `config.dev.yaml` 覆盖；如果使用 `-c xxx.yaml` 显式指定配置文件，则不会自动读取 `config.dev.yaml`。

> `session_secret` 和 `encryption_secret` 会在首次启动时自动生成，无需手动填写。

4. **安装前端依赖**

```bash
cd frontend && npm install && cd ..
```

5. **启动开发环境**

先确认 `/etc/fuse.conf` 有未注释的 `user_allow_other`，当前用户可访问 Docker
Unix socket。然后用一条命令启动完整开发栈：

```bash
# DOFS + Workspace Manager + Web + Vite
make dev
```

`make dev` 会构建 Workspace 镜像，并在 `tmp/dev` 下生成权限为 `0600` 的合并
配置和本地运行状态；Ctrl+C 会按 Frontend、Web、Workspace、DOFS 的顺序关闭。
后端默认端口由 `config.yaml` 决定，前端开发服务器默认使用 `8089`，并自动注入
正确的后端 API 地址。只调试 Web 且外部两项服务已经运行时可使用 `make dev-web`，
它不会回退到旧执行架构。本地四个子进程使用当前开发者
账号；生产环境仍必须使用 systemd 单元中相互隔离的 `domus` 与
`domus-workspace` 服务账号。

> 首次启动时，如果库里还没有用户，系统会自动创建 root 用户；初始密码必须由部署者提供（推荐 `server.root_bootstrap_password_file` 指向 0600 secret 文件，也可使用环境变量 `DOMUS_ROOT_BOOTSTRAP_PASSWORD`）。

真实浏览器 E2E 使用 Playwright，并复用同一套本地配置、PostgreSQL 与 OSS：

```bash
make test-e2e
```

当前用例覆盖登录、浏览器加密上传、OSS 分片写入、文本解密读回，以及 PDF 经
DOFS + 用户 Workspace 生成服务端缩略图、缩略图解密显示、PDF 原件解密预览和
缓存 Range 响应、派生文件级联清理；终端用例还会验证真实容器会话及 resize
消息不会形成反馈环。默认登录 `root` 并读取 `tmp/dev/root-bootstrap-password`；
密码已变更时可设置 `DOMUS_E2E_PASSWORD`，已有 `make dev` 实例可通过
`E2E_REUSE_SERVERS=1 make test-e2e` 复用。

Playwright 始终使用隔离的临时浏览器 profile，并屏蔽测试窗口/标签状态向同账号
交互式桌面的保存和广播；上传、OSS、任务、DOFS 和 Workspace 容器仍是真实链路。
需要针对本机系统 Chrome 做 headed 稳定性回归时可运行：

```bash
DOMUS_E2E_BROWSER_EXECUTABLE=/opt/google/chrome/chrome \
DOMUS_E2E_HEADED=1 \
DOMUS_E2E_ENABLE_ZERO_COPY=1 \
DOMUS_E2E_PREVIEW_STABILITY_MS=30000 \
make test-e2e
```

E2E 使用真实外部资源，因此不并入普通 `make test`。

### 构建

```bash
make build          # Linux 二进制
make build-win      # Windows 二进制
make build-prod     # 压缩二进制 (需要 UPX)
```

前端生产构建：

```bash
cd frontend && npm run build    # 输出到 frontend/dist/
```

### 实例维护命令

以下命令都会直接操作 `config.yaml` 指定的 PostgreSQL 数据库和 OSS bucket；其中 `restore` 与 `reset` 属于危险操作。

#### 备份实例

```bash
domus backup -c config.yaml -o backup-20260419.tar.gz
```

- 执行前必须先停止服务，否则命令会拒绝运行
- 备份内容包含 `database.sql`、`manifest.json` 和 `objects/`
- `-o` 可以指向目录，也可以指向 `.tar.gz` / `.tgz` 归档文件
- 若未显式传入 `-o`，会默认生成 `domus-backup-YYYYMMDD-HHMMSS.tar.gz`
- 备份数据库依赖本机可用的 `pg_dump`

#### 恢复实例

```bash
domus restore -c config.yaml -i backup-20260419.tar.gz --yes
```

- 执行前必须先停止服务，否则命令会拒绝运行
- `restore` 会先清空目标数据库和整个 bucket，再导入备份内容，因此必须带 `--yes`
- 恢复数据库依赖本机可用的 `psql`
- 恢复前会校验备份中的 `server.encryption_secret` 指纹；若与当前配置不一致，命令会拒绝执行，避免恢复后文件无法解密
- 恢复完成后，重新执行 `domus start -c config.yaml` 即可拉起实例

#### 重置实例

```bash
domus reset -c config.yaml --yes
```

- 执行前请先停止服务，否则命令会拒绝运行
- `reset` 会清空 `config.yaml` 指定的 PostgreSQL 数据库，并清空配置中的整个 OSS bucket
- `reset` 不会立即创建 `root`；下一次 `domus start` 时会走首次启动逻辑自动初始化
- 首次启动所需的 root 初始密码仍需通过 `server.root_bootstrap_password_file` 或 `DOMUS_ROOT_BOOTSTRAP_PASSWORD` 提供

## 开发工作流

### 分支

- `master` — 主开发分支
- 功能分支从 `master` 切出，完成后合并回 `master`

### 运行 Lint

```bash
make lint
```

等价于：

```bash
golangci-lint run                # Go: errcheck, govet, staticcheck, unused 等
cd frontend && npx eslint .      # JS/Vue: ESLint + eslint-plugin-vue
```

### 运行测试

```bash
go test ./...
```

### 依赖管理

```bash
make tidy                        # go mod tidy
cd frontend && npm install       # 前端依赖
```

## 接口文档

- `docs/api/README.md` — 项目的主接口文档入口，统一组织 HTTP 接口与 WebSocket 协议
- `API.md` — 根目录兼容入口，便于从仓库首页快速跳转
- `docs/dofs.md` / `docs/workspace.md` — 用户文件数据面与隔离执行面的架构
- `docs/dofs-production.md` / `docs/workspace-production.md` — Linux 生产部署与运维边界

若后端路由或 WebSocket 动作发生变化，请同步更新 `docs/api/` 下对应文档。

## 代码规范

### Go 后端

- 遵循 `.golangci.yml` 中定义的 lint 规则（errcheck, govet, ineffassign, staticcheck, unused）
- 使用 `gofmt` 格式化代码
- 业务代码放在 `internal/` 下，不对外暴露
- 通用工具放在 `shared/` 下
- Handler 层只做请求解析与响应，业务逻辑放在 `service/` 层
- 数据库操作封装在 `model/` 层
- 权限使用位掩码：`PermRead(1)`, `PermUpload(2)`, `PermEdit(4)`, `PermDelete(8)`
- **不造新轮子**：新增功能时必须复用已有的代码路径。如果发现现有流程不满足需求，应先改进现有流程而非另起炉灶
- **用户文件区操作规范**：普通后端逻辑不得绕过文件模型直接操作存储池。服务端预览、转码和容器命令只能通过用户对应的 DOFS 挂载读写；DOFS 负责加密、generation CAS、数据库记录和恢复语义

### Vue 前端

- 使用 Vue 3 Composition API (`<script setup>`)
- 状态管理使用 Pinia stores（`stores/` 目录）
- 可复用逻辑提取为 composables（`composables/` 目录，`use` 前缀）
- UI 组件使用项目自建的 Breeze 组件库（`components/breeze/`），不使用外部 UI 库
- 图标使用 Material Design Icons，通过 `unplugin-icons` 按需加载，格式：`~icons/mdi/icon-name`
- 国际化支持中英文，翻译定义在 `frontend/src/i18n/` 中，由 `useI18n.ts` 读取
- ESLint 规则见 `frontend/eslint.config.js`，关闭了部分 Vue 风格规则以保持灵活性

### 前端组件命名

前端应用组件以 KDE Plasma 桌面应用命名：

| 目录 | 对应功能 | KDE 原型 |
|------|---------|---------|
| `plasma/` | 桌面环境（窗口、面板、托盘） | Plasma Desktop |
| `dolphin/` | 文件管理器 | Dolphin |
| `kate/` | 文本/代码编辑器 | Kate |
| `elisa/` | 音频播放器 | Elisa |
| `ark/` | 压缩包管理 | Ark |
| `kfontview/` | 字体预览 | KFontView |
| `konsole/` | 终端模拟器 | Konsole |
| `notebook/` | 笔记应用 | - |
| `breeze/` | UI 组件库 | Breeze 主题 |

## 提交规范

提交信息使用以下前缀格式：

```
<类型>: <描述>
```

| 前缀 | 用途 | 示例 |
|------|------|------|
| `ADD:` | 新功能 | `ADD: 全文搜索与文件索引` |
| `OPT:` | 优化/重构 | `OPT: 收紧 HTTP / WS 职责边界` |
| `FIX:` | Bug 修复 | `FIX: 修复绕过数据库直接访问OSS的安全问题` |

- 描述部分使用中文
- 简明扼要地说明变更的内容与目的
- 一次提交聚焦于一个逻辑变更

## 架构指南

### 后端请求处理流程

```
HTTP 请求
  → Fiber 路由
    → middleware（认证 + 权限检查）
      → handler（请求解析 + 响应）
        → service（业务逻辑）
          → model（数据库操作）
          → store（OSS 文件存储）
```

终端和服务端媒体任务统一走 Linux 受限执行面：

```text
authenticated handler / terminal manager
  → permission-protected workspace Unix socket
    → per-user Docker container
      → exact /workspace bind
        → host DOFS FUSE mount
          → encrypted OSS objects
```

### HTTP / WebSocket 边界

- **HTTP 负责普通 query / command**：文件列表、搜索、创建目录、重命名、复制、移动、删除、文本保存、用户资料、安全设置、分享、任务列表、工作区持久化、管理员操作等，都走普通 HTTP API。
- **WebSocket 负责实时性**：目录订阅推送、`task.update` 进度广播、终端会话输入输出、工作区事件转发、客户端上传任务上报。
- **设计原则**：不要为同一业务同时维护一套 HTTP 和一套 WS 命令接口；如果一个动作不依赖长连接实时语义，就应该归入 HTTP。

### WebSocket 通信

```
客户端
  → ws/conn.go（连接管理）
    → ws/router.go（动作路由）
      → handler/ws_handlers.go（仅 session / subscribe / relay / report）
        → ws/push.go（dir.changed / task.update / session.* 推送）
          → ws/hub.go（广播 / 目录订阅）
```

### 认证流程

1. 用户提交用户名 + 密码
2. 后端验证密码（bcrypt）
3. 如启用 TOTP，返回挑战要求二次验证
4. 验证通过后创建 Session，设置 Cookie
5. 后续请求通过 middleware 校验 Session

### 数据库表

| 表名 | 用途 |
|------|------|
| `users` | 用户账户（密码哈希、邮箱、TOTP） |
| `files` | 文件元数据（路径、大小、类型、全文检索向量；回收站文件也在此表中，以 `__trash__` 路径空间表示） |
| `sessions` | 用户会话（带过期时间） |
| `jobs` | 后台作业追踪（转码） |
| `tasks` | 用户任务进度 |
| `workspace_states` | 工作区布局快照 |
| `shares` | 文件分享关系 |
| `audit_logs` | 操作审计日志 |

### 安全要点

- 密码使用 bcrypt 哈希存储
- 文件加密使用用户独立密钥（由 `encryption_secret` + 用户 ID 派生）
- 权限控制使用位掩码组合（Read=1, Upload=2, Edit=4, Delete=8）
- `session_secret` 和 `encryption_secret` 首次启动自动生成，务必妥善保管 `config.yaml`
- 首次启动若需要自动初始化 `root`，请通过 `server.root_bootstrap_password_file` 或 `DOMUS_ROOT_BOOTSTRAP_PASSWORD` 提供初始密码，避免从日志泄露凭据
- CORS 来源需在配置中显式指定
