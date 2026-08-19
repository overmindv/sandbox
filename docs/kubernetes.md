# Kubernetes Deployment

The manifests in `deployments/k8s` are a dev/stage production baseline for `sandbox-worker`.

## Apply Order

```powershell
kubectl apply -f deployments/k8s/namespace.yaml
kubectl apply -f deployments/k8s/sandbox-secrets.secret.example.yaml
kubectl apply -f deployments/k8s/sandbox-config.configmap.yaml
kubectl apply -f deployments/k8s/gojudge-runtime.deployment.yaml
kubectl apply -f deployments/k8s/gojudge-runtime.network-policy.yaml
kubectl apply -f deployments/k8s/sandbox-worker.deployment.yaml
kubectl apply -f deployments/k8s/sandbox-worker.pdb.yaml
kubectl apply -f deployments/k8s/network-policy.yaml
kubectl apply -f deployments/k8s/service-monitor.yaml
kubectl apply -f deployments/k8s/sandbox-worker.hpa.yaml
```

Replace the values in `sandbox-secrets.secret.example.yaml` before applying it to a real cluster.

## Assumptions

- Worker image: `overmindv/sandbox-worker:dev`.
- Worker nodes are labelled with `overmindv.io/node-pool=sandbox`.
- Runtime nodes are labelled with `overmindv.io/node-pool=sandbox-runtime`.
- Kafka is reachable through `sandbox-kafka-bootstrap.kafka.svc.cluster.local:9092`.
- go-judge is reachable through `http://gojudge.sandbox.svc.cluster.local:5050`.
- Postgres runs in namespace `data`.
- S3-compatible object storage is either external HTTPS or in namespace `object-storage`.
- `ServiceMonitor` requires Prometheus Operator.
- HPA scaling by queue lag requires Prometheus Adapter to expose `sandbox_kafka_queue_lag_ms_max` as a pod custom metric.
- Optional gVisor RuntimeClass example: `deployments/k8s/examples/runtimeclass-gvisor.example.yaml`.

## Security Baseline

- `automountServiceAccountToken: false`.
- `runAsNonRoot` with UID/GID `10001`.
- `allowPrivilegeEscalation: false`.
- `readOnlyRootFilesystem: true`.
- Linux capabilities dropped.
- `RuntimeDefault` seccomp profile.
- Default-deny NetworkPolicy with explicit DNS, Kafka, go-judge, Postgres, and object storage egress.
- go-judge runtime has a separate no-egress NetworkPolicy.
- go-judge runtime uses memory `emptyDir` mounts for `/tmp`, `/w`, and `/dev/shm`.
- Kubernetes manifests are tested for no `hostPath` volumes and no Docker socket mounts.
