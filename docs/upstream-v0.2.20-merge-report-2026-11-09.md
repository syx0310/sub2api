# v0.2.20 合并与本地适配报告（2026-11-09）

## 范围与交付状态

- 起点：`main@4939916a0eeff2aff9b08cc32a0c1a66e70ab46e`。
- 分支：`merge/upstream-v0.2.20`。只在该分支实施；不合回 main、不 push、不创建 PR、不部署。
- 固定上游 release：`v0.2.20`，提交 `dab829a4e9b7d1cc307189aea8b0319eb29e29d0`。
- 合并提交：`76a725199`；VERSION 同步 cherry-pick：`38eadf6a8`。
- release 标签中的 VERSION 仍为 `0.2.19`，另采用版本同步提交 `6b4b37db43443b5e88e424fe3c69602cc3810dfb`，不追随移动的 upstream/main。
- 采用批准的 [合并计划](upstream-v0.2.20-merge-plan-2026-11-09.md)。原始范围为 112 个提交、459 个文件；29 个文本冲突逐块解决，不按整文件 ours/theirs 覆盖。
- 没有访问真实模型、消费 credits、创建 OAuth 授权、自动充值或执行充值后操作。集成检查仅使用隔离测试数据库和 Redis。

## 合入的主要更新

| 范围 | 新行为与边界 |
|---|---|
| 管理 MCP | OAuth 授权、同意页、连接管理和受限管理工具。包含 S256 PKCE、resource 绑定、授权码/refresh 重放失效、撤销和只读权限。`MCP_PUBLIC_URL` 未配置时保持关闭；issuer/resource 来自部署配置，不来自本地入口观测或转发头。 |
| 管理员 Key | 增加统计、OpenAI OAuth、邀请等权限；全权兼容模式需显式确认，不自动升权。保留受限 Key 的响应裁剪及本地并发接口 `ops.read`。 |
| 邮箱黑名单 | 最多 20,000 条精确条目、Gmail 别名规范化、保存时可选禁用匹配用户。本次未配置名单或禁用用户。 |
| 资源治理 | 正文缓冲预算、读取/写入停滞超时、有界 SSE 队列/事件、断连后有限 drain、有界会话缓存与通知队列；包含异步图片预留及 failover 补丁预算回收。 |
| 热路径 | 减少 JSON 重复解码、拷贝和增量字符串累积；鉴权摘要升级 v27，加入分组隐私要求、用户/分组 RPM 覆盖值，保留本地目录 TTL。异步记账先快照再入队。 |
| OpenAI | 空 instructions 放行与原值回显、逐轮 TTFT、WS→HTTP 桥接指纹、强制路由免探测、cyber 历史前缀长度精确匹配。额外适配见下一节。 |
| 图片 | GPT Image 2.6 原生生成/编辑参数、1K/2K/4K 账号分流、未指定尺寸的偏好、溢出选号不覆盖既有粘性，以及账号测试修正。保留专用图片端点与混合文本/图片请求、按张与按 token 计费的区别。 |
| 其他供应商 | setup-token 每周额度、Antigravity 实际配额窗口/Argon/测试提示词/图像 INTERNAL 500、Kimi 并发 403 短冷却、Grok 委派消息与 Voice 读错分类、Gemini 工具媒体/直出流/schema、Cloudflare beta、Bedrock 帧长度保护。 |
| 导出 | Usage CSV 每页 1000 条，429 按 Retry-After 有限重试；新 Codex/CC Switch 导出默认 GPT-6.1 Sol/live 搜索并保留图片配置。不修改既有客户端配置，不因此给实际请求增加搜索权限。 |
| 依赖 | Vue I18n/compiler 11.4.16、follow-redirects、brotli 1.2.7、compress 1.20.2、MCP SDK 1.4.1 等。保留 Node ≥22、构建/CI Node 24、CSP/runtime 测试、Docker 法务文档构建和自有发布流程。 |

## 客户端语义：实际改变的逻辑

对照客户端 `origin/main=25f4940d4affcf8c60f96ffec5e3752e1f590b8a`，读取最新引用中的文件，而不是落后的工作树。未修改客户端工作区。最新稳定 tag 为 `rust-v0.167.0`，但本轮计划不升级默认身份，内置 UA/version 仍为 `0.166.0`。

### 1. instructions：保持放行，新增原值回显，不扩大提示词注入

- 空 instructions 放行与上游新方向一致，但本地合并前已经没有该 403 拒绝分支；不是本轮新修复的本地错误。本轮实际新增的是以下回显恢复。
- 本地 HTTP OAuth 标准化策略保持不变：需要的字段可补为空字符串，原有 system/developer 搬运规则仍在；不因为 Sol/Terra 描述中有 `instructions_template` 就新增默认 prompt。
- 回显按客户端原值恢复：原来缺失而上游字段被补写时回显 `null`；显式空串、空白、多行字符串及其他原始 JSON 值分别保存。未发生改写时不替换上游回显，不为原本没有该字段的响应凭空添加字段。
- 上游修复主要覆盖 HTTP→HTTP。本 fork 补齐 HTTP→上游 WS 的流式/非流式回显；原生 `ctx_pool` 的可选图片桥接会追加指令，因此在每次 create 保存独立快照，重试和 WS→HTTP 桥接复用本轮快照，不在连接级 Gin context 中反复覆盖。
- 这不启用新的传输转换：公共 handler 显式标记为 HTTP 的请求仍强制走 HTTP；上述 HTTP→WS 回归针对保留的 legacy/unknown-transport 内部分支。
- 原生 passthrough 没有新增 instructions 改写，继续透明传递。多轮回归检查它不会继承上一轮回显。
- 回显修正发生在最终下发字节统计前，不用上游改写前/后的错误长度计算 Usage Body Size。

### 2. WS→HTTP 桥接：指纹与逐请求身份一致

- 采用桥接路径的指纹 staging，但复用本地策略：默认 device，仅统一设备；session/full 仍需显式选择。
- 请求正文已在入站路径按账号和 API Key 隔离，桥接不重复做 namespace 哈希。
- HTTP 出站的 `x-codex-turn-metadata` 及相关头从本轮最终 body 投影；内嵌 JSON 是规范身份，不让旧握手值覆盖当前子线程。
- 对照客户端确认 `x-client-request-id` 也是 thread_id 的投影，一并按本轮修正。后续轮次缺失的身份/compact 标记不会从首轮握手恢复。
- 不因此补 `prompt_cache_key`；显式 key 及既有本地范围保持不变。

### 3. 子代理历史跨协议转换

- Grok 新增的 agent_message 支持复用本地兼容转换器：完全明文的输入按原顺序转换为消息。
- 最新客户端 `codex-rs/protocol/src/models.rs` 的 `plaintext_agent_message_content` 对含 encrypted_content 的混合输入返回 None，测试也明确禁止将其当明文。
- 因此不采用上游把 encrypted_content 字符串直接拼成任务正文的行为。密文子代理历史需支持原生 Responses 的上游；跨协议转换明确报错，不静默丢弃、也不把密文当正文。

### 4. Antigravity 与 TTFT

- Antigravity 只替换系统块开头的已知客户端身份句，并删除已有的 attribution 元数据行；身份句后面的任务内容、独立任务块、普通内容中提及 Claude/Codex 的文字仍保留。
- 上游逐轮首 token 时间修复与本地 terminal/error 判定合并，保留裸 error 快速收尾、error/response.failed 一次结算、多条 accepted steer 审计和自动后继归因。

## 明确保留、未采用及去重

### 模型与价格

- Terra 内嵌目录与最新客户端、上游 tag 完全相同：SHA256 `7afc1b34a6a0fe874f64e922984e38eef039bdfc4d05b66a99ca9fc44f16c37d`，使用共同描述，不另维护一份。
- 保留 Codex `low` 默认、500,000 默认/872,000 最大上下文、Lite、code_mode_only、multi_agent v2、ultra 和动态 effort 更新能力。
- 保留已批准的显式 `none/minimal → low`、`ultra → xhigh`；缺失 effort 不注入。不采用上游 Terra minimal→400，不把目录元数据当请求 prompt。
- 不采用上游 Terra OAuth/setup-token 长上下文豁免：继续保留本地 Terra 计费，Sol 订阅的既有豁免不变；保留组/账号 AND 门控、standard/fast/flex、自定义零价格及 12h 缓存写入计费。
- OpenAI Docs 核对影响了这项选择：[Terra API 文档](https://developers.openai.com/api/docs/models/gpt-6.1-terra) 仍有 API 长上下文倍率，[Codex/Work 价格页](https://learn.chatgpt.com/docs/pricing) 明确的 Sol 订阅说明不能外推为 Terra。客户端窗口大小也不能证明豁免。

### 本地功能

- credits：被动观测、管理员显式允许、历史最新正余额允许尝试调度、耗尽锁定/手动重新授权、普通编辑与数据库锁/CAS 保护、调度摘要字段、握手后复检均保留。未增加主动轮询、充值或自动重新授权。
- OAuth catalog：分组可配 60/300 秒默认、单正文缓存、较大目录可缓存、Pro/Spark 汇总与能力隔离均保留。
- HTTP 增量锚点仍要求完整历史重放；不删除 previous_response_id 后拿残缺历史生成。加密 compaction/summary 的公共清理和失效记录保护、原生 trigger/Lite additional_tools/搜索历史兼容、显式工具选择保护均保留。
- 保留 `stream_options.reasoning_summary_delivery`，不机械删除 Responses 的 stream_options。
- ingress：JSONB、默认不校验代理、多 IP/CIDR、Peer/对端展示、异步快照保留；客户端 IP 解析不变，入口观测不参与 MCP 或其他鉴权。
- 不恢复 turn-state 长度采集/DTO/UI/日志。实际 opaque 协议值继续转发，旧数据库列及历史数据保留。
- `pN` 版本比较、插件本机兼容门控、并发批量/稀疏接口、Body Size、Token/s、首页 $/min 与 TPM 同行同频、Command Code 凭据相关缓存边界均保留。
- 三项已提前 cherry-pick 的非流式 JSON/终态/SSE framing 修复随 tag 收敛，不重复施加；依赖安全升级采用上游补丁版本而不重新维护平行实现。

## 资源限制的实际影响

正确配置前缀是 `gateway.*`，不是 release 文案中的 `security.*`：

```yaml
gateway:
  request_body_memory_budget_bytes: 1073741824
  request_body_read_timeout_seconds: 120
  downstream_write_timeout_seconds: 120
```

- 保留上游默认及可配置能力；0 禁用对应限制，未改生产配置/容器 limits。
- 1 GiB 是纳入计数的 ingress 缓冲预算，不是整个进程 heap/RSS 上限，也不是所有协议转换都具有精确内存计数。预算不足返回 503+Retry-After。长 SSE 生命周期会延长其请求缓冲的占用。
- 本地原始/解压后 Body Size 统计接入同一读取实现，不另读一遍、不重复计数或释放 preread 借用缓冲；测试覆盖 identity/gzip/deflate/zstd、二次读取、路由替换与归还。
- 120 秒限制单次上传/下游写入停滞，不限制总推理时长。成功读写后清除 deadline；保持 HTTP/1 超时与客户端取消的错误区分。
- HTTP 断连后 drain 默认 180 秒，OpenAI 图像 900 秒；ping 不延长总窗口。超过窗口的最终 usage 可能丢失，不承诺有界清理还能无限等待完整账单。原生 WS 裸 error 的快速收尾规则不变。
- 会话缓存每缓存最多 16,384 项/32 MiB 记账预算，保留原隔离键；SSE 默认 8 MiB 队列允许一个合法大图片事件独占，不等同于整份响应最大 8 MiB。
- 异步记账先提取值快照再入队；新增真实 handler/Gin context 复用回归，验证入口 A 不会被下一请求入口 B 覆盖。
- 未重做已有 Key 并发聚合专项压测；本轮测试聚焦正文预算、取消/归还、超时、队列、会话淘汰和 worker 生命周期。

## 数据库与验证记录

- 同时保留 `249_mcp_oauth.sql`、`249_request_ingress_observation.sql` 原名和原 checksum。迁移身份是完整文件名，不以数字前缀或最大编号跳过。
- 新增真实 PostgreSQL 两阶段用例：先应用本地 249、写入 ingress/历史长度数据，再加入排序更靠前的 MCP 249；验证两次重复迁移、两条 checksum、MCP 表无自动授权及既有数据不变。全新库通过完整集成迁移检查两份 249。
- Ent/Wire 生成检查；Ent 不需要新增模型，MCP 表按上游原生 SQL 实现。
- 验证使用 Go 1.27.3、Node 24.22.2、pnpm 9；Go `GOMAXPROCS=2 -p 1`，前端单 worker，高内存构建错峰。

已完成的验证：

| 检查 | 结果 |
|---|---|
| 后端全量 `go test -json -tags=unit -p 1 ./...` | 62 个有测试的包、27,015 项测试/子测试通过，17 项既有条件跳过，0 失败；含真实隔离 PostgreSQL 的 scoped-admin/迁移用例。 |
| PostgreSQL/Redis repository integration | 1,667 项测试/子测试通过，1 项既有 `TestGetAccountsLoadBatch` TODO 跳过。 |
| 正文预算/WS relay 定向 race | 58 项通过，0 race；包含 preread、预算、TTFT、steer、裸 error 与 drain。 |
| service/handler/repository 定向 race | 92 项通过，0 race；包含 credits、调度缓存摘要、目录 TTL、WS/HTTP 回显、身份投影、Gin context 复用和双 249 迁移。 |
| 前端全量 Vitest | 389 个文件、3,140 项通过，单 worker。 |
| 前端 lint、`vue-tsc -b`、Vite build | 均通过。构建仍提示既有 Browserslist 数据较旧、部分 chunk >500 kB，未借此全面更新依赖或调整分包。 |
| Go 漏洞扫描 | 0 可达漏洞、0 已导入包漏洞；6 项所需模块级告警未发现本代码调用。 |
| Go lint | golangci-lint 2.14.0 全量检查通过，0 issues；`GOFLAGS=-p=1 --concurrency=1`。 |
| 生产前端依赖例外校验 | 通过，未扩大/延期例外。 |
| 发布辅助脚本 | 11 项通过。 |
| Ent/Wire | 生成验证通过；Ent 无差异，Wire 与上游新增依赖保持一致。 |
| 嵌入前端的后端构建 | `go build -p 1 -tags=embed ./cmd/server` 通过，未启动服务或构建/推送镜像。 |
| embed 模式测试 | `go test -tags=unit,embed ./internal/web ./cmd/server`，131 项通过。 |

首轮测试曾发现旧 Antigravity 身份预期、上游 Grok 将密文当明文的测试预期，以及新增测试未选择 legacy WS 路由的夹具问题；均修正并在最终全量中通过，没有放宽实际协议约束。验收中也明确忽略了测试连接关闭错误。全量通过后，lint 发现并清理了一个已无调用的旧模型判断 helper；删除后定向 race、嵌入构建和最终全量 lint 均通过，未再改变运行逻辑。

日志保存在 `/tmp/sub2api-v0220-merge.Ce15At`；全量最终记录为 `backend-unit-final.jsonl`。该目录仅用于本轮验证，不是运行时依赖。

本轮自建 `sub2api-v0220-scoped-tests` PostgreSQL 容器及其匿名卷已删除，其中仅有可重建的测试数据；不是生产数据备份。集成套件的 PostgreSQL/Redis 由 testcontainers 自动回收。未操作其他项目容器。

## 安全审计边界

- 生产依赖 audit：0 critical、2 high、12 moderate、2 low。2 个 high 为既有 xlsx 例外，原例外校验通过，未扩大/延期例外。
- 包含开发依赖的完整 audit：3 critical、34 high、37 moderate、4 low。critical 来自原有 Vitest/tinypool 测试工具链；所有被报告的包版本均存在于合并前 main 的 lockfile，非本次新增。没有启动对外测试 UI，也没有在本轮擅自跨主版本升级整个前端工具链。
- “功能验证通过”和“依赖零漏洞”是不同结论；本报告不宣称后者。
