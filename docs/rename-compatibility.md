# Zephyr → Domus 升级兼容

Domus 是新的产品名、Go module、CLI/binary 名称与前端品牌。升级不会重命名
已有的 OSS object key、用户路径、数据库表或加密材料。

## 保留的回退

| 新标识 | 旧标识回退 | 优先级与行为 |
| --- | --- | --- |
| `DOMUS_ROOT_BOOTSTRAP_PASSWORD` | `ZEPHYR_ROOT_BOOTSTRAP_PASSWORD` | 新变量非空时优先；仅在新变量为空时读取旧变量。 |
| `DOMUS_DAEMON` | `ZEPHYR_DAEMON` | 新进程只写新变量；启动时识别任一值为 `1` 的变量。 |
| `domus_session` | `zephyr_session` | 优先验证新 Cookie；新 Cookie 缺失时验证旧 Cookie，成功后自动换发新 Cookie；退出登录同时清除两者。 |
| backup manifest `domus_version` | `zephyr_version` | 新备份只写新字段；restore 可读取旧字段。 |
| Domus 前端 localStorage keys | `zephyr_*` keys | 新 key 缺失时复制旧值到新 key，旧值保留以支持回滚。 |
| IndexedDB `domus` | IndexedDB `zephyr` | 首次打开时复制待处理离线操作，旧数据库保留以支持回滚。 |
| Domus browser-history state | `zephyrDesktop` / `zephyrWindowId` | 当前页面仍可读取升级前的 history entry。 |
| Cache API `domus-decrypt` | `zephyr-decrypt` | Service Worker 激活时删除旧的临时解密缓存；缓存不属于持久用户数据。 |

## 有意保留的隐式默认值

历史配置如果完全省略 `server.pid_file` 或 `database.dbname`，仍分别得到
`zephyr.pid` 与 `zephyr`。这样旧实例不会因为升级而指向一个新 PID 文件或新建的
空数据库。新部署应使用 `config.example.yaml` 中显式的 `domus.pid` 与 `domus`。

显式配置值从不被重写：已有部署写了 `dbname: zephyr`、自定义 PID 路径、bucket
或域名时，Domus 会继续使用它们。

## 不提供的别名

构建产物和 CLI 已正式改为 `domus`，不会额外生成 `zephyr` 可执行文件。需要平滑
切换 service unit 或脚本时，应在部署层暂时提供软链接，然后把调用方迁移到
`domus`。

用户执行面不提供旧架构回退：VSH、进程内 SSH、Docker Workspace、服务端预览和转码
均已删除。`session.open`、`session.complete`、`session.done`、`session.ssh` 与
`workspace.event` 不再注册，后端与前端应作为同一次版本发布部署。

为避免旧配置阻断升级，遗留的整个 `workspace:` YAML 段会被忽略，并在下一次保存配置
时消失；这只是一项配置读取兼容，不会启动 Manager、容器或其他备用执行路径。
