# v0.2.22 合并与 GPT-6.2 适配报告

## 范围与状态

- 起点：`main@482c38f8af45710f9f7ebbdf4c6a2bf1873dd746`。
- 工作分支：`merge/upstream-v0.2.22-gpt62`。
- 合并目标：[上游正式 v0.2.22](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.22)，commit `b0fa9f3e6eeab1968a53e1289b69daa4bb40ca2b`。相对 v0.2.21，187 个提交、108 个非 merge 提交、340 个文件变化。
- tag 内 VERSION 仍是 0.2.21；采用发布后 `15871f904` 的等价单文件版本修正，应用版本为 **0.2.22**，未追随其它 upstream/main 变化。
- 客户端依据：已拉取的 `/home/siyixuan/codes/codex@b8f6787e95a5006d91bc712f59d72af32ef9b205`；模型目录固定为[正式版 0.169.1](https://github.com/openai/codex/releases/tag/rust-v0.169.1)，commit `c376cf7396eff83ce4c6ac61b9553df142e7a0be`。两份源码中的 6.2 descriptor 已逐字段比对一致。
- 本轮只操作本地合并分支，没有合回 main、push、创建 PR、发布镜像或操作生产实例。
- 代码合并提交：`14c474a8009a5c3ec319f839954f5da18e51a0e5`，两个父提交分别为上述 main 和 v0.2.22 commit。

## 上游更新的采用情况

| 领域 | 合入内容及本地处理 |
|---|---|
| 分组服务档 | 新增强制 Ultrafast，schema、Ent、DTO、认证缓存、复制分组和前端传播完整，默认关闭。优先级 Ultrafast > Fast > 客户端，之后仍执行全局 pass/filter/block 等策略。增加模型及账号能力判断，见下文。 |
| 扣费 | 使用上游按用户合并事务主体，每批至多 128 条命令。每条命令独立保留请求幂等、Key、额度、订阅和结果；失败整批回退逐条处理。沿用本地当前轮冻结 PricingAt，不按异步写库时刻重新选价。 |
| 调度 | 合入 bucket singleflight、合并回源、只读母本复用、last_used 短缓存、冷却成员保留和能力/模型过滤。保留本地 128 条目、20,000 账号、16 MiB 编码源数据预算，以及 credits 摘要字段。 |
| 内存 | 采用请求解析零拷贝和减少 JSON 重建。补充小字段所有权：request view 的 model、cache key、response anchor、tier、effort 单独复制，防止异步记账中的短字符串持有整份大 body。 |
| 连接与流 | 合入 TCP keepalive/TCP_USER_TIMEOUT、不支持设置时的软失败、首输出前 keepalive、keepalive 后 SSE 错误、Messages 首内容前 failover、输出后的账号副作用和断连后的粘性回写。约 30 秒是失联 TCP 检测目标，不是正常静默推理的读超时。 |
| 用量汇总 | 合入非阻塞重建登记和脏区间机制。账号过滤不再把 `actual_cost` 偷换成账号成本；单独返回 `account_cost`，同时保留本地 input/output/cache/other 实际费用分解。 |
| 协议转换 | 采用加密函数内容保留、Composite 公共模型回显、count_tokens 请求头去重、response_format strict 默认修正、Anthropic effort 和 per-turn beta 修复。角色和 instructions 继续遵守本地策略。 |
| 其它渠道 | Gemini 4 Carbon/Hydrogen、Vertex us/eu 和默认价卡区域倍率、工具参数分片与同名并行调用、Antigravity 空流/思考输出、Bedrock 错误分类、Kimi 套餐、Fable 周额度恢复、Grok 搜索/错误分离等更新随上游合入。Vertex 区域倍率不覆盖运营者显式账号成本配置。 |
| 管理与运维 | 等待 ping 可关闭、可选透传模型白名单及验证、刷新保留旧页面数据、品牌 HTML/CSP 缓存、并发 OAuth 轮换、RPM/组倍率事务、退款幂等等更新。仅合代码，没有调用支付或生产管理接口。 |
| 构建 | 保留主 Dockerfile 法务资源和自有 CI；补齐 backend/deploy Dockerfile 的 Go 1.27.4。jsonschema-go 仅从间接依赖改为直接依赖，版本不变；前端依赖和锁文件未升级。 |

8 个文本冲突均逐项处理，涉及 Anthropic 转换、scheduler cache、group/model 用量统计、billing、Codex transform、Forward 和请求体处理；没有以整文件 ours/theirs 覆盖本地功能。Ent/Wire 已重新生成核对。

## GPT-6.2 模型、目录与请求行为

只增加已发布的 `gpt-6.2-sol`、`gpt-6.2-astra`；不创建 6.2 Terra/Luna、裸 6.2 或日期快照等推测型号。

覆盖后端模型识别、默认列表、精确别名、OAuth 观察目录、API Key 目录、分组白名单、映射预设、客户端配置展示、默认定价和回退定价。保留原账号测试模型及列表首项，不顺便更换使用示例的默认模型。

从固定客户端源码导入完整 descriptor，文件位于 `backend/internal/pkg/openai/codex_gpt62_{sol,astra}.json`，并与固定 tag 逐字段校验。目录包含的 prompt 仅作为客户端目录数据，**不会统一注入用户请求**。

| 客户端目录属性 | Sol | Astra |
|---|---:|---:|
| 默认 context_window | 500,000 | 272,000 |
| max_context_window | 872,000 | 872,000 |
| 默认 effort | low | low |
| effort 选项 | low / medium / high / xhigh / max / ultra | 同左 |
| Ultra 实际 effort | xhigh | xhigh |
| 最低客户端版本 | 0.166.0 | 0.166.0 |
| 协议/能力 | 优先 WS、Lite、code_mode_only、multi_agent v2、动态 effort | 同左 |

公共 API 的 1,050,000 总上下文、922,000 最大输入和 128,000 最大输出用于公共价卡/配置说明，不替换 Codex 默认工作窗口。API Key 路径沿用本地已知 GPT 模型的 Lite 关停规则，保持标准 Responses，不因采用 Codex 模板而开启 ChatGPT-only Lite；这包括兼容提供方的 API Key 账号，是目录覆盖规则的既有例外。其它实际目录里的 false/null、空 tier 列表和第三方能力优先。

请求规则：

- 保留 `none/minimal → low`；`ultra → xhigh`，缺失 effort 不自动补 low，不触发代理侧自动创建子代理。
- 先选最终映射模型，再执行策略和模型参数归一化；effort 与 `service_tier=ultrafast` 是独立维度。
- 6.2 effort/compact 别名仅识别有限、明确的后缀；同步上游 6.1 Sol effort 别名的计费识别。旧家族的未知型号边界不扩大，例如 `gpt-6-sol-max` 继续不推测价格。
- 按官方端点规则处理不支持的 temperature/top_p/logprobs 字段；GPT-6 官方 Chat 工具请求要求走 Responses，不静默删除 tools。
- 公共 API 的 prompt_cache_options 与真实响应 30m/12h 用量分桶继续保留；不根据请求 TTL 猜测使用量，不向 OAuth 强塞公共 API 参数。
- 默认 Codex UA 为 `codex-tui/0.169.1 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.169.1)`，保留已有显式覆盖优先级与 UI 有效版本展示。

## 明确改变的转发逻辑

### 1. 配置更新和压缩

原先看到 configuration_update 就删除 context_management；现在保留其原始意图。标准 Responses 已允许配置更新与自动压缩共存。自动 truncation、旧 `/responses/compact` 等不支持组合由相应端点验证，不能通过悄悄删历史/删压缩策略把失败请求变成另一个任务。OAuth 原生 compaction_trigger 仍走原有独立识别。

原先仅修改 text.format 的更新也会清空解析出的 effort；现在字段级继承，未提供 reasoning.effort 不覆盖先前值。显式 null/非法值与缺失区别处理；compaction 建立新基线，后继更新仍须按客户端语义重述。响应里的 request-level effort 不能覆盖本地已经确定的本轮有效 effort。

协议依据：[官方配置更新说明](https://developers.openai.com/api/docs/guides/reasoning#change-reasoning-mid-conversation)。

### 2. 历史 ID、上下文和 schema

- 不采用上游 `at_<UUID>` 去连字符改写。additional_tools 声明及 item_reference 的 ID 是 opaque 值，保留短 ID、原 UUID 和跨轮引用；不建立仅本轮有效的临时 ID 映射。
- 不采用将 browser_info/goal_objective/untrusted_context 仅在 HTTP 改成普通用户消息的行为。对客户端已确认的 `agent` 命名空间、非空字符串输出、没有有效 call_id 的四种外部上下文（含默认 untrusted_content）做窄范围识别，保留原类型、namespace 和正文。普通工具结果和其它孤立输出仍校验；不是给任意缺 call_id 的调用结果放行。
- 同时补齐旧 namespace 清理层，否则预检放行后正文仍会被改变。既有经过结构验证的 legacy 页面快照适配保留。
- 工具 schema 的 Python regex 兼容清理限定到 OAuth，HTTP/WS 使用同一判断。标准 API Key/第三方的有效 pattern 不被统一删除或放宽；通用 null 类型修复继续保留。
- 保留 system/developer/instructions 本地规则，不采用上游对 Anthropic 会话中途 system 消息的新权限提升。已有初始 system 转换不变。
- `input_transformations` 诊断透传，不把 reasoning 移除诊断误作自动换号错误；不新增对话正文或密文日志。

以上历史保真处理与[官方前缀规则](https://developers.openai.com/api/docs/guides/keep-prefix-unchanged)对齐，但不承诺能绕过模型/组织/终端用户变化导致的服务端 reasoning 移除。

### 3. WS 与身份

- 在原生 WS 握手响应公布网关实际入口消息限额 `x-codex-websocket-max-message-bytes`，值取现有配置及默认值；没有扩大消息上限，也不借用某个池化上游连接的限制。该头只说明网关入口上限，不保证所有账号的上游都接受同样大小的消息。
- 客户端大请求回退到 HTTP 仍须完整重放；不会删除续接锚点后带着残缺增量继续请求。1 MiB frame 分片仍按一条 message 处理。
- 保留 generate:false 预热、压缩、裸 error 有限清理、steer 完整审计及本轮一次归因。
- 合入父 prompt_cache_key 身份域修复，仍区分父缓存亲和和子 thread 执行身份；device 默认启用，session/full 默认不启用，账号命名空间一一隔离和嵌套 turn metadata 保留。

## 定价与付费档位的变化

单位：美元/百万 token，Standard 短上下文。

| 模型 | 输入 | 输出 | 缓存读 | 30m 写 | 12h 写 |
|---|---:|---:|---:|---:|---:|
| GPT-6.2 Sol | 2 | 10 | 0.10 | 2.50 | 5 |
| GPT-6.2 Astra | 8 | 40 | 0.40 | 10 | 20 |

这是 global 基础价卡；其它既有运营者倍率和收费规则不因本次模型目录更新自动更改。依据：[官方定价](https://developers.openai.com/api/docs/pricing)、[6.2 迁移指南](https://developers.openai.com/api/docs/guides/latest-model)。

- 输入超过 272K 时整次输入/缓存价格 2 倍、输出 1.5 倍，继续受本地分组与账号双开关控制。6.2 不自动继承 6.1 Sol 的订阅长上下文豁免；Sol 的 500K 默认窗口不是 500K 短价阈值。
- Fast 为 2 倍。6.2 Ultrafast 在美西时间 2027-03-01 00:00（UTC 08:00）前为 3 倍，之后为 6 倍。旧 GPT-6 Astra/6.1 Sol 仍为 6 倍。
- 使用本轮冻结 PricingAt，包含无 resolver 的结算路径，避免异步落库跨优惠边界；不重算历史账单。
- 运营者显式价格、免费值、12h nullable/flat 配置继续优先；实际响应分桶驱动 30m/12h 计费。
- 强制 Ultrafast 同时检查模型和账号目录能力。官方 API 默认按地区能力识别；OAuth 和第三方必须有目录证据。OAuth 尚无证据时不强制，不根据 Pro/Promax 套餐名赋权，也不增加额外主动拉取。
- 6.2 EU 数据驻留不强制 Ultrafast；Fast 不被旧 Astra 的 EU 限制误伤。全局 tier 策略仍可过滤或拒绝；“免费 Fast”不豁免 Ultrafast。
- 不自动开启 2h/8h 后台完成窗口及折扣，没有新增充值或自动充值后的消费操作。

## 资源与数据库边界

两份迁移保持原完整文件名及内容校验，不重编号：`251_group_force_openai_ultrafast.sql`、`251_group_usage_rollup_nonblocking_build.sql`。已有三份 249 和 250 不修改。新开关默认 false；rollup 新增过期下界和重建脏区间，不重写旧账单，迁移仍存在 ALTER/函数替换的锁开销。

扣费每批 128 条不是队列总长度上限。上游保留同步等待各命令结果和停机排空机制；服务数据库长期缓慢时仍需要监控待结算积压，不能声称合并后内存恒定。调度 10 秒直接复用/20 秒宽限及 200ms last_used 缓存，与 OAuth catalog 的 60/300 秒配置是不同机制。16 MiB 是保留快照的编码源预算，不是 Go 堆或临时编码内存的硬上限。

补充资源观测（GOMAXPROCS=2）：

- 15 项计费通道用例单独执行并采集内存，包括 24 用户 × 60 命令、队列拆批、失败回退、超时及 Stop 排空；测试进程峰值 RSS **65,920 KiB（约 64.4 MiB）**。这是测试进程读数，不是生产内存容量保证。
- 使用上游自带的双 bucket 快照发布微基准，每项仅 3 次迭代。100 个账号：2,992,218 → 1,443,122 B/op；10,000 个账号：297,446,352 → 143,846,741 B/op，编码分配量约减少 52%。对应 10,000 账号耗时为约 1.141 → 0.785 秒/次。
- 上述 B/op 是一轮批量快照发布的累计分配量，**不是每个网关请求的常驻内存**；也不是 Redis/数据库端到端吞吐或生产规模峰值压测。没有重跑 Key 并发查询专项。

测试继续低并发。service 测试包本身很大，首次编译增加 swap 后已主动停止，后续使用 Go 堆预算与较低 GOGC，并将前端大堆类型检查/构建错峰；没有改应用运行内存配置。未重做 Key 并发查询专项压测。

## 本地功能保留清单

credits 显式授权、历史正余额试调度、被动观测、耗尽锁定/手动重新授权、普通编辑与 UpdateExtra 并发保护、调度摘要；无自动充值；Pro/Spark 目录并集；catalog 60/300 配置及单份正文缓存；当前轮 PricingAt/cyber 记账；加密 compaction 与完整重放；stream_options；入口 JSONB、多 IP/CIDR、默认校验关闭和客户端 IP 原行为；Peer 文案；移除 turn-state 长度；Token/s；首页 $/min 与 TPM 同行同频；pN 版本比较；插件门控；Command Code 目录凭据边界；MCP 默认关闭；Docker 法务文档和自有发布流程。

## 验证与交付记录

验证日志目录：`/tmp/sub2api-v022-gpt62.kGYf3k`。

已完成的验证：

| 项目 | 结果 |
|---|---|
| Go 1.27.4 全量 unit | 63 个包、29,523 项测试通过（含子测试），17 项既有条件跳过；独立 PostgreSQL 的 249/250/251 迁移用例实际执行，没有跳过。 |
| PostgreSQL/Redis repository integration | 1,736 项通过，1 项既有 TODO 跳过；涵盖批量扣费与逐条一致性、幂等/回退、rollup 重建并发写、调度与 credits。 |
| Node 24.22.3 前端完整测试 | 391 个文件、3,194 项通过；单 worker。 |
| 前端类型/lint/build | typecheck、lint:check、生产 build 均通过；build 只有既有的大 chunk 和 Browserslist 数据陈旧提示。 |
| golangci-lint 2.14.0 | 0 issues。 |
| govulncheck 1.8.0 全源码 | 0 项可达漏洞，导入包中 0 项；依赖模块仍有 6 项，但未发现调用受影响路径。不是声称整个依赖树已无漏洞。 |
| 发布与部署辅助检查 | 11 项 release matrix 测试通过；release-images 语法、Docker runtime resources、compose security/gateway env/simple mode、Caddy cache/SSE 检查通过。 |
| 定向 race | WS relay / HTTP 工具层 220 项、repository 调度缓存/credits/计费/汇总 146 项通过，共 366 项，无 race 告警。 |
| embed 构建与测试 | Go 1.27.4、CGO_ENABLED=0、embed、trimpath、去符号的 Linux/amd64 构建成功；internal/web 和 cmd/server 两包共 113 项通过。`-version` 实际输出 `Sub2API 0.2.22 (commit: 14c474a80, built: 2026-11-18)`，只查询版本，没有启动服务。 |

初次验证暴露的差异均有具体处理：保留原默认测试模型顺序；收窄 effort 别名范围，避免误接受旧 `gpt-6-sol-max`；修复原生外部上下文的 namespace 丢失；更新 OAuth/API schema 的差异预期；SQL mock 和真实 PostgreSQL 测试明确区分扣费与账号成本，没有移除费用分解断言。

迁移测试最初使用独立 schema，旧 108a 迁移明确检查 public，导致测试环境中的 109 迁移失败；改用独立数据库后通过，**没有修改历史迁移或校验和**。第一次前端全测使用环境自带 Node 20，随后已用校验 SHA256 的临时 Node 24 完整复跑，最终验收采用 Node 24 结果。类型检查的低堆预算曾不足，错峰提高到 3 GiB 后通过，未更换系统全局 Node。

Apple container 脚本语法检查通过，但其 runtime shell 测试使用 macOS 的 `stat -f %Lp`，在本机 GNU stat 下失败；未冒充通过，也没有为 Linux 测试而改写 Mac 脚本。仓库对应 CI job 运行于 macOS，仍需由该平台验证。

没有调用真实付费模型，因此真实 OAuth 的即时可用模型、tier 权限和服务端压缩成功不属于本地测试的证明范围。没有操作生产数据库、提交 PR、构建/推送 Docker 镜像。

## 最终交接

代码完成并提交于本地合并分支，报告另行提交保存。`main` 与 `origin/main` 仍为 `482c38f8a`，本轮没有合 main 或 push。

本次隔离 PostgreSQL/Redis 临时容器已清理，只删除了可通过重跑测试重建的测试数据，没有动现有业务容器。日志、内存 profile、微基准输出和本地二进制保留在上述 `/tmp/sub2api-v022-gpt62.kGYf3k` 目录。二进制是验证产物，不是已发布镜像。
