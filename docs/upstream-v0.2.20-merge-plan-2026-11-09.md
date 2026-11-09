# v0.2.20 合并计划（2026-11-09，仅计划）

## 1. 已完成的提交与推送

- 现有版本检查、入口观测、turn-state 长度清理，以及 `Peer` /「对端」文案，已提交为 `4939916a0eeff2aff9b08cc32a0c1a66e70ab46e`。
- `feature/version-ingress-cleanup` 已推送自有 `syx0310/sub2api`；随后 fast-forward 合入 `main` 并推送。
- 已通过远端引用核对，两分支均指向该提交；没有创建 PR，没有向上游写入。
- 收尾复核：Go 版本/入口包测试通过，前端 Usage/i18n 40 项通过，发布辅助脚本 11 项通过；此前完整验证见 `version-ingress-turn-state-cleanup-2026-11-04.md`。

## 2. 本次合并目标与范围

- GitHub 最新正式 release：[v0.2.20](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.20)，发布时间 **2026-11-07 14:31:24（北京时间）**，非 draft、非 prerelease。
- tag 提交：`dab829a4e9b7d1cc307189aea8b0319eb29e29d0`。
- 相对已合入的 v0.2.19：**112 个提交，其中 72 个非 merge 提交；459 个文件，29,482 行新增、1,457 行删除**。已检查完整提交范围，不只根据 release 摘要挑补丁。
- tag 内 `backend/cmd/server/VERSION` 仍为 `0.2.19`。计划另行采用上游版本同步提交 `6b4b37db43443b5e88e424fe3c69602cc3810dfb`，明确它是 tag 之后的版本修正，不把未来 upstream/main 的其他变化带进来。
- 只读 `git merge-tree` 预检发现 **29 个文本冲突文件**。该预检只生成 Git 对象，未 checkout 合并树、未改 index、未进入实际 merge 状态。
- 获批后从 `main@4939916a0` 新开 **`merge/upstream-v0.2.20`** 实施。当前还没有创建此分支或合入 v0.2.20。

## 3. 上游更新与采用方式

| 更新组 | 本次处理 |
|---|---|
| 管理 MCP | 合入 OAuth 授权、连接管理、权限受限的管理工具及配套 UI。保持 `MCP_PUBLIC_URL` 未配置时关闭，不替用户启用服务或授权工具。验证 PKCE、resource 绑定、撤销/refresh replay、只读权限及普通推理 Key 不能变成管理凭据。 |
| 管理员 Key | 采用统计、OpenAI OAuth、邀请等权限补齐，以及需显式确认的全权兼容模式。默认不升权；保留本地并发接口的 `ops.read` 权限和受限响应裁剪，覆盖新 `RestrictedFromContext` 分支。 |
| 注册邮箱黑名单 | 采用最多 20,000 条的精确名单、Gmail 点号/+后缀规范化、保存时可选禁用命中用户。默认不添加名单，不执行批量禁用。 |
| 请求与资源限制 | 采用正文内存预算、读写停滞超时、有界 SSE 事件/队列、有限断连后读取、会话缓存和后台通知队列限制；包含异步图片预留与 failover 补丁预算修复。必须与本地变换/计费/WS 生命周期一起验收。 |
| 热路径优化 | 采用减少 JSON 拷贝/解码、Gemini 增量输出拷贝，以及 RPM override、分组隐私门控进入鉴权快照。保留本地目录 TTL 投影等扩展字段，完成快照版本升级和失效验证。 |
| OpenAI 协议 | 采用空 instructions 不再本地 403、按客户端原值恢复 instructions 回显、WS HTTP 桥接指纹补齐、逐轮首 token 时间、强制路由不再额外探测，以及 cyber 历史前缀长度精确匹配。按第 5 节保留本地语义边界。 |
| 图像 | 采用 GPT Image 2.6 原生生成/编辑参数、1K/2K/4K 账号分流、未指定尺寸时的偏好规则、溢出选号不覆盖原有会话粘性，以及账号测试修复。保留本地混合请求/专用图片端点和按张/token 计费边界。 |
| 其他供应商 | 采用 setup-token 每周额度、Antigravity 真实配额窗口/Argon 单独显示/真实测试提示词/按图像模型处理 INTERNAL 500、Kimi 并发 403 短冷却、Grok 委派消息与 Voice 读错分类、Gemini 工具媒体/直接流/schema、Cloudflare beta 参数及 Bedrock 帧长度保护。 |
| 导出与通用修复 | 采用用户 CSV 每页 1000 条及 429 按 Retry-After 重试、Codex/CC Switch 新导出模板默认 GPT-6.1 Sol 和 live 搜索并保留图片配置；不改用户已保存客户端配置，也不让代理自行增加可调用搜索工具。采用 reasoning/message content 保真修复和 413 规则优先级修复。 |
| 依赖安全 | 采用上游 Vue I18n / compiler 11.4.16、URI decoder/follow-redirects 修补、brotli 1.2.7、compress 1.20.2、MCP SDK 1.4.1 等范围内升级。保留本地 Node ≥22、CI/Docker Node 24、CSP/runtime 回归及法务构建，不全面刷新无关依赖。 |

## 4. 内存与超时：必须按代码而非 release 文案配置

上游 release 将三个新配置写为 `security.*`，但 tag 中 [Config 默认值](https://github.com/Wei-Shaw/sub2api/blob/v0.2.20/backend/internal/config/config.go) 与 [EDGE_SECURITY.md](https://github.com/Wei-Shaw/sub2api/blob/v0.2.20/deploy/EDGE_SECURITY.md) 实际为：

```yaml
gateway:
  request_body_memory_budget_bytes: 1073741824
  request_body_read_timeout_seconds: 120
  downstream_write_timeout_seconds: 120
```

- 计划保留上游这些可配置能力及默认值；不猜测或修改生产容器的实际 limits。`0` 可禁用对应限制。
- 1 GiB 是进程内被纳入计数的正文缓冲预算，不是总 Go heap / RSS 的硬上限。正文可能在长 SSE 生命周期内一直占用预算；耗尽返回 503 + Retry-After。
- 120 秒分别限制一次上传读取/下游写入停滞，不是把模型总推理时间截为 120 秒。测试成功读写后清除 deadline、长思考仍可存活、HTTP/1 与 HTTP/2 的 408/499 区分。
- 断连后 HTTP drain 默认按流数据超时取 180 秒；OpenAI 图像有独立的 900 秒默认/回退。只有客户端取消后才启动总窗口，ping 不续期。到期后的最终 usage 可能取不到，不能宣称“资源有界且完整账单必不丢”。不把这个窗口套到本地原生 WS 裸 error 的快速收尾规则上。
- 会话内存缓存上游限制为每缓存 16,384 项/32 MiB 的记账预算；保留本地账号/API Key/thread 隔离键。淘汰后必须显式失败或按合法机制恢复，不能删除 compaction 或续接锚点后继续使用残缺历史。
- SSE 队列、事件和单帧是不同限制；默认 8 MiB 队列允许一条较大的合法图片帧独占，不能简单理解为所有响应均最多 8 MiB。
- 本地 body 修改、重试、HTTP↔WS 桥接和异步图片任务要纳入正确的分配/释放生命周期；不得在 handler 返回时释放后台任务仍需使用的预留，或在 failover 中累加同一旧补丁的预算。
- 上游将异步记账改为先快照再排队，预检结果显示可继续复用本地 `usageRecordContext` 保留 ingress。采用该时机优化，测试只保留值快照，不引用已复用的 Gin context 或整份请求正文。
- 不重新做既有 Key 并发聚合的专项性能测试；本轮性能验证聚焦新正文预算、慢读写、有界队列、会话淘汰及取消/释放。

## 5. 客户端语义与明确保留的本地规则

客户端已 fetch 到 `origin/main=25f4940d4affcf8c60f96ffec5e3752e1f590b8a`（2026-11-09），只读该引用，没有 pull/改动客户端工作树。最新稳定 tag 为 `rust-v0.167.0`；main 的 Cargo 版本是 `0.0.0`，不能拿来更新默认 UA。本计划不顺带调整内置 UA。

### Terra：同一份目录，但不同的计费/effort 决策

- 最新目录已移到客户端 `codex-rs/models-manager/models.json`。Terra 描述与本地嵌入描述逐字段相同；上游 v0.2.20 的 `codex_gpt61_terra.json` 与本地文件 SHA256 也完全相同：`7afc1b34a6a0fe874f64e922984e38eef039bdfc4d05b66a99ca9fc44f16c37d`。直接采用这一份共同描述，不再增加平行定义。
- 保留 Codex 默认 `low`、500,000 默认/872,000 最大窗口、Lite、code_mode_only、多代理 v2/ultra、动态 effort 更新等现有能力。不能把 API 文档的 1.05M/medium 覆盖成客户端默认。
- 保留已批准的显式 `none/minimal → low`、缺失不注入、`ultra → xhigh`。上游新增的 Terra `minimal → 400` 与之不同，不直接采用；这是 fork 的兼容策略，不是声称官方接受 minimal。
- 上游把 Terra OAuth/setup-token 加入长上下文附加费豁免。**本计划暂不采用该计费行为变化**：官方 [Terra API 文档](https://developers.openai.com/api/docs/models/gpt-6.1-terra) 仍规定 >272K 的 API 倍率；官方 [Work/Codex 价格页](https://learn.chatgpt.com/docs/pricing) 明确写出 Sol 的订阅豁免，但本次未找到同样明确的 Terra 声明。客户端 500k 窗口无法证明计费豁免。继续保留目前 Terra 的本地规则、组/账号开关 AND、自定义定价和 12h 缓存记账；若希望改成上游豁免，应作为单独的计费策略确认。
- 上游扩展 GPT-6 兼容路径的自动 prompt_cache_key。本地当前范围更窄，不能视作等价去重：保留调用方显式 key 和本地注入边界，不因新模型识别而扩大到原生 Codex 请求。

### 指令、身份、续接与错误终态

- 最新客户端 HTTP 请求结构没有 `previous_response_id`；WS 结构有。保留 HTTP 带锚点的增量请求要求完整重放的错误，不删锚点继续生成，也不假设上游 HTTP 支持 WS 续接。
- 最新客户端说明 `client_metadata["x-codex-turn-metadata"]` 是规范身份，平铺字段和头只是投影。桥接指纹补齐必须接入本地逐请求身份解析；保留 device 默认启用、session/full 默认关闭，不能按账号折叠子代理 thread，不能将首轮 compaction 标记继承到后续普通轮次。
- HTTP 与 WS 均支持客户端定义的 stream_options；保留本地针对 `reasoning_summary_delivery` 的语义，而非机械删除。
- 采用空 instructions 放行与回显修复，但不因此统一注入 prompt；继续保留 system/developer/instructions 本地策略。缺失、null、空字符串、输入历史中的指令分开处理。若 WS 路径需要回显恢复，必须按请求/响应轮次保存，不使用连接级可变值串轮。
- 保留原生 trigger、Lite additional_tools、历史 hosted web_search 的压缩适配及显式 named/required/allowed_tools 选择；加密 compaction/summary 不能作为恢复手段静默删除。
- 合入逐轮 TTFT 的同时，保留 bare error 后的有限清理、error/response.failed 不重复结算、多条 accepted steer 的完整审计、自动后继归因，以及逐轮 Key/分组/PricingAt 快照。
- 采用 Antigravity 身份前导修复，但保留携带任务内容的前导和本地角色策略，不扩大为通用 system/developer 删除。

## 6. 可以去重与不能被覆盖的功能

可收敛到上游主体：

1. `6b2fdfcac`、`9b5407449`、`4db8402b5` 三个非流式 JSON/终态/SSE framing 补丁已提前 cherry-pick；本次正式纳入 tag，不再重复打补丁。原 v0.2.19 VERSION 同步提交也已等价存在。
2. Terra 官方嵌入描述相同，采用共同文件；保留本地模型身份匹配、能力覆盖和回归测试。
3. Vue I18n v11 与 URI decoder 安全迁移方向一致，采用上游后续补丁版本；保留本地 Node/CSP/运行时与实际译文测试，不退回 v9 或旧构建要求。
4. 请求处理与异步记账的资源优化采用上游实现，不另建一套同作用预算/队列机制；适配本地字段和生命周期。

继续保留：

- Codex OAuth 目录 60/300 秒默认且分组可配、单正文缓存，避免 >512 KiB 目录因两份正文超过 1 MiB 而无法缓存；Pro/Spark 母子账号目录汇总和能力隔离。
- credits 被动观测、显式授权、历史最新正余额允许尝试调度、耗尽锁定、普通编辑/刷新/CAS/调度摘要保护、握手后检查；不新增主动高频拉取、自动充值或耗尽后自动重新授权。
- ingress 的 JSONB、默认不校验来源、多 IP/CIDR、Peer 展示与异步快照；客户端 IP 解析原行为不动，入口值不能用于 MCP issuer/resource 或其他鉴权决策。
- 不恢复已删除的 turn-state 长度采集/DTO/UI；保留真实协议值转发及历史列。
- `pN` 版本比较和插件启动/恢复的本机兼容校验。
- 并发批量/稀疏聚合接口、Usage Body Size/Token/s、首页 $/min 与 TPM 同行同频。
- Docker 法务文档构建和自有 release 流程；不重新开放任何已限制的隐私/权限/日志内容。
- 保留 Command Code 协议缓存的凭据身份边界、模型别名精确识别、已配置服务等级及自定义价格。

## 7. 数据库、冲突和验收步骤

1. 新建 `merge/upstream-v0.2.20`，合并固定 tag，再应用固定 VERSION 同步提交。先确认工作区和起点未变化。
2. 处理 29 个文本冲突；重点为 httputil/body、OpenAI 请求/响应/计费、模型目录、兼容转换、WS relay 和前端依赖。自动合并的鉴权快照、记账 context、后台队列、Wire 和权限目录也要复查。
3. 同时保留 `249_mcp_oauth.sql` 与已推送的 `249_request_ingress_observation.sql`。迁移记录以完整 filename 为主键，不因数字相同而改名或修改已落库迁移的 checksum；验证全新库和已执行本地 249 的升级库。
4. 采用鉴权摘要 v27，保留本地目录 TTL 等字段及 scheduler credits 摘要。验证旧缓存失效、新旧实例混跑、更新权限/分组后的刷新。
5. 定向回归：HTTP/SSE/native WS/passthrough/ctx_pool/HTTP bridge、空指令/复杂工具历史/compact/加密摘要、跨线程隔离、TTFT/steer/error、目录缓存边界、credits、图片分流与计费、MCP 权限及入口观测。
6. 资源验收：大请求/压缩输入/慢读写/断连 drain/failover/异步图片/worker 满载与关闭；验证预算归还、不会 double release 或死锁、不按无界响应分配内存，并说明迟到 usage 的限制。不对真实模型或生产账号压测。
7. 全量后端单测、隔离 PostgreSQL/Redis 集成、定向 race、前端测试/类型检查/lint/build、Go lint/vuln 与发布辅助脚本；Ent/Wire 生成无遗漏。Go 单包编译、前端单 worker，高内存任务错峰，结合实际内存再调整测试并发。
8. 交付逐项逻辑变更、采用/保留/未采纳列表和测试结果。完成后留在合并分支；是否再合回 main、push 或构建镜像，按届时用户指令处理。

## 当前结论

建议按以上边界合入 v0.2.20。资源治理、协议保真和安全更新有价值，但这不是可直接 `ours/theirs` 整文件覆盖的合并。当前只完成既有功能的 commit/main/push 和上游静态分析，未执行新的上游合并。

本次诊断临时记录：`/tmp/sub2api-v0220-plan.4ex08z/merge-tree.txt`。本计划为本地文档，未在上述 main 提交之后额外推送。
