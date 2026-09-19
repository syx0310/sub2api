# 上游 v0.2.7 合并与默认 Codex UA 更新报告

## 范围和基线

- 实施分支：`merge/upstream-v0.2.7`。本次不合回 main、不 push、不创建 PR、不部署。
- main 基线：`14c6d85810661e810d8668af185e189d5ef17ce2`，此前已同步到自己的 origin/main。
- 固定合并目标：[v0.2.7](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.7)，`aea725f2ea644d5592d0bbb1d63b607efa7e200a`，北京时间 2026-09-19 12:28:46 发布。
- v0.2.5 → v0.2.7：71 笔提交（38 笔非合并提交），131 个文件。合入全部目标历史，不只挑选 release 简述中的功能。
- 本次再次 fetch `/home/siyixuan/codes/codex`，按最新远端 `78245b47af` 检查工具输出、图片引用及请求元数据；没有改动客户端工作树。
- OpenAI Docs 的 [Function calling](https://developers.openai.com/api/docs/guides/function-calling) 与客户端 `protocol/src/models.rs` 均用于核对原生多模态工具结果，不能把第三方端点的字符串限制泛化到 OpenAI。

### 撤回的 v0.2.6

已核实 [v0.2.6 发布工作流](https://github.com/Wei-Shaw/sub2api/actions/runs/35331086048) 的原提交 `49a39b6dc`。它不是 v0.2.7 的祖先：两者共享 v0.2.5 后的 53 笔提交，随后 v0.2.6 有 7 笔独有提交，v0.2.7 有 18 笔独有提交。

v0.2.6 独有的 [#7315](https://github.com/Wei-Shaw/sub2api/pull/7315) 为主动采集、缓存和注入 Codex turn-state 票据，含后台任务、调度门控、设置和账号状态 UI，共 61 个文件。本次明确不恢复这些撤回提交。v0.2.7 的通用插件宿主服务不等于内置票据模块；原有 turn-state 透传与来源保护继续保留。暂缓的请求 zstd 功能也没有加入。

## 默认 UA：0.153.4 → 0.155.1

官方最新正式发布经 API 核对为 [rust-v0.155.1](https://github.com/openai/codex/releases/tag/rust-v0.155.1)，北京时间 2026-09-19 04:03:04 发布，非预发布版本；完成验证期间再次查询仍为该版本。

默认值更新为：

```text
codex-tui/0.155.1 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.155.1)
```

只修改统一版本常量及对应契约断言，首段、尾部和 version 头仍从同一版本派生。Ubuntu / x86_64 / xterm 模板不变；中英文设置输入示例一并更新。管理员系统设置的“网关转发”区域中，“自动同步 Codex 版本号”下的当前生效版本展示位置不变，没有新增 UI 功能。

现有优先级不变：管理员手填版本 → 已自动同步版本 → 内置版本。没有连接业务数据库强制覆盖已保存的配置，因此不能把本次源码默认值更新说成线上全部账号已经使用该 UA。应用自身 VERSION 单独同步为 0.2.7；上游 tag 文件仍是 0.2.5。

## 关键逻辑改变

### Anthropic 工具 schema：兼容但不静默放宽约束

采用上游“处理根级联合”的入口，但不保留其无条件摊平算法：

- `allOf` 的同名属性保持嵌套 `allOf`，不能改成 `anyOf`；必填字段保持合取。
- `anyOf` / `oneOf` 仅在各分支其他约束一致、最多一个属性不同的可证明情形下下移到该属性。`oneOf` 继续保持排他性，且区别属性必须为必填，避免缺省属性绕过排他验证。
- 单分支联合可以与根约束合并；现有嵌套联合保持原样。
- 跨字段关联、条件必填、非对象分支、不兼容的封闭对象、需要迁移引用位置等不可安全转换情况，明确返回兼容错误，而非丢弃约束后发往上游。
- 递归深度和联合节点数量受限。原生 OpenAI Responses 不经过这个转换。

因此，上游若干仅验证“移除了根级联合”的用例已改为验证明确拒绝有损转换；不是为了绿测而删除断言。新增同属性交集、重叠 oneOf、超大整数区分、封闭对象与复杂度边界回归。它是保守可转换子集，不宣称完整支持任意 JSON Schema。

### DeepSeek 图片工具结果：限定目标、保留内容和指令边界

- 原有多个 CN 平台共享的无状态处理继续存在，但新增的图片工具转换只作用于 DeepSeek 原生 Responses，不再误套 Kimi、MiniMax、OpenCode；OpenAI 请求不变。
- 只解释正式的工具输出内容数组。普通字符串即便包含 JSON 或 data URL，也仍作为工具数据，不擅自解释成图片。
- 连续并行工具结果先完整保留，再附带带 call_id 归属的图片消息；图片 detail 和其他字段保留，原始请求对象不被修改，避免重试使用已改写的数据。
- 图片提取不再先序列化整个大图再反序列化。整个 HTTP 请求的读入、解码和最终编码仍有分配，不能称为整个转发路径零拷贝。
- 无法解析的文件图片引用、图片混合音频/加密/未知内容，或需要把工具结果跨越 system/developer 指令重排的情况，明确拒绝；错误不回显加密载荷。不擅自移动中途指令。
- HTTP 普通构造与 passthrough 构造均返回客户端 400，而不是把同一坏请求当上游故障重复重试。

兼容边界：DeepSeek 本身无法无损表示上述混合内容/指令交错时，需使用支持原生多模态工具输出的目标；没有宣称所有客户端输入都能转换成功。

### 分组用量汇总：保留索引优化，同时保持一致快照

上游将水位从 CTE 移到前置查询，使尾段时间下界作为 SQL 参数参与规划。本次保留这项优化，并补齐两次查询的一致性：普通 DB/连接入口使用短生命周期的只读 REPEATABLE READ 事务，避免水位读取后遇历史删除、回填或汇总更新而混读新旧数据。显式事务调用者继续负责自身隔离级别。

这增加事务开始/结束的数据库往返，但不对写入行加排他锁；请求取消会回滚并释放事务。水位无效、时区变化或水位在未来时仍可回退全量重算，不宣称所有场景都不扫描历史。

### 插件宿主服务：采用上游主体并补充边界

- 接入 Redis 命名空间 KV、账号目录、出站身份解析、只读运行状态及 UI 桥；旧插件不实现新可选初始化 RPC 时仍可运行。
- 保留能力声明限制及插件身份由宿主绑定的命名空间隔离。
- 补齐直接解析账号 ID 的状态校验：已禁用/错误账号不能绕过目录过滤获取凭据；API Key、其他平台、影子及 setup-token 账号不进入该目录能力。
- KV 与账号目录 RPC 使用 3 秒 context 预算，令牌身份解析使用 10 秒预算以允许 OAuth 刷新；更短调用方 deadline 不被延长。Redis SCAN 每轮显式检查取消。
- 上游现有单值 256 KiB、列表最多 1000 项、非零 TTL 上限 90 天的规则保留。TTL=0 仍为持久保存；没有新增总存储配额。SCAN MATCH 仍可能遍历整个 Redis DB，超时预算不是索引，也不是绝对精确的网络耗时上限。
- 状态接口依然要求管理员鉴权，仅免二次验证；不启动临时运行时、不应用配置、不主动测试上游。插件测试成功提示由插件 UI 决定，失败提示保留。

## 其余采用上游的更新与行为边界

1. Seedance/Ark 原生异步视频创建、查询和删除，通过显式 Seedance 能力的 OpenAI API Key + 自定义地址账号路由。任务绑定原账号及调用方，查询成功任务时按实际 completion tokens 去重结算；不使用 Grok 按秒价格，不对结果不明确的创建请求额外重放。
2. 客户端取消后，HTTP response-account / owner 亲和关系使用独立但共享上限的 3 秒写入预算保存。不是放宽 WS store=false continuation 的连接亲和。
3. 严格 Chat 目标限定执行 developer → system，不把中途指令降为 user，不重排/合并对话；本地 system/developer/instructions 策略不变。
4. DeepSeek Chat 缺失 reasoning_content 时补兼容占位，已有明文不覆盖；不能理解为恢复了加密推理历史。
5. Gemini 目录合并实际可调度 Antigravity 映射，保留原生模型元数据，并遵守混合调度开关与模型白名单。
6. Antigravity 裸 Gemini 名称按 thinking 配置选已配置的变体，显式映射优先。缺省为 high，缺档位时可能回退到更高档；它不完全等同于“仅消除 404”。
7. Antigravity 对识别出的 Go/Python GenAI SDK 停止发送 SSE 注释心跳；只清理该适配路径的 Claude 归因元数据，不修改原生 Anthropic 通路。
8. Coding Plan 配额耗尽 403 使用可恢复冷却，不永久禁用。采用最早未来窗口重置点而非推断准确耗尽窗口；周额度耗尽时可能提前再试。持久化冷却失败时继续备用处理。
9. 暂停调度但仍 active 的 OAuth 账号继续刷新 token，不解除管理员暂停。
10. 模型 manifest 减少重复解析，保留精确 models 键校验；不替换本地 Pro + Spark 目录合并、Astra 窗口/Ultra 规则。
11. 兑换历史增加分页：不传参数继续旧数组响应；有分页参数时返回分页信封，单页最多 100 条，按 used_at + id 稳定排序。
12. 限额表单拒绝负数/非有限数，0 与未配置仍区分；支付配置并发等待、金额输入恢复、退款余额按本次金额判断、订单状态筛选回第一页。
13. 代理批量测试复用防重、公告保留部分已读成功、订阅清空复位 loading；TOTP 输入同步和规范错误、对话框 ID、Tab、数字分页、注册优惠码、复制备用错误及移动端模型广场入口修复。
14. gRPC 升至 v1.83.2 及相关依赖更新，对应原 main 安全扫描报告的 GO-2026-6443 / GO-2026-6348；不修改扫描或 lint 规则来规避检查。

## 本地功能与既有问题

保留 Astra ctx_pool、compact 分类、逐帧嵌套元数据、stream_options、子代理身份隔离、request_kind 分道、typed ID / previous_response replay、裸 error 有界收尾和 drain 修复。本次不新增原生 ctx_pool steer 支持。

默认 device 开启、session/更深 ID 关闭及显式选项不变。Docker 法务文档构建、Key 并发填充/精确批量/稀疏聚合接口、计费定制、原始请求字节统计和大整数精度保留。不重复 Key 并发专项性能压测。

唯一文本冲突为 ChannelMonitorView.grok.spec.ts 的数量断言，保留 PROVIDERS.length，不退回硬编码 10。没有新增数据库迁移。

9 月 16 日独立复核提到的图片缓存价覆盖、Images SSE 错误后等待，以及更早的 WS 设备/预热等遗留项不在此次补丁范围，不能视为随 v0.2.7 自动修复。

## 验证记录

日志目录：`/tmp/sub2api-v027-merge.Fh7xyz`。重任务串行，独立 scope 使用 MemoryMax=3G、MemorySwapMax=0；Go 包并行度 1，普通测试运行并行度 2，GOMEMLIMIT=1024MiB，GOGC=50。race 单独采用 GOMAXPROCS=1、运行并行度 1、GOMEMLIMIT=768MiB、GOGC=30。工具链通过 GOTOOLCHAIN=auto 使用仓库要求的 Go 1.27.0。

| 检查 | 结果 |
| --- | --- |
| schema 包初次回归 | 通过，apicompat-schema.log |
| 首轮协议/UA/契约定向回归 | 通过，targeted.log；峰值单进程 RSS 2,769,708 KiB，scope 未发生 OOM |
| 最终全量 unit | 通过，unit.log；service 188.803s、repository 21.959s，命令退出 0 |
| 最终全量 integration | 通过，integration.log；临时 PostgreSQL/Redis 实际运行，新增并发历史删除快照用例通过，测试容器已清理 |
| 严格 lint | golangci-lint 2.13.2 通过，0 issues，lint.log，未放宽规则 |
| govulncheck | v1.8.0 全项目扫描通过，govulncheck.log，退出 0；原 main 的两项 gRPC 可达漏洞不再被报告。仍有 11 项模块级公告，但扫描未发现当前代码调用相关漏洞路径，不等于所有依赖完全没有公告 |
| 相关 WS / 插件 / 转发 race | service、handler、openai_ws_v2 三个包的定向回归全部通过，race.log，退出 0；最大单进程 RSS 3,087,032 KiB |
| 前端类型检查 | vue-tsc -b 通过，frontend-typecheck.log，退出 0 |
| 前端全量测试 | 300 个文件、2255 个用例全部通过（含 i18n 检查），frontend-tests.log；单 worker，未跳过失败用例 |
| 前端生产构建 | Vite 构建通过，frontend-build.log，退出 0；27.78s，最大单进程 RSS 1,545,292 KiB |
| 后端 embed 构建 | Go embed 构建通过，backend-embed-build.log，退出 0；最大单进程 RSS 1,547,912 KiB。产物 -version 返回 0.2.7，未启动业务服务 |

未调用真实模型账号做计费请求；本地 mock、真实本地 WS 和临时数据库测试不等于官方上游全链路实测。未修改现有业务数据库。

unit 的最大单进程 RSS 为 2,753,724 KiB；scope 峰值约 3 GiB，memory.events 的 max=1382、oom=0、oom_kill=0，swap=0。编译确实触及限额并发生内存回收，不能称为完全没有内存压力，也不能把编译器 RSS 当作运行服务的内存指标。

前端类型检查和构建使用 Node 2 GiB 堆上限；测试使用 1.5 GiB 堆上限、单 fork worker、不并行测试文件。测试最大单进程 RSS 958,864 KiB，所观测 scope 峰值约 1.35 GiB。Vite 构建仅在本次调用的内存配置里排除重复的 checker 进程，独立 vue-tsc 和完整测试已先行通过，仓库 Vite 配置及 CI 检查规则没有修改。

前端单测包含组件 stub 警告及预期错误分支的日志，但没有失败断言或跳过用例；构建提示 Browserslist 数据过旧及部分 chunk 超过 500 kB，均为警告。本次没有为消除这些提示另行更新依赖、调整分包或放宽检查。

最终检查无未解决冲突，git diff --check 通过，main 与 origin/main 仍停在上述基线。报告和实现一同提交到实施分支；生成的前端资源与测试构建产物不纳入 Git，临时产物位于上述日志目录。
