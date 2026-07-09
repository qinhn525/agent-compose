# agent-compose 在 YAML 中支持 MCP 配置的交集方案

## 背景与约束

这版方案按下面 4 个约束重新收敛：

1. 配置必须覆盖 **当前仓库已支持的全部 provider**：`codex`、`claude`、`gemini`、`opencode`
2. YAML 里只能保留 **这四个 provider 都能实现的交集配置**
3. 配置字段可以少，但要覆盖 MCP 的核心能力
4. 真实下发方式必须尽量贴合当前仓库已有的 provider 配置下发模式

结论先说：

- 现阶段不要追求“大而全”的 MCP schema
- 应该先做一个 **最小交集版 MCP 配置**
- 这个交集版配置需要能让四个 provider 都真正生效

---

## 一、当前仓库真实支持的 provider

从当前代码可确认，agent provider 标准化后支持：

- `codex`
- `claude`
- `gemini`
- `opencode`

关键位置：
- `pkg/model/agent_model.go:68`
- `runtime/javascript/src/provider.ts:3`

所以本次方案不能只闭环某两个 provider；必须确保这四个 provider 都有可实现路径。

---

## 二、四个 provider 的 MCP 配置交集是什么

我结合当前能确认的配置 shape，筛了一遍真正稳定的交集。

### 1. Codex

从 Codex schema 能确认它支持：
- 本地进程型 MCP：`command`、`args`、`env`
- 远程 MCP：`url`、`http_headers`
- 还有更多扩展项，例如 `enabled_tools`、`disabled_tools`、`oauth`、`startup_timeout_ms`

### 2. Claude

从你给的 Claude SDK 仓库可以确认：
- `McpStdioServerConfig`：`command`、`args`、`env`
- `McpSSEServerConfig`：`url`、`headers`
- `McpHttpServerConfig`：`url`、`headers`
- 并且 SDK 支持直接通过 `mcp_servers` 传入
- 还支持 `strict_mcp_config`

关键位置：
- `/root/code/private/python/open_source/claude-agent-sdk-python/src/claude_agent_sdk/types.py:611`
- `/root/code/private/python/open_source/claude-agent-sdk-python/src/claude_agent_sdk/_internal/transport/subprocess_cli.py:307`

### 3. Gemini

Gemini 当前可确认支持：
- 本地进程型：`command`、`args`、`env`、`cwd`
- 远程型：`url`（SSE）或 `httpUrl`（streamable HTTP）、`headers`
- 还有扩展项如 `trust`、`includeTools`、`excludeTools`、`timeout`

### 4. OpenCode

OpenCode 当前可确认支持：
- `type: local` + `command[]` + `environment`
- `type: remote` + `url` + `headers`
- 还有 `oauth`、`timeout`、`disabled`

---

## 三、真正安全的最小交集

把四家都取交集后，当前最稳的 MCP server 配置只剩两类：

### A. 本地进程型 server

所有 provider 都可映射的字段：
- `command`
- `args`
- `env`

### B. 远程 server

所有 provider 都可映射的字段：
- `transport`：`sse` 或 `http`
- `url`
- `headers`

### C. 现阶段不应进入交集的字段

虽然某些 provider 支持，但**不能进当前公共 YAML**：

- `cwd`
  - Gemini 支持
  - Claude 当前 typed config 没看到
  - 不能放进交集

- `include_tools` / `exclude_tools`
  - Codex、Gemini 有
  - Claude 当前确认不到统一输入字段
  - OpenCode 当前也不是稳定公共形态
  - 不能放进交集

- `trust`
  - Gemini 有清晰字段
  - 其他 provider 没有统一等价物
  - 不能放进交集

- `disabled` / `enabled`
  - 有些 provider 有，但不统一
  - 交集里不需要它，YAML 里完全可以通过“是否引用该 server”表达启用与否

- `timeout`
  - 各家字段和语义不一致
  - 当前不进交集

- `oauth`
  - 各家差异很大
  - 当前不进交集

结论：

**现阶段最小公共 MCP 配置应只支持：**
- local: `command` / `args` / `env`
- remote: `transport` / `url` / `headers`

这已经覆盖 MCP 核心能力了。

---

## 四、建议的 YAML 设计

为了保证复用和可控，我建议仍然保留：
- project 级 server 库
- agent 级引用

但字段要收敛到交集。

### 推荐 YAML

```yaml
mcps:
  filesystem:
    type: local
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
    env:
      MCP_LOG_LEVEL: info

  docs:
    type: remote
    transport: http
    url: https://docs.example.com/mcp
    headers:
      Authorization:
        value: "Bearer ${DOCS_TOKEN}"
        secret: true

  queue:
    type: remote
    transport: sse
    url: https://queue.example.com/sse
```

```yaml
agents:
  reviewer:
    provider: codex
    mcps: ["filesystem", "docs"]

  claude_worker:
    provider: claude
    mcps: "filesystem"

  gemini_worker:
    provider: gemini
    mcps: ["docs", "queue"]

  opencode_worker:
    provider: opencode
    mcps: "filesystem"
```

### 为什么 agent 级使用 `mcps`，支持引用与内联

当前阶段建议 agent 级主语法改为：
- `mcps: "name"`
- `mcps: ["a", "b"]`
- `mcps:` 列表中混用项目级引用与 agent 级内联对象

推荐内联对象显式带 `name`，例如：
- `- name: docs`
- `  type: remote`
- `  transport: http`
- `  url: https://docs.example.com/mcp`

当前不保留 `mcp` 兼容层；agent 级只接受 `mcps`。

---

## 五、建议的数据结构

### compose 层

建议新增：

```go
type ProjectSpec struct {
    ...
    MCPs map[string]MCPServerSpec `yaml:"mcps,omitempty" json:"mcps,omitempty"`
}

type AgentSpec struct {
    ...
    MCPs AgentMCPEntriesSpec `yaml:"mcps,omitempty" json:"mcps,omitempty"`
}

// AgentMCPEntriesSpec supports scalar refs, inline objects, or a mixed list.
type AgentMCPEntriesSpec []AgentMCPEntrySpec

type MCPServerSpec struct {
    Type      string                `yaml:"type,omitempty" json:"type,omitempty"`
    Transport string                `yaml:"transport,omitempty" json:"transport,omitempty"`
    Command   string                `yaml:"command,omitempty" json:"command,omitempty"`
    Args      []string              `yaml:"args,omitempty" json:"args,omitempty"`
    Env       map[string]EnvVarSpec `yaml:"env,omitempty" json:"env,omitempty"`
    URL       string                `yaml:"url,omitempty" json:"url,omitempty"`
    Headers   map[string]EnvVarSpec `yaml:"headers,omitempty" json:"headers,omitempty"`
}
```

### 校验规则

#### `type=local`
- 必须有 `command`
- 允许 `args`
- 允许 `env`
- 禁止 `transport`
- 禁止 `url`
- 禁止 `headers`

#### `type=remote`
- 必须有 `transport`
- `transport` 只能是 `sse` 或 `http`
- 必须有 `url`
- 允许 `headers`
- 禁止 `command`
- 禁止 `args`
- 禁止 `env`

#### `mcps`（agent 级）
- 支持单个字符串、单个对象，或混合列表
- 字符串项：引用 project 级 `mcps` 中已定义的 key
- 对象项：定义 agent 级私有 MCP，必须显式提供 `name`
- 归一化后：
  - project refs 去重
  - agent 级私有 MCP 转成按 `name` 键控的有效 server map
  - 若内联 `name` 与已引用/已定义名称冲突，直接报错，当前不做覆盖语义

---

## 六、这套下发方式是否符合当前项目已有模式

这个问题非常关键。答案是：

**可以，但不能只用一种方式；要按当前项目里“各 provider 已有接法”分别落地。**

当前项目里已经存在两类典型下发模式：

### 模式 A：host 写会话内配置文件，provider 自动读取或由 env 指向

现有例子：

#### Codex LLM runtime config
- `pkg/llms/runtime_config.go:15`
- host 写：`<session-home>/.codex/config.toml`

#### OpenCode runtime config
- `pkg/llms/runtime_config.go:58`
- host 写：`<session-home>/.config/opencode/opencode.json`
- 再通过 env 传：`OPENCODE_CONFIG`
- 位置：`pkg/llms/runtimefacade/config.go:187`

这是当前项目里非常成熟的模式。

### 模式 B：host 把语义信息写到 session state，由 runtime 从固定路径读取

现有例子：

#### agent system prompt
- host 写文件：`pkg/execution/agent_files.go`
- runtime 从 `stateRoot/agents/system-prompts/system-prompt.txt` 读取：
  - `runtime/javascript/src/system-context.ts:8`
  - `runtime/javascript/src/prompt.ts:37`

这说明：
- host 写 state 文件
- runtime 根据约定路径读取

也是当前项目已有模式。

### 模式 C：runner 直接通过 SDK options 传入 provider 参数

现有例子：

#### Claude runner
- `runtime/javascript/src/runners/claude.ts:43`
- 目前把 `systemPrompt`、`outputFormat` 等直接通过 SDK options 传入

这说明 Claude 本身就更适合走“runtime 直接构造 SDK options”的方式。

---

## 七、MCP 的真实下发应该怎么贴当前项目方式

最合适的方案不是“一刀切”，而是：

### 第一步：host 统一写一份中立 MCP runtime 文件

新增例如：
- `pkg/execution/agent_mcp.go`

职责：
1. 从 `AgentDefinition.ConfigJSON` 解析 agent 绑定的 MCP 信息
2. 解析 agent `mcp` 引用，从 project `mcps` 里拿到最终 server 集合
3. 做变量插值
4. 写入 session state，例如：
   - `state/agents/mcp/config.json`

这一步对应当前项目的**模式 B**。

### 第二步：runtime/javascript 读取中立 MCP runtime 文件

在 `runtime/javascript` 新增一个公共 loader，例如：
- `src/mcp-config.ts`

职责：
- 从 `stateRoot/agents/mcp/config.json` 读取统一配置
- 给各 provider runner 返回已经解析好的 MCP config

这和当前 `system-context.ts` 很像。

### 第三步：各 provider 按“当前最自然的接法”下发

#### 1. `codex`

最贴合当前项目方式的是：
- 在 host 侧扩展已有 `.codex/config.toml` 写入逻辑
- 把 MCP 配置一起写进同一个 Codex config 文件

原因：
- 当前项目已经在写 `.codex/config.toml`
- 这是现有模式 A 的直接扩展
- 比 runtime 再单独写一份 Codex config 更一致

也就是说，Codex 不必走“runtime 再翻译”，而是直接复用 host 当前写 config 文件的模式。

#### 2. `opencode`

最贴合当前项目方式的是：
- 在 host 侧扩展已有 `opencode.json`
- 把 MCP 配置并入该文件
- 继续复用 `OPENCODE_CONFIG`

原因：
- 当前项目本来就这么给 OpenCode 下发 provider config
- 这是现有模式 A 的直接扩展

#### 3. `claude`

最贴合当前项目方式的是：
- runtime 读取统一 MCP config
- 在 `ClaudeRunner.queryOptions()` 里直接传 `mcpServers`
- 同时传 `strict_mcp_config: true`

原因：
- 当前 Claude runner 已经是 SDK options 驱动
- 你给的 Claude SDK 也明确支持：
  - `mcp_servers`
  - `strict_mcp_config`
- 位置：
  - `claude_agent_sdk/types.py`
  - `subprocess_cli.py:307`

这对应当前项目的**模式 C**。

并且 `strict_mcp_config=true` 很重要：
- 可确保只使用 `agent-compose` 下发的 MCP server
- 不混入用户全局 / project 外部 MCP 配置

#### 4. `gemini`

Gemini 当前 runner 是最“轻”的：
- `runtime/javascript/src/runners/gemini.ts`
- 直接 `spawn("gemini", ...)`
- 当前没有现成的 provider config 文件写入逻辑

所以对 Gemini，最贴当前项目模式的做法是：
- runtime 读取统一 MCP config
- 在运行前物化 Gemini CLI 期望的 settings 文件
- 然后再启动 `gemini`

也就是：
- Gemini 走“模式 B + runtime 本地翻译”的方式

这是四家里唯一需要新增这一步的 provider，但仍然符合当前项目已有的“host 写 state / runtime 读取并转化”的模式。

---

## 八、为什么不能强行统一成一种下发方式

因为当前仓库里四个 provider 的接入面本来就不一样：

- `codex`：已有 host 写 `.codex/config.toml`
- `opencode`：已有 host 写 `opencode.json`
- `claude`：当前更自然的是 SDK options
- `gemini`：当前最轻，需要 runtime 补一层 settings 文件转换

如果强行统一成“全部 host 写 provider 私有文件”：
- 会让 Claude 这条链路很别扭

如果强行统一成“全部 runtime 自己翻译写私有文件”：
- 会和 Codex / OpenCode 现有的 host 写 provider config 模式冲突

所以最佳方案是：
- **统一中立 MCP 模型**
- **provider-specific 下发沿用各自当前最自然的方式**

---

## 九、建议的实现路线

### Phase 1：定义最小交集 schema

改动：
- `pkg/compose/spec.go`
- `pkg/compose/normalize.go`
- `pkg/compose/output.go`
- 相关 tests

只支持：
- local: `command/args/env`
- remote: `transport/url/headers`
- agent 侧只支持 `server_refs`

### Phase 2：把 MCP 放入持久化链路

改动：
- `pkg/projects/records.go`

把最终 agent 生效的 MCP 信息编码进 `AgentDefinition.ConfigJSON`。

### Phase 3：新增统一 MCP runtime artifact

新增：
- `pkg/execution/agent_mcp.go`
- `runtime/javascript/src/mcp-config.ts`

职责：
- host 写 `state/agents/mcp/config.json`
- runtime 统一读取

### Phase 4：四个 provider 全部接通

#### `codex`
- 扩展 `pkg/llms/runtime_config.go`
- 把 MCP 一起写入 `.codex/config.toml`

#### `opencode`
- 扩展 `pkg/llms/runtime_config.go`
- 把 MCP 一起写入 `opencode.json`

#### `claude`
- 扩展 `runtime/javascript/src/runners/claude.ts`
- 从 runtime MCP config 读取并传入 `mcpServers`
- 同时开启 `strict_mcp_config`

#### `gemini`
- 扩展 `runtime/javascript/src/runners/gemini.ts`
- 运行前根据统一 config 生成 Gemini settings
- 再启动 CLI

---

## 十、最终结论

这次 MCP 方案应该明确收敛成：

### 1. 只做四个 provider 的交集

当前公共 YAML 只保留：
- local: `command` / `args` / `env`
- remote: `transport` / `url` / `headers`

### 2. agent 级先只支持引用

只支持：
- `mcps: "name"`
- `mcps: ["name-a", "name-b"]`

先不做：
- agent 级 inline 覆盖
- tool filter
- timeout
- trust
- oauth

### 3. 下发方式不能一刀切

要贴当前项目已有模式：
- `codex`：继续 host 写 `.codex/config.toml`
- `opencode`：继续 host 写 `opencode.json`
- `claude`：runtime 直接走 SDK `mcpServers` + `strict_mcp_config`
- `gemini`：runtime 生成 Gemini settings 后启动 CLI

### 4. 统一的是“中立模型”，不是“下发实现”

统一点在：
- `agent-compose.yml` 里的 project `mcps` 与 agent `mcps`
- `compose normalize`
- `AgentDefinition.ConfigJSON`
- `state/agents/mcp/config.json`

不强求统一点在：
- provider-specific 最终写入方式

这才是和当前仓库架构最一致、同时又能让四个 provider 都真正落地的方案。


## 十一、当前实现补充

### agent 侧 MCP YAML

当前代码实现已经调整为：

```yaml
agents:
  reviewer:
    provider: codex
    mcps:
      - filesystem
      - docs
      - name: notes
        type: local
        command: uvx
        args: [notes-server]
```

### 归一化与下发语义

- `agent.mcps`：主输入字段，可混用 project ref 与 inline definition。
- 归一化后的 `NormalizedAgentSpec` 会保存 agent 的有效 `mcps` map。
- 持久化到 `AgentDefinition.ConfigJSON` 时，会直接写入 agent 生效后的 `mcps`。
- provider 下发侧继续只消费统一的生效 `mcps` map，因此无需区分它来自 project ref 还是 inline definition。

### canonical 输出

- project 级保持 `mcps:` map。
- agent 级 canonical 输出统一使用 `mcps:`。
- canonical 输出会输出 agent 的有效 MCP server 集，并统一使用 `mcps`。
