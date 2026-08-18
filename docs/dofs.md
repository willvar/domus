# Domus 的 DOFS 文件数据面

Domus 的唯一文件架构建立在独立模块
[`github.com/willvar/dofs`](https://github.com/willvar/dofs) 上。每个 Domus 用户对应一个
以不可变用户 UUID 命名的 DOFS namespace；DOFS 是文件身份、目录层级、inode、大小、
generation、配额、包装密钥和对象键的唯一权威来源。

旧的 `files` 路径表、逻辑路径对象键、服务端正文上传和内容 diff 路由都不再是生产
路径，也没有双读回退。检测到旧 `files` 表时，新版本会拒绝启动并要求显式迁移或
重置，不会在启动迁移中静默删除数据。

## 三条数据路径

### 浏览器上传

```text
浏览器 File/Blob
  -> Web Worker 在浏览器内生成 DEK、分块 AES-256-GCM 加密
  -> 浏览器用 Domus 签发的 presigned URL 直接 PUT 密文到 OSS
  -> Domus 从 OSS ListParts/HeadObject 取得权威分片与大小
  -> DOFS 校验密文几何和文件头
  -> generation CAS 原子发布 inode
```

Domus HTTP 只接收小型 JSON 控制消息：目标路径、明文长度、DEK、上传 ID、媒体元数据
和完成确认。它不接收、缓存或转发文件正文；HTTP 全局请求体上限为 1 MiB，上传接口
还使用三套互斥的封闭字段集合，旧 `content`、`parts`、`search_text` 等字段会被拒绝。

这是“应用层加密 + 服务器托管密钥”，不是零知识加密。Domus 控制面会在内存中拿到
DEK，以便包装密钥并让 DOFS/预览/转码读取文件；保证的是文件正文不经过应用服务器、
OSS 只保存密文，而不是服务器永远无法解密。

### 浏览器读取与预览

普通浏览器读取由 `GET /file/access` 返回短期 OSS 下载 URL 和该文件的解密信息，
浏览器或 Service Worker 直接下载密文并在本地解密。目录、搜索、分享和任务接口只读
DOFS 元数据及 Domus 产品元数据。

公开头像也不例外：`GET /user/avatar/:username` 只返回短期 OSS URL、公开头像 DEK 和
密文几何，浏览器直读 OSS 密文并生成本地 `blob:` URL。Domus 不代理头像图片正文。

图片/视频/PDF 若无法在浏览器生成缩略图，Domus 会在该用户的复用容器中执行固定的
预览脚本。容器只看到：

```text
/workspace -> /var/lib/domus/dofs/mounts/<user-id> (该用户的 FUSE)
```

脚本通过普通文件 I/O 读取原件、写入隐藏派生文件；DOFS 完成解密、加密和 generation
发布。转码采用同一路径。容器没有 OSS 凭证、主密钥、DOFS 控制 socket 或 Docker
socket。

### 终端和应用 I/O

浏览器终端连接到用户复用 Docker 容器中的真实 TTY。`open/read/write/fsync/rename`
等操作都落到 `/workspace` 的 DOFS FUSE，因此终端、预览、转码和 HTTP 文件界面观察
的是同一套 inode/generation，不存在第二套文件数据源。

## 元数据职责

DOFS 持有：

- namespace、稳定 inode 和父子目录关系；
- 文件大小、mode、当前 generation、不可变对象键和包装 DEK；
- writer lease、外部直传 reservation、事件游标与配额使用量。

Domus PostgreSQL 只补充产品字段：

- `domus_file_metadata`：MIME、内容哈希、媒体尺寸、缩略图 inode；
- `domus_file_uploads`：上传任务、断点续传、客户端实例和分享写入者状态；
- 用户、会话、分享、任务、桌面布局和审计信息。

缩略图和分享都绑定稳定 inode，不靠易变路径授权。产品元数据带 generation；源文件
被覆盖后，旧缩略图元数据不会附着到新内容。

## SQLite 默认、PostgreSQL 可选

```yaml
dofs:
  metadata:
    driver: sqlite
    sqlite:
      path: /var/lib/domus/dofs/state/metadata.sqlite
      busy_timeout_seconds: 5
      max_open_connections: 8
```

SQLite 是单 Linux 主机的生产默认值。Web、DOFS Manager 和各 namespace 挂载进程
可并发打开同一个本地 SQLite 文件；WAL、立即写事务和持久 heartbeat lease 负责串行
化写入及排他挂载。数据库文件不能放在 NFS 或被多主机共享。

`driver: postgres` 复用 Domus 的 `database.*` 连接，适合多个进程/主机共享元数据。
它用事务锁和 PostgreSQL session advisory lock。PostgreSQL 仍不能自动迁移宿主机本地
的明文 writeback WAL；跨主机调度需要额外 fencing 和 drain 协议。

## 对象和加密

物理对象位于不可变前缀：

```text
.dofs/v1/namespaces/<user-id>/objects/<inode>/<generation>-<transaction>.dofs
```

逻辑路径永远不会成为对象键。每个文件有独立 DEK，namespace KEK 由 Domus 的
`server.encryption_secret` 包装。读取按 64 KiB 明文块做 OSS Range GET 和 AES-GCM
认证；写入先落到受保护的稀疏缓存/WAL，再流式生成不可变密文对象，最后通过
generation CAS 发布。

DOFS state 可能包含明文块，必须位于权限 `0700` 的加密本地卷。不能把它放入 OSS、
容器镜像层、NFS 或未加密备份。

## 一致性与恢复

- 同一 namespace 同时只允许一个 writable FUSE owner；控制面直传通过事务和
  generation CAS 与它安全竞争。
- `fsync` 后的 prepared WAL 会在重新挂载时恢复；未提交脏状态被丢弃。
- rename 只改 inode 目录元数据，不复制对象。
- copy 使用 OSS 侧密文复制，再发布独立 inode/generation，不把明文拉回 Domus。
- unlink/replace 先留下 tombstone；打开的 FUSE 句柄释放后再清理对应 generation。
- 被覆盖文件的历史 generation 为保护旧读句柄暂时保留；长期覆盖文件的自动保留/
  GC 策略尚未包含在本次迭代。

## 当前边界

- 挂载和工作区仅支持 Linux/FUSE；Go 的其余元数据、加密和对象存储代码仍可跨平台
  编译测试。
- DOFS 不是块设备，不适合数据库、包管理器状态、Unix socket 或高频构建缓存。
- 不支持 symlink、hard link、特殊节点、mmap 数据库语义和 POSIX advisory lock。
- 用户容器共享宿主内核；面对完全恶意的多租户代码，应增加 gVisor/Kata/VM 等隔离层。

生产部署、socket 权限、服务顺序和恢复操作见
[DOFS 生产部署](dofs-production.md)；通用独立接入方式见 sibling `dofs` 仓库 README。
