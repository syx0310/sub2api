# v0.2.21 合并与客户端语义适配报告

2026-11-12 补充：用户已批准升级 Go 1.27.4，变更和重新验证结果另见 [Go 安全升级报告](go1.27.4-security-upgrade-report-2026-11-12.md)。下文的 1.27.3 安全扫描结果保留为 11 月 11 日的历史记录，不代表升级后的扫描结果。

## 1. 范围与交付位置

- 工作分支：`merge/upstream-v0.2.21`，起点 `main@84593bd3471c93c23d5b7ea46ae78dca9ad4cf9e`。
- 合并目标：[上游 v0.2.21](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.21)，固定提交 `10ba8f9b4dc2e6023d4a9879ab77b21841babd10`，不追随移动的 upstream/main。
- tag 内 VERSION 仍为 0.2.20，另按批准计划纳入上游纯版本同步 `6dd5a045b62e27e5b5b139adef91a171097f639b`，最终应用版本为 **0.2.21**。
- 默认 Codex TUI 身份从 **0.167.0 → 0.167.1**；只更新基线，已有手动覆盖、同步覆盖优先级及有效版本 UI 保留。
- 本轮只在本地合并、验证并提交到上述分支；**不合 main、不 push、不创建 PR、不构建或推送 Docker 镜像**。
- 没有调用真实模型、修改生产配置或生产数据库，没有充值、自动重新授权 credits 或执行支付退款。
- 最终提交与验证汇总见第 7、8 节。

## 2. 采用的上游更新

相对 v0.2.20，上游包含 82 个提交、44 个非 merge 提交；其中旧版本同步已在本地等价存在，其余按批准计划合并。25 个文本冲突逐项处理，Ent 生成文件由合并后的 schema 重新生成。

| 更新 | 本次落地情况 |
|---|---|
| Claude Fable / Mythos 5.2 | 模型识别、Anthropic/Bedrock 映射、OpenCode 配置、Codex 兼容目录、thinking/采样/forced choice 校验及默认兜底价卡（每百万输入/输出 $8/$40，Fast 2 倍）。管理员自定义价格不被覆盖。 |
| OpenAI cache-write TTL | 新增实际 30m/12h token 桶、独立 nullable 12h 单价、区间/时间价格、账号统计、数据库字段与 Usage 展示；同时补齐 WS 与类型化兼容路径。 |
| WS 后续轮次 failover | 第 2 轮及以后无法安全重建时，不再让 handler 重放连接第一帧；交回客户端完整重放。 |
| 池化 OAuth 搜索能力 | 记录首次 create 的 hosted search 声明；需要改变能力时按完整历史边界换连接，不采用上游代理保存并重建整段会话的方式。 |
| TTFT 与元数据 | WS 统一使用 semantic/visible 判定；codex.* 元数据不当成模型输出，HTTP/SSE 不因此提前提交响应。 |
| OpenAI 错误处理 | 显式模型冷却规则覆盖 overloaded/slow_down/流内 503；只识别实际 error 字段；非法 UTF-8 本地 400；Codex 原生图片请求不被空白心跳提前写成 200。 |
| 协议保真 | 保留 namespace、encrypted_function_args 的 absent/null/[] 区别；仅跳过明确 CC Switch 前缀的外部 reasoning 包装；采用 Desktop 特定页面上下文的窄匹配转换。 |
| effort / 会话 | 合成 effort 先经过分组策略再做最终归一化；无显式会话标识时稳定工具回合的内容种子，保留本地身份边界。 |
| 资源与认证 | 请求体内存预算按物理内存/cgroup/GOMEMLIMIT 自动计算；os.Root 约束 cgroup 读取；账号补全失败释放并发槽；登录 refresh token 原子消费；安全审计入队唤醒本机 worker。 |
| 定价与目录 | 修复目录 hash/正文不一致后不再重试的问题、Sonnet 5.5 缓存读取默认价、普通输入免费时 Fast 倍率保留。GPT 本地价目条目保持原值。 |
| 其他供应商 | Anthropic 兼容入口采用账号错误策略；Gemini 非流式不混入 thought 摘要；Antigravity 并行 functionResponse 排序及媒体输出、Pro Argon 独立冷却、手动额度刷新。 |
| 前端 / 支付 | Key 列表与用量统计解耦；账号刷新保留旧行和旧配额；设置页悬浮保存；充值倍率 6 位小数；EasyPay 退款成功码可按实例设置。只合代码，不执行支付操作。 |
| 版本 | Claude Code 基线 2.1.321，Codex 默认基线 0.167.1；保留本地 Docker 法务文档、自有 CI、安全依赖补丁及 Node 24 构建。 |

## 3. 客户端依据与明确的逻辑变化

### 3.1 核对的源码版本

- 客户端目录：`/home/siyixuan/codes/codex`。
- 已抓取的 main：`4f8205c8084168d264083b4113ac00490484555f`。
- 稳定版本：`rust-v0.167.1`，提交 `0a94398fa60c4fac0e7659e4213a0a36e613dd46`。
- 使用固定引用阅读源码，客户端工作树没有被 checkout、覆盖或修改。此前完成的是远端引用更新，不宣称已经将客户端本地 main 快进。
- 本轮没有更新 GPT model catalog：所对照的稳定版本和 main 的相关目录没有需要本轮引入的模型条目差异。

关键依据：`core/src/client.rs` 的连接 reset / 增量历史判定；`codex-api/src/endpoint/responses_websocket.rs` 的带外事件与分帧发送；`core/src/tools/spec_plan.rs` 的 Lite hosted tools 排除；`core/src/session/step_context.rs` 的增量工具 provider 范围；`core/src/tools/router.rs` 的 namespace 和密封工具参数。

使用 OpenAI Docs 技能按官方资料核对两处协议边界：[授权接入限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)用于区分 HTTP 完整历史和 WS 同连接续接；[Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)用于确认写入桶口径。它们影响了下面的实现选择，不能据此声称真实 OAuth 后端已完成在线验收。

### 3.2 重连由客户端恢复历史，不由代理拼造

以前：原生 ctx_pool 后继轮次 failover 可能回到 handler 的连接首帧；上游新增搜索恢复还会收集历史并在换连接时重建。

现在：

1. 后继轮次无法安全换号时标记“没有可安全重建的当前轮”，关闭连接并要求客户端重试；不能把第一轮重复生成、重复计费。
2. 旧池化 OAuth 连接首次未启用 hosted search，本轮新增 hosted search 时：
   - 无锚点、输入可独立执行：废弃旧连接，新握手后原样发送当前请求。
   - 有 previous_response_id、item_reference 或缺上下文的孤立工具输出：不删字段、不重建历史；原生 WS 使用 1013，内部 HTTP→WS 返回明确的 `openai_ws_search_full_replay_required` 400。
3. 新连接仍先被动观测额度/credits，再执行发送前准入；重连不能绕过管理员授权。
4. `generate:false` 仍是预热；不会为了激活搜索而偷偷改成生成或给普通轮次增加搜索权限。
5. hosted `web_search` / `web_search_preview` 按协议工具类型识别；同名 function 和客户端 `web.run` 不算 hosted search。Lite 仍不自动全局启用 hosted 工具。
6. 能力检查针对最终选定的连接，包含空闲探活失败后重新借到另一条池化连接的情况，避免检查与实际发送使用了不同连接。

再次核对预热调用链：稳定客户端用传入的 Prompt 发 `generate:false` 并等终态，不应把所有预热都假定为空历史/空工具；正常 `stream()` 重试使用 `warmup=false`。因此本轮不注入预热工具、不将预热改为生成，也不在代理关闭后自行重造一轮预热。工具/请求属性不再符合增量条件时，客户端自身选择完整请求。

可见影响：有锚点的旧连接首次启用搜索时可能出现一次客户端可见重连。这是明确的历史安全边界，不是宣称所有断线都已消除。代理不会为此增加整段会话历史缓存。

HTTP 请求仍遵循已有完整重放保护；内部 HTTP→WS 的能力检测不代表公共 OAuth HTTP 支持 WS 锚点。已有 WS→HTTP bridge 的受控当前轮恢复保留。

### 3.3 TTFT 改统计口径，不改安全重试边界

以前：部分 WS 路径主要按 delta 判首 Token，与 HTTP/SSE 的可配置口径不一致。

现在：passthrough、ctx_pool、内部 HTTP→WS 和 WS→HTTP bridge 共用判定；semantic 可在推理/消息等语义输出事件开始计时，visible 等可见输出。`codex.rate_limits`、`codex.response.metadata`、裸 error 和终态不会单独启动 TTFT。

每轮独立计时；内部 HTTP→WS 另保留“是否已发生输出事件”的状态，不能因为 TTFT 更早而提前提交缓冲、削弱失败重试安全。原先多 JSON 文档一条 WS message 的修复继续有效，测试现在明确区分两种 TTFT 模式。

### 3.4 多帧仍是一条客户端请求

最新客户端把超过 1 MiB 的文本 message 分成多个 frame，分界可能位于 UTF-8 字符内部。继续依赖 message 级读取重组，不把 frame 数当请求数。

新增真实 WS 测试发送超过 1 MiB、跨 emoji 字节边界的内容，验证 ctx_pool / passthrough 的上游正文、请求字节统计和单次 turn 记账；既有读取限制、取消、裸 error 清理继续走原机制。

### 3.5 effort 策略顺序

Anthropic→OpenAI 转换先保留调用方/合成的 effort，执行分组策略一次，再进行 Codex / GPT 最终归一化。显式 deny 不会因提前把 none 改成 low 而被绕过，也不链式执行两次映射。

保留 fork 的 `none/minimal → low`、`ultra → xhigh`、缺失值不统一补 prompt/effort、GLM 已配置降级含义及原有转换 token floor。没有照搬上游将本 fork 允许值直接拒绝为 400 的处理。

### 3.6 密封字段及 Desktop 上下文

保留 namespace 路由和 encrypted_function_args，包括显式 null、空数组；不把未知密封内容转换为明文工具参数。只过滤带 `ccswitch-openai-reasoning-v1:` 前缀的特定包装，不泛化成任意 reasoning 或 compaction 删除。

Desktop 页面上下文转换受上游特定名称、namespace、call_id 和 page_id/分片规则限制，转换为不可信用户内容，不提升成 system/developer。开源 TUI 中未找到这些 Desktop 私有注入名称，本次只验证窄规则及负例，不声称获得了独立客户端实现证明。

### 3.7 登录刷新改为原子消费

以前：检查登录 refresh token 后单独删除，删除失败还可能继续签发；并发请求存在重复兑换窗口。

现在：Redis 原子读取并删除，只有一次消费成功；其他请求返回无效令牌，Redis 消费出错则返回服务不可用，不继续签发。前端已有刷新合并与跨标签页协调逻辑保留并通过其回归测试。它只涉及本机用户登录刷新，不等于改动 MCP OAuth 或上游账号 OAuth 的刷新机制。

## 4. 缓存写入计费：采用上游主体并补齐全路径

### 4.1 新口径

- 删除本地 `openai_cache_write_ttl.go`、`OpenAICacheWrite12h` 请求标记及旧倍率链。
- 以实际 usage 的 `cache_write_tokens_details.ttl_30m_tokens` / `ttl_12h_tokens` 为准，两桶非负整数、完整且合计等于写入总量才采用。
- 缺失、null、字符串、负数、小数、半份桶或总量不一致时，保留写入总量并按通用价计费；不会根据请求 TTL 推断。
- 普通输入只扣一次缓存读/写总量；不把两个桶再加到输入或写入总数，也不借用 Anthropic 5m/1h 字段。
- 12h 单价为 nullable：未设置的旧运营价卡沿用通用写入价；明确设置 0 表示免费。默认模型价卡按模型策略补长 TTL 价，不覆盖显式免费价卡。
- standard/priority/flex/ultrafast、长上下文、区间/时间定价、账号统计保留，使用本轮冻结的 PricingAt。

**重要费用变化：** 公共 Terra 请求即使写了 `ttl=12h`，若上游未给有效实际分桶，也不再套旧的推断长 TTL 倍率，而是回到聚合写入价。反之，实际报告的长 TTL 写入按新 12h 单价计费。历史账单不回算。

### 4.2 补齐的上游缺口

新增共享严格解析器 `internal/pkg/openai/cache_write_usage.go`，由服务层 JSON/SSE/WS parser 与 passthrough relay 共用。

- passthrough relay：逐轮解析、临时 usage 合并、终态、自动后继、连接累计均携带两个桶，下一轮清空，不继承上一轮 TTL。
- passthrough adapter：逐轮及连接结果都传给 OpenAIUsage。
- Responses / Chat 类型化 usage：保留原始 details，通过转换进入 ForwardAsAnthropic 等使用的记账结构；桶异常不使整条合法响应解码失败。
- ctx_pool、内部 HTTP→WS、HTTP JSON/SSE/透传：均进入相同计费及持久化字段。

## 5. 数据库、UI 与资源

- 三份 249 均保留原文件名：MCP、入口观测、usage API Key 成本覆盖索引。按 filename + checksum 跟踪，不按最大序号跳过。
- 新增 250：两个 usage 计数和四张价格表的 nullable 12h 单价，不重算历史账单。
- Ent 从合并 schema 生成；SQL INSERT 的 68 个参数、批量/单条/best-effort、SELECT/Scan 同时保留 ingress、prewarm、本地 body size 和新 TTL 字段。
- 新索引 `CREATE INDEX CONCURRENTLY` 仍需全表扫描/I/O/磁盘；失败遗留 invalid index 可清理重试。250 的 ALTER 仍有短暂锁需求，不能描述为完全无锁升级。
- Usage 的新 TTL 明细叠加于原 UI；保留 Peer/对端、Body Size、Token/s 一位小数；turn-state 长度列没有恢复。历史 239 迁移和数据库旧列未删除。
- 首页 $/min 仍与 TPM 同行、同刷新频率，没有增加高频请求。
- 自动请求体预算仍可被显式配置覆盖，显式 0 仍禁用。新默认是 `max(1 GiB, min(可探测内存限制)/4)`；它不是进程 RSS 上限，小实例也仍有 1 GiB 下限。本次不修改部署资源限制。
- 没有重做既有并发聚合性能专项，没有引入全量 Redis Key 扫描或额外余额轮询。

回退边界：本次数据库变更为追加字段/索引，不删除旧字段；回退应用二进制不会自动撤销新账单、价格配置或迁移记录，不应为回退而手工删除或重编号既有迁移。大表正式升级前仍需预留索引建设时间和磁盘空间。

## 6. 保留的本地功能与未采用部分

- system/developer/instructions、逐请求 instructions 回显、stream_options.reasoning_summary_delivery 不变，不统一加 prompt。
- device 默认启用，session/full 默认关闭；内嵌 turn metadata、子代理 thread 身份保留，不新增原生 WS 设备合并。
- 加密 compaction/summary 保护、显式 named/required/allowed_tools、HTTP 锚点完整重放、裸 error 有限清理、多 accepted steer 完整审计和当前轮异常 cyber 记账不变。
- credits 显式授权、历史正快照可尝试调度、被动观察、耗尽锁定/手动重新授权、scheduler 摘要、普通编辑和并发写保护不变。
- OAuth 模型目录 fresh/stale 默认 60/300 秒且分组可配、单份正文缓存、Pro/Spark 并集和能力隔离不变。
- GPT-6 / 6.1 精确模型与别名、本地 Terra 长上下文双开关政策不变，不采用上游 Terra 的订阅豁免。
- ingress JSONB、默认不校验转发来源、多 IP/CIDR、旧客户端 IP 解析不变；入口记录仅作观测，不参与 issuer 或授权判定。
- pN 更新比较、插件兼容门控、批量/稀疏并发查询、Command Code 目录凭据缓存边界保留。
- MCP 默认关闭、管理员 Key 不自动升权、Docker 法务文档、Node 24、自有 CI/release、安全依赖修补保留。
- 不合 tag 后的无关 sponsors 提交；不恢复代理全历史 replay collector；不引入自动充值或自动重新授权。

## 7. 验证与证据边界

日志目录：`/tmp/sub2api-v0221-merge.o0MEjb`。Go 编译使用 `GOMAXPROCS=2`、`-p 1`、`GOMEMLIMIT=1500MiB`，前端单 worker，大编译/构建错峰。Go 编译器实际 RSS 可高于软限制，因此仍检查进程内存，不能将 GOMEMLIMIT 宣称为硬限制。

已完成的阶段结果：前端 389 个测试文件、3,161 项全部通过，lint、`vue-tsc -b` 和 Vite 生产构建通过；repository PostgreSQL/Redis 集成测试通过 1,674 项（含子测试），另有原有 `TestGetAccountsLoadBatch` TODO 跳过 1 项；发布矩阵辅助测试 11 项通过；Docker 运行资源及 compose 网关配置脚本通过；隔离 PostgreSQL 的新建/已有两份 249 升级与重复运行通过；生产依赖审计符合已有例外策略。

前端类型检查及构建初次受设定的 1.5 GiB Node 堆上限影响退出，后以 2.5 GiB 上限重跑通过；未修改类型检查规则、关闭 checker 或变更依赖。Vite 仍有既有大 chunk / Browserslist 数据陈旧提示，本轮未扩大为分包或依赖升级任务。

`openai_ws_v2`、`apicompat`、`pkg/openai` 三个协议包 race 通过 960 项（含子测试）。其中补充了失败终态和自动后继的 TTL 逐轮清空、连接累计，避免新字段在这些路径丢失或继承上一轮。

生产依赖审计为 0 critical、2 high、12 moderate、2 low；两个 high 都是既有 xlsx 的 GHSA-4r6h-8v6p-xvw6 / GHSA-5pgg-2g8v-p4x9，现有例外校验通过，不宣称漏洞已经修复。没有修改前端 package/lock、go.mod 或 Dockerfile。

补充检查：Ent 再生成与已提交文件完全一致；compose 安全、simple mode 环境和 Caddy 缓存脚本通过。Apple container 与发布镜像脚本的 Bash 语法检查通过；macOS 专用生命周期用例在本机 Linux 上因 `stat -f '%Lp'` 不兼容退出，未宣称通过，也没有修改这个本轮未变动的脚本来适配测试环境。

测试创建的 PostgreSQL、Redis 临时容器及数据已清理，可由测试重新生成；保留本轮日志和本地构建产物，现有应用容器未改动。

首轮全量后端回归发现并处理了两类旧预期：未启用搜索的旧连接带锚点新增搜索不应被当作可续接；拼接 JSON 消息的 TTFT 应分别按 semantic/visible 断言。没有为了通过测试恢复删除锚点、猜测 TTL 或放宽密文保护。

最终全量 Go unit：63 个包通过、28,093 项测试通过（含子测试），17 项既有条件跳过，未调用需要真实模型/特定外部服务的跳过用例。全量验证之后仅清理静态检查指出的空分支和测试返回值顺序，修订版另做关键链路 race 与构建复验。

服务层 race 通过 326 项，覆盖实际 WS 搜索/探活换连接、TTL 记账、TTFT、分片、压缩、steer 后继、credits、登录刷新及 Chat 转换；调度摘要、refresh token 原子消费、审计入队/唤醒的定向 race 通过 20 项。加上三个协议包的 960 项，合计 1,306 项通过（含子测试），没有检测到数据竞争。

`golangci-lint v2.14.0` 全量复验通过，0 issues。最终源码的 embed 二进制构建、`internal/web` / `cmd/server` 两包 131 项测试通过；运行 `-version` 确认输出 `Sub2API 0.2.21 (commit: 0862b5b6d)`，没有启动服务或连接生产数据库。

### 11 月 11 日发现：Go 标准库安全扫描未通过

完整 `govulncheck ./...` **已完成**，退出码 3，报告一项可达标准库漏洞 **GO-2026-6859**；随后包级扫描及最终 embed 二进制扫描均检出同一项。不是扫描未完成，也不是新增 merge 冲突导致的编译失败。

- 当时的合并分支及合并前 main 均使用 Go **1.27.3**，Docker/CI 同样固定此版本，因此这是原有构建基线新被审计发现的问题，不是 v0.2.21 引入的新回归。
- [官方漏洞记录](https://vuln.go.dev/ID/GO-2026-6859.json)于 2026-11-11 发布：HTTP/1 服务使用 MaxBytesReader/MaxBytesHandler 时，超限处理与 handler 并发操作响应头可能造成 panic。扫描调用链不等于已动态复现利用；不能把每次 Header.Set 都描述为已触发漏洞。
- 修复版本为 **Go 1.27.4**；[官方发布记录](https://go.dev/doc/devel/release#go1.27.4)确认 11 月 10 日已发布，包含 net/http 安全修补。
- 另外 6 项仅位于依赖 module，完整符号级扫描未发现本项目调用其受影响路径；不将它们等同于上述可达标准库漏洞。
- 额外修复需要同步 go.mod、Docker 的 Go 镜像版本，以及 CI/release 中的版本校验并重跑验证。11 月 11 日未获确认时未扩大范围；11 月 12 日批准后的实施与验证见补充报告。

资源说明：符号级扫描一度达到约 4.3 GiB RSS，并占用 swap；准备因内存压力结束扫描时进程已自行完成，因此没有实际终止扫描。后续优先使用较轻的检查，避免同时运行大构建。

模拟服务、真实本地 WS、隔离数据库均不等于真实 OpenAI OAuth 服务端验收；客户端侧是固定源码引用的语义对照，未改客户端，也未宣称运行其完整 Rust 测试。

相对 main 的 whitespace 检查只保留 250 SQL 文件末尾的上游原有空行提示；该迁移正文与 tag 一致，没有为了格式提示改写已发布迁移。

## 8. 最终状态

- `ea99c8d180e9574c8d5449dcb332a9ac29f132ab`：v0.2.21 merge 和本地语义适配。
- `aaf4f42f6ff034acb24caf9d77ee069d505110e4`：上游纯 VERSION 同步。
- `0862b5b6d`：静态检查收尾，删除无行为的空分支、调整测试 helper 返回值顺序。
- 本计划和报告随单独文档提交保留；main 和 origin/main 均保持 `84593bd34`，未经新指令不会 push 或合 main。
- 批准的 v0.2.21 合并与客户端语义适配已完成；Go 1.27.4 额外安全升级于 11 月 12 日获批，结果见补充报告。
