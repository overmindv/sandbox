package k8s_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKubernetesManifestsParseAndContainRequiredObjects(t *testing.T) {
	manifests, err := loadManifests(".")
	if err != nil {
		t.Errorf("load manifests: %v", err)
		return
	}

	required := map[string]bool{
		"Namespace/sandbox":                      false,
		"ConfigMap/sandbox-worker-config":        false,
		"Secret/sandbox-worker-secrets":          false,
		"Deployment/sandbox-worker":              false,
		"HorizontalPodAutoscaler/sandbox-worker": false,
		"PodDisruptionBudget/sandbox-worker":     false,
		"NetworkPolicy/sandbox-worker":           false,
		"Service/sandbox-worker":                 false,
		"ServiceMonitor/sandbox-worker":          false,
	}

	for _, manifest := range manifests {
		key := manifest.Kind + "/" + manifest.Name
		if _, ok := required[key]; ok {
			required[key] = true
		}
	}

	for key, found := range required {
		if !found {
			t.Errorf("expected Kubernetes manifest %s", key)
		}
	}
}

func TestWorkerDeploymentSecurityBaseline(t *testing.T) {
	manifest, err := findManifest(".", "Deployment", "sandbox-worker")
	if err != nil {
		t.Errorf("find deployment: %v", err)
		return
	}

	spec := mapValue(manifest.Raw["spec"])
	template := mapValue(spec["template"])
	podSpec := mapValue(mapValue(template["spec"]))
	if podSpec["automountServiceAccountToken"] != false {
		t.Errorf("expected automountServiceAccountToken=false, got %#v", podSpec["automountServiceAccountToken"])
	}
	if len(mapValue(podSpec["nodeSelector"])) == 0 {
		t.Error("expected nodeSelector to be configured")
	}

	podSecurity := mapValue(podSpec["securityContext"])
	if podSecurity["runAsNonRoot"] != true {
		t.Errorf("expected pod runAsNonRoot=true, got %#v", podSecurity["runAsNonRoot"])
	}
	if mapValue(podSecurity["seccompProfile"])["type"] != "RuntimeDefault" {
		t.Errorf("expected RuntimeDefault seccomp profile, got %#v", podSecurity["seccompProfile"])
	}

	containers := listValue(podSpec["containers"])
	if len(containers) != 1 {
		t.Errorf("expected one worker container, got %d", len(containers))
		return
	}
	container := mapValue(containers[0])
	containerSecurity := mapValue(container["securityContext"])
	if containerSecurity["allowPrivilegeEscalation"] != false {
		t.Errorf("expected allowPrivilegeEscalation=false, got %#v", containerSecurity["allowPrivilegeEscalation"])
	}
	if containerSecurity["readOnlyRootFilesystem"] != true {
		t.Errorf("expected readOnlyRootFilesystem=true, got %#v", containerSecurity["readOnlyRootFilesystem"])
	}
	if len(mapValue(container["resources"])) == 0 {
		t.Error("expected container resource requests and limits")
	}
}

type manifest struct {
	Kind string
	Name string
	Raw  map[string]any
}

func loadManifests(dir string) ([]manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var manifests []manifest
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		decoder := yaml.NewDecoder(bytes.NewReader(content))
		for {
			var doc map[string]any
			if err := decoder.Decode(&doc); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return nil, err
			}
			if len(doc) == 0 {
				continue
			}
			kind, _ := doc["kind"].(string)
			name, _ := mapValue(doc["metadata"])["name"].(string)
			if kind == "" || name == "" {
				return nil, errors.New("manifest must contain kind and metadata.name")
			}
			manifests = append(manifests, manifest{
				Kind: kind,
				Name: name,
				Raw:  doc,
			})
		}
	}
	return manifests, nil
}

func findManifest(dir string, kind string, name string) (manifest, error) {
	manifests, err := loadManifests(dir)
	if err != nil {
		return manifest{}, err
	}
	for _, manifest := range manifests {
		if manifest.Kind == kind && manifest.Name == name {
			return manifest, nil
		}
	}
	return manifest{}, errors.New("manifest not found")
}

func mapValue(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	typed, _ := value.(map[string]any)
	if typed == nil {
		return map[string]any{}
	}
	return typed
}

func listValue(value any) []any {
	if value == nil {
		return nil
	}
	typed, _ := value.([]any)
	return typed
}
