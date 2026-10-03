# 账号编辑 credits 锁定与大 OAuth 目录缓存修复

在 `fix/codex-ws-semantics-credit-policy` 分支上处理两条复查意见，不合并 main、不 push。

## 1. 普通账号编辑覆盖耗尽锁定

根因是两条写入链路的保护不一致：`UpdateExtra` 已经以数据库中的 `BlockedAt` 做最大值保护，但管理服务先读账号，再进入 `UpdateWithAccountBillingSettings → updateLockedAccount → SetExtra`，后者仍可能整体写回旧 Extra。

本次变化：

- 在原有 `SELECT … FOR NO KEY UPDATE` 中，同时读取数据库当前的 `codex_credit_policy_state`、`codex_credits_snapshot`、`codex_credit_usage_resume_at` 和 `codex_allow_credit_usage`。
- 保存前从锁定行回填这四项。普通改名、未提供许可字段的 Extra 更新，以及通用 `AccountRepository.Update`，都不能用旧值覆盖这些字段，也不能伪造运行态状态或恢复标记。
- 整份策略状态保留，包括余额证据、时间、额度摘要、BlockedAt 以及未来新增字段；并非只保留一个数字。
- 管理服务通过独立的 `*bool` 参数传递“本次请求是否显式修改消费许可”，不从旧 Account 上反推意图。明确的 off→on 在锁内根据最新数据库状态重新计算恢复标记；true→true 不视为重新授权，显式关闭仍生效。
- 现有 `UpdateExtra` 的单调 BlockedAt 保护保留，因此普通编辑和被动/手动观测写入两个方向都受到保护。提交后的调度 outbox 和账号快照同步沿用原链路。

逻辑结果：管理员只是改名称时，即使余额刷新发生在读取和保存之间，持久化锁定也保留；新实例/重启后的准入不依赖原实例内存。显式关闭再开启仍能重新授权，不自动启用消费、不触发余额查询或充值操作。

开销：复用原有行锁和事务，没有新增数据库查询轮次；只增加四个字段的读取和小规模 JSON 合并。网关请求热路径不增加操作。

## 2. 大 OAuth catalog 的冗余正文

根因是 OAuth 返回内容不经 API Key 兼容转换，却也保存了 `Body` 和相同内容的 `upstreamSourceBody`。缓存以两者总长度执行 1 MiB 上限，导致正文超过 512 KiB 即被拒绝入缓存。

本次变化：

- OAuth 只保留 `Body`，不建立无用途的 `upstreamSourceBody` 副本；传给客户端的正文逐字节保持一致。
- API Key 目录继续保存用于客户端补全的原始正文，转换/元数据补全行为不变。
- 原有 `Body + upstreamSourceBody ≤ 1 MiB` 和最多 512 项限制不变。OAuth 的有效正文缓存范围恢复为不超过 1 MiB；更大目录仍可按读取上限转发，但不缓存。
- 调用者仍获得正文副本，避免分组映射或调用者修改污染共享缓存；上游 ETag、客户端条件请求和 304 续期不变，分组 TTL 配置继续生效。

开销：每个 OAuth 缓存条目少一份正文，读取缓存时也少克隆这一份副本。原来 600 KiB 目录每次顺序请求都拉取，现在在新鲜期内只需拉取一次。逻辑容量限制不等于整个进程的实际堆内存硬上限，读取缓冲、返回副本及在途请求仍有开销。

OpenAI Docs 用于核对模型目录按账号提供选择信息的用途；本次未修改目录内容、客户端目录选择规则或 upstream API 参数。[官方 Models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)。

## 验证

日志目录：`/tmp/sub2api-credit-catalog-review.tpseaz`。

- PostgreSQL 回归经真实管理服务和真实仓储，在读旧账号与保存之间由另一仓储提交“零余额→正余额”观测；覆盖仅改名、带旧 Extra 和不变许可的完整表单。验证数据库、同步缓存和新网关实例都保留锁定，之后显式 off→on 可恢复。
- 通用 Update 回归验证不会覆盖并发关闭的许可、最新恢复时间、锁定以及未知扩展状态。
- 单元回归验证锁内显式许可意图、true→true 不恢复、off→on 以当前锁定行计算、不会修改传入快照。
- OAuth 目录回归覆盖 600 KiB、恰好 1 MiB、超出 1 MiB、调用者副本隔离、自定义 TTL、上游 304 续期和客户端条件请求；API Key 回归覆盖原始正文保留及合计容量限制。
- 后端全量 `go test -tags=unit -p=2 -parallel=4 ./...`：58 个包通过，22,270 条测试通过事件（含子用例），17 条原有环境条件跳过，零失败。记录：`full-unit.jsonl`。
- 临时 PostgreSQL / Redis 的账号仓储完整套件和相关交错写入回归均通过，共 85 条通过记录（包含套件/子用例）；测试容器已回收。记录：`integration2.log`。首轮发现的 SQL 缺失字段投影为 null 的问题已修复，首轮日志不作为通过结论。
- 定向 `-race -p=1 -parallel=2`：service、repository 均通过，覆盖新锁内合并、显式许可传递、大目录正文隔离/304 续期及原有锁内合并。记录：`race.log`。
- 全量 golangci-lint（单并发、Go 构建 `-p=1`）：`0 issues`；`git diff --check` 通过。记录：`lint.log`。

验证使用 Go 1.27.0、GOMAXPROCS=2，普通测试最多并行构建 2 个包、race 单包构建；重型任务串行，普通测试组内存上限 3,500 MiB、race 上限 3,900 MiB。

本轮没有前端修改、数据库迁移、真实 OpenAI 账号请求、充值/重置操作，也没有改默认 UA `0.160.0` 或前次分组缓存配置。
