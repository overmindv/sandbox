package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const (
	releaseLabel   = "sandbox.overmindv.io/runtime-release"
	appNameLabel   = "app.kubernetes.io/name"
	componentLabel = "app.kubernetes.io/component"
	namespace      = "sandbox"
	templateDir    = "deployments/k8s/"
)

type object = map[string]any
type readSource func(string) ([]byte, error)

func (r releaseSpec) render(phase string, read readSource) ([]object, error) {
	if phase == "worker" {
		return r.renderWorker(read)
	}
	if phase != "runtime" {
		return nil, errors.New("unknown release phase")
	}
	var result []object
	for _, role := range []string{"gojudge", "gojudge-compiler"} {
		name := role
		if role == "gojudge" {
			name = "gojudge-runtime"
		}
		objects, err := decodeTemplates(read, name+".deployment.yaml")
		if err != nil {
			return nil, err
		}
		if err := r.pinRuntime(role, objects); err != nil {
			return nil, err
		}
		result = append(result, objects...)
	}
	data, err := read("configs/languages.yaml")
	if err != nil {
		return nil, err
	}
	pinned, err := r.pinnedLanguages(data)
	if err != nil {
		return nil, fmt.Errorf("pin language manifest: %w", err)
	}
	config := object{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": r.metadata("sandbox-languages-" + r.id), "immutable": true,
		"data": object{"languages.yaml": string(pinned)},
	}
	return append(result, config, r.ingressPolicy(), r.egressPolicy()), nil
}

func (r releaseSpec) pinRuntime(role string, objects []object) error {
	if len(objects) != 2 || objects[0]["kind"] != "Service" || objects[1]["kind"] != "Deployment" {
		return fmt.Errorf("%s template must contain a Service and a Deployment", role)
	}
	name := role + "-" + r.id
	for _, item := range objects {
		meta := mapAt(item, "metadata")
		if meta == nil || meta["name"] != role || meta["namespace"] != namespace {
			return fmt.Errorf("unexpected %s metadata", role)
		}
		meta["name"] = name
		addLabels(meta, object{appNameLabel: name, releaseLabel: r.id})
	}
	// Keep released pods out of the original, unversioned Service selectors.
	service := mapAt(objects[0], "spec")
	selector := mapAt(objects[1], "spec", "selector")
	template := mapAt(objects[1], "spec", "template", "metadata")
	pod := mapAt(objects[1], "spec", "template", "spec")
	container := singleContainer(pod, "gojudge")
	if service == nil || selector == nil || template == nil || container == nil {
		return fmt.Errorf("incomplete %s template", role)
	}
	labels := object{appNameLabel: name, componentLabel: "sandbox-runtime", releaseLabel: r.id}
	service["selector"] = labels
	selector["matchLabels"] = labels
	addLabels(template, labels)
	if spread, ok := pod["topologySpreadConstraints"].([]any); ok {
		for _, raw := range spread {
			item, _ := raw.(object)
			if match := mapAt(item, "labelSelector"); match != nil {
				match["matchLabels"] = labels
			}
		}
	}
	container["image"] = r.runtime
	return nil
}

func (r releaseSpec) renderWorker(read readSource) ([]object, error) {
	objects, err := decodeTemplates(read, "sandbox-worker.deployment.yaml")
	if err != nil {
		return nil, err
	}
	if len(objects) != 1 || objects[0]["kind"] != "Deployment" {
		return nil, errors.New("expected one worker Deployment")
	}
	item := objects[0]
	deployment := mapAt(item, "spec")
	meta := mapAt(item, "spec", "template", "metadata")
	pod := mapAt(item, "spec", "template", "spec")
	container := singleContainer(pod, "sandbox-worker")
	node := mapAt(pod, "nodeSelector")
	if meta == nil || container == nil || node == nil {
		return nil, errors.New("incomplete worker template")
	}
	delete(deployment, "replicas")
	addLabels(meta, object{releaseLabel: r.id})
	container["image"] = r.worker
	node["kubernetes.io/arch"] = "amd64"
	pod["terminationGracePeriodSeconds"] = 165
	env, _ := container["env"].([]any)
	env = append(env,
		object{"name": "SANDBOX_GOJUDGE_BASE_URL", "value": serviceURL("gojudge", r.id)},
		object{"name": "SANDBOX_GOJUDGE_COMPILER_URL", "value": serviceURL("gojudge-compiler", r.id)},
		object{"name": "SANDBOX_SHUTDOWN_TIMEOUT_MS", "value": "150000"},
	)
	container["env"] = env
	volumes, _ := pod["volumes"].([]any)
	pod["volumes"] = append(volumes, object{"name": "release-languages", "configMap": object{"name": "sandbox-languages-" + r.id}})
	mounts, _ := container["volumeMounts"].([]any)
	container["volumeMounts"] = append(mounts, object{
		"name": "release-languages", "mountPath": "/app/configs/languages.yaml",
		"subPath": "languages.yaml", "readOnly": true,
	})
	return objects, nil
}

func (r releaseSpec) metadata(name string) object {
	return object{"name": name, "namespace": namespace, "labels": object{releaseLabel: r.id}}
}

func (r releaseSpec) ingressPolicy() object {
	return object{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": r.metadata("sandbox-runtime-" + r.id),
		"spec": object{
			"podSelector": selector("sandbox-runtime", r.id),
			"policyTypes": []string{"Ingress", "Egress"},
			"ingress":     []object{{"from": []object{{"podSelector": selector("worker", r.id)}}, "ports": runtimePort()}},
			"egress":      []object{},
		},
	}
}

func (r releaseSpec) egressPolicy() object {
	return object{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": r.metadata("sandbox-worker-runtime-" + r.id),
		"spec": object{
			"podSelector": selector("worker", r.id),
			"policyTypes": []string{"Egress"},
			"egress":      []object{{"to": []object{{"podSelector": selector("sandbox-runtime", r.id)}}, "ports": runtimePort()}},
		},
	}
}

func selector(component, release string) object {
	return object{"matchLabels": object{componentLabel: component, releaseLabel: release}}
}

func runtimePort() []object {
	return []object{{"protocol": "TCP", "port": 5050}}
}

func serviceURL(role, id string) string {
	return "http://" + role + "-" + id + "." + namespace + ".svc.cluster.local:5050"
}

func decodeTemplates(read readSource, name string) ([]object, error) {
	data, err := read(templateDir + name)
	if err != nil {
		return nil, err
	}
	var items []object
	d := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var item object
		err := d.Decode(&item)
		if errors.Is(err, io.EOF) {
			return items, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		items = append(items, item)
	}
}

func mapAt(item object, path ...string) object {
	for _, key := range path {
		item, _ = item[key].(object)
	}
	return item
}

func addLabels(meta, added object) {
	labels := mapAt(meta, "labels")
	if labels == nil {
		labels = object{}
		meta["labels"] = labels
	}
	for name, value := range added {
		labels[name] = value
	}
}

func singleContainer(pod object, name string) object {
	list, _ := pod["containers"].([]any)
	if len(list) != 1 {
		return nil
	}
	container, _ := list[0].(object)
	if container["name"] != name {
		return nil
	}
	return container
}
