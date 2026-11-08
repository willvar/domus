# Domus 的 DOFS 文件数据面

Domus 使用独立模块 [`github.com/willvar/dofs`](https://github.com/willvar/dofs) 作为唯一
文件架构。每个用户对应一个以不可变 UUID 命名的 namespace；目录层级、稳定 inode、
generation、大小、包装密钥、对象键和配额以 DOFS 为权威。

用户看到的绝对路径直接相对于该 namespace：`/` 就是 DOFS 根 inode，`/Documents/a.txt`
就是根下的 `Documents/a.txt`。用户名不进入路径，也不会创建 `/home/<username>`。租户
隔离来自已认证用户 ID 到 namespace 的绑定，而不是路径前缀。`/.domus/` 是 Domus 保留
的隐藏目录，当前保存 `trash/`、`thumbnails/` 和 `user/`；公开文件 API 不允许直接访问。
回收站不是用户目录的路径别名：Domus 为每次删除建立不透明 ID，并把稳定 inode 移到
`/.domus/trash/items/<entry-id>`；原路径、删除时间和恢复状态保存在产品数据库中。
因此同一路径可以保留多个删除版本，目录内子项也可以单独浏览、还原或永久删除。
用户创建的 `/__trash__/` 是普通目录，不会与产品回收站重叠。

旧路径表、逻辑路径对象键和服务端正文上传没有双读回退。检测到未迁移的旧文件表时
服务会拒绝启动，避免静默丢失或重解释数据。

## 上传和读取

```text
浏览器 File/Blob
  -> 生成 DEK，按 64 KiB 块做 AES-256-GCM
  -> presigned URL 直接 PUT 密文到 OSS
  -> Domus 通过 ListParts/HeadObject 校验权威状态
  -> DOFS generation CAS 原子发布 inode
```

Domus HTTP 只接收路径、明文长度、DEK、上传 ID、哈希和完成确认等小型 JSON。文件
明文/密文正文都不进入 HTTP 请求体。读取时，`GET /file/access` 返回短期 OSS URL 与
解密元数据，浏览器/Service Worker 直接读取密文并在本地解密。

这属于服务器托管密钥的应用层加密，不是零知识加密。Domus 能在授权场景解包 DEK，
但常规传输路径不代理文件正文，OSS 中只保存密文。

可选的 [OAuth 只读 WebDAV](api/oauth-webdav.md) 是独立鉴权的数据入口：只向显式授权的
Bearer 客户端输出当前普通文件原文，使用现有 DOFS Range 解密；不改变上述浏览器路径、
加密格式或内部目录边界。

## 服务器缩略图与可见区域读取

图片、视频和 PDF 在服务器 worker 中生成 WebP 缩略图；PDF 使用 Poppler 的
`pdftoppm` 只渲染第一页。worker 只通过私有 FUSE 挂载读原件、写回派生文件，由 DOFS
完成加密和对象发布，不自行处理密钥。生成失败不阻止原件上传；按 generation 记录
状态、脱敏错误及尝试次数，最多自动尝试三次，也可显式请求重建。

关联不是靠文件名，而是：

```text
源 inode + 源 generation -> 缩略图 inode
```

因此重命名、移动不会失联；覆盖内容会清除旧 generation 的媒体元数据并回收旧缩略
图；复制创建独立源 inode且不继承缩略图；永久删除源文件会删除其缩略图。存储路径
`/.domus/thumbnails/` 只是内部组织方式，不是关系主键；前端使用产品私有虚拟路径
`/.user/thumbnails/`，它不会出现在文件列表中。

“显示缩略图”开关保存在当前浏览器的 localStorage，默认关闭。它只决定列表和详情
是否创建缩略图读取请求，不控制服务器生成。开启时，仅可见项注册临时解密映射，
最多四个并发读取；滚出视口取消未完成请求。已完成的图片在缓存视图中复用，读取或
解码失败立即回退文件图标。关闭时不产生缩略图对象的额外 OSS GET。

缩略图关联成功后，worker 经本机 RPC 通知 Web 推送源目录更新；失败状态也会通知。
列表不再定时检查缺图，前端收到通知才读取当前目录信息，仍只下载可见缩略图。

## 变更通知

Domus 桥接层使用 DOFS 已有的统一 `MetadataStore` 接口，在写入成功提交后发送本机
变化提示。SQLite 和 PostgreSQL 不需要各自的通知分支，也不修改独立 DOFS 库。
Web 收到提示后按现有事件游标读取变化日志，不再固定间隔扫描；任务和转码状态同样
通过本机 RPC 与现有 WebSocket 更新界面。此提示不是业务指令或可靠消息队列，漏提示
不影响保存结果，可通过重新进入视图、恢复 WebSocket 连接或手动刷新读取最新状态。

## 元数据职责

DOFS 保存 namespace、inode/目录、generation、大小、mode、对象键、包装 DEK、上传
reservation、writer/mount/reclaim lease、事件游标和配额。`used_bytes` 统计当前未 unlink
的活动文件（包括 Domus 隐藏回收站中的 payload）；永久删除会在元数据事务中立即把
相应大小移入 `pending_reclaim_bytes`。

Domus PostgreSQL 保存产品数据；其中 `domus_file_metadata` 只补充 MIME、内容哈希、
媒体尺寸及缩略图 inode，`domus_file_uploads` 保存上传任务与恢复状态，
`domus_trash_entries` 保存删除事件及跨数据库操作状态。删除、还原和清空回收站先持久化
状态，再修改 DOFS；启动和周期恢复任务会收敛中断在途的操作。

单服务器默认使用本地 SQLite 保存 DOFS 元数据：

```yaml
dofs:
  metadata:
    driver: sqlite
    sqlite:
      path: /var/lib/domus/dofs/state/metadata.sqlite
      busy_timeout_seconds: 5
      max_open_connections: 8
```

多主机共享 namespace catalog 时可用 `driver: postgres`，它复用 `database.*`。SQLite
文件不得位于 NFS/SMB 或被多主机共享。

## 对象、一致性与可选 FUSE

对象位于实例配置的 OSS 前缀下；`oss.prefix` 为空时保持旧版 bucket 根行为：

```text
<oss.prefix>/.dofs/v1/namespaces/<user-id>/objects/<inode>/<generation>-<transaction>.dofs
```

rename 只改目录元数据；copy 使用对象存储侧密文复制；replace/unlink 用 tombstone 保护
仍被打开的 generation。逻辑路径不会成为对象键。Domus 启动独立于 FUSE 的周期
reclaimer，并在永久删除后立即尝试一次：它以 tombstone 为持久化队列，幂等删除该
inode 的全部 OSS generation 后清除元数据；失败、重启或挂载占用都会保留任务重试。
旧内容 generation 会至少保留 4 小时 15 分钟，以覆盖 Domus 的 4 小时 presigned GET，
随后回收；明确的永久删除会立即使旧读取失效，不等待该宽限期。

回收站 payload 仍是 namespace 中的活动文件，因此继续计入用户用量；永久删除后才进入
回收流程。`pending_reclaim_bytes` 是待回收文件的明文逻辑大小，不是 OSS 账单中的精确密文字节。
bucket 应关闭版本控制；否则普通 DeleteObject 可能只产生 delete marker，必须另配非
当前版本生命周期清理。直接分片上传还应配置 incomplete multipart 生命周期规则。

Domus Web 直接使用 DOFS backend，不需要 FUSE。`domus dofs serve` 仅在 Linux 上按需
把同一 namespace 的根 inode 挂到管理员选择的挂载点，供 Domus 之外的本机程序接入；
挂载点本身决定 Linux 路径，不会额外生成 `home/<username>`。FUSE writeback
state 可能含明文，必须放在权限 0700 的加密本地卷；不能放进 OSS、NFS 或容器层。
通过 FUSE/Unix `rm` 的删除保持原生永久删除语义；只有 Domus 文件管理 UI/API 的删除
命令会创建产品回收站条目。

本次 Trash v2 升级不保留旧版按路径组织的 `/.domus/trash/` 内容。启动时只保留拥有
`domus_trash_entries` 记录且 inode 匹配的 `items/<entry-id>` 根，其余旧版或孤儿 payload
会被永久清理；`items/` 结构目录本身在后续重启中保持稳定。

DOFS 不适合作为数据库、包管理器状态或高频构建缓存，也不承诺 symlink、hard link、
特殊节点、mmap 数据库、POSIX lock 等完整本地文件系统语义。
