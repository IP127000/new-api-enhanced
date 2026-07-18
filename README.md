<div align="center">

![new-api](/web/default/public/logo.png)

# New API Enhanced

## 新版 Codex Web Search 与图像能力增强

**通过 New API 的 Codex 订阅渠道使用新版 Web Search、`gpt-image-2` 图像生成和图像编辑。**

`/v1/alpha/search` · `/v1/images/generations` · `/v1/images/edits` · `/v1/models`

</div>

> 这个版本最重要的变化，是补齐新版 Codex 自定义提供商需要的搜索与图像接口。Web Search 使用 Codex Responses Lite 的独立搜索协议；图像生成和编辑直接转发到 Codex 订阅后端，并保留流式响应、multipart 请求和用量统计。

## 关于这个仓库

这是我维护的 New API 增强版，代码基础来自 [QuantumNous/new-api](https://github.com/QuantumNous/new-api)。上游负责通用的大模型网关、渠道管理、用户与额度系统、协议转换和管理界面；这个仓库主要补充 Codex 订阅渠道的兼容性、请求稳定性和运行数据统计。

仓库采用快照方式维护，只保留当前分支和一个回滚分支。GitHub 上的提交记录因此不会复刻上游的完整历史；原项目作者、历史贡献和版本演进请以上游仓库为准。本仓库继续遵守 AGPLv3 及项目附加条款，界面中的 New API 署名和上游链接保持不变。

<details>
<summary>上游项目入口与多语言 README</summary>

<div align="center">

# New API

🍥 **Next-Generation LLM Gateway and AI Asset Management System**

<p align="center">
  <a href="./README.zh_CN.md">简体中文</a> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <strong>English</strong> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

<p align="center">
  <a href="https://raw.githubusercontent.com/Calcium-Ion/new-api/main/LICENSE">
    <img src="https://img.shields.io/github/license/Calcium-Ion/new-api?color=brightgreen" alt="license">
  </a><!--
  --><a href="https://github.com/Calcium-Ion/new-api/releases/latest">
    <img src="https://img.shields.io/github/v/release/Calcium-Ion/new-api?color=brightgreen&include_prereleases" alt="release">
  </a><!--
  --><a href="https://hub.docker.com/r/CalciumIon/new-api">
    <img src="https://img.shields.io/badge/docker-dockerHub-blue" alt="docker">
  </a><!--
  --><a href="https://goreportcard.com/report/github.com/Calcium-Ion/new-api">
    <img src="https://goreportcard.com/badge/github.com/Calcium-Ion/new-api" alt="GoReportCard">
  </a>
</p>

<p align="center">
  <a href="https://trendshift.io/repositories/20180" target="_blank">
    <img src="https://trendshift.io/api/badge/repositories/20180" alt="QuantumNous%2Fnew-api | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/>
  </a>
  <br>
  <a href="https://hellogithub.com/repository/QuantumNous/new-api" target="_blank">
    <img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=539ac4217e69431684ad4a0bab768811&claim_uid=tbFPfKIDHpc4TzR" alt="Featured｜HelloGitHub" style="width: 250px; height: 54px;" width="250" height="54" />
  </a><!--
  --><a href="https://www.producthunt.com/products/new-api/launches/new-api?embed=true&utm_source=badge-featured&utm_medium=badge&utm_campaign=badge-new-api" target="_blank" rel="noopener noreferrer">
    <img src="https://api.producthunt.com/widgets/embed-image/v1/featured.svg?post_id=1047693&theme=light&t=1769577875005" alt="New API - All-in-one AI asset management gateway. | Product Hunt" style="width: 250px; height: 54px;" width="250" height="54" />
  </a>
</p>

<p align="center">
  <a href="#-quick-start">Quick Start</a> •
  <a href="#-key-features">Key Features</a> •
  <a href="#-deployment">Deployment</a> •
  <a href="#-documentation">Documentation</a> •
  <a href="#-help-support">Help</a>
</p>

</div>

</details>

### 增强内容

- Codex 订阅渠道在 `APITypeCodex` 与 `ChannelTypeCodex` 同时匹配时，保留 `/v1/responses` 和 `/v1/responses/compact` 的原始请求体及必要客户端请求头；订阅认证仍由服务端渠道配置提供。
- 修正 Responses 流在 `response.completed` 或函数调用结束后的误报 `client_gone`，并完善流扫描器退出和资源清理。
- 同一渠道连续出现可重试错误后，本次请求会排除该渠道并继续选择下一条可用渠道，避免一直打到同一个故障上游。
- 渠道亲和使用 `prompt_cache_key`、Session、Thread 和请求 ID 等信息做稳定选路，不再为了亲和度改写请求体。
- 使用日志和管理看板增加输入、输出、总 Token、缓存命中 Token、命中率、RPM、TPM、请求 ID 与上游请求 ID。
- 请求数以成功消费日志为准；性能指标只负责延迟和吞吐量。失败请求仍返回并写进程日志，但不再挤占使用日志表。
- 模型分析可按已有数据范围查询最近 N 天，概览页保留用量、请求数和 Token 拆分。

### 最近更新

当前代码已经合并上游 `v1.0.0-rc.21`，同时保留原有 Codex 转发、统计和看板行为。最近补上的部分主要面向新版 Codex 客户端：

- 支持 Codex 订阅渠道的 `gpt-image-2` 图像生成和图像编辑，分别转发到订阅后端的 generations 与 edits 接口；流式响应、multipart 编辑请求和用量计算走各自的处理路径。
- 支持 Codex Responses Lite 使用的 `/v1/alpha/search` 独立搜索协议。请求体保持原格式，只移除上游不接受的缓存字段；搜索结果计入输入 Token，不按模型输出倍率重复计费。
- 带增强标记的 `/v1/models` 请求会从可用的 Codex 订阅渠道读取模型目录，并保留 ETag、请求 ID 等响应头；遇到订阅凭据过期时可刷新后重试一次。
- `-compact` 模型复用基础模型的渠道能力和映射，例如 compact 请求可以使用同一条基础模型渠道，不需要再维护一套重复能力记录。
- 普通 Responses 请求会补齐 `instructions`，固定 `store=false`，并移除订阅后端不支持的 `temperature` 和 `max_output_tokens`；compact 请求只补必需字段，其他内容保持不变。
- 新建或未配置透传规则的 Codex 渠道会自动获得安全的客户端请求头透传列表；已有自定义规则不会被覆盖，客户端 `Authorization` 也不会替换服务端订阅认证。
- 修正中文界面在 `Intl.NumberFormat` 中使用 `zhCN`、`zhTW` 时的语言标签问题，避免看板数字格式化报错。

完整实现位置、行为边界和部署记录见 [RELEASE_NOTES.md](./RELEASE_NOTES.md)，rc.21 合并范围见 [MIGRATION_REPORT.md](./MIGRATION_REPORT.md)。

### Codex 本地配置示例

下面是这台机器当前 `~/.codex/config.toml` 中与本仓库有关的最小配置。模型和推理强度可以按需要调整，其他桌面偏好、插件、项目目录和 MCP 配置不需要照搬。

```toml
web_search = "live"
model_provider = "custom"
model = "gpt-5.6-sol"
model_reasoning_effort = "xhigh"
service_tier = "default"

[model_providers.custom]
name = "custom"
requires_openai_auth = false
wire_api = "responses"
base_url = "https://api.kendeji.fun/v1"
http_headers = { "x-openai-actor-authorization" = "new-api-enhanced" }
```
