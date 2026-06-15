# Runtime Mount Manifest 设计

本文档描述当前 runtime mount manifest 的代码事实。agent-compose 不再把整个 session 目录挂到 guest 内部的 `/data`，而是在启动或恢复 runtime 前生成 manifest，把 session 子目录挂到 guest 的惯用路径。

## 设计目标

runtime 内工具应继续使用镜像默认的目录语义：

- workspace 位于 `/workspace`。
- `$HOME` 使用镜像默认值，当前约定为 `/root`。
- agent-compose 内部交换目录位于 `/data/state`、`/data/runtime`、`/data/logs`。

host 侧 session 状态仍保存在 `<session>` 下，但不把 `<session>` 整体暴露给 guest。`context`、`vm`、`proxy`、`metadata.json` 等 host 控制面状态不会出现在 manifest 中。

## Session Host Layout

`Store.CreateSession` 创建的 host session 目录结构包括：

```text
<session>/
  context/
  home/
  runtime/
  workspace/
  state/
  logs/
  vm/
  proxy/
  metadata.json
  vm/runtime.json
  proxy/jupyter.json
  state/cells.json
  state/events.json
```

guest/runtime 实际使用：

| Host path | Guest path | 用途 |
| --- | --- | --- |
| `<session>/workspace` | `/workspace` | Jupyter root、cell cwd、loader command cwd、agent working directory |
| `<session>/state` | `/data/state` | cell artifacts、loader request/result、agent prompt/schema/provider state |
| `<session>/runtime` | `/data/runtime` | runtime JS MPI/resource/cache |
| `<session>/logs` | `/data/logs` | Jupyter log |
| `<session>/home` 或其子路径 | `/root` 或其子路径 | session-local tool config/state |

不暴露给 guest：

- `<session>/context`
- `<session>/vm`
- `<session>/proxy`
- `<session>/metadata.json`

## Guest Path Defaults

默认 guest 路径：

| Config field | Default |
| --- | --- |
| `GuestWorkspacePath` | `/workspace` |
| `GuestHomePath` | `/root` |
| `GuestStateRoot` | `/data/state` |
| `GuestRuntimeRoot` | `/data/runtime` |
| `GuestLogRoot` | `/data/logs` |

`GuestHomePath` 是 manifest 目标路径，不表示 agent-compose 会覆盖 `HOME`。运行时不显式注入 `HOME`，guest 内工具继承镜像默认 home。

## Manifest File

启动或恢复 session 前，agent-compose 写入：

```text
<session>/vm/mount-manifest.json
```

manifest 结构：

```json
{
  "version": 1,
  "driver": "docker",
  "mounts": [
    {
      "hostPath": "/abs/path/to/session/workspace",
      "guestPath": "/workspace",
      "type": "bind",
      "readOnly": false
    }
  ]
}
```

约束：

- `version` 当前为 `1`。
- `driver` 是已解析 runtime driver，取值为 `docker`、`boxlite` 或 `microsandbox`。
- `type` 当前只支持 `bind`。
- `hostPath` 和 `guestPath` 都必须是绝对路径。
- 生成 manifest 前会创建所有需要的 host source。
- runtime consumer 会按 expected driver 校验 manifest，避免旧 manifest 被错误复用。

## Home 初始化

生成 manifest 前，agent-compose 会初始化 `<session>/home` 的默认配置，并且不覆盖已存在的目标：

| Asset | Host target |
| --- | --- |
| `assets/.codex` | `<session>/home/.codex` |
| `assets/.claude` | `<session>/home/.claude` |
| `assets/.claude.json` | `<session>/home/.claude.json` |
| `assets/.gitconfig` | `<session>/home/.gitconfig` |

guest 侧不再运行 `.codex` copy 同步逻辑。工具看到的 `$HOME` 仍是 `/root`，但相关配置和状态由 host session home 提供持久化。

## Driver 差异

Docker 支持 file bind mount，因此 Docker manifest 保留细粒度 home 子路径挂载，包括 `.claude.json` 和 `.gitconfig` 两个 file source。

BoxLite 和 Microsandbox 不依赖 file source mount。它们只挂一个目录 source：`<session> -> /data`。默认配置下 `/data/state`、`/data/runtime`、`/data/logs` 直接来自这个挂载，guest 启动命令会把 `/workspace` 和 `/root` 建成指向 `/data/workspace` 和 `/data/home` 的 symlink。这样 `.claude.json` 和 `.gitconfig` 在 guest 内仍位于 `/root/.claude.json` 和 `/root/.gitconfig`，但不会作为独立 file mount 出现在 manifest 中。

更详细的 driver-specific layout 见 `docs/runtime_mount_manifest_driver_specific_design.md`。

## Runtime Consumers

各 runtime driver 均读取 `<session>/vm/mount-manifest.json`：

- Docker 使用 `loadRuntimeMountManifest(session, RuntimeDriverDocker)`，并对每个 source 应用 `DOCKER_HOST_SESSION_ROOT` rebase。
- BoxLite 使用 `loadDirectoryRuntimeMountManifest(session, RuntimeDriverBoxlite)`，进入 `boxlite_options_add_volume` 前校验所有 source 都是目录。
- Microsandbox 使用 `loadDirectoryRuntimeMountManifest(session, RuntimeDriverMicrosandbox)`，构造 `microsandbox.Mount.Bind` map 前校验所有 source 都是目录。

## Runtime Paths

启动后的 guest 路径：

- Jupyter root: `/workspace`
- Jupyter log: `/data/logs/jupyter.log`
- cell/loader command artifacts: `/data/state/cells/...`
- agent prompt/schema/provider state: `/data/state/agents/...`
- runtime JS resources/cache/MPI: `/data/runtime/...`

runtime command 和 agent env 注入：

- `WORKSPACE=/workspace`
- `STATE_ROOT=/data/state`
- `RUNTIME_ROOT=/data/runtime`

不再注入：

- `HOME`
- `SESSION_WORKSPACE`
- guest-side `SESSION_ROOT`
