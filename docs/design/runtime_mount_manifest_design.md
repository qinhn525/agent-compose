# Runtime Mount Manifest

Chinese version: [../zh-CN/design/runtime_mount_manifest_design.md](../zh-CN/design/runtime_mount_manifest_design.md)

This document describes the current runtime mount manifest behavior in code.
agent-compose no longer mounts the entire session directory into the guest as
`/data`. Instead, before starting or resuming runtime, it generates a manifest
that maps session subdirectories to the guest's conventional paths.

## Design Goals

Tools inside runtime should continue to use image-default directory semantics:

- Workspace lives at `/workspace`.
- `$HOME` uses the image default, currently `/root`.
- agent-compose internal exchange directories live at `/data/state`,
  `/data/runtime`, and `/data/logs`.

Host-side session state still lives under `<session>`, but `<session>` is not
exposed wholesale to the guest. Host control-plane state such as `context`,
`vm`, `proxy`, and `metadata.json` does not appear in the manifest.

## Session Host Layout

The host session directory created by `Store.CreateSession` includes:

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

Guest/runtime actually uses:

| Host path | Guest path | Purpose |
| --- | --- | --- |
| `<session>/workspace` | `/workspace` | Jupyter root, cell cwd, loader command cwd, agent working directory |
| `<session>/state` | `/data/state` | Cell artifacts, loader request/result, agent prompt/schema/provider state |
| `<session>/runtime` | `/data/runtime` | Runtime JS MPI/resource/cache |
| `<session>/logs` | `/data/logs` | Jupyter log |
| `<session>/home` or child paths | `/root` or child paths | Session-local tool config/state |

Not exposed to the guest:

- `<session>/context`
- `<session>/vm`
- `<session>/proxy`
- `<session>/metadata.json`

## Guest Path Defaults

Default guest paths:

| Config field | Default |
| --- | --- |
| `GuestWorkspacePath` | `/workspace` |
| `GuestHomePath` | `/root` |
| `GuestStateRoot` | `/data/state` |
| `GuestRuntimeRoot` | `/data/runtime` |
| `GuestLogRoot` | `/data/logs` |

`GuestHomePath` is a manifest target path and does not mean agent-compose
overrides `HOME`. Runtime does not explicitly inject `HOME`; tools inside the
guest inherit the image default home.

## Manifest File

Before starting or resuming a session, agent-compose writes:

```text
<session>/vm/mount-manifest.json
```

Manifest structure:

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

Constraints:

- `version` is currently `1`.
- `driver` is the resolved runtime driver: `docker`, `boxlite`, or
  `microsandbox`.
- `type` currently supports only `bind`.
- `hostPath` and `guestPath` must both be absolute paths.
- All required host sources are created before the manifest is generated.
- Runtime consumers validate the manifest against the expected driver to avoid
  accidentally reusing an old manifest.

## Home Initialization

Before generating the manifest, agent-compose initializes default config under
`<session>/home` and does not overwrite existing targets:

| Asset | Host target |
| --- | --- |
| `assets/.codex` | `<session>/home/.codex` |
| `assets/.claude` | `<session>/home/.claude` |
| `assets/.claude.json` | `<session>/home/.claude.json` |
| `assets/.gitconfig` | `<session>/home/.gitconfig` |

The guest side no longer runs `.codex` copy synchronization logic. Tools still
see `$HOME` as `/root`, but related config and state are persisted by host
session home.

## Driver Differences

Docker supports file bind mounts, so the Docker manifest keeps fine-grained home
subpath mounts, including `.claude.json` and `.gitconfig` file sources.

BoxLite and Microsandbox do not rely on file source mounts. They mount one
directory source only: `<session> -> /data`. With default configuration,
`/data/state`, `/data/runtime`, and `/data/logs` come directly from that mount.
The guest startup command creates `/workspace` and `/root` symlinks pointing to
`/data/workspace` and `/data/home`. Therefore `.claude.json` and `.gitconfig`
still appear in the guest as `/root/.claude.json` and `/root/.gitconfig`, but do
not appear as independent file mounts in the manifest.

For the detailed driver-specific layout, see
`runtime_mount_manifest_driver_specific_design.md`.

## Runtime Consumers

Each runtime driver reads `<session>/vm/mount-manifest.json`:

- Docker uses `loadRuntimeMountManifest(session, RuntimeDriverDocker)` and
  applies `DOCKER_HOST_SESSION_ROOT` rebase to each source.
- BoxLite uses `loadDirectoryRuntimeMountManifest(session, RuntimeDriverBoxlite)`
  and validates that all sources are directories before calling
  `boxlite_options_add_volume`.
- Microsandbox uses
  `loadDirectoryRuntimeMountManifest(session, RuntimeDriverMicrosandbox)` and
  validates that all sources are directories before constructing
  `microsandbox.Mount.Bind`.

## Runtime Paths

Guest paths after startup:

- Jupyter root: `/workspace`
- Jupyter log: `/data/logs/jupyter.log`
- Cell/loader command artifacts: `/data/state/cells/...`
- Agent prompt/schema/provider state: `/data/state/agents/...`
- Runtime JS resources/cache/MPI: `/data/runtime/...`

Runtime command and agent env injection:

- `WORKSPACE=/workspace`
- `STATE_ROOT=/data/state`
- `RUNTIME_ROOT=/data/runtime`

No longer injected:

- `HOME`
- `SESSION_WORKSPACE`
- guest-side `SESSION_ROOT`
