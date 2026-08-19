# Domus 的 DOFS 文件数据面

Domus 使用独立模块 [`github.com/willvar/dofs`](https://github.com/willvar/dofs) 作为唯一
文件架构。每个用户对应一个以不可变 UUID 命名的 namespace；目录层级、稳定 inode、
generation、大小、包装密钥、对象键和配额以 DOFS 为权威。

用户看到的绝对路径直接相对于该 namespace：`/` 就是 DOFS 根 inode，`/Documents/a.txt`
就是根下的 `Documents/a.txt`。用户名不进入路径，也不会创建 `/home/<username>`。租户
隔离来自已认证用户 ID 到 namespace 的绑定，而不是路径前缀。`/.domus/` 是 Domus 保留
的隐藏目录，当前保存 `trash/`、`thumbnails/` 和 `user/`；公开文件 API 不允许直接访问。
界面中的虚拟路径 `/__trash__/` 映射到 `/.domus/trash/`。

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

## 浏览器缩略图

图片、视频和 PDF 在上传浏览器中生成 WebP 缩略图。PDF 只渲染第一页；生成失败不会
阻止原文件上传。缩略图随后使用独立 DEK 加密，通过 presigned URL 直传 OSS，并作为
隐藏的独立 DOFS 文件保存。

关联不是靠文件名，而是：

```text
源 inode + 源 generation -> 缩略图 inode
```

因此重命名、移动不会失联；覆盖内容会清除旧 generation 的媒体元数据并回收旧缩略
图；复制创建独立源 inode且不继承缩略图；永久删除源文件会删除其缩略图。存储路径
`/.domus/thumbnails/` 只是内部组织方式，不是关系主键；前端使用产品私有虚拟路径
`/.user/thumbnails/`，它不会出现在文件列表中。

“显示缩略图”开关保存在当前浏览器的 localStorage，默认关闭。它只决定列表和详情
是否创建缩略图读取请求；上传时仍会生成并保存缩略图。关闭时显示通用文件图标，不
产生缩略图对象的额外 OSS GET。

## 元数据职责

DOFS 保存 namespace、inode/目录、generation、大小、mode、对象键、包装 DEK、上传
reservation、writer/mount/reclaim lease、事件游标和配额。`used_bytes` 只统计当前可见
文件；永久删除会在元数据事务中立即把相应大小移入 `pending_reclaim_bytes`。

Domus PostgreSQL 保存产品数据；其中 `domus_file_metadata` 只补充 MIME、内容哈希、
媒体尺寸及缩略图 inode，`domus_file_uploads` 保存上传任务与恢复状态。

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

对象位于不可变前缀：

```text
.dofs/v1/namespaces/<user-id>/objects/<inode>/<generation>-<transaction>.dofs
```

rename 只改目录元数据；copy 使用对象存储侧密文复制；replace/unlink 用 tombstone 保护
仍被打开的 generation。逻辑路径不会成为对象键。Domus 启动独立于 FUSE 的周期
reclaimer，并在永久删除后立即尝试一次：它以 tombstone 为持久化队列，幂等删除该
inode 的全部 OSS generation 后清除元数据；失败、重启或挂载占用都会保留任务重试。
旧内容 generation 会至少保留 4 小时 15 分钟，以覆盖 Domus 的 4 小时 presigned GET，
随后回收；明确的永久删除会立即使旧读取失效，不等待该宽限期。

`pending_reclaim_bytes` 是待回收文件的明文逻辑大小，不是 OSS 账单中的精确密文字节。
bucket 应关闭版本控制；否则普通 DeleteObject 可能只产生 delete marker，必须另配非
当前版本生命周期清理。直接分片上传还应配置 incomplete multipart 生命周期规则。

Domus Web 直接使用 DOFS backend，不需要 FUSE。`domus dofs serve` 仅在 Linux 上按需
把同一 namespace 的根 inode 挂到管理员选择的挂载点，供 Domus 之外的本机程序接入；
挂载点本身决定 Linux 路径，不会额外生成 `home/<username>`。FUSE writeback
state 可能含明文，必须放在权限 0700 的加密本地卷；不能放进 OSS、NFS 或容器层。

DOFS 不适合作为数据库、包管理器状态或高频构建缓存，也不承诺 symlink、hard link、
特殊节点、mmap 数据库、POSIX lock 等完整本地文件系统语义。
