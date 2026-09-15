# CI lint 与 WebSocket drain 收尾修复报告

## 范围

- 起点：`main` / `origin/main` 的 `65d0e6329`。
- 修复分支：`fix/ci-lint-ws-drain-race`。本任务不合并 main、不 push、不创建 PR，不修改上游仓库。
- 实现提交：`1435b7b96`（lint 清理），`77b0ddf50`（relay 收尾与确定性回归）。
- 对应 [CI run 34918323703](https://github.com/syx0310/sub2api/actions/runs/34918323703)：lint 失败，unit 成功，integration 的 WebSocket 收尾用例失败；frontend、shell 成功。
- 仅修改后端相关实现和测试。不修改 CI workflow、lint 豁免规则、前端、数据库结构或部署配置。

## 一、lint 修复

[lint job](https://github.com/syx0310/sub2api/actions/runs/34918323703/job/104220668329) 列出 8 项问题（5 项 errcheck、2 项 staticcheck、1 项 unused）；页面的第 9 条是汇总失败注释。

首次本地不限量检查在修正这些位置后，又报告了 6 项类型断言问题：推理参数测试 5 项，HTTP/2 keepalive 测试 1 项。CI 默认同类问题输出上限不能用作完整问题清单。

| 文件 | 修改 | 逻辑影响 |
| --- | --- | --- |
| `backend/internal/handler/openai_ws_bare_error_test.go` | 清理闭包显式忽略 `CloseNow` 返回错误 | 仅测试清理，允许已关闭连接再次清理 |
| `backend/internal/pkg/httputil/preread_body_stats_test.go` | 检查 `WriteString` 错误 | 测试断言完善，零拷贝逻辑不变 |
| `backend/internal/service/openai_codex_transform_test.go` | 检查类型断言、数组长度和嵌套类型 | 保留原有字段转换期望 |
| `backend/internal/service/openai_gateway_request_body_reasoning_test.go` | 同上，清除嵌套未检查断言 | 保留顶层/配置更新的推理强度期望 |
| `backend/internal/repository/http_upstream_http2_keepalive_test.go` | 用 `http.NewResponseController(w).Flush()` 并检查错误 | 仍立即发送 SSE headers；HTTP/2 keepalive 配置不变 |
| `backend/internal/repository/concurrency_cache.go` | 用 `ZRangeArgs` 的 `ByScore` 选项替代弃用的 `ZRangeByScore` | 保留同一索引、`-inf` 到当前时间的闭区间、升序、零偏移和清理批量上限 |
| `backend/internal/service/openai_gateway_request_body.go` | Astra + EU 数据驻留条件提前返回 | 等价表达，仍不对该组合强制开启 Fast |
| `backend/internal/service/openai_reasoning_effort_policy.go` | 删除没有调用点的旧 WS 包装函数 | 不删除实际策略；ctx_pool 和 passthrough 继续调用 `reasoningSession.prepare` |

工具链：Go 1.27.0；使用 CI 实际执行的 golangci-lint 2.13.2（由 Go 1.27.0 构建）。该工具从官方 release 下载到独立临时目录并验证 SHA256，不覆盖机器上现有的 2.9.0。检查时指定 `--max-same-issues=0 --max-issues-per-linter=0`，没有放宽规则。

## 二、集成测试失败的真实原因

[integration job](https://github.com/syx0310/sub2api/actions/runs/34918323703/job/104220668551) 中失败的用例是：

`TestOpenAIWSBareErrorReleasesSilentExecution/passthrough/paired_failed`

错误为 `handler did not exit`。日志表明，首个失败请求的 11/4 tokens 已正常结算，用户/账号并发槽已释放；第二个请求也已成功并记录 2/1 tokens。真正卡住的是第二个成功请求断连后的 relay 收尾，最后到测试上游 10 秒期限触发 EOF 才结束。

时序如下：

1. 上游 `response.completed` 已写给客户端，客户端立即关闭连接。
2. 上游读取 goroutine 仍执行 `OnTurnComplete` / `AfterTurn`；relay 的完成计数在回调返回后才更新。
3. 断连处理读取旧计数，进入“仍有请求在途”的 drain 分支。
4. 回调随后结束并更新计数，但原 drain 只等待新的上游退出/终态信号，没有结算完成通知。上游保持静默时会白等 drain 期限或 EOF。

这是同步时序问题，不是 data race 检测器一定能报出的未同步内存访问，也不是裸 error 的 500ms 窗口失效。此前短次数运行通过不能排除此问题；分析阶段在 GOMAXPROCS=4 的重复运行中已复现。

## 三、实际逻辑改变

修改集中在 `backend/internal/service/openai_ws_v2/passthrough_relay.go`：

- 每个 relay 增加容量为 1 的完成通知通道。合法终态结算回调返回、完成计数更新后，以非阻塞方式发布通知。通知可合并，避免排队增长；完成先于 drain 开始时也不会丢失唤醒。
- drain 收到通知后，复查完成/请求计数及受锁保护的待处理请求表。旧 turn 的通知不能终止下一 turn；自动续轮在准入回调尚未发布计数时，也仍由请求表识别为待处理。
- 所有已准入请求结算完成后及时结束 drain，取消/关闭上游，并继续沿用原有 `upstreamDone` 等待，保证 reader 退出后才读取最终统计。
- 没有可选 `OnTurnComplete` 回调时，匹配请求的合法协议终态也更新内部完成状态；未匹配终态仍不算完成。实际网关配置了回调的路径仍先完成结算再发通知。
- 增加 `drain_start` trace，标明进入 drain 的位置，不记录额外请求内容。

保留不变的行为：

- ctx_pool 的请求处理逻辑；ctx_pool / passthrough 共用的裸 error 500ms 绝对补帧期限、错误连接废弃及用量归并。
- 真正仍在执行的请求断连后继续等待必要的 usage/terminal，原有 drain 超时及写入不确定性保护不缩短、不取消。
- 请求不自动重放，失败用量和并发槽只结算一次；1013 续轮限流关闭语义不变。
- `response.steer`、显式续轮、Astra、compact、client_metadata、stream_options、UA/版本展示、身份策略等既有兼容功能不作语义修改。

## 四、回归与验收

新增 `backend/internal/service/openai_ws_v2/passthrough_relay_drain_completion_test.go`：

- 用通道屏障固定“终态已送达、结算未完成、客户端关闭”的时序，覆盖通知在 drain 等待前和等待期间到达。
- 上游保持静默，配置一分钟 drain；要求结算完成后及时退出，不靠上游主动断开或缩短配置使测试通过。
- 验证不会先于结算回调退出，用量和完成回调各一次、没有请求重放。
- 验证旧通知不能跳过待完成 turn，以及没有可选观察回调时的合法/未匹配终态处理。

修复前，仅添加 trace 定位点和确定性回归测试，两条竞态用例均失败于 `completed settlement did not wake drain`；修复后通过。

最终验证记录：

| 验证 | 结果 |
| --- | --- |
| 修复前确定性回归 | 两条用例按预期失败，确认不是依靠缩短配置/上游 EOF 通过 |
| 修复后完整 relay 包 | 通过，6.237 秒 |
| 原失败 handler 用例，integration 标签、GOMAXPROCS=4、`-count=200` | 全部通过，测试执行 41.346 秒；没有增加重试或修改原 10 秒用例期限 |
| 完整 unit：`go test -p 1 -parallel 2 -tags=unit ./...` | 通过，退出码 0；handler 42.707 秒、repository 21.103 秒、service 175.565 秒、relay 6.237 秒；包含编译的总耗时 9 分 00.31 秒，单进程峰值 RSS 2,633,364 KiB |
| 完整 integration：`CI=true go test -p 1 -parallel 2 -tags=integration ./...` | 通过，退出码 0；handler 42.081 秒、repository 33.590 秒、service 121.130 秒、relay 6.319 秒；总耗时 6 分 56.24 秒，单进程峰值 RSS 1,955,332 KiB。真实 PostgreSQL/Redis 仓储测试已执行，临时容器已自动清理 |
| service / handler / relay 相关 race | 通过，退出码 0，无数据竞争报告；分别 10.187、5.952、1.090 秒；包含编译的总耗时 6 分 46.45 秒，单进程峰值 RSS 2,995,156 KiB |
| 新增确定性回归，`-race`、GOMAXPROCS=4、`-count=100` | 全部通过，2.777 秒 |
| 最终不限量 golangci-lint 2.13.2 | 通过，`0 issues.`、退出码 0；总耗时 4 分 34.28 秒，单进程峰值 RSS 2,373,444 KiB；首次发现的额外 6 项 errcheck 均已清除 |
| 格式与差异检查 | gofmt 已执行；`git diff --check` 通过 |

本地原始日志及校验过的 lint 工具位于 `/tmp/sub2api-ci-fix.99CsgO/`。前端代码没有变化，本次不重复前端测试和打包。

相关 race 的选择范围为：`ClientCloseDuringTerminalSettlement|CompletionDoesNotSkipPendingTurn|WithoutObserverStillSettlesProtocol|BareError|InvalidEncryptedContentLineage|CyberTerminal|LaterTurnPreOutputRateLimit|ClientDisconnectStillDrains|ClientSemantics|WSReasoning|WSNativeCompaction|ParentSubagents|GPT6AstraSteering|ResponseSteer|SucceededForScheduling`。大测试包的 race 编译使用 GOMAXPROCS=1；编译期间触发 cgroup 内存回收，但 `oom=0`、`oom_kill=0`、`oom_group_kill=0`，scope 的 swap 用量为 0。

## 五、资源开销与执行边界

- 新增通知是每连接 O(1) 的固定容量状态，不保存请求体/历史，不增加常驻 goroutine，不增加数据库或 Redis 请求。
- Redis 修改仍是一次有界活跃索引查询，清理批量与复杂度不变；按要求不重新执行并发查询性能压测。
- 重型验证串行执行，使用独立 systemd scope 的 `MemoryMax=3G`、`MemorySwapMax=0`，Go 包编译并行度 `-p 1`。普通验证 GOMAXPROCS=2；原失败用例重复验证采用 GOMAXPROCS=4。
- 不调用真实 OpenAI 账号，不部署、不构建/推送 Docker 镜像。GitHub 上原失败 run 不会因本地修改而改变，本任务不自行 push 或重跑远端任务。
