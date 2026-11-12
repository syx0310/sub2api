# Go 1.27.4 安全升级报告（2026-11-12）

## 范围与改动

用户批准补齐上轮合并审计发现的标准库安全升级。工作继续在 `merge/upstream-v0.2.21`，没有合回 main、push、创建 PR 或构建/推送 Docker 镜像。

代码提交：`26b2eeec2`（`build: upgrade Go to 1.27.4 for net/http security fix`）。本次代码只改 5 个文件中的 6 个版本引用：

| 位置 | 改动 |
|---|---|
| `backend/go.mod` | 最低 Go 版本 `1.27.3 → 1.27.4` |
| `Dockerfile` | 默认构建镜像 `golang:1.27.3-alpine → golang:1.27.4-alpine` |
| `.github/workflows/backend-ci.yml` | 单测/集成和 lint 两个 job 的版本校验同步 |
| `.github/workflows/security-scan.yml` | 安全扫描的版本校验同步 |
| `.github/workflows/release.yml` | 发布二进制构建的版本校验同步 |

所有 setup-go 继续由 `backend/go.mod` 选版本；没有改成浮动 latest，也没有变更第三方模块、go.sum、前端依赖或运行时镜像。Docker 法务文档复制和原有自定义构建步骤保持不变。

已只读确认官方 Docker 标签可用，manifest index 为 `sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf`；这只是核对记录，Dockerfile 仍采用原有 tag 策略，未新增 digest 固定策略。

## 修复内容与逻辑影响

[GO-2026-6859 官方记录](https://vuln.go.dev/ID/GO-2026-6859.json)描述 HTTP/1 服务中 MaxBytesReader/MaxBytesHandler 超限处理与 handler 并发操作响应头时可能发生数据竞争并触发 panic。Go 1.27.4 是该版本线的修复版本；[Go 发布记录](https://go.dev/doc/devel/release#go1.27.4)确认该补丁已于 11 月 10 日发布。

- 改变的是编译进最终二进制的 Go 标准库实现，移除上述已知竞态；不是在业务层绕过请求大小限制。
- 上游 Go 补丁同时包含其他安全/运行时修补，不能描述成只有一个函数发生变化。
- 网关请求体限额、413 处理、WS/SSE 转发、客户端预热/压缩/续接、credits 准入、TTL 计费、本地模型和身份配置均没有新增业务改动。
- 应用版本仍为 **0.2.21**，默认 Codex 身份仍为 **0.167.1**。
- 插件协议和兼容性门控不变：本仓库插件使用独立进程 gRPC，兼容性按应用/协议版本判断，没有因 Go 补丁版本新增拒绝条件；本轮也没有替换插件自身的二进制。
- 运行中的旧二进制不会因修改 go.mod 自动获修复；需要后续使用 Go 1.27.4 重新构建并部署。本轮没有操作运行实例。
- 开发机可使用 `GOTOOLCHAIN=auto` 按项目要求选择工具链；本次验证显式固定 `GOTOOLCHAIN=go1.27.4`，没有更换系统全局 Go。

## 验证

日志目录：`/tmp/sub2api-go1274.0tfU9Y`。编译使用 `GOMAXPROCS=2`、`-p 1`，大任务错峰，继续监控内存及磁盘。

已完成基础检查：Go 1.27.4 工具链实际版本、go.mod/Docker/4 处 CI 校验一致性、workflow YAML 解析、`go mod verify`、11 项发布矩阵辅助测试、Docker 资源及 compose 配置脚本。

完整后端 `go test -json -tags=unit -p 1 ./...`：**63 个包、28,093 项测试通过**（含子测试），17 项既有条件跳过，无失败。需要数据库的本地迁移用例使用本轮独立 PostgreSQL 实际运行，没有复用生产数据库。

竞态检查：Go 标准库 MaxBytesReader/MaxBytesHandler 相关测试通过 **34 项**（含子测试），其中新的 `TestServerMaxBytesHeaderRace` 在 h1/h2 用例下均通过；网关 `internal/pkg/httputil` 与 `internal/service/openai_ws_v2` 通过 **220 项**，均未发现 race。

完整源码 `govulncheck ./...` 已通过（退出码 0），报告 **0 项可达漏洞**，GO-2026-6859 不再检出；另有 6 项仅位于依赖模块，扫描未发现项目调用其受影响路径。这不是声称整个依赖树不存在任何漏洞。

隔离 PostgreSQL/Redis 的 repository 集成测试通过 **1,674 项**，原有并发缓存 TODO 跳过 1 项；两个 embed 包通过 **131 项**。静态 Linux/amd64 二进制按 `CGO_ENABLED=0 -tags=embed -trimpath -ldflags='-s -w ...'` 构建成功，build info 确认为 **go1.27.4**，`-version` 输出应用版本 **0.2.21**、代码提交 **26b2eeec2**。未启动应用服务。

### 去符号二进制扫描的证据边界

本轮使用 govulncheck v1.8.0 与在线 Go 漏洞数据库。发布构建使用的 `-s -w` 会去掉符号表，直接扫描此产物返回 6 项模块级告警（退出码 3），不能把这次直接扫描记成通过。`readelf --sections` 也确认发布产物没有 `.symtab` / `.debug_info`，审计对照产物有这两者。

核对扫描器实现及[官方限制说明](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck#hdr-Limitations)：无法抽取符号信息时会按依赖模块整体报告，输出中的符号列表不能据此当作真实调用链。

为区分构建差异与可达风险，额外完成两组对照，均通过：

1. 采用与发布产物一致的 `CGO_ENABLED=0`、`embed`、Go 1.27.4 扫描 `./cmd/server` 源码，0 项可达漏洞。
2. 使用同样的源码、Go 版本、CGO、embed 和 trimpath，只移除链接参数 `-s -w` 生成审计对照产物；二进制扫描为 0 项可达漏洞。

6 项模块级告警属于未变动的 `golang.org/x/image` 和 `golang.org/x/crypto`：GO-2026-6222、GO-2026-5932、GO-2026-5066、GO-2026-5062、GO-2026-5061、GO-2026-4961。对照扫描未发现引入受影响包/符号；本次没有声称这些依赖版本本身已修补，也没有擅自升级它们。目标标准库漏洞 GO-2026-6859 在各组扫描中均已消失。

`golangci-lint v2.14.0` 在 Go 1.27.4 环境下完成全量复验：**0 issues**，退出码 0。不重跑未变动的前端测试，不能把上轮前端结果冒充本次新增验证；前端既有 xlsx 审计例外也不因 Go 升级改变。

## 状态

**Go 1.27.4 升级和本轮验证已完成**，代码提交为 `26b2eeec2`，报告随单独文档提交保存。main 和 origin/main 均保持 `84593bd3471c93c23d5b7ea46ae78dca9ad4cf9e`，没有合 main、push 或线上变更。此次目标标准库漏洞已不再检出，去符号产物的模块级扫描限制如上单独说明。

本轮单独创建的 PostgreSQL 和集成测试临时容器均已清理，测试数据可重新生成；保留 `/tmp/sub2api-go1274.0tfU9Y` 的日志与本地构建产物，没有操作现有业务容器。
