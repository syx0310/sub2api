# Codex / Astra ctx_pool 客户端语义修复报告

日期：2026-09-14。修复分支：`fix/astra-ctx-pool-client-semantics`。

核心实现提交：`36e511a7c`（含协议、记账、缓存和回归用例）。设置 API 契约快照修正提交：`894db94e9`。

## 范围与依据

以 `506c36f53f427a1420978de644acc280aadc3f11` 的 main 为基线，参考同目录的 `codex-astra-ctx-pool-static-analysis-2026-09-14.md`，包括后来补充的内嵌 JSON 28 个固定字段和 flatten 扩展字段分析。

本次已对 `/home/siyixuan/codes/codex` 执行 `git pull --ff-only --no-tags origin main`，结果已是最新；对照版本为 `5b1d6560181680f95cde95c14ed042acc02248ed`。没有修改客户端仓库。

实现结合客户端源码和 OpenAI Docs 对 Responses 的说明；官方资料用于确认配置更新、请求基线和连接状态边界，不把公开 API 文档当作 ChatGPT OAuth 账号已实际联调成功的证据。[Reasoning 指南](https://developers.openai.com/api/docs/guides/reasoning)、[WebSocket 指南](https://developers.openai.com/api/docs/guides/websocket-mode)。

本次处理 Codex 串行 `response.create`、预热、增量输入、动态强度、Responses Lite、native compact v2 及对应记账。没有把 Realtime、重叠 create、多 lane 或生成中原生 steer 加入 ctx_pool。显式 passthrough 原有的 steer relay 继续保留。

## 主要逻辑变化

| 项目 | 修改前 | 修改后 |
| --- | --- | --- |
| Astra 路由 | Astra 会覆盖账号模式，强制进入 passthrough | 不再按模型强制分流；遵循 ctx_pool / passthrough / http_bridge / off 配置和原有桥接条件 |
| 内嵌回合元数据 | ctx_pool 每轮用最初握手头覆盖正文 JSON 字符串 | 当前 create 的正文快照优先；只允许首帧在字段缺省时兼容补充握手值 |
| 动态推理强度 | 主要读取顶层 effort；可能忽略当前或父响应中的 configuration_update | 读取有序控制项，并按 response ID 继承已接受的配置状态 |
| 强度策略与统计 | 顶层 low 可能掩盖实际 xhigh | 对当前有效强度做映射、上限和 deny；分别保存请求强度与出站有效强度 |
| 压缩后的强度 | 没有统一的 WS 压缩状态规则 | 必须完成有效 native v2 压缩才退役旧 override；失败不提前重置 |
| stream_options | OAuth 兼容处理删除整个对象 | 保留客户端 Responses 选项，不隐式启用摘要；只移除 Chat 专用 include_usage |
| compact 记账 | WS 请求没有逐轮传入 NativeCompactionV2 | 首帧、续帧、重建请求和 cyber 旁路均使用当前轮次不可变快照 |
| 预热统计 | 与普通生成共用成功 / TTFT 调度样本 | 保留准入和真实用量，剔除预热 TTFT 及普通生成成功调度样本 |
| 模型归一化顺序 | 首轮、续轮及两种模式的映射与兼容处理顺序不同 | 先按客户端模型检查强度策略，再映射实际上游模型，再执行兼容处理 |

### 1. 内嵌 JSON 是完整快照，不是补丁

`client_metadata["x-codex-turn-metadata"]` 继续保持 JSON 字符串形态，不改成对象，也不和握手或上一轮做深度合并。

- 首帧和后续帧已有该字段时，以正文为准；显式 null、空字符串和空对象字符串均不会触发旧值回填。
- 首帧缺省该字段，且 client_metadata 可作为对象使用时，允许一次握手兼容回填。后续帧不再回填，避免重新引入客户端主动移除的字段。
- 保留工具目录、workspaces、compaction、执行环境、父子关系、flatten 自定义键和显式 false 等内容。
- 同级 flat turn/session/window、tracing、turn-state、Lite 标记仍是同级字段；不会被误当成内嵌 JSON 的一部分。
- 保留原有账号身份映射范围；只修正输入来源，不重写身份策略。映射使用 UseNumber，避免大整数 ordinal / timestamp 经 float64 失真。
- 原始线程身份仍在连接入口冻结。更新回合元数据不重新注册 owner，不把 parent 与 subagent 合并为同一所有者。

相关实现：`openai_responses_request_semantics.go`、`openai_codex_account_identity.go`、`openai_ws_forwarder_payload.go` 及两个 WS 执行器。

### 2. configuration_update 与缓存基线分开处理

例如顶层 `reasoning.effort=low`，最后有效 `configuration_update` 要求 xhigh，分组上限为 high：

```text
顶层缓存基线：low → 仍为 low
当前控制项：xhigh → 按策略改为 high
requested_reasoning_effort：xhigh
reasoning_effort：high
后续只有 previous_response_id + 增量输入：继承 high，不退回 low
```

只检查实际 input 控制项，用户文本或工具输出中出现 configuration_update 字样不生效。按历史顺序选择有效更新；显式压缩历史建立新的基线。HTTP 共用强度提取与策略函数也同步理解当前正文里的控制项。

两种 WS 模式共用状态摘要，作用域包含租户 / API Key、连接入口冻结的原始线程、账号及 response ID。它不是完整历史存储，不能用来替客户端重建会话。

继承状态存在但本轮模型相关策略要求不同强度时，仅在能够安全插入配置控制项的情况下调整新增输入，不改已发送历史、不删除 response 锚点，且避免相邻 configuration_update、保持 compaction_trigger 最后的位置。无法安全表达则用可重试关闭要求客户端重发完整请求。

策略来源仍是原有连接级强度策略快照；本次没有另加分组配置热更新机制。

如果只收到 previous_response_id 而缓存中没有其状态：启用强度限制 / 映射时不猜测并放行，返回可重试关闭；没有这类策略时保持请求透传，但强度统计留空，不伪造顶层 low 为实际强度。这包括进程重启、跨实例且状态未命中、TTL 到期等情况。真正的策略 deny 仍为策略拒绝，而非可重试状态缺失。

### 3. “识别压缩请求”和“确认压缩成功”分开

识别当前请求时，要求 input 中有真实 compaction_trigger，且不是 `generate=false`。WS 不要求存在 HTTP 风格的 `stream=true`。

仅有历史 compaction / compaction_summary / context_compaction，或者仅有元数据 `request_kind=compaction`，都不会生成 NativeCompactionV2 记账标记。request_kind 可作为诊断用途；不能单凭客户端声明更改计费或准入。

网关结构化日志 `openai.websocket_request_semantics` 提供 `turn`、`request_kind`、`prewarm` 和 `native_compaction_v2`。turn / prewarm / compaction / memory 的用途识别可以从这里观察；除已有 compact 标记外，不新增用途数据库字段或 UI 分类。

确认成功则进一步对齐客户端 `compact_remote_v2.rs::collect_compaction_output`：本轮需要恰好一个有效的 `response.output_item.done` compaction 输出项，并且收到 `response.completed`。缺失输出、重复压缩输出、失败或未完成，都不提前退役旧强度 override。单独的 response.completed 不足以证明客户端会接受压缩结果。

记账沿用现有 NativeCompactionV2 字段，不新增数据库字段，不改定价。普通 → compact → 普通的三轮标记为 false → true → false。cyber 记录在启动异步任务前复制当前轮次标量，避免读到 gin.Context 中上一轮的状态。

管理员“用量日志”的请求类型单元格旁已有 compact 标记和压缩筛选，本次是让 WS 正确填充现有字段；用户用量页的现有压缩筛选继续使用同一字段。没有新增设置页、按钮或独立 compact 接口；native v2 仍走 Responses WS。

### 4. stream_options 按客户端选择转发

保留 `reasoning_summary_delivery=sequential_cutoff`，保留其他 Responses 扩展字段和显式 false，例如 include_obfuscation=false；字段缺省时不注入，也不代替客户端打开 concurrent_reasoning_summaries 或 reasoning.summary。

兼容规则只去掉 Chat Completions 专用 include_usage；对象因此变空时才删除对象。HTTP OAuth 的 map / raw 路径以及两种 WS 模式采用同样规则。保留未知扩展不代表代理保证所有上游版本都接受它，参数校验仍由相应上游负责。

### 5. 预热与连接生命周期

`generate=false` 单独识别为 prewarm，并优先于 compact 触发项判断。保留原始 generate、输入、response ID、previous_response_id 和合法空增量；不把预热响应丢弃或强制新建连接。

预热仍执行认证、分组限制、并发准入、安全策略和既有用量规则。上游如果返回真实 usage，仍按原规则记录；本次没有增加“预热免费”分支。仅不把预热当作普通生成的 TTFT / 成功调度样本。

没有重写连接池或放宽增量重放保护。保留同线程替换、父子线程隔离、独占租约、客户端断连后的既有有界收尾、脏连接丢弃及 previous_response_id 原连接依赖。显式 passthrough 的原生 steer / 自动续轮保留；Codex 当前的 turn/steer 仍按客户端实现，由后续 create 携带输入。

## 性能和内存边界

- 请求识别、控制项查找为当前正文线性扫描，不新增数据库查询，不扫描 Redis Key，也不复制全部历史到状态缓存。
- 强度状态共享缓存最多 4,096 项，复用 response sticky TTL；未配置时为 1 小时。满时淘汰一项，过期命中会删除。
- 每条活动连接额外保留一个已接受父响应摘要，避免其他连接填满共享缓存导致当前串行会话立即丢失父状态；同样受 TTL 约束。
- key 使用摘要，effort 字段限制长度；缓存和活动状态显式复制短字符串，避免 gjson 子串间接保留大型请求缓冲区。输出计数饱和在 2，不累计压缩内容。
- 本机新增处理阶段微基准：100 KiB 正文、末尾 xhigh 控制项、high 上限，约 **2.04 ms / 次、908,065 B / 次分配、75 次分配**（565 次采样，GOMAXPROCS=2、GOGC=50）。这是完整 prepare 阶段的瞬时分配，不是缓存常驻占用，也不是整条代理请求耗时。增量续帧扫描当前增量；大正文的时间和分配仍随当前正文大小线性增长。
- 未重做管理员 Key 并发查询、Redis ZCOUNT pipeline 或聚合接口压测；这些不在本次修复范围。
- 本地验证初期并行编译导致内存压力，收到提醒后已停止全部本次重任务。后续使用单任务、`GOMAXPROCS=2`、`-p 1`、`-parallel 2`、`GOGC=50` 和 `GOMEMLIMIT=1024MiB` 运行 Go 验证；该环境变量是软堆限制，不声称是整个进程组的硬上限。
- 后续重型验证使用独立的 systemd 用户 scope，设置 `MemoryMax=3G`、`MemorySwapMax=0`，将测试进程组与其他程序隔离；若超过上限则该验证失败退出，不通过增加宿主机内存压力强行完成。

## 验证记录

被手动停止的并行批次不计作通过。已通过：

- 后端全量：`GOTOOLCHAIN=auto GOMAXPROCS=2 GOGC=50 GOMEMLIMIT=1024MiB go test -p 1 -parallel 2 -tags=unit ./...`；service 包 180.040 秒，整条命令退出码 0，单进程峰值 RSS 约 2.49 GiB。
- 新增 prepare 微基准：`go test -tags=unit ./internal/service -run '^$' -bench '^BenchmarkWSReasoningPrepare100KiB$' -benchmem -benchtime=1s`，在 3 GiB / 不使用 swap 的独立 scope 内通过。
- 相关竞态回归：`go test -p 1 -parallel 1 -race -tags=unit ./internal/service ./internal/handler -run 'WSReasoning|ClientSemantics|WSNativeCompaction|WSTurnSnapshot|CodexTurnMetadata|ResponsesStreamOptions|ParentSubagents|WSIngressReplacement|WSIngressEffectiveAstra|ClientDisconnectStillDrains|GPT6AstraSteering|CodexFingerprintHandshakeBodyParity' -count=1`；service 5.546 秒、handler 1.926 秒，退出码 0，无数据竞争报告。在独立 3 GiB scope 下运行，swap 为 0、OOM / OOM kill 为 0。
- 全量静态检查：`go vet -p 1 -tags=unit ./...`，退出码 0。
- 标准 `pnpm build`：i18n 校验和前置 `vue-tsc -b` 已通过；Vite 内的 vite-plugin-checker 又并行启动一次 `vue-tsc --noEmit`，进程组触及 3 GiB 硬上限，被 scope 以 `oom-kill` 结果停止。因此不把这条标准命令记为通过，也没有扩大宿主机内存配额。
- 分步前端验证通过：使用 Vite 的 `loadConfigFromFile` 加载原配置，仅在本次调用的内存对象中移除重复检查插件，再通过 `build({...config, configFile:false, plugins})` 生成资源。其余生产配置不变，仓库配置和脚本均未修改；打包 27.53 秒，进程峰值 RSS 约 1.46 GiB。保留既有 Browserslist 数据过旧、大 chunk 提示，没有为此次后端修复更新前端依赖。
- 后端嵌入前端资源的构建：`go build -p 1 -tags embed -o <临时目录>/sub2api ./cmd/server`，57.38 秒，退出码 0，峰值 RSS 约 1.47 GiB。只生成本地验证二进制，没有启动服务。
- `git diff --check` 通过。

分步前端打包可复现命令如下。本次先前的 `vue-tsc -b` 已完成且退出成功；重新验证时，先分别运行 `pnpm run check:i18n` 和 `pnpm exec vue-tsc -b`，成功后再运行以下打包命令。需要从 frontend 目录执行，资源输出目录仍由原 Vite 配置决定：

```sh
systemd-run --user --scope --expand-environment=no \
  -p MemoryMax=3G -p MemorySwapMax=0 \
  env NODE_OPTIONS=--max-old-space-size=2048 \
  node --input-type=module -e '
    import { build, loadConfigFromFile } from "vite";
    const loaded = await loadConfigFromFile({ command: "build", mode: "production" });
    if (!loaded) throw new Error("Vite config not found");
    const plugins = (loaded.config.plugins ?? []).flat(Infinity)
      .filter(plugin => plugin && plugin.name !== "vite-plugin-checker");
    await build({ ...loaded.config, configFile: false, plugins });
  '
```

本次最终回归覆盖：

- 两种 WS 模式的真实本地 WebSocket 对端；普通模式和 Responses Lite 均比较连续 7 轮完整出站 JSON。
- 预热、空增量、客户端模型省略 / 别名映射、动态强度、继承、native compact、后续普通 / memory 请求。
- 内嵌 28 字段及扩展、工具目录、false / null / 缺省、uint64 大整数、同级 tracing / turn-state。
- 映射 / 上限 / deny、未知父响应、跨线程 / 跨账号隔离、缓存并发 / 容量 / TTL、压缩失败和无效输出。
- handler 实际 usage 落库的首轮 compact、普通 / compact / 普通以及 cyber compact 路径；重建请求的快照回归。
- 原有 parent / subagent / guardian 同时在途、同线程抢占、客户端断连收尾和显式 passthrough steer 回归。

前端全量测试已经通过：270 个测试文件、1,995 条用例。另补齐 main 原有 settings API 契约测试中遗漏的 `openai_codex_effective_client_version=0.153.4` 两处预期，未改设置 API 的实际行为。

原有 HTTP 转 WS 的指纹测试曾把握手视为正文的权威来源；本次同步修改其预期，验证各自来源只映射一次，正文不会退回握手快照。

验证边界：没有真实 OpenAI 账号 / 计费模型联调，没有部署、Docker build / push、主分支合并或 GitHub push。局部协议等价性由真实 WS 编解码的本地模拟对端验证，不等于已覆盖所有上游服务版本或其他客户端的全部 Responses 扩展。

## 保留的本地行为

默认 UA `codex-tui/0.153.4 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.153.4)` 及生效版本 UI 不变；device 默认开启、session / 深层身份默认不启用的策略不变；system / developer / instructions 定制策略、typed ID / replay 防护、Pro + Spark 模型列表合并、Astra 模型目录 / 定价、成本与并发查询逻辑、Docker 法务文档构建均未改动。

所有实现保留在修复分支。main 和 origin/main 仍指向本次起点，不会在本任务中自行合并或推送。

## 后续补修：裸 error 的执行收尾

实现提交：`008a21f72`，继续保留在 `fix/astra-ctx-pool-client-semantics` 分支；未合并 main、未 push。

问题：ctx_pool 收到未命中特殊重试的普通 `error` 后仍等待响应级终端事件；上游保持打开且静默时，可继续占用连接与用户 / 账号并发槽，直到 900 秒读取超时或其他取消条件出现。该缺口属于旧执行器；Astra 回到 ctx_pool 后同样暴露。客户端已经结束响应流或尝试重连，并不保证旧执行能立即释放，尤其新连接可能先被并发为 1 的旧槽位挡住。

本次补修行为：

- 采用两种模式共用的固定 **500 ms** 错误补帧窗口。普通错误仍立即下发；窗口内允许同一响应的 `response.failed` 补齐权威 usage，重复 error、辅助事件不延长截止时间。
- 若 failed / usage 超过该窗口才到达，不再等待或另行追账，本轮以截止前观察到的用量为准；这是及时释放失败执行与补采迟到用量之间的明确边界。
- 无补帧时以 `error` 结束当前执行；有匹配 failed 时以 `response.failed` 结束。错误之后的矛盾 completed、下一次 created 或不匹配响应不会覆盖本轮结果，不建立成功锚点或压缩成功基线。
- 上游错误连接直接废弃，既不回池也不等待正常关闭握手；用量 / AfterTurn / 并发槽只结算一次。passthrough 在收尾前不再准入同连接的新请求。
- 补齐 `normalizeOpenAIWSTerminalEvent("error")`，使裸错误不再因终端类型为空而误计为调度成功。没有全局把 error 简单加入正常终端列表。
- 字段兼容重试、首输出前限流切换、previous_response_not_found 的专门处理保留；错误已下发后不自动重放，更不会去掉 previous_response_id 后重发增量。
- passthrough 的错误窗口只在专门限流处理判定之后启用；续轮首输出前限流仍发送原有 1013 重连关闭，不能被普通错误收尾改成 EOF。
- 既有 invalid_encrypted_content lineage 回归改为验证“错误后新建连接、同一原始线程保留密文黑名单”，不再要求错误连接继续复用。独立的 response.failed 没有裸 error 前缀时维持原有行为；response.steer.failed 也不被当作此处裸错误。

开销：只在遇到裸 error 后建立固定截止时间及短状态，不引入数据库查询、Redis 查询或额外常驻 goroutine；正常推理的 900 秒超时不变。继续串行测试，独立 scope 限制 3 GiB 内存、禁用验证进程 swap。

验证重点：真实本地 WebSocket 的上游发错后保持打开、下游不主动关闭；检查用户 / 账号槽位及时释放，后续并发为 1 的请求可进入且使用新连接；检查有无 response ID、持续辅助事件、error/failed 用量归并、矛盾 completed、正常重试与显式 steer 的回归。

目标回归已通过：service 3.701 秒；真实 handler 的 2 种模式 × 5 种错误场景通过（4.106 秒），两次逻辑请求各释放一次用户 / 账号槽位，恰好两次上游请求、两条不同连接、两条 usage。仅有裸 error 时保留其 3/1 token 用量；匹配 failed 到达时以 11/4 token 为准，不相加重复计费；矛盾 completed 的 99/99 token 不计入。首次测试中的账号槽计数失败来自测试 fixture 未向选号器注入并发服务，补齐测试依赖后通过，未因此改变生产准入逻辑。

本次补修的最终全量后端回归通过：`go test -p 1 -parallel 2 -tags=unit ./...`，handler 42.590 秒、service 177.289 秒，命令退出码 0；包括原有 `LaterTurnPreOutputRateLimitRequestsReconnect` 的 1013 关闭码断言。以 GOMAXPROCS=2、GOGC=50、GOMEMLIMIT=1024MiB 和独立 3 GiB / 不使用 swap 的 scope 执行，单进程峰值 RSS 约 2.54 GiB。

相关竞态回归通过：`go test -p 1 -parallel 1 -race -tags=unit ./internal/service ./internal/handler ./internal/service/openai_ws_v2 -run 'BareError|InvalidEncryptedContentLineage|CyberTerminal|LaterTurnPreOutputRateLimit|ClientDisconnectStillDrains|ClientSemantics|WSReasoning|WSNativeCompaction|ParentSubagents|GPT6AstraSteering|SucceededForScheduling' -count=1`；service 8.600 秒、handler 5.734 秒、relay 1.044 秒，退出码 0，无数据竞争报告。使用 GOMAXPROCS=1 和相同 3 GiB 硬内存上限，swap 为 0；编译触发内存回收但没有 OOM / OOM kill。

全量静态检查 `go vet -p 1 -tags=unit ./...` 通过，退出码 0。后端 `go build -p 1 -tags embed -o <临时目录>/sub2api ./cmd/server` 通过，63.38 秒、退出码 0、峰值 RSS 约 1.48 GiB。`git diff --check` 通过。本次仅改后端，不重复运行此前已通过且无代码变更的前端测试 / 打包；未启动生成的二进制、未调用真实 OpenAI 账号。
