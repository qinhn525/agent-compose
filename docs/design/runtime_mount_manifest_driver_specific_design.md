# Driver-Specific Runtime Mount Manifest

Chinese version: [../zh-CN/design/runtime_mount_manifest_driver_specific_design.md](../zh-CN/design/runtime_mount_manifest_driver_specific_design.md)

This document describes current mount manifest behavior for the three runtime
drivers. The core rule is: do not depend on the ability to mount a single file
source directly into the sandbox. Docker can continue using file binds; BoxLite
and Microsandbox use directory sources only.

## Background

Early manifests contained both directory sources and file sources:

- Directory sources: `workspace`, `state`, `runtime`, `logs`, `home/.codex`,
  `home/.claude`, and similar paths.
- File sources: `home/.claude.json`, `home/.gitconfig`.

BoxLite reports an error for file sources:

```text
[internal] boxlite async operation: configuration error: Volume host path is not a directory: /data/sessions/<session_id>/home/.claude.json
```

Current implementation generates manifests by driver:

- `docker`: keep fine-grained directory and file binds.
- `boxlite`: generate directory source mounts only.
- `microsandbox`: generate directory source mounts only.

The manifest is always written to:

```text
<session>/vm/mount-manifest.json
```

The manifest contains a `driver` field. Runtime consumers validate that the
manifest driver matches the current runtime driver.

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

`loadRuntimeMountManifest` validates:

- `version` is the currently supported version.
- `driver` is a valid runtime driver.
- If the caller passes an expected driver, manifest driver must match it.
- Mount `type` must be `bind`.
- `hostPath` and `guestPath` must be absolute paths.

`loadDirectoryRuntimeMountManifest` adds the requirement that every `hostPath`
is a directory. BoxLite and Microsandbox use this loader.

## Docker Layout

Docker manifest keeps fine-grained sources:

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

Docker runtime applies `DOCKER_HOST_SESSION_ROOT` rebase to each source. This
includes `.claude.json` and `.gitconfig` file sources.

Under Docker driver, a fresh session host layout includes all manifest sources:

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

`<session>/home/.claude.json` and `<session>/home/.gitconfig` are file mount
sources. Other host sources are directories.

## BoxLite Layout

BoxLite manifest contains one directory source only:

| Host path | Guest path |
| --- | --- |
| `<session>` | `/data` |

When the BoxLite consumer reads the manifest, it uses the directory-only loader
to ensure all sources passed to `boxlite_options_add_volume` are directories.
The guest startup command creates `/workspace` and `/root` symlinks pointing to
`/data/workspace` and `/data/home`. Default `/data/state`, `/data/runtime`, and
`/data/logs` are already inside the session mount and do not need symlinks.

## Microsandbox Layout

Microsandbox manifest is the same as BoxLite and contains one directory source
only:

| Host path | Guest path |
| --- | --- |
| `<session>` | `/data` |

When the Microsandbox consumer reads the manifest, it uses the directory-only
loader to ensure no file source is present before constructing
`microsandbox.Mount.Bind`. The guest startup command uses the same symlink
bootstrap as BoxLite to expose compatible paths.

## BoxLite / Microsandbox Host Layout

Under BoxLite and Microsandbox, a fresh session's minimal host layout is:

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

`initializeSessionHomeDefaults` prepares these under `<session>/home`:

- `.codex/`
- `.claude/`
- `.claude.json`
- `.gitconfig`

BoxLite and Microsandbox do not create `.gemini/`, `.config/claude/`,
`.config/Claude/`, `.config/gemini/`, or `.local/share/gemini/` home subdirs for
the manifest. If the guest runtime creates them, or if the same session was
previously prepared with Docker driver, these paths may still remain on the host
side.

The directory mount `<session> -> /data` overrides the final visible content of
the guest image's native `/data`. With default configuration, `/data/state`,
`/data/runtime`, and `/data/logs` come directly from mounted directories, while
`/workspace` and `/root` are recreated as symlinks by the startup command. The
current guest home convention is `/root`, and host session home initializes the
required config, so this is the cross-driver compatibility approach.

## Driver Switch Behavior

Before start/resume, the manifest is always rewritten according to the currently
resolved driver. If the same session first generated a Docker manifest and is
later started with BoxLite or Microsandbox, the final manifest becomes the
directory-only layout and does not reuse old Docker file source mounts.

## Runtime Image Source Order

The mount manifest describes only how session data directories are mounted. It
does not describe the guest rootfs source. BoxLite and Microsandbox rootfs/image
resolution follows a Docker-first strategy:

- BoxLite: if `BOX_ROOTFS_PATH` is non-empty, use that directory directly.
  Otherwise, when Docker daemon is available, first materialize OCI layout from
  the local Docker image. When Docker daemon is unavailable or the Docker image
  is missing, use OCI cache; cache miss pulls through go-containerregistry, then
  materializes to `image-cache/<image-id>/oci` beside `IMAGE_CACHE_ROOT` and
  passes that path to BoxLite.
- Microsandbox: when Docker daemon is available, first materialize rootfs from
  the local Docker image. When Docker daemon is unavailable or the Docker image
  is missing, use OCI cache; cache miss pulls through go-containerregistry, then
  extracts to `image-cache/<image-id>/rootfs` beside `IMAGE_CACHE_ROOT` and
  passes that absolute path to Microsandbox. Absolute rootfs path uses
  `PullPolicyNever`.
- Docker runtime still uses only Docker daemon image store and does not consume
  OCI cache directly.

Therefore, in an environment without Docker daemon, BoxLite/Microsandbox do not
silently pass the raw image ref to the runtime for pulling. The daemon OCI cache
is the image source of truth for Dockerless paths. This strategy does not change
the BoxLite/Microsandbox directory-only mount manifest or guest environment
contract.

## Verification Coverage

Current tests cover:

- Docker manifest includes `.claude.json` and `.gitconfig` file sources.
- Docker mount rebase covers file sources.
- BoxLite/Microsandbox manifests do not contain file sources.
- BoxLite/Microsandbox manifests contain only `<session> -> /data`.
- All host sources in BoxLite/Microsandbox manifests are directories.
- Directory-only loader rejects file sources.
- Driver switching rewrites the manifest.
- Manifest writing, rewriting, and directory loader consumption are covered by
  Go unit/integration/e2e test shapes.

## Runtime Smoke Tests

Real runtime startup smoke tests are explicit opt-in. Default `go test` does not
start a sandbox.

Enable with:

```bash
task test:runtime-smoke
```

This task is a standalone manual task. It is not included in `task test`,
`task all`, or CI. It first exports local runtime artifacts from the
`boxlite-build` and `microsandbox-build` stages in `Dockerfile` into
`build/boxlite` and `build/microsandbox`, then by default attempts BoxLite and
Microsandbox smoke tests. Each driver runs the existing directory-only mount
smoke. If `SMOKE_OCI_IMAGE_REF` is provided, it also runs the
go-containerregistry OCI image smoke to verify OCI cache image consumption on
the Dockerless path.

Use `SMOKE_RUNTIME_DRIVERS` to choose drivers:

```bash
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=boxlite,microsandbox task test:runtime-smoke
```

Smoke tests create and start the real runtime and validate startup markers:

- BoxLite/Microsandbox manifests can be consumed by the directory-only loader.
- Manifest does not contain independent file sources for `/root/.claude.json` or
  `/root/.gitconfig`.
- `<session>` is mounted at `/data`.
- Guest `/root/.claude.json` and `/root/.gitconfig` exist.
- Guest writes to `/data/state` and `/root` persist to host `<session>/state`
  and `<session>/home`.
- When `SMOKE_OCI_IMAGE_REF` is set, BoxLite uses OCI cache materialized layout
  and Microsandbox uses OCI cache rootfs. The test forces Docker daemon to be
  unavailable to avoid fallback to local Docker materialization.

Optional image overrides:

- `SMOKE_DEFAULT_IMAGE`
- `SMOKE_DOCKER_DEFAULT_IMAGE`
- `SMOKE_MICROSANDBOX_DEFAULT_IMAGE`
- `SMOKE_BOX_ROOTFS_PATH`
- `SMOKE_OCI_IMAGE_REF`

`SMOKE_OCI_IMAGE_REF` must point to a bootable agent-compose guest image. It
must include at least the shell and Jupyter startup dependencies required by the
smoke test, and an environment where guest bootstrap can write
`/data/state/runtime-mount-smoke.txt` and `/root/.agent-compose-smoke-home`.
When unset, OCI image smoke is skipped; directory-only mount smoke still follows
the original logic.
