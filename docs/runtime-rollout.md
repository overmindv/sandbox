# Runtime rollout

Use the published Infra `release.json` for `linux/amd64`. It must contain
the Sandbox worker and go-judge images by digest. Use the Sandbox checkout at
`plan.sources.sandbox`. The renderer reads templates from that commit, not
from uncommitted files.

Render both files and keep them with the release receipt:

```sh
go run ./cmd/sandbox-release -manifest /path/to/release.json -phase runtime > runtime.json
go run ./cmd/sandbox-release -manifest /path/to/release.json -phase worker > worker.json
```

`runtime.json` creates a separate pair of Services and Deployments, network
policies and an immutable language manifest. The runner and compiler use the
same released image. The worker gets their versioned URLs and the exact
Python and Go versions. Both release revisions must fit on the runtime
node pool until the old workers finish.

On an existing Sandbox installation, use the same Kafka group and durable
PostgreSQL store. The namespace, worker ConfigMap and Secret, worker network
policy, image pull access and HPA must already be configured. Do not reapply
the development runtime Deployments as part of this rollout.

Expose `sandbox_scheduler_slots_utilization_percent` through Prometheus
Adapter as a per-Pod custom metric. It reports the highest live utilization
of the global or per-language slots. The HPA target is 80 percent; CPU stays
as the other signal. Check `sandbox_scheduler_queue_depth`,
`sandbox_scheduler_queue_capacity` and `sandbox_scheduler_slots_capacity`
before adjusting the worker limits. Add the rule from
[`prometheus-adapter-values.yaml`](../deployments/k8s/examples/prometheus-adapter-values.yaml)
to the existing adapter config. Check that the metric appears in the custom
metrics API before applying the HPA.

1. Apply `runtime.json` without pruning. Wait for both Deployments in the file
   to become available. Check the image digests and the language versions.
2. Update the worker with a merge patch so the HPA replica count stays intact:

   ```sh
   kubectl -n sandbox patch deployment sandbox-worker --type=merge --patch-file worker.json
   kubectl -n sandbox rollout status deployment/sandbox-worker
   ```

   On first install, create the Deployment from `worker.json` after the
   runtime is ready and let the HPA set the replica count.
3. Check the request queue and the result topic. A terminating worker stops
   fetching and has up to 150 seconds to finish or leave the attempt for
   recovery. The Pod grace period is 165 seconds. Keep the old runtime and
   language ConfigMap until no old worker Pods remain, including terminating
   Pods. Check the Pod label `sandbox.overmindv.io/runtime-release`; the
   unversioned worker has no such label.
4. Keep the previous manifests for rollback. Bring its runtime pair back
   first if it has been retired, then patch the worker with the previous
   `worker.json`. Do not delete a runtime pair while any worker still uses it.
