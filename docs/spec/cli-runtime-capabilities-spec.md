# CLI runtime capabilities spec

## 背景与目标

`docs/zh-CN/command-line-manual.md` 中有一组命令和选项已经出现在手册里，但当前实现缺失或语义不完整。本文定义这些能力的首版完整实现方案，并按从简单到复杂的顺序覆盖。

本文覆盖：

1. `pull` 对本地已有 OCI image 的处理。
2. `run --rm` 的终态清理语义。
3. `run --trigger` 的 trigger 解析和校验。
4. `logs --follow` 的实时日志跟随。
5. `stats` 的运行时资源统计。
6. `run --jupyter`、`run --jupyter-expose` 和 agent YAML 中的 Jupyter 配置。
7. `run -d/--detach` 的后台运行。
8. `run -i/--interactive` 的 prompt/command REPL。
9. `exec <sandbox>` 的一次性 command transcript 执行。

本文不覆盖：

- `build` 和 `push`。这两个命令涉及镜像构建、制品命名、远端仓库、鉴权和发布策略，需要后续单独设计。
- `up` 的 attach/detach 语义。`up` 只注册 agent 和 trigger，不直接启动 agent sandbox；sandbox 由 trigger 或 `agent-compose run` 启动，因此 `up` 执行完成后自然退出，不存在 attach/detach。

## 现状和 harness 约束

项目入口和服务边界：

- `cmd/agent-compose/main.go` 同时承载 daemon 入口和 Cobra CLI；daemon 启动 HTTP/Connect 服务，注册 API、Connect、Jupyter proxy 路由并调用 `pkg/agentcompose/app.Register(di)` 和 `pkg/agentcompose/app.StartBackground(di)`。
- `pkg/agentcompose/app/` 负责服务图注册、路由装配和后台 manager 启动。
- `pkg/agentcompose/api/` 负责 Connect handler 和 protobuf/domain 映射。
- `pkg/agentcompose/adapters/` 负责 daemon-only runtime、session、loader、capability、LLM 和 image backend adapter。
- `pkg/runs/` 负责 project run 状态机、session 准备、command/agent execution 编排和 run cleanup。
- `pkg/projects/`、`pkg/loaders/`、`pkg/storage/configstore/`、`pkg/storage/sessionstore/` 分别拥有 project reconciliation、loader scheduling、SQLite config store 和 session file store。
- v2 Connect API 定义在 `proto/agentcompose/v2/agentcompose.proto`。
- 当前 v2 Connect services 包括 `ProjectService`、`RunService`、`ExecService`、`ImageService` 和 `SandboxService`。其中 `RunService` 已有 `RunAgent`、`RunAgentStream`、`GetRun`、`ListRuns`、`StopRun`，`ExecService` 已有 `Exec`、`ExecStream`，`ImageService` 已有 `ListImages`、`PullImage`、`InspectImage`、`RemoveImage`，`SandboxService` 当前只有 `RemoveSandbox`。
- Jupyter proxy HTTP 路由在 `pkg/agentcompose/proxy/proxy.go`，路径前缀由 `JUPYTER_PROXY_BASE` 决定，默认 `/jupyter`，形态为 `<base>/:sessionID` 和 `<base>/:sessionID/*`。
- runtime driver 抽象在 `pkg/driver/types.go`，当前默认 driver 为 `docker`，并支持 `boxlite`、`microsandbox`。
- compose YAML schema 在 `pkg/compose/spec.go` 和 `pkg/compose/normalize.go`；当前 `AgentSpec` 支持 `provider`、`model`、`system_prompt`、`image`、`driver`、`env`、`capset_ids`、`workspace`、`scheduler`，还没有 `jupyter` 字段。
- image domain 已从 runtime driver 中拆出：`pkg/images.Backend` 定义 `ListImages`、`PullImage`、`InspectImage`、`RemoveImage`，`pkg/agentcompose/adapters.ImageBackends` 提供 Docker daemon、OCI cache 和 Auto backend；`IMAGE_STORE_MODE=auto|docker|oci` 控制默认选择。

持久化现状：

- 全局环境变量、workspace config、loader definition、loader trigger、loader run、loader event 存储在 `DATA_ROOT/data.db`。
- session metadata、notebook cells、event history、runtime state、proxy state 存储在 `SESSION_ROOT`。
- run 输出已经持久化：`project_run.output` 存储汇总输出，`project_run.logs_path` 指向输出文件。command run 使用 `state/runs/<run_id>/output.txt`，agent run 复用 cell artifact `state/cells/<cell_id>/output.txt`。
- 因此首版不新增 run output chunk 表。实时日志能力基于现有 `logs_path/output.txt` 做 append 和 tail。
- 当前 `logs --follow` 仍在 CLI 侧每 250ms 轮询 `ListRuns/GetRun`，并按 `RunDetail.output` 做增量打印；还没有 v2 server streaming follow API。
- 当前 `run --command` 和 `ExecStream` 都直接调用 runtime driver `ExecStream` 运行进程；`run --command` 使用 `bash -lc <command>` 并由 host 写入 command artifacts，还没有统一改为 guest `agent-compose-runtime exec` transcript。
- 当前 session 创建会生成 `proxy/jupyter.json`，Docker、BoxLite、MicroSandbox driver 在 session 启动时按 daemon 级 `JUPYTER_GUEST_PORT` 和 proxy state 启动 Jupyter；还没有 per-run CLI/YAML 开关。

质量门禁：

- `TESTING.md` 是测试标准来源。
- `Taskfile.yml` 中的主质量命令是 `task lint`、`task build`、`task test`。
- `task build` 会构建 Go binary，并进入 `runtime/agent-compose-runtime-sdk` 执行 `npm ci`、`npm run build`、`npm run test:packaging`。
- `task test` 通过 `scripts/test-coverage.sh` 汇总 unit/integration/e2e coverage；`task test:unit` 还会运行 `runtime/agent-compose-runtime-sdk` 和 `runtime/javascript` 的 npm 测试。
- 涉及 proto、CLI、`pkg/runs`、`pkg/agentcompose/api`、`pkg/agentcompose/app`、runtime driver、image backend 或 JS runtime 的变更必须至少通过 `task build` 和相关 Go/JS 单测；合并前按范围运行 `task lint` 和 `task test`。

## 核心概念或领域模型

- `ProjectRun`：一次可观察的运行记录。无论前台、后台、trigger、interactive prompt 单轮或 interactive command 单轮，都必须对应一条 run 记录，便于 `logs`、状态查询和审计。
- `Session/Sandbox`：daemon 管理的 runtime 实例。interactive REPL 模式中，同一个 CLI REPL 复用同一个 session/sandbox。
- `Trigger`：loader 注册的触发器定义。`run --trigger` 是手动触发某个 trigger 的执行，不等同于 scheduler 自动触发。
- `Provider conversation`：由 guest runtime 内的 provider runner 维护的 provider 会话状态。当前 Codex、Claude/cc、OpenCode 已通过 `state/agents/providers/<provider>.json` 持久化 provider session；Gemini runner 可从 CLI 事件读出 session id，但当前没有持久化/恢复语义，因此首版不纳入 interactive prompt。
- `Log artifact`：run 的权威日志文件，由 `project_run.logs_path` 指向。`project_run.output` 是汇总视图，不作为实时 tail 的唯一来源。
- `Transcript event`：JS runtime 对 prompt 和 command 过程输出的统一呈现事件。首版可继续使用现有 `chunk/is_stderr` 流式字段承载纯文本 transcript，后续如需结构化展示，可在 v2 中提升为 `kind/text/name/payload_json/is_stderr` 形态。

## 架构和组件边界

### CLI 层

CLI 负责参数互斥、终端交互、输出格式和退出码映射：

- `run -i` 只允许和 `--command` 或 `--prompt` 组合，不允许和 `-d`、`--trigger` 组合。
- `run -d` 不允许和 `-i` 组合。
- `--jupyter` 可作为单次 run 的覆盖选项。
- `--jupyter-expose` 只作为显式 CLI 覆盖，不进入 YAML agent 默认配置。
- `logs --follow` 使用服务端 follow API，不直接读取 daemon 本地文件。
- `/exit` 和 Ctrl+D 退出 interactive REPL。
- Ctrl+C 首版只做 best-effort 当前轮取消；provider adapter 不承诺一定能中断已经发送给 provider 的 turn。

### Connect service 层

`pkg/runs` 和 `pkg/agentcompose/api` 负责校验、状态流转、run 记录、日志文件、后台 supervisor 和 driver 调用：

- `RunAgentStream` 保持前台流式 run 能力。
- `RunAgent` 当前是非 streaming 但仍同步执行 run；`run -d` 需要新增真正的后台启动语义，或扩展现有 API 明确 detach 后由 daemon supervisor 接管。
- 新增日志 follow API，服务端按 `logs_path` 做 offset tail。
- 新增 stats API，由 service 调用 driver 的 stats 能力。
- 不新增 TTY、WebSocket 或 bidirectional stdin 传输。interactive 是 CLI 层的 run-level REPL：每轮读取完整用户输入，提交一次 run 或 exec，并消费服务端 transcript stream。
- `StopRun` 当前只把非 terminal run 标记为 canceled；后台 run 需要把 cancellation context 和实际执行 goroutine/driver 调用关联起来，避免只改 DB 状态、不停止执行。

### Runtime driver 层

driver 负责 runtime 相关的不可移植能力：

- driver 不承载 interactive prompt/command 协议；driver 只继续提供已有 `ExecStream` 等一次性进程执行能力，由 guest JS runtime 负责 transcript 化。
- stats 通过 driver optional interface 渐进实现。Docker、BoxLite、MicroSandbox 按各自可获得指标映射到统一响应；缺失字段显示 unknown/null 或文本表格 `-`，只有 driver 没有稳定指标入口时才返回 typed unsupported。
- `pull` 不属于 runtime driver 能力。它面向 OCI image reference，由 image service/backend 负责 inspect/pull；Docker daemon 可以作为当前 OCI image backend，未来也可以替换或补充 daemon-less OCI image backend。BoxLite 和 MicroSandbox 从 OCI image 派生自身可运行 artifact，但不改变 `pull` 语义。

### Guest runtime 层

`runtime/javascript/src/prompt.ts`、`runtime/javascript/src/command.ts` 和 provider runner 负责 prompt/command 执行及 transcript 呈现：

- `agent-compose-runtime prompt --provider --message-file ...` 仍是一轮 provider 调用的执行单元。
- `agent-compose-runtime exec --request-file ...` 已存在，是 loader `scheduler.exec`/`scheduler.shell` 的一轮 command 调用执行单元。command 输出通过 JS runtime 归档到 `stdout.txt`、`stderr.txt`、`output.txt`、`command-result.json`，并以 transcript stream 返回给 host。本方案要求 CLI `run --command` 和 `exec <sandbox>` 也收敛到这一路径。
- Codex、Claude/cc、OpenCode 通过已有 provider session 文件复用上下文。
- interactive REPL 中每条用户输入触发一次新的 `ProjectRun`，但复用同一个 session/sandbox。prompt 模式额外复用 provider conversation。
- Gemini runner 当前是一轮式行为，首版 interactive prompt 返回 unsupported。

## API、CLI、配置、数据模型或协议变化

### 1. `pull`

CLI 行为：

- `agent-compose pull <image>`：如果 `<image>` 已存在于本地 OCI image store/backend，则直接成功并输出 skipped 信息。
- 如果不存在，则调用 OCI image backend pull。
- pull 失败返回非 0，错误中包含 image reference 和 image backend/store，不包含 runtime driver 语义。
- `agent-compose pull` 无 image 参数时，按当前 project normalized agents 收集 image refs 并逐个拉取。
- `agent-compose image pull <image>` 是 deprecated wrapper，行为必须与顶层 `pull <image>` 保持一致。

服务/image backend 行为：

- `ImageService.InspectImage`、`ImageService.PullImage` 和 `pkg/images.Backend.InspectImage/PullImage` 已存在；本方案只要求在 pull 路径增加 pull 前 inspect-and-skip。
- Docker daemon backend 使用 Docker image inspect 判断本地是否已有 OCI image；OCI cache backend 使用 `imagecache.Cache.Inspect` 判断布局/rootfs cache 是否已有。
- `IMAGE_STORE_MODE=auto|docker|oci` 选择默认 backend；请求里的 `ImageStoreKind` 仍可显式指定 Docker daemon 或 OCI cache。
- inspect 命中时 `PullImageResponse.status=succeeded`，`image` 填充本地镜像信息，`warnings` 包含 skipped/local already exists 说明。
- 后续 `build`、`push` 也应复用同一 OCI image domain，而不是挂到 Docker/BoxLite/MicroSandbox runtime driver 上。
- BoxLite/MicroSandbox 在启动 runtime 时可以基于已存在或新拉取的 OCI image materialize 自己的 runtime artifact；materialization 失败属于启动 runtime 的错误，不是 `pull` 的 driver-specific 行为。

### 2. `run --rm`

当前问题：

- `RunAgentRequest.cleanup_policy` 只有 stop-on-completion 和 keep-running 两种语义；默认 run 会在 terminal 后停止 VM，`--keep-running` 保留 runtime。
- `cmd/agent-compose/main.go` 当前在 `runComposeRunCommand` 中只在 run 成功后、CLI 获取 terminal detail 后调用 `SandboxService.RemoveSandbox(force=true)`；run 失败、取消、CLI 中断或获取 detail 失败时不会可靠删除本次 run 创建的 sandbox。
- service 端已有 `pkg/runs.cleanupProjectRunSession` 和 `project_run.cleanup_error`，但 cleanup 语义是停止 VM，不是删除 session/sandbox。

目标语义：

- `--rm` 表示 run 进入 terminal 状态后清理本次 run 创建的 session/sandbox。
- 清理必须在成功、失败、取消后都执行。
- 如果 run 使用的是用户显式指定的已有 sandbox，则不能删除该 sandbox，只清理本次 run 的临时状态。
- `--rm` 不应只依赖 CLI 进程存活；首选在 `pkg/runs.Controller` 记录 run 是否创建了新 session，并在 terminal transition 后调用 session removal 路径。CLI 可保留最终提示，但不是权威 cleanup owner。

失败语义：

- run 的原始退出码优先于 cleanup 退出码。
- 如果 run 成功但 cleanup 失败，CLI 返回 cleanup 失败，并在 stderr 中说明 run 已成功但 sandbox 清理失败。
- 如果 run 失败且 cleanup 也失败，CLI 返回 run 失败，并附带 cleanup warning。

### 3. `run --trigger`

当前问题：

- 当前 `RunAgentRequest.trigger_id` 会传到 `pkg/runs.Coordinator.BeginRun` 并写入 `project_run.trigger_id`，但 service 未按 project/agent 解析 managed loader trigger。
- 当前 `run --trigger` 未把 trigger `prompt` 解析进实际 `ExecuteAgentRequest.Message`；如果 CLI 没有同时传 `--prompt`，`project_run.prompt` 和 agent message 仍为空。
- `pkg/loaders.Controller.LoadLoaderForRun(loaderID, triggerID)` 只能在已知 loader id 时解析 loader trigger；manual project run 需要从 project id、agent name 和 trigger id 反查 managed scheduler/loader。

目标语义：

- `run --trigger <trigger_id>` 必须解析 managed loader trigger。
- trigger 不存在时直接失败，不创建可执行 run。
- trigger 存在时，从 trigger 定义解析 agent、workspace、prompt/template、环境变量和 Jupyter 默认配置。
- CLI 手动执行 disabled trigger 时允许运行，因为这是显式 operator 操作；输出 warning，并在 JSON 输出中包含 `warnings` 字段。

接口变化：

- `RunAgentRequest.trigger_id` 保留。
- `pkg/runs.Controller` 在创建 run 前增加 `ResolveTriggerForManualRun(ctx, projectID, agentName, triggerID)` 或等价依赖，查询 `project_scheduler`/managed loader/`loader_trigger`，并校验 trigger 属于当前 project agent。
- 解析后的 prompt 必须进入实际 agent execution request，而不是只作为 run metadata。

### 4. `logs --follow`

当前问题：

- `cmd/agent-compose/main.go` 当前 `followOrPrintProjectLogs` 每 250ms 调用 `ListRuns/GetRun`，再用 `writeLogDetails` 按 `RunDetail.output` 的字符串长度计算增量。
- 该实现不会读取 `project_run.logs_path` 指向的 `output.txt`，也不是服务端 streaming API；大输出、daemon 远程部署和 run 进行中 artifact flush 都容易出现延迟或重复/截断语义不清。

目标语义：

- `logs <run_id>` 默认读取完整日志。
- `logs --tail N <run_id>` 读取最后 N 行。
- `logs --follow <run_id>` 从当前末尾或指定 tail 位置开始持续输出，直到 run terminal 后文件 tail 完成。

持久化设计：

- 首版复用现有 `project_run.logs_path` 指向的 `output.txt`。
- command run 和 agent run 都必须在执行过程中实时 append 到该文件。
- `project_run.output` 在 run 结束时保存汇总输出，或按现有节奏更新，但不作为 follow 的权威来源。
- 不新增 DB chunk 表。
- 如后续需要 stdout/stderr、时间戳、provider event 等结构化元数据，可在同目录增加 `output.jsonl` sidecar；DB chunk table 仅作为未来高查询压力或跨节点日志索引的增强项。
- command run 当前只在 runtime `ExecStream` 完成后通过 `WriteCellArtifacts` 写 artifact；本方案需要在 stream writer 收到 chunk 时同步 append run log artifact。
- agent run 当前通过 cell artifact `state/cells/<cell_id>/output.txt` 形成 `logs_path`；如果 cell artifact 只在结束时完整写入，需要在 `AgentExecutionStream.OnChunk` 或等价 host sink 中同步 append，确保 follow 能看到中间输出。

接口变化：

- 在 `RunService` 新增 v2 server streaming API，例如 `FollowRunLogs(FollowRunLogsRequest) returns (stream RunLogChunk)`。
- `FollowRunLogsRequest` 包含 `project_id`、`run_id`、`tail_lines`、`start_offset`、`follow`。
- `RunLogChunk` 包含 `data`、`offset`、`is_final`、`run_status`。

### 5. `stats`

当前问题：

- 当前 CLI 没有 `stats` command；`proto/agentcompose/v2/agentcompose.proto` 的 `SandboxService` 只有 `RemoveSandbox`。
- `pkg/driver.BoxRuntime` 当前没有 stats 方法，Docker、BoxLite、MicroSandbox driver 也没有统一资源指标 adapter。

目标语义：

- `agent-compose stats <sandbox>` 输出 CPU、memory、network、block IO 和 uptime。
- 默认表格输出；`--json` 输出机器可读 JSON。
- JSON 字段集合必须稳定；不同 driver 的差异只能体现在 metric value 为 null、metric status 为 unknown/unavailable，或文本表格显示 `-`，不得因 driver 不同省略字段。
- sandbox 不存在返回非 0。
- driver 不支持 stats 时返回 typed unsupported，CLI 显示明确错误。

接口变化：

- driver 增加 optional `Stats(ctx, session, vmState)` 或等价接口；service 以 sandbox/session id 解析 session 与 driver runtime。
- v2 `SandboxService` 新增 `GetSandboxStats`，避免另起语义重复的 service。
- `SandboxStats` 字段使用 proto optional/wrapper，或使用 `value + status` 的 metric message，保证 JSON key 稳定且能表达 unknown/unavailable。
- Docker 首版基于 Docker stats API 实现，目标返回 CPU、memory、network、block IO 和 uptime。
- MicroSandbox 首版应优先映射 SDK `Metrics` 中已有的 CPU、memory、disk IO、network 和 uptime 字段。
- BoxLite 如当前 cgo header 的 box metrics 回调稳定可用，首版同步映射 CPU、memory、network 和命令统计等可获得字段；block IO、memory limit、uptime 等无法可靠获得的字段返回 unknown/null。

### 6. Jupyter：CLI 和 YAML

当前问题：

- 当前 Jupyter 是 session/runtime 级默认启动行为：`sessionstore.Store.CreateSession` 创建 `proxy/jupyter.json`，driver 启动 runtime 时按 `JUPYTER_GUEST_PORT`、`JUPYTER_PROXY_BASE` 和 `ProxyState` 启动 Jupyter。
- 当前 CLI 没有 `run --jupyter`、`run --jupyter-expose` 或 `--no-jupyter` flags；v2 `RunAgentRequest` 和 compose `AgentSpec` 也没有 Jupyter 字段。
- 当前 `ProxyState.HostPort` 是 daemon 与 runtime/Jupyter 连接所需的 per-session proxy/port 信息，不应被 agent YAML 扩展为用户可控的外部 host bind。

目标语义：

- Jupyter 默认关闭。
- `run --jupyter` 为本次 run 启用 Jupyter。
- `run --jupyter-expose` 是显式 CLI 行为，用于让 daemon 通过 agent-compose proxy 暴露可访问入口；不得要求 runtime driver 直接做 host port expose。
- YAML 中支持 agent 级默认 Jupyter 配置，使 trigger 创建的 sandbox 也能按 agent 定义启动 Jupyter。
- YAML 不支持外部 bind/host port，避免部署配置被 agent 定义隐式打开；proxy 的 listen/bind 属于 daemon 部署配置，不属于 agent/runtime driver 配置。

YAML 示例：

```yaml
agents:
  reviewer:
    image: agent-compose-guest:latest
    provider: codex
    jupyter:
      enabled: true
      guest_port: 8888
```

配置规则：

- `jupyter.enabled` 默认 `false`。
- `jupyter.guest_port` 默认使用 daemon 当前 `JUPYTER_GUEST_PORT` 配置。
- CLI `--jupyter` 覆盖 YAML 的 disabled。
- CLI `--no-jupyter` 如后续提供，则覆盖 YAML 的 enabled；首版可以不提供该选项。
- `--jupyter-expose` 只允许 CLI 显式开启，效果是创建/标记 agent-compose proxy access endpoint；如需外部可达地址，使用 daemon/proxy 级配置表达，不写入 agent YAML，也不传递为 agent 可控的 runtime driver host bind/port mapping。

服务变化：

- compose `AgentSpec`、normalized spec、v2 `AgentSpec` 和 managed agent definition schema 增加 `jupyter` 字段。
- session 创建时把 resolved Jupyter config 写入 runtime/session state。
- `ProxyState` 记录 proxy path/access URL、guest endpoint、guest port、Jupyter URL 和 token；`HostPort` 如保留只能表示 daemon 连接 runtime/Jupyter 所需的内部 proxy/port 结果，不得变成 agent YAML 可配置的外部 host bind。
- trigger-created sandbox 使用 agent resolved Jupyter config。

### 7. `run -d/--detach`

当前问题：

- 当前 CLI 没有 `-d/--detach` flag。
- v2 `RunAgent` 虽然是 unary API，但 `pkg/agentcompose/app.runControllerDelegate.RunAgent` 仍同步调用 `pkg/runs.Controller.RunProjectAgent`，不是后台启动。
- 当前 `StopRun` 只把 DB 中 pending/running run 标记为 canceled；还没有 daemon supervisor、run goroutine registry 或与执行 context 绑定的 cancellation。

目标语义：

- `run -d` 创建 run 后立即返回，不 attach 输出。
- CLI 输出 `run_id`、`session_id/sandbox_id`、初始状态，以及查看日志的命令。
- 后台 run 由 daemon supervisor 管理，不由 CLI 进程持有。
- `run -d` 与 `-i` 互斥。
- `StopRun` 通过统一 run context cancellation 请求停止后台 run。服务层负责把 run 进入 canceling/canceled 或 failed 的 terminal 状态，不因不同 runtime driver 能力差异留下永久 running。
- prompt 和 command 都走 JS runtime transcript 流；取消优先沿用 common execution context 传播到 `RunAgentStream`/`ExecStream(ctx)` 及其下游进程，不为 Docker/BoxLite/MicroSandbox 暴露不同的用户语义。
- 如果某个 driver/provider/子进程不能被即时中断，服务仍记录 cancel requested 和最终确定状态；强制终止是 best-effort implementation detail，不成为 API 语义差异。

接口变化：

- 新增后台启动 API，例如 `StartRun(StartRunRequest) returns StartRunResponse`，或在现有 `RunAgentRequest` 中明确 `detach=true` 并走非 streaming path。
- `StartRunResponse` 包含 `run_id`、`session_id`、`status`、`logs_path/logs_url`。
- `pkg/runs.Controller` 或上层 app supervisor 启动 goroutine 执行 run，并按既有 `Coordinator` 状态机更新 `ProjectRun`。
- supervisor 必须登记 cancel func；`StopRun` 对 running run 先请求 cancel，再由执行路径进入 canceled/failed terminal 状态。仅当执行对象已不存在或 daemon 重启后，才退化为 DB reconciliation。

失败和恢复语义：

- 如果 daemon 在创建 run 前失败，CLI 返回失败且无 run。
- 如果 daemon 在 run 创建后失败，重启 reconcile 将 pending/running run 标记为 failed，错误说明 daemon interrupted。
- 首版不实现 durable queue 和跨 daemon restart 自动恢复执行。

### 8. `run -i` prompt/command REPL

当前问题：

- 当前 CLI 没有 `-i/--interactive` flag。
- 当前 prompt run 可通过 `RunAgentStream` 连续提交，但 CLI 没有复用 sandbox/provider conversation 的 REPL loop。
- 当前 command run 使用 host 侧 `bash -lc` direct exec 和 host artifact 写入，还没有复用 guest `agent-compose-runtime exec` transcript。

目标语义：

- `run -i --prompt` 进入 provider 对话式 REPL。
- `run -i --command` 进入 command REPL。初始 `--command <cmd>` 可作为第一轮输入；后续每条用户输入是一轮 command run。
- 每一轮用户输入都是一条新的 `ProjectRun`，复用同一个 session/sandbox。prompt 模式复用 provider conversation，command 模式复用 workspace、home、state 和 runtime session。
- 当前轮完成后才读取下一条用户输入；首版不支持运行中 stdin 透传。
- `/exit` 和 Ctrl+D 退出 REPL。
- Ctrl+C 首版做 best-effort：可以取消当前轮等待和请求 `StopRun`，但不承诺 provider adapter 或 command 子进程一定能被即时中断。
- `run -i` 与 `-d`、`--trigger` 互斥；只允许和 `--prompt` 或 `--command` 组合。

Prompt REPL：

- CLI 每轮调用现有 agent run path，传入同一个 `session_id`，cleanup policy 为 keep-running。
- guest runtime 继续使用 `runtime/javascript/src/prompt.ts` 的 one-shot provider 调用。
- Codex、Claude/cc、OpenCode 依赖已有 `state/agents/providers/<provider>.json` 复用 provider session。
- Gemini 首版返回 unsupported，因为当前 runner 没有稳定的 provider session 复用语义。

Command REPL：

- CLI 每轮把用户输入作为一条 command run 提交到 `RunAgentStream`，同样传入同一个 `session_id`，cleanup policy 为 keep-running。
- service 执行 command run 时应通过 guest `agent-compose-runtime exec --request-file ...` 统一执行和归档，而不是直接 `bash -lc` 后只由 host 拼 artifacts。这样 prompt 和 command 都由 JS runtime 输出 transcript，并共享 artifact 行为。
- command 输入首版按完整命令行或粘贴文本提交，不支持交互式 stdin；需要运行中读取 stdin 的程序应改成一次性命令或脚本输入。
- command transcript 默认保留 stdout/stderr 原样输出；可在开头显示 `$ <command>`，在结束时显示非零 exit code 摘要。JSON/verbose 模式应额外展示 `run_id`、`session_id`、`exit_code` 和 artifact 路径。

接口变化：

- 不新增 `ExecInteractive`，不引入 WebSocket，不把 v2 `ExecStream` 改成 bidirectional streaming。去掉 TTY/stdin/resize 后，交互不再需要客户端运行中上行帧。
- `RunAgentStream` 继续作为 project/run 入口，支持 `prompt | command | trigger`。interactive REPL 由 CLI 循环调用该接口完成。
- `ExecStream` 保留为“对已有 sandbox 执行一次 command 并流式返回 transcript”的低层能力。v2 无兼容约束时，可调整其 response 字段以复用统一 `TranscriptEvent`，但不应再新增语义重叠的 `ExecInteractive`。

### 9. `exec <sandbox>` 一次性 command transcript

当前问题：

- 当前 CLI 已有 `exec <sandbox> [command] [args...]`，并保留 deprecated `--agent`、`--run-id`、`--session-id` target flags。
- 当前 `ExecService.ExecStream` 直接调用 runtime driver `ExecStream`，返回 `ExecStreamResponse.chunk/is_stderr`，不会创建 `ProjectRun`。
- 当前低层 exec 没有通过 guest `agent-compose-runtime exec` 写入 `stdout.txt`、`stderr.txt`、`output.txt`、`command-result.json` 等统一 command artifacts。

目标语义：

- `exec <sandbox> [command] [args...]` 在已有 sandbox 中执行一次 command，并流式返回 JS runtime transcript。
- `exec <sandbox> --command "..."` 保持一次性 shell command 语义。
- `exec` 不进入 REPL；如需要 REPL，应使用 `run -i --command --sandbox <sandbox>` 或后续显式 `exec -i` 作为 CLI sugar，但实现仍是“每轮一次 ExecStream/RunAgentStream”，不是运行中 stdin。
- `exec` 不创建 `ProjectRun`，除非用户通过 `run --command` 入口执行。低层 exec 的结果仍返回 `ExecResult`，并可在 sandbox cell artifacts 中保留 command artifact。

接口变化：

- `ExecStream` 继续是 server streaming：`ExecRequest` 一次性提交 target、command、cwd、env，服务端返回 transcript/output/completed。
- `ExecService` 内部执行路径改为在 session `state/cells/<exec_id>` 或等价 artifact 目录写 request file，再调用 guest `agent-compose-runtime exec --request-file ...`；`ExecResult` 继续保留 stdout/stderr/output/truncation/error 字段。
- v2 可破坏兼容时，优先把 `ExecStreamResponse` 与 `RunAgentStreamResponse` 的输出字段统一到 `TranscriptEvent`，减少 CLI 对 prompt/command 的分叉展示代码。

## 工作流和失败语义

### 从简单到复杂的覆盖顺序

1. 先实现 OCI image `pull` inspect-and-skip，风险最低，主要影响 CLI 和 image service/backend。
2. 实现 `run --rm` cleanup-on-terminal，修正生命周期语义。
3. 实现 `run --trigger` 解析，保证手动 trigger 真正使用 trigger 定义的 prompt/config。
4. 实现 `logs --follow`，同时统一 run 过程中的文件 append。
5. 实现 `stats`，按 driver optional interface 渐进映射可获得指标。
6. 实现 Jupyter YAML 和 CLI 覆盖，确保 trigger-created sandbox 可以按 agent 配置启动 Jupyter。
7. 实现 `run -d` daemon supervisor，支持后台运行和日志查看。
8. 实现 `run -i` prompt/command REPL，复用 session/sandbox，每条输入生成一个 run。
9. 整理 `exec <sandbox>` 一次性 command transcript 输出，减少与 run command 的展示分叉。

### 统一错误模型

- 参数互斥错误由 CLI 本地拦截。
- 资源不存在返回 not found。
- driver 不支持返回 unsupported，不伪装成普通 execution failure；`pull` 例外，它返回 image backend/store 维度的错误，不返回 runtime driver unsupported。
- provider 不支持 interactive prompt 返回 unsupported，并列出当前支持 provider。
- 需要运行中 stdin、terminal resize 或原生 TTY 的 command 返回明确 unsupported/usage error；本方案不提供 process-level interactive exec。
- run 已创建后的执行失败必须落库为 failed，并保留日志。
- terminal run 的 cleanup、日志 flush、proxy shutdown 都不得吞掉原始执行错误。

## 测试、质量门禁和验收标准

必须覆盖的测试：

- CLI 参数互斥和输出格式单测：`run -i`/`-d`、`--jupyter`、`--jupyter-expose`、`logs --follow`、`stats`。
- `pkg/runs` controller、`pkg/agentcompose/api` handler 和 CLI 单测：
  - `run --trigger` 不存在 trigger 失败。
  - disabled trigger 手动运行成功但带 warning。
  - `run --rm` 成功/失败/取消后都触发 cleanup。
  - `logs --follow` 从 offset/tail 读取并在 run terminal 后结束。
  - `run -d` 创建 run 后立即返回，后台状态最终 terminal。
- driver 单测或集成测试：
  - Docker stats 字段解析。
  - MicroSandbox stats 字段映射，缺失字段保持稳定 JSON key。
  - BoxLite stats 可得字段映射，缺失字段为 unknown/null。
- image service/backend 测试：
  - OCI image inspect skip pull。
  - Docker-backed OCI image backend inspect 命中和未命中。
  - `pull` 结果不随当前 runtime driver 为 Docker/BoxLite/MicroSandbox 而改变。
- runtime/provider 测试：
  - Codex、Claude/cc、OpenCode interactive prompt 连续两轮复用同一 provider session。
  - command REPL 连续两轮复用同一 session/workspace，并为每轮生成独立 `ProjectRun` 和 `logs_path`。
  - JS runtime command transcript 写入 stdout/stderr/output artifacts，并通过 service stream 输出。
  - Gemini interactive prompt 返回 unsupported。
- Jupyter 测试：
  - YAML `jupyter.enabled: true` 被 trigger-created sandbox 使用。
  - YAML 不产生外部 host bind/host port 配置。
  - CLI `--jupyter-expose` 只创建 agent-compose proxy access endpoint，不暴露 agent 可控的 runtime driver host bind/port mapping。

质量门禁：

- 修改 proto 后重新生成 Go/TS client。
- 至少运行相关包单测和 `task build`。
- 合并前运行 `task lint` 和 `task test`，或在 PR 中说明无法运行的环境原因。

验收标准：

- 手册中本文覆盖的命令均有真实实现或明确 unsupported 错误。
- `docs/zh-CN/command-line-manual.md` 不再描述 `up` attach/detach。
- `build`、`push` 在 CLI 手册中标记为后续设计，或暂不作为已实现命令展示。
- `logs --follow` 不依赖轮询 `RunDetail.output`。
- `run -i --prompt` 可对 Codex、Claude/cc、OpenCode 完成连续多轮交互。
- `run -i --command` 可连续执行多轮 command，每轮有独立 run 记录和日志，且不依赖 TTY/stdin/resize。

## 首版不做事项

- 不实现 `build`、`push`。
- 不给 `up` 增加 attach/detach。
- 不新增 run output chunk DB 表。
- 不实现 durable background run queue。
- 不保证 provider turn 的强中断。
- 不实现任何 TTY、PTY、terminal resize、WebSocket TTY endpoint 或运行中 stdin 透传。
- 不新增 `ExecInteractive`；不把 v2 `ExecStream` 改成 bidirectional streaming。
- 不要求所有 driver 首版支持完整 stats 字段；缺失字段按 unknown/null/`-` 表达。
- 不在 YAML agent 配置中支持 Jupyter 外部 host bind/host port。
- 不把 Gemini 纳入 interactive prompt 首版 provider。

## 关键假设和已确认决策

- `run -i` 必须支持 prompt 和 command 两种 REPL；二者都是 run-level 交互，不是 process-level TTY。
- prompt interactive 首版支持 Codex、Claude/cc、OpenCode；Gemini 暂不支持。
- interactive prompt/command 中一条用户输入对应一条 run，同一个 REPL 复用同一个 session/sandbox。
- prompt 和 command 的效果呈现都走 JS runtime transcript 流。prompt 使用 `agent-compose-runtime prompt`；command 使用 `agent-compose-runtime exec`。
- v2 不需要保持兼容，但不新增语义重叠的 `ExecInteractive`；保留 `ExecStream` 作为一次性 command transcript stream，并可调整输出事件模型以复用 `TranscriptEvent`。
- provider turn 首版不承诺可被中途强制打断。
- 退出 REPL 使用 `/exit` 和 Ctrl+D。
- `build` 和 `push` 从本方案移除，后续单独定义和开发。
- `up` 只是注册 agent 和 trigger，不启动 sandbox，因此没有 attach/detach。
- YAML 中需要 agent 级 Jupyter 配置，默认关闭；YAML 只控制 proxy 访问，不隐式开启外部 host 暴露。
- 当前 run 输出已经持久化到 DB 字段和文件，首版实时日志应复用现有日志文件，而不是新增 chunk 表。
