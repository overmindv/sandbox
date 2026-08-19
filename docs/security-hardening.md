# Sandbox Hardening

## Isolation Layers

- Worker pods run without service account token automount, as non-root, with a read-only root filesystem.
- go-judge runtime pods run on a dedicated `sandbox-runtime` node pool.
- Runtime pods have no Kubernetes secrets and no `envFrom`.
- Runtime pods have no egress NetworkPolicy. User code should not be able to reach the Internet, cluster services, or cloud metadata endpoints.
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
- go-judge runtime pods declare `seccompProfile: RuntimeDefault`.
- go-judge runtime pods declare AppArmor `unconfined` because the supervisor needs mount/cgroup operations. This should only be allowed on the dedicated runtime node pool.
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
