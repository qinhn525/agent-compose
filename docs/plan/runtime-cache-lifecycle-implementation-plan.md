# Runtime cache lifecycle 实施计划

本计划对应 `docs/spec/runtime-cache-lifecycle-spec.md`。实施目标是在 daemon 权威边界内提供显式、可观测、带保护条件的 runtime cache 生命周期管理能力，并把当前 BoxLite/Microsandbox 启动路径中的隐式清理迁出热路径。

## 阶段 1：协议模型和生成物

目标：v2 API 具备 `CacheService` 的稳定 wire contract，Go 和 TypeScript 生成物同步更新，但 handler 可先返回未接线实现。

依赖：已确认的 spec、`proto/agentcompose/v2/agentcompose.proto`、`proto-client/package.json`、`Taskfile.yml`。

实施工作：

1. 在 `proto/agentcompose/v2/agentcompose.proto` 新增 `CacheService`，包含 `ListCaches`、`InspectCache`、`PruneCaches`、`RemoveCache`。
2. 新增 `CacheDomain`、`CacheStatus`、`CacheReference`、`CacheItem`、filter/request/response messages。
3. `ListCachesRequest` 至少支持 `driver`、`domain/type`、`status`、`older_than_seconds`；`PruneCachesRequest` 支持同等 filter、`include_referenced`、`force`；`RemoveCacheRequest` 支持 `cache_id`、`force`。
4. 保持 proto 字段命名和 spec 一致：`cache_id`、`size_bytes`、`blocked_reasons`、`last_used_at`、`last_used_source`、`references`、`warnings`。
5. 重新生成 Go proto 和 Connect Go 代码：

```bash
protoc -I proto \
  --go_out=. --go_opt=paths=source_relative \
  --connect-go_out=. --connect-go_opt=paths=source_relative \
  proto/agentcompose/v2/agentcompose.proto
```

6. 重新生成并构建 TypeScript client：

```bash
cd proto-client && npm ci && npm run gen && npm run build
```

测试和验证：

- `go test ./proto/agentcompose/v2 ./proto/agentcompose/v2/agentcomposev2connect`
- `cd proto-client && npm ci && npm run gen && npm run build`
- `task build`

验收标准：

- `agentcomposev2connect` 中出现 `CacheServiceClient`、`CacheServiceHandler` 和四个 procedure 常量。
- Go build 能编译新增 proto types。
- `proto-client/src/agentcompose/v2/` 生成 CacheService TS types 和 client。
- 现有 `ImageService` proto 和 `RemoveImageRequest.prune_children` 语义不改变。

适用 harness 约束或命令：

- `Taskfile.yml` 的 `task build` 会编译 `./proto/agentcompose/v2` 和 `./proto/agentcompose/v2/agentcomposev2connect`。
- CI `proto-client` job 要求 `npm ci`、`npm run gen`、`npm run build` 通过。

## 阶段 2：runtime cache 领域包

目标：建立不依赖 Connect 的 `pkg/runtimecache` 领域模型和 inventory/prune 核心逻辑，先用 fake 文件树和 fake reference source 完整证明保护语义。

依赖：阶段 1 的 proto 字段定义；`pkg/imagecache`、`pkg/storage/sessionstore`、`pkg/storage/configstore` 的当前事实源。

实施工作：

1. 新增 `pkg/runtimecache`，定义内部 `Domain`、`Status`、`Item`、`Reference`、`Filter`、`ListRequest`、`PruneRequest`、`RemoveRequest`、`Result`。
2. 实现稳定 `cache_id` 生成和解析。ID 必须包含 domain、driver、kind 和 path/digest/name hash，不允许把用户传入字符串当 filesystem path。
3. 实现 filter：driver、domain/type、status、older-than、cache-id 精确选择。
4. 实现保护规则：`active`、`unknown` 永不可删；`referenced` 默认不可删，只有 `include_referenced` 允许；`unused`、`expired`、`orphaned` 在 `force=true` 时可删。
5. 实现 dry-run：`force=false` 时只返回 matched/skipped，不删除文件。
6. 实现 path safety helper：canonical path 必须位于所属 root 下；删除时不 follow symlink 到 root 外；根目录自身不可删；parent root 删除前后都要验证。
7. 实现 size walk 和 warning 聚合：stat/read/walk 失败转为 item warning，除非根路径不可访问导致 inventory 无法建立。
8. 设计引用源接口，用 fake 支持 running/resuming/stopped session、project/agent image refs、image metadata refs、driver active state。

测试和验证：

- Unit tests 覆盖 `cache_id` 稳定性、重复生成一致性、不同 path/digest 不冲突、非法/未知 `cache_id` 返回 NotFound/invalid。
- Unit tests 覆盖 filter 每个参数：driver、type/domain、status、older-than、组合 filter、空 filter、无匹配项。
- Unit tests 覆盖所有保护状态：active、referenced、unused、expired、orphaned、unknown。
- Unit tests 覆盖 `force=false` dry-run 不删除、`force=true` 删除、`include_referenced=false` 跳过 referenced、`include_referenced=true` 删除 referenced、active/unknown 在 force 下仍跳过。
- Unit tests 覆盖 path traversal、symlink escape、cache_id path 注入、root deletion、broken symlink、permission/stat failure warning。
- Unit tests 覆盖 size walk 成功、size walk 失败 warning、last-used from metadata、mtime fallback 和 `last_used_source=mtime`。

验收标准：

- `pkg/runtimecache` 不导入 `connectrpc.com/connect`，可由 API、CLI 测试直接复用。
- 删除逻辑只能作用于 inventory 生成出的 item path。
- 未知引用或读取失败默认导致 `status=unknown`、`removable=false`，除非该失败只影响 size/warning。

适用 harness 约束或命令：

```bash
go test ./pkg/runtimecache
```

## 阶段 3：materialized image cache inventory 和 prune

目标：覆盖 `<DATA_ROOT>/image-cache` 下 BoxLite OCI layout、Microsandbox rootfs、ready flags、temp dirs，以及 `IMAGE_CACHE_ROOT/metadata.json` 中的 metadata 引用。

依赖：阶段 2 的领域包；`pkg/imagecache.Cache` 的 `MaterializationRoot()`、metadata、lock 和 ready flag helper。

实施工作：

1. 在 `pkg/runtimecache` 中实现 materialized cache scanner，根路径来自 `imagecache.Cache.MaterializationRoot()`。
2. 读取 `IMAGE_CACHE_ROOT/metadata.json`，关联 `layout_cache_path`、`rootfs_cache_path`、`config_digest`、`manifest_digest`、repo tags/digests。
3. 识别 kind：`materialized-oci-layout`、`materialized-rootfs`、`materialized-ready-flag`、`materialized-temp-dir`。
4. 对 `.ready`、`.rootfs.ready`、`oci.tmp`、`rootfs.tmp` 建立 item 或 warnings，确保 temp dir 可被 orphaned/expired prune。
5. 使用 `imagecache.Lock` 或同级 lock 保护 materialized cache 删除。
6. 删除 materialized item 时同步处理 ready flag 和目录一致性：删除 layout/rootfs 时移除对应 ready flag；删除 temp dir 不影响完整 layout/rootfs。
7. 不改变 `pkg/imagecache.Cache.Remove` 和 `agent-compose rmi` 行为。

测试和验证：

- Unit tests 覆盖 metadata 中 layout/rootfs 均存在、仅 layout 存在、仅 rootfs 存在、metadata 指向缺失路径、磁盘有 orphaned image dir。
- Unit tests 覆盖 `.ready`、`.rootfs.ready`、`oci.tmp`、`rootfs.tmp` 的 inventory kind、status、last-used mtime。
- Unit tests 覆盖 referenced materialized cache 默认不可删，`include_referenced` 后可删；orphaned/temp/expired 在 force 下可删。
- Unit tests 覆盖删除 layout 同时删除 `.ready`，删除 rootfs 同时删除 `.rootfs.ready`，删除 temp dir 不删除 sibling layout/rootfs。
- Regression tests 覆盖 `pkg/imagecache.Cache.Remove` 只删除 metadata，不删除 `MaterializationRoot()` 下任何目录，即使 `PruneChildren=true`。

验收标准：

- materialized cache list 能解释每个 item 的 image id/ref 或 orphaned 原因。
- `cache prune --type materialized` 的核心服务调用不会误删 OCI image store root 或其他 image 的完整 cache。

适用 harness 约束或命令：

```bash
go test ./pkg/runtimecache ./pkg/imagecache
```

## 阶段 4：BoxLite 和 Microsandbox driver cache adapter

目标：driver 层提供 cache inventory/removal adapter，并移除启动热路径的隐式清理，同时保留当前 session rollback/stop 清理。

依赖：阶段 2/3；`pkg/driver/boxlite_cache_gc.go`、`pkg/driver/boxlite_cgo.go`、`pkg/driver/microsandbox_runtime.go`。

实施工作：

1. 为 BoxLite 实现 runtime-derived inventory：`BOXLITE_HOME/images/local`、`BOXLITE_HOME/images/disk-images`，以及可通过 BoxLite DB 或运行时目录安全识别的 box/runtime state。
2. 将 `cleanupLegacyBoxliteImageCaches` 从无条件 remove-all helper 改为 inventory-aware removal，或只作为受保护删除函数的内部实现调用。
3. 从 `resolveRootfsPath()` 热路径迁出 `maybeRunCacheGC()`；保留过期 materialized cache 的判断能力，由 `cache prune --older-than` 触发。
4. 为 Microsandbox 实现 session-ephemeral inventory：`MICROSANDBOX_HOME/docker-disks/*.raw`、`MICROSANDBOX_HOME/sandboxes/*`。
5. 从 `prepareEnvironment()` 移除 `gcDockerDisks()` startup sweep；保留 `createSandbox` 失败 rollback 的 `removeDockerDisk(sessionID)`，保留 `StopSession` 对当前 session disk 和 stopped sandbox state 的 best-effort 删除。
6. 删除 Microsandbox orphaned/stopped state 时走 runtimecache path safety 和 driver-specific state cleanup，不直接按 glob 删除。

测试和验证：

- BoxLite unit tests 覆盖 `EnsureSession` 成功路径不调用 legacy cleanup；`resolveRootfsPath()` 不再调用 `maybeRunCacheGC()`；`cleanupLegacyBoxliteImageCaches` 不可被启动路径触发。
- BoxLite unit tests 覆盖 `BOXLITE_HOME/images/local`、`images/disk-images` inventory，active box 引用阻止删除，stopped/orphaned cache 可 dry-run 和 force 删除。
- BoxLite unit tests 覆盖 active BoxLite DB schema 缺失、表缺失、查询失败时状态为 unknown 且不可删。
- Microsandbox unit tests 覆盖 `prepareEnvironment()` 不删除任何已有 `docker-disks/*.raw`。
- Microsandbox unit tests 覆盖 `createSandbox` 失败仍删除当前 session disk，成功后不触发 rollback。
- Microsandbox unit tests 覆盖 `StopSession` 删除当前 session disk 和 stopped sandbox state；删除失败只产生 warning/log，不掩盖原始 stop error。
- Microsandbox unit tests 覆盖 `docker-disks/<session>.raw` 对应 running session 为 active、stopped referenced、metadata 缺失 orphaned。

验收标准：

- 启动或 resume BoxLite/Microsandbox session 不会删除其他 image、其他 session 或全局 cache 项。
- runtime smoke 前置条件仍可通过现有 driver tests 构建。

适用 harness 约束或命令：

```bash
go test ./pkg/driver
go test -tags boxlitecgo ./pkg/driver
```

## 阶段 5：daemon CacheService 和 route 注册

目标：daemon 暴露 v2 `CacheService`，实现 list/inspect/prune/remove，并与 app service graph 集成。

依赖：阶段 1-4；`pkg/agentcompose/api/`、`pkg/agentcompose/app/app.go`。

实施工作：

1. 新增 `pkg/agentcompose/api/cache.go`，负责 proto/domain 映射、参数校验、Connect code 映射。
2. 新增 app 层依赖注入：构造 runtimecache controller，注入 `appconfig.Config`、`sessionstore.Store`、`configstore.ConfigStore`、imagecache cache、runtime driver cache adapters。
3. 在 `pkg/agentcompose/app/app.go` 注册 `agentcomposev2connect.NewCacheServiceHandler`。
4. `ListCaches`：只读 inventory，支持 driver/type/status/older-than filter，返回 warnings。
5. `InspectCache`：按 `cache_id` 精确查找；找不到返回 `connect.CodeNotFound`；引用检查不完整时返回 item + `UNKNOWN`。
6. `PruneCaches`：`force=false` dry-run；`force=true` 删除；批量删除 item-level 失败继续处理并返回 skipped/warnings。
7. `RemoveCache`：默认 dry-run；`force=true` 删除单个 cache id；active/unknown 永远返回 failed precondition 或 skipped 结果。
8. 明确错误映射：invalid filter/duration 为 InvalidArgument，missing cache 为 NotFound，protected delete 为 FailedPrecondition，读取根路径不可用为 Internal/Unavailable。

测试和验证：

- API unit tests 覆盖四个 RPC 的成功路径、invalid argument、not found、protected active/unknown、referenced without include、dry-run、force delete。
- API unit tests 覆盖 proto/domain enum 映射所有 enum 值和 unknown enum 值。
- API unit tests 覆盖 `ListCachesRequest` 每个参数：driver、type/domain、status、older-than、组合 filter。
- API unit tests 覆盖 `PruneCachesRequest` 每个参数：`unused`/status、`orphaned`/status、`older_than_seconds`、`include_referenced`、`force`。
- Integration tests 使用临时 `DATA_ROOT`、`SESSION_ROOT`、fake runtime homes 和 generated Connect client 调用 `CacheService` 四个 RPC。
- App route test 更新 `TestSetupRegistersServiceGraph`，断言 `/agentcompose.v2.CacheService/*` 注册。

验收标准：

- daemon 是唯一删除执行者，CLI 不需要本地路径权限。
- 所有 RPC response 包含足够 warnings/blocked reasons 解释跳过原因。
- `ImageService.RemoveImage` 相关测试仍通过，证明 image domain 未被 CacheService 接管。

适用 harness 约束或命令：

```bash
go test ./pkg/agentcompose/api ./pkg/agentcompose/app ./pkg/runtimecache
```

## 阶段 6：CLI cache 命令组

目标：`cmd/agent-compose/main.go` 新增 `cache` 命令组，所有命令和参数都有文本、JSON、请求映射、错误映射测试。

依赖：阶段 1 和阶段 5；`newCLIServiceClients` 需要新增 CacheService client。

实施工作：

1. 新增 CLI client 字段：`cache agentcomposev2connect.CacheServiceClient`。
2. 新增命令：
   - `agent-compose cache ls`
   - `agent-compose cache inspect <cache-id>`
   - `agent-compose cache prune`
   - `agent-compose cache rm <cache-id>`
3. 通用参数实现：
   - `--driver <boxlite|microsandbox|docker|all>`
   - `--type <oci|materialized|runtime|session>`
   - `--status <active|referenced|unused|expired|orphaned|unknown>`
   - `--json`
4. `cache prune` 参数实现：
   - `--unused`
   - `--orphaned`
   - `--expired`
   - `--older-than <duration>`
   - `--include-referenced`
   - `--force`
5. `cache rm` 实现 `--force`，默认 dry-run。
6. 文本输出：
   - `cache ls` 表头为 `CACHE ID  DRIVER  TYPE  STATUS  REMOVABLE  SIZE  REF/SESSION  PATH`。
   - `cache inspect` 展示完整 item、references、blocked reasons、warnings。
   - `cache prune` 展示 dry-run/removed/skipped/warnings。
   - `cache rm` 展示 dry-run 或删除结果。
7. JSON 输出结构保持稳定，包含 `dry_run`、`matched`、`removed`、`skipped`、`warnings` 等字段。
8. 退出码：无可删项为 0；usage error 为现有 usage code；Connect protected/not found/invalid 按现有 `commandExitErrorForConnect` 映射；dry-run 中有 protected skipped 不应失败。

测试和验证：

- CLI integration tests 覆盖 `cache ls` 文本输出、`--json` 输出、空结果输出。
- CLI integration tests 覆盖 `cache ls --driver` 的所有值：boxlite、microsandbox、docker、all；非法 driver 为 usage error。
- CLI integration tests 覆盖 `cache ls --type` 的所有值：oci、materialized、runtime、session；非法 type 为 usage error。
- CLI integration tests 覆盖 `cache ls --status` 的所有值：active、referenced、unused、expired、orphaned、unknown；非法 status 为 usage error。
- CLI integration tests 覆盖 `cache inspect <cache-id>` 文本、JSON、NotFound、missing arg、extra arg。
- CLI integration tests 覆盖 `cache prune` 默认 dry-run，请求 `force=false`。
- CLI integration tests 覆盖 `cache prune --force` 请求 `force=true`。
- CLI integration tests 覆盖 `cache prune --unused`、`--orphaned`、`--expired`、`--older-than 7d`、`--include-referenced` 的 request 映射。
- CLI integration tests 覆盖互斥/组合规则：`--unused`、`--orphaned`、`--expired` 与 `--status` 的冲突或优先级必须稳定；`--older-than` 非法 duration 为 usage error；负数/零 duration 按实现约定拒绝。
- CLI integration tests 覆盖 `cache rm <cache-id>` dry-run、`--force`、active/unknown protected error、missing arg、extra arg。
- CLI integration tests 覆盖 JSON stdout 不被 warning/deprecated 文案污染；warnings 应在 JSON 字段或 stderr 中按现有 CLI 约定稳定。

验收标准：

- CLI 不调用 filesystem API 读取或删除 daemon cache path。
- 每个命令、每个参数都有至少一个成功或失败测试覆盖。
- 文本输出可读且 JSON 可被 `json.Unmarshal` 解码。

适用 harness 约束或命令：

```bash
go test ./cmd/agent-compose
```

## 阶段 7：端到端集成和非回归

目标：跨 CLI、Connect handler、runtimecache、临时文件树证明完整用户工作流，不依赖真实 BoxLite/Microsandbox。

依赖：阶段 1-6。

实施工作：

1. 增加 in-process daemon/Connect integration tests，使用临时 `DATA_ROOT`、`SESSION_ROOT`、`IMAGE_CACHE_ROOT`、`BOXLITE_HOME`、`MICROSANDBOX_HOME`。
2. 构造 image metadata、materialized layout/rootfs、BoxLite legacy dirs、Microsandbox docker disk/sandbox state。
3. 通过 CLI `cache ls/prune/rm/inspect` 调用 fake/stub daemon 或 in-process app，验证 request/response 和实际文件删除。
4. 保留 `agent-compose rmi` 非回归：删除 image metadata 后 materialized/runtime cache 仍存在。
5. 保留 session lifecycle 非回归：Microsandbox startup 不删除其他 `.raw`；BoxLite image resolution 不触发 TTL prune。

测试和验证：

- Integration tests 覆盖完整 dry-run：先构造 unused/orphaned cache，执行 `cache prune`，断言文件仍存在且输出列出计划。
- Integration tests 覆盖完整 force prune：执行 `cache prune --force`，断言仅目标 item 删除。
- Integration tests 覆盖 running session reference：构造 session metadata/runtime state，断言 `active` item 不可删。
- Integration tests 覆盖 stopped session reference：默认不可删，`--include-referenced --force` 可删。
- Integration tests 覆盖 unknown：模拟 BoxLite DB 读取失败或根路径权限失败，断言不可删且 warning 清晰。
- Integration tests 覆盖 `rmi` 不删除 `image-cache/<image-id>/oci`、`rootfs`、`BOXLITE_HOME/images/*`、`MICROSANDBOX_HOME/docker-disks/*.raw`。

验收标准：

- 完整 workflow 能证明 dry-run、force、保护、warnings 和删除边界。
- 每个阶段结束后 `go test` 对触达包通过。

适用 harness 约束或命令：

```bash
go test ./cmd/agent-compose ./pkg/agentcompose/api ./pkg/agentcompose/app ./pkg/runtimecache ./pkg/driver ./pkg/imagecache
```

## 阶段 8：文档、质量门禁和真实 runtime smoke

目标：文档与实现一致，默认质量门禁和可选 runtime smoke 通过。

依赖：阶段 1-7。

实施工作：

1. 更新 `docs/command-line-manual.md` 和 `docs/zh-CN/command-line-manual.md`，记录 `cache` 命令、参数、dry-run 默认、force 行为和保护状态。
2. 如 `.env.example` 或部署文档提到 `BOX_CACHE_TTL`，更新为“不再驱动启动热路径 GC；用于显式 prune 或后续维护”语义。若仓库无对应配置项文档，不新增无必要环境变量。
3. 更新 `docs/design/agent-compose_design.md` 和中文对应设计文档中的 v2 API/CLI 列表，加入 `CacheService` 和 cache 命令。
4. 检查生成物全部提交：`proto/agentcompose/v2/agentcompose.pb.go`、`proto/agentcompose/v2/agentcomposev2connect/agentcompose.connect.go`、`proto-client/src/**`。
5. 运行局部与全量质量门禁。
6. 在具备 runtime 依赖的环境中运行 opt-in smoke。

测试和验证：

```bash
go test ./cmd/agent-compose ./pkg/agentcompose/api ./pkg/agentcompose/app ./pkg/runtimecache ./pkg/driver ./pkg/imagecache
cd proto-client && npm ci && npm run gen && npm run build
task build
task lint
task test
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
```

验收标准：

- `task lint`、`task build`、`task test` 通过，并满足 `TESTING.md` 中 unit/integration/E2E/combined coverage baseline。
- 若本地缺少 BoxLite/Microsandbox 真实依赖，必须在变更说明中记录未运行的 smoke 命令和原因；默认合并前应在具备依赖的环境补跑。
- 文档清楚说明 `cache prune` 默认 dry-run，真实删除必须 `--force`。

适用 harness 约束或命令：

- `Taskfile.yml`：`task lint`、`task build`、`task test` 是主质量门禁。
- `TESTING.md`：unit/integration/E2E 均至少 60%，combined 至少 70%。
- CI：Go tests、coverage gate、runtime-sdk、scheduler-runtime、proto-client build。

## 风险和停止条件

- 如果新增 proto 后无法稳定生成 Go Connect 或 TS client，停止在阶段 1，不进入实现阶段。
- 如果 runtimecache 删除路径无法证明 canonical path 位于对应 root 下，停止实现删除，只允许 list/inspect dry-run。
- 如果 BoxLite DB schema 与测试假设不一致，不能硬编码 schema 执行删除；该类 item 必须标记 `unknown` 并不可删，直到有兼容读取策略。
- 如果 Microsandbox SDK/daemon 无法可靠区分 running/draining/stopped state，相关 sandbox state 必须标记 `unknown` 或 `active`，不得删除。
- 如果任一 CLI 参数没有测试覆盖，不视为该阶段完成。
- 如果 `task test` coverage 因新增代码低于 baseline，必须先补测试，不得只调整 coverage 排除规则。

## 首版不做事项

- 不实现自动后台周期性 GC。
- 不实现跨节点或多 daemon cache 协调。
- 不提供 UI 页面。
- 不让 `rmi` 默认删除 runtime/materialized cache。
- 不删除 Docker daemon image/container/volume cache。
- 不支持按任意 filesystem path 删除 cache。
- 不优化 `run --command` 跳过 Jupyter readiness。
