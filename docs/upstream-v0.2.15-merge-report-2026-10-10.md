# v0.2.15 合并及 Codex 客户端兼容报告

## 范围与基线

- 实施分支：`merge/upstream-v0.2.15`。main / origin/main 保持 `28ae263c300ea256beb59583029cd059b4255854`。
- 合并正式 [v0.2.15](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.15)，标签提交 `f2669c8cf62555cd92389b3f55920e9e6e7c6ff2`。相对 v0.2.14 包含 146 个提交、278 个文件变化。
- 合并提交 `70f4c85dd`。标签内 VERSION 仍为 0.2.14，单独 cherry-pick `3a6fd1c9d`，本地提交 `974dbc961` 将源码版本同步为 0.2.15；不引入其他 tag 后功能。
- Codex 客户端采用计划阶段已拉取的 `2351d9e1b608e6f9d9a3699b71d7eb39ee41cfa4`。稳定版 0.162.0 与其之后的 main 更新分别核对，没有修改客户端源码或替换已安装的客户端。
- 默认 TUI UA 继续为 0.160.0，保留设置覆盖、自动同步优先级和生效版本 UI。没有刷新整份模型目录内容。
- 不合回 main，不 push，不创建 PR，不构建/发布 Docker 镜像，不调用真实模型账号、生产数据库、支付或充值接口。

## 上游更新

### 平台和协议路由

- 新增 Cline（ClinePass 5h/7d/30d 窗口、积分钱包）和 Command Code（按模型选择 Anthropic / Responses / Chat 协议）。这些平台的余额与额度能力不等于 OpenAI OAuth credits 授权，也不增加 OpenAI 自动充值。
- 采用上游平台清单、provider profile 和统一协议分流，减少多处枚举、端点表及前端账号表单的重复维护。账号显式 base URL、协议和映射配置仍优先。
- 包含 Claude 计费、钱包冷却和账号级目录缓存隔离修复；保留原平台路由和本地账号、分组自定义字段。
- 迁移 `242_drop_platform_check_constraints.sql` 删除用户平台配额与组合路由目标的平台 CHECK 白名单，改为 handler / service / repository / Ent 校验。渠道监控 provider CHECK 仍保留。

### 转发、目录与统计

- 合入远程 Codex 目录账号映射、组合分组精确别名、目录变更失效及 ETag 优化。
- 合入 developer 角色、指定 tool_choice 格式、旧 function_call 配对、refusal、Anthropic thinking signature、负数 block index、SSE 心跳等兼容修复。
- 合入 WS 图片输入 token 与托管 image_gen 用量补全：有实际 usage 明细时优先使用，缺失才回填；逐轮及连接合计都传递图片输入用量，不重复相加。
- 合入长连接后续 turn 刷新分组价格，具体本地预占适配见下文。
- Usage 在延迟单元格默认展示请求输出 TPS；Ops 增加输出 TPS 的 P5/P10/P50、均值和样本数。修复长上下文标记、零倍率、科学计数法价格和小于 1 B 的速率显示。
- 其余包括 Grok 空响应切换和设备授权、OpenCode Zen 协议和退避、Anthropic cache TTL 与 beta 头、API 裸路径不被 SPA 截获、渠道健康样本判断，以及支付、备份、告警、资料编辑等异步页面状态修复。

### 构建

采用 Go 1.27.2、x/net 0.60.0 等安全依赖更新，以及 golangci-lint 2.14.0 和 schema 导入器兼容修复。保留 HTTP/2 兼容检查和本地 Docker 法务文档 COPY/构建路径，未用上游 Dockerfile 整体覆盖本地。

## 客户端语义和实际逻辑变化

OpenAI Docs 用于确认 append-only 工具历史及模型/地域/价格边界，具体 Codex wire 行为同时以本地客户端源码核对。[工具历史与调用限制](https://developers.openai.com/api/docs/guides/prompt-caching)、[GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol)。

### 1. 历史 web_search 声明：兼容历史，但不扩大本轮工具权限

本节为合并时的初始实现；其原生 v2 压缩覆盖缺口及后续修复见文末「2026-10-15 搜索历史压缩兼容补修」。

上游为包含 `web_search_call` 的 OAuth 历史补 cached-only 搜索声明，修复本地 compact 的历史校验错误。直接照搬会在仍有其他工具可用时额外开放搜索，因为 `external_web_access: false` 不代表禁止调用。

本地适配后的规则：

- 仅用于对应 OAuth 路径；不对普通 API Key 请求做此注入，旧 `/responses/compact` wire 不做此处理。
- `tool_choice: none` 时可以补历史声明，但不改变调用权限。
- 无工具、无 previous_response_id 且 choice 为缺失/auto/none 时，可补声明并固定 none。
- 客户端声明的本地 compact（内嵌元数据 request_kind=compaction、无 native compaction_trigger、无顶层新工具、无 previous_response_id）允许只追加历史声明并禁止工具调用，即使历史中还留有旧函数声明。元数据不据此授予任何工具权限、账号权限或免费计费。
- 普通请求仍有其他工具、指定/required 工具选择，或增量请求可能继承工具时，不自动添加一个可调用的搜索工具；不确定的情况保留请求和原有错误，而不放宽语义。
- Lite 使用独立追加的 developer-role additional_tools，不修改原有声明的 ID/内容；如有尾部 compaction_trigger，则插入其前。再次处理同一正文不会重复追加。

### 2. WS 分组价格、利润门、预占与结算同源

- 后续 turn 经 API Key 认证缓存读取最新分组；只有 Key ID、Group ID、平台、订阅类型一致时采用。换组或读取失败仍遵循上游的连接快照回退，不在旧连接中切换租户/路由身份。
- 每个 turn 保存独立的计费 Key 和 pricing context，最多保留当前及上一轮。利润门、BeforeForward 预占、异步最终结算共用它们，避免旧价预占/新价扣款。
- 已知客户端请求接受时刻的 passthrough 和自动后继，在准入前记录该时刻；ctx_pool 无更早时刻时使用准入时刻。高峰倍率估算读取同一个冻结时间，不再临时使用预占时的墙钟时间。
- 保留最终映射模型、实际 service_tier、有效 reasoning effort、长上下文开关、预热免计费、同轮重试不重复预占、异步计费引用交接和自动后继一次归因。
- 新增开销为后续 turn 一次已有认证缓存查询；未增加上游 OpenAI 请求或独立计费轮询。

### 3. 加密 reasoning 恢复与线程隔离

- 合入上游对特定 HTTP 400 thinking_signature_invalid 内容的识别，保持一次恢复上限；不泛化到任意签名错误或所有 WS 错误。
- HTTP 请求携带加密 compaction / compaction_summary 时，不再删除摘要后隐式重试。转发器不拥有摘要中的原始历史，失败交由客户端处理，避免生成时丢失唯一上下文。
- HTTP 失效密文记录的写入改用与读取相同的 tenant/thread/account 键。旧写入使用 sticky-session hash，既无法正确命中读取，也不符合子代理隔离边界。
- 回归覆盖共享 session/prompt_cache_key 的兄弟线程：受影响线程后续请求可清理已失效 reasoning，兄弟线程保留自己的密文。

### 4. 新客户端字段与错误提示

- 保留 history_initialization 的六种初始化类型、thread/session/parent 关系和未知内嵌 JSON 字段；它不参与身份合并或历史重建。
- 保留本地 stream_options、typed ID、previous_response_id、compact 和 ctx_pool/passthrough 语义；不代替客户端判断增量/完整历史续轮，不重新注入工具描述前缀或统一 prompt。
- WS/HTTP 桥接需要合成 response.failed 时，将来源错误中的 Retry-After 放入 response.error.headers，供 0.162 客户端读取。仅复制这一公开提示，拒绝非字符串/换行值，不复制 Authorization、Cookie 等任意头。

### 5. GPT-6.1 Sol Ultrafast 独立适配

- 独立提交 `a1813a508`，不是上游 v0.2.15 已完成的适配。
- 已知 GPT-6.1 Sol 及规范化模型名的 Ultrafast 使用 Standard 的 6 倍，涵盖输入、输出、缓存读写、长上下文与预占。保留明确的零价、渠道基础价格和独立 Fast 配置，不修改管理员数据库设置。[官方价格](https://developers.openai.com/api/docs/pricing?latest-pricing=ultrafast)
- 6.1 Sol 支持 EU Fast/Ultrafast；不再应用原来的 EU Fast 限制。已有显式 force/filter/block 规则仍有效，缺省不主动开启付费档位。[官方地域说明](https://developers.openai.com/api/docs/guides/ultrafast-mode)
- 官方 API 的 global/US/EU 端点可以补缺省 Ultrafast 能力；Astra 的 EU 限制不变。自定义代理不推断，OAuth 仍必须由账号目录声明，明确 null/空能力不被覆盖。
- 未刷新 catalog 快照内容，未根据 Pro 名称推断 entitlement，未启用 credits 或充值。

### 6. 统计口径与性能

- 新 TPS 为 output_tokens / 完整请求耗时，可能包含推理 token；不是纯解码速度，也不是首页聚合 TPM。预热、图片请求和无有效时长/输出的记录不显示该值，Ops 分位数也排除预热。
- Usage 行内计算不增加数据库查询。Ops 即使使用预聚合模式，也增加一次当前筛选窗口的原始 usage_logs 分位数查询；保留 2 秒查询超时和超时后该字段为空的降级，不假称零开销。
- 在临时 PostgreSQL 上对同类筛选/分位聚合做 EXPLAIN：使用 `idx_usage_logs_request_type_created_at`，索引条件包含 request_type 与时间上下界，其余条件作为过滤。这里只验证小规模合成数据的执行计划，不据此声称线上大窗口查询成本固定或已经完成生产压测。
- 首页 `$ /min` 完全保留：最近 5 分钟已入库 actual_cost / 5，与 RPM/TPM 同查询、同缓存、同刷新；不引入高频轮询。
- 未重跑已验收的 Key 并发性能专项。

## 本地保留与去重

- 通用角色转换复用上游主体，删除重复的 developer 转换函数，但保留 OAuth system→developer 与原有 instructions 策略。
- service_tiers 保留本地类型检查、按 tier ID 求交集和 legacy Fast 清理；上游 null→[] 修复已被更完整实现覆盖，不退化到字符串相等判断。
- 保留 Pro/Spark 目录合并、OAuth 60 秒 fresh / 300 秒 stale 可配置项、策略隔离、singleflight、ETag/304 与 600 KiB 目录单份正文缓存修复。
- credits 被动观测、缓存摘要、握手后检查、耗尽锁定/手动重新授权、普通账号编辑行锁保护均保留。
- 保留原生 WS 预热、裸 error 有界释放、多个 accepted steer 完整审计、线程隔离、逐轮并发与预占；不追加原生 WS 设备合并。
- 保留默认 device 指纹与默认关闭的 session/深层 ID 改写、UA 生效位置、Usage 四路长度观测默认隐藏列、插件权限与真实 Key 并发接口。

## 验证记录

日志目录：`/tmp/sub2api-v0215-merge.xgLZaF`。通过记录包括 Go 套件/子用例，不等同于独立顶层测试数量。

| 检查 | 结果 | 日志 |
| --- | --- | --- |
| 后端完整 unit | 58 个包、22,925 条通过，17 条既有跳过，零失败 | backend-unit-final.jsonl |
| PostgreSQL / Redis 仓储完整集成 | 1,547 条通过、1 条既有跳过，零失败 | repository-integration.jsonl |
| 重点 race | 5 个包、1,093 条通过，零失败、无数据竞争报告 | race.jsonl |
| TPS 分布、超时与执行计划复验 | 通过；包括预热排除和 raw/preagg 一致性 | ops-output-tps-integration.jsonl、ops-output-tps-explain.log |
| 前端完整 Vitest | 364 文件、2,836 用例通过 | frontend-full.log |
| 前端类型检查 | vue-tsc -b 通过 | frontend-typecheck-final.log |
| 前端 ESLint | 通过，无自动修复 | frontend-lint.log |
| 前端生产构建 | 通过，29.43 秒 | frontend-build.log |
| 后端生产构建 | Go 1.27.2、CGO_ENABLED=0、embed、trimpath；静态二进制构建及 -version 检查通过，版本 0.2.15 / 代码提交 df7dab42f | backend-build.log、backend-version.log |
| 内嵌前端资源测试 | 使用本次新构建的前端资源，123 条通过，零失败 | web-embed-tests.jsonl |
| 后端 lint | golangci-lint 2.14.0，0 issues | golangci-final.log |
| 后端安全 | govulncheck 1.8.0：0 可达漏洞、0 导入包漏洞；7 项依赖模块级不可达告警 | govulncheck.log |
| 前端安全门禁 | 原有例外校验通过，不等于零漏洞 | frontend-audit.json、frontend-audit-check.log |

本次计划内验证全部完成。代码及报告提交保留在合并分支；main / origin/main 未改变，没有推送远程。生产二进制只执行 -version，没有启动业务服务或执行线上数据库迁移。

补充与边界：

- 全量 unit 首轮的三项失败分别为 HTTP 失效记录键与本地 thread 隔离不一致、图片用量测试缺少真实轮次、路由 golden 的既有错误包装差异。修复/适配后完整复验通过，没有删除或跳过失败测试。
- 前端类型检查首轮触及人为设置的 1,400 MiB Node 堆限制而退出。提高单任务堆预算至 2,600 MiB 后通过，没有关闭类型检查。生产资源构建使用原 Vite 配置，仅在本次执行中去掉已经独立完成的重复 checker 插件，不修改仓库构建配置。
- 前端构建仍有旧 Browserslist 数据和大 chunk 提示，没有通过提高警告阈值来隐藏。
- xlsx 仍有 2 项 high，另有 13 项 moderate、4 项 low、0 项 critical；高危门禁依赖仓库已有的限期例外（至 2027-01-06），没有声称修复 xlsx。当前用途仍是管理员 Usage 写出导出，没有增加读取不可信 XLSX 的路径。
- 集成测试使用临时 PostgreSQL 18.1、Redis 8.4，测试容器由测试框架回收；不使用业务实例。最后添加执行计划观测只修改集成测试，随后再次运行 TPS 集成用例。
- 发布辅助 Python 10 项通过；Linux Compose 安全/环境/资源/Simple/Caddy 检查及 shell 语法通过。Apple Container 功能测试仅适用于 macOS，本机不声称通过该项。

测试适配不改变生产语义：上游 SSE 心跳用例补显式 include_usage 参数；图片用量用例改为四个真实 create/terminal 轮次，不放宽本地重复终端计费保护；上游路由 golden 保持原始基线，只严格识别本地对测试桩 stop-after-capture 错误的既有 502 包装差异，仍比较全部端点、认证、模型、正文协议和异常字段，并固定允许差异数量。

普通 Go 测试使用 GOMAXPROCS=2、-p=2/-parallel=4，GOMEMLIMIT 为 1200–1800MiB；race 使用 -p=1/-parallel=2，独立 cgroup 上限 3900MiB；前端两个 worker。重型编译、race、lint 和构建按内存余量错峰，测试均不使用真实 OAuth 推理或充值。

## 2026-10-10 审阅补修：摘要保护、协议目录身份与 cyber 兜底

本节记录在 `993c1f8c8` 之后的工作区修复，尚未 commit / push，不改变 main。

### 加密 compaction 保护补齐

- 公共清理函数不再删除 `compaction` / `compaction_summary`；旧版本留下的失效摘要记录也不能触发删除。raw 请求、对象形式 input 和 replay 历史都保持这一规则。
- 带加密 compaction 的 HTTP/SSE → 上游 WS 请求遇到 `invalid_encrypted_content` 时，不做剥离重试，也不因此删除 `previous_response_id`，错误交由客户端处理。
- `ctx_pool` / HTTP 桥接的公共失效记录不再登记 compaction；同包有加密 compaction 时，也不把无法确定来源的拒绝归因到同包 reasoning。仅 reasoning 的既有一次恢复、线程/账号隔离保持不变。
- 全量回归发现 Grok 的同类恢复也使用公共函数：除公共层保留摘要外，Grok 在兼容转换前的原始请求上检查 compaction，防止其被改写为 reasoning 后在错误重试中删除。Grok 两个恢复 helper 同样保留摘要及锚点，正常兼容转换流程不改。
- 语义依据：客户端 `compact_remote_v2.rs` 的历史替换测试，以及 [OpenAI compaction 文档](https://developers.openai.com/api/docs/guides/compaction) 中由摘要承接后续上下文的约束。这里保护的是 opaque 历史，不尝试在代理侧重建摘要内容。

### Command Code 协议目录缓存隔离

- 缓存键改为 URL + account ID + 实际目录请求头/代理配置的 SHA-256。后台刷新使用参与计算的同一份请求配置快照；换 Key、租户头、认证覆写或代理后，不会使用旧身份的目录或失败退避。
- 旧配置的晚到刷新只能写旧键，不能覆盖新身份结果。缓存键不包含明文 API Key；名称、余额等运行态更新不因 UpdatedAt 变化而额外拉目录。
- 原有 30 分钟 freshness、失败退避和同身份 stale-on-error 保留。因此同一配置下旧目录仍可能使用超过 30 分钟；本次解决的是跨配置误复用，不新增最大陈旧期限。Codex OAuth 的 60/300 秒目录缓存不变。
- 额外正常请求成本为小型请求头快照编码及摘要计算，不增加数据库查询或独立轮询；身份改变后的首次请求按已有冷启动等待/回退规则重新拉取。

### cyber 错误补记价格同源

- `AfterTurn` 的异常补记与正常结算共用本轮 API Key / 分组快照，不再传建连快照。
- 将冻结的 `PricingAt` 从本轮上下文捕获并显式传过异步 cyber 输入，再传给 `RecordUsage`，不因异步执行时刻变化而切换高峰倍率。
- 正常 `response.failed` 终态结算不改；原有防重复记录、真实 token 计费、预热免计费及预占引用交接保留。
- 动态回归使用本地模拟上游：两种原生 WS 模式下，第二轮裸 cyber error 携带非零用量，分组由 3 改为 0.3 后，预占和第二轮实际费用均按新倍率变化；另覆盖固定高峰/非高峰时刻的异步补记。

### 补修验证

日志：`/tmp/sub2api-forwarding-fixes.q0WYCb`。

- 完整后端用例分两部分执行：58 个包共 22,950 条通过、17 条既有跳过；进程级分配断言独立运行 5 次全部通过。无数据库 schema / 前端改动，未重跑部署、生产数据库集成或 Docker 构建。
- 首轮完整运行暴露 Grok 旧测试要求删除摘要，已按上述安全语义修正；另一个未改动的 `AllocsPerRun` 断言在整包运行时失败，疑似进程级后台分配噪声，同一代码隔离后通过 5 次，未提高阈值或删除测试。最终完整运行排除这一项，由独立运行补齐。
- 重点 race：service / handler 共 100 条通过，零失败、无数据竞争报告（`race.jsonl`）。
- lint 首次执行触发任务 OOM 保护；重跑保持 3500MiB 硬上限，改为单并发编译/分析及 GOMEMLIMIT=1000MiB，不提高内存上限。
- lint 重跑通过：golangci-lint 2.14.0，0 issues（`golangci-final.log`）。`git diff --check` 通过。

## 2026-10-15 搜索历史压缩兼容补修

本次仍保留在 `merge/upstream-v0.2.15` 工作区，不提交、不合 main、不 push；已有摘要保护、协议目录、cyber 和 Usage Token/s 改动均保留。

### 原因与对照依据

- 上游 [#7939](https://github.com/Wei-Shaw/sub2api/pull/7939) 对历史 `web_search_call` 缺少 hosted 搜索声明的兼容已合入，并非漏合并。fork 后续 `dfd26bd1a`（2026-10-10）收紧工具权限时排除了 native v2，导致「trigger＋历史函数/namespace＋auto」未获修复。
- 对照本地 Codex `2351d9e1b608e6f9d9a3699b71d7eb39ee41cfa4` 的 `compact_remote_v2_attempt.rs`：原生压缩保留历史并追加 trigger，工具可位于历史 `additional_tools`；本地摘要式压缩则使用 `Prompt` 的空工具默认值。客户端仓库本轮仅阅读，未修改或拉取。
- 使用 OpenAI Docs 核对工具权限约束：[官方说明](https://developers.openai.com/api/docs/guides/prompt-caching)将声明工具与允许调用区分开，`tool_choice=none` 用于保留定义但禁止调用。OAuth 的具体历史兼容仍结合客户端源码和上游 PR，不将公开 API 的全部行为直接等同于 OAuth。
- 二次审阅继续对照[官方工具选择定义](https://developers.openai.com/api/docs/guides/function-calling)及 [ChatGPT 授权接入限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)。后者明确限制的是 SIWC / api.openai.com 接入；本 fork 的 ChatGPT backend 路径还结合本地客户端 `codex-api/src/common.rs`（HTTP 结构无锚点、WS 结构有锚点）及现有 HTTP handler 的 OAuth 账号排除规则判断，不声称仅凭公共文档完成了 backend endpoint 实测。

### 行为改变

1. 明确的原生 v2 压缩，即使带其他工具或 `previous_response_id`，在默认、null、auto、none 选择下也补齐缺失的 hosted 搜索声明，并将本次工具选择限制为 none。标准 Responses 追加顶层工具；Lite 追加独立 developer-role `additional_tools`，置于尾部 trigger 前。已有有效搜索声明时不重复注入、不重写原选择。
2. 兼容注入不改原 `additional_tools` 的 ID/内容，不删除搜索历史、加密摘要或有效 WS 锚点，不把搜索结果转成文本。二次审阅修正了先前「HTTP 保留锚点交上游」的方案：OAuth HTTP 不能借用 WS 锚点，service 层在普通 `/responses` 发现此种请求时直接返回 400 / `openai_http_continuation_unsupported`，要求客户端携带完整上下文且不带锚点重发，或者在原认证 WS 连接续接；不调用上游、不静默删锚点。无法可靠判断当前 input 是否完整，因此带非空锚点即拒绝，而不猜测。API Key HTTP、有效原生 WS 续接和旧 compact wire 保持原规则。
3. map/raw 共用逐请求语义优先级：control > generate=false 的 prewarm > 原生 trigger > 内嵌 JSON 的 request_kind。control/prewarm 不被误识别为原生或本地压缩；仅仅存在历史 compaction 摘要也不视为一次新的压缩。原来安全的无工具／显式 none 历史兼容继续保留，包括预热，不因新增压缩分支再次缩小原有覆盖。
4. HTTP 的当前请求头先补入缺失的 body metadata，再进入 transform/passthrough；旧 compact 端点和 API Key 不采用这一新补缺。`ctx_pool` 将首帧握手补缺提前到兼容处理之前，与 WS passthrough 一致。body 显式值（含 null/空值）优先，后续 WS create 不继承握手的 compaction 标记。
5. 普通轮次含其他可调用工具／可能继承工具时不扩大权限。required、named、allowed_tools 等显式选择不强行改为 none；无法安全兼容时保留请求及上游错误。HTTP transform 的旧 `normalizeCodexToolChoice` 同时取消「在顶层找不到声明就降级 auto」的行为，不再误伤 namespace / additional_tools 或缺失声明；只保留 legacy function envelope → Responses name 的等价转换，未知或无效选择交上游校验。新增 debug 事件 `openai_web_search_history_compatibility_skipped`，仅包含固定原因、请求类型及工具/锚点存在标志，不记录正文、工具名、ID、密文或凭据。
6. 公共 HTTP handler 原本已排除不支持 HTTP 续接的 OAuth 候选、继续寻找 API Key 账号；这一调度规则不变。耗尽后的错误补齐与 service 一致的完整重放指引和可识别 code，不再只有「需要 API Key」一句提示。

没有新增目录拉取、数据库/Redis 查询、探测请求或自动重试。复用既有 gjson/sjson 路径及 map 变换；新增字段写入仍可能复制请求体，成本随正文长度增长。压缩修复不改配置、计费、模型目录或前端逻辑。

### 随附：默认 Codex 版本 0.162.1

- 按追加要求，将内置 `codexCLIVersion` 从 0.160.0 更新为 0.162.1。默认 UA 的首段/尾段与 `Version` 头继续来自同一常量，OS/架构/终端形态不变；目录请求未提供版本时，也复用该客户端版本兜底，目录缓存与过滤规则不改。
- 中英文设置页的 UA 占位文案同步；管理员「系统设置 → 网关转发」中原有的 Codex「当前生效版本」通过后端字段显示，无需新增组件。
- 保留管理员显式版本 → 自动同步稳定版本 → 内置默认值的优先级，不清空或覆盖已有数据库设置，不触发手动同步或更新客户端仓库。显式传入 0.160.0 的目录缓存测试仍作为旧客户端兼容输入保留，不全仓替换版本字面量。
- 同步默认 UA 一致性、设置接口和 API contract 的断言，并另行复验相关用例。

### 回归与边界

- helper 对照覆盖 standard/Lite、默认/null/auto/none、required/named/allowed_tools、已有声明、幂等性、typed ID、加密摘要、显式锚点、大整数、prewarm/control 和 body metadata 类型。
- 完整 Forward 回归覆盖 transform SSE、HTTP passthrough SSE、保留的旧 HTTP→WS 分支；生产 HTTP 入站仍遵循既有 SSE 路由规则，不为测试开启线上混用。
- 二次审阅新增穿过完整 Forward 的 required / named / namespaced / allowed_tools / custom 选择矩阵，覆盖历史函数、历史 namespace、顶层 namespace、缺失声明；模拟上游返回验证错误，断言原选择送达且仅请求一次。先前 HTTP 携带锚点的「模拟成功」预期已删除，改为上游零调用的明确拒绝及客户端完整重放；测试不作为 HTTP 真实续接支持的证据。
- 原生 WS 在 ctx_pool / passthrough 两种执行器中分别验证首轮压缩和后续普通轮次，覆盖 native、header-only local、body turn/null/empty 覆盖握手；下一轮不注入新搜索权限，不继承旧 compaction metadata。
- 测试使用本地 WS 客户端与模拟上游，不调用真实 OpenAI 推理或业务数据库。只能确认代码覆盖和出站结构，不能确认用户此前那次同名报错必然属于本缺口；上游已具备声明而仍失败的情况仍需另行诊断。
- 本轮验证日志目录：`/tmp/sub2api-web-search-compaction.E7aUuL`。

### 验证结果

最终代码包含原生压缩兼容、二次审阅补修及默认版本 0.162.1。unit 按「全量运行＋旧预期更新后的定向复验」完成，没有删除或跳过此次发现的不匹配用例；不能将下面记录表述为一次全量零失败。

- 最后一轮全量覆盖 58 个包，23,124 条测试/子用例通过、17 条既有跳过（`backend-unit-reviewed.jsonl`）。唯一不匹配是旧 `OAuthDropsOrphanAfterDroppingPreviousResponse` 用例仍要求删锚点、清空工具输出并继续请求；这是本次明确禁止的行为。将其改为更严格的「返回 400、正文不变、上游零调用」后，最终 service / handler 定向复验 292 条全部通过（`review-fixes-final.jsonl`），该用例另连续 5 次通过（`legacy-assertion-recheck.jsonl`）。这一步仅改测试预期，生产代码未再改变。
- 重点 race：service / handler 两个包共 343 条通过，零失败、无数据竞争报告（`race-reviewed.jsonl`）。覆盖本次新增完整 Forward 矩阵、两种原生 WS 连续轮次、显式锚点、HTTP 重放错误及 handler 的 JSON/SSE 错误返回。二次审阅到来时取消的旧 race 不计为通过。
- 后端静态检查：golangci-lint 2.14.0，0 issues（`golangci-reviewed.log`）；`git diff --check` 通过。
- 0.162.1 版本更新专项：service / admin handler / server 共 323 条通过，包含 UA/Version 同源、版本优先级、目录默认版本、设置接口与 API contract（`compaction-and-version-final.jsonl`）；版本更新也包含在上述最终全量代码中。
- 与此前相同，进程级分配断言 `TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits` 不与全量后台任务混跑，单独重复 5 次全部通过（`allocation-guard-reviewed.jsonl`）；未提高阈值或删除用例。
- 前端中英文键完整性 3 条通过，修改的两份设置文案 ESLint 通过，无自动修复（`frontend-i18n.log`、`frontend-eslint.log`）。本次未重跑前端生产构建、数据库集成、Docker 或发布流程。
- 普通 Go 使用 GOMAXPROCS=2、-p=1/-parallel=4；race 使用 -p=1/-parallel=2、3900MiB 上限；lint 使用单并发、GOMEMLIMIT=1000MiB、3500MiB 上限。重型编译与分析错峰，测试运行阶段可在内存余量内重叠。

截至 2026-10-15，以上代码和报告尚未 commit；当时 HEAD 为 `993c1f8c8`，main / origin/main 均保持 `28ae263c3`，未推送远程。

## 2026-10-19 main 交接复验

按本次授权，将 v0.2.15 合并分支及上述后续修复一并提交、快进合回 main，并仅向自有仓库 `syx0310/sub2api` 推送。前文的“不提交 / 不合 main / 不 push”均为对应实施阶段的历史状态，不再是本次交接限制。下一轮上游 release 仅检查和制定计划，不在本次交接中合入。

- 提交前确认 main 与 origin/main 没有分叉，main 是当前合并分支的祖先；没有修改客户端源码、生产数据库或运行发布部署。
- 重新执行 service / handler 的压缩兼容、工具选择、HTTP 续接拒绝、WS 语义和版本一致性定向测试：292 条通过，零失败（复用 Go 测试缓存）。
- 重新执行 UsageTable、latencyHealth 与中英文键完整性测试：3 个文件、63 条通过，零失败。Browserslist 数据过期提示仍保留，未为此更新依赖。
- 日志：`/tmp/sub2api-main-release-review.CsYQ3f/backend-smoke.jsonl`、`frontend-smoke.log`。本轮不是新的全量测试或构建；完整验证及其边界仍以上文记录为准。
