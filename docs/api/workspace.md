# 工作区接口 `/workspace`

返回总入口：[返回接口文档首页](./README.md)

## `GET /workspace/`
读取工作区状态。

鉴权：已登录

响应：
```json
{"state": {...}}
```
或：
```json
{"state": null}
```

## `PUT /workspace/`
保存工作区状态。

鉴权：已登录

请求体：
```json
{"state": {"windows": [], "tabs": []}}
```

响应：
```json
{"ok": true}
```

## `DELETE /workspace/`
清空工作区状态。

鉴权：已登录

响应：
```json
{"ok": true}
```
