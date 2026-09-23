# GPT-6 Sol / Luna 适配报告

## 范围与依据

- 基线：`main` / `origin/main` 的 `b1dd172a2`；分支 `feature/gpt-6-sol-luna-support`。
- Codex CLI 已 fast-forward 更新至 `408a77dc1a1cf95413df26b5185623cf245161d7`。没有修改客户端代码。
- 模型目录以用户提供的 `docs/model-catalog-2026-09-23.json` 为静态兜底基准；真实账号原生目录仍有优先权。只提取 Sol/Luna，不整体替换 Astra 或其他模型目录。
- API 依据：[Sol](https://developers.openai.com/api/docs/models/gpt-6-sol)、[Luna](https://developers.openai.com/api/docs/models/gpt-6-luna)、[迁移规则](https://developers.openai.com/api/docs/guides/latest-model)、[动态推理及 pro](https://developers.openai.com/api/docs/guides/reasoning)、[价格](https://developers.openai.com/api/docs/pricing)。使用 OpenAI Docs 技能区分公开 API 能力与 Codex 工作流配置。
- 按用户最终要求：Sol/Luna 的 `none` 统一转为 `low`；请求 prompt 处理沿用原逻辑。没有合并回 main 或 push。

## 逻辑变化

### 模型识别与可用性

新增两个正式 ID，覆盖后端默认模型列表、模型映射识别、管理员白名单预设及 OpenCode 配置。支持已有的供应商前缀、大小写和分隔符归一化，不推断裸 `gpt-6`、`*-pro` 或日期快照；未知 GPT-6 名称也不再掉入旧 Codex 子串兜底而变成 GPT-5 型号。

把原本 Astra 的近期账号目录检查扩展到 Sol/Luna：新鲜的实际模型列表可参与路由；目录缺失或过期仍沿用既有宽容策略。分组限制、显式账号映射、透传账号语义和 Spark 影子账号的目录补充规则不变。静态目录不是账号权限证明。

### Codex 目录

| 项目 | Sol | Luna |
| --- | --- | --- |
| 默认推理档 | medium | medium |
| 菜单档位 | low / medium / high / xhigh / max / ultra | low / medium / high / xhigh / max |
| multi_agent_reasoning_effort | null | null |
| 动态推理更新 | true | true |
| 默认服务档 | null | null |
| Codex 默认 / 最大上下文 | 272,000 / 872,000 | 同左 |
| 最低客户端版本 | 0.155.0 | 同左 |
| node_repl_auto_review_required | true | false |

CLI 内置目录当时缺少这两个模型的 `supports_reasoning_effort_updates`，且默认服务档是 priority；提供的 catalog 已更新，因此不直接复制内置目录的这两个值。能力同步、合并和序列化支持动态推理标记，保留实时目录的明确 false/null。`available_access_programs` 使用给定目录的 standard 声明，不开启新权限；原生目录已有值不被静态兜底覆盖。

OAuth 沿用 Responses Lite 声明；API Key 路径把两个模型纳入现有 Lite 禁用保护，使用标准 Responses。公开 API 的 1,050,000 上下文 / 128,000 输出上限只用于对应 API 能力和 OpenCode 配置，不覆盖 Codex 的压缩窗口。

### 请求 prompt：没有新增策略

`CodexBaseInstructionsForModel`、已有 instructions 文本、system/developer 提升逻辑、空 instructions 占位、强制模板配置、兼容请求的 prompt-cache-key / todo guard 门控均维持原逻辑。

没有统一拼接 prompt，没有新增 Sol/Luna 请求侧 prompt 兜底。新 JSON 内的 `model_messages` 仅用于模型目录，是客户端自行构造请求的原生元数据；网关请求转换不读取它。未随本次变更更新默认模型、默认 UA 或 Astra 的 prompt。

### 推理、转发与兼容入口

- 对最终映射到 GPT-6 的请求应用共用规则。按用户要求，显式 `none` 和 `minimal` 转为 `low`；未传 effort 不自动补值。
- 顶层 effort 与 `configuration_update.reasoning.effort` 使用相同规则，`max` 保持独立档位。
- Ultra 是客户端预设：Astra 仍转换为 xhigh；Sol 的 null 委派档按 CLI 解析为 max。Luna 菜单不显示 Ultra，手工传入时遵循 CLI 通用解析的 max 回退。网关不会因此插入委派参数。
- `reasoning.mode` 与 effort 独立，保留 pro/standard。OAuth HTTP 入口提前按实际映射模型判断，避免公开别名被旧 strip-mode 逻辑误处理。
- 由于 none 会提升至 low，采样和 logprobs 参数按推理模式过滤；不再保留一个实际上已不成立的无推理采样分支。
- 覆盖 HTTP Responses、OAuth passthrough、WS ctx_pool / passthrough、Chat Completions → Responses、Anthropic → Responses，并以最终请求的实际 effort 记录使用情况。
- 普通 Chat Completions 工具请求继续走现有 Responses 桥接。若官方账号被配置为直接走原生 Chat Completions，携带工具的 GPT-6 请求返回明确 400，提示使用支持 Responses 的账号；不静默降级推理、删除工具或绕过账号的协议选择。第三方兼容商的原生 CC 能力不按官方域名规则提前否定。
- `prompt_cache_options` 延续既有账号边界：官方 API Key 保留，OAuth 内部端点和其他兼容路径按原过滤策略处理。没有新增票据采集、缓存或注入。

### WS、动态推理与 compact

复用现有 thread / owner 隔离、previous_response_id、typed ID、stream_options、自动续接和裸 error 清理机制，不新建会话缓存或改连接池生命周期。

动态更新不重写顶层推理基线及历史前缀；兼容规则处理更新项内的 effort。保留 compaction_trigger 的末尾位置、压缩成功后的状态重置和失败时的继承规则。配置更新与自动压缩/自动截断冲突时沿用已存在的兼容过滤，不删除历史配置项；独立 `/responses/compact` 的官方限制也不通过改写历史来绕过。

完整 WS 回归发现并修复了继承分支的旧假设：`applyOpenAIConfigurationEffortValue` 曾无条件把 Ultra 转成 xhigh，后续 compact 因而把 Sol/Luna 降档。现在无策略时与首次请求一样保留原值，避免额外注入配置更新；有策略时按具体模型解析 Ultra，再执行原有映射、封顶和拒绝规则。

### 价格

修复 PricingService 原先仅允许 Astra 的 GPT-6 限制；更新内置价格表、PricingService 与 BillingService 的专用兜底。Sol/Luna 的两层静态兜底共享一份数据定义。

USD / 百万 token：

| 模型 | 输入 | 缓存读取 | 缓存写入 | 输出 |
| --- | ---: | ---: | ---: | ---: |
| Sol | 2 | 0.20 | 2.50 | 10 |
| Luna | 0.10 | 0.01 | 0.125 | 0.50 |

输入总量含缓存读取/写入，超过 272K 后整单输入及缓存侧 ×2、输出 ×1.5；等于阈值不加价。Fast ×2、Flex ×0.5，采用现有计费引擎避免重复叠乘。Catalog 的 1.5x speed 是速度描述，不是收费倍率。管理员自定义价格和显式零缓存写入价优先。

GPT-6 EU 处理仅允许 Standard；扩展现有 Fast 保护到 Sol/Luna，并避免 Flex/Ultrafast 或 force-priority 规则绕过。普通管理员 block 规则仍生效。

## 验证与开销

测试日志目录：`/tmp/sub2api-sol-luna.ClPdjA`。初始后端编译设置 `MemoryMax=3G` / `MemorySwapMax=0`，全量单测使用 `-p 1 -parallel 1`、`GOMAXPROCS=2`。用户随后要求在内存充足时增加并发：没有重启即将完成的后端测试，而是让前端类型检查与后端测试执行阶段重叠，前端 Vitest 使用最多两个 worker；Go lint 提高到 `GOMAXPROCS=4`、分析并发 2。编译、类型检查及 lint 的高峰避免重叠。

| 检查 | 结果 |
| --- | --- |
| 后端全量 `go test -tags=unit ./...` | 通过；service 191.561s，handler 43.978s，WS relay 10.627s；整体 10m14.48s，退出 0 |
| 前端 `vue-tsc -b` | 通过，42.74s |
| UseKeyModal / useModelWhitelist | 2 个文件、37 个用例通过；Vitest 3.10s，含启动 4.53s |
| 本次前端文件 ESLint | 通过，3.06s |
| Go 严格 lint | golangci-lint 2.13.2：0 issues，5m23.40s，退出 0 |
| 静态目录与用户提供目录比对 | 两个模型完全一致，仅去除与 model_messages 模板重复的 base_instructions 字段 |
| 请求 prompt 选择器及既有文本比对 | 与基线一致；没有新增请求侧 prompt 注入 |

全量后端最大单进程 RSS 2,776,408 KiB，前端类型检查 1,923,644 KiB，Go lint 2,452,760 KiB。Vitest 和前端 lint 的最大单进程 RSS 分别为 184,392 / 192,576 KiB；这些不是多进程总峰值，也不是服务运行内存。所有最终验证均以对应完成日志为准。

定向测试过程中，先修正了 compact 触发项“必须位于第一个输入”的错误测试假设，随后完整续接用例暴露并促成了 Ultra 继承降档修复。Anthropic prompt 断言也按原有 developer 输入、空 instructions 语义修正，未为了测试改变生产 prompt 行为。上述用例均包含在最后通过的全量单测中。

系统默认 golangci-lint 2.9.0 由 Go 1.26 构建，不能检查本项目 Go 1.27；最终检查使用先前已准备好的 2.13.2 / Go 1.27 二进制，不修改依赖或 lint 配置。全量 unit 通过后，lint 发现新增用例引用了仅在 unit 标签下存在的辅助函数，已改为等价的局部浮点变量；生产逻辑及测试断言未改变，最终无标签 lint 编译与检查也通过。

没有数据库迁移、逐请求 Redis/数据库查询或外部探测。新增静态目录只解码一次；请求归一化复用已有路径，不额外保存请求正文或票据。此次使用模拟上游验证协议，没有调用真实模型或验证具体 OAuth 账号权限。
