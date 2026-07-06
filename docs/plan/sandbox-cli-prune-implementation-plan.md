# agent-compose sandbox prune 实施计划

输入 spec：`docs/spec/sandbox-cli-prune-spec.md`

## 阶段 1：建立 `sandbox` 命令组并复用现有行为

目标：新增 `agent-compose sandbox` 一级命令，先让 `ls/stop/resume/rm` 与现有顶层命令行为一致，项目保持可构建。

依赖：

- 现有 `cmd/agent-compose/main.go` 中的 `psCmd`、`stopCmd`、`resumeCmd`、`rmCmd`、`runComposePSCommand`、`runComposeSandboxActionCommand`、`runComposeSandboxRemoveCommand`。
- 现有 CLI integration test helper：`newComposeServiceStubServer`、`executeCLICommand`、`testCLIProject`、`testCLISessionSummary`。

实施工作：

1. 在 `cmd/agent-compose/main.go` 构建 `sandboxCmd`，`Use` 为 `sandbox`，默认执行 help。
2. 在 `sandboxCmd` 下注册 `ls`、`stop`、`resume`、`rm` 子命令。
3. `sandbox ls` 使用独立的 `composePSOptions` 实例，挂载 `--all/-a`、`--status`、`--verbose`，RunE 直接调用 `runComposePSCommand`。
4. `sandbox stop`、`sandbox resume` 复用 `sandboxActionArgs` 和 `runComposeSandboxActionCommand`。
5. `sandbox rm` 使用独立的 `composeSandboxRemoveOptions` 实例，挂载 `--force`，RunE 调用 `runComposeSandboxRemoveCommand`。
6. 将 `sandboxCmd` 加入 root command；保留顶层 `ps/stop/resume/rm` 不变。

测试和验证：

- 新增或扩展 CLI integration test，证明 `sandbox ls --json` 与 `ps --json` 的 project/sandbox 字段等价。
- 新增测试证明 `sandbox rm --force <id>` 调用 `RemoveSandbox` 且 `force=true`。
- 局部运行：

```bash
go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox)' -count=1
```

验收标准：

- `agent-compose sandbox --help` 显示 `ls`、`stop`、`resume`、`rm`、`prune` 以外的新增项不出现。
- 顶层 `ps/stop/resume/rm` 测试继续通过。
- 不修改 proto、generated code、daemon API 或 runtime driver。

适用 harness：

- `AGENTS.md` 要求 CLI 入口集中在 `cmd/agent-compose/main.go`。
- `TESTING.md` 要求用户可见 CLI 工作流优先用 integration tests 覆盖。

## 阶段 2：实现 `sandbox prune` 过滤和 dry-run 模型

目标：实现 `sandbox prune` 的候选选择、过滤、安全校验和 dry-run JSON/text 输出，但暂不执行删除路径以外的新后端能力。

依赖：

- 阶段 1 的 `sandboxCmd`。
- 现有 `composePSOutputFromProject(ctx, clients, project, composePSOptions{All: true})`。
- 现有 duration 解析逻辑 `parseCacheOlderThanSeconds`。

实施工作：

1. 新增 `composeSandboxPruneOptions`，字段包括 `Status`、`Agent`、`Driver`、`OlderThan`、`Force`。
2. 新增 `composeSandboxPruneOutput`，字段包括 `DryRun`、`Matched`、`Removed`、`Skipped`、`Warnings`。
3. 新增 `composeSandboxPruneSkipped`，字段包括 `Sandbox` 和 `Reason`。
4. 在 `sandbox prune` 上挂载 `--status`、`--agent`、`--driver`、`--older-than`、`--force`。
5. 新增 `runComposeSandboxPruneCommand`：
   - 解析当前 compose project。
   - 获取当前 project 下全部 sandbox。
   - 默认匹配 `stopped,failed`。
   - `--status` 覆盖默认状态，按逗号分隔、小写比较。
   - `--agent`、`--driver` 使用 trim 后大小写不敏感精确匹配。
   - `--older-than` 使用 `updated_at`，缺失时回退 `created_at`。
6. 新增通用 duration 解析 helper，复用或重命名 `parseCacheOlderThanSeconds`，保证 `7d`、`168h`、正数校验和错误消息风格保持一致。
7. 如果 `--status` 包含 `running` 或 `pending`，返回 `exitCodeUsage` 的 `commandExitError`，提示使用 `sandbox rm --force <sandbox>` 处理运行中 sandbox。
8. dry-run 下不调用 `RemoveSandbox`。

测试和验证：

- `sandbox prune` 默认 dry-run 只匹配 stopped/failed。
- `sandbox prune --status error` 只匹配 error。
- `sandbox prune --agent worker`、`--driver microsandbox` 过滤正确。
- `sandbox prune --older-than 24h` 使用 `updated_at`，缺失时使用 `created_at`。
- `sandbox prune --status running` 和 `--status pending` 返回 usage error。

验收标准：

- dry-run JSON 包含 `dry_run=true`、`matched`、空 `removed`、`skipped`、`warnings`。
- dry-run 不触发 sandbox remove stub。
- 时间无法解析的 sandbox 不进入 matched，并出现在 warnings 中。

适用 harness：

- `TESTING.md` 要求覆盖 validation、serialization、error paths。
- `Taskfile.yml` 中局部 Go 测试可用 `go test ./cmd/agent-compose ...`。

## 阶段 3：实现 forced prune 删除和失败语义

目标：`sandbox prune --force` 按 dry-run 相同匹配结果逐个删除 sandbox，并正确表达 partial failure。

依赖：

- 阶段 2 的候选选择和输出模型。
- 现有 `removeSandbox(ctx, clients.sandbox, sandboxID, force)`。

实施工作：

1. 在 `runComposeSandboxPruneCommand` 中，当 `options.Force` 为 true 时遍历 matched sandbox。
2. 对每个 sandbox 调用 `removeSandbox(ctx, clients.sandbox, sandbox.Sandbox, false)`。
3. 删除成功时将 sandbox id 加入 `Removed`。
4. 删除失败时将 `{sandbox, reason}` 加入 `Skipped`，继续处理后续 sandbox。
5. forced prune 完成后，如果 `Skipped` 非空，先输出结果，再返回非零 `commandExitError`。
6. 确保 prune 删除路径永远不传 `force=true`，避免批量强删运行中 sandbox。

测试和验证：

- `sandbox prune --force` 对 matched sandbox 逐个调用 `RemoveSandbox(force=false)`。
- forced prune 中一个 sandbox 删除失败时，后续 sandbox 仍继续删除。
- forced prune 有 skipped 时 stdout 包含 removed/skipped 信息，命令 exit code 非零。
- forced prune 全部成功时 exit code 为 0，removed 列表完整。

验收标准：

- 删除顺序与 matched 顺序一致，便于用户对照 dry-run。
- partial failure 不吞错误，skipped reason 能说明失败来源。
- 未匹配项、foreign project、running/pending 不会被删除。

适用 harness：

- `AGENTS.md` 要求不直接删除用户未要求的文件；本阶段只通过现有 `SandboxService.RemoveSandbox` 执行删除。
- `TESTING.md` 要求跨 service boundary 的用户工作流用 integration tests 证明。

## 阶段 4：文本输出、JSON 输出和 CLI 手册更新

目标：补齐用户可见输出和文档，使 `sandbox` 命令组与既有 CLI 手册一致。

依赖：

- 阶段 1-3 的命令行为。
- 现有 `writePSText`、`writeStringListSection`、`writeCacheOperationOutput` 风格。
- `docs/command-line-manual.md` 和 `docs/zh-CN/command-line-manual.md`。

实施工作：

1. 新增 `writeSandboxPruneOutput`，支持 text 和 JSON。
2. 文本 dry-run 输出包含 matched 数、skipped 数、would remove 数，并提示使用 `--force` 实际删除。
3. 文本 forced 输出包含 removed 数、matched 数、skipped 数。
4. matched 表格至少展示 `SANDBOX`、`AGENT`、`STATUS`、`DRIVER`、`UPDATED`、`REASON`。
5. JSON 输出严格使用 spec 中定义的字段：`dry_run`、`matched`、`removed`、`skipped`、`warnings`。
6. 更新英文和中文命令行手册，新增 `sandbox` 命令组和 `sandbox prune` 示例。
7. 文档明确区分 `sandbox prune` 和 `cache prune`。

测试和验证：

- 文本输出测试覆盖 dry-run 和 forced prune 的关键字。
- JSON 输出测试解码为 `composeSandboxPruneOutput` 并验证字段。
- 手册更新不需要生成命令，但要保持中英文语义一致。

验收标准：

- `--json` stdout 是合法 JSON，stderr 不包含普通提示。
- 文本输出能直接告诉用户当前是 dry-run 还是实际删除。
- 手册中不暗示 `sandbox prune` 会清理 runtime cache 文件。

适用 harness：

- `docs/zh-CN/design/agent-compose-cli-improvement-plan.md` 中 CLI 对外使用 sandbox 术语，文档必须与该术语一致。

## 阶段 5：完整验证和停止条件

目标：完成局部和项目级质量门禁，确认无 proto/API 范围外变更。

依赖：

- 阶段 1-4 全部完成。

实施工作：

1. 检查 `git diff`，确认未修改 `proto/`、generated Connect 文件、runtime driver、compose deployment 配置。
2. 运行 focused CLI tests：

```bash
go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox)' -count=1
```

3. 运行相关 package tests：

```bash
go test ./cmd/agent-compose ./pkg/agentcompose/api ./pkg/storage/sessionstore -count=1
```

4. 在提交或合并前运行 harness 门禁：

```bash
task lint
task test
task build
```

测试和验证：

- Focused tests 证明 CLI 行为。
- `task lint` 证明格式和静态检查。
- `task test` 证明 coverage quality gate。
- `task build` 证明主二进制和 runtime SDK build gate。

验收标准：

- 所有新增测试通过。
- 顶层兼容命令 `ps/stop/resume/rm` 行为不变。
- `sandbox prune` 不修改 proto/RPC 和 package 客户端。
- 如果 `task test` 或 `task build` 因环境依赖失败，必须记录具体失败命令、失败阶段和可复现错误。

停止条件：

- 发现首版必须修改 proto/RPC 才能满足 spec 时停止，并回到 spec 更新。
- 发现 `sandbox prune` 可能删除 running/pending sandbox 时停止，不允许用 `force=true` 绕过。
- 发现当前 project 归属判断会误包含 foreign project 时停止，先修复候选选择或收缩 prune 范围。

## 首版不做的事项

- 不实现 `SandboxService.PruneSandboxes`。
- 不修改 `ListRunsResponse` 的分页字段。
- 不把顶层 `ps/stop/resume/rm` 标记为 deprecated。
- 不改变 `cache prune` 行为。
- 不提供 daemon 全局 sandbox prune。
- 不新增环境变量、配置项、部署 compose 设置或 image build 行为。
- 不直接删除 `SESSION_ROOT`、`DATA_ROOT` 或 runtime driver 私有目录。
