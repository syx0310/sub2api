# Codex WS 语义与被动 credits 准入修复报告

## 范围与基线

- 工作分支：`fix/codex-ws-semantics-credit-policy`，基线 `c04fbab9566286160e09aba17d4c63f5c099b1e2`（已有 v0.2.11 合并内容）。
- 实现及测试提交：`95fea1961ad1d028fa333fab490dadc680627432`。报告另行提交，便于区分业务差异与验收说明。
- 本轮不合并 main、不 push、不部署。main / origin/main 保持 `4d15f45958efbcfeea5417efbd421ce33a6798fa`。
- 对照 `docs/codex-astra-ctx-pool-static-analysis-2026-09-14.md` 的后续复核，以及 2026-10-02 的非 catalog 部分。已完成的裸 error 500ms 收尾保留。
- 明确不做原生 WS 设备合并，不改 model catalog、默认 UA、模型 prompt、system/developer/instructions 策略、none→low 策略，不重做并发聚合接口性能检查。
- 未请求真实模型、真实 OAuth 配额或 credits 接口；没有购买、充值、兑换重置券、充值轮询或充值后自动重试操作。测试中的上游、余额、重置卡均为本地 mock。

## 客户端与 OAuth 证据

本地参考版本：Codex `dd90f160ed9bf91320c417335ddff848b6a23dd0`；CLIProxyAPI `6fecc6e5567912661654a4eaf9b8f5436facd1c2`。本轮只读参考，没有修改这两个仓库，也不把本轮参考描述为重新拉取。

1. Codex `codex-rs/core/src/client.rs` 的 prewarm 使用 `generate=false`，成功的 Completed 才建立可供增量请求使用的响应锚点。`session/startup_prewarm.rs` 将失败预热标为 unavailable，`session/turn.rs` 可创建新客户端会话。
2. CPA `sdk/api/handlers/openai/openai_responses_websocket_prewarm.go` 区分原生增量与非原生桥接：前者交给上游；后者本地合成响应，但必须保存预热基线并重建后续增量。只删 `generate` 或只伪造 completed 都不完整。
3. Codex `codex-api/src/rate_limits.rs` 的 WS 窗口使用绝对时间 `reset_at`，credits 的两个布尔字段必需，balance 可缺失。CPA `internal/runtime/executor/helps/codex_quota.go` 还处理 OAuth 的附加配额对象及 error 内嵌 headers；不能只照搬普通 API 配额头。
4. 公开 [WebSocket 指南](https://developers.openai.com/api/docs/guides/websocket-mode) 用于核对预热边界；[steering 指南](https://developers.openai.com/api/docs/guides/steering) 和 [事件参考](https://developers.openai.com/api/reference/cli/resources/beta/subresources/responses) 说明同父 pending 输入只需一次匹配续请求。CPA 的双向 steering 集成测试也使用“两条 accepted、一次后继”的场景。这里仅修复已有单 lane 记账队列，不宣称已验证真实 OAuth steering 的全部行为。
5. [Sign in with ChatGPT 文档](https://learn.chatgpt.com/docs/sign-in-with-chatgpt) 中的应用 credits 授权，不等于导入原生 Codex OAuth 凭据后存在一个通用的“禁止扣 credits”请求字段。本轮没有臆造这类上游参数。

采用 OpenAI Docs 技能时，以本地客户端和 CPA 源码优先，公开文档补充协议证据；公共 API 的行为没有直接当作 OAuth 扣费保证。

## 逻辑变更

| 项目 | 修改前 | 修改后 |
| --- | --- | --- |
| 原生 WS 预热 | 日志无法独立标记；按次规则仍可能收一次费用 | ctx_pool / passthrough 继续原样转发 `generate=false`；逐轮传播 `Prewarm`，不收推理请求按次费用，真实 token 用量保留 |
| 预热统计 | 预热进入业务请求数和平均耗时 | 独立持久化 `prewarm`；业务请求数、平均耗时排除预热，token/费用仍按实值求和；Usage 列表仍保留记录 |
| HTTP bridge 预热 | 删除 generate 后可能发成真实生成 | 转发前返回 `response.failed / unsupported_prewarm` 并关闭该 WS；上游 HTTP 调用为零，不合成成功锚点、不保存完整历史 |
| 推断父响应 | prepare 后补 previous_response_id，可能未继承父 override | 出站锚点确定后重算父状态；父无 override 时保留已映射 baseline，避免 high→medium→low 被应用两遍 |
| 强度策略与别名 | Ultra 可能按公开别名而非实际模型解释 | 模型相关 preset 按实际上游模型解释，策略模型匹配仍使用公开模型名；保留 Sol/Luna 与 Astra/6.1 Sol 的现有强度差异 |
| Persistent / disabled | 数值策略下提示为不认识的值 | 无数值策略时原样保留；有上限/映射时明确拒绝无法比较的非数值强度，不猜测成 low/high |
| 压缩状态 | 输出只识别 compaction | 同时识别 compaction_summary；成功且满足既有有效输出条件时退休旧 override，不因任意相似文本清空状态 |
| HTTP SSE 本地读取失败 | 发送普通 error，Codex 可能等不到正确终态 | 输出标准 response.failed 包络；保留实际已收集用量，并标记响应已提交，避免 handler 再追加失败 |
| OAuth 映射到 GPT-5.5 | WS 仍可能声明 Lite 能力，与 HTTP 不一致 | 按实际出站模型移除 Lite 请求头/正文标志，使用既有非 Lite 兼容逻辑；不为 API Key 通路强加 OAuth 规则 |
| EU 与管理员拒绝 | 地区兼容降级可能提前绕过 block | 管理员 block 优先；允许的请求再执行地区兼容调整 |
| refresh 截止时间 | 后台与按需刷新入口只看 context.Err，可能漏掉 timer 尚未调度的超时 | 两个入口都在持久化前核对实际 Deadline；已经持久化的成功仍按原有成功语义处理，不再次刷新 |
| 同父多个 steer | 一次后继只消费一条 accepted，遗留项可污染后续轮次归因 | 一次匹配后继消费同父已接受项，保留其他父响应队列；不重放用户输入、不扩展 ctx_pool steer 或多 lane |

HTTP bridge 的客户端退路由源码分析和本地模拟验证；未运行真实 Codex 连接真实上游的端到端测试。正常原生预热不被本地模拟替换。

### 数据与 UI

- 新迁移：`backend/migrations/241_usage_log_prewarm.sql`，增加 `usage_logs.prewarm BOOLEAN NOT NULL DEFAULT FALSE`。
- 所有 INSERT 路径、原生 SQL 扫描、DTO 和前端类型同步；统计 CTE 显式投影该字段。
- 管理员及用户 Usage 页复用的“请求类型”单元格显示独立“预热”标记，仍保留 WS/流式等传输类型。
- 旧日志默认 false，不从历史文本推断预热，不回填旧费用、不自动退款。生产数据库没有执行此迁移；临时测试数据库验证了迁移与各写入路径。

## credits 开关

位置：管理员「账号管理 → 编辑 OpenAI OAuth 母账号 → 配额设置区域」，开关名称为「允许套餐额度耗尽后使用已有 Codex credits」，默认关闭。与已有“自动使用重置卡”是两个独立配置，开启本开关不会开启后者。

配置键：`extra.codex_allow_credit_usage`。仅接受 OAuth 母账号上的 boolean；影子账号使用母账号的许可和 credits 证据，但使用自己的 Spark 配额。通用批量/运行态 Extra 更新不能绕过专用校验写入许可、恢复时间或内部状态。

### 被动输入

- HTTP 响应头、WS 握手头，包括已有插件转发链。
- 原生 WS、流式 SSE、HTTP bridge 中的 `codex.rate_limits` 和携带额度头的 `error`。
- 非流式调用收到的 OAuth SSE 正文，使用无正文副本的逐行遍历提取观测。
- `reset_at` 转换为已有快照的重置时间口径；Spark 识别专属名字、`x-codex-bengalfox-*` 头，以及 `additional_rate_limits` 的对象/数组形态。最多检查 8 个附加条目，未知功能窗口不混入普通或 Spark 配额。
- credits 金额保持字符串，balance 缺失不当成 0；矛盾的零/负数或无效余额不当成可用资金。不会保存原始响应正文。
- 同步更新已有额度/余额展示快照，减少页面因为看不到被动 WS 更新而发起额外查询的机会。

### 准入规则

1. 不知道套餐配额时不主动探测，沿用普通准入；这不是扣费保证。
2. 明确知道任一套餐窗口耗尽且尚未重置时，关闭开关即拒绝后续请求。
3. 开关打开时，还需最近 5 分钟内的有效可用 credits 或 unlimited 证据；未知、过期、不可用均不允许依赖 credits 继续。
4. 明确发现 credits 不可用后保留阻塞标记，后续正余额不会自动解锁超额请求。管理员须取得有效观测，并显式关闭再开启开关授权恢复。
5. 自然套餐窗口重置、或新观测显示套餐仍有额度，可以继续普通额度内请求；这不是 credits 充值恢复。
6. 更低的手动暂停阈值、账号健康、并发、余额等原有约束不被此开关绕过。WS 后续 turn 重新检查；passthrough 的 steer 发出前和自动后继准入处也检查。
7. 无法取得影子母账号状态时，不制造空母账号状态覆盖已有阻塞记录；过额许可保持保守。

优先级的一个具体变化：已明确耗尽且 credits 不获许可时，新的拒绝先于本次调度里的自动重置卡通知分支。开关是独立保存的配置，但这不代表两个准入策略互不影响。已有独立重置服务/worker 没有被修改或由新开关启用；若账号原本开启了该旧功能，仍需单独管理其配置。

本轮没有新建定时 quota/credits 拉取器，没有调用充值或重置服务，没有为了余额增加安排重试、重放或自动恢复。已有其他独立功能的行为不被本开关启用。

### 性能与保证边界

- 不新增上游配额查询；仅解析已收到的头/事件。常见输出帧先判断类型，避免复制大输出正文。
- 本机最多缓存 4,096 个账号的少量额度摘要，不保存会话历史。最多 4 个后台写入者；一般持久化间隔至少 30 秒，关键耗尽变化可立即写入；单个工作者最多合并写两次、总截止 5 秒。
- 额度/许可准入复用既有调度快照。影子需要读取母账号快照；WS turn 需要重读当前账号许可。每次快照读取独立限制为 100ms，避免缓存故障时无限等候数据库降级；这是既有缓存/数据库访问，不是对 OpenAI 请求余额。影子额度事件可增加至多一次这样的读取等待。
- 持久化、调度缓存传播及多实例观测并非强一致财务账本。仍在执行中的请求、尚未得到配额信号的请求，以及上游已开始执行的自动后继，不能被事后检查撤销。不能保证开关关闭后上游绝不扣费。
- 新 SQL 字段不增加列表查询次数；业务统计在原聚合中增加 FILTER，保留分页总记录数。未重跑与本任务无关的并发聚合压测。

## 验证记录

日志目录：`/tmp/sub2api-ws-credit.UEmlOR`。

- 前端定向组件及 i18n：96 个测试通过；`vue-tsc --noEmit` 和修改文件 ESLint 通过。生产 Vite 构建通过；为控制内存，类型检查先单独执行，随后通过 Vite API 仅禁用重复运行的 checker 插件，未改仓库构建配置。
- 数据库集成：使用临时 PostgreSQL/Redis，Usage 写入/读取、统计、Dashboard 相关测试通过，测试容器已经自动回收。
- `steer-repro.log` 保留修复前同父队列只消费一条的失败证据。
- 早期完整测试暴露的新字段契约和旧能力断言已同步；失败的中间日志不作为最终验收结论。
- 全量复核还复现了 `TestRefreshIfNeeded_LateSuccessAfterDeadlineDoesNotPersist`：按需刷新存在同类截止时间竞态，随后同步修复，并新增 `Err()==nil`、Deadline 已过的确定性用例。
- 首次 lint 在 3.9GB 进程组内存上限触发隔离 OOM，后续降低为单并发；不通过放宽宿主机内存保护解决。
- 最终后端 `go test -json -tags=unit -count=1 -p=2 -parallel=4 ./...` 通过：58 个含测试包、22,215 个通过事件（含子测试）。17 个按环境条件跳过的用例包括真实 TLS/模型接口、插件运行环境及其他需外部资源的既有用例，不宣称这些已执行。记录：`final-unit.jsonl`。
- 定向 race 覆盖 credits、预热、强度、隐式锚点、两种 WS 模式、steer 和两个 refresh 截止入口，`-count=3 -p=1 -parallel=2` 通过。记录：`race-final.log`。
- golangci-lint 2.13.2 全量静态检查通过，`0 issues`（单并发、Go 构建 `-p=1`、进程组内存上限 3.9GB）。记录：`lint-pass.log`。
- `go build -p=2 -tags=embed ./cmd/server` 通过，包含本轮前端产物；二进制仅存放于日志临时目录，未运行或部署。记录：`backend-build.log`。
- 两个 refresh 入口的迟到成功、未触发 timer 回调、已持久化成功等定向回归重复 20 次通过。记录：`deadline-repeat.log`。
- `git diff --check` 通过。测试结束后没有运行中的测试容器；原生 OAuth / credits 真实上游联调未执行。

## 保留的边界

原生 WS 设备合并、完整多 lane/重叠 response.create、真实 OAuth 全链路联调、model catalog 维护均不在本轮范围。ctx_pool 仍以串行 response.create 为边界；需要原生 steer 的请求仍走已有 passthrough 路由。此次修复不会把这些边界改写成“已全部支持”。
