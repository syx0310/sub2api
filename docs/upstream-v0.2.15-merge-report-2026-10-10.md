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
