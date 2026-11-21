import contextlib
import copy
import io
import subprocess
import unittest
from unittest.mock import patch

import check_runtime_isolation as check


def runtime(role):
    compiler = role == "gojudge-compiler"
    return {
        "nodeSelector": {check.POOL_LABEL: check.POOL, "kubernetes.io/arch": "amd64"},
        "tolerations": [dict(check.RUNTIME_TAINT, operator="Equal")],
        "automountServiceAccountToken": False,
        "enableServiceLinks": False,
        "containers": [{
            "name": "gojudge",
            "image": f"registry.invalid/{role}@sha256:" + "a" * 64,
            "securityContext": {"privileged": True, "readOnlyRootFilesystem": True},
            "resources": {"limits": {"cpu": "4", "memory": "3Gi" if compiler else "4Gi"}},
            "args": [
                "-http-addr", "0.0.0.0:5050", "-parallelism", "2" if compiler else "16",
                "-container-cred-start", "10000", "-tmp-fs-param", "size=128m,nr_inodes=4096",
                "-output-limit", "33554432" if compiler else "1048576",
                "-copy-out-limit", "1048576", "-file-timeout", "2m", "-no-fallback",
            ],
            "ports": [{"containerPort": 5050, "protocol": "TCP"}],
            "volumeMounts": [
                {"name": "tmp", "mountPath": "/tmp"},
                {"name": "work", "mountPath": "/w"},
                {"name": "shm", "mountPath": "/dev/shm"},
            ],
        }],
        "volumes": [
            {"name": "tmp", "emptyDir": {"medium": "Memory", "sizeLimit": "256Mi" if compiler else "128Mi"}},
            {"name": "work", "emptyDir": {"medium": "Memory", "sizeLimit": "128Mi"}},
            {"name": "shm", "emptyDir": {"medium": "Memory", "sizeLimit": "256Mi"}},
        ],
    }


def snapshot():
    policy = {"kind": "NetworkPolicy", "metadata": {"name": "gojudge-runtime"}, "spec": {
        "podSelector": {"matchLabels": {check.COMPONENT_LABEL: check.POOL}, "matchExpressions": [
            {"key": check.ROLE_LABEL, "operator": "In", "values": ["gojudge", "gojudge-compiler"]},
        ]},
        "policyTypes": ["Ingress", "Egress"],
        "ingress": [{
            "from": [{"podSelector": {"matchLabels": check.WORKER_LABELS}}],
            "ports": [{"port": 5050, "protocol": "TCP"}],
        }],
    }}
    objects = [policy]
    nodes = [{"metadata": {"name": "runner-node", "labels": {
        check.POOL_LABEL: check.POOL, "kubernetes.io/arch": "amd64", "kubernetes.io/os": "linux",
    }}, "spec": {"taints": [check.RUNTIME_TAINT.copy()]}}]
    for role in ("gojudge", "gojudge-compiler"):
        labels = {check.ROLE_LABEL: role, check.COMPONENT_LABEL: check.POOL}
        spec = runtime(role)
        objects.extend([
            {"kind": "Deployment", "metadata": {"name": role}, "spec": {
                "replicas": 1, "template": {"metadata": {"labels": labels}, "spec": spec},
            }},
            {"kind": "Service", "metadata": {"name": role}, "spec": {
                "type": "ClusterIP", "selector": labels, "ports": [{"port": 5050}],
            }},
            {"kind": "Pod", "metadata": {"name": f"{role}-1", "labels": dict(labels, cohort="runtime")}, "spec": {
                **copy.deepcopy(spec), "nodeName": "runner-node",
            }, "status": {"conditions": [{"type": "Ready", "status": "True"}]}},
        ])
    return objects, nodes


class IsolationSnapshotTest(unittest.TestCase):
    def test_healthy_snapshot(self):
        self.assertEqual(check.audit(*snapshot()), [])

    def test_deny_policy_does_not_cancel_an_additive_allow(self):
        objects, nodes = snapshot()
        objects.append({"kind": "NetworkPolicy", "metadata": {"name": "extra"}, "spec": {
            "podSelector": {"matchExpressions": [{"operator": "NotIn", "key": "domain", "values": ["app"]}]},
            "policyTypes": ["Egress"], "egress": [{}],
        }})
        self.assertTrue(any("NetworkPolicy/extra widens" in error for error in check.audit(objects, nodes)))
        objects[-1]["spec"]["podSelector"]["matchExpressions"][0]["operator"] = "In"
        self.assertEqual(check.audit(objects, nodes), [])

    def test_policy_for_ready_pod_labels_is_also_checked(self):
        objects, nodes = snapshot()
        objects.append({"kind": "NetworkPolicy", "metadata": {"name": "extra-ingress"}, "spec": {
            "podSelector": {"matchLabels": {"cohort": "runtime"}},
            "policyTypes": ["Ingress"], "ingress": [{}],
        }})
        errors = check.audit(objects, nodes)
        self.assertTrue(any("Pod/gojudge-1: NetworkPolicy/extra-ingress widens" in error for error in errors))

    def test_rejects_ineligible_nodes_and_foreign_workloads(self):
        objects, nodes = snapshot()
        bad_node = copy.deepcopy(nodes[0])
        bad_node["metadata"]["name"] = "unprepared"
        bad_node["spec"]["taints"] = []
        nodes.append(bad_node)
        objects.append({"kind": "Pod", "metadata": {"name": "app"}, "spec": {"nodeName": "runner-node"}})
        errors = check.audit(objects, nodes)
        self.assertIn("Pod/app does not belong on a dedicated runtime node", errors)
        self.assertIn("Node/unprepared has an invalid runtime pool profile", errors)
        objects[3]["spec"]["nodeName"] = "application-node"
        self.assertIn("Pod/gojudge-1 is not on an eligible runtime node", check.audit(objects, nodes))

    def test_rejects_runtime_exposure_and_missing_policy(self):
        objects, nodes = snapshot()
        objects[2]["spec"]["type"] = "NodePort"
        self.assertTrue(any("Service/gojudge exposes" in error for error in check.audit(objects[1:], nodes)))
        self.assertTrue(any("isolation policy is missing" in error for error in check.audit(objects[1:], nodes)))

    def test_manifest_profile_rejects_drift(self):
        cases = [
            ("host network", {"hostNetwork": True}, {}, "host namespaces"),
            ("host PID", {"hostPID": True}, {}, "host namespaces"),
            ("direct binding", {"nodeName": "other"}, {}, "direct node binding"),
            ("token automount", {"automountServiceAccountToken": True}, {}, "service account token"),
            ("init container", {"initContainers": [{"name": "init"}]}, {}, "only the supervisor"),
            ("injected ephemeral", {"ephemeralContainers": [{"name": "debug"}]}, {}, "only the supervisor"),
            ("secret mount", {"volumes": [{"name": "secret", "secret": {"secretName": "unused"}}]}, {}, "bounded memory"),
            ("tag only", {}, {"image": "runtime:dev"}, "digest-pinned"),
            ("command injection", {}, {"command": ["/bin/sh"]}, "command override"),
            ("host port", {}, {"ports": [{"hostPort": 5050}]}, "host port"),
            ("secret injection", {}, {"envFrom": [{"secretRef": {"name": "unused"}}]}, "environment overrides"),
            ("fallback", {}, {"args": ["-no-fallback"]}, "flags differ"),
        ]
        for name, pod_changes, container_changes, message in cases:
            with self.subTest(name=name):
                spec = runtime("gojudge")
                spec.update(pod_changes)
                spec["containers"][0].update(container_changes)
                self.assertTrue(any(message in error for error in check.runtime_spec_errors(spec, "gojudge")))
        spec = runtime("gojudge")
        spec["containers"][0]["args"].append("-no-fallback=false")
        self.assertIn("go-judge flags differ from the reviewed profile", check.runtime_spec_errors(spec, "gojudge"))

    def test_read_only_with_explicit_context_and_sanitized_errors(self):
        result = subprocess.CompletedProcess([], 0, stdout=b'{"items":[]}', stderr=b"")
        with patch.object(check.subprocess, "run", return_value=result) as run:
            self.assertEqual(check.get_objects("stage", "nodes", "-l", "pool=runtime"), [])
            argv = run.call_args.args[0]
            self.assertEqual(argv[:6], ["kubectl", "--context=stage", "--request-timeout=10s", "get", "nodes", "-l"])
        stderr = io.StringIO()
        with patch.object(check.sys, "argv", ["script", "--context", "stage"]), patch.object(
            check, "get_objects", side_effect=subprocess.CalledProcessError(1, [], stderr=b"private error")
        ), contextlib.redirect_stderr(stderr):
            self.assertEqual(check.main(), 1)
        self.assertNotIn("private error", stderr.getvalue())

    def test_empty_context_is_rejected_before_kubectl(self):
        with patch.object(check.sys, "argv", ["script", "--context="]), patch.object(
            check, "get_objects"
        ) as get, contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as failure:
            check.main()
        self.assertEqual(failure.exception.code, 2)
        get.assert_not_called()


if __name__ == "__main__":
    unittest.main()
