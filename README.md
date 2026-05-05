# `exec format error` in `RunNogo` with a darwin_arm64 target

## Symptoms

Building any Go target for `darwin_arm64` — e.g. `//:hello` — with
`--platforms=@rules_go//go/toolchain:darwin_arm64` fails with:

```
nogo: fork/exec bazel-out/darwin_arm64-opt-exec-ST-b85f8706ffbe/bin/nogo_actual_/nogo_actual: exec format error
Target //:hello failed to build
```

The same build succeeds for the default (Linux / RBE) target because in that case
both the `nogo` binary and the `RunNogo` action land on the same Linux exec platform.

## Steps to reproduce

On a macOS host:

```bash
bazel build //:hello \
    --platforms=@rules_go//go/toolchain:darwin_arm64 \
    --verbose_failures
```

Expected output: the target is built successfully.

Actual output (truncated):

```
ERROR: /path/to/exec-platform-test/BUILD.bazel:9:10: Running nogo on //:hello failed: \
    (Exit 1): builder failed: error executing RunNogo command (from target //:hello)
  (cd ... && exec env - \
    CGO_ENABLED=0 GOARCH=arm64 GOOS=darwin ... \
    bazel-out/darwin_arm64-opt-exec-ST-517f3058b798/bin/.../builder nogo \
    ... -nogo bazel-out/darwin_arm64-opt-exec-ST-b85f8706ffbe/bin/nogo_actual_/nogo_actual)
# Execution platform: //platforms:local_fallback
nogo: fork/exec bazel-out/darwin_arm64-opt-exec-ST-b85f8706ffbe/bin/nogo_actual_/nogo_actual: exec format error
Target //:hello failed to build
```

## Dependency chain to the failing action

### 1 → `//:hello` (rule: `go_binary`)

The build is invoked with
`--platforms=@rules_go//go/toolchain:darwin_arm64`, which sets the target
platform directly on the command line.

`go_binary` carries a **rule-level transition** `go_transition` which reads
the platform's `goos`/`goarch` constraints and keeps
`platforms = @rules_go//go/toolchain:darwin_arm64` (idempotent here).

`go_binary` declares `toolchains = [GO_TOOLCHAIN] + CGO_TOOLCHAINS`.
`CGO_TOOLCHAINS` includes `@bazel_tools//tools/cpp:toolchain_type`.

The registered LLVM C++ toolchain for darwin has
`exec_compatible_with = [darwin, arm64]`. With CGO toolchain constraints in
play, Bazel's exec-platform selection finds that only
`//platforms:local_fallback` (the darwin_arm64 host) satisfies all toolchain
requirements.

**Configuration:** target platform = `@rules_go//go/toolchain:darwin_arm64`
**Exec platform (without `--incompatible_auto_exec_groups`):**
`//platforms:local_fallback` (darwin_arm64)

---

### 2 → `@rules_go//:go_context_data` (rule: `go_context_data`)

Edge: implicit attribute `_go_context_data` on every `go_binary`, with
**rule-level transition** `request_nogo_transition` applied to the dependency.

`request_nogo_transition` only sets `//go/private:request_nogo = True`; it
does **not** change `//command_line_option:platforms`. So `go_context_data`
is analysed in the same `darwin_arm64` target configuration.

`go_context_data` declares **only** `toolchains = [GO_TOOLCHAIN]` — no C++
toolchain.

Without the C++ constraint, both the darwin SDK toolchain
(`exec_compatible_with = [darwin, arm64]`) and the linux_amd64 SDK toolchain
(`exec_compatible_with = [linux, amd64]`) are candidates for the
`darwin_arm64` target. Bazel tries exec platforms in the order given by
`--extra_execution_platforms` before `--host_platform`:

1. `//platforms:rbe_linux_platform` → linux SDK's `go_darwin_arm64` has
   `exec_compatible_with = [linux, amd64]` → **match**

`rbe_linux_platform` wins; there is no C++ constraint to veto it.

**Configuration:** target platform = `@rules_go//go/toolchain:darwin_arm64`
**Exec platform:** `//platforms:rbe_linux_platform` (linux/amd64)
(different from `go_binary`'s exec platform despite the identical target
platform, because `go_context_data` lacks the C++ toolchain declaration)

---

### 3 → `//:nogo_actual` (rule: `_nogo`)

Edge: `nogo` attribute on `go_context_data`, with `cfg = "exec"`.

`cfg = "exec"` produces an exec configuration whose **target platform is the
exec platform of `go_context_data`**, i.e. `//platforms:rbe_linux_platform`
(linux/amd64).

On top of this, the `_nogo` rule itself carries a **rule-level transition**
`go_tool_transition`, which resets all `//go/config:*` settings but does
**not** change `//command_line_option:platforms`. The platform remains
`//platforms:rbe_linux_platform`.

With `platforms = rbe_linux_platform`, the linux_amd64 Go SDK
(`rules_go++go_sdk+main___download_0_linux_amd64`) is selected.
Compilation of `nogo_actual` runs on `//platforms:rbe_linux_platform`
with `GOOS=linux GOARCH=amd64`.

**Configuration:** `darwin_arm64-opt-exec-ST-b85f8706ffbe`
**Exec platform:** `//platforms:rbe_linux_platform`
**Output:** `bazel-out/darwin_arm64-opt-exec-ST-b85f8706ffbe/bin/nogo_actual_/nogo_actual` — a **Linux/amd64 ELF**

---

### 4 → `RunNogo` action (inside `//:hello`)

The `RunNogo` action is emitted by `compilepkg.bzl` for every Go package
compiled as part of `//:hello`. It runs the Go SDK `builder`
binary and passes `nogo_actual` via `-nogo`:

```
exec bazel-out/darwin_arm64-opt-exec-ST-517f3058b798/bin/.../builder \
    nogo -src main.go -importpath exec_platform \
    -nogo bazel-out/darwin_arm64-opt-exec-ST-b85f8706ffbe/bin/nogo_actual_/nogo_actual
```

Because `go_binary` has `CGO_TOOLCHAINS`, its exec platform is
`//platforms:local_fallback` (darwin_arm64). Without
`--incompatible_auto_exec_groups`, **all** actions in the rule — including
`RunNogo` — share that single exec platform.

**Exec platform:** `//platforms:local_fallback` (darwin_arm64)
**`nogo_actual` input:** Linux/amd64 ELF (from step 3)
→ `fork/exec … nogo_actual: exec format error`

## Misc - similar issue, but with go binaries using protobuf

A similar issue is encountered with go binaries that depend on a go proto library: `go-protoc-gen` is built for the wrong architecture and a similar `exec format error` is thrown at build time.

To reproduce (on a macOS host):
- Disable `nogo` in `BUILD.bazel` and `MODULE.bazel` (to avoid hitting the nogo issue above)
- Run `bazel build //hello_proto --platforms=@rules_go//go/toolchain:darwin_arm64`
