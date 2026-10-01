# v0.2.11 合并与客户端语义修正报告

## 范围与基线

- 实施分支：`merge/upstream-v0.2.11`。
- 合并前 main 与 origin/main：`4d15f45958efbcfeea5417efbd421ce33a6798fa`；本轮不移动 main、不 push、不建 PR。
- 上游正式 [v0.2.11](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.11)：`96f4c115c9749078f90cbf210a01d39baf3f53b6`。
- 已合入完整 release 范围：相对 v0.2.10 为 23 个提交、17 个非 merge 提交、90 个文件；21 个冲突文件已逐项处理，并复核自动合并部分。
- 合并提交：`c19da71dd`。tag 中 VERSION 仍为 0.2.10，因此另取上游版本同步 `42bc7f6cf`，本地提交为 `9d3aae170`，源码版本为 0.2.11。
- `794072c2e` 补齐出站前回调的显式 effort 回退：继承状态优先；没有继承状态时读取最终 body，不把“没有状态覆盖”误作“没有 effort”。
- `14a142112` 将旧版 Fast 提示与账号聚合后的 service_tiers 对齐。
- `75798076b` 补充真实 Redis 验证、隔离进程级堆测量，并说明原生自动续轮没有新的出站写入；这是本报告对应的代码提交。
- 不包含 release 之后的 Grok 身份头修复及 Axios 1.20.0 更新；没有把 upstream/main 整体作为 release 合入。
- 客户端基线：`/home/siyixuan/codes/codex` 已在计划阶段 pull 至 `dd90f160ed9bf91320c417335ddff848b6a23dd0`，本轮核对该版本的模型、目录发现、WS 和套餐代码。
- 本轮没有新增数据库迁移、依赖升级或 Dockerfile 变化；不发布镜像，不访问真实模型账号，不兑换真实 Claude 重置额度，不修改业务数据库和配置。

## 上游功能覆盖

1. GPT-6.1 Sol：目录、定价、模型识别和兼容入口；与本地已有实现去重，保留下面列出的语义差异。
2. Astra Ultrafast：账号目录能力、独立 6× 倍率、请求/返回服务层级的计费比较。
3. Codex 远程目录：使用密钥弹窗支持远程 URL / 本地 JSON 文件选择，超过远程大小限制时切换文件模式。
4. 余额在途预占：Redis 原子登记、续期、释放、异步计费交接、媒体与兼容入口覆盖；包括上游后续几个修正提交，不只合入第一版。
5. API Key 创建防滥用：数量限制、固定小时窗口计数；删除/切换状态不再清空创建相关计数。
6. Claude 原生限额重置兑换：管理员确认、服务端选择 grant、幂等、账号与组织级锁、结果不确定时的持久保护。
7. Claude Code-only 分组：有降级组时，Chat / Responses 兼容入口允许进入既有降级调度；没有降级组仍拒绝。
8. ChatGPT 套餐：保留 SKU 身份，更新 Pro 100/200/500、Business、Enterprise、Edu 等展示；不凭标签赋予模型权限。

界面入口：远程/本地目录切换位于 API Key 的使用配置弹窗 → Codex →「Codex 模型目录」；Claude 重置位于管理员账号列表的重置次数旁。次数查询仍然只读，只有查询确认存在可用额度后点击重置并确认，才执行兑换。

## 逻辑变化及本地保留项

### GPT-6.1 Sol 数据收敛，不改 prompt 策略

上游 `backend/internal/pkg/openai/codex_gpt61_sol.json`、上一版 fork 条目、最新客户端对应条目逐字段一致。现在复用上游文件作为唯一 6.1 Sol 数据源，从本地 Sol/Luna JSON 中移除重复条目。旧 Sol、Luna 数据逐字段保持不变，Astra 文件不变。

复用上游 descriptor 原始字段补全能力，保留客户端新增字段与嵌套 model_messages。目录初始化通过 sync.Once 解析，原始字段补全发生在目录生成，不是在每个推理请求上解析一份 prompt。

没有采用上游按 6.1 Sol 切换请求 base instructions 的新增分支；目录 model_messages 不等于网关向请求注入 instructions。已有 system/developer、nullable instructions、prompt/cache key 注入边界保持原样。

- 保留 none/minimal→low；不采用上游直接拒绝这两个值的兼容入口校验。
- 保留 Ultra→xhigh、显式 max、缺省 effort 不主动填充。客户端目录默认 low 与 API 缺省 effort 不混用。
- 保留精确正式 ID，不新增裸 gpt-6.1、推测日期或 effort 后缀型号。
- 保留三个 Chat 出站入口的共同归一化，实际 body、日志和计费 effort 继续一致。
- 官方 OpenAI 的 Chat 工具能力限制不扩大到第三方兼容账号。
- 保留 6.1 Sol 的独立缓存读取价、显式零价、EU Flex 与原有 Fast 限制。

参数与工具能力复核使用 OpenAI Docs：[GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol)。上游新用例中与上述 fork 契约相反的断言改为验证本地契约，不恢复已明确拒绝的行为。

### 远程目录与客户端版本

采用上游远程目录 UI，并保留 OpenAI HTTP/WS、其他兼容分组、本地文件模式和 Windows 路径。

核对客户端发现：最新主线 `api_key_model_discovery` 已默认开启，但 release/0.156 分支仍默认关闭。因此生成远程模式配置时，除 provider.model_catalog_url 外还显式生成 `features.api_key_model_discovery = true`；文件模式不强制开启。

客户端远程目录限制是 1 MiB、5 秒、拒绝重定向，并要求唯一且非空的 slug。采用上游按原始响应 UTF-8 字节数判断大小的逻辑；大于 1 MiB 时切换文件模式，完整保留模型内容，不删除工具说明来凑大小。

远程地址不附加 API Key，认证仍使用所选 provider 的凭据；不依赖管理员页面 JWT。分组过滤、公开别名、Pro/Spark 合并和 API Key 的 Responses Lite 限制保留。

目录预览请求的 client_version 采用上游 0.158.0；这不是转发 UA。默认 TUI UA 0.155.1 及其生效版本 UI 没有升级，也不修改用户已有客户端配置。

### Astra Ultrafast 与 service_tiers 聚合

采用上游 model-owned UltrafastMultiplier=6，独立于运营者的 FastMultiplier。模型仍是 gpt-6-astra，服务层级为 ultrafast；不是新模型，也不是 reasoning Ultra。

官方资料：[Ultrafast 使用方式](https://developers.openai.com/api/docs/guides/ultrafast-mode)、[价格](https://developers.openai.com/api/docs/pricing)。价格覆盖输入、输出和缓存，长上下文继续使用原有开关与倍率组合。

修正上游直接把 service_tiers 放进通用 RawMessage 相等比较的方式：不同账号的标签、顺序或 tier 集合不同，不再生成 null。现在按 tier ID 取共同能力，无共同能力返回 []；显式 null 按“不授予能力”处理为合法空数组，不据此补出权限。其他真正 nullable 的能力字段维持原有 false/null 优先级。

客户端的模型选择器还会读取旧 additional_speed_tiers 中的 fast 标记，而请求生成只认 service_tiers。聚合结果移除 priority 时，现在同步清理该旧标记，避免 UI 仍显示 Fast、实际请求却被客户端降为普通档；不添加客户端未声明的能力。

官方 API Key 的 Astra 可以补齐已知 Ultrafast 能力，但明确 EU 账号不由默认值补出该能力。OAuth 必须由真实账号目录声明，不凭 Pro/ProMax 标签推断。不默认强制 Ultrafast，也不把它并入普通 Fast 的优惠逻辑。

### WS 余额预占从连接级改为轮次级

没有采用上游“按首帧估算一次、直到整个 WS 关闭才释放”的接线。该做法会让空闲连接持续占额度，并让后续换模型/档位仍沿用首轮金额。

保留上游 Redis 与引用计数主体，增加最终出站前的 BeforeForward 回调，覆盖 ctx_pool、HTTP bridge、passthrough 和已经支持的原生自动 steering continuation。回调取得最终模型、策略处理后的 body、继承/配置更新后的有效 effort；借用 payload 只在回调内读取，不保存完整请求。

原生自动 steering continuation 没有新的出站请求：在现有 BeforeAutomaticTurn 边界观察到上游新轮次时登记，使用审计视图和继承模型/effort。此时上游可能已经开始执行，不能宣称该路径能在计算开始前完成拒绝；没有为预占伪造 response.create 或重放历史。

- 每个实际执行轮次单独预占；真正 generate=false 的预热跳过预占，元数据中的 prewarm 声称不作为跳过依据。
- session.update 等控制帧不登记新的执行额度；compact 按实际执行处理，不因 request_kind 元数据免除预占。
- 同一轮透明传输重试复用预占，不能重复扣预估额度；跨账号尝试结束时清理该次未交接句柄。
- AfterTurn 先将引用交给实际计费任务，再结束本轮 handler 引用；所有相关任务结束后释放，空闲连接不再续期占额。
- 拒绝、断连、取消、未进入 AfterTurn 的异常也有连接收尾清理；丢弃任务和 panic 的引用归还保持幂等。
- 风控错误原有的独立异步计费也接管预占引用，避免只修正常计费 worker 而遗漏错误路径。
- 读取余额时重新检查余额资格阈值，避免长连接沿用已耗尽的首次余额；不重复计入 RPM。
- Simple、Relay 与订阅条件按原有业务语义跳过。Relay 是本 fork 需要额外保留的“不检查余额”边界。

没有修改显式 previous_response_id、子代理/thread/window 隔离、隐式锚点边界或裸 error 的 bounded drain。没有新增整段会话 replay 缓存，也不因预占强制断开空闲连接。

### 预估费用与真实计费

token 估算复用统一计费入口，纳入可获得的服务层级、最终有效 effort、渠道/分组倍率和长上下文双开关。已选定账号的 WS 使用真实映射目标作为无价别名回退，不再扫描其他候选账号；未知目标的普通准入保持上游的快照回退。

区分“显式零定价”和“无法定价”，前者不因 fail_closed_on_unpriced 被错误拒绝。预占不是扣费，最终费用仍由真实 usage 计算。

这仍是 best-effort 估算，不是绝不透支保证：输入按 body 字节数/4，默认估算输入上限 200,000、输出缺省 8,192、输出上限 128,000；增量请求不代表完整计费上下文，也不为追求精确而补造历史。首个在途请求仍在余额资格通过后允许执行；无法定价或 Redis 故障默认 fail-open。异步扣减失败回退等异常边界也不宣称强一致。

默认 200K 估算上限低于 GPT-6 的 272K 长上下文阶梯，因此预占金额仍可能低于最终账单；开关与倍率的回归在提高估算上限的条件下验证。没有以增加无限历史缓存来消除这种近似。

## 默认行为与性能边界

- API Key 默认每用户 200 个未删除 Key、每固定小时窗口 60 次创建。已有 Key 不会被清理或失效；删除不返还次数；配置两个值为 0 可关闭。
- 数量检查为先查再创建，不是事务性库存硬锁；小时窗口计数在数据库创建前发生，后续落库失败也可能消耗次数。
- 余额预占默认开启，低余额用户并发请求可能更早被拒绝；可通过 billing.inflight_reservation.enabled 关闭。没有替用户修改实际配置。
- 预占一般增加一次 Redis Lua 登记、一次释放及定价/余额缓存读取；默认 TTL 900 秒，长请求约每 300 秒续期。
- Lua 只遍历当前用户在途项，成本随该用户在途数量增长，不扫描全站 Redis key。
- 活跃请求有一个小型引用计数句柄/续期任务；WS 管理器只保存一个活跃轮次，不建立随历史轮次增长的 map。
- 计费 worker 在释放前同步更新余额缓存；失败仍按上游机制回退。估算中无价别名读取调度快照，不新增按任意模型名增长的缓存。
- 不重新进行此前已验收的 Key 并发查询性能专项；上述为新增路径的静态成本说明，不能等同于实测延迟 SLA。

## 保留的本地定制

Docker 法务文档构建、TUI 默认身份和 UA 可观测位置、device 默认开启而 session/深层 ID 默认关闭、system/developer/instructions 策略、typed ID、Pro/Spark 模型目录合并、Astra ctx_pool、嵌套 turn metadata、请求类型识别、WS 错误释放、插件权限/信息隔离、Key 真实并发及批量/稀疏聚合接口、Usage 四路 Turn State 长度默认隐藏列均保留。

没有恢复原始 turn state 采集/注入，也没有加入此前暂缓的 zstd 请求压缩。

## 验证记录

日志目录：`/tmp/sub2api-v0211-merge.i4BrzM`。

| 检查 | 最终结果 | 日志文件（位于上面的目录） |
| --- | --- | --- |
| 后端完整 unit | 57 包通过，service 内 1 个既有用例失败，详见下文 | `unit-acceptance.log` |
| 合并相关定向 race / apicompat 全量 race | 均通过 | `race.log`、`race-apicompat.log` |
| repository 集成测试 | 通过；CI=true，真实临时 PostgreSQL / Redis | `integration-recheck.log` |
| 前端全量测试 | 337 个文件、2,592 个用例通过 | `frontend-full.log` |
| 前端 vue-tsc / ESLint | 通过；未执行 lint --fix | `frontend-typecheck.log`、`frontend-lint.log` |
| golangci-lint 2.13.2 | 0 issues；未调整 lint 规则 | `golangci.log` |
| 前端生产构建 | 通过 | `frontend-build.log` |
| 后端生产构建 | CGO_ENABLED=0、embed、trimpath 构建通过；版本为 0.2.11，代码提交 75798076b | `backend-build.log`、`backend-version.log` |
| 发布辅助 | Python 10 个用例通过，release-images.sh bash 语法检查通过 | `release-tests.log` |

另已逐字段比较上游、旧 fork 与客户端的 6.1 Sol 原始目录数据，并确认旧 Sol/Luna 条目保持不变。

竞态检查通过：service / handler / repository / openai_ws_v2 / admin 的定向 race，以及 apicompat 全量 race。范围包含预占登记与计费引用交接、任务丢弃/panic、WS 预热/控制帧/续轮/模型与档位变化、裸错误收尾、Turn State 长度、长上下文、GPT-6 家族和目录聚合、Claude 重置幂等，以及 Key 创建限制；没有 race detector 报告。

**后端全量单测尚非全绿**：最后一次 `unit-acceptance.log` 中 57 个包通过，service 包仅有 `TestTokenRefreshService_LateSuccessPastAttemptDeadlineIsRejected` 失败。该模块及用例相对 main 没有任何改动；本轮没有为了获得绿色结果跳过测试、放宽断言或擅改 Token 刷新逻辑。合并相关用例（包括隔离后的内存断言）在该轮没有失败，但这不能替代完整门禁通过。

验证过程中处理的事项：

- 新增出站回调在没有 reasoning 继承状态时漏读显式 effort，已由 `794072c2e` 修正，并通过自动 continuation 用例的重复运行。
- 既有 `TestTokenRefreshService_LateSuccessPastAttemptDeadlineIsRejected` 在两轮全量中失败，中间曾有一次完整 58 包通过，单独重复 20 次也通过。失败表现是 mock 忽略取消、延迟 30 ms 返回，而 10 ms attempt 的超时没有被识别。静态检查发现该路径以 `attemptCtx.Err()` 判断超时，没有独立核对 deadline；定时取消尚未被调度时可能接受过期返回。这是尚未修复的既有边界，不归因于本轮新增预占代码；后续修复需同时维护“凭据已持久化不能再误判失败并重试”的现有语义。
- 上游新增 `TestInflightEstimate_AccountMappingNoDBAndBoundedMemory` 的全进程 HeapAlloc 差值在一轮全量中达到 9,327,056 字节，超过 8 MiB；独立重复 10 次均通过。已将这一测量隔离到当前测试二进制的子进程，并用 KeepAlive 保证被测 service 的缓存不会提前被回收。仍运行 20,000 个随机模型、仍断言小于 8 MiB，没有加大阈值或修改生产缓存。
- 补充真实 Redis 的原子登记、续期不复活、幂等释放、固定窗口计数验证；测试键前缀 hook 补齐 HLEN / HEXISTS，避免断言误读没有隔离前缀的键。全部使用临时测试资源，不接触业务 Redis / 数据库。

前端构建沿用仓库 Vite 配置；类型检查已独立通过，因此本次运行时过滤重复的 vite-plugin-checker 实例，没有修改仓库配置。构建保留 Browserslist 数据较旧与部分 chunk 超过 500 kB 的提示，未借机更新依赖或重新分包。

验证保持低并发：Go 单测 `-p=2 -parallel=4`，race `-p=1 -parallel=2`，Vitest 两个 worker；重量级作业串行执行，通过 systemd scope 限制内存，scope 不使用 swap。本轮未进行真实模型端到端计费或真实重置兑换，也未执行 Docker build/push。
