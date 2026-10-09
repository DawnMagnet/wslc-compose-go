# Architecture

```
compose.yaml ──► project (compose-go loading + policy check + config hash)
                    │
                    ▼
             app (use cases: up/down/ps/logs/...)
          ┌─────────┼──────────────┐
          ▼         ▼              ▼
     translate     plan          logs
  (service→argv) (desired vs    (log multiplexer)
          │        actual)
          │         │
          └────► wslc (typed client: Runner interface, dry-run, serialisation/retry, JSON parsing)
                    │
                    ▼
               wslc.exe / wslc
```

| Package | Responsibility | Pure? |
|---|---|---|
| `internal/project` | Loads via compose-go v2 (interpolation, merging, profiles, env_file, service selection); `Check`/`Enforce` handle unsupported fields; `ServiceHash` computes the config hash | yes, except loading |
| `internal/translate` | `ServiceConfig` → `wslc run` / `wslc build` argv | yes |
| `internal/plan` | Derives keep/start/create/recreate/remove from config hash and container state; deterministic topological order | yes |
| `internal/wslc` | `Runner` interface + `Exec` implementation; `Client` wraps list/inspect/run/build/...; dry-run, concurrency limit, transient-error retries; tolerant JSON parsing | no |
| `internal/labels` | `com.docker.compose.*` and `com.wslc.compose.config-hash` labels | yes |
| `internal/paths` | Windows / WSL path conversion (`wslpath -w`, forward slashes) | mostly |
| `internal/logs` | Concurrency-safe, line-buffered log multiplexing with `[service] |` prefixes | yes |
| `internal/app` | Wires the components above into command use cases | no |
| `internal/cli` | cobra command tree; argument parsing only | no |

## Design principles

1. **State lives only in labels.** There is no local state file. `ps`/`down`/`up` find containers via
   `wslc list --filter label=com.docker.compose.project=<p>` and use `com.wslc.compose.config-hash`
   to decide whether a container must be recreated.
2. **Pure core.** `translate` and `plan` perform no I/O; golden tests lock the complete command sequence.
3. **One boundary interface.** The only external dependency is `wslc.Runner` (a single method). Tests inject
   a fake; dry-run is implemented inside the client, invisible to callers.
4. **Determinism.** Dependency ordering uses a built-in Kahn sort (ties broken by name) instead of
   compose-go's concurrent `graph.InDependencyOrder`, whose map-order traversal of independent services
   would make dry-run output and golden files unstable. Cycles are rejected by compose-go at load time
   and again by `plan.Order`.
5. **Conservative concurrency.** The wslc preview's session store rejects bursts of concurrent calls
   (`ERROR_SHARING_VIOLATION`), so short calls are serialised by default (tunable with `--parallel`) and known
   transient errors are retried a bounded number of times. Long-lived `logs -f` / `exec` calls do not consume
   concurrency slots.

## `up` flow

1. Load the project → policy check (with `--strict`, any WARN aborts before wslc is called) →
   rewrite anonymous volumes into project-scoped named volumes (`project.NameAnonymousVolumes`) → apply `--scale`.
2. Create the required networks (ipam subnet/gateway, internal, driver_opts, labels) and named volumes
   (with project/volume labels); external resources are only checked for existence.
3. Images: services with `build` are built on `--build`, `pull_policy: build` or a missing image; all others are
   pulled according to `pull_policy` (always / missing / never), each image once. Rebuilt services are recreated.
4. Walk services in dependency order: wait for dependency conditions first (`service_healthy` polls
   `wslc inspect`; `service_completed_successfully` waits for exit code 0), then apply the planned action.
5. Handle orphan containers (warn, or remove with `--remove-orphans`).
   Every network, volume and container created in steps 2–5 is registered with `rollback`; any failure removes
   them in reverse order (best effort; no rollback in dry-run).
6. `--wait` waits for readiness; without `-d`, logs are attached and Ctrl+C stops containers in reverse order.
