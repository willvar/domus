# 浏览器上传流水线

文件正文由浏览器加密后直传对象存储。Domus HTTP 接口仍只处理初始化、签名、任务状态
及完成校验。实现入口为 `frontend/src/uploads/` 和 `frontend/src/workers/encrypt.worker.ts`。

## 容量与所有权

每个页面的上传 Store 使用一个共享 Worker。Worker 持有两份可复用资源，每份包括：

- 一个 8 MiB 的密文分片缓冲；
- 一个复用的 hash-wasm SHA-256 实例；
- 填充时最多一批 384 KiB 明文，六个并行 WebCrypto 加密操作。

任务必须先借到资源才可读文件；借用覆盖填充分片、等待签名、发送、重试及清理的整个
生命周期。密文按实际长度写入 OPFS，关闭写句柄后把磁盘 File 交给 Axios；发送期间
临时文件不可改写。HTTP 完成或实际终止、磁盘暂存清理完成后才归还。

一个任务可以在上一片发送时填充下一片。所有任务共享同两份资源，新增任务只增加
File 引用、游标和小型哈希检查点。等待资源的任务不读取正文。资源按等待顺序分配。

这是应用持有的正文缓冲边界，不是整个浏览器的 RSS 上限。XHR、WebCrypto、GC 和
媒体解码的内部资源不包含在 16 MiB 分片缓冲中；多个页面各有自己的 Worker。

## 暂停、取消和完成

成功 PUT 后保存下一片的文件位置、分片号、已上传长度及 SHA-256 状态。暂停会终止
正在进行的 PUT、签名等待和容量等待；已经开始的有限读取／WebCrypto 操作完成后，
归还资源并丢弃未确认的分片。恢复从最后成功 PUT 的检查点重新生成，可能重传一片。

加密 AAD 序号由明文文件偏移决定。重建分片使用新的随机 nonce，序号及最终 wire
格式保持兼容。只有成功上传后才推进可恢复的哈希状态，避免重复哈希或跳过内容。
暂停／恢复针对当前页面；不提供关闭页面后的跨会话续传。

取消和失败必须等待所有持有资源的操作结束。没有剩余任务时终止共享 Worker。
签名等待、缩略图处理和远程 multipart 清理响应任务取消。初始化请求最多四个在途；
已发出的初始化会等到响应，以便取得 upload ID 后取消服务端 reservation。进入发布
阶段后等待服务端完成结果；这时 UI 不再提供暂停／取消。
UI 取消只发出本地中止信号，正在运行的上传流程在资源释放后统一清理远程 reservation，
避免 UI 和 finally 重复请求。发布期间的通用失败事件不抢先中止流程，保留 HTTP 完成
响应里的具体错误，便于重试与定位。

PUT 使用 120 秒无上传进度超时，不限制单次请求的总时长。上传字节数增加时续期；
连接建立前、发送停滞或发送完毕后等待确认超过该期限，才中止当前 XHR 并交给
axios-retry 重试。同一密文 File 和容量租约覆盖全部重试，每次尝试有独立的计时器；
暂停／取消仍立即中止请求及退避等待。

## 磁盘请求体与大分片

普通 8 MiB 分片也暂存到 OPFS。在 Chromium 151 实测中，循环发送 ArrayBuffer／Blob
时，浏览器原生请求体占用仍会随发送量增长；这在不加载 Domus 的最小原生 fetch 页面
同样可复现，单换 Axios adapter 或设置 no-store 不能消除。磁盘 File 请求体避开了
这条大内存副本路径。这里的磁盘文件只含密文，不包含原文。

S3 的 10000 分片限制会使大文件的逻辑分片超过 8 MiB。此时仍只借用固定大小的内存
缓冲，密文分批写入 OPFS 临时文件；关闭写句柄后，将磁盘 File 交给 XHR 发送。
最多两片在暂存／发送；磁盘需求取决于逻辑分片大小。

成功、暂停、取消或失败后删除文件。每个文件以 Web Lock 保护，下一次使用磁盘暂存
时回收已无活动持有者的文件，兼容多个页面同时上传。浏览器不支持 OPFS／Web Locks
或磁盘配额不足时明确失败，不退回整片内存分配。常规上传最多约 16 MiB 活动暂存文件，
大文件的自适应分片会增加磁盘空间需求，不增加缓冲尺寸。

## 复用边界

- Axios XHR adapter：发送、进度通知和 AbortSignal 取消；外围只维护无进度期限。
- axios-retry：幂等 PUT 的网络错误／5xx 重试、退避及取消等待。
- hash-wasm：增量哈希与 save/load 检查点。
- WebCrypto 和 escrow wire 常量／头：分块认证加密。
- 浏览器 OPFS／Web Locks：磁盘请求体及所有权。

Domus 保留的逻辑仅负责协议衔接、检查点、资源借还和产品任务生命周期。
缩略图的生成和上传共用一个独立名额，防止完成正文的多个任务积累缩略图正文。
超时／取消会卸载媒体元素、撤销对象 URL、清空 canvas 或销毁 PDF.js loading task；
清理结束后才允许下一个解码任务进入。媒体解码峰值仍与格式、像素及浏览器实现有关。

## 验证

在 `frontend` 目录运行（Node 22.18+ 或支持原生 TypeScript 的更新版本）：

```bash
node --test --test-timeout=30000 tests/encrypt-worker.test.mjs tests/upload-transport.test.mjs
UPLOAD_TEST_BYTES=8589934592 UPLOAD_TEST_FILES=2 node --test --test-timeout=240000 --test-name-pattern='large-file streaming roundtrip' tests/encrypt-worker.test.mjs
E2E_REUSE_SERVERS=1 npm run test:e2e -- e2e/upload-pipeline.spec.ts
UPLOAD_TEST_BYTES=8589934592 UPLOAD_TEST_FILES=2 node tests/browser-upload-stress.mjs
```

Node 测试直接运行生产引擎，合成数据按需生成，接收端逐块认证解密并检查 SHA-256，
不构造完整大文件。覆盖多文件总读取超前量、缓冲复用、慢网络、暂停检查点、取消、
失败后容量归还及大分片磁盘暂存。
传输测试运行真实 Axios adapter／重试逻辑，以可控 XHR 和时钟验证持续五分钟的上传、
连接／发送／等待确认的停滞重试、重复进度通知，以及发送／退避中的取消与计时器清理。

浏览器集成测试运行真实共享 Worker、XHR 和 OPFS，控制接口／对象存储由测试响应
替代，覆盖分片边界、503 重试、Store 多文件状态、晚到初始化取消和媒体资源释放。

`browser-upload-stress.mjs` 使用磁盘稀疏文件启动多个独立上传，接收端在本机按流
认证解密并校验哈希，不在测试工具中截留全部请求体。它采样 Chromium 各进程的 Linux
PSS 总和（与 RSS 不同，按比例计算共享页），默认在超过 1 GiB 时终止测试。测试通过
CDP 操作文件选择及读取结果，不开启 Network 请求正文记录。完整真实后端读回另由
`e2e/upload-boundaries.spec.ts` 验证，需要有效的测试账户。

### 2026-09-26 20:00 HKT 验证记录

- 生产引擎回归：8 项通过，8 个文件的总读取超前量最大 16,646,144 字节，
  复用两块分片缓冲；包含快速暂停／恢复、等待签名取消、失败及配额耗尽清理。
- Chromium 集成：5 项通过，覆盖真实共享 Worker、XHR 重试、OPFS、锁保护的
  残留清理、Store 取消和缩略图资源释放。
- Chromium 151.0.7922.34 压力测试：同一 8 GiB 磁盘稀疏文件启动两个独立加密
  上传，共 16 GiB，126.34 秒完成（本机回环约 129.7 MiB/s）；接收端逐块认证解密，
  两份 SHA-256 均与发送端一致。128 次采样的浏览器进程总 PSS 峰值
  657,728,512 字节（约 627 MiB），起始约 482 MiB；没有出现随正文量线性增长。
- `npm run check`、`npm run build`、`git diff --check` 通过。
- 真实后端上传／下载测试未进入上传阶段：测试账户认证返回 401，未计为通过。

压力测试使用本机流式接收端，吞吐数字不代表远程对象存储或移动端性能。

### 对象存储分页与完成校验

8 GiB 文件的默认分片数会超过 ListParts 单页的 1000 条上限。部分 S3-compatible
实现把 `part-number-marker` 对应的边界分片也放入下一页。Store 只略过与上一页末项
编号、尺寸和 ETag 均一致的重复边界；冲突的重复项、不能推进的游标仍报错。
完成校验继续要求分片连续、有有效 ETag、尺寸合法且总字节数匹配。

开发对象存储的 1003 分片回归已验证这一兼容行为：原始响应为 1000 条和 4 条，
规范化后必须恰好得到 1003 条。测试创建独立微小分片，结束后取消自己的 multipart：

```bash
DOMUS_TEST_OSS_CONFIG="$PWD/tmp/dev/config.yaml" go test ./internal/store -run TestOSSListParts -count=1 -v
```

浏览器完成请求失败后保留本地任务和原始错误，后续 `task.update: failed` 不移除该项，
也不重复发送取消请求。补充回归覆盖失败事件先于／晚于 HTTP 响应，以及初始化后
等待签名时的单次取消清理。上传相关 14 项 Node 测试和 8 项 Chromium 集成测试通过。
