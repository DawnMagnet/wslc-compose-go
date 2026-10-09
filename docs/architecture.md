# 架构说明

```
compose.yaml ──► project (compose-go 加载 + 策略检查 + 配置哈希)
                    │
                    ▼
             app (用例编排: up/down/ps/logs/...)
          ┌─────────┼──────────────┐
          ▼         ▼              ▼
     translate     plan          logs
  (Service→argv) (期望 vs 实际)  (日志多路复用)
          │         │
          └────► wslc (类型化客户端: Runner 接口、dry-run、串行/重试、JSON 解析)
                    │
                    ▼
               wslc.exe / wslc
```

| 包 | 职责 | 纯函数? |
|---|---|---|
| `internal/project` | 通过 compose-go v2 加载（插值、合并、profiles、env_file、服务选择）；`Check`/`Enforce` 处理不支持字段；`ServiceHash` 计算配置哈希 | 除加载外是 |
| `internal/translate` | `ServiceConfig` → `wslc run` / `wslc build` argv | 是 |
| `internal/plan` | 依据配置哈希和容器状态得出 keep/start/create/recreate/remove；确定性的依赖拓扑排序 | 是 |
| `internal/wslc` | `Runner` 接口 + `Exec` 实现；`Client` 封装 list/inspect/run/build/...；dry-run、并发限流、瞬时错误重试；宽松 JSON 解析 | 否 |
| `internal/labels` | `com.docker.compose.*` 与 `com.wslc.compose.config-hash` 标签 | 是 |
| `internal/paths` | Windows / WSL 路径转换（`wslpath -w`、正斜杠） | 基本是 |
| `internal/logs` | 带 `[service] |` 前缀、行缓冲、并发安全的日志复用 | 是 |
| `internal/app` | 把上述组件串成命令用例 | 否 |
| `internal/cli` | cobra 命令树，只做参数解析 | 否 |

## 设计原则

1. **状态只在标签里**：没有任何本地状态文件。`ps`/`down`/`up` 都靠
   `wslc list --filter label=com.docker.compose.project=<p>` 找回容器，
   靠 `com.wslc.compose.config-hash` 判断是否需要重建。
2. **纯函数核心**：translate 与 plan 不碰 I/O，golden 测试锁定完整命令序列。
3. **边界接口**：唯一的外部依赖是 `wslc.Runner`（一个方法）。测试注入假实现，
   dry-run 在客户端内部统一实现，调用方无感知。
4. **确定性**：依赖排序使用自带的 Kahn 算法（同层按名称排序），而不是
   compose-go `graph.InDependencyOrder` 的并发遍历——后者在独立服务之间按 map
   顺序访问，会让 dry-run 输出和 golden 文件不稳定。依赖环检测由 compose-go
   加载阶段完成，`plan.Order` 也会再次报错。
5. **保守并发**：wslc 预览版的会话存储会拒绝大量并发调用（`ERROR_SHARING_VIOLATION`），
   因此短调用默认串行（`--parallel` 可调），并对已知瞬时错误做有限重试；
   `logs -f`/`exec` 这类长连接不占用并发额度。

## up 的执行流程

1. 加载项目 → 策略检查（`--strict` 时任何 WARN 都中止，且在调用 wslc 之前）→
   匿名卷改写为项目内命名卷（`project.NameAnonymousVolumes`）→ 应用 `--scale` 覆盖。
2. 创建所需网络（含 ipam subnet/gateway、internal、driver_opts、标签）和命名卷（带 project/volume 标签）；
   external 资源只校验存在。
3. 镜像准备：有 `build` 的服务按 `--build` / `pull_policy: build` / 镜像缺失 决定是否构建；
   其余按 `pull_policy`（always / missing / never）拉取，同一镜像只拉一次。
   被重新构建的服务会被强制重建容器。
4. 依赖顺序遍历每个服务：先等待依赖条件
   （`service_healthy` 轮询 `wslc inspect`、`service_completed_successfully` 等待退出码 0），
   再执行 plan 给出的操作。
5. 处理孤儿容器（警告或 `--remove-orphans` 删除）。
   第 2–5 步中每个新建的网络、卷、容器都会登记到 `rollback`；任一步失败即逆序删除它们（best effort，
   dry-run 不回滚）。
6. `--wait` 等待就绪；非 `-d` 时附着日志，Ctrl+C 后按逆序停止容器。
