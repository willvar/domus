# 已移除的 Workspace 执行面

Domus 曾使用每用户 Docker 容器承担终端、转码和服务端缩略图任务。这套 Workspace
Manager、Unix 控制协议、Docker runtime、镜像和公开执行入口已经移除。

当前产品是纯文件管理器：

- 文件正文由浏览器加密后直传 OSS；
- 图片、视频和 PDF 缩略图由上传浏览器生成并加密直传；
- 文件预览在浏览器完成；
- 服务端只有上传任务，不执行用户命令、转码或预览 worker。

配置中的旧 `workspace:` 段会在升级时被忽略，保存配置后不会再次输出。它不再启用
任何兼容路径。退役的 `domus workspace ...` CLI、`session.*`、`workspace.event` 和
`/workspace/` 路由均不存在。

未来若引入 AI/容器执行，应作为新的、显式授权和独立威胁模型设计，不能复活或暗中
依赖旧 Workspace 协议。
