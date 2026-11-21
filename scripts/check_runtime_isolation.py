#!/usr/bin/env python3
import argparse
import json
import re
import subprocess
import sys


NAMESPACE = "sandbox"
POOL_LABEL = "overmindv.io.node-restriction.kubernetes.io/pool"
POOL = "sandbox-runtime"
RUNTIME_TAINT = {"key": "overmindv.io/sandbox-runtime", "value": "true", "effect": "NoSchedule"}
ROLE_LABEL = "app.kubernetes.io/name"
COMPONENT_LABEL = "app.kubernetes.io/component"
WORKER_LABELS = {ROLE_LABEL: "sandbox-worker", COMPONENT_LABEL: "worker"}
VOLUMES = {"tmp": "/tmp", "work": "/w", "shm": "/dev/shm"}
PROFILES = {
    "gojudge": ("16", "1048576", "128Mi"),
    "gojudge-compiler": ("2", "33554432", "256Mi"),
}


def matches(selector, labels):
    if any(labels.get(key) != value for key, value in selector.get("matchLabels", {}).items()):
        return False
    for rule in selector.get("matchExpressions", []):
        key, op = rule["key"], rule["operator"]
        values = rule.get("values", [])
        if op not in {"In", "NotIn", "Exists", "DoesNotExist"}:
            raise ValueError("unknown selector operator")
        match = {
            "In": labels.get(key) in values,
            "NotIn": labels.get(key) not in values,
            "Exists": key in labels,
            "DoesNotExist": key not in labels,
        }
        if not match[op]:
            return False
    return True


def args_match(args, expected):
    remaining = dict(expected)
    index = 0
    while index < len(args):
        flag = args[index]
        if flag not in remaining:
            return False
        value = remaining.pop(flag)
        index += 1
        if value is not None:
            if index == len(args) or args[index] != value:
                return False
            index += 1
    return not remaining


def runtime_spec_errors(spec, profile, deployed=False):
    errors = []
    if spec.get("nodeSelector", {}).get(POOL_LABEL) != POOL:
        errors.append("protected runtime node selector is missing")
    if not any(
        item.get("operator", "Equal") == "Equal"
        and all(item.get(key) == value for key, value in RUNTIME_TAINT.items())
        for item in spec.get("tolerations", [])
    ):
        errors.append("runtime taint toleration is missing")
    if (not deployed and spec.get("nodeName")) or any(
        spec.get(key) for key in ("hostNetwork", "hostPID", "hostIPC")
    ):
        errors.append("direct node binding or host namespaces are enabled")
    if spec.get("automountServiceAccountToken") is not False or spec.get("enableServiceLinks") is not False:
        errors.append("service account token or service environment is enabled")

    containers = spec.get("containers", [])
    if len(containers) != 1 or spec.get("initContainers") or spec.get("ephemeralContainers"):
        return errors + ["only the supervisor container is allowed"]
    container = containers[0]
    if container.get("name") != "gojudge" or container.get("command") or not re.search(
        r"@sha256:[0-9a-f]{64}\Z", container.get("image", "")
    ):
        errors.append("expected the digest-pinned supervisor image without a command override")
    security = container.get("securityContext", {})
    if security.get("privileged") is not True or security.get("readOnlyRootFilesystem") is not True:
        errors.append("supervisor privilege or read-only root differs from the reviewed profile")
    if container.get("env") or container.get("envFrom") or container.get("volumeDevices"):
        errors.append("runtime must not receive environment overrides or raw volumes")
    if any(port.get("hostPort") or port.get("hostIP") for port in container.get("ports", [])):
        errors.append("runtime port must not bind to a host port")
    limits = container.get("resources", {}).get("limits", {})
    if limits.get("cpu") != "4" or limits.get("memory") != ("4Gi" if profile == "gojudge" else "3Gi"):
        errors.append("runtime CPU or memory limit differs from the reviewed profile")

    parallelism, output, tmp = PROFILES[profile]
    expected = {
        "-http-addr": "0.0.0.0:5050", "-parallelism": parallelism, "-container-cred-start": "10000",
        "-tmp-fs-param": "size=128m,nr_inodes=4096", "-output-limit": output,
        "-copy-out-limit": "1048576", "-file-timeout": "2m", "-no-fallback": None,
    }
    if not args_match(container.get("args", []), expected):
        errors.append("go-judge flags differ from the reviewed profile")

    volumes = spec.get("volumes", [])
    sizes = {"tmp": tmp, "work": "128Mi", "shm": "256Mi"}
    if len(volumes) != len(sizes) or {
        volume.get("name"): volume.get("emptyDir") for volume in volumes if set(volume) == {"name", "emptyDir"}
    } != {name: {"medium": "Memory", "sizeLimit": size} for name, size in sizes.items()}:
        errors.append("only the three bounded memory volumes are allowed")
    mounts = container.get("volumeMounts", [])
    if len(mounts) != len(VOLUMES) or {
        mount.get("name"): mount.get("mountPath") for mount in mounts
        if not any(mount.get(key) for key in ("subPath", "subPathExpr", "mountPropagation"))
    } != VOLUMES:
        errors.append("runtime mount paths differ from the reviewed profile")
    return errors


def worker_ingress_only(rule):
    return (
        rule.get("from") == [{"podSelector": {"matchLabels": WORKER_LABELS}}]
        and rule.get("ports") == [{"protocol": "TCP", "port": 5050}]
    )


def network_errors(objects, labels):
    errors = []
    expected_policy = False
    for obj in objects:
        spec = obj["spec"]
        name = obj["metadata"]["name"]
        if obj["kind"] == "NetworkPolicy" and matches(spec["podSelector"], labels):
            ingress = spec.get("ingress", [])
            if spec.get("egress") or not all(worker_ingress_only(rule) for rule in ingress):
                errors.append(f"NetworkPolicy/{name} widens runtime access")
            if name == "gojudge-runtime":
                expected_policy = set(spec.get("policyTypes", [])) == {"Ingress", "Egress"} and len(ingress) == 1
        elif obj["kind"] == "Service" and spec.get("selector") and matches(
            {"matchLabels": spec["selector"]}, labels
        ):
            if spec.get("type", "ClusterIP") != "ClusterIP" or spec.get("externalIPs") or any(
                port.get("nodePort") for port in spec.get("ports", [])
            ):
                errors.append(f"Service/{name} exposes a runtime port outside the cluster")
    if not expected_policy:
        errors.append("gojudge-runtime ingress and egress isolation policy is missing")
    return errors


def node_is_eligible(node):
    labels = node["metadata"].get("labels", {})
    return (
        labels.get(POOL_LABEL) == POOL
        and labels.get("overmindv.io/node-pool") in (None, POOL)
        and labels.get("kubernetes.io/arch") == "amd64"
        and labels.get("kubernetes.io/os") == "linux"
        and not ({"node-role.kubernetes.io/control-plane", "node-role.kubernetes.io/master"} & labels.keys())
        and any(all(taint.get(key) == value for key, value in RUNTIME_TAINT.items())
                for taint in node["spec"].get("taints", []))
    )


def audit(objects, nodes):
    by_name = {(o["kind"], o["metadata"]["name"]): o for o in objects}
    pods = [o for o in objects if o["kind"] == "Pod"]
    network = [o for o in objects if o["kind"] in {"NetworkPolicy", "Service"}]
    eligible = {node["metadata"]["name"] for node in nodes if node_is_eligible(node)}
    errors = ["no eligible dedicated runtime nodes"] if not eligible else []
    for node in nodes:
        if node["metadata"]["name"] not in eligible:
            errors.append(f"Node/{node['metadata']['name']} has an invalid runtime pool profile")
    for pod in pods:
        labels = pod["metadata"].get("labels", {})
        is_runtime = labels.get(ROLE_LABEL) in PROFILES and labels.get(COMPONENT_LABEL) == POOL
        if pod["spec"].get("nodeName") in eligible and not is_runtime:
            errors.append(f"Pod/{pod['metadata']['name']} does not belong on a dedicated runtime node")
    for role in PROFILES:
        deployment = by_name.get(("Deployment", role))
        if not deployment:
            errors.append(f"Deployment/{role} is missing")
            continue
        template = deployment["spec"]["template"]
        labels = template["metadata"]["labels"]
        identity = {ROLE_LABEL: role, COMPONENT_LABEL: POOL}
        if not matches({"matchLabels": identity}, labels):
            errors.append(f"Deployment/{role} runtime labels are missing")
        service = by_name.get(("Service", role))
        if not service or service["spec"].get("selector") != identity:
            errors.append(f"Service/{role} does not select its runtime")
        errors.extend(f"Deployment/{role}: {error}" for error in runtime_spec_errors(template["spec"], role))
        errors.extend(f"Deployment/{role}: {error}" for error in network_errors(network, labels))

        current = [
            pod for pod in pods if matches({"matchLabels": identity}, pod["metadata"].get("labels", {}))
            and not pod["metadata"].get("deletionTimestamp")
        ]
        desired = deployment["spec"].get("replicas", 1)
        if desired < 1 or len(current) != desired:
            errors.append(f"Deployment/{role} must have all requested replicas present")
        for pod in current:
            name = pod["metadata"]["name"]
            spec = pod["spec"]
            if spec.get("nodeName") not in eligible:
                errors.append(f"Pod/{name} is not on an eligible runtime node")
            if not any(condition["type"] == "Ready" and condition["status"] == "True"
                       for condition in pod.get("status", {}).get("conditions", [])):
                errors.append(f"Pod/{name} is not ready")
            if [c.get("image") for c in spec.get("containers", [])] != [
                c.get("image") for c in template["spec"].get("containers", [])
            ]:
                errors.append(f"Pod/{name} still uses a different runtime image")
            errors.extend(f"Pod/{name}: {error}" for error in runtime_spec_errors(spec, role, deployed=True))
            errors.extend(f"Pod/{name}: {error}" for error in network_errors(network, pod["metadata"]["labels"]))
    return errors


def get_objects(context, resource, *options):
    result = subprocess.run(
        ["kubectl", f"--context={context}", "--request-timeout=10s", "get", resource, *options, "-o", "json"],
        capture_output=True, timeout=45, check=True,
    )
    return json.loads(result.stdout)["items"]


def main():
    parser = argparse.ArgumentParser(description="Read-only check of a dedicated sandbox runtime deployment.")
    parser.add_argument("--context", required=True, help="explicit kubeconfig context for the test cluster")
    args = parser.parse_args()
    if not args.context.strip():
        parser.error("--context must name a kubeconfig context")
    try:
        objects = get_objects(args.context, "deployments,services,networkpolicies,pods", "-n", NAMESPACE)
        nodes = get_objects(args.context, "nodes", "-l", f"{POOL_LABEL}={POOL}")
        errors = audit(objects, nodes)
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError, AttributeError):
        print("Cannot read or validate runtime state. Check the context and read permissions.", file=sys.stderr)
        return 1
    for error in errors:
        print(error, file=sys.stderr)
    if errors:
        return 1
    print("Runtime placement and policy configuration checked. Runtime enforcement is not tested.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
