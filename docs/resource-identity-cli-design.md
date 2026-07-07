# agent-compose 资源标识与 CLI 展示设计

状态：设计方案与输出对比文档，暂不调整代码。

本文基于当前 CLI 行为整理，示例使用：

- `agent-compose/examples/docker-minimal/agent-compose.yml`
- `agent-compose/examples/docker-scheduler-timeout/agent-compose.yml`

文档中的路径均已脱敏为项目相对路径；示例中不包含密码、密钥等敏感信息。

## 结论摘要

当前 agent-compose 已经有一部分资源使用“可读前缀 + hash”的 ID，例如 `project-docker-minimal-55521f60a3e9`、`agent-reviewer-6a3d03099bc3`。这对机器唯一性有帮助，但作为 CLI 展示和用户复制输入不够简洁，也和 Docker Compose 的使用体验不完全一致。

建议采用统一模型：

1. 每个资源都有唯一 ID。
2. CLI 默认展示 `hash12`，也就是 ID 中最短稳定可用的 12 位识别片段。
3. 用户预定义的资源优先展示用户定义的名字，例如 project name、agent name、声明式 trigger name。
4. 运行时产生的资源优先展示短 ID，并在相邻列补充 project、agent、status 等上下文。
5. 默认 CLI 输出偏紧凑；`--verbose` 和 `--json` 输出更完整的信息。
6. JSON 输出保留完整字段和完整 hash，例如 `sha256:...` 不截断。
7. Image 完全遵循 Docker/OCI 语义，不再设计 agent-compose 自定义 image ID。
8. CLI 参数解析统一支持：完整 ID、短 ID 前缀、预定义 name。

核心变化不是“删除旧字段”，而是在展示层和解析层补齐一致体验。除非字段本身确实是内部实现泄漏，否则尽量保留现有字段以兼容历史用户和客户端。

## 设计目标

### 用户体验目标

用户在 CLI 中应该能形成连续操作：

```bash
agent-compose ls
agent-compose up
agent-compose run reviewer --command 'echo ok' --keep-running
agent-compose ps
agent-compose logs --run 103f88fea811
agent-compose inspect run 103f88fea811
agent-compose inspect sandbox c5582b466ada
agent-compose exec c5582b466ada pwd
```

也就是说，一个命令展示出来的 `ID`、`NAME`，应该可以直接作为后续命令参数。

### 产品目标

1. 对用户可见的概念尽量少：Project、Agent、Scheduler/Trigger、Run、Sandbox、Image、Cache。
2. 对用户预定义的资源，优先使用名字。
3. 对运行时产生的资源，优先使用短 ID。
4. 对系统内部概念，例如 Session，不作为一等 CLI 资源暴露。
5. 保持 Docker/Compose 心智：短 ID 可操作，完整 ID 可追溯。

## 资源分类

### 预定义资源

预定义资源来自 `agent-compose.yml` 或 CLI 参数。它们有用户显式定义的名字，应优先展示名字。

| 资源 | 来源 | 默认展示 | 唯一身份 | CLI 参数 |
| --- | --- | --- | --- | --- |
| Project | `name:` 或 `--project-name` | project name | project ID | name、完整 ID、短 ID |
| Agent | `agents.<name>` | agent name | agent ID | project 内的 agent name、完整 ID、短 ID |
| Scheduler | project 下的调度器资源 | 不单独起业务名 | scheduler ID | 通常不直接作为默认输入；verbose/json 可展示完整 ID |
| Trigger | scheduler 下的触发器 | 声明式 trigger name；脚本注册 trigger 用内部 ID 或序号 | trigger ID | trigger name、完整 ID、短 ID |

示例：

```yaml
name: docker-minimal

agents:
  reviewer:
    provider: codex
    image: agent-compose-guest:latest
    driver:
      docker: {}
```

这里 `docker-minimal` 和 `reviewer` 是用户定义的名字，CLI 中应优先展示和支持输入。

Scheduler/Trigger 的关系需要单独说明：

1. 当前项目维度下只有一个 scheduler 资源，但可以有多个 trigger。
2. Trigger 有两种来源：`agent-compose.yml` 中声明式配置的 trigger，通常带 `name`；loader script 运行时注册的 trigger，通常没有用户定义 name，只能使用内部 trigger ID 或序号。
3. 因此 `agent-compose scheduler ls` 的默认关注点应是 trigger，而不是 scheduler 本身。
4. Scheduler ID 仍然存在，用于机器唯一性和排障，但默认文本输出不需要展示；`--verbose` 或 `--json` 可以展示。

### 运行时资源

运行时资源由执行过程产生，通常没有用户预定义名字，因此短 ID 是最合适的默认展示。

| 资源 | 来源 | 默认展示 | 唯一身份 | CLI 参数 |
| --- | --- | --- | --- | --- |
| Run | `run`、scheduler、API 调用 | 短 run ID + project/agent/status | run ID | 完整 ID、短 ID |
| Sandbox | runtime/session/container 实例 | 短 sandbox ID + project/agent/status | sandbox ID | 完整 ID、短 ID |
| Cache | runtime cache 扫描 | 短 cache ID + type/ref/path | cache ID | 完整 ID、短 ID |

资源 ID 不建议把上下文全部拼进展示值，例如：

```text
trigger-docker-scheduler-timeout-reviewer-run-once-after-15-seconds-xxxxxxxxxxxx
```

这种值在展示和复制时都太长。上下文应该通过表格列呈现，而不是塞进 ID。

### 内部资源

| 资源 | 当前角色 | 建议 CLI 角色 |
| --- | --- | --- |
| Session | notebook/runtime 持久化状态 | 内部实现与兼容字段 |

当前 `inspect sandbox` 仍输出 `session_id`，这会让用户困惑。建议新增并优先展示 `sandbox_id`，保留 `session_id` 作为兼容字段。

### Docker/OCI 资源

| 资源 | 身份模型 |
| --- | --- |
| Image | Docker/OCI 的 `image_id`、digest、tag、repo digest |

Image 不需要 agent-compose 自定义命名。表格里展示 Docker 风格短 ID，JSON 中保留完整 `sha256:...`。

## ID 与展示规则

每个资源建议具备以下字段：

| 字段 | 说明 |
| --- | --- |
| `id` | 完整唯一 ID，机器使用 |
| `short_id` | 默认展示 ID，通常为 12 位 |
| `name` | 人类友好名，仅在确实有意义时提供 |

展示规则：

1. 表格默认偏紧凑，展示常用上下文和可复制的 `short_id`。
2. 表格中的 `ID` 列展示短 ID，不展示超长 hash。
3. `--verbose` 可以增加完整 ID、完整 `sha256`、完整路径等列。
4. `--json` 保持完整字段，不截断 hash。
5. `sha256:...` 在摘要区和 JSON 中保持完整语义，不改写成裸 hash。

示例：

```text
# 文本表格
PROJECT ID    NAME
55521f60a3e9  docker-minimal

# JSON
{
  "id": "project-docker-minimal-55521f60a3e9",
  "short_id": "55521f60a3e9",
  "name": "docker-minimal",
  "spec_hash": "sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd"
}
```

## CLI 参数解析规则

每个命令只在自己的资源类型内解析目标。例如 `inspect run` 只解析 Run，`inspect sandbox` 只解析 Sandbox。

命令作用域也需要明确：

| 命令 | 作用域 | 默认展示重点 |
| --- | --- | --- |
| `agent-compose ls` | 全局 daemon | project name、project short ID、config file |
| `agent-compose up/down` | 当前 compose project | project、agent、scheduler/trigger 变更 |
| `agent-compose inspect project` | 当前 project 或指定 project | project 完整信息 |
| `agent-compose inspect agent` | 当前 project | agent 信息 |
| `agent-compose scheduler ls` | 当前 project | trigger 列表 |
| `agent-compose scheduler inspect` | 当前 project 的某个 trigger | trigger 配置 |
| `agent-compose run` | 当前 project 的某个 agent | agent 执行结果 |
| `agent-compose ps/logs` | 当前 project，除非显式全局选项 | sandbox/run 列表或日志 |
| `agent-compose images/cache` | daemon 级资源 | Docker/OCI image 或 runtime cache |

解析顺序：

1. 完整 ID 精确匹配。
2. ID 前缀唯一匹配。
3. 有名字的资源按 name 匹配。
4. 在 project 上下文中优先解析当前 project 内的资源。
5. 匹配多个候选时返回 ambiguous，并列出候选项。

示例：

```bash
# project 可用 name 或短 ID
agent-compose inspect project docker-minimal
agent-compose inspect project 55521f60a3e9

# agent 在 project 上下文中优先使用 name
agent-compose run reviewer
agent-compose inspect agent reviewer

# run/sandbox 使用短 ID
agent-compose inspect run 103f88fea811
agent-compose inspect sandbox c5582b466ada
agent-compose exec c5582b466ada pwd
```

## 字段兼容策略

原则：尽量不减少现有字段，优先新增更清晰字段。

| 当前字段 | 建议 | 说明 |
| --- | --- | --- |
| `project.id` | 保留，新增 `project.short_id` | JSON 保留完整 ID |
| `managed_agent_id` | 保留，新增 `agent.id`、`agent.short_id`、`agent.name` | `managed_` 更像内部存储概念，不建议作为主展示 |
| `scheduler_id` | 保留，新增 `scheduler.short_id` | 默认文本输出不展示；verbose/json 展示 |
| `trigger_id` | 保留，新增 `trigger.short_id` | `scheduler ls` 默认重点展示 trigger |
| `run_id` | 保留，新增 `run.short_id` 或顶层 `short_id` | 运行时操作主入口 |
| `session_id` | 保留，新增 `sandbox_id`、`sandbox.short_id` | `session_id` 作为兼容字段 |
| `cache_id` | 保留，新增 `cache.short_id` | cache 操作支持短 ID |
| `image_id` | 保持不变 | Docker/OCI 语义 |
| `spec_hash` | 保持完整 `sha256:...` | 文本摘要和 JSON 不改语义；表格 ID 列可展示短 hash |

## 操作流程与输出对比

下面按照典型使用流程组织：查看项目、应用项目、查看项目详情、运行任务、查看 Run/Sandbox、日志、镜像和缓存。

所有“当前输出”均来自当前 CLI 行为，已对本机路径做脱敏处理。

### 1. 查看项目列表：`agent-compose ls`

当前输出：

```text
PROJECT                   CONFIG FILE                                         REVISION  AGENTS  SCHEDULERS  SERVICES
docker-minimal            agent-compose/examples/docker-minimal/agent-compose.yml            1  0  0  -
docker-scheduler-timeout  agent-compose/examples/docker-scheduler-timeout/agent-compose.yml  1  0  0  -
```

建议输出：

```text
PROJECT ID    NAME                      CONFIG FILE                                         REVISION  AGENTS  SCHEDULERS
55521f60a3e9  docker-minimal            agent-compose/examples/docker-minimal/agent-compose.yml            1  1  0
c71c6cd85296  docker-scheduler-timeout  agent-compose/examples/docker-scheduler-timeout/agent-compose.yml  1  1  1
```

说明：

- 默认列表应展示可直接复制使用的短 ID。
- `NAME` 展示用户定义的 project name。
- 参考 Docker Compose 的使用习惯，默认保留 config file path，方便用户判断 project 来源。
- 当前示例中 `ls` 显示 `AGENTS=0`，但 `inspect project` 显示 agent_count 为 1。这是独立的统计一致性问题，不属于命名设计本身。

### 2. 查看详细项目列表：`agent-compose ls --verbose`

当前输出：

```text
PROJECT                   CONFIG FILE                                         REVISION  AGENTS  SCHEDULERS  SERVICES  PROJECT ID                                     PROJECT DIR                              SPEC HASH                                                                UPDATED               STATUS
docker-minimal            agent-compose/examples/docker-minimal/agent-compose.yml            1  0  0  -  project-docker-minimal-55521f60a3e9            agent-compose/examples/docker-minimal            sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd  2026-07-07T08:33:40Z  active
docker-scheduler-timeout  agent-compose/examples/docker-scheduler-timeout/agent-compose.yml  1  0  0  -  project-docker-scheduler-timeout-c71c6cd85296  agent-compose/examples/docker-scheduler-timeout  sha256:283623fe82f0f04270f27a0ec9da4809fc45b4a45c3f15df3f688aba074990b2  2026-07-07T08:33:40Z  active
```

建议输出：

```text
PROJECT ID    NAME                      REVISION  AGENTS  SCHEDULERS  SERVICES  STATUS  SPEC HASH                                                               CONFIG
55521f60a3e9  docker-minimal            1         1       0           -         active  sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd  agent-compose/examples/docker-minimal/agent-compose.yml
c71c6cd85296  docker-scheduler-timeout  1         1       1           -         active  sha256:283623fe82f0f04270f27a0ec9da4809fc45b4a45c3f15df3f688aba074990b2  agent-compose/examples/docker-scheduler-timeout/agent-compose.yml
```

说明：

- 默认 `ls` 保持短表格。
- `--verbose` 可以显示完整 `sha256`，因为这是用户主动要求更多信息。
- `PROJECT ID` 列仍然用短 ID，避免表格过宽。

### 3. 查看配置：`agent-compose config`

当前输出：

```yaml
name: docker-minimal
agents:
    - name: reviewer
      provider: codex
      image: agent-compose-guest:latest
      driver:
        name: docker
        docker: {}
network:
    mode: default
```

建议输出：保持不变。

说明：

- `config` 展示的是用户声明配置。
- 不应混入运行时 ID。

### 4. 应用项目：`agent-compose up`

当前输出：

```text
Project: docker-minimal
ID: project-docker-minimal-55521f60a3e9
Revision: 1
Spec: sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd
Status: applied
Agents: 1
Schedulers: 0

ACTION   TYPE              NAME                                                                     ID
created  project           docker-minimal                                                           project-docker-minimal-55521f60a3e9
created  project_revision  sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd  project-docker-minimal-55521f60a3e9/1
created  project_agent     reviewer                                                                 agent-reviewer-6a3d03099bc3
created  agent_definition  reviewer                                                                 agent-reviewer-6a3d03099bc3
```

建议输出：

```text
Project: docker-minimal
ID: 55521f60a3e9
Revision: 1
Spec: sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd
Status: applied
Agents: 1
Schedulers: 0

ACTION   TYPE              NAME            ID
created  project           docker-minimal  55521f60a3e9
created  project_revision  revision 1      0e351a523ae7
created  agent             reviewer        6a3d03099bc3
```

说明：

- 顶部摘要中的 `Spec` 保持完整 `sha256:...`，不破坏语义。
- 表格 `ID` 列展示短 ID。
- `project_agent` 和 `agent_definition` 对用户来说都是 agent 的创建结果，建议在文本输出中合并为 `agent`，JSON 中仍可保留更细字段。

### 5. 查看项目详情：`agent-compose inspect project`

当前输出：

```json
{
  "project": {
    "id": "project-docker-minimal-55521f60a3e9",
    "name": "docker-minimal",
    "source_path": "agent-compose/examples/docker-minimal/agent-compose.yml",
    "current_revision": 1,
    "spec_hash": "sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd",
    "agent_count": 1,
    "scheduler_count": 0
  },
  "agents": [
    {
      "agent_name": "reviewer",
      "managed_agent_id": "agent-reviewer-6a3d03099bc3",
      "provider": "codex",
      "image": "agent-compose-guest:latest",
      "driver": "docker",
      "scheduler_enabled": false
    }
  ],
  "schedulers": null
}
```

建议输出：

```json
{
  "project": {
    "id": "project-docker-minimal-55521f60a3e9",
    "short_id": "55521f60a3e9",
    "name": "docker-minimal",
    "source_path": "agent-compose/examples/docker-minimal/agent-compose.yml",
    "current_revision": 1,
    "spec_hash": "sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd",
    "agent_count": 1,
    "scheduler_count": 0
  },
  "agents": [
    {
      "agent_name": "reviewer",
      "managed_agent_id": "agent-reviewer-6a3d03099bc3",
      "agent_id": "agent-reviewer-6a3d03099bc3",
      "short_id": "6a3d03099bc3",
      "provider": "codex",
      "image": "agent-compose-guest:latest",
      "driver": "docker",
      "scheduler_enabled": false
    }
  ],
  "schedulers": []
}
```

说明：

- JSON 保留 `managed_agent_id`，新增更产品化的 `agent_id` 和 `short_id`。
- `schedulers` 建议输出空数组而不是 `null`，便于客户端处理。

### 6. 查看 Agent：`agent-compose inspect agent reviewer`

当前输出：

```json
{
  "project": {
    "id": "project-docker-minimal-55521f60a3e9",
    "name": "docker-minimal",
    "source_path": "agent-compose/examples/docker-minimal/agent-compose.yml",
    "current_revision": 1,
    "spec_hash": "sha256:0e351a523ae793f780fc0933f3b88920501f20dfd4d855654fe711a8a3cb4edd",
    "agent_count": 1,
    "scheduler_count": 0
  },
  "agent": {
    "agent_name": "reviewer",
    "managed_agent_id": "agent-reviewer-6a3d03099bc3",
    "provider": "codex",
    "image": "agent-compose-guest:latest",
    "driver": "docker",
    "scheduler_enabled": false
  },
  "schedulers": null
}
```

建议输出：

```json
{
  "project": {
    "id": "project-docker-minimal-55521f60a3e9",
    "short_id": "55521f60a3e9",
    "name": "docker-minimal"
  },
  "agent": {
    "agent_name": "reviewer",
    "managed_agent_id": "agent-reviewer-6a3d03099bc3",
    "agent_id": "agent-reviewer-6a3d03099bc3",
    "short_id": "6a3d03099bc3",
    "provider": "codex",
    "image": "agent-compose-guest:latest",
    "driver": "docker",
    "scheduler_enabled": false
  },
  "schedulers": []
}
```

### 7. 查看 Trigger 列表：`agent-compose scheduler ls`

作用域：当前 project。

说明：当前 project 只有一个 scheduler 资源，但可以有多个 trigger。因此这个命令默认展示 trigger 列表，不需要默认展示 scheduler ID。

当前输出：

```text
AGENT     TRIGGER                    KIND     SOURCE       SCHEDULER                                ENABLED
reviewer  run-once-after-15-seconds  timeout  declarative  scheduler-reviewer-default-cd228d46fd7d  true
```

建议输出：

```text
TRIGGER ID    AGENT     TRIGGER                    KIND     SOURCE       ENABLED
<trigger-12>  reviewer  run-once-after-15-seconds  timeout  declarative  true
```

如果声明式 trigger 暂时没有独立 trigger ID，也可以先展示 trigger name，等内部 trigger ID 可稳定获取后补齐：

```text
AGENT     TRIGGER                    KIND     SOURCE       ENABLED
reviewer  run-once-after-15-seconds  timeout  declarative  true
```

`--verbose` 或 `--json` 可以展示 scheduler ID：

```text
TRIGGER ID    AGENT     TRIGGER                    KIND     SOURCE       SCHEDULER ID   ENABLED
<trigger-12>  reviewer  run-once-after-15-seconds  timeout  declarative  cd228d46fd7d  true
```

### 8. 查看 Trigger 配置：`agent-compose scheduler inspect reviewer run-once-after-15-seconds`

作用域：当前 project 的当前 scheduler。

当前输出：

```yaml
name: run-once-after-15-seconds
prompt: 'Reply with exactly: timeout scheduler ok'
timeout: 15s
```

建议输出：保持不变。

说明：

- 这是用户定义的 trigger 配置。
- YAML 输出以声明内容为主。
- 如需 trigger ID、scheduler ID，应在 `--json` 或 verbose 中补充，而不是污染 YAML 主体。

### 9. 运行 Agent：`agent-compose run reviewer --command ... --keep-running`

当前输出：

```text
keep-sandbox
```

建议输出：保持不变。

说明：

- 前台运行应忠实输出命令 stdout/stderr。
- Run ID 和 Sandbox ID 通过 `ps`、`logs --json`、`inspect` 获取。

如果是 detached 模式，建议输出短 ID：

```text
Run: 103f88fea811
Sandbox: c5582b466ada
Status: running
Logs: agent-compose logs --run 103f88fea811 --follow
```

### 10. 查看 Sandbox：`agent-compose ps --all --verbose`

当前输出：

```text
SANDBOX                               AGENT     STATUS   RUN                        CREATED                        UPDATED                         DRIVER  IMAGE                       WORKSPACE
c5582b46-6ada-4288-9664-19bdb8788a68  reviewer  running  run-reviewer-103f88fea811  2026-07-07T08:34:44.73204762Z  2026-07-07T08:34:45.954019094Z  docker  agent-compose-guest:latest  agent-compose/data/sessions/c5582b46-6ada-4288-9664-19bdb8788a68/workspace
```

建议默认输出：

```text
SANDBOX ID    PROJECT         AGENT     STATUS   RUN ID        DRIVER  IMAGE
c5582b466ada  docker-minimal  reviewer  running  103f88fea811  docker  agent-compose-guest:latest
```

建议 verbose 输出：

```text
SANDBOX ID    PROJECT         AGENT     STATUS   RUN ID        DRIVER  IMAGE                       CREATED               WORKSPACE
c5582b466ada  docker-minimal  reviewer  running  103f88fea811  docker  agent-compose-guest:latest  2026-07-07T08:34:44Z  agent-compose/data/sessions/c5582b46-6ada-4288-9664-19bdb8788a68/workspace
```

说明：

- 默认不要展示完整 UUID，表格会过宽。
- `PROJECT` 和 `AGENT` 提供上下文。
- `RUN ID` 展示可直接用于 `logs --run` 和 `inspect run` 的短 ID。

### 11. 查看日志：`agent-compose logs reviewer`

当前输出：

```text
reviewer-run-revi | keep-sandbox
reviewer-run-revi | naming-ok
```

建议输出：

```text
reviewer 103f88fea811 | keep-sandbox
reviewer a06f0f90b83b | naming-ok
```

说明：

- 当前 `reviewer-run-revi` 信息不够明确，也不方便复制操作。
- 新输出中 `103f88fea811` 可直接用于后续命令。

```bash
agent-compose inspect run 103f88fea811
agent-compose logs --run 103f88fea811
```

### 12. 查看 JSON 日志：`agent-compose logs --json reviewer`

当前输出节选：

```json
{
  "runs": [
    {
      "run_id": "run-reviewer-103f88fea811",
      "project_id": "project-docker-minimal-55521f60a3e9",
      "project_name": "docker-minimal",
      "agent_name": "reviewer",
      "source": "manual",
      "status": "succeeded",
      "session_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
      "exit_code": 0,
      "output": "keep-sandbox\n",
      "driver": "docker",
      "image_ref": "agent-compose-guest:latest"
    }
  ]
}
```

建议输出节选：

```json
{
  "runs": [
    {
      "run_id": "run-reviewer-103f88fea811",
      "short_id": "103f88fea811",
      "project_id": "project-docker-minimal-55521f60a3e9",
      "project_short_id": "55521f60a3e9",
      "project_name": "docker-minimal",
      "agent_name": "reviewer",
      "source": "manual",
      "status": "succeeded",
      "sandbox_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
      "sandbox_short_id": "c5582b466ada",
      "session_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
      "exit_code": 0,
      "output": "keep-sandbox\n",
      "driver": "docker",
      "image_ref": "agent-compose-guest:latest"
    }
  ]
}
```

说明：

- 保留 `run_id`、`project_id`、`session_id` 等现有字段。
- 新增 `short_id`、`sandbox_id`、`sandbox_short_id`，便于新 CLI 和客户端使用。

### 13. 查看 Run：`agent-compose inspect run 103f88fea811`

当前命令需要完整 run ID：

```bash
agent-compose inspect run run-reviewer-103f88fea811
```

当前输出：

```json
{
  "run_id": "run-reviewer-103f88fea811",
  "project_id": "project-docker-minimal-55521f60a3e9",
  "project_name": "docker-minimal",
  "agent_name": "reviewer",
  "source": "manual",
  "status": "succeeded",
  "session_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
  "exit_code": 0,
  "duration_ms": 165,
  "output": "keep-sandbox\n",
  "driver": "docker",
  "image_ref": "agent-compose-guest:latest"
}
```

建议支持：

```bash
agent-compose inspect run 103f88fea811
```

建议输出：

```json
{
  "run_id": "run-reviewer-103f88fea811",
  "short_id": "103f88fea811",
  "project_id": "project-docker-minimal-55521f60a3e9",
  "project_short_id": "55521f60a3e9",
  "project_name": "docker-minimal",
  "agent_name": "reviewer",
  "source": "manual",
  "status": "succeeded",
  "sandbox_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
  "sandbox_short_id": "c5582b466ada",
  "session_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
  "exit_code": 0,
  "duration_ms": 165,
  "output": "keep-sandbox\n",
  "driver": "docker",
  "image_ref": "agent-compose-guest:latest"
}
```

### 14. 查看 Sandbox：`agent-compose inspect sandbox c5582b466ada`

当前命令需要完整 session/sandbox UUID：

```bash
agent-compose inspect sandbox c5582b46-6ada-4288-9664-19bdb8788a68
```

当前输出：

```json
{
  "session_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
  "title": "docker-minimal/reviewer run",
  "driver": "docker",
  "vm_status": "running",
  "workspace_path": "agent-compose/data/sessions/c5582b46-6ada-4288-9664-19bdb8788a68/workspace",
  "proxy_path": "/jupyter/c5582b46-6ada-4288-9664-19bdb8788a68/lab",
  "guest_image": "agent-compose-guest:latest",
  "trigger_source": "manual",
  "tags": {
    "agent": "reviewer",
    "project": "project-docker-minimal-55521f60a3e9",
    "run_id": "run-reviewer-103f88fea811",
    "source": "manual"
  }
}
```

建议支持：

```bash
agent-compose inspect sandbox c5582b466ada
agent-compose exec c5582b466ada pwd
agent-compose stop c5582b466ada
```

建议输出：

```json
{
  "sandbox_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
  "sandbox_short_id": "c5582b466ada",
  "session_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
  "title": "docker-minimal/reviewer run",
  "project_id": "project-docker-minimal-55521f60a3e9",
  "project_short_id": "55521f60a3e9",
  "project_name": "docker-minimal",
  "agent_name": "reviewer",
  "run_id": "run-reviewer-103f88fea811",
  "run_short_id": "103f88fea811",
  "driver": "docker",
  "status": "running",
  "workspace_path": "agent-compose/data/sessions/c5582b46-6ada-4288-9664-19bdb8788a68/workspace",
  "proxy_path": "/jupyter/c5582b46-6ada-4288-9664-19bdb8788a68/lab",
  "image_ref": "agent-compose-guest:latest",
  "trigger_source": "manual"
}
```

说明：

- `session_id` 保留，避免破坏已有调用方。
- CLI 主概念改为 `sandbox_id`。

### 15. 查看镜像：`agent-compose images --query agent-compose-guest`

当前输出：

```text
IMAGE ID      REF                         STATUS     SIZE        CREATED
e67e6413b80b  agent-compose-guest:latest  available  3277198228  2026-07-06T08:28:20Z
```

建议输出：保持不变。

说明：

- 这已经符合 Docker 风格。
- `IMAGE ID` 是短 ID。
- Image 继续使用 Docker/OCI 语义。

### 16. 查看镜像详情：`agent-compose inspect image agent-compose-guest:latest`

当前输出：

```json
{
  "image": {
    "image_id": "sha256:e67e6413b80b665a4ca89279a67d709e77ee50640b3d267b568d379d211c9a8b",
    "image_ref": "agent-compose-guest:latest",
    "resolved_ref": "agent-compose-guest:latest",
    "repo_tags": [
      "agent-compose-guest:latest"
    ],
    "store": "docker",
    "availability_status": "available",
    "platform": "linux/arm64",
    "size_bytes": 3277198228,
    "virtual_size_bytes": 3277198228,
    "created_at": "2026-07-06T16:28:20.555628364+08:00",
    "inspected_at": "2026-07-07T08:35:08.08874343Z",
    "dangling": false,
    "container_count": 0
  },
  "store_status": {
    "store": "docker",
    "available": true,
    "endpoint": "unix:///var/run/docker.sock"
  }
}
```

建议输出：

```json
{
  "image": {
    "image_id": "sha256:e67e6413b80b665a4ca89279a67d709e77ee50640b3d267b568d379d211c9a8b",
    "short_id": "e67e6413b80b",
    "image_ref": "agent-compose-guest:latest",
    "resolved_ref": "agent-compose-guest:latest",
    "repo_tags": [
      "agent-compose-guest:latest"
    ],
    "store": "docker",
    "availability_status": "available",
    "platform": "linux/arm64",
    "size_bytes": 3277198228,
    "virtual_size_bytes": 3277198228,
    "created_at": "2026-07-06T16:28:20.555628364+08:00",
    "inspected_at": "2026-07-07T08:35:08.08874343Z",
    "dangling": false,
    "container_count": 0
  },
  "store_status": {
    "store": "docker",
    "available": true,
    "endpoint": "unix:///var/run/docker.sock"
  }
}
```

说明：

- 只新增 `short_id`。
- `image_id` 保持完整 `sha256:...`。

### 17. 查看缓存：`agent-compose cache ls`

当前空列表输出：

```text
CACHE ID  DRIVER  TYPE  STATUS  REMOVABLE  SIZE  REF/SESSION  PATH
Warnings:
- microsandbox session references are not fully resolved
```

建议有数据时输出：

```text
CACHE ID      DRIVER  TYPE       STATUS      REMOVABLE  SIZE   REF
8b42ac739d10  docker  sandbox    referenced  false      789    c5582b466ada
4a19e01f64ca  docker  rootfs      unused      true       123M   agent-compose-guest:latest
```

建议 verbose 输出：

```text
CACHE ID      DRIVER  TYPE       STATUS      REMOVABLE  SIZE   REF                         PATH
8b42ac739d10  docker  sandbox    referenced  false      789    c5582b466ada                agent-compose/data/sessions/c5582b46-6ada-4288-9664-19bdb8788a68
4a19e01f64ca  docker  rootfs      unused      true       123M   agent-compose-guest:latest  agent-compose/data/image-cache/e67e6413b80b/rootfs
```

说明：

- 默认 `PATH` 可以不展示，避免输出过宽。
- `cache inspect` 和 `cache rm` 应支持短 ID。

```bash
agent-compose cache inspect 8b42ac739d10
agent-compose cache rm 4a19e01f64ca
```

## 迁移建议

建议按兼容优先顺序推进：

1. 增加统一 `short_id` 计算与展示工具。
2. 在 CLI JSON 输出中新增 `short_id`、`project_short_id`、`run_short_id`、`sandbox_short_id` 等字段。
3. 在 sandbox/run 输出中新增 `sandbox_id`，保留 `session_id`。
4. 调整文本表格：默认 `ID` 列展示短 ID，`--verbose` 展示完整 ID 或完整 hash。
5. 增加 CLI 短 ID 前缀解析能力。
6. 调整日志前缀，使用 `agent + run short ID`。
7. 逐步弱化 `managed_agent_id`、`session_id` 等内部感较强的字段在默认展示中的存在感，但 JSON 保留兼容。

## 风险与注意事项

1. 短 ID 解析必须处理歧义，不能静默选错资源。
2. Project/Agent 的 name 在不同作用域下可能重复，因此必须结合 project 上下文解析。
3. JSON 字段新增不会破坏兼容；字段删除或重命名需要单独版本策略。
4. Image 不应套用 agent-compose 的 ID 规则，避免和 Docker/OCI 生态冲突。
5. Session 到 Sandbox 的迁移需要清晰标注兼容期，避免 UI/API 使用方误解。

## 最终产品形态示例

一次完整操作应类似：

```bash
$ agent-compose ls
PROJECT ID    NAME            CONFIG FILE                                      REVISION  AGENTS  SCHEDULERS
55521f60a3e9  docker-minimal  agent-compose/examples/docker-minimal/agent-compose.yml  1  1  0

$ agent-compose run reviewer --command 'echo ok' --keep-running
ok

$ agent-compose ps
SANDBOX ID    PROJECT         AGENT     STATUS   RUN ID        DRIVER  IMAGE
c5582b466ada  docker-minimal  reviewer  running  103f88fea811  docker  agent-compose-guest:latest

$ agent-compose logs --run 103f88fea811
reviewer 103f88fea811 | ok

$ agent-compose inspect sandbox c5582b466ada
{
  "sandbox_id": "c5582b46-6ada-4288-9664-19bdb8788a68",
  "sandbox_short_id": "c5582b466ada",
  "project_name": "docker-minimal",
  "agent_name": "reviewer",
  "run_id": "run-reviewer-103f88fea811",
  "run_short_id": "103f88fea811",
  "status": "running"
}
```

这个形态里，用户看到的每个 ID 都可以自然复制到下一步命令中，同时产品语义保持清晰：预定义资源看名字，运行时资源看短 ID，镜像看 Docker/OCI 标识。
