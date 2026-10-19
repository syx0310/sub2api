# v0.2.17 合并与客户端语义报告

## 范围与基线

- 分支：`merge/upstream-v0.2.17`，从 `main` / `origin/main` 的 `2f7f86728bf7d9e5b8325e1cd49104055b0f6574` 建立。本轮不合回 main、不 push、不创建 PR、不部署。
- 上游目标：[v0.2.17](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.17)，tag 提交 `060d0160417782cd962a0c7f460f6cd4565d4916`。覆盖 v0.2.16 的 79 个提交和后续 48 个提交，合计 127 个提交（含 merge）、332 个文件的上游差异。
- tag 内 VERSION 仍为 0.2.16，按计划另行采用仅改 VERSION 的上游 `b0a02a330`，不夹带 tag 外功能。
- 合并提交 `7bf63aa2b`；版本同步提交 `af55bd424`，最终源码版本 0.2.17。
- 客户端对照已 fetch 的 `origin/main`：`89461ea6833832b0551ce24ee32205824d5c108d`；本地 checkout 保持 `e89b7c442c0433ad5167e72a5a25bb6d0712766a`，没有修改或安装客户端。稳定 release 为 0.163.0，但本 fork 内置 UA 继续为用户指定的 0.162.1。
- 所有测试均使用本地模拟上游或隔离测试资源，不调用真实模型、生产数据库、支付、充值或自动充值接口。

## 合入的上游功能

### v0.2.16

- Claude Haiku 5.5：模型识别、推理／采样边界、Responses 桥接和计费。
- Gemini 显式上下文缓存：创建、续期、删除、上游账号绑定、存储费与额度预留，修改租约及失败恢复。
- Vertex API Key 认证、服务账号模型同步和模型详情，AI Studio AQ. Key，以及第三方上游不套用 AI Studio 模拟额度。
- GPT-6.1 Sol 订阅长上下文模型附加费豁免及生成目录的 500k 默认窗口，具体本地适配见下文。
- 临时不可调度规则的账号／模型作用范围，包括明确配置的模型级 401；未配置的既有规则保持原默认。
- 分组多语言、Ops 请求详情账号名称，以及监控、表单、输入法、计时器和错误展示修复。
- 结构化输入拒绝时的受限 Chat 回退、非 OpenAI 平台不再误注入 Codex 图像生成桥接。

### v0.2.17

- Gemini 4 Argon、Antigravity 真实端点目录和能力同步，兼容入口思考档位及流式思考摘要。
- `gateway.min_output_tokens`：协议转换下限，默认 128、配置最低 16。
- OpenAI 上游 API Key 账号的 `/v1/messages` 搜索模拟；OAuth / SetupToken 明确排除。
- GPT-5.5 ChatGPT 订阅退役过滤、Codex 账号导入按 workspace/account 与 user 隔离。
- Responses 缺少 created_at 时的兼容、组合分组 OAuth 歧义路由拒绝、Anthropic 协议专用 base URL。
- 支付回调确认、待定退款扣减保留与原子回退、支付宝退款查询、Stripe client_secret 不经 URL 传递。
- Vertex 容量型 429 同号退避、失效服务账号停用、Gemini 单次 401 策略判定、工具图片与签名保留、GLM 用量窗口修复及账号筛选布局恢复。

更新依据：[v0.2.16 release](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.16)、[v0.2.17 release](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.17)，并核对最终树而不只检查提交标题。

## 本地适配及实际逻辑变化

### 1. 长上下文：结算豁免与已选账号预占同源

- 采用上游按实际凭据和最终发送模型判断的主体；OpenAI OAuth / SetupToken 的 GPT-6.1 Sol 不再应用模型价卡长上下文附加费。客户别名、渠道计费标签或响应模型重计价不能改变这一判断。
- 上游 API Key 仍使用 API 价卡；管理员显式配置的区间价格不被模型豁免取消。其余模型原有长上下文规则不变。
- 保留本地分组／账号开关的既有组合规则，不借此次合并改成上游原有的 OR 语义。
- 保留 GPT-6 规范模型名的严格识别，不凭未映射的努力后缀猜测实际模型；压缩别名有明确最终发送模型时按最终模型处理。
- 提取共同判断供预占和结算使用。WS 已知实际账号、最终映射模型时，估算也应用同一豁免；HTTP 在选号前没有实际账号时仍保守估算，不据本站订阅分组猜测上游凭据，不为估算增加数据库查询。
- 保留逐轮 PricingAt / 分组快照、cyber 异常补记、预热免请求费、真实 token、Fast / Ultrafast 和影子账号凭据归属。
- 新增预占／结算对照覆盖 272000、272001、500000 tokens，OAuth / SetupToken / API Key / 未知账号，普通 / Priority / Ultrafast，及计费模型与最终模型不同的情况。

### 2. 目录：只更新兜底窗口，不覆盖明确能力

- 500k 调整位于本地 GPT-6 系列真正使用的提前返回分支；未在不可达分支追加代码。最大窗口、工作流、工具描述及其他离线元数据保持原模板，未刷新整份 model catalog 快照。
- 原始账号目录中的明确 context_window 优先；本地已观测模型补全、Pro / Spark 目录合并、能力交集、管理员映射及白名单继续生效。
- 退役过滤在公开别名投影后执行：指向退役 GPT-5.5 的 OAuth 请求不调度；映射到可用替代模型的旧公开名称不按名称误删；上游 API Key 不受订阅退役过滤影响。没有自动替用户改模型。
- 采用上游账号集合加载与最终 ETag 计算顺序，移除已无调用者的重复加载 helper，同时保留本地 observed-model 补全。
- OAuth 目录 60 / 300 秒可配置缓存、600 KiB 单份正文缓存、singleflight、ETag / 304 和凭据隔离保留。退役过滤不主动拉取模型或 credits；合并目录时使用上游新增的可调度／可用账号集合读取，不宣称所有本地处理零开销。

官方退役范围与上游规则一致：[ChatGPT 登录的 GPT-5.5 退役说明](https://learn.chatgpt.com/docs/models#gpt-55-retirement)。500k 为本次采用的 sub2api 生成目录默认，不将它等同于公开 API 最大上下文窗口。

### 3. 转换配置与本地角色策略同时生效

- 新增输出 token floor 的转换入口与本地 preserve-system-role / provider-signature 入口组合；默认包装函数仍兼容旧调用方。
- 不恢复转换器里过早的 GPT-6.1 Sol none 拒绝；保留本地最终模型解析后执行的 none → low 规则。
- OAuth 的 system → instructions / developer 策略、Grok 的 system 保留和签名过滤不变；不注入统一 prompt。
- 纯原生 Responses 不因该配置新增 max_output_tokens。新增回归覆盖 16 / 128 / 256 的转换下限、角色保留和原生 HTTP 压缩不注入输出上限。

### 4. HTTP / WS 顺序及完整历史保护

- ctx_pool 接入上游带 Config 的规范化入口，但在本地原位置执行：先取得本轮模型／metadata、完成映射和推理策略，再进行协议兼容。没有把原生压缩识别重新移到 metadata 补齐之前。
- passthrough 后续 create 保留原始请求审计、模型继承与逐轮映射，之后调用新的规范化入口；不会因冲突解决跳过本地 BeforeRequest。
- 保留原生 v2 / Lite / 本地摘要 / 旧 compact wire 区分、显式工具选择、不扩大普通轮次搜索权限、加密 compaction 完整性及 HTTP 锚点明确拒绝／完整重放提示。
- 采用受限 Chat 回退的上游实现，保留请求起始时间；不把真实 Responses 请求当作 Chat 请求重发，也不在已经输出／已有用量或错误证据不完整时额外重试。
- Responses 缺失时间戳补齐与合成失败事件采用上游主体；补齐本地额外的读失败、预热拒绝调用点，并保留 WS 合成失败中的安全 Retry-After。透传路径只观察已有时间戳供合成失败使用，不强行重写上游所有事件。

OpenAI Docs 用于核对 opaque compaction 应完整保留的约束；OAuth 的实际 wire 另对照客户端，而不把公开 API 能力直接套到 ChatGPT backend。[Compaction](https://developers.openai.com/api/docs/guides/compaction)、[GPT-6.1 Sol API 规格](https://developers.openai.com/api/docs/models/gpt-6.1-sol)。

### 5. 最新客户端回归与未扩展的边界

- 对照客户端 `compact_remote_v2.rs` / `compact_remote_v2_attempt.rs`：后台原生压缩使用 HTTP 和完整 input + trigger。新增完整 Forward 回归，覆盖 transform / passthrough、standard / Lite，保留内嵌 metadata、output.encrypted_content include 和 reasoning_summary_delivery。
- 不由代理主动启用客户端 output token replay，不删除其显式 include；不改变前台有效 WS 续接。
- 元数据的 request_kind、后台阶段及未知字段保留；身份字段仍按本地既有凭据／API Key 命名空间做确定性隔离，保留不同 thread 的区别。这不是默认关闭的 session 指纹合并，也不承诺身份值逐字节不变。
- 客户端现在会在特定取消路径等待 response.interrupt.accepted / failed。ctx_pool 仍只接受 create，本次上游也未扩展该能力；按批准计划留作独立后续适配，不声称完整支持最新客户端全部控制帧，也不在此轮重构连接池。

## 本地功能保留与去重

- Docker 法务文档构建路径保持不变。
- 默认 TUI UA 0.162.1、管理员覆盖 / 自动同步 / 内置兜底优先级和生效版本 UI 不变。
- 默认 device、默认关闭 session / 深层 ID 改写，thread 隔离和子代理回归保留。
- credits 被动观测、缓存候选摘要、握手准入、耗尽锁定、手动重新授权及普通编辑行锁保护保留；不新增自动充值。
- Command Code 协议目录按实际请求配置摘要隔离，与 OAuth 目录缓存分别维护。
- ctx_pool 裸 error 有界释放、accepted steer 多输入审计、逐轮并发、预占引用交接和异常结算保留。
- 首页 $/min 与 TPM 同行及同刷新、Usage Token/s 一位小数、四路长度默认隐藏列和真实 Key 并发接口保留。不重复已验收的 Key 并发性能专项。
- 通用新增平台、Gemini 缓存、支付、模型退役和时间戳功能采用上游主体；仅在本地语义交界处适配，未建立第二套平行实现。

## 数据库与资源

- 新增 `242_group_i18n.sql`、`243_gemini_cached_contents.sql`、`244_gemini_cached_contents_channel_usage.sql`、`245_gemini_cached_contents_mutation_lease.sql`。
- 迁移器以完整文件名为主键；已有 `242_drop_platform_check_constraints.sql` 与新增 242 可以共存。新增集成断言验证两者都已记录，以及 i18n、渠道快照、修改租约列和索引。没有改名或覆盖已应用迁移。
- 新增 Gemini 缓存恢复任务沿用上游的每分钟、每批最多 50 条修改意图恢复，区别于本地 OAuth 目录缓存和被动 credits；不增加 OpenAI 额度轮询。
- 历史虽含 SheetJS / Vue 安全升级提交，最终 tag 的 package.json / pnpm-lock.yaml 与 v0.2.15 相同，不能将那些提交列作本次已落地修复；不额外扩大依赖升级范围。
- 随上游移除两张旧赞助商 logo 和被上游回退的筛选布局测试 `AccountTableFilters.spec.ts`；原文件均可从 main 的 `2f7f86728` 或 Git 历史恢复。没有删除用户数据，也未为消除本轮失败而删除测试。

## 验证记录

日志目录：`/tmp/sub2api-v0217-merge.mDnqba`。测试数包括套件与子用例；模拟上游通过不等于真实账号线上验证。

已完成：

- 后端完整 unit：58 个包、24,200 条通过、17 条既有跳过、零失败（`backend-unit-final.jsonl`）；未变化的包复用 Go 测试缓存。
- 进程级分配断言 `TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits` 与全量后台任务分开执行，连续 5 次通过（`allocation-guard.jsonl`），不提高阈值、不删除用例。
- 合并语义定向复验：194 条通过（`fork-semantics-recheck.jsonl`）。
- 重点 race：service / handler / repository / openai_ws_v2 共 605 条通过、零失败，无数据竞争报告（`race.jsonl`）。覆盖后台压缩、两种 WS 模式的搜索历史兼容、显式工具选择、逐轮计费、credits 候选准入、协议目录换凭据、steer 审计、Gemini 缓存和待定退款等路径。
- 前端完整 Vitest：374 个文件、2,914 条通过，零失败。
- 前端 ESLint：通过，无自动修复。
- 前端类型检查：`vue-tsc -b` 通过。
- 前端生产构建：通过，30.91 秒。采用原 Vite 配置，单次调用移除已独立完成的重复 checker 插件以控制内存，没有修改仓库构建配置。Browserslist 过期和大 chunk 告警保留。
- 新构建资源的 embed 回归：123 条通过、零失败（`web-embed-tests.jsonl`）。
- 后端生产构建：Go 1.27.2、CGO_ENABLED=0、embed、trimpath；静态二进制构建通过，`-version` 显示 0.2.17 / af55bd424（`backend-build.log`、`backend-version.log`）。没有启动业务服务或执行线上迁移。
- PostgreSQL / Redis 仓储集成：1,560 条通过、1 条既有跳过、零失败。使用临时测试容器，不连接业务数据库。
- 后端 govulncheck：0 可达漏洞、0 导入包漏洞，7 项依赖模块级不可达告警（`govulncheck.log`）。
- 后端静态检查：golangci-lint 2.14.0，0 issues（`golangci.log`）。
- 前端 audit：既有例外校验通过；2 high（xlsx）、13 moderate、4 low、0 critical。不延长或扩大例外，不声称零漏洞。

计划内验证全部完成。上述结果不包括真实模型推理、生产账号端到端验证、生产数据库迁移、Docker 镜像构建／发布或部署。本轮未执行这些操作。

资源约束：普通 Go 使用 GOMAXPROCS=2、-p=1 / -parallel=4，完整 unit 内存硬上限 3500MiB；race 使用 -p=1 / -parallel=2、3900MiB 上限；lint 为单并发、GOMEMLIMIT=1000MiB、3500MiB 上限。前端测试为 1 worker，类型检查／资源构建与大型 service 测试编译错峰；race 在 lint 结束后启动。未通过取消门禁或降低断言来减少资源占用。

首轮发现与处理：

- 编译验证发现自动合并不能识别的函数签名变化，补齐本地读失败／预热拒绝的 created_at、WS 桥接请求字节数和 Chat 回退起始时间；保留对应回归。
- 首轮全量 unit 中，上游测试的未知模型后缀推断、API Key 长上下文 OR 开关预期与本地规则不同；按既有严格模型识别与 AND 规则修订，并增加未映射后缀的否定断言。
- 新增后台压缩用例最初错误地期待身份字面值不变；改为验证既有凭据隔离恰好执行一次，同时完整保留非身份元数据。
- Spark 合并测试原先仍期待退役的 GPT-5.5。现在严格比较其他全量模型，并明确断言退役模型消失、Spark 补充仍存在。
- 上述测试适配后重新执行完整 unit，结果以上述最终记录为准；未删除或跳过这些失败用例，未用宽松断言掩盖差异。

## 交接状态

- 代码与本报告提交在 `merge/upstream-v0.2.17`，没有推送。
- main / origin/main 仍为 `2f7f86728`；结束前只读核对 GitHub 远端 main 也是同一提交。
- 需要后续单独决定是否合回 main；本轮不创建 PR、不操作上游仓库、不触发发布。
