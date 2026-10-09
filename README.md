# wslc-compose

> 用标准 Compose 文件驱动 **WSL Containers（`wslc`）** 的 Go 实现。
> 在微软官方 `wslc compose` 发布之前，提供 `docker compose` 风格的无缝体验。

`wslc-compose` 用官方解析器 [compose-spec/compose-go v2](https://github.com/compose-spec/compose-go)
加载 `compose.yaml`（插值、多文件合并、profiles、env_file、extends 全部由官方实现处理），
再把每个服务翻译成类型化的 `wslc` 命令行调用。

- **零状态文件**：项目状态全部存放在容器标签上（`com.docker.compose.*`），与 Docker Compose 标签对齐。
- **增量更新**：基于配置哈希（`com.wslc.compose.config-hash`），配置不变的容器不会被重建。
- **随时 `--dry-run`**：打印将要执行的 `wslc` 命令；即使本机没有 wslc 也能运行。
- **`--strict`**：遇到 wslc 无法实现的字段直接报错，而不是静默忽略。
- **单一可执行文件**：Windows 原生 exe，或在 WSL 发行版内运行并自动调用 `wslc.exe`。

> 状态：预览。所有 wslc 交互都经过 mock 测试；真实 wslc 上的行为请以 `--dry-run` 先确认，
> 发现差异欢迎提 issue。

---

## 目录

- [安装](#安装)
- [让 `wslc compose` 直接可用](#让-wslc-compose-直接可用)
- [快速开始](#快速开始)
- [命令参考](#命令参考)
- [Compose 字段支持矩阵](#compose-字段支持矩阵)
- [dry-run 示例](#dry-run-示例)
- [设计要点](#设计要点)
- [已知限制](#已知限制)
- [开发与贡献](#开发与贡献)

## 安装

需要 Windows 11 + WSL 2.9.3 及以上（包含 `wslc`）。构建需要 Go 1.24+。

```powershell
# 方式一：go install（Windows 上直接得到 wslc-compose.exe）
go install github.com/dawnmagnet/wslc-compose-go/cmd/wslc-compose@latest

# 方式二：源码构建
git clone https://github.com/dawnmagnet/wslc-compose-go
cd wslc-compose-go
make build          # 本机平台 -> bin/wslc-compose
make windows        # 交叉编译 -> dist/wslc-compose-windows-{amd64,arm64}.exe
make linux          # 在 WSL 发行版内使用的 Linux 版本
```

`wslc` 可执行文件的查找顺序：`--wslc` 参数 → `$WSLC_COMPOSE_BIN` → `PATH` 中的 `wslc.exe` / `wslc`
→ `C:\Program Files\WSL\wslc.exe`（WSL 内为 `/mnt/c/Program Files/WSL/wslc.exe`）。

**在 WSL 发行版里运行时**，若调用的是 `wslc.exe`，bind mount 路径会自动经 `wslpath -w` 转换并改为正斜杠
（`/mnt/c/src/app` → `C:/src/app`），因为 wslc 把反斜杠视为转义字符。

## 让 `wslc compose` 直接可用

`wslc` 是微软的二进制，无法注册子命令，因此提供三个轻量包装（其他子命令原样转发给真正的 `wslc.exe`）：

| 环境 | 做法 |
|---|---|
| PowerShell | 在 `$PROFILE` 中加入 `. <repo>\scripts\wslc-compose.ps1` |
| bash / zsh（WSL 内） | 在 `~/.bashrc` 中加入 `source <repo>/scripts/wslc-compose.sh` |
| cmd.exe / 脚本 | 把 `scripts\wslc.cmd` 所在目录放到 PATH 中 `C:\Program Files\WSL` **之前** |

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
| `up [SERVICE...]` | `-d` `--build` `--no-build` `--pull` `--force-recreate` `--no-recreate` `--no-deps` `--remove-orphans` `--wait` `--wait-timeout` `-t` | 创建网络/卷 → 构建/拉取 → 依赖顺序收敛容器；非 `-d` 时附着日志，Ctrl+C 停止 |
| `down` | `-v` `--remove-orphans` `-t` | 逆依赖顺序停止并删除容器，删除项目网络；`-v` 删除声明的命名卷（external 不动） |
| `ps [SERVICE...]` | `-a` `-q` `--format table\|json` | 列出项目容器 |
| `logs [SERVICE...]` | `-f` `-n/--tail` `-t` `--since` `--until` `--no-log-prefix` | 多容器日志合并，带 `name |` 前缀 |
| `build [SERVICE...]` | `--no-cache` `--pull` | 构建所有带 `build:` 的服务 |
| `pull [SERVICE...]` | `--ignore-pull-failures` | 拉取镜像（同一镜像只拉一次） |
| `config` | `--services` `--volumes` `--networks` `--images` `--hash "*"` `--format yaml\|json` | 输出规范化模型或其投影 |
| `exec SERVICE CMD...` | `-i`(默认开) `-t` `-T` `-d` `-u` `-w` `-e` `--index` | 在运行中的容器里执行命令；TTY 默认随 stdin 是否为终端 |
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
| `volumes` bind / 命名卷 / 匿名卷 / tmpfs（含 size）、`tmpfs`、`read_only` 挂载 | `-v` / `--tmpfs`，bind 源路径转换为 Windows 正斜杠形式 |
| `networks`（含默认网络 `<project>_default`、`aliases`）、`network_mode: none` | `--network` + `--network-alias <service>` + aliases |
| 顶层 `networks`：`driver`、`internal`、`ipam.config[0].subnet/gateway`、`driver_opts`、`labels`、`external`、`name` | `wslc network create …` |
| 顶层 `volumes`：`name`、`external` | `wslc volume create` |
| `working_dir`、`user`、`hostname`、`domainname`、`labels` | `-w` `-u` `-h` `--domainname` `-l` |
| `dns`、`dns_search`、`dns_opt` | `--dns` `--dns-search` `--dns-option` |
| `mem_limit` / `deploy.resources.limits.memory`、`cpus` / `limits.cpus`、`shm_size` | `-m 512M`（自动大写单位）`--cpus` `--shm-size` |
| `ulimits`、`stop_signal`、`stop_grace_period` | `--ulimit` `--stop-signal` `--stop-timeout` |
| `gpus`、`deploy.resources.reservations.devices[capabilities: gpu]` | `--gpus all` |
| `tty`、`stdin_open` | `-t` `-i` |
| `depends_on`（started / healthy / completed_successfully / required） | 依赖排序 + 轮询 `wslc inspect` 等待 |
| `profiles`、`pull_policy`（always/missing/never/build） | 服务选择 / 镜像准备策略 |
| 插值、`.env`、多 `-f` 合并、`extends`、`include` | 由 compose-go 原生处理 |

**ℹ️ 已映射但需在真机核实**（不会触发 `--strict` 失败）

| 字段 | 说明 |
|---|---|
| `healthcheck` | 映射为 `--health-cmd/--health-interval/--health-timeout/--health-start-period/--health-retries`、`--no-healthcheck`。不同文档对 wslc 健康检查支持说法不一，请先 `wslc run --help` 确认。`CMD` 形式会被 shell 转义后拼接 |
| `depends_on: service_healthy` | 轮询 `wslc inspect` 的 `State.Health.Status`；若 wslc 不上报健康状态，连续 3 次后退化为“运行即视为健康”并给出警告 |
| `ports[].mode: host` | 按普通端口发布处理 |

**⚠️ 忽略并警告**（`--strict` 时报错）

`restart`、`deploy.restart_policy`、`privileged`、`cap_add`/`cap_drop`、`devices`、`device_cgroup_rules`、
`security_opt`、`sysctls`、`extra_hosts`、`secrets`、`configs`、`logging`、`platform`、`init`、`read_only`、
`ipc`、`pid`、`uts`、`userns_mode`、`group_add`、`cgroup`/`cgroup_parent`、`runtime`、`isolation`、`mac_address`、
`links`/`external_links`、`volumes_from`、`storage_opt`、`oom_*`、`pids_limit`、`blkio_config`、
`cpu_shares`/`cpu_quota`/`cpuset` 等、`mem_reservation`/`memswap_limit`、`post_start`/`pre_stop`、`develop`(watch)、
`provider`、`models`、`use_api_socket`、`network_mode: host|service:|container:`、
多网络（只接入优先级最高的一个）、静态 `ipv4_address`、卷 `nocopy`/`subpath`、npipe/image/cluster 卷类型、
`build.secrets/ssh/platforms/cache_from/cache_to/additional_contexts/network/extra_hosts/dockerfile_inline`、
`healthcheck.start_interval`、非 bridge 网络驱动、IPv6、卷驱动及选项、`deploy.resources.reservations`（内存）。

**❌ 不支持的命令**：`watch`、`attach`、`cp`、`top`、`pause`/`unpause`、`port`、`events`、`images`、`scale`、`kill`、`rm`、`create`、`push`、`ls`（多项目）。

## dry-run 示例

```console
$ wslc-compose -f minimal.yaml --dry-run up -d
# dry-run: cannot query wslc (...); assuming empty state
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
  project.working_dir/project.config_files` 以及 `com.wslc.compose.config-hash`、`com.wslc.compose.version`。
  `ps`/`down` 只依赖 `wslc list --filter label=...`；若 list 输出缺少标签，则逐个 `inspect` 补全。
- **配置哈希。** 对服务配置做 JSON + SHA-256，排除 `build`、`pull_policy`、`scale`/`replicas`、`depends_on`、
  `profiles`，避免只调整副本数或依赖时重建容器；`up --build` 重新构建过的服务会强制重建。
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
- **确定性顺序。** 依赖顺序使用稳定的拓扑排序（同层按名称），dry-run 输出与 golden 测试完全可复现。

更详细的包结构见 [docs/architecture.md](docs/architecture.md)。

## 已知限制

- wslc 对 Compose 关键能力缺失：没有重启策略、`--privileged`、`--cap-add`、`--device`、`--platform`、
  `--network host`、secrets/configs，这些字段只能警告忽略。
- **每个容器只能接入一个网络**（wslc run 只接受一个 `--network`），跨多个网络的服务需要调整拓扑。
- `wslc inspect` 输出遵循 OCI 风格而非 Docker 模式，解析采用多路径容错；若你的 wslc 版本字段名不同，
  `ps` 的状态/端口列或 `service_healthy` 等待可能退化（会有警告）。
- WSL 发行版内部路径（非 `/mnt/<盘符>`）会变成 `//wsl.localhost/...` UNC 形式，wslc 是否接受取决于版本；
  建议把项目放在 Windows 盘符下。
- 未声明值的环境变量（如 `environment: [TOKEN]` 且宿主未设置）会被跳过，因为 wslc 进程运行在 Windows 侧。
- 匿名卷在 `down -v` 时不会被跟踪删除；使用 `wslc volume prune` 清理。
- `--entrypoint` 覆盖（`run`）按空白拆分，不支持引号。
- `scripts/wslc.cmd` 的参数转发不处理带引号的复杂参数，复杂场景请直接用 `wslc-compose`。

## 开发与贡献

```bash
make test     # 单元 + golden 测试（无需真实 wslc，Runner 被 mock）
make race     # 竞态检测
make cover    # 覆盖率
make golden   # 输出有意变化后重写 testdata/golden
make lint     # gofmt + go vet
make windows  # 交叉编译 Windows exe
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
scripts/              `wslc compose` 包装脚本
```

新增一个字段映射的步骤：

1. 在 `internal/translate` 中实现映射，并在 `testdata/compose/full.yaml` 中使用该字段；
2. 若该字段此前在 `internal/project/policy.go` 中被警告，删除对应规则；
3. `make golden` 更新 golden 文件，检查 diff 是否符合预期；
4. 更新本 README 的支持矩阵。

提交前请确保 `make lint race` 通过。欢迎附上真机 `wslc --version` 与 `wslc <cmd> --help` 输出来校正映射。

## 许可

[MIT](LICENSE)
