# v0.2.10 合并与 GPT-6.1 Sol 适配报告

## 范围与基线

- 实施分支：`merge/upstream-v0.2.10`；本地 `main` 保持 `6082f642e7261fb98d87b4293bfa38c7d4077060`。
- 完整合入上游正式 [v0.2.10](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.10)，目标 `2f3fed2fdb0787141294cec81487a5df30426f7f`。包含 v0.2.9：从 v0.2.8 起共 103 个提交、59 个非合并提交、214 个文件变更。
- 合并提交 `3be358706`；上游 tag 的 VERSION 仍为 0.2.9，因此单独 cherry-pick 版本同步 `a60a29549`，本地为 `c96041ca9`，源码版本为 0.2.10。不混入发布后的 Claude Code-only 降级路由等其他功能提交。
- 合并回归契约修正：`42b51aab0`；GPT-6.1 Sol 目录、请求与计费适配：`9948316ae`。
- WS 身份快照持有整理及并行测试初始化修正：`35cbf8e68`。
- Codex 客户端在本轮计划阶段已 pull 到 `9ef9cb1d9fc6013f6c1994346e0ee93ad9e6f986`；本次以该版本的 `codex-rs/models-manager/models.json`、请求构造、增量续接和推理档位解析为准。
- 仅本地合并、提交、测试与构建；不合回 main、不 push、不建 PR、不发布镜像，不调用真实模型账号，不修改业务数据库。

## 上游更新覆盖

1. Claude Sonnet 5.5 的目录、价格、thinking/tool 协议与兼容入口；Claude 原生重置额度状态按需查询。
2. composite 分组模型归属、OpenAI-compatible WebSocket 别名、模型映射与白名单冲突修复。
3. Antigravity 首个内容前保活、签名事件识别、异常空流 failover，以及 PDF 输入兼容。
4. 客户端取消统一 499、账号额度暂停保持到未来 reset、自动额度重置的无额度负缓存与失败退避。
5. Anthropic 用量归一化、缓存读取/写入分桶、收到 usage 时的零值识别；工具参数初始值与增量衔接、终态缺失文本补全、多个工具名的一次性无损改写。
6. 长上下文账号成本统计开关、图片价格回退、Free Fast 缺价时保留零费用记录、成功完成后才计搜索调用、视频价格展示等。
7. 风控用户白名单、Token/消费金额仪表盘切换、Claude Code-only 客户端标签、Windows Codex 目录路径和 CCSwitch 地址修复。

本范围没有新增数据库迁移或依赖版本变动；Dockerfile 不变。风控白名单只是增加可配置能力，没有替用户启用白名单或执行账号额度重置。

## 合并中的协议取舍与逻辑变化

### WS 窗口边界只限制代理的隐式续接

没有直接采用上游“窗口 ID 变化就删除 previous_response_id”的行为。最新客户端依据完整输入前缀和请求配置决定全量/增量；窗口元数据变化不足以证明请求是完整历史。

现在按原始客户端 metadata 中的 thread、window、request_kind 和执行 lane 建立上一轮作用域：

- 作用域相同：保留原有、受条件限制的工具输出续接 ID 推断。
- 窗口、子线程、请求类型等发生变化：不沿用上一作用域的隐式锚点。
- 客户端显式提供 previous_response_id：不因上述变化删除，不补造历史，也不强制重连。
- 判断使用指纹改写前的原始帧，复用嵌套 turn metadata 解析；仅保留小型身份快照，不引入完整历史缓存。
- 原有逐请求 BeforeRequest 已在解析入口调用，未再叠加上游循环内的第二次调用，避免重复审计/准入。

实际本地 WS 回归覆盖同作用域推断、新窗口/子线程/memory/compaction 不推断、显式续接保留、单次审计及连接不重建。原有子代理隔离、ctx_pool 错误收尾和并发槽释放仍保留。

### 不移除 OpenAI 分组的 Codex 目录

保留 OpenAI HTTP/WS 使用弹窗的目录获取、下载和 `model_catalog_json` 配置。最新客户端的 API Key 模型发现仍需要 provider 支持和默认关闭的功能开关，不能以自动发现为理由移除目录能力。

采用上游 Windows 修复：config.toml 中统一使用 `~/.codex/codex-models.json`；Windows 页面仍展示相应的实际保存位置。Pro/Spark 合并、分组过滤和公共别名能力保留。顺便清理了此前已有的 Sol/Luna 预设映射重复项。

### 用量收集与 Chat 输出分别处理

采用上游准确的后台 usage 收集和 Anthropic 缓存分桶，不对含义不明确的迟到缓存字段猜测扣减。后台计量不依赖客户端是否请求 usage。

但转换后的 Chat 流仅在客户端明确设置 `stream_options.include_usage=true` 且上游确实提供 usage 时输出独立用量块；明确的零值仍能输出，缺失/null 不伪造成已知值。不采用上游“无论客户端选项都输出 usage”的改动。原生透传和 Responses 自己的 stream_options 契约不被混用。

### 角色、thinking 与 beta

- 保留 fork 的 system/developer/instructions 策略，同时采用上游显式 `type: message` 的格式修复。
- 采用 `thinking.disabled` 优先于 output_config.effort 的转换；最后实际目标是已识别 GPT-6 时，继续执行 fork 的 none/minimal→low。
- 保留有效的 OpenAI beta；OAuth 普通 HTTP 只清除不适用的旧 Responses beta，不擅自启动 hosted multi-agent。
- 上游按 GPT 数字代际识别推理模型只用于兼容采样参数，不拿来猜测型号、价格或别名。

### 长上下文：账号成本与客户售价分开

采用上游“账号成本只看账号开关”的修复，分组不再决定账号是否承担上游长上下文成本。

客户售价仍保留 fork 的 **分组开关 AND 账号开关**，没有换成上游的更宽松组合。两边开关的四种组合均有回归；本次没有改业务收费配置。

## GPT-6.1 Sol 适配

### 目录与精确识别

正式 ID `gpt-6.1-sol` 已加入后端模型常量、精确别名识别、账号/分组目录补充、前端白名单与映射预设、OpenCode 配置、Codex 完整目录和定价查找。

客户端目录增量保存在 `backend/internal/service/openai_codex_gpt6_sol_luna_models.json`，来源与提交写在 `openai_codex_gpt6_models.go`。新条目与上述客户端源码逐字段一致，只省略重复的 base_instructions。已有 Sol/Luna 条目逐字段比对未变，Astra 快照也不变。

关键客户端字段：默认 effort=low、Ultra 的实际 effort=xhigh、multi_agent_version=v2、272K 默认窗口/872K 上限、动态 effort 更新、WS 优先、默认服务层级 null。原生目录的显式 false/null 保留；自定义 API Key 的 Lite 限制继续生效。

不猜测裸 `gpt-6.1`、`-pro`、`-codex` 或日期后缀。新鲜账号目录明确不含 6.1 时，不因为账号有旧 Sol 就宣称可调度。没有替换用户既有默认模型。

部署后，如果现有账号快照尚未同步到新型号，需要刷新该账号的模型目录；显式分组/账号白名单仍须允许这个型号。本次没有替用户修改业务库中的账号权限或白名单。

### 请求与客户端语义

- 显式 none/minimal→low；Ultra→xhigh；显式 max 保留。
- 缺少 effort 的 API 请求不凭客户端目录自动填 low。客户端默认 low 与官方 API 默认 medium 是两个不同层级。
- 普通请求和 configuration_update、WS 继承及 compact 后的恢复共用现有规范化，不另造一套 6.1 转发器。
- 不统一增加 prompt，不变更现有请求 instructions 策略；model_messages 是客户端目录元数据，不等于网关主动插入提示词。
- OAuth HTTP、passthrough、API Key、ctx_pool、Chat→Responses 和 Messages→Responses 的捕获出站测试包含新型号。
- EU 禁止 Fast，且不能由分组/规则强制开启；6.1 的 Flex 不被旧型号的 Standard-only 兼容分支顺带删除。旧型号既有 EU 策略未扩改。

参数与 API 限制参照 [官方 GPT-6.1 Sol 文档](https://developers.openai.com/api/docs/models/gpt-6.1-sol)。API 的 1,050,000 上下文上限没有覆盖 Codex 的 272K/872K 目录配置。

### 补齐 Chat 回退入口，避免出站参数与计费不一致

此前 Responses→Chat、Messages→Chat 缺少 GPT-6 出站归一化，但计费提取函数已将 none 解释为 low。这不是“只增加型号白名单”能解决的问题。

新增 `prepareGPT6ChatUpstreamBody`，由直接 Chat 和这两个回退入口共用：先确定最终映射模型，再归一化参数、检查官方 Chat 工具能力、处理 prompt_cache_options，最后从实际出站请求读取 effort。

- 官方 OpenAI 的 GPT-6.1 Sol Chat 工具请求明确 400，要求 Responses-capable 路由；不静默丢工具、不改 endpoint。
- 不把这个官方限制套在第三方兼容端点上。
- 已有 Astra/Sol/Luna 也补齐这两个旁路；这是本轮修复带来的额外但相关的逻辑变化。
- 新回归捕获三个入口的实际 body，覆盖四个 GPT-6 型号、none/minimal/ultra/max、流式/非流式、公开别名，并将 ForwardResult 的实际 effort 带入自定义倍率计费，验证参数、记录和费用一致。

### 独立价格

标准价每百万 tokens：输入 $2、缓存读取 $0.10、缓存写入 $2.50、输出 $10。缓存读取与旧 Sol 的 $0.20 不同，采用独立价格对象和内置价格条目。

超过 272K 输入时整次请求输入/缓存 ×2、输出 ×1.5；Fast ×2、Flex ×0.5。复用原有倍率组合和管理员覆盖规则，明确零缓存写入价不被 1.25× 回退覆盖。边界 272000/272001、内置价格/动态回退/billing 回退都纳入验证。[官方价格依据](https://developers.openai.com/api/docs/models/gpt-6.1-sol)

既有显式 Fast 规则可对新型号生效，但此次没有自行开启 Fast，也没有修改旧型号的价格。

## 保留的本地功能与边界

Docker 法务构建、UA 0.155.1 与 UI 生效版本、device 默认开启而 session/深层身份默认关闭、typed ID、Pro/Spark 目录、插件授权与信息隔离、Key 真实并发/批量/稀疏聚合、Usage 四路 Turn State 长度默认隐藏列均保留。没有恢复原始 turn state 采集/注入，没有加入暂缓的 zstd 请求压缩。

不重新开展 Key 并发性能专项。本次模型支持不等于增加 ctx_pool 的完整 steering/hosted multi-agent 协议；先前静态复核里的其他问题不因合并而被宣称已经修复。

## 验证记录

日志目录：`/tmp/sub2api-v0210-merge.4TFRJd`。

| 检查 | 结果 |
| --- | --- |
| 后端全量 unit | 58 个含测试的包通过，`unit-final.log`；service 199.303s。其后身份快照持有整理及测试初始化修正，由下述最终 race 覆盖复验 |
| repository 全量 integration | 临时 PostgreSQL/Redis 实际运行通过，35.241s，`integration.log`；CI=true，测试容器已清理 |
| 协议转换全量 race | apicompat 全量通过，`protocol-race.log` |
| 最终 WS/转发 race | service、handler、openai_ws_v2 均通过，分别 107.161s、10.623s、7.176s，`race-final.log`；覆盖最终身份快照改动、GPT-6、续接、子代理隔离、usage 和转换入口 |
| 严格 Go lint | golangci-lint 2.13.2 / Go 1.27.0，全项目 0 issues；最终代码以恢复原并发后的 `lint-original.log` 为准，初轮为 `lint.log` |
| 前端全量测试 | 337 个文件、2548 个用例通过，195.70s，`frontend-tests.log` |
| 新型号 UI 目录补充回归 | 最终 UseKeyModal 26 个用例通过，包含之后新增的 6.1 Sol HTTP/WS 目录默认值用例，`frontend-catalog-final.log` |
| 前端类型检查 | 独立 vue-tsc -b 通过，`frontend-typecheck.log` |
| 前端 ESLint | 无自动修复、未放宽规则，退出 0，`frontend-lint.log` |
| 前端生产构建 | Vite 构建通过，31.61s，`frontend-build.log`；类型检查已独立完成，构建时临时排除重复 checker，仓库构建配置未改动 |
| 后端生产构建 | Go 1.27.0、CGO_ENABLED=0、embed 构建通过；未注入 Version ldflag，二进制从源码读取 `Sub2API 0.2.10`；`backend-build.log`、`backend-version.log`。未启动业务服务 |
| 发布辅助检查 | 10 个 Python release helper 单测、发布脚本 bash 语法检查通过；未执行发布 |
| 目录与合并完整性 | 新条目与客户端 JSON 一致，旧 Sol/Luna 条目不变；价格 JSON 无重复型号键；release 是分支祖先；diff --check 通过 |

以上检查均已完成。最终保留在实施分支；main 未移动，未 push。

已经定位并处理的合并差异：上游 role 断言与 fork 角色策略不同；WS 测试夹具缺少 response.created 或漏算 session.update 控制帧；长上下文测试把客户售价按上游组合计算。修正了对应夹具/契约断言，未删除测试、未放宽拒绝转发与成本断言。

扩大 race 覆盖后发现 `gateway_forward_as_chat_completions_test.go` 的并行用例仍写 Gin 进程全局模式，与其他用例的 CreateTestContext 竞争。移除了该文件五处不必要的 SetMode，保留 t.Parallel 和全部业务断言；不是把测试初始化竞争误报为生产转发状态竞争。

最后的内存复核把 WS 原始帧身份解析前移为每帧一次：跨轮只保存克隆后的身份字符串，不为身份比较额外持有完整原始请求。续接判断规则不变。

验证使用约 5.8 GiB RAM 的宿主机。普通重型 Go scope 上限 3500 MiB、禁用 scope swap；普通测试包并行 2/用例并行 4，race 包并行 1/用例并行 2，前端两个 worker。unit/race 监测到上限回收、没有 OOM；但最后追加的 lint 复验出现了一次 OOM 终止，当时该 scope 上限为 3200 MiB，不能称为全程无 OOM。用户随后说明正在编译 Docker，并要求等待后保持原并发；因此暂停了较低并发的重试，待连续检查没有 Docker 构建/编译进程后，按原并发 2、GOMEMLIMIT=2300MiB 单独重跑 lint，scope 上限 3800 MiB，仍禁用 scope swap，最终退出 0、0 issues。宿主机原有 swap 不等于测试 scope 的 swap。

另一个独立 Docker 构建出现时，仅冻结本次 race 编译 scope，待其结束后恢复，没有终止或修改其他构建。未将外部作业的构建结果算作本次验证结果。

前端仍有既有的 Browserslist/大 chunk 提示；没有为消除提示调整依赖或放宽校验。生成资产与验证二进制不提交。未使用真实 OAuth/API Key 做付费模型请求；实际账号权限仍以同步目录和上游授权为准。
