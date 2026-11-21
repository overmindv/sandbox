# Sandbox Hardening

## Isolation Layers

- Worker pods run without service account token automount, as non-root, with a read-only root filesystem.
- The compiler and runner are scheduled on the dedicated runtime pool using
  `overmindv.io.node-restriction.kubernetes.io/pool=sandbox-runtime`.
- Runtime pods have no Kubernetes secrets and no `envFrom`.
- Runtime NetworkPolicy declares empty egress and allows ingress only from
  worker pods in the same namespace on TCP 5050.
- Worker egress excludes `169.254.0.0/16`; runtime egress is empty.
- All Kubernetes deployment manifests are tested for absence of `hostPath` volumes and `/var/run/docker.sock` mounts.

## go-judge Privilege Boundary

go-judge needs Linux cgroups and namespaces to enforce CPU, memory, PID, and filesystem isolation. In containerized deployments this usually means the go-judge supervisor container needs elevated privileges. Because of that, the production boundary is:

- isolate go-judge to a dedicated tainted node pool;
- forbid runtime egress;
- forbid hostPath and Docker socket mounts;
- do not mount Kubernetes secrets into runtime pods;
- make go-judge fail closed with `-no-fallback`;
- enforce per-run `cpuLimit`, `clockLimit`, `memoryLimit`, `procLimit`, output limits, and copy-out limits.

## Runtime Limits

The worker maps every execution request to go-judge hard limits:

- CPU: `cpuLimit`
- wall time: `clockLimit`
- memory: `memoryLimit`
- process count: `procLimit`
- stdout/stderr file descriptor max sizes
- workspace/copy-out max bytes

The go-judge deployment also sets:

- `-tmp-fs-param size=128m,nr_inodes=4096`
- `-output-limit 1048576`
- `-copy-out-limit 1048576`
- memory `emptyDir` size limits for `/tmp`, `/w`, and `/dev/shm`
- `-container-cred-start 10000`
- `-no-fallback`

## Seccomp, AppArmor, gVisor

- Worker pods use `seccompProfile: RuntimeDefault` and AppArmor `runtime/default`.
- The privileged go-judge supervisor does not get seccomp or AppArmor protection
  from pod settings. See [Kubernetes privileged containers](https://kubernetes.io/docs/concepts/security/linux-kernel-security-constraints/#privileged-containers).
- A gVisor RuntimeClass example is provided, but it is not applied by default because go-judge's nested cgroup/namespace model must be validated against the chosen runtime handler before rollout.

## Secret Boundary

The language manifest and go-judge request mapper reject runtime environment variable names that look like secrets:

- `SECRET`
- `TOKEN`
- `PASSWORD`
- `PRIVATE_KEY`
- `ACCESS_KEY`
- `API_KEY`
- `CREDENTIAL`

This prevents accidental leakage of storage/Kafka/database credentials into user code execution.

## Stage Acceptance

1. Verify the control plane uses the Node authorizer and the NodeRestriction
   admission plugin. Label only dedicated Linux amd64 nodes with the protected
   runtime label, then apply the runtime taint. Prepare the protected label before
   updating deployments that used the old label. Do not assign it to application
   or control-plane nodes. Use cluster admission and RBAC to limit who can create
   privileged pods or schedule other workloads onto this pool. Restrict workload
   writers in the sandbox namespace so they cannot impersonate a worker by label.
2. Pin the runner and compiler images by digest. Apply the network policy before
   the deployments. Wait for the rollouts to complete, then run:

   ```powershell
   python scripts/check_runtime_isolation.py --context $StageContext
   ```

   The checker uses only `kubectl get`: node metadata and the sandbox namespace's
   Deployments, Pods, Services and NetworkPolicies. It checks both runtimes and
   rejects an additional policy that broadens ingress or egress. It does not read
   Secret objects or execute commands on a node.
3. Verify placement across namespaces: the runtime nodes should only carry the
   reviewed infrastructure agents and sandbox runtimes. Confirm the CNI enforces
   pod policies in this cluster. Kubernetes NetworkPolicy does not block all
   traffic to the pod's own node, so verify node and cloud metadata isolation
   separately. Check both IPv4 and IPv6 when enabled.
4. Use the production-equivalent stage stack to verify both supported languages,
   denial of runtime egress and host access, hard CPU, memory, PID, filesystem and
   output limits, and workspace cleanup after timeout. Record the tested node
   image, CNI, runtime digests and results before enabling untrusted execution.

Passing preflight confirms placement and configuration, not live enforcement.

## Smoke Tests

Run hostile workload smoke tests against a live local or dev stack:

```powershell
make security-smoke
```

The script submits:

- path traversal payload;
- sleep bomb;
- memory bomb;
- output bomb;
- fork bomb;
- metadata endpoint access check.

The expected result is that each job either gets a controlled terminal sandbox status or, for metadata access, proves the endpoint is blocked from user code.
