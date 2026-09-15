# 上游 v0.2.5 合并报告

## 范围与基线

- 工作分支：`merge/upstream-v0.2.5`；本次不合回 main、不 push、不创建 PR、不部署。
- 本地 main / origin/main 基线：`1377a5ddb44be94860ac90bf6e801f4841123090`。
- 已有上游版本：v0.2.4；目标为 [v0.2.5 release](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.5)，tag 对应 `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`。
- 上游 release 发布时间：2026-09-15 17:02:34（Asia/Shanghai）。v0.2.4 → v0.2.5 的上游差异为 197 笔提交（包含 merge）、447 个文件。
- 本次再次 fast-forward 更新 `/home/siyixuan/codes/codex`，核对版本为 `a113f3e063dcb4b0a7d8a4e63cfb2fea83996116`。客户端仓库没有本次代码修改。
- 上游 tag 内 `backend/cmd/server/VERSION` 仍为 `0.2.4`，本 fork 同步为 `0.2.5`。应用版本与 Codex 客户端身份版本是不同字段。

## 合入的主要上游更新

1. OpenCode 平台及 Zen / GO 账号类型，按模型选择 Chat Completions、Responses、Anthropic Messages 协议，派生 OpenCode 会话提示以支持上游缓存。
2. 站点计费模式三态开关：充值与订阅、仅充值、仅订阅；关联入口、页面权限和文案随配置调整。
3. 订阅批量操作、Key 批量编辑、选中用户批量删除、Key 分组按提供商筛选；注册确认密码和资料错误提示等管理端、用户端改进。
4. WS 连接池常驻 reader、上游 ping 应答、连接关闭及时出池、排队等待者随池状态变动重选，以及重试换新连接、放宽非热路径探活超时。
5. Codex 模型目录独立保留默认与最大上下文窗口、保留展示名；单模型检索基于当前可见目录，避免检索接口和列表权限不一致。
6. OAuth 图像请求的原生 Codex Images 通道与兼容回退、图像及用量费用八位小数、批量图像账号优先级修正。
7. 大图片 Responses 请求低分配读取、原始 JSON 输入片段复用；Responses Lite namespace 调用保留，流式文本仅在可判断时从 done / terminal 恢复，避免重复输出。
8. `service_tier` 新增显式 `missing` 匹配规则，修复指定条件下未传 tier 的 priority 策略。
9. DeepSeek 模型校验、V4.1-Flash 计费与模型切换；Antigravity 模型、账号级 token 缓存与 SSE 修正；Grok sequence_number、媒体槽位；Gemini 成功 HTTP 状态内的错误判定。
10. 运维 Token 统计扩展全部平台、TTFT 展示；渠道监控平台/UTC 分桶/刷新修正；代理凭据显式清空、导出筛选一致性、兑换与订阅管理等修复。
11. 平台限额仅保留实际配置的限额记录；Ollama Cloud 用量窗口异步重置；临时服务故障不再错误清除用户会话等。

以上采用上游主体实现。下面列出的客户端语义差异做了适配，不按 release 标题直接覆盖本地行为。

## 客户端语义与实际逻辑变化

### 一套线程/执行作用域

上游的 request_kind 分道接入本地线程作用域构造器，抢占、池兼容性、HTTP→WS 转发、turn-state 与 reasoning 状态不维护第二套平行 owner。

| 场景 | 合并后行为 |
| --- | --- |
| 正文内嵌 `client_metadata["x-codex-turn-metadata"]` | 对当前请求的身份与类型优先于握手兼容快照；它是 JSON 字符串，不按普通字符串丢弃其中结构 |
| 正文平铺 thread/session 与旧头 | 作为兼容投影及缺省回退；正文声明优先于过期握手头 |
| 明确清空内嵌快照 | 不从旧握手快照复活 request_kind；明确清空 thread_id 不重用旧线程身份 |
| `turn` / `prewarm` / `compaction` | 同一线程的主执行通道，保留顺序采样、预热与压缩续链 |
| `memory` / 未知显式 request_kind | 按类型隔离；不能抢占同线程用户 turn |
| 旧式 guardian，无独立 canonical thread | 按 subagent 类型隔离，避免占用父线程主通道 |
| 现代 guardian / 普通子代理，具有独立 thread | 使用自身线程身份，父子不互抢 |
| 只有 session、prompt_cache_key 或请求内容 | 不声明排他线程 owner；本地状态使用请求/连接局部匿名身份，不把共享根 session 当成子代理身份 |

身份在出站账号 namespace / 指纹改写前捕获并冻结。连接内后续帧和重试不会移动 owner；逐帧业务元数据仍按客户端当前报文转发，不能用冻结 owner 的动作冻结所有 client_metadata。

身份构造只从大报文提取一次 `client_metadata`，内嵌快照也只解析一次；匿名 ID 按需生成，不再为了匿名分支重跑整份身份解析。排查大帧 race 测试时发现早期合并实现存在重复扫描，因此做了这项优化；复测证明它不是测试超时的全部原因，不能把它单独称为该超时的修复。

与原 main 相比，本次新增后台请求类型隔离，并把身份提取的优先级改为客户端正文优先。没有 lane 时保留本地 `thread:v1:` 主通道哈希形式；group、API Key、session 的隔离仍在，运输状态另带 account ID。出站指纹配置不会改变本地池的原始线程隔离。

账号 failover 最终进入 passthrough / HTTP bridge 时仍解除旧池模式的抢占登记；旧 owner 的缓存写入保护、compare-and-delete 清理和新 owner 状态保护继续保留。

客户端依据：`codex-rs/core/src/responses_metadata.rs` 的 canonical blob / compatibility projection 约定，`CodexResponsesRequestKind` 枚举和 memory 的身份投影；`turn_metadata.rs` 的独立 memory turn。最新 Guardian 提交把 reviewer 清理归到 ThreadManager，仍使用独立 reviewer 与临时 fork，没有恢复“所有后台请求共享父线程 owner”的语义。

### 抢占关闭与连接释放

- 同一执行作用域被新连接替换时，旧 owner 立即失去写入资格，然后先向客户端发送 `1013` 和明确的替换原因，再取消 I/O。
- 关闭通知最长给 1 秒宽限；新连接不等待旧客户端完成关闭握手。
- 只由新 owner 清理自己的账号状态，旧 owner 回调不删除替代连接写入的数据。
- 保留本地普通裸 `error` 的 500ms 非续期结算窗口、成对 `response.failed` 的权威 usage、失败连接废弃和并发槽释放。
- 本次将 ctx_pool 裸 error 结束路径接到 `conn.abort()`，同时关闭池的退出信号与实际 socket；常驻 reader 即使阻塞在结果投递，也能退出。
- 保留已有 passthrough drain 完成通知竞态修复（`77b0ddf50`），上游本次没有等价覆盖它。

### ctx_pool / passthrough 一致性

保留原 main 已完成的 Astra ctx_pool 支持、逐帧嵌套元数据、configuration_update 继承、推理策略、多轮计费、预热 `generate=false` 和 compact 识别。保留真实 native compaction 终端与普通采样的区分，不把带 compaction 标签的请求一律当成独立 `/responses/compact`。

`stream_options.reasoning_summary_delivery=sequential_cutoff` 仍按客户端语义保留；不因上游通用“不支持参数列表”重新删除它，也不因走 WS 就擅自强制改变请求的 stream 语义。客户端当前在 OpenAI 路由、开启并发 reasoning summary 且指定 summary 时发送该选项。

连接本地的 `previous_response_id` / store=false 续链仍保持严格亲和；丢失原连接时不自动删除 previous_response_id 并把仅含 delta 的请求伪装为完整历史。该边界也对照了 [OpenAI WebSocket mode 文档](https://developers.openai.com/api/docs/guides/websocket-mode)。显式依赖原生 response.steer 的客户端继续使用 passthrough，本次没有宣称 ctx_pool 新增原生 steer 支持。

### system / developer / instructions 与 agent_message

不采纳上游 `52558381c` 在通用 Responses→Chat 桥内合并开头指令、把会话中途 system/developer 降为 user 的行为。保留本地规则：instructions 单独保留，developer 按现有 Chat 兼容规则映射为 system，不因出现位置而降低指令优先级；OAuth 对 system→developer 的既有目标协议适配不变。

采纳 `agent_message` 可读正文恢复，但遵循客户端 `protocol/src/models.rs::plaintext_agent_message_content`：明文片段按原顺序以换行连接；任何 `encrypted_content` 都不是可直接拼接的明文。Chat 桥对含加密片段的请求明确报错并要求原生 Responses，不泄漏密文到错误文案，也不仅发送残缺信封。原生 Responses 通道仍原样传递加密消息。

### Astra 模型目录与 tier

- 通用模型采用上游最大上下文窗口同步、持久化与账号能力交集实现。
- Astra 继续区分通用 API 模型上限和 Codex 客户端 compact 窗口；新增 `MaxContextWindow` 也纳入原有保护，不能通过新字段间接覆盖本地窗口与 Ultra 工作流。
- 本地 Codex Astra 默认仍为 `context_window=272000`、`max_context_window=872000`；已有原生 Codex manifest 的显式字段按原有 completion 逻辑保留。
- Pro + Spark 影子账号的合格贡献账号模型目录合并规则保留，不退回“只取一份影子目录”的行为。
- 缺少 service_tier 不等于全局启用 priority：只有显式 `missing` + force_priority 规则或原有合法的分组策略才注入。旧 `all` 规则不新增此含义。
- Astra EU 驻留保护也覆盖新 `missing` 分支，HTTP 与 WS 都不会绕过它注入 priority。

## 保留的本地自定义功能

- Docker 法务文档构建保留；Dockerfile、Go 依赖与 CI/lint 配置未因本次合并改变。
- 默认 TUI 身份与指定 UA 保留：`codex-tui/0.153.4 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.153.4)`。
- 有效客户端版本仍在管理员设置的网关转发 / OpenAI Codex 版本同步区域显示；本次没有移动 UI 位置。
- device 指纹默认开启，session 与更深层 ID 默认不启用；显式选项和本地身份派生修复保留。
- system/developer/instructions 策略、typed ID / replay 边界、Astra compact / Ultra / 计费、父子线程隔离及已有裸 error / drain 修复保留。
- 原有 Key 并发填充、精确批量与稀疏聚合接口保留。按用户要求，本次不重复该接口的专项性能检查。
- preread 零拷贝、原始传输字节/解压字节统计、JSON 大整数精度与原始图片片段保留。

## 内存与性能边界

### 连接池

动态容量开启且账号并发为正时，使用上游公式 `min(ceil(account_concurrency × factor), max_conns_per_account)`；默认 OAuth / API Key factor 为 5.0，默认硬上限 128。显式配置值不被覆盖，真实请求并发槽不放大五倍。

这不只是改默认数值：原 mode-router-v2 路径直接使用 `min(account_concurrency, hard_cap)`，绕过了系数和动态开关。本次统一到上述配置逻辑；关闭动态容量时使用硬上限。mode-router-v2 下非正账号并发仍不分配池容量，与原 main 相同。

每条已建池连接新增一个常驻 reader，结果 channel 容量为 1；最多额外持有一个正在投递的完整消息，不无限预读。池内上游单消息读取上限仍为 16MiB（客户端入站 / HTTP bridge 的读限制另行配置），因此“一个缓冲消息 + 一个在途消息”的极端数据量可接近 32MiB/连接；128 条同时遭遇最大消息的极端场景仅这一部分就可接近 4GiB，尚不包括请求、解析、响应累计、TLS/WS 等开销。它不是预分配，也不能被描述为整池固定小内存。

新回归通过无缓冲假上游验证：消费者停滞时，reader 只接受两个消息，不读取第三个；abort 能结束阻塞投递。真实 WS 保活、关闭回收、排队唤醒以及原有裸 error 并发槽释放用例另行验证。该检查证明代码的有界性，不代替目标部署规模的压测。

### 大请求

采用上游分块读取后一次精确合并、原始 JSON 输入片段复用；避免完整图片字符串在兼容层反复解码与复制。保留所有本地统计与精度边界。本次大图片微基准和完整测试结果列于下节；不把微基准分配量等同于服务端总峰值。

69MiB 原生图片请求、每项 3 次迭代的本机微基准：legacy ingress 0 B/op，input ID 检查 24 B/op，reasoning replay 0 B/op，API-key store=false replay 21 B/op（均为请求不需要改写的路径）。读取同样大小的 body 为 144,707,136 B/op、83 allocs/op，约为正文大小两倍；读入/合并本身不是零分配。这是 HTTP body / 兼容处理微基准，不表示 WS 支持 69MiB 单消息。日志为 `large-request-bench.log`。未额外编译旧版本做同机前后对比，不给出没有实测的改善百分比。

构建与测试重任务串行，使用 `MemoryMax=3G`、`MemorySwapMax=0` 的独立 scope；Go 限制包并行与 GC 目标。前端类型检查与打包分开执行，避免重复类型检查进程同时占用内存。

## 数据库迁移

完整保留两个不同文件，不因数字前缀相同而改名：

- `238_opencode_go_platform.sql`：为 quota、composite route、monitor 和 template 约束增加 OpenCode，保留 MiniMax。
- `238_purge_unlimited_user_platform_quotas.sql`：仅删除日、周、月三个 limit 全部为 NULL 的 quota 行；0 是配置值，不应删除。任一已配置限额的行（包含软删除历史）及其 usage 保留。

迁移记录按完整 filename + checksum 区分。新增集成回归在临时 PostgreSQL 中检查两个迁移分别登记、重复执行幂等、四类平台约束、NULL / 0 / 部分限额 / 软删除记录和 retained usage。没有连接或迁移现有业务库。未来实际升级时该 DELETE 会删除历史无限额 quota 行，这是上游的数据整理行为，不是只读迁移。

## 验证记录

临时日志目录：`/tmp/sub2api-v025-merge.DRXNqk`。后端使用 Go 1.27.0，前端环境 Node 20.20.2 / pnpm 9.15.0。

| 检查 | 结果 / 日志 |
| --- | --- |
| 全量后端 unit：`go test -p 1 -parallel 2 -tags=unit ./...` | 最终代码通过；`unit-verified.log`，在身份解析优化和测试夹具调整后重新运行，service 189.944s |
| 全量后端 integration：`CI=true go test -p 1 -parallel 2 -tags=integration ./...` | 最终代码通过；`integration-verified.log`，service 132.206s、handler 41.857s。repository 复用成功缓存；此前 `integration.log` 中实际临时数据库执行为 33.973s，包含两个 238 迁移验证，临时容器已清理 |
| 严格 golangci-lint 2.13.2 | 最终代码 `0 issues`；`lint-verified.log`，未修改或放宽 lint 配置 |
| 69MiB 大请求微基准 | 通过；`large-request-bench.log`，每项 3 次；结果及边界见上节 |
| 相关 WebSocket / Astra / relay race | 通过；`race-verified.log`。同样的 `GOMAXPROCS=1`、`-parallel 1` 下，service 60.633s、handler 5.489s、relay 11.446s，无 DATA RACE；17MiB 功能用例的测试等待窗口调整见下文 |
| 前端类型检查 | `vue-tsc -b` 通过；`frontend-typecheck.log` |
| 前端全量 Vitest | 287 个文件 / 2188 条测试通过；`frontend-tests.log`，单 fork 执行 |
| 前端打包 | 通过；`frontend-build.log`，Vite 26.99s。先单独通过类型检查，打包时仅在内存中的配置移除重复 checker，不改 Vite 配置文件；全量 Vitest 已覆盖构建前要求的 i18n 检查 |
| 后端 embed 构建 | 通过；`backend-embed-build.log`，`go build -p 1 -tags=embed ./cmd/server`，产物输出到临时日志目录，未启动服务或发布镜像 |

最终 unit / integration / lint / race 峰值单进程 RSS 分别约 2.61 / 1.96 / 2.12 / 2.94GiB；这些数值包含构建工具，不能当作运行服务的内存测量。进程组内存限制为 3GiB，已观察的阶段没有 OOM，scope swap 为 0；race 编译曾触及硬上限并触发内存回收，不是没有内存压力。

前端类型检查 / Vitest / 打包峰值单进程 RSS 分别约 1.84 / 0.95 / 1.40GiB。保留 Browserslist 数据较旧、部分 chunk 较大及测试 mock 的既有提醒，没有为消除提醒而更改依赖锁文件、构建阈值或功能断言。

race 首轮和独立复跑没有 DATA RACE 报告，但 17MiB 首帧 HTTP bridge 功能用例超出其 10 秒客户端等待。减少身份扫描后仍会超时；CPU 采样中七成以上时间落在 race/ThreadSanitizer 读写插桩相关符号，解析持续推进，并非停在等待锁或上游 I/O。这条现有测试并非延迟 SLA 测试，因此仅把该 fixture 的代理上下文和客户端等待改为 30 秒，17MiB 数据、路由/事件/计费结果断言全部保留，没有改生产超时。诊断日志为 `race.log`、`race-large-frame-repeat.log`、`race-large-frame-profile.log`，CPU profile 为 `large-frame-race.cpu`。

没有调用真实模型账号进行计费请求；客户端源码检查和本地真实 WS / mock 上游回归不能等同于官方上游全链路验收。

## 关键实现位置

- [统一执行身份与请求类型分道](../backend/internal/service/openai_ws_execution_scope.go)、[原始身份冻结](../backend/internal/service/openai_ws_thread_scope.go)。
- [抢占关闭与 owner 写入保护](../backend/internal/service/openai_ws_session_preemption.go)。
- [连接池常驻 reader](../backend/internal/service/openai_ws_pool.go)、[ctx_pool 裸 error 关闭衔接](../backend/internal/service/openai_ws_forwarder_ingress.go)。
- [指令角色与 agent_message 桥接](../backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go)。
- [Astra 模型目录保护](../backend/internal/service/openai_codex_model_metadata.go)、[缺省 tier 的 EU 保护](../backend/internal/service/openai_gateway_request_body.go)。
- [两个 238 迁移的数据库回归](../backend/internal/repository/upstream_v025_migrations_integration_test.go)。
