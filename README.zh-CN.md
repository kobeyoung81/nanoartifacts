# nanoartifacts

作品库：智能体发布自包含的 HTML 文档，人在这里浏览、分享、看版本。

它是八个一级服务之一。它拥有 `artifacts_` 前缀、自己的前端，以及自己的 MCP 端点。

## 它是什么

- **在对话里发布。** 智能体写好一份报告、一个看板或一个小应用，经 MCP 发布，不用把整篇文档贴进聊天。
- **空间与树。** 文件夹、文档、应用构成层级，权限按空间或按作品授予。
- **不会膨胀的版本。** 相同字节只存一份、只计一次配额；同一份页面发两次也只有一个副本。
- **两个面，一套代码。** `/artifacts` 整页，以及控制台工作台里的两个面板。
- **浏览器能渲染的一切。** 每个版本在沙箱里以预览 cookie 提供，所以链接在新标签页里也能打开。

## 它在哪

| | |
|---|---|
| 集合 | `artifacts_*` 与根集合 `artifacts`；正文在 `artifacts_fs.files` / `.chunks` |
| 端口 | 网络内 `8080`；经控制台的 nginx 从 `/nanoartifacts/` 进来 |
| 路径 | `/artifacts`（分享出去的链接，由门户提供）与 `/nanoartifacts/`（本服务） |
| 上游 | 智能体（MCP）、浏览器（JWT 或预览 cookie） |
| 读取 | `nanoauth`（账号）、`nanoportal`（智能体归属）—— 不读它们的集合 |

## 快速开始

```bash
go build ./cmd/nanoartifacts
NANOARTIFACTS_MONGO_URI="mongodb://localhost:27017/nanosec" \
NANOARTIFACTS_SESSION_SECRET="$(openssl rand -hex 32)" \
NANOAUTH_JWKS_URL="http://localhost:14401/auth/v1/.well-known/jwks" \
NANOSEC_PORTAL_URL="http://localhost:14403" \
NANOAUTH_INTERNAL_URL="http://localhost:14401" \
NANOSEC_BUS_INTERNAL_TOKEN="…" \
  ./nanoartifacts serve

./nanoartifacts migrate up   # 应用内嵌清单
```

## 配置

| 变量 | 必需 | 含义 |
|---|---|---|
| `NANOARTIFACTS_MONGO_URI` | 是 | 平台库；URI 的路径里必须写出库名 |
| `NANOARTIFACTS_SESSION_SECRET` | 是 | 预览 cookie 的 HMAC 密钥 |
| `NANOSEC_PORTAL_URL` | 是 | 智能体归属 |
| `NANOAUTH_INTERNAL_URL` | 是 | 分享对话框查账号 |
| `NANOAUTH_JWKS_URL` | 是 | 令牌校验 |
| `NANOSEC_BUS_INTERNAL_TOKEN` | 是 | MCP 调用方与上面两处查询 |
| `NANOARTIFACTS_QUOTA_BYTES` | 否 | 每人配额，默认 2 GiB |
| `NANOARTIFACTS_WEB_DIR`、`NANOARTIFACTS_ADDR`、`NANOARTIFACTS_COOKIE_PATH` | 否 | 前端产物、监听地址、cookie 作用域 |

## HTTP

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 存活 |
| GET | `/api/v1/artifacts?scope=owned\|shared&q=&kind=` | 可见性由调用者的 JWT 决定 |
| POST | `/api/v1/artifacts` | 发布（`html`、`title`、`kind`，可选 `artifact_id`） |
| GET | `/api/v1/artifacts/{id}` | 元数据 + 版本 + 授权 |
| POST | `/api/v1/session` | 用 JWT 换预览 cookie |
| GET | `/api/v1/artifacts/{id}/versions/{seq}/raw?download=1` | cookie 或 bearer；每次加载都重查权限 |
| POST | `/api/v1/artifacts/{id}/rollback` | `{"seq": n}`；历史保留 |
| POST/DELETE | `/api/v1/artifacts/{id}/grants[/{userID}]` | 仅所有者 |
| DELETE | `/api/v1/artifacts/{id}` | 软删除 |
| POST | `/mcp`、`/mcp/blobs` | 仅智能体：直接发或按句柄发 |

## MCP 工具

`artifact_publish` · `artifact_list` · `space_list` · `tree_get` · `artifact_get` ·
`artifact_rename` · `artifact_grant` · `artifact_delete`

由 `nanoadmin mcp seed` 以 `scope=platform` 播种，按智能体类型逐条开启 ——
新服务不能悄悄扩大智能体能力。

**发布大页面。** 走 JSON-RPC 会让模型把文档写两遍：一次作为输出，之后每次重放对话再来一遍。
所以工具收的是路径：智能体把文件写进工作区，调
`artifact_publish(html_file: "artifacts/report.html")`，运行时把它上传到
`POST /mcp/blobs` 并换成句柄，服务端在 15 分钟内、仅对上传者本人认领一次。
读取反过来同理：`artifact_get(download_to: …)` 返回描述符而不是正文。

## 设计决策

- **id 就是内容的 sha256。** 相同字节只存一份、只计一次；版本行是元数据，正文共享。
- **正文先写、版本行后写。** 崩溃只会留下孤儿块，由 `nanoartifacts gc` 清掉，
  绝不会出现指向空气的版本。
- **删除是软的。** 行被标记，正文留到垃圾回收证明没人引用它为止。
- **两个前缀是刻意的。** 页面在 `/artifacts`（门户），应用与 API 在 `/nanoartifacts/`（本服务）。
  代理的尾斜杠归一会把门户地址送进本服务，于是新标签页打开时没了外壳。
- **预览 cookie 让链接能用。** Ctrl+点击与粘贴的链接没有父窗口可以递令牌，
  所以页面问会话端点「我是谁」。发 cookie 只认真 bearer，所以 cookie 不能无限续期。
- **面板不是页面。** 工作台用同一次构建托起两个面板（联邦 remote，React 取自宿主）；
  面板一次只显示一个视图，窄栏放不下三栏布局。

## 文档

- [`docs/ARCHITECTURE.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/docs/ARCHITECTURE.md) —— 它所在的整个平台
- [`docs/DEPLOYMENT.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/docs/DEPLOYMENT.md) —— 端口、nginx 挂载、运维
- [`docs/README.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/docs/README.md) —— 全部文档索引

## 状态

随 `v0.1.0` 发布；本模块的版本钉在超仓的 `versions.lock` 里。

## 贡献与安全

开发与提交规则见
[`CONTRIBUTING.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/CONTRIBUTING.md)；
漏洞请按
[`SECURITY.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/SECURITY.md) 私下报告，
不要开公开 issue。

## 许可证

[AGPL-3.0](./LICENSE)。名称与标志另有说明 —— 见 [`NOTICE`](./NOTICE)。
