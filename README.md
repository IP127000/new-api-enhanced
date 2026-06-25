# New API Enhanced

语言：**中文** | [English](./README.en.md)

New API Enhanced 是基于 `QuantumNous/new-api` 的下游增强发行版，面向 Codex 和
ChatGPT Subscription 渠道的生产代理场景，提升流式响应稳定性、订阅渠道缓存命中率和
缓存指标可观测性。

本仓库独立维护，不是上游 New API 官方仓库。原 New API、QuantumNous 署名、原项目链接、
NOTICE 和 AGPLv3 许可义务均保留。

## 核心增强

1. **Codex 流式响应稳定性**：修正 Responses API 在工具调用边界的结束判定，避免正常流结束被记录为异常断流，并减少 `client_gone`、`scanner error` 等误报。
2. **订阅渠道缓存命中优化**：为 ChatGPT Subscription (Codex) 请求保持渠道亲和，并同步 `Session-Id`、`Session_id` 与 `prompt_cache_key`，降低同一会话跨渠道转发或缓存身份不一致造成的缓存损失。
3. **订阅渠道故障转移优化**：同一请求内按渠道逐个消耗最大重试次数，失败耗尽后切换到下一个可用渠道；已耗尽渠道不会在本次请求内反复命中，成功后亲和缓存更新到最终成功渠道。
4. **缓存指标可观测性**：在使用日志中展示缓存命中 token 与命中率。

## 使用方式

### Docker Compose

```bash
git clone https://github.com/IP127000/new-api-enhanced.git
cd new-api-enhanced
docker compose up -d --build
```

启动后访问：

```text
http://localhost:3000
```

默认 `docker-compose.yml` 会从当前仓库构建镜像，并启动 New API Enhanced、PostgreSQL 和 Redis。
生产部署前应更换数据库密码、Redis 密码和 `SESSION_SECRET`。

### Docker

```bash
git clone https://github.com/IP127000/new-api-enhanced.git
cd new-api-enhanced
docker build -t new-api-enhanced:latest .

docker run --name new-api-enhanced -d --restart always \
  -p 3000:3000 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)/data:/data" \
  new-api-enhanced:latest
```

如需连接 MySQL、PostgreSQL 或 Redis，可通过环境变量配置：

```bash
SQL_DSN="user:password@tcp(mysql:3306)/new-api"
REDIS_CONN_STRING="redis://:password@redis:6379"
SESSION_SECRET="replace-with-a-random-secret"
```

### 升级

```bash
git pull
docker compose up -d --build
```

私有主机解析、内网地址、日志目录、代理参数、密钥和订阅凭据应保存在服务器本地配置、
Compose 覆盖文件、环境变量、密钥管理器或部署平台中，不应提交到公开仓库。

## 运维建议

- ChatGPT Subscription (Codex) 渠道建议开启渠道亲和，并确认分组、模型和渠道能力状态一致。
- 如果数据库中已有自定义 Codex 亲和模板，请确认模板包含 `Session-Id`、`Session_id` 与 `prompt_cache_key` 的同步逻辑。
- 多个订阅渠道承载同一模型时，建议设置清晰的优先级；故障转移会在当前请求内跳过已耗尽渠道，并在成功后刷新亲和目标。
- 生产环境建议启用 Redis；多节点部署应使用一致的 `SESSION_SECRET`。
- 公开仓库不应包含 API 密钥、订阅凭据、私有主机解析、内网地址、私有部署命令或服务器交接文档。

## 验证

```bash
go test ./model ./service ./controller
go test ./relay/...
```

服务启动后可检查：

```bash
curl http://localhost:3000/api/status
```

## 引用与合规声明

New API Enhanced 是基于 `QuantumNous/new-api` 的修改版本。上游原项目地址：

```text
https://github.com/QuantumNous/new-api
```

本项目依据 GNU Affero General Public License v3.0 发布。任何部署、修改、再分发或通过网络向用户提供服务的行为，均应遵守 AGPLv3 的条款，包括在网络服务场景下向用户提供对应修改版源代码的义务。

本项目保留上游 NOTICE 中的 AGPLv3 Section 7 附加条款。包含用户界面的修改版本应保留合理法律声明、作者署名和原项目链接，包括但不限于：

```text
Frontend design and development by New API contributors.
https://github.com/QuantumNous/new-api
```

修改版本不得误导软件来源，应明确标识其为下游修改版本，并保留 New API、QuantumNous、原项目链接、LICENSE、NOTICE 和第三方许可证声明。本仓库中的 New API Enhanced 标识仅用于说明本下游发行版的增强内容，不代表上游官方发布。

本项目仅面向合法授权的 AI API 网关、组织内部鉴权、多模型管理、用量统计、成本核算和私有化部署场景。使用者应自行确认已获得上游 API、账号、密钥、额度、模型服务和相关接口权限，并遵守上游服务条款、平台规则及所在地法律法规。面向公众提供生成式 AI 服务时，使用者应自行完成适用的备案、许可、内容安全、实名、日志留存、税务、支付和上游授权等合规义务。
