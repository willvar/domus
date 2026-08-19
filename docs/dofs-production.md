# Domus 生产部署

文件管理产品的唯一必需进程是 `domus.service`：

```text
browser <-> Domus HTTP/WS <-> PostgreSQL / DOFS metadata / OSS
                    browser <-> OSS ciphertext
```

不需要 Docker、Workspace Manager、preview worker、FUSE 或 `user_allow_other`。Web
进程内直接使用 DOFS backend；文件正文由浏览器加密后直传 OSS，缩略图也由浏览器生成。

## 必需依赖

- PostgreSQL；
- 专用 S3-compatible bucket 与 HTTPS endpoint；
- 持久的 `server.encryption_secret`、`server.session_secret` 和 root bootstrap secret；
- 单机默认的本地 DOFS SQLite 元数据目录，或多主机部署使用 PostgreSQL metadata；
- 静态前端产物与 Domus binary。

OSS CORS 必须允许产品 origin 对 presigned URL 执行 `PUT`、`GET` 和 `Range`。浏览器
不需要读取上传 ETag；Domus 使用服务器端 `ListParts` 获取权威结果。bucket 应专供
当前实例，因为 backup/reset/restore 以整个 bucket 为边界。bucket 必须关闭版本控制，
或配置规则及时清除非当前版本；同时配置 incomplete multipart 生命周期清理，否则
DeleteObject/中断上传不一定马上降低供应商计费空间。

Domus Web 内置 DOFS reclaimer：永久删除在事务内立即释放用户逻辑用量，密文删除失败
则由持久化 tombstone 每 30 秒重试。`pending_reclaim_bytes` 可用于判断物理回收积压；它
表示明文逻辑大小，不等于供应商账单字节。每个 namespace 同时只允许一个 FUSE 挂载；
其独占 mount lease 让本地打开句柄表成为回收依据。没有挂载时，后台回收器才会取得
reclaim lease；挂载存活期间则由挂载进程在最后一个相关句柄关闭后执行同样的回收。

## 配置

完整模板见 `config.example.yaml`。单服务器推荐：

```yaml
dofs:
  metadata:
    driver: sqlite
    sqlite:
      path: /var/lib/domus/dofs/state/metadata.sqlite
      busy_timeout_seconds: 5
      max_open_connections: 8
```

SQLite 必须位于本机文件系统。多个 Domus Web 节点共享命名空间时改用：

```yaml
dofs:
  metadata:
    driver: postgres
```

这会复用 `database.*`。多节点仍需在负载均衡、WebSocket 和操作调度层自行设计高可用，
本配置项只解决 DOFS catalog 的共享与事务锁。

## systemd

安装：

```text
deploy/systemd/domus.sysusers.conf -> /usr/lib/sysusers.d/domus.conf
deploy/systemd/domus.service       -> /etc/systemd/system/domus.service
```

`/etc/domus/config.yaml` 建议为 `domus:domus 0600`。随后：

```bash
systemd-sysusers
systemd-analyze verify /etc/systemd/system/domus.service
systemctl daemon-reload
systemctl enable --now domus
```

`domus.service` 不需要 Docker socket、`/dev/fuse`、宿主 mount namespace 或提升能力。
部署时应保留 unit 内的 filesystem/kernel hardening，并允许 PostgreSQL/OSS 网络访问。

## 可选 Linux FUSE

仅当 Domus 之外的本机程序要以普通 I/O 访问 DOFS 时，额外安装并启动
`deploy/systemd/domus-dofs.service`。这需要 Linux、FUSE 3、`/dev/fuse`、
`fusermount3`；`allow_other: true` 时还需在 `/etc/fuse.conf` 启用
`user_allow_other`。

```bash
systemd-analyze verify /etc/systemd/system/domus-dofs.service
systemctl enable --now domus-dofs
domus dofs status -c /etc/domus/config.yaml
```

该服务不是 `domus.service` 的依赖。它与 Web 访问相同 namespace/metadata；FUSE 的
`state_root` 可能含明文 writeback 数据，必须位于私有加密本地卷。不要把 mount root
整体暴露给不可信进程。

## 备份、恢复与发布验证

执行 `backup`、`restore --yes` 或 `reset --yes` 前，停止 Domus Web 和所有共享相同
DOFS metadata 的可选挂载服务。SQLite 备份同时包含数据库快照；PostgreSQL 模式的
DOFS 表随产品数据库转储。

```bash
go test ./... -count=1 -timeout 120s
go vet ./...
cd frontend && npm run check && npm run build
make test-e2e
```

生产验收至少验证两个普通用户互相不可见、上传正文不经过 Domus origin、缩略图默认
不加载、开启后可解密显示、覆盖/永久删除能回收旧缩略图，以及服务重启后 inode 与
generation 稳定。`make test-e2e` 最后还会通过 OSS Stat 确认一个隔离测试对象已被物理
删除。若启用 FUSE，再运行 sibling `dofs` 仓库的真实 FUSE 测试。
