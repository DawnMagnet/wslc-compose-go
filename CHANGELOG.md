# Changelog

## v0.2.1 - 2026-11-04

### Fixed

- `wslc system session run` on wslc 3.0.3.0 can exit 1 with `ERROR_INVALID_HANDLE`
  after the script has already finished. The session script records
  `<nonce>-<attempt> <exit>` in `/run/<owner>.status` from an `EXIT` trap
  (temp file, then `mv -f`). On that error the client reads the file and uses
  only this attempt's line. The full script runs again only when the line is
  missing, so an earlier success cannot hide a later failure. The outer loop is
  up to 5 attempts with a 2s backoff. On each `ERROR_INVALID_HANDLE` the status
  file gets its own up to 5 reads, also with a 2s backoff (not a shared budget).
  Persistent errors fail after about 8s. The theoretical worst case is about
  48s of waiting, excluding wslc execution time and flock waits (up to 10s
  each). `TERM` / `INT` / `HUP` are recorded as 143 / 130 / 129.
  `ERROR_SHARING_VIOLATION` and `ERROR_ALREADY_EXISTS` are still retried, as in
  v0.2.0. Session calls, including the status read, pass an empty stdin, and
  session stdout is captured. On a non-zero script exit, wslc's stderr is kept.
  Failures writing or renaming the status file are ignored and do not change
  the exit code. A recorded exit 0 is success. v0.2.0 `down` frequently failed
  with exit 1 on wslc 3.0.3 (every cycle in our real test) because each `down`
  makes several session calls that intermittently fail; those calls now use the
  recorded status. `up` with host-gateway plus published ports no longer rolls
  the project back after the chains are installed.
- Host-gateway cleanup uses a write-ahead marker (`host-gateway` in the project
  state directory). It is written before the install script and kept if `up`
  rolls back, so a failed `up` is cleaned on the next `down`. On wslc 3.0.3,
  `down` runs that cleanup whenever the compose file uses host-gateway, a
  container carries the label, or the marker is set, which also removes chains
  left by v0.2.0 (v0.2.0 did not write a marker). An empty `down` of a project
  that uses host-gateway still runs one idempotent cleanup, same as v0.2.0. A
  project that does not use host-gateway and has no such record makes no
  session call. Behavior change: failing to write the marker now fails `up`
  (the first revision of this fix only warned, and it wrote the marker after
  the script). On wslc 3.0.1 or an unknown version, a leftover marker makes
  `down` warn and keep the marker; run `down` again after upgrading to
  wslc 3.0.3. On wslc 3.0.1, v0.2.1 emits exactly the same commands as v0.2.0;
  the only new 3.0.1 behavior is the leftover-marker warning.

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
