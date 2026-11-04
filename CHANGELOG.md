# Changelog

## Unreleased

### Fixed

- `wslc system session run` on wslc 3.0.3.0 can exit 1 with `ERROR_INVALID_HANDLE`
  after the script has already finished. Host-gateway scripts record their exit
  status in the session VM (`/run/<owner>.status`). When wslc reports this error,
  that recorded status is the result, and the full script runs again only when
  the status for this attempt is missing. `up` keeps a project whose rules were
  programmed. `down` runs session cleanup only when a removed container carries
  the host-gateway label or project state records that rules were programmed.
  An empty `down` exits 0 without calling `wslc system session`.

## v0.2.0 - 2026-11-04

### Fixed

- Generated secret and config files are replaced atomically, so a second `up`
  (or two services sharing one source) can rewrite a read-only file. `--dry-run`
  computes those paths and does not write them. On Windows the project state
  directory is under `%LOCALAPPDATA%`, and file modes are not asserted as Unix
  bits. A failed `up` removes a state directory it created. `down` still
  removes networks and that directory when host-gateway cleanup fails.
- Config hashes stay identical to v0.1.5 for services whose container command
  did not change (`minimal.yaml` hello is `0df62ea0…`). Schema version 2 is
  added for mounted secrets, configs and extra_hosts. The 3.0.3 tier is added
  only for multi-network, static IPv4 and secondary endpoint options.
  `ip_range`, `build.secrets`, external secrets and external configs do not
  change the hash.
  A subnet is a hard error only when that network will be created and a
  service assigns a static IPv4 address on it. An `ip_range` that is outside
  its subnet, or that has no usable IPv4 subnet (missing subnet, or an IPv6
  subnet), is a hard error on wslc 3.0.3 before any wslc mutation, because
  `--ip-range` is not sent by older versions. An IPv6 subnet with no
  `ip_range`, and `/31` pools, stay warnings, and the subnet is still passed
  to `network create`. External networks are not checked. On wslc 3.0.1,
  static addresses and `ip_range` stay warnings and `--ip-range` is not sent,
  as in v0.1.5.
  An unreadable `wslc --version` does not recreate containers. A service is
  warned only when its stored hash matches neither the legacy nor the 3.0.3
  digest of the current config. `--strict` rejects the unknown version on
  `up`, `run`, and `build` (before any wslc mutation) and still allows
  `down`, `start`, `stop`, `restart`, `ps`, `logs`, and `exec`. A failed
  `run` or `build` removes a state directory it created. `run` checks
  networks before it creates networks or pulls images.

### Added

- Multiple networks per service. Every declared network is created (project-prefixed
  name, `external` networks are only checked). The container is created on the
  highest-priority endpoint (`--network`, `--ip`, `--network-alias`) and the others
  are attached with `wslc network connect` (`--ip`, `--network-alias`, `--link-local-ip`,
  `--driver-opt`) before `start`. A single network still uses `wslc run -d`.
- Network IPAM `ip_range` (`network create --ip-range`) (wslc 3.0.3.0+), plus validation that static
  addresses and ranges sit inside the subnet. Replicas cannot share one static address.
- Secrets and configs. `file:` sources are mounted read-only at `/run/secrets/<name>`
  or `/<name>` (or `target`). `environment:` and `content:` are written under the
  project state directory and removed by `down`. `build.secrets` becomes
  `wslc build --secret` when the binary accepts it.
- `extra_hosts`, including `host-gateway` (default route inside the container).
  Published-port hairpin uses session iptables (`wslc system session run`) on
  wslc 3.0.3.0 or newer and is removed by `down`.
- `wslc --version` detection. Multi-network attach, static IPv4, `--ip-range`,
  `build --secret` and host-gateway DNAT are skipped with a warning on older
  binaries; `--strict` turns those warnings into errors. `--dry-run` with no
  wslc binary plans the current feature set.

### Already mapped, unchanged

`stop_grace_period`, `stop_signal`, `tmpfs`, `shm_size`, `ulimits`, `gpus`
(`--gpus all`), `labels`, `user`, `working_dir`, entrypoint and command
forms, healthcheck disable, `pull_policy`, build args/target, scale/replicas,
profiles, `--wait`, `--remove-orphans`, `-d`, `--build`, `--force-recreate`,
`down -v`, `logs -f`/`--tail`, `ps --format json` and `config`.

`init`, `sysctls`, `cap_add`/`cap_drop`, `security_opt`, `devices`, `logging`
and `restart` stay warnings: wslc `run` has no flags for them.

### Not verified on a live wslc

Mocked Runner and golden files only: `create`, `network connect`, `--ip`,
`--ip-range`, `--link-local-ip`, `--driver-opt`, `build --secret`, the
`exec` `/etc/hosts` script, and `system session run` iptables. Behavior that
was already verified on wslc 3.0.1 is unchanged when the probed version is
older than 3.0.3.0.
