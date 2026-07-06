# agent-compose sandbox prune 技术方案

## 背景与目标

`agent-compose ps -a` 当前可以列出当前 project 下的大量 stopped/failed sandbox，但清理这些 sandbox 只能手工拼接 `ps | awk | rm`。这与 CLI 已经建立的 sandbox 领域概念不一致，也让用户误以为需要直接清理 runtime cache 或数据目录。

目标是新增 `agent-compose sandbox` 命令组，并提供 `sandbox prune` 批量清理入口：

- 用户可以从 `sandbox` 语义入口完成 list/stop/resume/rm/prune。
- `sandbox prune` 默认 dry-run，只清理当前 compose project 下可识别的非运行 sandbox。
- 不改变现有顶层 `ps`、`stop`、`resume`、`rm` 行为。
- 首版不修改 proto/RPC，不处理 `ListRunsResponse` 分页契约。

## 现状和 harness 约束

`AGENTS.md` 约束：

- CLI 入口在 `cmd/agent-compose/main.go`。
- daemon 服务图和路由在 `pkg/agentcompose/app/`。
- Connect API 映射在 `pkg/agentcompose/api/`。
- session metadata、runtime state、proxy state 存在 `SESSION_ROOT`，由 `pkg/storage/sessionstore` 管理。
- runtime drivers 包括 `docker`、`boxlite`、`microsandbox`。
- 质量门禁为 `task lint`、`task build`、`task test`。

`TESTING.md` 约束：

- 新功能应按风险选择 unit/integration/e2e 测试形态。
- 用户可见 CLI 工作流应优先覆盖 integration tests。
- `task test` 是测试质量门禁，要求输出并检查 unit/integration/e2e/combined coverage。

`Taskfile.yml` 约束：

- Go lint 范围为 `./cmd/... ./pkg/... ./proto/...`。
- 主测试命令为 `task test`，局部验证可用 `go test ./cmd/agent-compose ...`。
- 本次不改 proto，因此不需要新增 proto codegen 或 proto-client 发布流程。

现有设计文档约束：

- `docs/zh-CN/design/agent-compose-cli-improvement-plan.md` 已把 sandbox 定义为对外统一运行态隔离环境，内部继续复用 session store、runtime state 和 proxy state。
- 当前 `SandboxService` 只有 `RemoveSandbox` 和 `GetSandboxStats`。
- `cache prune` 负责 runtime cache inventory，不负责删除 `ps -a` 中的 sandbox/session 记录。
- `rm` 删除 running sandbox 时需要 `--force`，强制删除会先 stop 再 remove。

## 核心概念

- sandbox：CLI 对外统一概念，当前 ID 与 session id 兼容。
- session：daemon 内部持久化对象，保存 sandbox metadata、workspace、runtime state、proxy state。
- sandbox prune：面向 `ps -a` 结果的 session/sandbox 记录清理，不是 cache 文件清理。
- cache prune：面向 daemon runtime cache inventory 的清理，继续由 `CacheService` 管理。

## 架构和组件边界

CLI 负责：

- 注册 `agent-compose sandbox` 命令组。
- 复用当前 project 解析、`ps` 聚合、sandbox remove 客户端逻辑。
- 对 `sandbox prune` 执行 status/agent/driver/older-than 过滤。
- 输出 dry-run、removed、skipped、warnings 的文本和 JSON 结果。

daemon 负责：

- 继续通过现有 v1 `SessionService.ListSessions`、v2 `RunService.ListRuns`、v2 `ProjectService.GetProject` 提供数据。
- 继续通过 v2 `SandboxService.RemoveSandbox` 执行单个 sandbox 删除。
- 删除 running sandbox 的保护语义保持在 `RemoveSandbox` 中。

不新增后端批量 prune RPC。这样首版避免 proto、generated Go、proto-client 和 UI 消费方变更。

## API、CLI、配置和数据模型变化

新增命令组：

```bash
agent-compose sandbox ls
agent-compose sandbox stop <sandbox...>
agent-compose sandbox resume <sandbox...>
agent-compose sandbox rm <sandbox...>
agent-compose sandbox prune
```

兼容入口保留：

```bash
agent-compose ps
agent-compose stop <sandbox...>
agent-compose resume <sandbox...>
agent-compose rm <sandbox...>
```

子命令语义：

- `sandbox ls` 等价于 `ps`，支持 `--all/-a`、`--status`、`--verbose`、`--json`。
- `sandbox stop/resume/rm` 复用现有顶层命令实现。
- `sandbox rm --force` 继续允许删除 running sandbox。
- `sandbox prune` 默认 dry-run；只有 `--force` 才实际删除。

`agent-compose sandbox prune` 参数：

```bash
--status <status>[,<status>...]
--agent <agent>
--driver <docker|boxlite|microsandbox>
--older-than <duration>
--force
--json
```

默认匹配状态为 `stopped,failed`。`--status` 会覆盖默认状态。

JSON 输出形态：

```json
{
  "dry_run": true,
  "matched": [],
  "removed": [],
  "skipped": [
    {"sandbox": "sandbox-id", "reason": "remove failed: ..."}
  ],
  "warnings": []
}
```

## 工作流和失败语义

`sandbox prune` 工作流：

1. CLI 解析当前 compose project。
2. 调用现有 `composePSOutputFromProject(..., composePSOptions{All: true})` 获取当前 project 的 sandbox 列表。
3. CLI 应用 `status`、`agent`、`driver`、`older-than` 过滤。
4. 无 `--force` 时只输出 dry-run 结果。
5. 有 `--force` 时逐个调用 `RemoveSandbox(force=false)`。
6. 单个删除失败时记录 skipped 并继续处理后续 sandbox。
7. 如果 forced prune 存在 skipped，命令输出结果后返回非零。

安全语义：

- `sandbox prune` 不允许匹配 `running` 或 `pending`；如果用户通过 `--status` 指定这些状态，返回 usage error。
- 实际删除时不向 `RemoveSandbox` 传 `force=true`，避免 prune 批量强删运行中 sandbox。
- `older-than` 使用 `updated_at` 判断；缺失时回退 `created_at`。时间无法解析的项跳过并输出 warning。
- `sandbox prune` 不删除 project 配置，不直接删除 `SESSION_ROOT` 文件，不直接清理 runtime cache。
- runtime cache 残留仍由 `agent-compose cache prune` 或 `cache rm` 显式处理。

## 测试、质量门禁和验收标准

必须新增 CLI integration tests，放在 `cmd/agent-compose/main_test.go` 附近现有 CLI 测试区域：

- `sandbox ls --json` 与 `ps --json` 行为等价。
- `sandbox rm --force <id>` 调用 `RemoveSandbox(force=true)`。
- `sandbox prune` 默认 dry-run，只匹配 stopped/failed，跳过 running 和 foreign project。
- `sandbox prune --force` 对 matched sandbox 逐个调用 `RemoveSandbox(force=false)`。
- `sandbox prune --agent`、`--driver`、`--status`、`--older-than` 过滤正确。
- `sandbox prune --status running` 和 `--status pending` 返回 usage error。
- forced prune 中单个删除失败时继续处理后续项，输出 skipped，最终返回非零。
- JSON 输出字段稳定，不向 stderr 写非 warning/deprecated 内容。

局部验证命令：

```bash
go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox)' -count=1
```

最终质量门禁：

```bash
task lint
task test
task build
```

## 首版不做事项

- 不新增 `SandboxService.PruneSandboxes` RPC。
- 不修改 `ListRunsResponse` 分页 wire shape。
- 不改变 `cache prune` 语义。
- 不提供 daemon 全局 sandbox prune。
- 不允许 `sandbox prune` 删除 running/pending sandbox。
- 不新增配置项或环境变量。
- 不直接操作 `SESSION_ROOT`、`DATA_ROOT` 或 runtime driver 私有目录。

## 关键假设和已确认决策

- 文档文件名使用 `docs/spec/sandbox-cli-prune-spec.md`。
- sandbox prune 首版是 CLI 编排能力，不是后端批量 API。
- 顶层 `ps/stop/resume/rm` 保持兼容，不标记 deprecated。
- `sandbox prune` 默认 dry-run，`--force` 才删除。
- 默认清理状态为 `stopped,failed`。
- `ListRunsResponse` 暂时不补 `has_more/next_offset`，沿用现有 CLI 聚合方式。
