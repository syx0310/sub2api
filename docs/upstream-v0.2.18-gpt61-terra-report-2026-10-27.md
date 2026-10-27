# v0.2.18 合并与 GPT-6.1 Terra 适配报告

## 范围和来源

- 工作分支：`merge/upstream-v0.2.18-gpt61-terra`；起点为 main / origin/main 的 `947b43b97a45d043bd9338a1f3cf4638a7efc061`。
- 上游目标：[v0.2.18](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.18)，`ac1b8f1271be0d674fa6f074403c1410f30a3a81`。相对 v0.2.17 核对了 45 个提交（含 merge）、114 个文件，不只读取 release 摘要。
- 合并提交 `263fdeb94`；版本修正提交 `770e910f4`（上游 `5c185c5c5`，只修改 VERSION），源码版本为 0.2.18。合并测试接口协调提交 `44e2e6d31`。
- Terra 实现提交 `389f129ae`；SSE 等待提示补全提交 `1524d8696`。
- 不夹带 release 之后的 scoped admin keys、多图片按张计费等功能；没有合并整个 upstream/main。
- 客户端已于前一轮核对时快进同步到 `c587d6e54cb1e4013bf0cf9ea769edde48a23a1e`。本次直接读取该版本的 models-manager/models.json、Responses 请求构造及 reasoning_effort resolver；不修改、编译或安装客户端。
- 使用 OpenAI Docs 核对指定的 Terra，而不是把 Astra/Sol 的能力和价格全部复制给它。来源：[Terra 规格](https://developers.openai.com/api/docs/models/gpt-6.1-terra)、[GPT-6 兼容和区域限制](https://developers.openai.com/api/docs/guides/latest-model)。
- 本轮仅本地实现和验证，不合回 main、不 push、不创建 PR、不发布 Docker 镜像、不部署、不调用真实模型或充值接口。

## 合入的上游更新

1. Anthropic Chat/Responses 兼容流：客户端断开后继续受读间隔限制地收集用量；上游异常结束时输出协议错误，而不是伪造成功；只有未输出内容时允许 failover。
2. Gemini：中途断连按已收集用量计费；缓存命中且输入被拦截时不再产生负输入量/费用；工具 const/注解 schema 兼容。
3. Vertex：总额、日、周额度限制及调度过滤、通知、进度展示。
4. 调度：解码快照进程内复用、按平台修订号失效、模型映射签名优化、限流状态 copy-on-write、非 Antigravity 路径不重复探测单账号分组。
5. 配额累加按已配置周期选择 SQL，减少事务持锁开销。
6. Cloudflare 1010 不误判为账号失效；DeepSeek 图片不再保留重复 URL 字段。
7. 异步生图下载检查公网地址、每次重定向和图片内容类型。
8. Grok 4.7 Fast/Build Fast 价卡修正、账号管理筛选持久化。
9. Go 1.27.3、DOMPurify 3.4.17 及相关安全检查/依赖锁定更新。

升级注意：迁移 246 会开启既有 Gemini/Antigravity 分组的生图开关；不会替 composite 分组开启。关闭开关的 composite 分组请求 Gemini 生图会返回 400。这里只运行测试环境迁移，不操作业务分组。

## 合并中的本地策略协调

### 身份不被静默替换

初次合并保留 `codex-tui` 默认和内置版本 `0.162.1`；随后按用户要求将默认版本更新为 `0.164.0`，详见末尾补充。Ubuntu/终端后缀和 TUI 尾部标识保留。面板版本、自动同步版本、内置版本的优先级以及 UI 生效版本展示不变。

上游会把显式配置的 TUI 身份也强制改成 CLI。本 fork 不调用这一改写，继续保留管理员选择的客户端家族；版本仍按既有策略更新。上游空输出过载恢复修复与身份改写分离采用。

### 用量收尾复用上游实现

采用上游成功/部分错误共用的 `submitForwardUsage`，补回本地 request/response body bytes；保留 PricingAt、QuotaPlatform、请求推理档位、SessionID、渠道用量字段和本轮 API Key，不维护两套重复结算闭包。

Chat 兼容流继续遵循调用方的 include_usage；更新上游测试调用签名，不用删除本地参数的方式解决冲突。

### 重试等待不短于上游要求

- 接受 HTTP Retry-After/Retry-After-Ms，以及流内 error.headers 的大小写不敏感字段（含数组表示）。
- 秒、毫秒、HTTP 日期均保留；拒绝非有限浮点，极大正值饱和处理，不发生 duration 溢出。
- 8 秒是本地单次自动等待预算，不是可以缩短上游等待的许可。超过预算时停止自动重放/换号，未提交 HTTP 响应时把 503 和安全的 Retry-After 交给客户端。
- SSE 心跳已提交 HTTP 200 时，不能补发 HTTP 状态/响应头；改在 response.failed.error.headers 写入经过同一安全规则校验的 retry-after。最新 Codex 读取这一字段，未透传 Authorization 或其他上游头。
- 只有无有效语义输出的请求可进入恢复；已经产生文本、工具输出或 opaque 内容时，不因此重放。
- 此变化仅针对本次新增的请求级 capacity-shed 重试；不借机重写所有既有 429 策略。

### 调度缓存收益与内存界限

上游缓存最多复用 10 秒，读取 ready/active/修订号后决定是否重用解码对象；每次仍读取候选账号 last_used 并复制账号顶层结构，不宣称总成本变为 O(1)。嵌套 map/切片只读共享，运行态改写必须 copy-on-write。

额外限制整个实例的快照缓存：最多 128 个 bucket、20,000 个账号副本、16 MiB 原始编码数据。达到限制按加载时间淘汰；单个超限分组仍可查询，只跳过进程内复用。16 MiB 是编码数据预算，不是实际 Go 堆/RSS 上限。没有增加后台轮询和新 Redis 查询。

保留 credits 授权/阻塞/恢复/快照字段的摘要投影与真实候选准入链路；账号更新修订号、共享限流对象和缓存上限均列入回归。不重复运行已确认无问题的 Key 并发统计性能专项。

## Terra 实际适配和逻辑变化

### 模型识别与目录

- 精确支持 `gpt-6.1-terra` 及既有可确定的大小写、分隔符、provider 前缀规范化；不把 `gpt-6-terra`、`gpt-6.1`、虚构后缀猜成 Terra。
- 加入后端内置模型、已观测模型补全、前端模型白名单/映射预设及 OpenCode 配置示例；不改原默认模型。
- 独立保存客户端 Terra 完整描述：默认 low、500000/872000 窗口、text/image、original 图像细节、code_mode_only、Lite、多代理 v2、动态 reasoning 更新及 Fast。未知字段及完整 model_messages 保留。
- 账号实时目录明确的 false/null/窗口仍优先；保留 Pro/Spark 合并、映射和白名单。API Key 路径强制完整 Responses 的本地规则继续生效，OAuth 才采用其 Lite wire。
- 官方 API 的 1.05M 上限不用于覆盖 Codex 默认窗口。模型目录内的 instructions 描述不等于代理向请求注入 prompt；请求端原有 system/developer/instructions 策略未改变。

### 推理、工具与传输

- 沿用批准的本地 GPT-6 策略：显式 none/minimal 转 low，缺失不补。官方 Terra 允许 none，该转换是 fork 选择，不是官方强制要求。
- ultra 依照客户端 multi_agent_reasoning_effort 转 xhigh，不套用原 Sol/Luna 的 max 后备；不由代理创建子代理或注入多代理提示。顶层和 configuration_update 共用规范化。
- Lite 的 additional_tools、namespace、ID、密文、工具选择和压缩 trigger 保持既有保护；不因模型支持并行工具而反向强制打开 Lite 的 parallel_tool_calls。
- 兼容普通 HTTP/SSE、原生 WS ctx_pool/passthrough、现有 HTTP→WS 内部桥接以及 Chat/Messages 转换。官方 Chat-only 路由带工具明确拒绝，不丢工具或暗改端点。
- API 官方端点识别补全 us.api.openai.com/eu.api.openai.com，避免区域端点误走第三方工具/缓存选项语义。仍严格匹配 hostname，不接受相似恶意域名。
- Terra 允许 EU Fast，不继承 Astra/原 Sol 的限制；顺带修正同一条件对 Luna EU Fast 的旧限制。没有开启默认 Fast，也不为 Terra 广告 Ultrafast。
- 保留 stream_options、output.encrypted_content、body 内嵌 turn metadata、compaction 密文和完整历史保护；OAuth HTTP + WS 增量锚点仍要求完整重放，不假装 HTTP 能续接同一 WS 连接。

### 定价与缓存写入

Terra 独立默认价格（USD/百万 token）：输入 1、输出 5、缓存读 0.05、30m 缓存写 1.25；Fast ×2，Flex ×0.5；总输入严格大于 272000 后，整次请求输入/缓存 ×2、输出 ×1.5。静态目录、动态查价及两层 fallback 同步，未知 GPT-6 的严格拒绝策略不变。

12h 写入为 30m 的两倍：仅当最终发送到官方 OpenAI API 的 Terra 请求明确保留 ttl=12h 时，携带请求级标志进入 token 结算。普通 HTTP、Chat 转换/直转、HTTP→WS、原生 WS 均从最终出站请求捕获；WS 新 create 缺失 TTL 不继承上一轮，自动后继使用当前执行的既有设置。不会反向给请求注入 ttl。

这一标志独立于 Anthropic 的 5m/1h token 桶，不修改返回的真实 token 数；长上下文/Fast/Flex 只各乘一次。渠道/分组自定义价格继续优先，显式零缓存写价仍为零。其他 GPT 模型原有缓存时长计费不在本次自动扩展。

Terra 不继承 GPT-6.1 Sol 的订阅长上下文附加费豁免。OAuth credits 仍基于上游被动观测和手动授权；不拿 API 美元价格推算 credits，不新增拉取、自动充值或耗尽后的自动重新授权。

## 保留的本地功能

Docker 法务文档；本地角色/instructions 策略；设备指纹默认开启而 session/full 合并默认关闭；账号/API Key 命名空间隔离及子线程区分；typed ID/历史重放；显式工具选择；compaction/错误恢复保护；ctx_pool 裸 error 及时释放；steer 多输入审计；逐轮计费/预占/影子账号归属；credits 耗尽锁定的数据库并发保护；60/300 秒可配置模型目录缓存、单正文缓存和身份隔离；Usage 长度观测与 Token/s；首页 $/min 与 TPM 同行同频。

## 验证记录

日志目录：`/tmp/sub2api-v0218-terra.Pjh9rM`。所有请求均使用模拟上游/隔离资源，不调用真实模型。

| 检查 | 结果 |
|---|---|
| 后端全量 unit | 24,675 通过，17 项既有跳过，零失败（backend-unit-complete.jsonl） |
| PostgreSQL / Redis 仓储集成 | 1,628 通过，1 项既有跳过，零失败；测试容器已清理（repository-integration.jsonl） |
| 前端全量 Vitest | 374 个文件、2,918 项通过，零失败（frontend-full.log） |
| 前端 ESLint / vue-tsc -b | 通过，未自动修复或跳过类型检查 |
| 前端生产资源 | 通过，28.59 秒；类型检查已独立完成，构建单次过滤重复的 checker 插件以控制内存，未修改仓库构建配置 |
| 新资源 embed 回归 | 完整 web 包 123 项通过；SSE + embed 重点补跑 72 项通过 |
| Go 生产构建 | Go 1.27.3、CGO_ENABLED=0、embed、trimpath、静态链接；-version 显示 0.2.18 / 1524d8696 |
| 后端 golangci-lint 2.14.0 | 0 issues（golangci-verified.log） |
| 后端 govulncheck | 0 可达漏洞、0 导入包漏洞；7 项依赖模块级不可达告警 |
| 前端依赖 audit | 既有例外校验通过；2 high（xlsx）、13 moderate、2 low、0 critical；未延长或扩大例外 |
| 并发安全 race | 388 次用例执行通过、零失败、零数据竞争报告；覆盖 OpenAI 工具包、调度缓存/credits、Terra 多轮 WS、客户端语义对照、限流 copy-on-write、steer 队列（race-openai.jsonl / race.jsonl / race-cow-steer.jsonl，含交叉覆盖） |

首轮全量的两项失败来自上游把默认/账号 TUI 改成 CLI 的断言，已恢复精确 TUI 预期并重跑全量通过。首次编译还发现上游测试的 include_usage 和 DeepSeek helper 签名变化，均按实际接口补齐。静态检查发现新增测试的两处类型断言未检查布尔结果，已修正并重跑，未新增 lint 豁免。

最终新增测试类型断言检查在 `608bfb0a4`，不改变生产二进制代码；该测试另行补跑 4 项通过，race 和最终 lint 都已覆盖最终版本。内置价格 JSON 无重复顶层键；Terra 描述与客户端目标条目完整逐字段一致。

Go 测试计数包含子用例。全量 unit 使用 Go 1.27.3、GOMAXPROCS=2、-p=1、-parallel=4、GOMEMLIMIT=1200–1500MiB；前端使用单 worker。重点 race 在全量 unit、前端构建及静态检查完成后进入大包编译，使用 -p=1 / -parallel=2 / GOMEMLIMIT=1200MiB；类型检查与前端资源构建分开运行。GOMEMLIMIT 是软限制，不当作进程 RSS 的硬上限。

首次 lint 在 1000MiB 软限制下长时间运行，主动中断后以 2200MiB、单并发重跑；最终检查通过。不把中断的执行计为通过。构建保留 Browserslist 过期/大 chunk 提示；没有为消除提示改动业务拆包。

## 未扩展范围

- 不新增 ctx_pool response.interrupt 支持；这是既有独立边界，不宣称支持最新客户端所有取消流程。
- 不添加 Bedrock/GCP 的新 Terra provider 集成，不猜测账号套餐或区域授权；目录中的 availability 不是 entitlement 证明。
- 不改变既有地域附加费策略，不把 OAuth 额度等同于公开 API 价格。
- 不修改生产数据，不开启 composite 生图开关，不部署或发布镜像。

## 交接状态

- 实现、回归和报告保留在 `merge/upstream-v0.2.18-gpt61-terra`；无 PR、无 push。
- main / origin/main 均保持 `947b43b97a45d043bd9338a1f3cf4638a7efc061`。
- 本地生产构建位于 `/tmp/sub2api-v0218-terra.Pjh9rM/sub2api`，只执行过 `-version`，未启动业务服务。
- 所有测试临时数据库/Redis 容器已清理；编译缓存和上述日志保留，便于复查。
- 未做真实账号可用性或真实模型推理验证；模型访问仍由上游账号授权决定。

## 后续补充：默认身份跟随最新客户端（2026-10-27）

- 客户端仓库再次 fetch 后快进至 `b7f7e7f7e85a1c63e520f27cac42b836f242d9f7`，无本地修改。GitHub latest release API 确认为稳定版 `rust-v0.164.0`（2026-10-23 发布），并读取对应 tag 的 Cargo.toml 验证版本；不使用 main 的开发占位版本 0.0.0 或 alpha。
- 对照 stable tag 及最新 main：`tui/src/lib.rs` 以 client_name=codex-tui 初始化 app-server；`request_processors/initialize_processor.rs` 和 `startup_config.rs` 将其作为 originator，并设置 `name; version` UA 后缀。login 的 DEFAULT_ORIGINATOR=codex_cli_rs 是基础回退，不应据此把实际 TUI 改成 CLI。
- 内置 UA 更新为 `codex-tui/0.164.0 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.164.0)`。中英文设置占位文本及一致性测试同步更新；User-Agent 和 version 头继续同源。
- 不修改管理员手填身份或版本，不清空已同步版本；运行时仍是面板版本 > 自动同步版本 > 内置版本。实际版本可在管理设置的 Codex 客户端版本号区域查看。
- 本补充未重新运行前一节全量验证；仅变更默认常量、UI 示例和对应测试。后端身份/版本/HTTP/WS 回归 213 项通过（含子用例），前端设置/文案 46 项通过，修改文件 ESLint 和 git diff --check 通过。日志：`/tmp/sub2api-codex-identity-0164.eJUKid`。
