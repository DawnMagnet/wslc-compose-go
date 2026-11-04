# wslc-compose

[![ci](https://github.com/DawnMagnet/wslc-compose-go/actions/workflows/ci.yml/badge.svg)](https://github.com/DawnMagnet/wslc-compose-go/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/DawnMagnet/wslc-compose-go)](https://github.com/DawnMagnet/wslc-compose-go/releases/latest)
[![license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**English** | [中文](#中文说明)

> Run standard Compose files on **WSL Containers (`wslc`)**.
> A `docker compose`-style experience for `wslc`, until Microsoft ships an official `wslc compose`.

```powershell
wslc-compose up -d        # main command (any shell)
wslc compose up -d        # PowerShell sugar for the same thing
```

`wslc-compose` loads `compose.yaml` with the official parser,
[compose-spec/compose-go v2](https://github.com/compose-spec/compose-go) (interpolation, multi-file merge,
profiles, env_file and extends are all handled upstream), then translates every service into typed
`wslc` command lines.

- **No state file**: all project state lives in container labels (`com.docker.compose.*`), matching Docker Compose.
- **Incremental updates**: driven by a config hash (`com.wslc.compose.config-hash`); unchanged containers are kept.
- **`--dry-run` anywhere**: prints the `wslc` commands it would run, even on machines without wslc.
- **`--strict`**: fields wslc cannot implement become errors instead of warnings.
- **Single binary**: native Windows exe (UPX-packed, ~1.8 MB), or run inside a WSL distro and drive `wslc.exe`.

> Status: preview (v0.1). Unit and golden tests cover every wslc interaction (mocked Runner). Verified on
> Windows 11 + **wslc 3.0.1**: `up`/`up -d`/`down -v`/`ps`/`logs`/`exec`/`run`/`build`/`pull`/`--scale`,
> healthcheck waits, anonymous volumes and **GPU (RTX 5060 Ti, CUDA)**.
> Multi-network attach, static IPv4, `ip_range`, secrets/configs, `build --secret`, `extra_hosts` and
> host-gateway session rules are covered by the mocked Runner and golden files only — they need a
> **wslc 3.0.3.0** (or newer) machine before they can be treated as verified. Please attach `--dry-run` output to issues.

## Contents

- [Install](#install)
- [`wslc compose` (PowerShell sugar)](#wslc-compose-powershell-sugar)
- [Quick start](#quick-start)
- [Command reference](#command-reference)
- [Compose support matrix](#compose-support-matrix)
- [Dry-run example](#dry-run-example)
- [Design notes](#design-notes)
- [Known limitations](#known-limitations)
- [Development](#development)

## Install

Requires Windows 11 + WSL 2.9.3 or later (ships `wslc`; upgrade with `wsl --update`).

### One-line install (recommended)

In PowerShell (5.1 or 7, no admin needed):

```powershell
irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1 | iex
```

The script:

1. detects amd64 / arm64, downloads `wslc-compose-windows-<arch>.exe` from
   [GitHub Releases](https://github.com/DawnMagnet/wslc-compose-go/releases) and verifies it against `checksums.txt`;
2. installs it to `%LOCALAPPDATA%\Programs\wslc-compose\wslc-compose.exe` (re-running upgrades in place);
3. adds that directory to the **user** PATH;
4. dot-sources `wslc-compose.profile.ps1` from the Windows PowerShell 5.1 **and** PowerShell 7 profiles,
   enabling `wslc compose ...` in both.

Open a new terminal, then:

```powershell
wslc-compose version
wslc compose version   # PowerShell only
```

With parameters (`irm | iex` cannot pass them, so use the scriptblock form):

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1))) -Version v0.1.5 -InstallDir D:\tools\wslc-compose
```

| Parameter | Env var (for `irm \| iex`) | Meaning |
|---|---|---|
| `-Version` | `WSLC_COMPOSE_VERSION` | Release tag, default `latest` |
| `-InstallDir` | `WSLC_COMPOSE_INSTALL_DIR` | Install dir, default `%LOCALAPPDATA%\Programs\wslc-compose` |
| `-BaseUrl` | `WSLC_COMPOSE_BASE_URL` | Releases root; point at a mirror or fork |
| `-Arch` | | Force `amd64` / `arm64` |
| `-NoPath` | | Do not modify the user PATH |
| `-NoProfile` | | Do not modify `$PROFILE` (no `wslc compose`) |
| `-Uninstall` | | Remove the install dir, PATH entry and `$PROFILE` line |

> If the execution policy is `Restricted` (the Windows client default), `$PROFILE` is not loaded and the script
> suggests `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`. `wslc-compose` itself is unaffected.

Uninstall:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1))) -Uninstall
```

### Manual download / inside a WSL distro

Every release ships `wslc-compose-{windows,linux}-{amd64,arm64}`, the wrapper scripts and `checksums.txt`.
The amd64 binaries are packed with [UPX](https://upx.github.io/); `upx -d <file>` restores the original if
your antivirus dislikes packed executables. Inside a WSL distro:

```bash
curl -fsSLo ~/.local/bin/wslc-compose \
  https://github.com/DawnMagnet/wslc-compose-go/releases/latest/download/wslc-compose-linux-amd64
chmod +x ~/.local/bin/wslc-compose
```

### Build from source

Requires Go 1.24+:

```bash
go install github.com/DawnMagnet/wslc-compose-go/cmd/wslc-compose@latest
# or
git clone https://github.com/DawnMagnet/wslc-compose-go && cd wslc-compose-go
make build   # host platform -> bin/wslc-compose
make dist    # all platforms + UPX + scripts + checksums.txt -> dist/  (UPX= to skip packing)
```

The `wslc` executable is located via: `--wslc` → `$WSLC_COMPOSE_BIN` → `wslc.exe` / `wslc` on `PATH`
→ `C:\Program Files\WSL\wslc.exe` (`/mnt/c/Program Files/WSL/wslc.exe` inside WSL).

**Inside a WSL distro**, when calling `wslc.exe`, bind-mount sources are converted with `wslpath -w` and
forward slashes (`/mnt/c/src/app` → `C:/src/app`), because wslc treats backslashes as escapes.

## `wslc compose` (PowerShell sugar)

`wslc-compose` is the real command. `wslc` is Microsoft's binary and has no plugin mechanism, so
`wslc compose` is provided by thin wrappers that forward `compose ...` to `wslc-compose` and every other
subcommand unchanged to the real `wslc.exe`:

| Shell | Setup |
|---|---|
| PowerShell | Done by `install.ps1`. Manually: add `. <install dir>\wslc-compose.profile.ps1` to `$PROFILE` |
| bash / zsh (in WSL) | Add `source <path>/wslc-compose.sh` to `~/.bashrc` (needs the Linux `wslc-compose` on PATH) |
| cmd.exe / batch | Put the directory holding `wslc.cmd` **before** `C:\Program Files\WSL` in PATH. The system PATH wins over the user PATH, so this needs admin; just use `wslc-compose` instead |

The PowerShell wrapper is intentionally *not* called `wslc-compose.ps1`: PowerShell prefers `.ps1` over `.exe`
for the same name, so such a file would shadow `wslc-compose.exe` (the v0.1.0 bug).

```powershell
wslc compose up -d
wslc compose logs -f web
wslc compose down -v
```

When an official `wslc compose` ships, drop the wrapper; labels already match Docker Compose.

## Quick start

```yaml
# compose.yaml
services:
  web:
    build: .
    ports: ["8080:80"]
    depends_on:
      db: { condition: service_healthy }
  db:
    image: postgres:16-alpine
    environment: { POSTGRES_PASSWORD: example }
    volumes: [dbdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
volumes:
  dbdata: {}
```

```powershell
wslc-compose up -d --dry-run   # preview the wslc commands
wslc-compose up -d             # networks/volumes, build, start in dependency order
wslc-compose ps
wslc-compose logs -f
wslc-compose exec db psql -U postgres
wslc-compose down -v
```

## Command reference

Global flags (before or after the subcommand):

| Flag | Meaning |
|---|---|
| `-f, --file` | Compose file, repeatable (also `COMPOSE_FILE`) |
| `-p, --project-name` | Project name (also `COMPOSE_PROJECT_NAME`; default `name:` or directory name) |
| `--project-directory` | Working directory |
| `--profile` | Enable a profile, repeatable (also `COMPOSE_PROFILES`) |
| `--env-file` | Interpolation env file (default `.env`) |
| `--dry-run` | Print wslc commands only (commands on stdout, progress on stderr) |
| `--strict` | Treat unsupported fields as errors |
| `--wslc` | Path to the wslc executable |
| `--default-dns` | DNS injected when a service has no `dns:`; default `1.1.1.1`, `--default-dns=` disables |
| `--parallel` | Concurrency for wslc queries (default 1, see [Design notes](#design-notes)) |
| `--ansi` | Colourised log prefixes: `auto`/`always`/`never` (honours `NO_COLOR`) |

| Command | Main flags | Behaviour |
|---|---|---|
| `up [SERVICE...]` | `-d` `--build` `--no-build` `--pull` `--force-recreate` `--no-recreate` `--no-deps` `--remove-orphans` `--scale SVC=N` `--wait` `--wait-timeout` `-t` | Create networks/volumes → build/pull → converge containers in dependency order; attaches logs without `-d` (Ctrl+C stops). `--scale` overrides `scale`/`deploy.replicas` (repeatable, may be 0). Rolls back resources created by this run on failure |
| `down` | `-v` `--remove-orphans` `-t` | Stop and remove containers in reverse dependency order, remove project networks; `-v` removes declared named volumes (not external ones) |
| `ps [SERVICE...]` | `-a` `-q` `--services` `--status STATE` (repeatable, implies `-a`) `--format table\|json` | List project containers; running containers with a healthcheck get a `HEALTH` column |
| `logs [SERVICE...]` | `-f` `-n/--tail` `-t` `--since` `--until` `--no-log-prefix` | Merged multi-container logs with `name |` prefixes |
| `build [SERVICE...]` | `--no-cache` `--pull` | Build every service with `build:` |
| `pull [SERVICE...]` | `--ignore-pull-failures` | Pull images (each image once) |
| `config` | `--services` `--volumes` `--networks` `--images` `--hash "*"` `--format yaml\|json` | Print the normalised model or a projection; list modes print names only |
| `exec SERVICE [--] CMD...` | `-i` (default on) `-t` `-T` `-d` `-u` `-w` `-e` `--index` | Run a command in a running container; TTY follows stdin; a leading `--` is stripped (same for `run`) |
| `run SERVICE [CMD...]` | `--rm` `-d` `--name` `--no-deps` `--service-ports` `-T` `-u` `-w` `-e` `--entrypoint` `--build` | One-off container (`oneoff=True` label); dependencies are started first |
| `start` / `stop` / `restart` | `-t` | wslc has no restart; `restart` = stop + start |
| `version` | `--short` | Show own and wslc versions |

## Compose support matrix

**✅ Mapped**

| Compose field | wslc flags |
|---|---|
| `name`, `container_name`, `deploy.replicas`/`scale` | `--name <project>-<service>-<n>` or `container_name` |
| `image`, `build.context/dockerfile/args/target/labels/tags/no_cache/pull` | `wslc build -t … -f … --build-arg … --target … -l … --no-cache --pull` |
| `command`, `entrypoint` | `--entrypoint <first>` + image + remaining items + command |
| `environment`, `env_file` | `-e K=V` (env_file merged by compose-go, sorted by key) |
| `ports` (host_ip, ranges, udp) | `-p [ip:]host:container[/udp]` |
| `volumes` bind / named / anonymous / tmpfs (with size), `tmpfs`, read-only mounts | `-v` / `--tmpfs`; bind sources become Windows forward-slash paths. wslc rejects bare paths, so anonymous volumes (`- /data`) become project-scoped named volumes `<project>_<service>_<path>_anon` (kept across recreation, removed by `down -v`) |
| `networks` (default `<project>_default`, `aliases`, `priority`, `ipv4_address`), `network_mode: none` | one network: `run --network --ip --network-alias`. Several: `create` on the highest-priority network, `network connect` the rest (`--ip`, `--network-alias`, `--link-local-ip`, `--driver-opt`), then `start` |
| Top-level `networks`: `driver`, `internal`, `ipam.config[0].subnet/gateway/ip_range`, `driver_opts`, `labels`, `external`, `name` | `wslc network create --subnet --gateway --ip-range -o …` (name prefixed with the project unless `external` or `name:`) |
| `secrets` / `configs` (`file`, `environment`, `content`, `target`; short and long syntax) | read-only `-v` at `/run/secrets/<name>` or `/<name>` (or `target`). `environment` / `content` are written under the project state dir and removed by `down` |
| `build.secrets` | `wslc build --secret id=<target\|source>,src=<path>` when wslc is 3.0.3.0 or newer |
| `extra_hosts`, including `host-gateway` | `wslc exec -u 0` rewrites marked `/etc/hosts` lines after start. `host-gateway` is the container's default gateway. Published-port hairpin (DNAT/MASQUERADE) runs via `wslc system session run` on wslc 3.0.3.0+ and is removed by `down` |
| Top-level `volumes`: `name`, `external`, `labels` | `wslc volume create -l com.docker.compose.project=… -l com.docker.compose.volume=…` |
| `working_dir`, `user`, `hostname`, `domainname`, `labels` | `-w` `-u` `-h` `--domainname` `-l` |
| `dns`, `dns_search`, `dns_opt` | `--dns` `--dns-search` `--dns-option` |
| `mem_limit` / `deploy.resources.limits.memory`, `cpus` / `limits.cpus`, `shm_size` | `-m 512M` (upper-case units) `--cpus` `--shm-size` |
| `ulimits`, `stop_signal`, `stop_grace_period` | `--ulimit` `--stop-signal` `--stop-timeout` |
| `gpus`, `deploy.resources.reservations.devices[capabilities: gpu]` | `--gpus all` |
| `tty`, `stdin_open` | `-t` `-i` |
| `depends_on` (started / healthy / completed_successfully / required) | dependency order + polling `wslc inspect` |
| `profiles`, `pull_policy` (always/missing/never/build) | service selection / image strategy |
| Interpolation, `.env`, multiple `-f`, `extends`, `include` | handled natively by compose-go |

**ℹ️ Mapped (verified on wslc 3.0.1)**

| Field | Notes |
|---|---|
| `healthcheck` | `--health-cmd/--health-interval/--health-timeout/--health-start-period/--health-retries`, `--no-healthcheck`; `CMD` form is shell-quoted. `ps` reads `HealthStatus` from `wslc list` (falls back to `inspect`) |
| `depends_on: service_healthy` | Polls `State.Health.Status` from `wslc inspect`; if wslc never reports health, after 3 polls "running" counts as healthy, with a warning |
| `ports[].mode: host` | Published as a normal port (INFO, does not trip `--strict`) |

### Example

Networks, a generated secret and host-gateway in one file. `wslc-compose up -d --dry-run` prints the `create` / `network connect` / `start` sequence, the read-only secret mount and the session rules (the last only when wslc is 3.0.3.0 or newer, or when `--dry-run` cannot see a binary and assumes that version).

```yaml
services:
  web:
    image: nginx:alpine
    ports: ["8080:80"]
    extra_hosts: ["host.docker.internal:host-gateway"]
    secrets: [token]
    networks:
      front:
        ipv4_address: 172.28.0.10
      back:
        priority: 10
secrets:
  token:
    environment: TOKEN   # written under the project state dir, mounted at /run/secrets/token
networks:
  front:
    ipam:
      config:
        - subnet: 172.28.0.0/24
          gateway: 172.28.0.1
          ip_range: 172.28.0.128/25
  back: {}
```

**Partial** (implemented, with a warning where wslc cannot match Compose; warnings are errors under `--strict`)

| Field | What happens |
|---|---|
| `secrets`/`configs` `uid`/`gid` | Bind mounts cannot set them; the file is still mounted |
| `secrets`/`configs` `mode` on a `file:` source | Host file mode is kept. `mode` on `environment`/`content` is applied to the generated file |
| `external: true` secrets/configs | Mount is skipped |
| `ipv6_address`, `enable_ipv6` | wslc networks are IPv4-only |
| Primary endpoint `driver_opts` / `link_local_ips` | No `wslc run` flag; applied only when that network is added with `network connect` |
| `ipam.config` past the first entry, `aux_addresses`, custom IPAM driver/options | First subnet/gateway/`ip_range` only |
| `extra_hosts` | Applied after start, so the entrypoint may already have read `/etc/hosts` (INFO, does not trip `--strict`) |
| `host-gateway` published-port loopback on wslc &lt; 3.0.3.0 | `/etc/hosts` is still updated; session DNAT is skipped |
| `build.secrets` on wslc &lt; 3.0.3.0 | Skipped |
| Multi-network, `--ip`, `--ip-range` on wslc &lt; 3.0.3.0 | Only the highest-priority network is attached, without a static address |
| Endpoint `mac_address`, `gw_priority`, `interface_name` | Ignored |
| Host port ranges in host-gateway DNAT | Skipped (the published port is chosen at run time and is not a stable DNAT target) |

**⚠️ Ignored with a warning** (errors under `--strict`)

`restart`, `deploy.restart_policy`, `privileged`, `cap_add`/`cap_drop`, `devices`, `device_cgroup_rules`,
`security_opt`, `sysctls`, `logging`, `platform`, `init`, `read_only`,
`ipc`, `pid`, `uts`, `userns_mode`, `group_add`, `cgroup`/`cgroup_parent`, `runtime`, `isolation`, `mac_address`,
`links`/`external_links`, `volumes_from`, `storage_opt`, `oom_*`, `pids_limit`, `blkio_config`,
`cpu_shares`/`cpu_quota`/`cpuset` etc., `mem_reservation`/`memswap_limit`, `post_start`/`pre_stop`, `develop` (watch),
`provider`, `models`, `use_api_socket`, `network_mode: host|service:|container:`,
volume `nocopy`/`subpath`, npipe/image/cluster volume types,
`build.ssh`/`platforms`/`cache_from`/`cache_to`/`additional_contexts`/`network`/`extra_hosts`/`dockerfile_inline`,
`healthcheck.start_interval`, non-bridge network drivers, volume drivers and options,
`deploy.resources.reservations` (memory).

wslc's container `run` argument list has no flags for capabilities, devices, sysctls, a log driver, `--init`,
`--privileged` or `--read-only` (tracked upstream, for example [WSL#41181](https://github.com/microsoft/WSL/issues/41181)).
Those fields stay warnings rather than guesses.

**❌ Unsupported commands**: `watch`, `attach`, `cp`, `top`, `pause`/`unpause`, `port`, `events`, `images`,
`scale` (use `up --scale`), `kill`, `rm`, `create`, `push`, `ls` (multi-project).

## Dry-run example

```console
$ wslc-compose -f minimal.yaml --dry-run up -d
wslc.exe network create -l com.docker.compose.network=default -l com.docker.compose.project=minimal minimal_default
wslc.exe pull nginx:alpine
wslc.exe run -d --name minimal-hello-1 -l com.docker.compose.container-number=1 -l com.docker.compose.oneoff=False -l com.docker.compose.project=minimal -l com.docker.compose.project.config_files=C:/src/demo/minimal.yaml -l com.docker.compose.project.working_dir=C:/src/demo -l com.docker.compose.service=hello -l com.wslc.compose.config-hash=<sha256> -l com.wslc.compose.version=dev -p 8080:80 --network minimal_default --network-alias hello --dns 1.1.1.1 nginx:alpine

$ wslc-compose --dry-run down -v
wslc.exe network remove minimal_default
```

With wslc present, dry-run **really queries** current state (list/inspect) and only skips mutations, so it
previews exactly which containers are kept or recreated. Without wslc it assumes an empty state.
See [`testdata/golden/`](testdata/golden); `up_full.golden` covers nearly every mapped field.

## Design notes

- **Project state = container labels.** Each container carries `com.docker.compose.project/service/container-number/oneoff/
  project.working_dir/project.config_files` plus `com.wslc.compose.config-hash`, `com.wslc.compose.version` and,
  when the service uses `host-gateway`, `com.wslc.compose.host-gateway=v1`.
  `ps`/`down` rely only on `wslc list --filter label=...`, falling back to per-container `inspect` if labels are missing.
  The exception is generated secret/config files (`environment:` and `content:`), which live under
  `$XDG_CONFIG_HOME/wslc-compose/<project>` (or `%LOCALAPPDATA%\wslc-compose\<project>` on Windows) and are deleted by `down`.
  `file:` sources are mounted in place and are not copied.
- **Config hash.** JSON + SHA-256 of the service config, excluding `build`, `pull_policy`, `scale`/`replicas`,
  `depends_on` and `profiles`, so changing replica counts or dependencies does not recreate containers.
  A service whose wslc command is unchanged from v0.1.5 keeps that historical digest (`minimal.yaml` hello is
  `0df62ea0…`), so upgrading does not recreate it. Schema version 2 and the `3.0.3` tier are mixed in only when
  the service uses a feature whose command changed (secrets, configs, extra_hosts, or, on wslc 3.0.3.0, multiple
  networks, static IPv4, `ip_range`, secondary endpoint options, `build.secrets`).
  Services rebuilt by `up --build` are always recreated. Inspect with `wslc-compose config --hash "*"`.
- **Default DNS 1.1.1.1.** The wslc utility VM forwards DNS to the Windows host resolver; on many LAN/corporate
  networks containers then get `SERVFAIL` for public names (apt/npm/pip fail). Services without `dns:` therefore
  get `--dns 1.1.1.1`; for internal names set `dns:`, `--default-dns=10.0.0.53`, or disable with `--default-dns=`.
- **Paths.** On Windows everything becomes `C:/x/y`; inside WSL `/mnt/c/...` is converted directly and other paths go
  through `wslpath -w` (`//wsl.localhost/<distro>/...`). Relative Dockerfiles are resolved against the build context,
  because wslc resolves `-f` relative to its own working directory.
- **Upper-case memory units.** wslc rejects `512m` and only accepts `512M`, so all byte sizes use `K/M/G`.
- **Serialised + retried.** The wslc preview may return `ERROR_SHARING_VIOLATION` under concurrent calls and
  `ERROR_ALREADY_EXISTS` when recreating a just-stopped container; short commands run serially and those errors are
  retried up to 5 times (2 s apart). `pull` also retries registry hiccups (`EOF`, timeouts, resets, 429/502/503).
  wslc error output already streamed live is not repeated in the final error.
- **Rollback on failure.** If `up` fails midway (pull/build/start failure, unhealthy dependency), containers,
  networks and volumes created **by this run** are removed in reverse order; pre-existing resources are untouched.
  Failures in the `--wait` phase are not rolled back, so logs stay inspectable.
- **Deterministic order.** Stable topological sort (ties by name); dry-run output and golden tests are reproducible.

Package layout: [docs/architecture.md](docs/architecture.md).

## Known limitations

- wslc lacks key Compose capabilities: restart policies, `--privileged`, `--cap-add`, `--device`, `--sysctl`,
  `--platform`, `--network host`, log drivers, `--init`. These fields are ignored with a warning.
- **Several networks** need wslc 3.0.3.0 (`create`, `network connect`, `start`). Older builds attach only the
  highest-priority network and warn. `wslc run` still accepts a single `--network`, which is why secondary
  endpoints are connected before `start`. That ordering is taken from the reference frontend and has not been
  re-checked on a live wslc in this repository.
- **host-gateway published ports** need `wslc system session run` plus iptables inside the session VM (wslc 3.0.3.0).
  The chain names (`WCGP_`/`WCGS_`/`WCGF_` plus a project hash) match bacarndiaye/wslc-compose so the two tools
  can reconcile one project. `/etc/hosts` is updated after start on every wslc that can `exec`.
- `wslc inspect` output is OCI-style rather than Docker-style and is parsed tolerantly; if your wslc version
  renames fields, `ps` status/ports or `service_healthy` waits may degrade (with a warning).
- Paths inside a WSL distro (not under `/mnt/<drive>`) become `//wsl.localhost/...` UNC paths, whose acceptance
  depends on the wslc version; keep projects on a Windows drive.
- Environment variables without a value (`environment: [TOKEN]` with nothing set on the host) are skipped, because
  the wslc process runs on the Windows side.
- Anonymous volumes become per-service named volumes, so replicas of a service **share** one (Docker gives each container its own).
- wslc's `--gpus` only accepts `all`; GPU `count` / `device_ids` are treated as all GPUs.
- `--entrypoint` overrides (`run`) are split on whitespace; quoting is not supported.
- `scripts/wslc.cmd` does not handle complex quoted arguments; use `wslc-compose` directly.

## Development

```bash
make test     # unit + golden tests (no real wslc; Runner is mocked)
make race     # race detector
make cover    # coverage
make golden   # rewrite testdata/golden after an intended output change
make lint     # gofmt + go vet
make dist     # all binaries (amd64 UPX-packed) + scripts + checksums.txt
```

Layout:

```
cmd/wslc-compose/     entry point (signals, exit-code passthrough)
internal/cli/         cobra commands (argument parsing only)
internal/app/         use cases (up/down/ps/logs/exec/run/...)
internal/project/     compose-go loading, field policy, config hash
internal/translate/   ServiceConfig -> wslc argv (pure)
internal/plan/        desired vs actual -> actions; dependency order (pure)
internal/wslc/        Runner interface, typed client, dry-run, output parsing
internal/labels/      label keys and helpers
internal/paths/       Windows/WSL path conversion
internal/logs/        prefixed log multiplexing
internal/golden/      test helper (golden file comparison)
testdata/compose/     compose files used by tests
testdata/golden/      locked wslc command sequences
scripts/              install.ps1 and the `wslc compose` wrappers
.github/workflows/    ci.yml (test + build), release.yml (tag → release)
```

Adding a field mapping:

1. Implement it in `internal/translate` and use the field in `testdata/compose/full.yaml`;
2. If `internal/project/policy.go` warned about it, remove that rule;
3. Run `make golden` and review the diff;
4. Update the support matrix in this README (both languages).

Run `make lint race` before submitting. Real `wslc --version` and `wslc <cmd> --help` output is welcome for fixing mappings.

Releasing: write `docs/release-notes/vX.Y.Z.md`, then `git tag vX.Y.Z && git push origin vX.Y.Z`.
`release.yml` tests, cross-compiles, packs with UPX and publishes the GitHub Release (with `checksums.txt`).
Releases are published only by CI.

## License

[MIT](LICENSE)

---

<a id="中文说明"></a>

# 中文说明

[English](#wslc-compose) | **中文**

> 用标准 Compose 文件驱动 **WSL Containers（`wslc`）** 的 Go 实现。
> 在微软官方 `wslc compose` 发布之前，提供 `docker compose` 风格的无缝体验。

```powershell
wslc-compose up -d        # 主命令（任意 shell）
wslc compose up -d        # PowerShell 语法糖，等价
```

`wslc-compose` 用官方解析器 [compose-spec/compose-go v2](https://github.com/compose-spec/compose-go)
加载 `compose.yaml`（插值、多文件合并、profiles、env_file、extends 全部由官方实现处理），
再把每个服务翻译成类型化的 `wslc` 命令行调用。

- **零状态文件**：项目状态全部存放在容器标签上（`com.docker.compose.*`），与 Docker Compose 标签对齐。
- **增量更新**：基于配置哈希（`com.wslc.compose.config-hash`），配置不变的容器不会被重建。
- **随时 `--dry-run`**：打印将要执行的 `wslc` 命令；即使本机没有 wslc 也能运行。
- **`--strict`**：遇到 wslc 无法实现的字段直接报错，而不是静默忽略。
- **单一可执行文件**：Windows 原生 exe（UPX 压缩，约 1.8 MB），或在 WSL 发行版内运行并自动调用 `wslc.exe`。

> 状态：预览（v0.1）。单元/golden 测试覆盖全部 wslc 交互（mock Runner）；并已在 Windows 11 +
> **wslc 3.0.1** 真机上回归 `up`/`up -d`/`down -v`/`ps`/`logs`/`exec`/`run`/`build`/`pull`/`--scale`、
> 健康检查等待、匿名卷与 **GPU（RTX 5060 Ti，CUDA）**。
> 多网络接入、静态 IPv4、`ip_range`、secrets/configs、`build --secret`、`extra_hosts` 与 host-gateway
> 会话规则目前只由 mock Runner 和 golden 文件覆盖，需要在 **wslc 3.0.3.0**（或更新）的真机上才能算验证过。
> 遇到差异欢迎附 `--dry-run` 输出提 issue。

---

## 目录

- [安装](#安装)
- [`wslc compose`（PowerShell 语法糖）](#wslc-composepowershell-语法糖)
- [快速开始](#快速开始)
- [命令参考](#命令参考)
- [Compose 字段支持矩阵](#compose-字段支持矩阵)
- [dry-run 示例](#dry-run-示例)
- [设计要点](#设计要点)
- [已知限制](#已知限制)
- [开发与贡献](#开发与贡献)

## 安装

需要 Windows 11 + WSL 2.9.3 及以上（自带 `wslc`，可用 `wsl --update` 升级）。

### 一键安装（推荐）

在 PowerShell（5.1 或 7 均可，无需管理员）中运行：

```powershell
irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1 | iex
```

脚本会：

1. 自动识别 amd64 / arm64，从 [GitHub Releases](https://github.com/DawnMagnet/wslc-compose-go/releases) 下载最新的
   `wslc-compose-windows-<arch>.exe`，并用 `checksums.txt` 校验 SHA-256；
2. 安装到 `%LOCALAPPDATA%\Programs\wslc-compose\wslc-compose.exe`（覆盖旧版本即为升级）；
3. 把该目录加入**用户** PATH；
4. 在 Windows PowerShell 5.1 **和** PowerShell 7 的 profile 中各加一行 dot-source `wslc-compose.profile.ps1`，
   让两种 PowerShell 里 `wslc compose ...` 都直接可用。

重新打开终端后：

```powershell
wslc-compose version
wslc compose version   # 仅 PowerShell
```

带参数安装（`irm | iex` 无法传参，用 scriptblock 形式）：

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1))) -Version v0.1.5 -InstallDir D:\tools\wslc-compose
```

| 参数 | 环境变量（适用于 `irm \| iex`） | 说明 |
|---|---|---|
| `-Version` | `WSLC_COMPOSE_VERSION` | 指定版本标签，默认 `latest` |
| `-InstallDir` | `WSLC_COMPOSE_INSTALL_DIR` | 安装目录，默认 `%LOCALAPPDATA%\Programs\wslc-compose` |
| `-BaseUrl` | `WSLC_COMPOSE_BASE_URL` | Releases 根地址，可换成镜像或 fork |
| `-Arch` | | 强制 `amd64` / `arm64` |
| `-NoPath` | | 不修改用户 PATH |
| `-NoProfile` | | 不修改 `$PROFILE`（不启用 `wslc compose`） |
| `-Uninstall` | | 删除安装目录、PATH 条目和 `$PROFILE` 中的那一行 |

> 若 PowerShell 执行策略为 `Restricted`（Windows 客户端默认），`$PROFILE` 不会被加载，脚本会提示运行
> `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`。`wslc-compose` 命令本身不受影响。

卸载：

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1))) -Uninstall
```

### 手动下载 / WSL 发行版内使用

每个 Release 都附带 `wslc-compose-{windows,linux}-{amd64,arm64}` 二进制、包装脚本和 `checksums.txt`。
amd64 二进制经 [UPX](https://upx.github.io/) 压缩；若杀毒软件误报，可用 `upx -d <文件>` 还原。
在 WSL 发行版内：

```bash
curl -fsSLo ~/.local/bin/wslc-compose \
  https://github.com/DawnMagnet/wslc-compose-go/releases/latest/download/wslc-compose-linux-amd64
chmod +x ~/.local/bin/wslc-compose
```

### 从源码构建

需要 Go 1.24+：

```bash
go install github.com/DawnMagnet/wslc-compose-go/cmd/wslc-compose@latest
# 或
git clone https://github.com/DawnMagnet/wslc-compose-go && cd wslc-compose-go
make build   # 本机平台 -> bin/wslc-compose
make dist    # 全部平台 + UPX 压缩 + 包装脚本 + checksums.txt -> dist/（UPX= 跳过压缩）
```

`wslc` 可执行文件的查找顺序：`--wslc` 参数 → `$WSLC_COMPOSE_BIN` → `PATH` 中的 `wslc.exe` / `wslc`
→ `C:\Program Files\WSL\wslc.exe`（WSL 内为 `/mnt/c/Program Files/WSL/wslc.exe`）。

**在 WSL 发行版里运行时**，若调用的是 `wslc.exe`，bind mount 路径会自动经 `wslpath -w` 转换并改为正斜杠
（`/mnt/c/src/app` → `C:/src/app`），因为 wslc 把反斜杠视为转义字符。

## `wslc compose`（PowerShell 语法糖）

主命令是 `wslc-compose`。`wslc` 是微软的二进制，无法注册子命令，因此提供三个轻量包装：`compose ...` 转给 `wslc-compose`，其他子命令原样转发给真正的 `wslc.exe`：

| 环境 | 做法 |
|---|---|
| PowerShell | `install.ps1` 已自动完成；手动方式：在 `$PROFILE` 中加入 `. <安装目录>\wslc-compose.profile.ps1` |
| bash / zsh（WSL 内） | 在 `~/.bashrc` 中加入 `source <path>/wslc-compose.sh`（需 PATH 中有 Linux 版 `wslc-compose`） |
| cmd.exe / 批处理 | 把 `wslc.cmd` 所在目录放到 PATH 中 `C:\Program Files\WSL` **之前**。系统 PATH 优先于用户 PATH，因此需要管理员把它加进系统 PATH；一般直接用 `wslc-compose` 更省事 |

三个脚本都在仓库 `scripts/` 下，也作为 Release 附件提供。PowerShell 包装刻意不叫 `wslc-compose.ps1`：同名时 PowerShell 优先 `.ps1`，会挡住 `wslc-compose.exe`（v0.1.0 的问题）。

之后即可：

```powershell
wslc compose up -d
wslc compose logs -f web
wslc compose down -v
```

当官方 `wslc compose` 发布后，删掉包装即可无缝切换——标签与 Docker Compose 保持一致。

## 快速开始

```yaml
# compose.yaml
services:
  web:
    build: .
    ports: ["8080:80"]
    depends_on:
      db: { condition: service_healthy }
  db:
    image: postgres:16-alpine
    environment: { POSTGRES_PASSWORD: example }
    volumes: [dbdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
volumes:
  dbdata: {}
```

```powershell
wslc-compose up -d --dry-run   # 先看要执行的 wslc 命令
wslc-compose up -d             # 创建网络/卷，构建镜像，按依赖顺序启动
wslc-compose ps
wslc-compose logs -f
wslc-compose exec db psql -U postgres
wslc-compose down -v
```

## 命令参考

全局参数（放在子命令前后均可）：

| 参数 | 说明 |
|---|---|
| `-f, --file` | Compose 文件，可多次指定（亦支持 `COMPOSE_FILE`） |
| `-p, --project-name` | 项目名（亦支持 `COMPOSE_PROJECT_NAME`，默认 `name:` 或目录名） |
| `--project-directory` | 工作目录 |
| `--profile` | 启用 profile，可多次（亦支持 `COMPOSE_PROFILES`） |
| `--env-file` | 插值用的环境文件（默认读取 `.env`） |
| `--dry-run` | 只打印 wslc 命令（输出到 stdout，进度信息到 stderr） |
| `--strict` | 不支持的字段视为错误 |
| `--wslc` | wslc 可执行文件路径 |
| `--default-dns` | 服务未声明 `dns:` 时注入的 DNS，默认 `1.1.1.1`；`--default-dns=` 关闭 |
| `--parallel` | wslc 查询并发数（默认 1，见[设计要点](#设计要点)） |
| `--ansi` | 日志前缀着色：`auto`/`always`/`never`（遵循 `NO_COLOR`） |

| 命令 | 主要参数 | 行为 |
|---|---|---|
| `up [SERVICE...]` | `-d` `--build` `--no-build` `--pull` `--force-recreate` `--no-recreate` `--no-deps` `--remove-orphans` `--scale SVC=N` `--wait` `--wait-timeout` `-t` | 创建网络/卷 → 构建/拉取 → 依赖顺序收敛容器；非 `-d` 时附着日志，Ctrl+C 停止。`--scale` 覆盖 `scale`/`deploy.replicas`（可多次、可为 0）。失败时回滚本次新建的容器/网络/卷 |
| `down` | `-v` `--remove-orphans` `-t` | 逆依赖顺序停止并删除容器，删除项目网络；`-v` 删除声明的命名卷（external 不动） |
| `ps [SERVICE...]` | `-a` `-q` `--services` `--status STATE`（可多次，隐含 `-a`） `--format table\|json` | 列出项目容器；声明了 healthcheck 的运行中容器会额外显示 `HEALTH` 列 |
| `logs [SERVICE...]` | `-f` `-n/--tail` `-t` `--since` `--until` `--no-log-prefix` | 多容器日志合并，带 `name |` 前缀 |
| `build [SERVICE...]` | `--no-cache` `--pull` | 构建所有带 `build:` 的服务 |
| `pull [SERVICE...]` | `--ignore-pull-failures` | 拉取镜像（同一镜像只拉一次） |
| `config` | `--services` `--volumes` `--networks` `--images` `--hash "*"` `--format yaml\|json` | 输出规范化模型或其投影；列表模式只输出名字（不打印 INFO/WARN） |
| `exec SERVICE [--] CMD...` | `-i`(默认开) `-t` `-T` `-d` `-u` `-w` `-e` `--index` | 在运行中的容器里执行命令；TTY 默认随 stdin 是否为终端；`--` 分隔符会被去掉（`run` 同理） |
| `run SERVICE [CMD...]` | `--rm` `-d` `--name` `--no-deps` `--service-ports` `-T` `-u` `-w` `-e` `--entrypoint` `--build` | 一次性容器（`oneoff=True` 标签），默认先拉起依赖 |
| `start` / `stop` / `restart` | `-t` | wslc 没有 restart，`restart` = stop + start |
| `version` | `--short` | 显示自身与 wslc 版本 |

## Compose 字段支持矩阵

**✅ 已映射**

| Compose 字段 | wslc 参数 |
|---|---|
| `name`、`container_name`、`deploy.replicas`/`scale` | `--name <project>-<service>-<n>` 或 `container_name` |
| `image`、`build.context/dockerfile/args/target/labels/tags/no_cache/pull` | `wslc build -t … -f … --build-arg … --target … -l … --no-cache --pull` |
| `command`、`entrypoint` | `--entrypoint <首项>` + 镜像 + 其余项 + command |
| `environment`、`env_file` | `-e K=V`（env_file 由 compose-go 合并，按键排序） |
| `ports`（含 host_ip、范围展开、udp） | `-p [ip:]host:container[/udp]` |
| `volumes` bind / 命名卷 / 匿名卷 / tmpfs（含 size）、`tmpfs`、`read_only` 挂载 | `-v` / `--tmpfs`，bind 源路径转换为 Windows 正斜杠形式。wslc 不接受裸路径，匿名卷（`- /data`）自动改写为项目内命名卷 `<project>_<service>_<path>_anon`（重建容器时数据保留，`down -v` 删除） |
| `networks`（默认网络 `<project>_default`、`aliases`、`priority`、`ipv4_address`）、`network_mode: none` | 单网络：`run --network --ip --network-alias`。多网络：在优先级最高的网络上 `create`，对其余网络 `network connect`（`--ip`、`--network-alias`、`--link-local-ip`、`--driver-opt`），再 `start` |
| 顶层 `networks`：`driver`、`internal`、`ipam.config[0].subnet/gateway/ip_range`、`driver_opts`、`labels`、`external`、`name` | `wslc network create --subnet --gateway --ip-range -o …`（除非 `external` 或 `name:`，否则名称加项目前缀） |
| `secrets` / `configs`（`file`、`environment`、`content`、`target`；长短语法） | 只读 `-v`，路径为 `/run/secrets/<name>` 或 `/<name>`（或 `target`）。`environment` / `content` 写到项目状态目录，`down` 时删除 |
| `build.secrets` | wslc 为 3.0.3.0 或更新时：`wslc build --secret id=<target\|source>,src=<path>` |
| `extra_hosts`，含 `host-gateway` | 启动后用 `wslc exec -u 0` 重写带标记的 `/etc/hosts` 行。`host-gateway` 取容器默认网关。已发布端口的回环（DNAT/MASQUERADE）在 wslc 3.0.3.0+ 上通过 `wslc system session run` 安装，`down` 时删除 |
| 顶层 `volumes`：`name`、`external`、`labels` | `wslc volume create -l com.docker.compose.project=… -l com.docker.compose.volume=…` |
| `working_dir`、`user`、`hostname`、`domainname`、`labels` | `-w` `-u` `-h` `--domainname` `-l` |
| `dns`、`dns_search`、`dns_opt` | `--dns` `--dns-search` `--dns-option` |
| `mem_limit` / `deploy.resources.limits.memory`、`cpus` / `limits.cpus`、`shm_size` | `-m 512M`（自动大写单位）`--cpus` `--shm-size` |
| `ulimits`、`stop_signal`、`stop_grace_period` | `--ulimit` `--stop-signal` `--stop-timeout` |
| `gpus`、`deploy.resources.reservations.devices[capabilities: gpu]` | `--gpus all` |
| `tty`、`stdin_open` | `-t` `-i` |
| `depends_on`（started / healthy / completed_successfully / required） | 依赖排序 + 轮询 `wslc inspect` 等待 |
| `profiles`、`pull_policy`（always/missing/never/build） | 服务选择 / 镜像准备策略 |
| 插值、`.env`、多 `-f` 合并、`extends`、`include` | 由 compose-go 原生处理 |

**ℹ️ 已映射（已在 wslc 3.0.1 真机验证）**

| 字段 | 说明 |
|---|---|
| `healthcheck` | 映射为 `--health-cmd/--health-interval/--health-timeout/--health-start-period/--health-retries`、`--no-healthcheck`；`CMD` 形式会被 shell 转义后拼接。`ps` 的 `HEALTH` 列读取 `wslc list` 的 `HealthStatus`（缺失时回退到 `inspect`） |
| `depends_on: service_healthy` | 轮询 `wslc inspect` 的 `State.Health.Status`；若 wslc 不上报健康状态，连续 3 次后退化为“运行即视为健康”并给出警告 |
| `ports[].mode: host` | 按普通端口发布处理（INFO 提示，不触发 `--strict`） |

组合示例（多网络、生成的 secret、host-gateway）见上方英文矩阵中的 Example。

**部分支持**（已实现；wslc 无法完全对齐的地方会警告，`--strict` 时这些警告变为错误）

| 字段 | 行为 |
|---|---|
| secrets/configs 的 `uid`/`gid` | 绑定挂载无法设置属主；文件仍会挂上 |
| `file:` 来源上的 `mode` | 保留宿主文件权限。`environment`/`content` 生成的文件会按 `mode` chmod |
| `external: true` 的 secret/config | 跳过挂载 |
| `ipv6_address`、`enable_ipv6` | wslc 网络只有 IPv4 |
| 主端点的 `driver_opts` / `link_local_ips` | `wslc run` 没有对应参数；只在 `network connect` 接入该网络时生效 |
| 第二条及以后的 `ipam.config`、`aux_addresses`、自定义 IPAM | 只用第一条的 subnet/gateway/`ip_range` |
| `extra_hosts` | 在启动之后写入，入口进程可能已经读过 `/etc/hosts`（INFO，不触发 `--strict`） |
| wslc &lt; 3.0.3.0 上的 host-gateway 端口回环 | 仍更新 `/etc/hosts`；跳过会话里的 DNAT |
| wslc &lt; 3.0.3.0 上的 `build.secrets` | 跳过 |
| wslc &lt; 3.0.3.0 上的多网络、`--ip`、`--ip-range` | 只接入优先级最高的网络，且不设静态地址 |
| 端点 `mac_address`、`gw_priority`、`interface_name` | 忽略 |
| host-gateway DNAT 里的主机端口范围 | 跳过（发布端口在运行时才选定，不是稳定的 DNAT 目标） |

**⚠️ 忽略并警告**（`--strict` 时报错）

`restart`、`deploy.restart_policy`、`privileged`、`cap_add`/`cap_drop`、`devices`、`device_cgroup_rules`、
`security_opt`、`sysctls`、`logging`、`platform`、`init`、`read_only`、
`ipc`、`pid`、`uts`、`userns_mode`、`group_add`、`cgroup`/`cgroup_parent`、`runtime`、`isolation`、`mac_address`、
`links`/`external_links`、`volumes_from`、`storage_opt`、`oom_*`、`pids_limit`、`blkio_config`、
`cpu_shares`/`cpu_quota`/`cpuset` 等、`mem_reservation`/`memswap_limit`、`post_start`/`pre_stop`、`develop`(watch)、
`provider`、`models`、`use_api_socket`、`network_mode: host|service:|container:`、
卷 `nocopy`/`subpath`、npipe/image/cluster 卷类型、
`build.ssh`/`platforms`/`cache_from`/`cache_to`/`additional_contexts`/`network`/`extra_hosts`/`dockerfile_inline`、
`healthcheck.start_interval`、非 bridge 网络驱动、卷驱动及选项、`deploy.resources.reservations`（内存）。

wslc 的 `run` 参数表里没有 capabilities、设备、sysctl、日志驱动、`--init`、`--privileged` 或 `--read-only`
（上游仍在跟踪，例如 [WSL#41181](https://github.com/microsoft/WSL/issues/41181)）。这些字段保持警告，而不是猜测一个参数。

**❌ 不支持的命令**：`watch`、`attach`、`cp`、`top`、`pause`/`unpause`、`port`、`events`、`images`、`scale`（请用 `up --scale`）、`kill`、`rm`、`create`、`push`、`ls`（多项目）。

## dry-run 示例

```console
$ wslc-compose -f minimal.yaml --dry-run up -d
wslc.exe network create -l com.docker.compose.network=default -l com.docker.compose.project=minimal minimal_default
wslc.exe pull nginx:alpine
wslc.exe run -d --name minimal-hello-1 -l com.docker.compose.container-number=1 -l com.docker.compose.oneoff=False -l com.docker.compose.project=minimal -l com.docker.compose.project.config_files=C:/src/demo/minimal.yaml -l com.docker.compose.project.working_dir=C:/src/demo -l com.docker.compose.service=hello -l com.wslc.compose.config-hash=<sha256> -l com.wslc.compose.version=dev -p 8080:80 --network minimal_default --network-alias hello --dns 1.1.1.1 nginx:alpine

$ wslc-compose --dry-run down -v
wslc.exe network remove minimal_default
```

有 wslc 时，dry-run 会**真实查询**当前状态（list/inspect），只省略变更操作，
因此能准确预览“哪些容器保持、哪些重建”。没有 wslc 时按空状态预览。
更完整的例子见 [`testdata/golden/`](testdata/golden)，其中 `up_full.golden` 覆盖了几乎全部映射字段。

## 设计要点

- **项目状态 = 容器标签。** 每个容器带有 `com.docker.compose.project/service/container-number/oneoff/
  project.working_dir/project.config_files` 以及 `com.wslc.compose.config-hash`、`com.wslc.compose.version`；
  使用了 `host-gateway` 时还有 `com.wslc.compose.host-gateway=v1`。
  `ps`/`down` 只依赖 `wslc list --filter label=...`；若 list 输出缺少标签，则逐个 `inspect` 补全。
  例外是 `environment:` / `content:` 生成的 secret 和 config 文件，放在
  `$XDG_CONFIG_HOME/wslc-compose/<project>`（Windows 上为 `%LOCALAPPDATA%\wslc-compose\<project>`），由 `down` 删除。
  `file:` 来源按原路径挂载，不复制。
- **配置哈希。** 对服务配置做 JSON + SHA-256，排除 `build`、`pull_policy`、`scale`/`replicas`、`depends_on`、
  `profiles`，避免只调整副本数或依赖时重建容器。命令与 v0.1.5 相同的服务保持原来的摘要（`minimal.yaml` 的 hello 为
  `0df62ea0…`），升级不会重建。只有用到新翻译的服务（secrets、configs、extra_hosts，以及在 wslc 3.0.3.0 上的多网络、
  静态 IPv4、`ip_range`、次要端点选项、`build.secrets`）才会把模式版本 2 和能力档混进哈希。
  `up --build` 重新构建过的服务会强制重建。
  `wslc-compose config --hash "*"` 可查看。
- **默认 DNS 1.1.1.1。** wslc 的工具 VM 把 DNS 转发到 Windows 主机解析器；在很多局域网/企业网络里，
  容器内解析公网域名会得到 `SERVFAIL`（apt/npm/pip 失败）。因此对未声明 `dns:` 的服务注入 `--dns 1.1.1.1`；
  需要解析内网域名时，在服务里写 `dns:` 或使用 `--default-dns=10.0.0.53` / `--default-dns=` 关闭。
- **路径。** Windows 上统一转为 `C:/x/y`；WSL 内调用 `wslc.exe` 时 `/mnt/c/...` 直接换算，
  其余路径经 `wslpath -w` 转为 `//wsl.localhost/<distro>/...`。相对 Dockerfile 会按构建上下文解析成绝对路径，
  因为 wslc 是相对自身工作目录解析 `-f` 的。
- **内存单位大写。** wslc 拒绝 `512m`，只接受 `512M`，因此所有字节数都格式化为 `K/M/G` 大写后缀。
- **串行 + 重试。** wslc 预览版在并发调用时可能返回 `ERROR_SHARING_VIOLATION`，刚停止的容器重建时可能返回
  `ERROR_ALREADY_EXISTS`；短命令默认串行执行，并对这两类错误最多重试 5 次（间隔 2 秒）。
  `pull` 另外对镜像仓库的网络抖动（`EOF`、超时、连接重置、429/502/503）重试。已实时显示过的 wslc 错误输出
  不会在最终错误信息里再打印一次。
- **失败回滚。** `up` 中途失败时（拉取/构建/启动失败、依赖不健康），按逆序删除**本次**创建的容器、网络和卷，
  已存在的资源不受影响；`--wait` 阶段的失败不回滚，便于查看日志。
- **确定性顺序。** 依赖顺序使用稳定的拓扑排序（同层按名称），dry-run 输出与 golden 测试完全可复现。

更详细的包结构见 [docs/architecture.md](docs/architecture.md)（英文）。

## 已知限制

- wslc 对 Compose 关键能力缺失：没有重启策略、`--privileged`、`--cap-add`、`--device`、`--sysctl`、
  `--platform`、`--network host`、日志驱动、`--init`，这些字段只能警告忽略。
- **多个网络**需要 wslc 3.0.3.0（`create`、`network connect`、`start`）。更旧的版本只接入优先级最高的网络并给出警告。
  `wslc run` 仍然只接受一个 `--network`，所以其余端点在 `start` 之前用 `network connect` 接上。这个顺序来自参考实现，
  本仓库没有在真机 wslc 上重新验证。
- **host-gateway 的已发布端口**需要 `wslc system session run` 以及会话虚拟机里的 iptables（wslc 3.0.3.0）。
  链名（`WCGP_`/`WCGS_`/`WCGF_` 加项目哈希）与 bacarndiaye/wslc-compose 对齐，这样两个前端可以调和同一个项目。
  凡是能 `exec` 的 wslc 都会在启动后更新 `/etc/hosts`。
- `wslc inspect` 输出遵循 OCI 风格而非 Docker 模式，解析采用多路径容错；若你的 wslc 版本字段名不同，
  `ps` 的状态/端口列或 `service_healthy` 等待可能退化（会有警告）。
- WSL 发行版内部路径（非 `/mnt/<盘符>`）会变成 `//wsl.localhost/...` UNC 形式，wslc 是否接受取决于版本；
  建议把项目放在 Windows 盘符下。
- 未声明值的环境变量（如 `environment: [TOKEN]` 且宿主未设置）会被跳过，因为 wslc 进程运行在 Windows 侧。
- 匿名卷被改写为按服务命名的卷，因此同一服务的多个副本**共享**该卷（Docker 中每个容器各有一个）。
- wslc 的 `--gpus` 只接受 `all`，GPU 设备的 `count` / `device_ids` 会被当作全部 GPU。
- `--entrypoint` 覆盖（`run`）按空白拆分，不支持引号。
- `scripts/wslc.cmd` 的参数转发不处理带引号的复杂参数，复杂场景请直接用 `wslc-compose`。

## 开发与贡献

```bash
make test     # 单元 + golden 测试（无需真实 wslc，Runner 被 mock）
make race     # 竞态检测
make cover    # 覆盖率
make golden   # 输出有意变化后重写 testdata/golden
make lint     # gofmt + go vet
make dist     # 全部平台二进制（amd64 经 UPX 压缩）+ 脚本 + checksums.txt
```

项目结构：

```
cmd/wslc-compose/     入口（信号处理、退出码透传）
internal/cli/         cobra 命令（只做参数解析）
internal/app/         用例编排（up/down/ps/logs/exec/run/...）
internal/project/     compose-go 加载、字段策略、配置哈希
internal/translate/   ServiceConfig -> wslc argv（纯函数）
internal/plan/        期望 vs 实际 -> 操作；依赖排序（纯函数）
internal/wslc/        Runner 接口、类型化客户端、dry-run、输出解析
internal/labels/      标签键与辅助函数
internal/paths/       Windows/WSL 路径转换
internal/logs/        带前缀的日志多路复用
internal/golden/      测试辅助（golden 文件比对）
testdata/compose/     测试用 compose 文件
testdata/golden/      锁定的 wslc 命令序列
scripts/              install.ps1 与 `wslc compose` 包装脚本
.github/workflows/     ci.yml（测试 + 构建）、release.yml（打 tag 自动发布）
```

新增一个字段映射的步骤：

1. 在 `internal/translate` 中实现映射，并在 `testdata/compose/full.yaml` 中使用该字段；
2. 若该字段此前在 `internal/project/policy.go` 中被警告，删除对应规则；
3. `make golden` 更新 golden 文件，检查 diff 是否符合预期；
4. 更新本 README 的支持矩阵（中英文两部分）。

提交前请确保 `make lint race` 通过。欢迎附上真机 `wslc --version` 与 `wslc <cmd> --help` 输出来校正映射。

发布新版本：编写 `docs/release-notes/vX.Y.Z.md`，然后 `git tag vX.Y.Z && git push origin vX.Y.Z`，
`release.yml` 会测试、交叉编译、UPX 压缩并创建 GitHub Release（附 `checksums.txt`）。Release 只由 CI 发布。

## 许可

[MIT](LICENSE)
