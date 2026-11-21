# Runtime Trust Boundary

The worker handles Kafka, database and object storage credentials. Only the
trusted runtime supervisor is privileged. Submitted programs and compilers
processing their code must run within go-judge limits and must not receive
application credentials.

The compiler and runner use dedicated Linux nodes. A successful breakout from
the privileged supervisor can compromise that node, so the node must not host
ordinary application workloads. A taint is a scheduler constraint, not a
permission boundary. Restrict who can label nodes, assign workloads to the pool
or create privileged pods.

Pod NetworkPolicy does not prove that access to the node itself or its cloud
metadata is blocked. CNI enforcement, node isolation and the per-run limits need
separate checks on the deployment environment. The
[stage checklist](security-hardening.md#stage-acceptance) lists the required
evidence.
