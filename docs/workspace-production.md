# Workspace 生产部署已退役

当前 Domus 版本不部署 Workspace Manager、Docker worker 或 preview image。请删除旧
`domus-workspace.service`、`/etc/domus/workspace.yaml` 与旧受管容器，并按
[Domus 生产部署](dofs-production.md) 运行单一 Web 服务。

旧 `workspace:` 配置段仅为平滑升级而忽略，不表示仍支持该执行面。浏览器缩略图的
数据关系与部署要求见 [DOFS 文件数据面](dofs.md)。
