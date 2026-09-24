# v0.2.8 合并报告（2026-09-24）

## 范围与基线

- 实施分支：`merge/upstream-v0.2.8`；本地 `main` 保持 `ecbd6c919b90706cd58a27931e2ee6a800815bd8`。
- 上游 release：[`v0.2.8`](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.8)，目标提交 `fd80b08c90b55edcad5b00171b53f08721d30da1`。
- 完整纳入 `v0.2.7 → v0.2.8` 的 237 个提交（140 个非合并提交），上游范围涉及 473 个文件。不是只摘取 release 文案中的模型支持。
- 发布后的 `a3eb7ef30` 仅同步 `backend/cmd/server/VERSION`，单独纳入，使源码版本为 `0.2.8`。不追随其他未发布功能。
- 合并提交：`bacb53ec4`，两个父提交为上述 main 基线和 release 提交；版本同步 cherry-pick 为 `5882d7e7a`。
- Codex 客户端已 `pull --ff-only` 到 `a16381c4457e23191d4786968011434c37a04041`。相对计划时的 `7dae8c53d9`，新增 prewarm 状态检查复用等改动；typed ID 的原始前缀检查规则没有改变。
- 仅本地合并、提交与验证；不合回 main、不 push、不创建 PR、不触发发布，不修改业务数据库，不调用真实模型账号。

## 上游更新覆盖

1. 模型：GPT-6 Sol/Luna、Claude Opus 5.5、Grok 4.7 的模型目录、兼容入口和定价。Sol/Luna 与 fork 的等价代码去重，差异见下文。
2. 转发：HTTP 流在终端事件完整输出后结束；响应体关闭前取消对应请求尝试；OpenAI HTTP/2 ReadIdle/Ping 容错窗口；协议正确且不重复的流式错误；`error.status` 分类；心跳不计语义输出/TTFT；补齐工具参数 done 事件；恢复公开响应模型别名。
3. 调度：普通调度支持 response 所属账号亲和，包含分组、能力、渠道、代理隔离和传输检查；RPM 投影及调度倍率回退；选号读取分组不再附带账号计数；API Key 账号对未知模型不被 OAuth 目录限制误伤。
4. 计费：按推理等级配置倍率、最终转发 effort 记录、token 区间科学计数法解析、视频按秒价格展示。
5. OpenCode Go：官方用量窗口、自动刷新、同 Key 共享、活动去抖、主动查询、7d/1m 展示；更新账号时保留服务管理的用量状态；规范化 `/zen/go`；CF 1010 不再误禁用账号。
6. 账号与版本：Codex 积分/推荐邀请及结果处理；Claude Code 版本自动同步；OAuth 重新授权保留设置。
7. 运维与业务：月度备份独立保留、S3 密钥继承加密、滚动日志保留、简易模式 API Key 消费窗口及默认分组开关、线下提现幂等、TypeSafe 独立审核引擎配置。
8. 其他协议：工具 Schema 的非法 null required/type 清理及 Antigravity 数组 Schema 兼容；DeepSeek 图片 url 别名；Gemini 传输错误 failover；Vertex RetryInfo 与短窗口冷却；Grok 冷却期间配额查询；图像余额错误及兼容路由。
9. 前端：异步过期响应隔离、用户/账号切换、IME 提交、日期/到期展示、对话框滚动锁、失败重试、列表就地更新等。
10. CI：独立构建矩阵、固定 release 工具和缓存路径、dry-run、release 辅助脚本测试。仓库所有者及镜像凭据仍取自运行仓库；没有实际执行发布。

注意：release 摘要对 Vertex 429 的文字有歧义。代码实际是遵从 RetryInfo；无法解析时 Vertex service-account 使用短暂冷却，而不是继续一律等到 PST 午夜。

## 关键逻辑变化与保留项

### Sol/Luna：采用共享实现，但不改变 fork 策略

- 上游与本地重复的模型列表、映射、switch 分支、前端配置和 JSON 定价条目已去重。价格主定义复用上游，billing 与 pricing lookup 指向同一份回退定义。
- 采用动态价格中“字段是否显式出现”的判断，保留显式零缓存写入价；管理员覆盖优先，Fast、Flex 和长上下文倍率仍按原有计费组合规则执行。
- GPT-6 Astra/Sol/Luna 均维持 `none/minimal → low`，缺失 effort 不主动补齐。Ultra 仍按 Astra→xhigh、Sol→max、Luna 手动输入→max 处理，Luna 不新增 Ultra 菜单。
- 原生 `reasoning.mode` 与 effort 独立；使用最终映射的模型处理，不把公共别名误当旧模型。configuration_update 内的 effort 仍参与规范化和最终计费。
- 只识别已有正式 GPT-6 型号及既有显式映射，不恢复裸 `gpt-6 → Astra`，不猜测 effort/日期/preview 后缀型号的价格。
- 完整保留 9 月 23 日 catalog 的能力、272K/872K 客户端窗口、Sol Ultra、动态更新、null tier、原生 false/null 优先及 API Key Lite 限制；不替换成上游简化的旧型号模板。
- 官方 Chat Completions 的工具调用要求与 fork 的 none→low 策略不能同时满足时，仍明确返回 400，使用 Responses-capable 路由；不把这个官方限制扩展到任意第三方端点。

额外修正了核对中发现的一条原有旁路：兼容 API Key Responses 入口可能把 `none` 当目录占位值提前删除，导致后续 GPT-6 转换看不到它。现在已识别的 GPT-6（含账号映射后的别名）保留该字段直至转换为 low；其他模型的目录占位清理规则不变。该问题不是声称由本次上游 release 引入。

官方 Sol/Luna 支持 none；这里转换为 low 是用户明确指定的 fork 策略，不是官方限制。[Sol 官方模型说明](https://developers.openai.com/api/docs/models/gpt-6-sol)、[reasoning 与 configuration_update](https://developers.openai.com/api/docs/guides/reasoning)。

### Prompt：不随新增模型扩大改写范围

保留原 system/developer/instructions 处理及 Codex 目录模板。未采用上游新增的非 GPT 目录模板身份替换、Antigravity system 身份中和，也未扩大 Sol/Luna 的自动 prompt-cache-key、todo guard、历史补写/续写开关。原有 Antigravity 归因元数据清理不变。客户端原生 model_messages 元数据不等于网关主动向请求注入提示词。

### 流式结束、WS 与统计

采用上游普通 HTTP SSE 终端即收尾，但先处理完整终端事件中的 usage、原始响应模型和 Turn State 长度，再结束当前请求。上游针对 Codex bare error 后续事件组合的保守例外仍保留，不能理解为任意错误流都会立即结束。连接关闭使用每次请求尝试的取消上下文，不取消外层重试或后续计费。

WS 桥接中的 keepalive 可以继续发送，但不作为“已有语义输出”而禁止安全 failover；客户端已断开后不重放请求。实际发出的心跳字节仍计入响应大小，不与 TTFT 混淆。

本地原生 WS/ctx_pool 的子代理 thread 隔离、request_kind 分道、Astra 接入、compact 识别、stream_options、裸 error 有界收尾、连接废弃、drain 和并发槽释放继续保留。passthrough 自动续响应不被 HTTP 收尾规则截断；没有把 ctx_pool 扩展成新的 steer 实现。官方 steering 可以在前一响应结束后自动生成后继响应，不能把单个 response 的结束等同于 WS 连接结束。[官方 steering 说明](https://developers.openai.com/api/docs/guides/steering)。

### Typed ID 与 turn metadata

Codex `ResponseItemId::is_prefixed` 只检查下划线前后非空。保留本地对应规则和引用/工具配对，不采用上游按 item 类型固定前缀、长度超过 64 即删除的通用做法。

采用 turn metadata 的 Unicode→ASCII JSON 转义，包括代理对；headers 与 client_metadata 内嵌 JSON 使用同样规则。补齐 UseNumber 解码，回归 `9007199254740993`，防止改写无关 ID/计数的精度。显式 session/full 模式仍可使用，但默认 device 开启、session/更深 ID 关闭不变。

### 计费倍率：一项明确的默认行为改变

采用上游 `reasoning_effort_multipliers`，替代旧的单独 max 倍率。已有显式 max 配置迁入 map，未配置的档位按 1×，显式清空后重复迁移不能恢复旧配置。计费取最终转发 effort；GPT-6 none→low 按 low 档位收费，configuration_update 的最后生效值优先。

**Fable 5.1 不再隐式 max=3×，未显式配置时变为 1×。** 这是批准计划中的上游规则变化。需要原 3× 时应显式配置 `max: 3`；此次没有读取或修改业务数据库中的收费设置。

### 插件目录：上游主体与本地安全边界

采用结构化账号信息和宿主授予的 scope；保留旧 AccountIds 响应。active 但限流/暂停的账号可列出，其 schedulable 状态由宿主判断；管理性禁用账号不能直接解析凭据。

保留 KV/目录 3 秒、身份解析 10 秒的调用预算及更短调用方 deadline。只读快照去掉 Credentials、任意 Extra、Proxy 对象和循环关系，保留 ProxyID 等调度字段；不会因为拥有元数据枚举权限就从快照取得代理密码或未知 Extra 中的秘密。凭据仍通过授权的 ResolveOutboundIdentity 单独取得。这比上游直接暴露 Extra/Proxy 更保守；依赖这些原始字段的新插件需要显式设计非敏感契约。

### 其他重要兼容

- Opus 5.5 的带标记 Anthropic thinking envelope 回放与本地已有的签名兼容分支合并，不会被自动合并产生的前一个 reasoning case 短路。
- DeepSeek input_image 增加 url 别名，仅作用于实际 DeepSeek 目标（含映射到官方 DeepSeek 地址的 OpenAI 类型账号）；保留已有工具媒体提取的错误返回、指令边界和大整数精度。不把 Kimi/MiniMax/OpenCode 的输入全部套用 DeepSeek 图片转换。
- 保留本地工具 Schema 的保守无损转换和复杂度限制，不用有损摊平替代组合约束。
- Docker 法务文档、Codex UA 0.155.1 及 UI 生效版本展示、Pro/Spark 目录合并、Key 真实并发/精确批量/稀疏聚合、用量快照一致性、原始请求大小均保留。
- Usage 页 Turn State 四路长度默认隐藏在 body size 后，0 与未知区分；没有恢复被撤回的原始票据采集、缓存、注入模块，也没有加入暂缓的请求 zstd 压缩。

## 数据库迁移

新加入 238b（审核元数据）、239（推理倍率）、240（提现幂等）；保留本地 `239_add_usage_log_codex_turn_state_lengths.sql` 原名原内容。迁移主键是完整 filename，所以两份 239 共存，而不是重命名已应用的迁移。

新集成回归在临时 PostgreSQL 上检查完整迁移后的两份记录和四个 nullable 长度字段；用会话临时表模拟旧版倍率结构，覆盖显式值、未配置、已有空 map/非空 map、旧字段清理以及重放幂等。不改业务数据库。

## 验证记录

日志目录：`/tmp/sub2api-v028-merge.3p9oHR`。以下最终检查均已通过。

| 检查 | 结果与日志 |
| --- | --- |
| 后端全量 unit | 58 个含测试的包通过，`unit2.log`；service 197.876s，整体 7:04.99。之后只清理了测试初始化的全局写入，并由下述扩大范围的 race 与 lint 复验；版本文件另由最终构建确认 |
| repository 全量 integration | 临时 PostgreSQL/Redis 实际运行，35.428s，包含两项新增迁移回归，`integration.log`；CI=true 防止 Docker 不可用时静默跳过；测试容器已清理 |
| HTTP 连接/协议 race | repository、apicompat 两包通过，`transport-race.log`；覆盖 Close/Read 竞争、取消、HTTP/2、Opus 5.5、GPT-6 和工具 Schema |
| WS/兼容入口 race | service、handler、openai_ws_v2 三包均通过，`ws-race2.log`；保持 `-parallel 2`，扩大包含 ForwardAsAnthropic；运行时间分别 68.834s、5.943s、7.156s |
| 严格 Go lint | golangci-lint 2.13.2 / Go 1.27.0 全项目 0 issues，`lint.log`；测试初始化调整后 service 包再次 0 issues，`lint-final.log` |
| 前端全量测试 | 335 个文件、2513 个用例通过，两个 worker，211.76s，`frontend-tests.log` |
| 前端类型检查 | 独立 `vue-tsc -b` 通过，44.70s，`frontend-typecheck.log` |
| 前端全量 ESLint | 无自动修复、无放宽规则，退出 0，`frontend-lint.log` |
| 前端生产构建 | Vite 通过，29.54s，`frontend-build.log`；独立类型检查已通过，构建调用中临时排除重复 checker 进程，未修改仓库构建配置 |
| 最终后端 embed 构建 | CGO_ENABLED=0、Go 1.27.0 构建成功，`backend-build.log`；未注入 Version ldflag，直接从源码 VERSION 得到 `Sub2API 0.2.8`，见 `backend-version.log`；未启动业务服务 |
| release/配置检查 | 10 个 Python release helper 测试、发布脚本 bash 语法、简易模式 Compose 环境检查通过；不构建或推送镜像 |
| 合并完整性 | release 提交是实施分支祖先；无冲突标记、`git diff --check` 通过；定价 JSON 的 204 个顶层型号键没有重复 |

初轮发现并修正了自动合并的重复定义/重复 reasoning 分支、上游新用例的本地函数签名差异、指纹 SQL mock，以及与已批准 fork 策略相反的断言。没有跳过失败用例或放宽 lint 规则。

提高 race 测试并行度后，还捕获到既有测试初始化竞争：`openai_compat_model_test.go` 中两个并行用例同时写 `gin.SetMode`。同一文件共 27 处并行初始化有这个模式，现移除这些不必要的进程全局写入，保留 t.Parallel 和全部业务断言，并将该文件的 ForwardAsAnthropic 用例纳入并行 race 复验。冲突栈来自测试初始化，不能把它误报成生产转发状态的数据竞争。

测试采用受限并行：普通 Go 包并行 2、测试并行 4，后端重任务 scope 限制内存且禁用该 scope 的 swap；前端 Vitest 使用两个 worker。WS race 大包编译等待全量 lint 结束后启动，测试执行阶段与较轻或独立检查重叠。

全量 unit 的 scope 观测峰值 3,684,003,840 bytes（约 3.43 GiB），memory.events 的 max/oom/oom_kill 均为 0，scope swap 为 0。最终 WS race 最大单进程 RSS 为 3,200,792 KiB；全量 lint 为 2,530,320 KiB；前端测试为 867,996 KiB，类型检查为 1,953,408 KiB。最大单进程 RSS 不等于整组进程峰值，更不代表线上转发服务的运行内存。

最终 backend build 初始限制 2 GiB，触发了回收（观测 max=2311、oom=0、oom_kill=0）；其他重任务结束、宿主机仍有约 4.7 GiB 可用内存后，将该 scope 上限调为 3 GiB，保持 MemorySwapMax=0，构建成功。这里的 swap=0 指测试 scope，不是声称宿主机原有 swap 完全没有变化。

前端测试存在 stub/预期错误分支日志；构建有 Browserslist 数据过旧和大 chunk 等提示，均未导致失败，也未为消除这些提示另行更新依赖或放宽检查。构建二进制和前端生成资源不纳入 Git。

未重复开展 Key 并发性能专项，未访问真实 OAuth 账号做付费请求。先前报告中的其他未处理问题不因本次合并而被宣称已经修复。
