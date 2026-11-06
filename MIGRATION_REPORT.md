# 1105 迁移报告

## 基线

- New API：[v1.0.0-rc.48](https://github.com/QuantumNous/new-api/releases/tag/v1.0.0-rc.48)，提交 `706144769e5bed5a47b78721e3bb11da3cf4c0b9`，2026-11-04 发布；11 月 5 日开始迁移，11 月 6 日再次查询 GitHub，仍为最新发布版本。
- Codex：[rust-v0.166.0](https://github.com/openai/codex/releases/tag/rust-v0.166.0)，11 月 6 日查询到的最新稳定版本参考。
- 原工程提交 `d97386da3`；原工作区未提交改动已先保存为 `90f88ee31`。
- 新分支：`1105`。原分支及远端默认分支保留。11 月 6 日已按后续指令部署根域名隔离测试实例，原 Codex 服务保持原版本。

## 官方实现优先

采用官方 Responses HTTP/WebSocket、重试与渠道约束、流式处理、AlphaSearch、图片响应与 usage 处理、审计日志、错误日志和性能健康统计。删除被上述实现替代的旧分支代码及其重复测试。失败请求继续进入官方健康统计，成功消费日志用于消费统计，避免把“成功消费次数”与“请求成功率”混为一谈。

前端跟随官方迁到 `web/`，使用统一 React 应用。官方新功能、全部翻译键和品牌归属保留；迁入 token 消耗图、输入／输出／缓存 token、缓存命中率、自定义日期、API 访问限制、内存诊断和本地站点图标。现有业务组件和官方健康面板继续复用。

## 三项重点能力

| 能力 | 实现与回归范围 |
| --- | --- |
| web_search | 官方 `/v1/alpha/search`，保留未知 JSON 字段；Responses 原始 tools 不丢失；配置渠道的 OAuth/account 认证；模拟上游请求与响应 |
| 生图 | Codex generations、JSON edits 和 SSE；复用官方 OpenAI 图片响应、图片计数及 usage 归一化；模拟上游验证认证、图片载荷与输入／输出 token |
| 实时语音 | 官方 WebSocket 传输与 Realtime 转发器；新增 POST `/v1/live`、`/v1/realtime/calls` 和 GET `/v1/live/:call_id`；SDP、session、Location、双向音频事件、跨用户／令牌拒绝、固定创建渠道和多密钥账号；账号禁用或替换后拒绝续接 |

Codex 源码依据：[Realtime call](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/codex-api/src/endpoint/realtime_call.rs)、[Realtime WebSocket](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/codex-api/src/endpoint/realtime_websocket/methods.rs)。

## 保留的请求优化

- 原始 Responses 路径仅对 `APITypeCodex` + `ChannelTypeCodex` 生效。普通 API 渠道和协议转换继续走官方路径。
- 大请求按顶层字段读取／重写，流建立后释放请求存储；保留未知输入与工具字段。
- 模型修饰符和渠道系统提示词使用官方转换，避免只修改模型名而遗漏 reasoning 等参数。
- 只传递选定的客户端元数据头；上游 Authorization、chatgpt-account-id 来自服务端配置。客户端 actor marker 不转发。
- 使用官方亲和与重试策略，保留有界亲和字段解析；不恢复 prompt_cache_key 改写或临时调试日志。

## 验证

2026-11-06 本地验证，Go 1.26.10、Bun 1.4.4：

| 验证 | 命令／结果 |
| --- | --- |
| Go 主模块 | `go test ./...`，通过 |
| 独立模块 | 分别在 `relaykit/`、`tokenkit/` 执行 `GOWORK=off go test ./...` 和 `GOWORK=off go build ./...`，通过 |
| 前端回归 | `cd web && bun run test`，32 个文件、501 项测试通过 |
| 前端类型与生产构建 | `cd web && bun run build:check`，1,413 个文件类型检查及构建通过 |
| 前端改动检查 | 对相对 rc.48 修改的 27 个 TS/TSX 文件执行 Oxlint、Oxfmt 检查，退出码均为 0；Oxlint 留有一条既有 JSON 深复制风格警告 |
| Linux 构建 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go build -trimpath -ldflags "-s -w -X github.com/QuantumNous/new-api/common.Version=$(cat VERSION)" -o .git/1105-validation/new-api .`，得到静态链接的 x86-64 ELF |
| 本地启动 | 原生二进制使用独立 SQLite 数据目录连续启动两次，两次 `/api/status` 均成功，版本为 `v1.0.0-rc.48-enhanced-20261105` |

实际数据库版本：SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **16.15**。MySQL 和 PostgreSQL 均为本机临时测试实例，未连接生产数据库。

```bash
TEST_MYSQL_DSN='root@tcp(127.0.0.1:13306)/newapi_1105?charset=utf8mb4&parseTime=True&loc=Local' \
TEST_POSTGRES_DSN='postgres://codex_1105@127.0.0.1:15433/newapi_1105?sslmode=disable' \
go test ./model -count=1 -v

TEST_PERF_MYSQL_DSN='root@tcp(127.0.0.1:13306)/newapi_1105_perf?charset=utf8mb4&parseTime=True&loc=Local' \
TEST_PERF_POSTGRES_DSN='postgres://codex_1105@127.0.0.1:15433/newapi_1105_perf?sslmode=disable' \
go test ./pkg/perf_metrics -count=1 -v
```

两套数据库矩阵均通过。覆盖官方新建／旧结构迁移、重复迁移、索引与唯一性约束，以及本地新增的消费 token／缓存聚合、请求 ID 过滤、模型请求次数、最早统计时间和迁移后数据保留。性能测试在独立数据库验证聚合和刷新。此分支相对 rc.48 没有新增数据库字段；未使用生产数据副本做升级演练。

搜索、图片及语音使用 `httptest` HTTP/WebSocket 上游：验证实际出站路径、认证、响应字节、SSE usage、SDP/session、Location、双向音频事件及会话路由隔离。未将这些模拟结果视为真实订阅账户的端到端验收。

## 使用边界

迁移阶段使用本地数据库和模拟上游。后续已按用户指令部署 `https://kendeji.fun`，通过真实订阅账号完成搜索、生图和官方 harness 0.167.0 WebSocket 音频测试；升级后的 Mac 客户端也已完成麦克风问答、图片展示及历史恢复；新版 V1/V3 WebRTC 被上游账号权限拒绝。根域名使用独立数据副本，原 Codex 服务不变。部署详情与回滚位置见 [RELEASE_NOTES.md](RELEASE_NOTES.md)。

Codex 0.166 的 WebRTC sideband 默认直接访问 `api.openai.com`，只修改主 provider 的 base URL 不足以让该连接经过本网关；客户端需同时指向本网关。使用 Redis 时会话路由可跨进程共享；不使用 Redis 时需保持同一实例，重启会清空尚未结束的会话路由。

认证相关核查遵循 [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 与 [Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 的服务端身份校验、认证信息隔离和服务端到期控制。回归覆盖上游认证不受客户端替换，以及会话跨用户、跨令牌拒绝；这不是整站 ASVS 合规认证。
