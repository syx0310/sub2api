# v0.2.19 合并与客户端语义适配报告

## 范围与来源

- 起点：main / origin/main 的 `6a22022095e82310653195efc31b7c84bc1fc53d`。
- 实施分支：`merge/upstream-v0.2.19`。本轮不合回 main、不 push、不创建 PR、不部署、不发布镜像。
- 正式 release：[v0.2.19](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.19)，`dece26153621959ccd7635814f4b2d08304e681a`，2026-10-28 10:16 UTC 发布。相对 v0.2.18 核对 71 个提交（50 个非 merge）、259 个文件；不是只按 release 摘要挑功能。
- 合并提交 `d2860f7ef`；解决 18 个冲突文件，自动合并的运行态、鉴权缓存、生成代码和用量调用链另行核对。
- tag 内 VERSION 仍为 0.2.18，单独采用上游 `a9240d501`，本地提交 `8b542350d`，版本修正为 0.2.19。
- 按批准计划附带 tag 之后三个补丁，不把它们称为正式 release 自带功能：`6b2fdfcac` → `3c9f8f50e`（OAuth 非流式返回 JSON）、`9b5407449` → `82d41e11a`（incomplete/cancelled 终态）、`4db8402b5` → `855c3520b`（缺 Content-Type 的 SSE）。没有直接合入整个 upstream/main。
- 客户端参考 fetch 后的 `origin/main=55c099a142eb302188fee00ab9e9062f2db1c359` 和稳定 tag `rust-v0.166.0`；未修改或编译客户端工作区。
- 使用 OpenAI Docs 核对 [Terra 规格与价卡](https://developers.openai.com/api/docs/models/gpt-6.1-terra)。客户端模型描述与官方 API 的默认值分开处理。

## 合入的更新

1. 受限管理员 API Key：按操作和资源授权、IP/CIDR、过期、轮换、撤销与保留审计；只存不可逆摘要。生命周期操作只允许管理员 JWT，沿用二次验证。旧全权 Key 保留兼容，但不能管理管理员 Key 本身。
2. 图片按张计费：OpenAI / Composite 分组新增默认关闭的开关；以真实返回图片数量计数；保留价格档位匹配所需 token；Gemini fileData 图片参与计数。开关开启且成功结果无法解析张数时采用上游一张后备，不用请求 n 放大计费；关闭时保持上游原有后备规则。
3. Gemini：默认列表去掉旧 2.x 和 3-pro-preview，更新 3.x 选项；连接测试使用 gemini-3.5-flash；各账号均声明映射时按分组声明汇总目录。旧模型仍可通过手工映射/白名单配置，不清理用户已有配置。
4. Gemini 重试：同账号普通重试默认 4 次、可配 0–10（总尝试为 1+重试次数）；签名兼容降级另有阶段预算。显式缓存绑定的网关池模式请求只在同账号重试，不切走缓存所属账号，也不因此封禁本地网关账号。
5. Grok：手动/后台目录共用解析；诊断显示来源和可信情况、与映射的差异；失败请求保留成功快照，CAS 避免乱序覆盖，普通账号编辑保留最新受管值；完善原始上游响应模型审计。
6. DeepSeek：Pro 不再按 9 月 14 日日期强制改用 Flash 价；读取 prompt_cache_hit_tokens；OpenCode 上只有明确 DeepSeek 目的模型才改写不被支持的 developer 角色。保留分组/渠道自定义价格及本地角色策略。
7. 国产 Coding Plan：频率型 429 在有未接近耗尽的额度证据时走短冷却；缺快照、含额度耗尽标记或窗口 >=90% 时仍保守处理，不把所有 429 都改成短重试。
8. 目录缓存 singleflight 内复查新鲜结果，防止上一轮刚结束后的重复拉取。
9. Antigravity 畸形工具调用显式报错；multipart 单 part 超过 50 MiB 拒绝而非截断；分页整数溢出、管理员保护规则错误码、渠道搜索配置维度和模型广场价格优先级修复。
10. Claude Code 指纹按 JS UTF-16 码元语义取样；golang.org/x/net 0.62.0 及相关依赖更新。Docker 法务 COPY 保持本地实现。

## 适配与逻辑改变

### Terra 去重与客户端能力

- 不重复维护上游新增的同名模型、前端映射和价卡；继续由一份 GPT-6 标准价格表生成 billing fallback，采用上游 Terra 识别函数名但保留正式 ID 的精确匹配。
- 最新客户端的完整 Terra JSON 与本地嵌入描述逐字段相同。保留 Codex low 默认、500000/872000 窗口、Lite、code_mode_only、multi_agent v2、ultra 和动态 effort 更新；不使用上游通用目录的 medium 默认替代。
- 继续显式 none/minimal→low（fork 策略，官方 API 允许 none），缺失不注入；ultra→xhigh；官方 Chat-only 工具请求不能静默丢工具，第三方端点保留原兼容边界。
- 上游 Anthropic 转 Responses 的 Terra max 保留逻辑采用；原有显式 thinking disable 优先级不变。
- 保留 12h 写缓存标志、>272K 长上下文、Fast/Flex、EU Fast 和自定义零价格；不把 Terra 继承成 Sol 的订阅长上下文豁免。
- 保留本地回归，并另存上游账号发现/实时能力覆盖测试；不采用要求 none 原样转发、别名宽泛猜测或残缺目录的冲突断言。
- 不添加统一 prompt，不改变 system/developer/instructions 的既有策略。

### 图片专用端点与混合请求

上游 Gemini/Antigravity 把 ImageEndpoint 设为 ImageCount>0；这会在 Composite 开关开启时使混合请求跳过原有 token 价格分支。已按转发入口纠正：Messages、Chat 和 Gemini generateContent 不是专用图片端点；OpenAI Images 保持 true，Grok 媒体按具体 generations/edits 操作判断，不看是否返回图片。

回归实际执行转发器并把其 ForwardResult 交给结算，覆盖流式/非流式及开关前后价格一致；专用兼容图片接口验证仍有端点标记。原有显式按次/图片价格继续生效，不把所有 Gemini 生图强制改为 token 计费。

### 账号运行态和外层事务

- 同一行锁内读取并合并本地 credits 授权、BlockedAt、恢复时间、展示快照，以及上游新增 Grok 目录和 upstream_model_metadata。
- 普通旧表单/改名不能清除耗尽锁定或用旧目录覆盖新观测；手动 off→on 仍按已锁定数据库状态重新授权，不擦除锁定历史。
- 本地 UpdateWithAccountBillingSettings 也进入上游事务绑定层，ORM、原生 SQL、查询使用同一外层事务，未提交不发布调度快照。
- 保留 UpdateExtra 的 BlockedAt 单调保护、真实调度候选摘要字段和握手后的 credits 准入。不新增主动余额轮询、自动充值或耗尽后的自动重新授权。

### 受限 Key 与本地并发接口

将 GET /api/v1/admin/api-keys/concurrency 和 POST /api/v1/admin/api-keys/concurrency/query 明确登记到全站 ops.read。只返回可用性、完整性、时间和 ID→并发数量；不连带开放用户/分组 Key 列表、凭据、批量修改并发或管理员 Key 管理。真实注册路由经过鉴权验证，缺权限不能读底层缓存。

### OAuth 非流式响应

客户端 stream=false 与 OAuth 上游必须 stream=true 分开保存。下游按客户端语义聚合 JSON 并纠正 Content-Type；incomplete/cancelled 保留其终态和用量；OAuth 缺 Content-Type 时根据 SSE framing 聚合。compact 专用桥接、失败处理、opaque 密文、搜索历史、显式工具选择及完整重放要求保持原保护。

全量回归发现新的 JSON 聚合路径漏记 SSE `response.metadata` 中的 turn-state 长度，现于聚合前复用同一遍帧解析观测长度。覆盖普通 transform / passthrough、存在 / 缺少 Content-Type 四种组合；不把 opaque 值写入最终 JSON，也不增加持久化字段或额外正文副本。

### Grok 与本地通用目录快照去重

上游已为 Grok live IDs 提供身份隔离、CAS 更新的受管目录，本地通用 ID-only 快照不再重复保存 Grok IDs，避免额外非 CAS 写入。Grok 完整能力元数据仍按原流程保存；Codex 和其他提供方的本地 ID 快照不变。上游原有“元数据注册表失败时只保存 ID 目录”的回归保持不变。

### 默认身份

rust-v0.166.0 Cargo 版本及 TUI 初始化身份均已核对：

```text
codex-tui/0.166.0 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.166.0)
```

UA 和 version 头同源；设置页中英文示例同步。手动配置 > 自动同步 > 内置默认优先级不变，不清除用户已保存设置。

### 经额外授权的前端安全升级

验证时发现的 Vue I18n 和 URI 解码漏洞均已存在于合并前，非 v0.2.19 引入。经用户明确同意“本次额外升级并验证”后执行：

- Vue I18n 9.14.5 → 11.4.14，`@intlify/message-compiler` 同版本；修复包括 [GHSA-83rm-6c8r-g728](https://github.com/advisories/GHSA-83rm-6c8r-g728) 在内的翻译 HTML 转义漏洞，不新增全局 `escapeParameter` 策略，不改变现有业务文案/富文本行为。
- `decode-uri-component` 0.4.1 → 0.5.1，仅定向覆盖该传递依赖，修复 [GHSA-qr28-fpjm-wq7f](https://github.com/advisories/GHSA-qr28-fpjm-wq7f)；不顺带全面升级 lockfile。
- 前端最低 Node 版本改为 22；CI 三条构建/安全流程改用 Node 24。Docker 原本已用 Node 24，法务文档构建不变。本地使用临时安装、校验 SHA256 的 Node 24.22.2，不改系统 Node 或用户全局配置。
- 保留 Composition API、动态加载语言包和 runtime bundle。v11 已默认 AST/JIT，移除旧的 `__INTLIFY_JIT_COMPILATION__` 宏；新增使用真实 runtime 的加载、切换、插值、复数、链接、富文本、禁止 `unsafe-eval` 和 `srcdoc` 安全回归。
- 套餐卡片 3 项旧测试原先断言编译失败时返回的翻译 key，改为断言真实译文；不修改卡片业务逻辑或布局。
- 迁移依据：[v10 变化](https://vue-i18n.intlify.dev/guide/migration/breaking10)、[v11 变化](https://vue-i18n.intlify.dev/guide/migration/breaking11)、[11.4.14 release](https://github.com/intlify/vue-i18n/releases/tag/v11.4.14)。仓库不使用受影响的旧式 `$tc` / `tc` / legacy Composition API 兼容层，不需要更改业务翻译调用。
- 生产依赖审计从 6 high 降至 2 high；余下为原有 xlsx 例外，现有例外校验通过，没有扩大例外范围。另有 12 moderate、2 low，不能宣称依赖零漏洞。

## 迁移、性能与保留项

- 迁移 247 新建 scoped_admin_api_keys，248 添加 groups.image_count_billing，默认 false；只在隔离数据库验收，不操作生产。
- 鉴权摘要版本升至 25 并包含图片开关，本地模型目录缓存配置仍投影保留。保留 OAuth 60/300 秒可配 TTL、单正文缓存、Pro/Spark 汇总与身份隔离。
- 新受限 Key 每次读取并条件更新使用记录，校验创建管理员，写审计；不描述为纯内存鉴权，不增加插件刷新频率。
- singleflight 修复减少冷启动重复外呼，不增加后台定时任务。缓存关闭仍不留正文；可配窗口仍生效。
- 保留调度快照 128 bucket / 20000 账号副本 / 16 MiB 原始编码预算；这不是 Go 堆硬上限。
- multipart 提高单 part 上限不取消整体请求限制；测试单包低并发，前端单 worker。不重做既有 Key 并发统计性能专项。
- 保留设备默认开启、session/full 默认关闭；线程/账号/API Key 隔离；ctx_pool 裸 error 收尾；多 steer 审计；逐轮 PricingAt、Key/分组快照和影子账号归属；Usage 长度与 Token/s；首页 $/min 与 TPM 同行同频。
- 不扩展 response.interrupt、Guardian 路由或 zstd 请求压缩；不宣称本次覆盖客户端全部新能力。

## 验证记录

日志目录：`/tmp/sub2api-v0219.vpaKqU`。未调用真实模型、生产账号或充值接口。

- 首批前端：9 个文件、273 项通过（frontend-targeted.log）。
- 后端全量：`GOTOOLCHAIN=auto GOMAXPROCS=2 GOMEMLIMIT=1000MiB go test -json -tags=unit -p 1 -parallel 4 ./...`，59 个测试包、25,411 项测试及子测试通过，17 项原有显式/环境条件跳过，0 失败（backend-full-verified.jsonl）。scoped admin 的数据库用例配置了隔离 PostgreSQL DSN，未因缺 DSN 跳过。
- 仓储集成：真实隔离 PostgreSQL 18.1 + Redis 8.4，完整应用迁移后 1,653 项测试及子测试通过，1 项既有 `TestGetAccountsLoadBatch` 的 TODO 跳过，0 失败（backend-integration.jsonl）。新外层事务 commit / rollback、credits 耗尽锁定与目录更新竞态均执行并通过。
- Ent 重新生成无差异；Wire 生成成功并保留受限 Key 依赖注入。Wire 首次缺少工具依赖校验和，使用 `go run -mod=mod github.com/google/wire/cmd/wire ./cmd/server` 补齐后成功，没有扩大业务 Go 依赖版本。
- 前端 i18n 专项 11 个文件、54 项通过；真实 query-string 消费链的 UTF-8 和 150 KB 畸形 URI 输入检查通过；生产 audit 例外校验通过。
- 首轮回归发现并修复 OAuth 聚合漏长度、Grok ID-only 重复写入；旧内置版本和宽泛 Terra 别名断言同步到批准的语义，API 契约测试最终通过。不删除本地保护用例来换取通过。
- 验证限流：Go `-p 1`、race `-parallel 2`、前端单 worker。类型检查首次触及人为设置的 1.4 GiB Node 堆上限，后续改为错峰重跑；两项 Go 重编译叠加时主动中断第二轮 lint，待 race 完成后串行复跑。该中断不记为 lint 通过。
- Race：4 个包、366 项测试及子测试通过，0 skip、0 failure，未报告数据竞争（backend-race.jsonl）；覆盖 WS relay、credits、目录缓存 singleflight、Grok CAS、混合图片计费和长度观测等本次相关路径。这是定向 race 验证，不宣称全部代码无竞态。
- 升级后前端全量：379 个文件、3,060 项通过（frontend-full-verified.log）。Node 24.22.2 下 `vue-tsc -b` 通过，单进程堆上限 3 GiB（frontend-typecheck-verified.log）。
- 临时 scoped-admin PostgreSQL 容器 `sub2api-v0219-pg-vpakqu` 已停止并自动移除，仅清理本次测试数据；集成测试的 PostgreSQL/Redis/Ryuk 容器也已退出，无用户容器被清理。
- 前端 ESLint 与标准 `pnpm run build` 均通过（frontend-lint-verified.log、frontend-build-verified.log）；构建保留原脚本的 i18n 校验、`vue-tsc` 和 Vite checker，Vite 生产构建 38.11 秒。仍有既有大 chunk / Browserslist 数据过旧，以及 Node 24 的依赖弃用提示，不影响退出状态。
- 最终 `golangci-lint run --concurrency=1 --timeout=12m` 通过，0 issues（backend-lint-verified.log）。
- `govulncheck ./...` 通过：0 个可达漏洞、0 个已导入包漏洞；另提示所需模块中有 7 项未被本代码调用的漏洞，不能表述为所有依赖零漏洞（backend-govulncheck.log）。
- `CGO_ENABLED=0 go build -p 1 -tags embed -trimpath` 的 release 形态构建通过，嵌入本轮标准前端构建产物；临时二进制为 `/tmp/sub2api-v0219.vpaKqU/sub2api-verified`。执行 `-version` 返回 `Sub2API 0.2.19 (commit: 42c70c311, built: unknown)`，未启动服务；未设置发布时间，因为这是本地验证产物，不是发布（backend-build-verified.log、backend-version-verified.log）。

## 交接状态

实现保留在 `merge/upstream-v0.2.19`；main / origin/main 保持 `6a2202209`，未 push。主要本地提交：

- `d2860f7ef`：正式 release 合并。
- `8b542350d`、`3c9f8f50e`、`82d41e11a`、`855c3520b`：批准附带的版本修正及三个非流式补丁。
- `9e55d5bf6`：本地计费、运行态事务、受限 Key、可观测性和缓存回归适配。
- `acdb58f92`：默认 TUI 身份更新至 0.166.0。
- `15708a4c5`：JSON 聚合长度观测与 Grok ID-only 去重。
- `42c70c311`：额外授权的 Vue I18n / URI 解码安全升级，Node 24 配套与回归。

最后报告提交不改变业务代码。未构建/推送 Docker 镜像，未触发 GitHub CI 或发布；生产数据库迁移仍由后续部署流程执行。
