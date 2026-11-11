# v0.2.21 合并计划（2026-11-11，仅计划）

## 1. 本轮已完成：提交、合回 main、推送

- 现有 v0.2.20 合并成果及默认 Codex TUI 身份 `0.167.0` 已进入 `main`。
- 本轮新增提交：`84593bd3471c93c23d5b7ea46ae78dca9ad4cf9e`，`chore: bump default Codex TUI identity to 0.167.0`。只提交了原有四处 UA/版本常量、测试、前端占位文案改动。
- 先推送 `merge/upstream-v0.2.20`，随后 `--ff-only` 合入 main 并推送。远端 `syx0310/sub2api` 的两个分支均已核对指向该提交。
- 没有创建 PR，没有向 `Wei-Shaw/sub2api` 写入，没有构建或推送镜像。
- 本轮提交前复验：后端身份/版本定向单测通过，前端两份 i18n 测试文件共 5 项通过。v0.2.20 的此前整体验证记录见 `docs/upstream-v0.2.20-merge-report-2026-11-09.md`；不将它冒充为 v0.2.21 的验证结果。

## 2. 固定合并目标

- 最新正式 release：[v0.2.21](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.21)，**2026-11-11 10:21:55（北京时间）**发布，非 draft、非 prerelease。
- tag 对应提交：`10ba8f9b4dc2e6023d4a9879ab77b21841babd10`。
- 相对 v0.2.20：**82 个提交，其中 44 个非 merge 提交；200 个文件，新增 6,699 行、删除 541 行**。已检查完整非 merge 提交清单、主要实现及本地交叉修改，不只读发布摘要。
- 其中 v0.2.20 的版本同步提交 `6b4b37db4` 已在本地等价存在；`git cherry` 显示 1 个等价补丁、43 个尚未等价合入的非 merge 提交。
- v0.2.21 tag 内 `backend/cmd/server/VERSION` 仍写 `0.2.20`。实施时单独采用上游后续的纯版本同步提交 **`6dd5a045b62e27e5b5b139adef91a171097f639b`**。本次检查到的另一个 tag 后提交是 sponsors 更新，不把整个 upstream/main 当作合并目标。
- 只读 `git merge-tree --write-tree main v0.2.21` 发现 **25 个文本冲突文件**；未修改 index/工作树，未进入实际 merge。
- 获批后从 `main@84593bd34` 新开 **`merge/upstream-v0.2.21`**。当前没有创建该分支，也没有合入 v0.2.21。

## 3. 上游更新：采用范围

| 更新组 | 计划处理 |
|---|---|
| Claude Fable / Mythos 5.2 | 采用模型列表、Anthropic 预设、Bedrock 映射、OpenCode 配置、Codex 兼容目录描述及转换校验。采用上游新增的默认兜底价卡（输入/输出 $8/$40 每百万 token，Fast 2 倍），不覆盖管理员自定义价格。保留签名 thinking，按目标模型校验 adaptive thinking、采样参数和 forced tool choice。 |
| OpenAI 缓存写入计费 | 采用实际 usage 的 30m/12h 分桶、独立 12h 价卡字段、区间价格、分组/渠道/账号统计和 Usage 明细。替换本地请求 TTL 推断，并补齐上游尚未贯通的 WS/类型化兼容路径，详见第 5 节。 |
| WS 稳定性 | 采用后续轮次限流不再重放首帧、池化 OAuth 首次启用 hosted search 的连接能力检测，以及可配置 TTFT。历史重放方式按本地客户端主导规则适配，不恢复代理保存整段历史，详见第 4 节。 |
| OpenAI HTTP/SSE | 采用 codex 元数据不提前提交响应流、显式模型冷却规则覆盖 overloaded/slow_down/流内 503、只匹配实际 error 字段、非法 UTF-8 本地返回 400、原生图片请求不被空白心跳提前写成 200。 |
| 协议保真 | 采用响应重建保留 namespace / encrypted_function_args、只跳过明确带 CC Switch 前缀的外部 reasoning 封装、Desktop 特定页面上下文转换。不能扩大成任意孤立 tool output、密文或 compaction 清理。 |
| effort 与会话 | 采用合成 effort 经过策略映射、上限与最终校验的顺序修复，保留已配置的 GLM 降级含义；采用无显式会话标识时工具回合的稳定内容种子。保留 fork 的模型归一化、none/minimal → low、缺失值不额外注入规则。 |
| 资源与安全 | 采用请求体内存预算自动计算及 os.Root 限制 cgroup 读取范围；采用账号补全失败立即释放已占并发槽、登录 refresh token 原子消费、安全审计入队唤醒本机 worker。 |
| 定价修复 | 采用目录 hash 与正文不一致时继续重试同步、Sonnet 5.5 默认缓存读取价修正、普通输入免费时仍保留 Fast 倍率。合并价格 JSON 时逐模型处理，不覆盖本地 GPT / Terra 的计费策略。 |
| 其他供应商 | 采用 Anthropic 兼容入口的账号自定义错误策略、Gemini 非流式输出不将 thought 摘要混入正文、Antigravity 并行 functionResponse 先连续输出再附媒体、Pro Argon 独立冷却及手动越过缓存查询额度。 |
| 前端与支付 | 采用 Key 列表与 30 天用量统计解耦加载、账号刷新保留旧行/配额、设置页悬浮保存按钮、充值倍率最多 6 位小数、EasyPay 按实例配置退款成功码（默认 1、可选 0）。支付能力仅合入实现，不操作支付、充值、退款或生产配置。 |
| 版本与依赖 | 采用 Claude Code 基线 2.1.321。上游本轮未改 go.mod、前端 package.json/pnpm-lock 或 Dockerfile；go.sum 是补充校验项，不能称为一次依赖安全升级。保留本地现有安全修补、Node 24 构建和法务文档。 |

## 4. 客户端语义核对与 WS 合并决策

### 4.1 本次核对的客户端版本

- 客户端仓库：`/home/siyixuan/codes/codex`。首次全量 fetch 因 TLS 中断失败，随后限定 main + 稳定 tag 的 fetch 成功。
- 最新已抓取 `origin/main`：**`4f8205c8084168d264083b4113ac00490484555f`**，提交时间 2026-11-11 16:00:39（北京时间）；相比上轮检查的 `25f4940d4a` 新增 105 个提交。
- 最新稳定版：**`rust-v0.167.1` / `0a94398fa60c4fac0e7659e4213a0a36e613dd46`**，Cargo 版本 0.167.1。[官方 Codex changelog](https://learn.chatgpt.com/docs/changelog)列明该版本于 11 月 10 日发布。
- 本次只更新远端引用并用 `git show` / `git grep` 检查这些固定引用，**没有 pull/checkout 客户端工作树**，其本地 main 和文件保持原状。
- 当前已推送的网关默认 UA 仍是 0.167.0；建议下一轮合并时顺带同步到稳定版 0.167.1，继续保持单一版本来源、可观测 UI 和手动/同步覆盖优先级。本轮不再修改它。
- 客户端 0.167.1 的实际补丁包含增量工具声明及其 provider 范围修正：非 Lite 的 legacy 路径最终仅给 Bedrock，默认关闭；Lite 保持自身的 incremental_tools。不能只看第一个提交标题，就误判普通 OpenAI 路由默认全面改成增量工具。
- `spec_plan.rs::hosted_model_tool_specs` 在 Lite 下仍不声明 hosted tools；客户端执行的 `web.run` 不等于 hosted `web_search`。不能借客户端更新给全部 Lite 请求开启 hosted 搜索。

### 4.2 后续轮次 failover：采用上游修复，不重放首帧

上游 `0578fb160` 在原生 WS 第 2 轮及以后遇到需要换号的错误时，标记“当前轮不可安全重建”，使 handler 关闭连接，而不是把连接第一帧重新送出。本地已有 HTTP bridge 的逐轮重建保护，但原生 ctx_pool 仍需补这层兜底。

计划：

1. 原生 ctx_pool 后继轮次禁止回退到首轮 payload，无法安全恢复时返回可重试关闭（1013）。
2. 不删除客户端 previous_response_id，也不把增量 input 当完整历史。
3. 保留 HTTP bridge 已有、能证明重建完整当前轮的受控恢复；不要用全局关闭逻辑误伤该路径。
4. 确保分组/Key/PricingAt 仍取当前逻辑轮次，只释放一次并发与预占，已经完成的上一轮不再次计费。

客户端 `core/src/client.rs` 的 `WebsocketSession::reset`、`get_incremental_items`、建连逻辑说明：连接失效后续接状态被清空；只有请求属性和历史前缀相符时才发送增量，否则使用完整 request.input。`responses_retry.rs` 还负责退避及 HTTPS fallback。官方[ChatGPT 授权接入限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)同样区分 HTTP 完整历史与 WS 同认证连接续接；不能将公共 API 的持久存储能力直接套到 OAuth 后端。

### 4.3 池化 OAuth 启用搜索：采用能力检测，不照搬代理全历史重建

上游 `142d22cc4` 记录首次 response.create 是否声明 hosted search；当不兼容的旧连接收到搜索声明时，收集历史并换连接重建请求。它解决的方向合理，但和本地已经删除代理历史重建的设计有实质冲突。

计划采用以下边界：

- 复用上游连接能力状态与工具类型识别，识别顶层 tools、历史 additional_tools，不把名为 web_search 的普通 function 或 web.run 当 hosted search。
- 当本轮没有锚点且可独立执行时，换新上游连接并发送原始完整本轮内容；不截断输入，不额外保存/复制全会话。
- 当携带 previous_response_id、依赖旧连接状态时，结束不兼容连接并要求客户端完整重放，使用可重试关闭，不能删锚点再发。
- 无锚点但只有孤立 function_call_output，不因此视为完整历史。仍使用已有完整性检查。
- generate:false 预热不改成真实生成；不为了让连接“支持搜索”而主动注入可执行搜索工具。
- 新连接握手后的额度/credits 被动观测和 BeforeForward 检查必须保留，在真实发送前完成；没有授权就不能利用重连绕过准入。
- 原生 passthrough 不引入历史收集器；池化 HTTP→WS 如复用同类连接，也检查能力变化，但不扩大 HTTP 对 WS 锚点的支持。
- 新旧客户端的 compaction trigger、Lite 历史工具、显式 named/required/allowed_tools 和压缩摘要保护保持现有边界。正常轮次不额外增加搜索权限。

这一取舍会在需要新增 hosted search 且只有增量历史时产生一次可见重连，但避免静默缺历史和无界历史内存。上游“首次 create 固定搜索能力”属于其后端兼容判断，本次未调用真实 OAuth 模型验证该服务端内部行为。

### 4.4 元数据、TTFT、图像和密封工具调用

- `codex.rate_limits` / `codex.response.metadata` 是带外状态，不作为模型输出提交 SSE、解除首输出超时或启动 TTFT；仍执行被动 credits / 模型 ETag 等观测。客户端 `codex-api/src/endpoint/responses_websocket.rs` 对这两类事件有独立处理，支持上游修复方向。
- 上游 TTFT 补丁直接覆盖 passthrough relay；本地 ctx_pool、内部 HTTP→WS、WS→HTTP bridge 仍有独立判定。实施时共用 semantic/visible 模式判定并覆盖这些路径。**仅改变统计触发条件，不用新 TTFT 值代替“是否已对客户端输出”的重试安全判断**；终态/error、逐轮计时和裸 error 有限清理继续保留。
- 采用 namespace + encrypted_function_args 保真，包含 absent/null/[] 的区分。最新客户端 `core/src/tools/router.rs` 用 namespace/name 路由，保留密封字段；空数组还参与 collaboration 明文消息分类，绝不是可随手删除的冗余字段。
- 原生 Images 请求携带 `x-codex-image-turn-id`，客户端收到成功响应后会按 ImageResponse 解码。因此采用该路由禁用提前空白心跳的修复，保留真实失败状态；不借此关闭普通图像请求或 SSE 的全部保活。
- Desktop 页面上下文修复仅接受特定名称、agent namespace、无有效 call_id、合法 page_id/分片标记的形态，转换为不可信用户内容，不升格为 system/developer。本次开源客户端源码没有找到这些 Desktop 私有注入名称，不能声称已从 TUI 源码独立证明该私有行为；按上游窄规则及负例测试验收，不扩展到通用孤立工具结果。
- 最新 main 将大于 1 MiB 的 WS 文本消息拆成多个协议 frame，仍为一个 message。本地 coder/websocket 的 message 读取应负责重组；新增跨分片 UTF-8、单轮计数/审计/请求字节、限制与取消回归即可，不把 frame 改算成 request。
- 最新 main 的工具输出可保留 file-backed audio，并继续维护 opaque context-window 身份。原生转发不得把未知媒体字段改成文本或按 ID 前缀重写；本轮不额外建设音频跨协议转换，也不因为公共 API 能力推断所有 OAuth 路由支持音频。

## 5. 计费：本地功能收敛与上游覆盖缺口

### 5.1 从请求推断改为响应事实

本地现有 `openai_cache_write_ttl.go` / `OpenAICacheWrite12h` 只在特定公共 Terra 路径按最终请求的 `prompt_cache_options.ttl=12h` 选择倍率。上游的新实现按实际 usage 分桶，更能处理同一请求同时出现短、长 TTL 的写入。

官方 [Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching) 明确两个写入桶互斥且合计等于 cache_write_tokens；请求 TTL 不能证明本次每个 token 实际按哪一桶计费。因此计划：

1. 采用上游 30m/12h 分桶、独立 nullable 12h 单价，以及相应 DTO、数据库、价卡编辑/展示。
2. 有效分桶使用实际数量；缺失、负数、非整数、部分字段或总数不一致时，回到通用聚合写入价，不从请求 TTL 猜测。
3. 删除或解除本地请求 TTL 计费标记及重复倍率链，不允许在上游已按 12h 定价后再乘一次本地 2 倍。
4. 保留明确配置的旧通用写入价：未设置独立 12h 价时不擅自加价。独立 12h 价显式为 0 必须仍为免费；nil 和 0 不合并。
5. 普通输入按 input - cache reads - cache writes 计算，分桶计算后不重复累加写入总量，不复用 Anthropic 5m/1h 字段。
6. 保留标准/priority/flex/ultrafast、区间/时间价格、账号统计、组/账号长上下文 AND 开关和本地 Terra 附加费策略。按本轮 PricingAt 定价，不按异步记录时刻。
7. 历史账单不重算；只有未来请求按新规则处理。对“请求 12h、响应没有有效分桶”的 Terra 公共 API 请求，费用可能从原本推断的长 TTL 价回到聚合价，这是明确的逻辑变化，最终报告必须单列。
8. 不据此改变 OAuth 的请求参数过滤，不向未声明支持的路由强塞公共 API prompt_cache_options。

### 5.2 不能仅合入 HTTP parser 就宣称全部完成

静态检查 v0.2.21：

- 通用 `OpenAIUsage` / JSON、SSE parser 已添加两个 TTL 字段；ctx_pool 和内部 WS 的部分解析可以复用它。
- **`openai_ws_v2.Usage` 及 passthrough adapter 的逐轮/连接结果仍只有写入总量**，尚不能保留这两个分桶。
- `apicompat.ResponsesUsage` 等类型化转换及 `ForwardAsAnthropic` 的 usage 映射也需一并核对，不能让转换路径把字段丢掉后永久回退聚合价。

实施时将补齐这些传递和聚合点，尽量抽取共用解析规则，避免 relay 与 service 各维护一套不一致的 TTL 校验。验证矩阵覆盖 HTTP JSON/SSE、HTTP→WS、原生 ctx_pool、原生 passthrough、多轮、automatic successor、失败终态以及兼容转换到记账的完整链路。

## 6. 资源、数据库和前端边界

### 请求体预算

上游新默认仅在配置/环境变量未显式设置时生效：

`max(1 GiB, min(已探测的物理内存、cgroup 限制、GOMEMLIMIT) / 4)`

- 配置键仍是 `gateway.request_body_memory_budget_bytes`；显式固定值优先，显式 0 仍禁用。
- 例如有效上限 8 GiB 时预算 2 GiB；2 GiB 时仍为 1 GiB，并非 512 MiB。探测不到时也回退 1 GiB。
- 它限制纳入记账的正文缓冲，不是进程总 RSS 硬上限；1 GiB 下限在小实例上并不保守。合并不自动修改用户容器 limits、生产配置或 GOMEMLIMIT。
- 保留本地压缩/解压 body size 统计与预算读入口，不多读一次完整正文，不重复计费预算；继续验证失败/取消/failover 的归还。
- 本轮不重做既有 Key 并发聚合性能专项。只对新预算、索引/SQL、连接切换和释放做有针对性的验证。

### 数据库迁移

完整保留以下三个同号不同名的 249：

```
249_add_usage_logs_api_key_cost_index_notx.sql  # 新：CONCURRENTLY 覆盖索引
249_mcp_oauth.sql                             # 已有：上游 MCP
249_request_ingress_observation.sql           # 已有：本地入口观测
```

并加入 `250_openai_cache_write_ttl.sql`。

- runner 以完整 filename + checksum 跟踪，不能因序号相同重命名已执行迁移，也不能按 max(version) 跳过新文件。
- 249 新索引包含 `(api_key_id, created_at) INCLUDE(actual_cost)`。并发建索引减少写阻塞，但仍有扫描/I/O/磁盘成本，大表启动可能较慢；保留失败遗留 invalid index 的清理重试。
- 250 给 usage_logs 增加两个计数，给四张价卡/区间表增加 nullable 12h 价格；不会回填改写历史账单。ALTER TABLE 仍有短暂锁需求，不能将整个升级宣传为完全无锁。
- 手工合并 Ent schema，随后重新生成 8 个冲突的生成文件；不直接任选一侧生成结果。保留 ingress JSONB 与本地字段，不恢复已移除的 turn-state 长度 schema/写入。
- 覆盖单条、批量、best-effort INSERT 参数顺序、SELECT/Scan、SQL 类型及重试幂等，尤其是本地 ingress/body size 与两个新字段同时存在时的错位风险。
- 旧 `239_add_usage_log_codex_turn_state_lengths.sql` 与数据库历史列保留，不做破坏性删列。

### UI 与身份

- 新 TTL 明细叠加到现有 Usage UI，保留 ingress 的 Peer/对端、Body Size、Token/s 一位小数；不恢复 turn-state 长度列。
- 保留首页 $/min 与 TPM 同行、同刷新频率，不新增高频请求。
- Key 用量异步加载保留 AbortSignal / 旧请求不得覆盖新页；账号刷新保留旧值但明确 loading，不让旧行承接错误的编辑操作。
- 登录 refresh token 改为原子消费：并发只有一个请求成功，其他返回无效；Redis 消费失败不继续签发。验证现有前端刷新合并行为，区分本机登录令牌、MCP OAuth 令牌和上游账号 OAuth，不能把三个领域误当同一套轮换。

## 7. 必须保留的本地自定义功能

- system/developer/instructions 策略，不统一加 prompt；逐请求 instructions 回显、stream_options.reasoning_summary_delivery 保真。
- device 默认启用、session/full 默认关闭；client_metadata 内嵌 turn metadata 为规范身份；不折叠子代理 thread，不让握手首轮 compaction 标记污染后续轮次，不新增原生 WS 设备合并。
- 加密 compaction/summary 保护；HTTP 锚点需完整重放；原生 WS 裸 error 有限清理、accepted steer 全部审计、逐轮计费与异常 cyber 补记。
- credits 显式授权、历史最新正余额允许尝试调度、被动观察、耗尽锁定、必须手动重新授权、普通编辑/刷新/并发写保护、scheduler 摘要与握手后准入。不新增自动充值、自动重新授权或高频主动余额探测。
- Codex OAuth 模型目录分组 fresh/stale TTL 默认 60/300 秒且可配、单份正文缓存、Pro/Spark 汇总和能力隔离。
- GPT-6 / 6.1 精确模型与别名规则、none/minimal → low、ultra → xhigh、缺失 effort 不额外注入；保留 Terra 长上下文本地政策，不顺带改成上游订阅豁免。
- ingress JSONB 与异步值快照、默认不校验转发来源、可配置多个 IP/CIDR；旧客户端 IP 解析不变。入口字段仅用于观测，不能当 MCP issuer、权限或安全判断依据。
- pN 版本比较、插件运行时兼容门控、并发批量/稀疏接口与权限裁剪、Command Code 目录缓存凭据边界。
- MCP 默认未配置即关闭、管理员 Key 不自动升权、Docker 法务文档与自有 CI/release 流程。

## 8. 去重与实施顺序

可降低维护量的部分：用上游实际 TTL 分桶替代本地请求标记倍率；通用错误分类/模型冷却、密封工具调用、内存探测、Key UI/覆盖索引直接采用上游主体；统一 WS TTFT 与 usage 解析辅助逻辑。保留本地回归用例，并修改确实已改变的行为预期，而不是为了测试通过恢复旧错误逻辑。

实施顺序：

1. 确认 main 起点和工作区，创建 `merge/upstream-v0.2.21`，合并固定 tag，单独纳入纯 VERSION 同步。
2. 先解决 schema、价卡类型、Usage 持久化和 Ent 生成，再处理计费及 WS/兼容路径，最后处理前端冲突。25 个文本冲突之外，自动合并的 metadata、身份、队列与校验顺序也要复查。
3. 实施第 4、5 节的语义适配与缺口补齐；同步建议的 Codex UA 0.167.1，保留单一来源及覆盖优先级。
4. 定向回归先行：客户端真实请求结构、重连不重放首帧、搜索能力切换/预热/锚点、metadata→overload、TTFT 两种模式、加密 compaction、显式工具选择、密封字段和图片真实错误状态。
5. 计费测试贯穿解析→逐轮结果→RecordUsage→DB/扣费/账号统计，覆盖混合 TTL、缺失/非法桶、旧价卡、免费价、priority/flex/ultrafast、长上下文和异步 PricingAt；测试不做真实扣费或模型调用。
6. 隔离 PostgreSQL/Redis 验证：全新库、已有两个 249 的升级库、并发索引失败恢复、TTL 与 ingress 同存、credits 锁定与候选缓存、登录刷新并发消费。
7. 全量 Go unit、相关集成和定向 race；前端测试、类型检查、lint/build；Go lint/vuln、发布辅助脚本、embed 构建。没有依赖升级也要报告审计结果，不宣称既有依赖风险已经消失。
8. 资源控制延续上一轮：Go 编译 `-p 1` / GOMAXPROCS=2，前端单 worker，大编译/构建错峰；根据实际内存使用调整，不同时启动多个高内存任务。
9. 完成后给出采用/保留/未采用清单、明确的逻辑变化及测试结果，暂留合并分支。下一次合 main/push 或交付动作需届时用户指令。

## 9. 当前状态与证据范围

建议按以上边界合入 v0.2.21。尤其不能直接用上游 ingress 整文件覆盖本地，也不能把新的 HTTP TTL 测试通过视为 WS 全路径已完成。

当前只完成旧成果 commit/main/push、官方资料核对、上游/客户端 fetch 与静态预检；未对 v0.2.21 进行运行测试，未调用真实模型、修改生产配置或执行数据库迁移。

静态记录目录：`/tmp/sub2api-release-plan-20261111.vbni9A`，包含 release JSON、完整非 merge 提交清单、diff stat、merge-tree 结果。本计划是本地文档，未在上述 main 推送之后额外提交或推送。
