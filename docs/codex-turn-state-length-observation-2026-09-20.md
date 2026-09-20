# Codex Turn State 长度观测

## 范围

- 从 main `22342f86f` 新建 `feature/usage-codex-turn-state-length`。
- 只观测 `x-codex-turn-state` 的字节长度，不恢复撤回 PR #7315 的采集、票据缓存、注入或调度门控。
- 不剥离或改写票据，不改变身份、连接亲和、重试、计费、Body Size 的原有语义。
- 不合回 main、不 push、不连接业务数据库；数据库验证使用临时测试实例。

## 页面与字段

管理员 Usage 页的列设置新增 **Turn State 长度 / Turn State Length**，默认隐藏，顺序紧接 **Body Size**。展开后显示四项：

| 项目 | 含义 |
| --- | --- |
| 请求头 | 网关交给上游 transport 的 HTTP / 新建 WS 握手头中的长度 |
| 请求元数据 | 发往上游的 `client_metadata["x-codex-turn-state"]` 字符串长度 |
| 响应头 | HTTP / 新建 WS 握手响应头中的长度 |
| 响应元数据 | 本响应 `response.metadata.headers` 中首次非空声明的长度 |

单位为字节（B），JSON 字符串按解码后的 UTF-8 字节计算，不计算 JSON 转义字符或整个请求大小。HTTP 头按照现有取值惯例去掉首尾空白后计长。

- `0 B`：该位置已观测，但没有值或为空。
- `—` / NULL：历史记录、关闭观测、不适用或没有观测到该来源。
- 不按 292 等特定长度判断票据有效性；“本响应收到的值”不等于“客户端正在使用的值”。
- 老版本列配置只补上新列的默认隐藏标记，不重置其他显式显示/隐藏选择。用户打开本列后，刷新仍保留选择。
- 隐藏列仅影响展示，默认仍采集新请求的长度；历史记录不会反推或回填。
- 仅管理员 DTO 暴露 `codex_turn_state`；普通用户 Usage DTO 不增加该字段。
- 本次没有增加长度筛选、全局排序、独立统计接口或错误请求页的新列；没有 Usage 记录的失败请求不会凭空生成计费行。

## 转发侧实现

HTTP 普通 Responses、passthrough、Anthropic 兼容桥、WS → HTTP bridge 在每次上游尝试前新建观测器；请求元数据长度从最终 HTTP 构造体携带整数快照，避免重读或复制整个请求体。响应解析只针对 `response.metadata` 读取对应头。

ctx_pool 和 passthrough WS 每个 response 独立记录，后续轮次不继承前一轮观测；复用连接时不把缓存的握手头冒充本次新收到的头。自动续接没有新的上行请求，相关上行字段保持 NULL。观测器以锁保护快照，passthrough 使用独立的原子指针切换轮次，异步 Usage 记录只接收整数值快照。

保留客户端同 turn 回传、不跨 turn 继承的现有协议行为，不对透传数据做任何新增改写。已核对的[客户端远端基线](https://github.com/openai/codex/blob/5c5308fc9a9ee789049d646ef11e5400384b9c6f/codex-rs/core/src/client.rs#L272)为 `5c5308fc9a`；结合 OpenAI Docs 的 [WebSocket 文档](https://developers.openai.com/api/docs/guides/websocket-mode)核对，该公开指南不定义此私有头的 292 长度有效性。

## 数据库、关闭与开销

新增迁移 `239_add_usage_log_codex_turn_state_lengths.sql`，为 usage_logs 添加四个可空 INTEGER 列。所有单条、批量、best-effort / 去重与回退写入路径以及查询扫描顺序同步更新。沿用当前手写 SQL 扩展字段模式。

- 无默认值、无历史回填、无新增索引；历史记录保持 NULL。执行 ALTER TABLE 仍需要数据库 DDL 锁，不是完全无锁操作。
- 数值本体至多 16 字节/行，不含行布局、NULL bitmap、WAL 等额外开销。
- 沿用既有日志队列及批量 INSERT，不新增逐请求数据库/Redis 查询或外部请求。
- 不在观测器中保留票据字符串、摘要、头集合或请求正文；转发原有的协议状态管理不变。
- 取已解析字符串长度为 O(1)，字段定位/JSON 解码仍有成本；只在相关 metadata 事件解析，不对所有流式增量重新序列化。

关闭观测可设置：

```yaml
gateway:
  disable_codex_turn_state_length_observation: true
```

也可用 `GATEWAY_DISABLE_CODEX_TURN_STATE_LENGTH_OBSERVATION=true`，**需重启生效**；没有新增设置页开关。关闭后新记录不再采集长度，原始票据继续按原逻辑转发。

若后续删除功能，应先停读写并移除 UI，再通过后续迁移删除列。不要修改已经执行的迁移，也不要为清理观测值删除整条 Usage / 计费记录。

## 验证

日志目录：`/tmp/sub2api-turn-state-length.deTKqC`。重任务串行，scope 设置 `MemoryMax=3G`、`MemorySwapMax=0`，Go 包编译并行度 1。

| 检查 | 结果 |
| --- | --- |
| 后端全量 unit | 通过，unit.log；service 189.316s、repository 20.724s，整体退出 0 |
| repository 全量 integration | 通过，integration.log；真实临时 PostgreSQL/Redis，包含迁移、所有四种写入路径及 NULL/零值检查，33.930s |
| 前端类型检查 | vue-tsc -b 通过，frontend-typecheck.log |
| Usage / i18n 定向 Vitest | 4 个文件、51 个用例通过，frontend-tests.log；包含默认隐藏、列顺序、已保存偏好与用户手动显示后的再加载 |
| 严格 lint | golangci-lint 2.13.2 通过，0 issues，lint.log；未放宽检查规则 |
| 长度观测与相关 WS race | service 定向回归通过，race.log，退出 0；包含 HTTP 观测、ctx_pool / passthrough 两轮隔离及并发快照 |
| 前端生产构建 | Vite 构建通过，frontend-build.log，26.70s；保留原有配置，独立类型检查通过后在本次调用中省去重复的 checker 进程 |
| 后端 embed 构建 | 通过，backend-build.log；产物 -version 返回 0.2.7，未启动业务服务 |

unit 最大单进程 RSS 为 2,770,180 KiB，repository integration 为 1,267,168 KiB；前端类型检查为 1,926,036 KiB、定向测试为 450,428 KiB。这些是测试/编译指标，不是运行服务内存指标。未发起真实账号的模型请求。

race 使用单并发、GOGC=30、GOMEMLIMIT=768MiB，最大单进程 RSS 3,088,788 KiB；编译触及约 3 GiB 的内存预算，最终正常退出。前后端构建最大单进程 RSS 分别为 1,556,412 KiB 与 1,552,224 KiB。

前端构建保留 Browserslist 数据过旧及较大 chunk 警告，没有借此更新依赖或放宽检查。Go / 前端依赖、Dockerfile、CI 规则均未改动。测试数据库容器已清理，业务数据库没有执行本次迁移。

最终 git diff --check 通过；生成的构建资源和临时日志不纳入 Git。该分支尚未合回 main，也未 push。
