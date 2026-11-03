# Codex 历史 credits 快照准入调整

## 范围

- 基于 main / origin/main `cdce48681`，实现分支 `fix/codex-historical-credit-admission`。
- 实现及回归提交：`d5628856a`。main / origin/main 未变，本轮不合回 main、不 push。
- 本次只调整“已有 credits 正余额证据因超过 5 分钟而被拒选”的行为，不修改上游 release、模型目录、默认身份、充值或重置卡流程。
- Codex 客户端参考本轮 fetch 后的 `origin/main=29eed280828d498fead76c3c5dedd9642836b571`。未切换、修改或编译客户端工作区；本地客户端与该版本的 `codex-api/src/rate_limits.rs` 无差异。

## 逻辑变化

此前：套餐 5h / 7d 任一窗口耗尽且尚未重置时，即使管理员允许使用 credits，正余额证据超过 5 分钟也会拒绝候选和 WS 后续请求。这使空闲账号无法依靠下一次真实请求刷新余额。

现在：最新已知 credits 快照为可用，且具有有效的非未来观测时间，就允许尝试真实请求，不再因快照年龄单独拒选。共用判断覆盖调度候选、100% 自动暂停阈值豁免，以及 WS 握手后和逐轮检查。

“历史快照可用”不是搜索历史中的任意一条正余额：

- 管理员仍须明确开启允许使用 credits；默认关闭。
- 最新快照为未知、零余额或无效值时仍拒绝依赖 credits，不能用更早的正余额覆盖它。
- 新上游观测明确 credits 耗尽后，原有 `BlockedAt` 锁定继续生效；后来的正余额不自动解锁，仍需管理员 off→on 并保存重新授权。
- 未携带 credits 的被动响应只更新其实际包含的套餐信息，保留原余额及原采样时间，不制造新余额证据。手动刷新成功但余额未知仍按原语义使快照失效。
- 低于 100% 的手动暂停阈值、429 冷却、账号停调、模型/分组限制等不被绕过。
- Spark 影子继续读取母账号的消费授权和余额证据，使用自己的套餐窗口。
- 不额外主动拉取 `/wham/usage`，不新增探测请求、后台轮询、自动充值、重置卡兑换或失败后自动重新授权。真实请求的失败仍按既有错误和冷却逻辑处理。

该策略以最新已知状态允许尝试，并非保证余额实际未变。服务端可能不在每次响应中提供余额；在没有新余额证据时，历史正余额不会单独因时间推移失效。

## 周额度未耗尽时是否返回 credits

可以返回，但不能保证每个账号、每次响应都有。

- 客户端 `codex-rs/codex-api/src/rate_limits.rs` 独立解析套餐窗口和 credits，不要求 `used_percent=100`。
- HTTP 响应或 WS 握手可携带 `x-codex-credits-has-credits`、`x-codex-credits-unlimited`、可选 `x-codex-credits-balance`。两个布尔标记需要可解析；余额字符串可以缺失。
- 原生 WS 可通过 `codex.rate_limits` 事件的可选 `credits` 对象提供余额，不一定出现在握手响应头。客户端 WS 回归包含 `allowed=true`、`limit_reached=false`、用量 42%、credits 正余额的形态。
- 本 fork 在 HTTP 公共发送层、HTTP 流式/非流式 SSE 解析、WS 握手及后续事件中被动观测，套餐仍有余额或消费开关关闭也会记录返回的 credits；消费开关控制准入，不关闭观测。
- [OpenAI 官方 App Server 文档](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt)将 credits 描述为服务端返回相关信息时才提供。OpenAI Docs 的这一约束用于限定本次结论：源码和模拟回归能确认兼容路径，不能证明实际账号每次响应必定包含该字段。

## 性能与验证

生产代码只移除共享余额判断中的 5 分钟有效期门槛；准入判断本身不增加 I/O、状态表、缓存容量或扫描，也不刷新旧快照的时间戳。被选中的账号正常发起真实请求，不额外发送余额探测；并发和异步观测写入限额不变。

新增/调整回归覆盖：历史被动及手动余额穿过缓存摘要进入实际调度器；历史零余额/未知/未授权/耗尽锁定仍拒绝；较新的未知快照优先；quota-only 流量不伪造余额时间；新负余额阻止下一轮；Spark 母账号授权；HTTP transform / passthrough × 流式/非流式的头部及 SSE 观测；WS 历史准入和握手新负余额收回准入。

验证日志：`/tmp/sub2api-historical-credit.YmMk5e`。

- 定向回归：`go test -json -count=1 -tags=unit -p 1 -parallel 2 ./internal/service ./internal/repository -run 'Test(CodexCredit|CacheCodexCreditsSnapshot|ForwardOpenAIWSV2_CreditAdmissionAfterHandshake|SchedulerCacheCodexCreditCandidateAdmission)'`，79 项测试及子测试通过，0 失败、0 跳过（targeted.jsonl）。
- 同范围 `go test -race`：79 项测试及子测试通过，0 失败、0 跳过，未报告数据竞争（race.jsonl）。
- 为控制内存，首轮 lint 包加载出现双编译器时主动中断，并以 `GOFLAGS=-p=1`、`GOMAXPROCS=2`、`--concurrency=1` 串行重跑；中断不记为通过。格式检查 `gofmt -l` 和 `git diff --check` 无问题。
- 相关包 `golangci-lint run --concurrency=1 --timeout=12m ./internal/service ./internal/repository` 通过，0 issues（lint-serial.log）。
- 未修改前端、数据库结构或依赖，不重跑此前整仓合并的前端/数据库专项。未调用真实模型、生产账号、充值或兑换接口。
