# 贡献指南

感谢你对 Zephyr 项目的关注！本文档将帮助你快速了解项目结构、搭建开发环境并参与贡献。

## 目录

- [项目概述](#项目概述)
- [技术栈](#技术栈)
- [项目结构](#项目结构)
- [开发环境搭建](#开发环境搭建)
- [开发工作流](#开发工作流)
- [代码规范](#代码规范)
- [提交规范](#提交规范)
- [架构指南](#架构指南)

---

## 项目概述

Zephyr 是一个全栈云文件管理平台，提供 Plasma 桌面风格的 Web UI。核心功能包括：

- 用户认证（密码 + TOTP 两步验证 + 邮箱验证）
- 文件管理（上传、下载、搜索、收藏、回收站）
- 媒体转码与缩略图生成（基于 FFmpeg）
- WebSocket 实时通信
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
zephyr/
├── cmd/                        # CLI 入口
│   └── zephyr/main.go          # 程序主入口
│   └── root.go                 # CLI 命令定义 (start/stop/restart/status)
├── config/                     # 配置解析
│   └── config.go               # YAML 配置加载与默认值
├── internal/                   # 核心业务代码（不对外暴露）
│   ├── auth/                   # 认证模块
│   │   ├── cookie.go           #   会话 Cookie 管理
│   │   ├── crypto.go           #   加密/解密工具
│   │   ├── challenge.go        #   多因素认证挑战
│   │   └── totp.go             #   TOTP 两步验证
│   ├── handler/                # HTTP 路由处理器
│   │   ├── handler.go          #   路由注册与依赖注入
│   │   ├── auth.go             #   登录/登出/验证
│   │   ├── account.go          #   用户资料与安全设置
│   │   ├── files.go            #   文件 CRUD
│   │   ├── upload.go           #   分片上传
│   │   ├── download.go         #   文件下载与流媒体
│   │   ├── content.go          #   文本编辑、原始内容
│   │   ├── thumbnail.go        #   缩略图生成
│   │   ├── transcode.go        #   媒体格式转换
│   │   ├── trash.go            #   回收站（软删除与恢复）
│   │   ├── jobs.go             #   异步任务管理
│   │   ├── indexer.go          #   全文搜索索引
│   │   ├── bookmarks.go        #   用户收藏
│   │   ├── admin.go            #   管理员用户管理
│   │   └── ws_handlers.go      #   WebSocket 消息处理
│   ├── middleware/             # 中间件
│   │   └── middleware.go       #   认证校验、权限检查、WebSocket 升级
│   ├── model/                  # 数据模型与数据库
│   │   ├── db.go               #   数据库初始化与表结构管理
│   │   ├── user.go             #   用户模型（bcrypt 密码哈希）
│   │   ├── file.go             #   文件记录模型（全文检索向量）
│   │   ├── session.go          #   会话管理
│   │   ├── job.go              #   后台作业
│   │   ├── task.go             #   用户任务追踪
│   │   └── audit.go            #   审计日志
│   ├── service/                # 业务逻辑服务
│   │   ├── dispatcher.go       #   作业队列分发器
│   │   ├── transcode.go        #   FFmpeg 媒体转码
│   │   └── email.go            #   邮件服务 (SMTP)
│   ├── store/                  # 外部存储抽象
│   │   └── oss.go              #   阿里云 OSS 客户端
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
│   │   ├── main.js             #   前端入口
│   │   ├── router.js           #   路由定义 (/ → PlasmaShell, /admin → AdminView)
│   │   ├── App.vue             #   根组件
│   │   ├── components/
│   │   │   ├── breeze/         #   Breeze 自建 UI 组件库
│   │   │   ├── plasma/         #   Plasma 桌面环境组件 (Desktop, Window, Panel...)
│   │   │   ├── dolphin/        #   文件管理器
│   │   │   ├── kate/           #   文本/代码编辑器
│   │   │   ├── elisa/          #   音频播放器
│   │   │   ├── okular/         #   文档查看器
│   │   │   ├── ark/            #   压缩包管理器
│   │   │   ├── kfontview/      #   字体预览器
│   │   │   ├── notebook/       #   笔记应用
│   │   │   ├── LoginPage.vue   #   登录页
│   │   │   ├── ViewerApp.vue   #   文件内容查看器
│   │   │   ├── GlobalDialog.vue      # 全局对话框
│   │   │   └── TranscodeDialog.vue   # 转码对话框
│   │   ├── composables/        #   Vue 组合式函数
│   │   │   ├── useApi.js       #     Axios 实例与拦截器
│   │   │   ├── useWebSocket.js #     WebSocket 连接管理
│   │   │   ├── useI18n.js      #     国际化 (中/英)
│   │   │   ├── useKeyboard.js  #     键盘快捷键
│   │   │   ├── useCodeMirror.js#     代码编辑器集成
│   │   │   ├── useFileIcon.js  #     文件类型图标映射
│   │   │   └── ...             #     其他 composables
│   │   ├── stores/             #   Pinia 状态管理
│   │   │   ├── auth.js         #     用户认证状态
│   │   │   ├── fileSystem.js   #     文件树与目录状态
│   │   │   ├── upload.js       #     上传队列与进度
│   │   │   ├── windowManager.js#     窗口管理器
│   │   │   ├── jobs.js         #     后台作业追踪
│   │   │   ├── operations.js   #     撤销/重做操作
│   │   │   └── pendingOps.js   #     操作队列管理
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
| Node.js | 18+ | 前端构建 |
| PostgreSQL | 14+ | 数据库 |
| FFmpeg / FFprobe | - | 媒体转码（可选，不用则跳过） |
| 阿里云 OSS | - | 文件存储（需配置 Access Key） |
| golangci-lint | - | Go 代码检查（可选） |

### 步骤

1. **克隆仓库**

```bash
git clone <repo-url> && cd zephyr
```

2. **配置数据库**

确保 PostgreSQL 运行中，Zephyr 会在首次启动时自动创建数据库。

3. **创建配置文件**

```bash
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，填写必要的配置项：
- `oss.*` — 阿里云 OSS 凭证与 Bucket
- `database.*` — PostgreSQL 连接信息
- `transcode.*` — FFmpeg 路径（如需媒体转码）
- `smtp.*` — 邮箱验证（可选）

> `session_secret` 和 `encryption_secret` 会在首次启动时自动生成，无需手动填写。

4. **安装前端依赖**

```bash
cd frontend && npm install && cd ..
```

5. **启动开发环境**

分别启动后端和前端开发服务器：

```bash
# 终端 1：后端
make dev

# 终端 2：前端 (Vite dev server, 端口 5173)
cd frontend && npm run dev
```

后端默认端口 `8080`，前端开发服务器端口 `5173`。

> 首次启动时，系统会自动创建 root 用户并在终端输出随机密码，请注意保存。

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
- **用户文件区操作规范**：后端不直接操作用户文件区，用户区的读写通常由前端指令驱动。当后端确实需要操作用户文件时（如 SSH known_hosts 自动写入），必须沿照前端操作文件的完整流程，包括加密、数据库记录、目录通知。禁止直接操作存储池

### Vue 前端

- 使用 Vue 3 Composition API (`<script setup>`)
- 状态管理使用 Pinia stores（`stores/` 目录）
- 可复用逻辑提取为 composables（`composables/` 目录，`use` 前缀）
- UI 组件使用项目自建的 Breeze 组件库（`components/breeze/`），不使用外部 UI 库
- 图标使用 Material Design Icons，通过 `unplugin-icons` 按需加载，格式：`~icons/mdi/icon-name`
- 国际化支持中英文，翻译定义在 `useI18n.js` 中
- ESLint 规则见 `frontend/eslint.config.js`，关闭了部分 Vue 风格规则以保持灵活性

### 前端组件命名

前端应用组件以 KDE Plasma 桌面应用命名：

| 目录 | 对应功能 | KDE 原型 |
|------|---------|---------|
| `plasma/` | 桌面环境（窗口、面板、托盘） | Plasma Desktop |
| `dolphin/` | 文件管理器 | Dolphin |
| `kate/` | 文本/代码编辑器 | Kate |
| `elisa/` | 音频播放器 | Elisa |
| `okular/` | 文档查看器 | Okular |
| `ark/` | 压缩包管理 | Ark |
| `kfontview/` | 字体预览 | KFontView |
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
| `OPT:` | 优化/重构 | `OPT: 前端HTTP通信全面迁移至WebSocket` |
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

### WebSocket 通信

```
客户端
  → ws/conn.go（连接管理）
    → ws/router.go（消息路由）
      → handler/ws_handlers.go（业务处理）
        → ws/push.go（推送响应）
          → ws/hub.go（广播 / 目录订阅）
```

### 作业调度

```
handler 发起作业请求
  → service/dispatcher.go（作业队列分发）
    → service/transcode.go（FFmpeg 转码）
    → thumbnail 生成
    → OSS 上传
  → model/job.go（状态追踪）
  → ws/push.go（实时进度推送）
```

**Job vs Task**：`Job` 是后台调度的内部工作单元（如转码、缩略图生成），由 `dispatcher` 消费，不直接暴露给前端。`Task` 是面向用户的进度追踪记录，显示在前端任务面板中。一个 Job 可通过 `TaskID` 字段关联到一个 Task，Job 的状态/进度变化会通过 `cascadeToTask` 自动同步到对应的 Task 并推送给客户端。

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
| `files` | 文件元数据（路径、大小、类型、全文检索向量） |
| `sessions` | 用户会话（带过期时间） |
| `trash` | 回收站（软删除文件） |
| `bookmarks` | 用户收藏 |
| `jobs` | 后台作业追踪（转码、缩略图） |
| `tasks` | 用户任务进度 |
| `audit_logs` | 操作审计日志 |

### 安全要点

- 密码使用 bcrypt 哈希存储
- 文件加密使用用户独立密钥（由 `encryption_secret` + 用户 ID 派生）
- 权限控制使用位掩码组合（Read=1, Upload=2, Edit=4, Delete=8）
- `session_secret` 和 `encryption_secret` 首次启动自动生成，务必妥善保管 `config.yaml`
- CORS 来源需在配置中显式指定
