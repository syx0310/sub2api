# v0.2.14 合并与 Performance 金额速率报告

## 范围与基线

- 实施分支：`merge/upstream-v0.2.14`；main / origin/main 保持 `45b9284b86b8a245f115f982d6116a588884f509`。
- 合入正式 [v0.2.14](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.14)，commit `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`。相对 v0.2.11 覆盖 47 个提交（31 个非 merge），176 个文件；包含 v0.2.12 和 v0.2.13 的全部中间修复。
- 合并提交 `286e03821`；仅 VERSION 和 UseKeyModal 的 Grok 配置注释发生文本冲突，自动合并的账号编辑、计费、模型列表与生成代码也经过复核。
- tag 内 VERSION 仍为 0.2.13，另 cherry-pick 上游纯版本同步 `3f1a2ea0a`，本地为 `3093634a9`，源码版本为 0.2.14。没有合入其他 tag 后功能。
- `e3ad0beb7` 补齐平台、迁移和真实 Redis 验证；`dd6f62873` 实现 Performance 金额速率；`86b0755f3` 修正既有 Dashboard 清理测试桩的原子计数；`20164a0b2` 将 embed 静态资源测试对齐实际 SVG。后两项不改生产代码。
- Codex 客户端本轮执行 `pull --ff-only`，已是最新 `18e28fe1b96db7e1d6b13584bec37c41f71b8b0e`；未修改客户端源码。
- 按用户补充要求，金额速率沿用 RPM/TPM 的统计窗口、缓存和刷新时机，不新增高频轮询、独立实时接口或额外缓存。
- 不合回 main，不 push，不创建 PR，不构建/发布 Docker 镜像，不接触业务数据库、真实模型账号、支付或充值。

## 上游更新及逻辑变化

### TypeSafe / Jev System One

合入独立 `/v1/systemone` 协议、API Key 账号、账号测试、分组/渠道/配额、Composite 路由、调度快照、计费及内容/提示词审计。包含后续请求 schema 校验、解析差异、端点隔离、审计覆盖和错误策略修正，以及 v0.2.13 对计费探测资格的修复。

这是专用协议，不把 Jev 当作 Chat/Responses 对话模型。普通 Composite 模型列表可按能力列出它，但 Codex catalog 排除仅支持 System One 的模型；不新增 Codex 的 Jev 对话配置。400/413/422 请求错误不因此禁用池中账号。

### 充值优惠与支付安全

- 余额充值新增赠金、折扣阶梯和 Markdown 活动说明。未配置阶梯时保持原行为；订阅订单不参与。
- `payment_orders.bonus_amount` 保存赠送部分；到账金额、实付金额分别计算并在下单时固定，后续修改配置不改变已创建订单。赠送部分不计入联盟返利基数。
- EasyPay 通知拒绝非标准参数；支付返回地址移除客户端查询参数，保留服务端生成的返回信息。非标准支付变体的通知可能被拒绝，已有上游订单查询补偿链路仍保留。
- 匿名订单核验增加按 IP 限流。
- 这是 sub2api 自身的支付订单能力，与 OpenAI OAuth credits 无关。没有增加 OpenAI 自动充值、充值后自动恢复或主动余额轮询。

### 在途请求删除 Key 后结算

此前已发生的请求可能因 Key 被删除、Key 自身额度计数更新不到行而回滚整笔结算。现在仅忽略这两个 Key 计数步骤的 `ErrAPIKeyNotFound`，继续用户余额/订阅及账号侧结算。其他数据库错误不被吞掉；请求去重机制不变。

本地逐轮 WS 预占、异步计费引用交接、Relay/Simple 边界均保留，不恢复连接级预占。

### 邮箱、安装与错误信息

- 邮箱验证码通过 Redis 原子计数限制并发尝试；重置 token 保存 SHA-256 摘要并原子单次消费。升级前的旧重置链接不能继续使用，需要重新申请。
- 全新安装不再固定使用 `admin@sub2api.local`；未指定邮箱时生成随机登录邮箱。实际创建管理员时校验邮箱和 8～72 字节密码，已有用户/管理员的部署在跳过创建后不执行该校验。
- Antigravity 返回客户端的错误保留必要状态与脱敏信息，不再透出完整账号池项目身份；原有运维诊断路径保留。
- Grok OAuth 身份头采用上游交互式 CLI 行为；API fallback 去除专用身份头。Codex 默认 UA 不受影响。

### 管理界面与依赖

- 账号优先级可直接加减/输入，连续点击合并保存。该控件只提交优先级，不把旧 Extra 整体发回；后端仍执行本地 credits 锁内保护。
- Key 列表支持按分组名称在分页前排序；无分组置后，并以 ID 稳定打破同名排序。真实 Key 并发字段及批量/稀疏接口不变。
- 采用上游 Axios 1.20.0、Vue 3.5.43 和 source-map-js 最低版本限制及 lockfile。
- xlsx 不是已修复依赖：仍使用上游有期限的审计例外。已核对本 fork 只有管理员 Usage 导出中的惰性导入与写出，没有 XLSX.read 解析不可信文件的路径。

## Codex 语义、去重与本地保留

OpenAI Docs 用于核对目录元数据与网关路由的边界：目录不能替代实际能力验证，不能为适配别名而改变不相关语义。[官方网关部署说明](https://learn.chatgpt.com/docs/enterprise/roll-out-a-gateway)。远程发现开关的细节以客户端源码复核。

- 最新客户端 main 中 `api_key_model_discovery` 已默认开启，但 release/0.160 中仍默认关闭。远程目录配置继续显式生成该开关；本地文件模式不强制添加。
- 本地已经实现的远程发现修复与上游收敛；Grok 的差异仅为注释/换行组织，采用上游写法。保留所有入口和平台的本地回归用例。
- 客户端远程目录仍为 1 MiB / 5 秒上限，检查唯一且非空的 slug；不删除目录内容来规避上限。
- 保留 OAuth 分组缓存 60 秒新鲜期、300 秒最大年龄的默认值与可配置项、策略键隔离、singleflight 和 ETag/304 行为。
- 保留 OAuth 只缓存一份正文的大目录修复；API Key 目录的原始正文与转换逻辑不变。
- 保留 credits 的被动观测、调度摘要、握手后检查、母子账号归属、耗尽锁定和显式 off→on 重新授权。
- 普通账号编辑继续在数据库行锁内合并四个 credits 字段，`UpdateExtra` 继续保持 BlockedAt 单调性。
- 保留 ctx_pool/passthrough 的客户端身份、子代理隔离、previous_response_id、typed ID、嵌套 turn metadata、compact、generate=false 预热、多 steer 审计、裸错误释放及逐轮计费边界。
- 保留 GPT-6 家族、none/minimal→low、既定 effort 和 system/developer/instructions 策略；不额外注入 prompt。
- 保留默认 TUI UA 0.160.0 及生效版本 UI、device 默认开启而 session/更深 ID 默认关闭、Docker 法务文档、插件权限隔离、Usage 四路长度观测和真实 Key 并发接口。

## Performance 金额速率

### 口径与位置

管理员首页、用户首页的 Performance / 性能指标卡片，在 TPM 后新增 `$/min`。

`actual_cost_per_minute = 最近 5 分钟 usage_logs.actual_cost 合计 / 5`。

使用已入库的实际计费金额，不用标准费用、账号成本、credits 余额差或在途预占估算。小数以浮点金额返回，不像 RPM/TPM 整数速率那样向下取整；显示沿用各自页面已有费用格式。缺少新字段的旧响应显示 `—`，有效零值正常显示零。

管理员为全站，用户按已认证用户 ID 过滤；API Key dashboard 使用其已有 Key 范围，保持共享 DTO 的金额含义一致。未暴露账号成本。

这是与 Usage 页一致的计费金额，不是现金营收或 OpenAI 实际支出。订阅、赠金、简易模式等场景不能据此推导现金扣款。流式请求/WS 轮次尚未上报的用量不估计；最终日志仍可能受异步计费、落库延迟影响。

### 刷新与性能

- 在已有 RPM/TPM 聚合 SQL 中增加 `SUM(actual_cost)`，不新增数据库往返。
- 保持原有 5 分钟窗口和 RPM/TPM 的 token、prewarm 计数规则；金额按日志中真实记录的值求和，不另造成功请求或余额扣款判定。
- 管理员 stats、snapshot、用户统计和 Key 统计一起返回新字段；沿用管理员统计/快照缓存、页面加载及手动刷新时机，不引入定时器。
- 不增加 Redis 操作、上游请求、推理热路径写入或数据库迁移。
- 沿用现有短窗口查询与索引，不扩大查询范围，只多做一次数值聚合。这是静态开销说明，不是生产数据压测结果；没有重跑已验收的 Key 并发查询性能专项。

## 数据迁移

原样保留上游 `241_add_payment_order_bonus_amount.sql`、`241_add_typesafe_platform.sql`，与本地 `241_usage_log_prewarm.sql` 共存。迁移器以完整文件名及校验和识别，不重命名或改写已发布的迁移。补充真实 PostgreSQL 检查三项分别被记录，且 bonus_amount/prewarm 列及默认值存在。

## 验证记录

日志目录：`/tmp/sub2api-v0214-merge.t6rOIj`。

| 检查 | 结果 | 日志 |
| --- | --- | --- |
| 后端完整 unit | 58 个包通过，22,505 条测试通过事件（包含子用例），17 条既有测试跳过 | `backend-unit-acceptance.jsonl` |
| PostgreSQL / Redis 仓储完整集成 | 1,538 条通过记录（包含套件/子用例），零失败；1 条既有跳过 | `repository-integration.jsonl` |
| 定向 race | service / openai_ws_v2 / repository / admin 四包通过，共 1,037 条通过记录 | `race-final.jsonl` |
| 前端完整 Vitest | 339 个文件、2,631 个用例全通过 | `frontend-tests-final.log` |
| 前端类型检查 | `vue-tsc -b` 通过 | `frontend-typecheck.log` |
| 前端 ESLint | `lint:check` 通过，未执行自动修复 | `frontend-lint.log` |
| 后端 lint | golangci-lint 2.13.2，0 issues | `golangci.log` |
| 后端安全扫描 | govulncheck 未报告可达漏洞；11 项模块级告警未被当前代码调用 | `govulncheck.log` |
| 前端生产构建 | 通过，31 秒；保留 Browserslist 数据较旧及大 chunk 提示 | `frontend-build.log` |
| 后端生产构建 | CGO_ENABLED=0、embed、trimpath 构建通过；版本输出 0.2.14 / 86b0755f3，之后仅修改测试和报告 | `backend-build.log`、`backend-version.log` |
| embed 资源服务测试 | 93 条通过记录，零失败；包含新版及 legacy 静态资源入口、指纹缓存、HTML/CSP 等 | `web-embed-tests-final.jsonl` |
| 前端 High/Critical 安全门禁 | 通过仓库现有例外规则，不等于零漏洞 | `frontend-audit.json`、`frontend-audit-check.log` |
| 发布辅助及 Linux shell 检查 | Python 10 项、Compose 安全/Gateway 环境/资源/Simple/Caddy 检查通过，发布脚本和 Apple 脚本 bash 语法通过 | 终端输出 |

补充与边界：

- 集成验证使用测试 PostgreSQL 18.1、Redis 8.4 容器，结束后已回收，不接触业务实例。继承的 `TestGetAccountsLoadBatch` 在源码中带有 CI TODO skip，本轮没有新增或扩大跳过范围。
- unit 的完整通过记录在最后两项仅测试修正之前；修正后额外以 race 覆盖 Dashboard、UsageCleanup 及本次合并相关用例，并通过 embed 完整套件。没有更改生产清理、聚合、资源服务或计费逻辑来迁就测试。
- race 首轮发现旧 `dashboardRepoStub.recomputeCalls` 被后台重算协程写入、同时由 Eventually 读取。生产文件与该测试相对 main 均未改变；将测试桩改为 atomic.Int32，断言和超时不变，复验通过。没有通过排除用例获得绿色结果。
- 前端首轮失败来自旧配额测试仍断言 5 平台。补入 TypeSafe，并验证显式 0 / 有限额度可保留后，全量复验通过。
- embed 首轮两个静态资源用例仍请求已移除的 logo.png，而现有首页、public 目录及生产构建均为 logo.svg；相关生产文件相对 main 未变。更新测试使用真实 SVG，同时逐字节验证返回正文，仍验证 Content-Type 和缓存规则，复验 93 条记录通过。
- 本轮新增金额缓存测试最初断言了聚合查询调用次数，但禁用聚合时走范围查询回退；已改为验证该回退只调用一次、第二次读共享缓存。不是生产缓存行为变更。
- 前端审计保留 2 项 xlsx high、13 项 moderate、4 项 low、0 项 critical。高危门禁通过依赖 xlsx 写出用途的有限期例外（至 2027-01-06），没有声称修复 xlsx 本身或消除所有告警。
- Apple Container 功能测试在本 Linux 环境因 BSD `stat -f` 接口不兼容失败，不能计作通过；仓库 CI 将该项放在 macOS。本轮没有为适配本机而改部署脚本。
- 显式使用 `GOTOOLCHAIN=go1.27.0`（本机 shell 的默认工具链为 1.26.5 且 GOTOOLCHAIN=local，没有降低仓库的 Go 版本要求）。普通 Go 测试 `-p=2 -parallel=4`，race `-p=1 -parallel=2`，前端 2 个 worker。编译服务包时原 3,500 MiB scope 出现反复回收，主动停止后加 `GOMEMLIMIT=1800MiB` 重跑；race scope 上限 3,900 MiB，均禁用 scope swap，没有 OOM。普通测试仅在核对内存余量后有限并行，多个重型编译、race、lint 和构建不叠加。
- 前端已经独立通过 `vue-tsc -b`；随后通过 Vite API 读取原配置，仅在本次构建运行中移除重复的 vite-plugin-checker。没有改仓库 Vite 配置、跳过类型检查或调整 chunk 警告阈值。

本轮实现与本地验证已完成。没有改 main 或推送远端，没有执行真实支付、充值/重置或模型调用。上述 Apple Container 平台限制、既有跳过及依赖告警均保留记录，不等同于“所有平台全量检查无告警”。
