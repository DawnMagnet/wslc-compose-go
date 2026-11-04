# Changelog

## Unreleased

### Fixed

- Generated secret and config files are replaced atomically, so a second `up`
  (or two services sharing one source) can rewrite a read-only file. `--dry-run`
  computes those paths and does not write them. On Windows the project state
  directory is under `%LOCALAPPDATA%`, and file modes are not asserted as Unix
  bits. A failed `up` removes a state directory it created. `down` still
  removes networks and that directory when host-gateway cleanup fails.
- Config hashes include a translation schema version and the wslc capability
  tier, so containers created by v0.1.5 or by an older wslc are recreated
  after an upgrade. wslc 3.0.1 command sequences for `up`/`down` of the
  minimal and full examples stay byte-identical to v0.1.5.

### Added

- Multiple networks per service. Every declared network is created (project-prefixed
  name, `external` networks are only checked). The container is created on the
  highest-priority endpoint (`--network`, `--ip`, `--network-alias`) and the others
  are attached with `wslc network connect` (`--ip`, `--network-alias`, `--link-local-ip`,
  `--driver-opt`) before `start`. A single network still uses `wslc run -d`.
- Network IPAM `ip_range` (`network create --ip-range`), plus validation that static
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
