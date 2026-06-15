# Driver-Specific Runtime Mount Manifest 设计

本文档描述当前三种 runtime driver 的 mount manifest 行为。核心原则是：不要依赖“直接把单个文件作为 mount source 挂进 sandbox”的能力。Docker 可以继续使用 file bind；BoxLite 和 Microsandbox 只使用目录 source。

## 背景

早期 manifest 同时包含目录 source 和文件 source：

- 目录 source: `workspace`、`state`、`runtime`、`logs`、`home/.codex`、`home/.claude` 等。
- 文件 source: `home/.claude.json`、`home/.gitconfig`。

BoxLite 对 file source 会报错：

```text
[internal] boxlite async operation: configuration error: Volume host path is not a directory: /data/sessions/<session_id>/home/.claude.json
```

当前实现按 driver 生成 manifest：

- `docker`: 保留细粒度目录和文件 bind。
- `boxlite`: 只生成目录 source mount。
- `microsandbox`: 只生成目录 source mount。

manifest 始终写入：

```text
<session>/vm/mount-manifest.json
```

manifest 包含 `driver` 字段。runtime consumer 会校验 manifest driver 与当前 runtime driver 一致。

## Manifest Model

```json
{
  "version": 1,
  "driver": "boxlite",
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

`loadRuntimeMountManifest` 校验：

- `version` 是当前支持版本。
- `driver` 是合法 runtime driver。
- 如果调用方传入 expected driver，manifest driver 必须匹配。
- mount `type` 必须是 `bind`。
- `hostPath` 和 `guestPath` 必须是绝对路径。

`loadDirectoryRuntimeMountManifest` 在上述校验基础上额外要求所有 `hostPath` 都是目录。BoxLite 和 Microsandbox 使用这个 loader。

## Docker Layout

Docker manifest 保持细粒度 source：

| Host path | Guest path |
| --- | --- |
| `<session>/workspace` | `/workspace` |
| `<session>/state` | `/data/state` |
| `<session>/runtime` | `/data/runtime` |
| `<session>/logs` | `/data/logs` |
| `<session>/home/.codex` | `/root/.codex` |
| `<session>/home/.claude` | `/root/.claude` |
| `<session>/home/.claude.json` | `/root/.claude.json` |
| `<session>/home/.gitconfig` | `/root/.gitconfig` |
| `<session>/home/.gemini` | `/root/.gemini` |
| `<session>/home/.config/claude` | `/root/.config/claude` |
| `<session>/home/.config/Claude` | `/root/.config/Claude` |
| `<session>/home/.config/gemini` | `/root/.config/gemini` |
| `<session>/home/.local/share/gemini` | `/root/.local/share/gemini` |

Docker runtime 对每个 source 应用 `DOCKER_HOST_SESSION_ROOT` rebase。这包括 `.claude.json` 和 `.gitconfig` 两个 file source。

Docker driver 下，fresh session 的 host 侧 layout 会包含全部 manifest source：

```text
<session>/
  workspace/
  state/
  runtime/
  logs/
  home/
    .codex/
    .claude/
    .claude.json
    .gitconfig
    .gemini/
    .config/
      claude/
      Claude/
      gemini/
    .local/
      share/
        gemini/
  vm/
    mount-manifest.json
```

其中 `<session>/home/.claude.json` 和 `<session>/home/.gitconfig` 是 file mount source；其他 host source 都是目录。

## BoxLite Layout

BoxLite manifest 只包含一个目录 source：

| Host path | Guest path |
| --- | --- |
| `<session>` | `/data` |

BoxLite consumer 读取 manifest 时会使用 directory-only loader，确保传给 `boxlite_options_add_volume` 的 source 都是目录。guest 启动命令会在容器内把 `/workspace` 和 `/root` 建成指向 `/data/workspace` 和 `/data/home` 的 symlink。默认的 `/data/state`、`/data/runtime`、`/data/logs` 已经位于 session mount 内，不需要再建 symlink。

## Microsandbox Layout

Microsandbox manifest 与 BoxLite 相同，只包含一个目录 source：

| Host path | Guest path |
| --- | --- |
| `<session>` | `/data` |

Microsandbox consumer 读取 manifest 时会使用 directory-only loader，确保构造 `microsandbox.Mount.Bind` map 前不会包含 file source。guest 启动命令使用与 BoxLite 相同的 symlink bootstrap 暴露兼容路径。

## BoxLite / Microsandbox Host Layout

BoxLite 和 Microsandbox driver 下，fresh session 的 host 侧最小 layout 是：

```text
<session>/
  workspace/
  state/
  runtime/
  logs/
  home/
    .codex/
    .claude/
    .claude.json
    .gitconfig
  vm/
    mount-manifest.json
```

`initializeSessionHomeDefaults` 在 `<session>/home` 下准备：

- `.codex/`
- `.claude/`
- `.claude.json`
- `.gitconfig`

BoxLite 和 Microsandbox 不会为了 manifest 额外创建 `.gemini/`、`.config/claude/`、`.config/Claude/`、`.config/gemini/`、`.local/share/gemini/` 这些 home 子目录。如果 guest 运行时创建，或同一 session 曾经用 Docker driver 准备过，这些路径可能仍会留在 host 侧。

目录级挂载 `<session> -> /data` 会覆盖 guest image 原生 `/data` 的最终可见内容。默认配置下 `/data/state`、`/data/runtime`、`/data/logs` 直接来自挂载目录，`/workspace` 和 `/root` 会被启动命令重建为 symlink。当前 guest home 约定为 `/root`，host session home 会初始化必要配置，因此这是跨 driver 的兼容方案。

## Driver Switch Behavior

start/resume 前 manifest 始终按当前已解析 driver 重写。同一个 session 如果先用 Docker 生成 manifest，再用 BoxLite 或 Microsandbox 启动，最终 manifest 会变为 directory-only layout，不会复用旧的 Docker file source mount。

## Runtime Image Source Order

mount manifest 只描述 session 数据目录如何挂载，不描述 guest rootfs 来源。BoxLite 和 Microsandbox 的 rootfs/image 解析遵循 Docker-first 策略：

- BoxLite：`BOX_ROOTFS_PATH` 非空时直接使用该目录；否则 Docker daemon 可用时先从本地 Docker image materialize OCI layout；Docker daemon 不可用或 Docker image miss 时使用 OCI cache，cache miss 会通过 go-containerregistry pull，再 materialize 到 `IMAGE_CACHE_ROOT` 同级的 `image-cache/<image-id>/oci` 并传给 BoxLite。
- Microsandbox：Docker daemon 可用时先从本地 Docker image materialize rootfs；Docker daemon 不可用或 Docker image miss 时使用 OCI cache，cache miss 会通过 go-containerregistry pull，再展开到 `IMAGE_CACHE_ROOT` 同级的 `image-cache/<image-id>/rootfs` 并作为绝对路径传给 Microsandbox。绝对 rootfs path 使用 `PullPolicyNever`。
- Docker runtime 仍只使用 Docker daemon image store，不直接消费 OCI cache。

因此 BoxLite/Microsandbox 在无 Docker daemon 环境中不会把原始 image ref 静默交给 runtime 自行拉取；daemon 的 OCI cache 是无 Docker 路径的镜像事实源。该策略不改变 BoxLite/Microsandbox 的 directory-only mount manifest 和 guest environment contract。

## Verification Coverage

当前测试覆盖以下行为：

- Docker manifest 包含 `.claude.json`、`.gitconfig` file source。
- Docker mount rebase 覆盖 file source。
- BoxLite/Microsandbox manifest 不包含 file source。
- BoxLite/Microsandbox manifest 只包含 `<session> -> /data`。
- BoxLite/Microsandbox manifest 中所有 host source 都是目录。
- directory-only loader 拒绝 file source。
- driver 切换会重写 manifest。
- mount manifest 写入、重写和 directory loader 消费进入 Go unit/integration/e2e 测试形状。

## Runtime Smoke Tests

真实 runtime 启动 smoke test 是显式 opt-in，默认 `go test` 不启动 sandbox。

开启方式：

```bash
task test:runtime-smoke
```

该 task 是独立手工任务，不接入 `task test`、`task all` 或 CI。执行时会先从 `Dockerfile` 的 `boxlite-build` 和 `microsandbox-build` stage 导出本地运行所需工件到 `build/boxlite`、`build/microsandbox`，然后默认尝试运行 BoxLite 和 Microsandbox smoke tests。每个 driver 会运行现有 directory-only mount smoke；如果提供 `SMOKE_OCI_IMAGE_REF`，还会运行 go-containerregistry OCI image smoke，验证无 Docker daemon 路径下的 OCI cache 镜像消费。

可以用 `SMOKE_RUNTIME_DRIVERS` 选择 driver：

```bash
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=boxlite,microsandbox task test:runtime-smoke
```

smoke test 会真实创建并启动对应 runtime，并通过启动期 marker 验证：

- BoxLite/Microsandbox manifest 可被 directory-only loader 消费。
- manifest 不包含 `/root/.claude.json` 或 `/root/.gitconfig` 的独立 file source。
- `<session>` 挂载到 `/data`。
- guest 内 `/root/.claude.json` 和 `/root/.gitconfig` 存在。
- guest 对 `/data/state` 和 `/root` 的写入会持久化到 host `<session>/state` 和 `<session>/home`。
- 设置 `SMOKE_OCI_IMAGE_REF` 时，BoxLite 会使用 OCI cache materialized layout，Microsandbox 会使用 OCI cache rootfs；测试会强制 Docker daemon 不可用，避免回退到本地 Docker materialization。

可选镜像覆盖：

- `SMOKE_DEFAULT_IMAGE`
- `SMOKE_DOCKER_DEFAULT_IMAGE`
- `SMOKE_MICROSANDBOX_DEFAULT_IMAGE`
- `SMOKE_BOX_ROOTFS_PATH`
- `SMOKE_OCI_IMAGE_REF`

`SMOKE_OCI_IMAGE_REF` 必须指向可启动 agent-compose guest 的镜像，至少包含当前 smoke 需要的 shell、Jupyter 启动依赖，以及可由 guest bootstrap 写入 `/data/state/runtime-mount-smoke.txt` 和 `/root/.agent-compose-smoke-home` 的环境。未设置时 OCI image smoke 会 skip，directory-only mount smoke 仍按原逻辑运行。
