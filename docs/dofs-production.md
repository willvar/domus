# DOFS 生产部署

Domus 的受支持生产形态是单台 Linux 主机上的三个独立服务：

```text
domus-dofs.service       FUSE、用户密钥、OSS、writeback/WAL、挂载控制
        ↓ ready
domus-workspace.service  Docker socket、每用户容器、Exec/TTY
        ↓ ready
domus.service            HTTP/WS 控制面、浏览器直传签名、产品元数据
```

DOFS 使用独立 `github.com/willvar/dofs` 模块，但 Domus 仍运行自己的多用户 Mount
Manager：它负责把 Domus 用户 UUID 映射成 namespace、持久化 desired mounts，并通过
权限受限的 Unix socket 向 Workspace Manager 交付精确挂载点。

## 主机要求

- Linux、`/dev/fuse`、FUSE 3 和 `fusermount3`；
- rootful Docker Engine，并保证 dockerd 能看到宿主 mount namespace；
- PostgreSQL（Domus 产品数据始终需要）；
- 专用 S3-compatible bucket 和 HTTPS endpoint；
- DOFS state 所在的加密本地卷；
- systemd 及仓库内的三个 unit。

`dofs.allow_other: true` 时，`/etc/fuse.conf` 必须有未注释的：

```text
user_allow_other
```

DOFS 启动前会验证 `/dev/fuse`、`fusermount3` 和该配置。合成 UID/GID 仍通过内核
`default_permissions` 限制访问；`allow_other` 只是允许 dockerd/容器 UID 经过 FUSE，
不是把共享 mount root 变成公共目录。

## 配置

单主机推荐 SQLite：

```yaml
dofs:
  metadata:
    driver: sqlite
    sqlite:
      path: /var/lib/domus/dofs/state/metadata.sqlite
      busy_timeout_seconds: 5
      max_open_connections: 8
  mount_root: /var/lib/domus/dofs/mounts
  state_root: /var/lib/domus/dofs/state
  control_socket: /run/domus/dofs.sock
  socket_group: domus
  uid: 1000
  gid: 1000
  allow_other: true
  writable: true
  max_mounts: 32
  reconcile_interval_seconds: 30
  mount_timeout_seconds: 60
  shutdown_timeout_seconds: 30

workspace:
  control_socket: /run/domus-workspace/control.sock
```

SQLite 文件必须在该主机的本地文件系统，不能放在 NFS/SMB，也不能由另一台主机同时
打开。Domus Web 和 DOFS Manager 会并发访问它，这是受支持的单主机模式；SQLite WAL
和 namespace 事务负责协调。

只有需要多主机共享 DOFS 元数据时才改为：

```yaml
dofs:
  metadata:
    driver: postgres
```

这会复用 `database.*`。生产 PostgreSQL 应启用验证服务器身份的 TLS。PostgreSQL
advisory lease 能阻止两个 writable mount，但 host-local 明文 WAL 仍要求调度器先 fence
旧主机，当前版本不提供自动跨主机 failover。

OSS 必须允许浏览器 origin 对 presigned URL 执行 `PUT`，并允许客户端读取下载所需的
`GET`/`Range` 响应。浏览器不依赖暴露上传 ETag：Domus 使用服务器端 `ListParts` 取得
权威 ETag。bucket 应专供该 Domus 实例；备份、恢复和 reset 都以整个配置 bucket 为
操作边界。

## 文件系统布局

```text
/var/lib/domus/dofs/mounts/<user-id>       明文 FUSE 挂载
/var/lib/domus/dofs/state/metadata.sqlite  SQLite 权威元数据（默认）
/var/lib/domus/dofs/state/users/<user-id>  稀疏明文缓存与恢复 WAL
/var/lib/domus/dofs/state/desired/         持久 desired mount 标记
/run/domus/dofs.sock                       本地挂载控制 API
```

`state_root` 必须在加密本地卷上且保持 `0700`。稀疏缓存只为已读或已改区间分配块，
但发布完整 generation 时仍可能需要接近文件逻辑大小的临时空间；应监控 inode、容量和
WAL 增长。当前没有每用户本地缓存配额。

`mount_root` 本身保持 `0700`，dockerd 以 root 只 bind 一个精确子目录。任何服务都不应
bind 整个 mount root；用户容器也不能得到 `/dev/fuse`、DOFS socket、OSS 凭证或密钥。

## systemd 安装与 mount 可见性

安装：

```text
deploy/systemd/domus.sysusers.conf      -> /usr/lib/sysusers.d/domus.conf
deploy/systemd/domus-dofs.service       -> /etc/systemd/system/domus-dofs.service
deploy/systemd/domus-workspace.service  -> /etc/systemd/system/domus-workspace.service
deploy/systemd/domus.service            -> /etc/systemd/system/domus.service
deploy/workspace.example.yaml           -> /etc/domus/workspace.yaml
```

配置文件权限：

```text
/etc/domus/config.yaml     domus:domus                    0600
/etc/domus/workspace.yaml domus-workspace:domus-workspace 0600
```

然后运行：

```bash
systemd-sysusers
systemd-analyze verify /etc/systemd/system/domus-dofs.service \
  /etc/systemd/system/domus-workspace.service \
  /etc/systemd/system/domus.service
systemctl daemon-reload
systemctl enable --now docker domus-dofs domus-workspace domus
```

`domus-dofs.service` 必须位于宿主 mount namespace。`PrivateTmp`、`ProtectSystem`、
`ProtectHome`、`RootDirectory`、`BindPaths`、`InaccessiblePaths` 等指令即使同时设置
`PrivateMounts=no`，仍可能创建 filesystem namespace，使 dockerd 看不到 FUSE。仓库 unit
因此明确不使用这些指令。`NoNewPrivileges` 也不能用于需要 setuid `fusermount3` 的
非 root DOFS 进程。

DOFS unit 使用 `Type=notify`，只有数据库/OSS/主机预检、管理器锁和 Unix listener 都
准备好才发 ready。Workspace unit 随后检查 DOFS、Docker 和镜像，最后 Web 再检查
Workspace 协议版本。没有 `workspace.enabled` 或旧执行回退。

## 服务账号和 socket

- `domus`：持有数据库/OSS/包装密钥、FUSE 和 Web 控制面；
- `domus-workspace`：持有 Docker socket，但没有数据库、OSS、会话或密钥配置；
- 用户容器：只有非 root UID/GID 和精确 `/workspace` bind。

`/run/domus/dofs.sock` 默认 `0600`；配置 `socket_group: domus` 后为 `0660`。能访问它的
进程可以请求任意用户的明文挂载，因此 socket 和其父目录都不能进入用户容器。

常用命令：

```bash
domus dofs health -c /etc/domus/config.yaml
domus dofs ensure -c /etc/domus/config.yaml --user root
domus dofs status -c /etc/domus/config.yaml --json
domus dofs unmount -c /etc/domus/config.yaml --user-id <uuid>
domus dofs reconcile -c /etc/domus/config.yaml
```

readiness 在任一 desired mount pending/failed、发生 foreign mount、lease loss 或清理失败
时保持 degraded。不要用无限 liveness 重启掩盖 readiness 告警。

## 恢复、升级与删除

Mount Manager 对 `state_root` 持有本机 `flock`。正常关闭保留 desired markers，重启后
恢复挂载。每次挂载生成新的 `mount_id`；Workspace Manager 发现 ID 改变会重建容器，
避免继续使用 Docker 私有 bind 中已经断开的旧 FUSE。

显式卸载顺序：

```text
取消/等待用户命令
  -> 停止并删除该用户的精确容器
    -> 正常 FUSE unmount
      -> 删除 desired marker 和空 mountpoint
```

失败时保留恢复意图，不使用 lazy unmount。用户删除也必须先完成该顺序，再删除 DOFS
namespace 元数据和精确对象前缀。

旧路径型 `files` 表没有隐式迁移。升级发现它时会拒绝启动且不修改旧行。先用旧版本
完成备份/导出，再选择显式迁移工具；若确认旧数据可丢弃，停止全部三个服务后执行：

```bash
domus reset -c /etc/domus/config.yaml --yes
```

备份/恢复也必须在三服务停止后运行：

```bash
domus backup  -c /etc/domus/config.yaml -o domus-backup.tar.gz
domus restore -c /etc/domus/config.yaml -i domus-backup.tar.gz --yes
```

格式 v2 包含 Domus PostgreSQL dump、bucket 中的密文对象，以及 SQLite 模式下的
checkpoint 后 DOFS metadata snapshot；PostgreSQL 模式的 DOFS 表已经在 dump 中。归档
采用临时文件完成后原子发布，不覆盖现有目标。恢复会先完整校验 manifest、对象文件、
密钥指纹和 SQLite schema，再开始清库。三个维护命令都会同时检查 Domus Web PID、
DOFS control socket 和 Workspace control socket，无法证明两个管理器已经停止时拒绝
执行。reset/restore 会清除 DOFS 的 `desired/`、`users/` 与 Workspace 的 `desired/`、
`identities/` 瞬态树，避免已删除用户在冷启动时复活；Workspace `manager-id` 会保留，
以便重启后继续识别并回收精确归属的遗留容器。

## 监控和发布验证

至少监控：

- DOFS live/ready、desired/mounted/degraded 数量；
- SQLite busy/lease loss，或 PostgreSQL 连接/advisory lease；
- state 卷容量、inode、WAL 和异常增长；
- OSS `Head/GetRange/Put/Copy/Delete` 错误和 direct-upload 残留；
- Docker 容器 recycle、OOM、PID/CPU/内存限制；
- FUSE busy、disconnected mount 和 foreign mount。

发布前执行：

```bash
go test ./... -count=1 -timeout 180s
go test -race ./internal/dofs ./internal/fileview ./internal/workspace \
  ./internal/terminal ./internal/handler -count=1
go vet ./...
cd frontend && npm run check && npm run build
```

在真实主机上还要跑 FUSE→Docker 和 Playwright E2E，并用两个非 root 用户确认：互相
不可见；浏览器明文只在本地加密；OSS 收到密文；终端、预览、转码和 HTTP 看到同一
generation；重启 DOFS 后容器因新 `mount_id` 被重建。
