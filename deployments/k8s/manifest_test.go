package k8s_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
		"Service/gojudge":                        false,
		"Deployment/gojudge":                     false,
		"Service/gojudge-compiler":               false,
		"Deployment/gojudge-compiler":            false,
		"HorizontalPodAutoscaler/sandbox-worker": false,
		"PodDisruptionBudget/sandbox-worker":     false,
		"NetworkPolicy/sandbox-worker":           false,
		"NetworkPolicy/gojudge-runtime":          false,
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

func TestRuntimeDeploymentHardeningBaseline(t *testing.T) {
	manifest, err := findManifest(".", "Deployment", "gojudge")
	if err != nil {
		t.Errorf("find runtime deployment: %v", err)
		return
	}

	podSpec := podSpecFromDeployment(manifest)
	if podSpec["automountServiceAccountToken"] != false {
		t.Errorf("expected runtime automountServiceAccountToken=false, got %#v", podSpec["automountServiceAccountToken"])
	}
	nodeSelector := mapValue(podSpec["nodeSelector"])
	if nodeSelector["overmindv.io/node-pool"] != "sandbox-runtime" {
		t.Errorf("expected sandbox-runtime node pool, got %#v", nodeSelector)
	}
	podSecurity := mapValue(podSpec["securityContext"])
	if mapValue(podSecurity["seccompProfile"])["type"] != "RuntimeDefault" {
		t.Errorf("expected RuntimeDefault seccomp profile, got %#v", podSecurity["seccompProfile"])
	}

	template := mapValue(mapValue(manifest.Raw["spec"])["template"])
	annotations := mapValue(mapValue(template["metadata"])["annotations"])
	if annotations["container.apparmor.security.beta.kubernetes.io/gojudge"] == "" {
		t.Errorf("expected AppArmor annotation, got %#v", annotations)
	}

	container, ok := containerByName(podSpec, "gojudge")
	if !ok {
		t.Error("expected gojudge container")
		return
	}
	if len(listValue(container["env"])) != 0 || len(listValue(container["envFrom"])) != 0 {
		t.Errorf("runtime container must not receive env or secrets, got env=%#v envFrom=%#v", container["env"], container["envFrom"])
	}
	containerSecurity := mapValue(container["securityContext"])
	if containerSecurity["privileged"] != true {
		t.Errorf("expected privileged go-judge supervisor, got %#v", containerSecurity["privileged"])
	}
	if containerSecurity["readOnlyRootFilesystem"] != true {
		t.Errorf("expected runtime readOnlyRootFilesystem=true, got %#v", containerSecurity["readOnlyRootFilesystem"])
	}
	args := stringList(container["args"])
	for _, expected := range []string{"-no-fallback", "-container-cred-start", "-tmp-fs-param", "-output-limit", "-copy-out-limit"} {
		if !containsString(args, expected) {
			t.Errorf("expected runtime args to contain %q, got %#v", expected, args)
		}
	}

	volumes := listValue(podSpec["volumes"])
	if !hasMemoryEmptyDir(volumes, "tmp") || !hasMemoryEmptyDir(volumes, "work") || !hasMemoryEmptyDir(volumes, "shm") {
		t.Errorf("expected tmp/work/shm memory emptyDir volumes, got %#v", volumes)
	}
}

func TestWorkerShutdownFitsTerminationGracePeriod(t *testing.T) {
	deployment, err := findManifest(".", "Deployment", "sandbox-worker")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := findManifest(".", "ConfigMap", "sandbox-worker-config")
	if err != nil {
		t.Fatal(err)
	}
	grace, _ := podSpecFromDeployment(deployment)["terminationGracePeriodSeconds"].(int)
	value, _ := mapValue(cfg.Raw["data"])["SANDBOX_SHUTDOWN_TIMEOUT_MS"].(string)
	timeout, err := strconv.Atoi(value)
	if err != nil || timeout <= 0 || timeout >= grace*1000 {
		t.Errorf("shutdown timeout %q must fit inside %ds termination grace period", value, grace)
	}
}

func TestKafkaCredentialsMountedOnlyInWorker(t *testing.T) {
	for _, name := range []string{"sandbox-worker", "gojudge", "gojudge-compiler"} {
		deployment, err := findManifest(".", "Deployment", name)
		if err != nil {
			t.Fatal(err)
		}
		pod := podSpecFromDeployment(deployment)
		var secretOK, mountOK bool
		for _, item := range listValue(pod["volumes"]) {
			volume := mapValue(item)
			secret := mapValue(volume["secret"])
			if secret["secretName"] == "sandbox-kafka-client" {
				if name != "sandbox-worker" {
					t.Fatal("go-judge received Kafka credential volume")
				}
				secretOK = volume["name"] == "kafka-client" && secret["defaultMode"] == 0o440
			}
		}
		for _, item := range listValue(pod["containers"]) {
			for _, entry := range listValue(mapValue(item)["volumeMounts"]) {
				mount := mapValue(entry)
				if mount["mountPath"] == "/run/secrets/sandbox-kafka" {
					if name != "sandbox-worker" {
						t.Fatal("go-judge received Kafka credential mount")
					}
					mountOK = mount["name"] == "kafka-client" && mount["readOnly"] == true
				}
			}
		}
		if name == "sandbox-worker" && (!secretOK || !mountOK) {
			t.Error("worker lacks its read-only Kafka Secret mount")
		}
	}
}

func TestCompilerHasSeparateResourceProfile(t *testing.T) {
	for _, profile := range []struct {
		name string
		cap  string
	}{{"gojudge", "1048576"}, {"gojudge-compiler", "33554432"}} {
		deployment, err := findManifest(".", "Deployment", profile.name)
		if err != nil {
			t.Fatal(err)
		}
		pod := podSpecFromDeployment(deployment)
		container, ok := containerByName(pod, "gojudge")
		if !ok {
			t.Fatal("missing go-judge container", profile.name)
		}
		args := stringList(container["args"])
		for flag, value := range map[string]string{
			"-output-limit": profile.cap, "-copy-out-limit": "1048576", "-file-timeout": "2m",
			"-tmp-fs-param": "size=128m,nr_inodes=4096",
		} {
			i := slices.Index(args, flag)
			if i < 0 || i+1 >= len(args) || args[i+1] != value {
				t.Error("unexpected resource flag", profile.name, flag)
			}
		}
		if pod["automountServiceAccountToken"] != false || !containsString(args, "-no-fallback") ||
			len(listValue(container["env"])) != 0 || len(listValue(container["envFrom"])) != 0 {
			t.Error("runtime must not receive credentials or fall back to host execution", profile.name)
		}
		if mapValue(pod["nodeSelector"])["kubernetes.io/arch"] != "amd64" {
			t.Error("compiler and runner must use the release image architecture", profile.name)
		}
	}
}

func TestRuntimeNetworkPolicyDeniesAllEgress(t *testing.T) {
	manifest, err := findManifest(".", "NetworkPolicy", "gojudge-runtime")
	if err != nil {
		t.Errorf("find runtime network policy: %v", err)
		return
	}

	spec := mapValue(manifest.Raw["spec"])
	types := stringList(spec["policyTypes"])
	if !containsString(types, "Egress") || !containsString(types, "Ingress") {
		t.Errorf("expected runtime network policy to include Egress and Ingress, got %#v", types)
	}
	if len(listValue(spec["egress"])) != 0 {
		t.Errorf("expected runtime egress to be empty, got %#v", spec["egress"])
	}
	selector := mapValue(spec["podSelector"])
	expressions := listValue(selector["matchExpressions"])
	if len(expressions) != 1 {
		t.Fatal("expected a selector for both compiler and runner")
	}
	expression := mapValue(expressions[0])
	if expression["key"] != "app.kubernetes.io/name" || expression["operator"] != "In" ||
		!slices.Equal(stringList(expression["values"]), []string{"gojudge", "gojudge-compiler"}) {
		t.Error("network policy must include both compiler and runner")
	}
	ingress := listValue(spec["ingress"])
	if len(ingress) != 1 {
		t.Fatal("expected one runtime ingress rule")
	}
	from := listValue(mapValue(ingress[0])["from"])
	if len(from) != 1 {
		t.Fatal("expected ingress only from workers")
	}
	peer := mapValue(from[0])
	labels := mapValue(mapValue(peer["podSelector"])["matchLabels"])
	if len(peer) != 1 || labels["app.kubernetes.io/name"] != "sandbox-worker" ||
		labels["app.kubernetes.io/component"] != "worker" {
		t.Error("runtime ingress must only allow workers in the sandbox namespace")
	}
}

func TestWorkerNetworkPolicyBlocksMetadataEndpointRanges(t *testing.T) {
	manifest, err := findManifest(".", "NetworkPolicy", "sandbox-worker")
	if err != nil {
		t.Errorf("find worker network policy: %v", err)
		return
	}

	if !networkPolicyExceptsCIDR(manifest, "169.254.0.0/16") {
		t.Error("expected worker network policy to exclude metadata endpoint CIDR 169.254.0.0/16")
	}
}

func TestManifestsDoNotUseHostPathOrDockerSocket(t *testing.T) {
	manifests, err := loadManifests(".")
	if err != nil {
		t.Errorf("load manifests: %v", err)
		return
	}

	for _, manifest := range manifests {
		if manifest.Kind != "Deployment" {
			continue
		}
		podSpec := podSpecFromDeployment(manifest)
		for _, volume := range listValue(podSpec["volumes"]) {
			volumeMap := mapValue(volume)
			if _, ok := volumeMap["hostPath"]; ok {
				t.Errorf("%s/%s must not use hostPath volume %#v", manifest.Kind, manifest.Name, volumeMap)
			}
		}
		for _, container := range listValue(podSpec["containers"]) {
			containerMap := mapValue(container)
			for _, mount := range listValue(containerMap["volumeMounts"]) {
				mountPath, _ := mapValue(mount)["mountPath"].(string)
				if mountPath == "/var/run/docker.sock" {
					t.Errorf("%s/%s must not mount Docker socket", manifest.Kind, manifest.Name)
				}
			}
		}
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

func podSpecFromDeployment(manifest manifest) map[string]any {
	spec := mapValue(manifest.Raw["spec"])
	template := mapValue(spec["template"])
	return mapValue(template["spec"])
}

func containerByName(podSpec map[string]any, name string) (map[string]any, bool) {
	for _, container := range listValue(podSpec["containers"]) {
		containerMap := mapValue(container)
		if containerMap["name"] == name {
			return containerMap, true
		}
	}
	return nil, false
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

func stringList(value any) []string {
	values := listValue(value)
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		if text != "" {
			result = append(result, text)
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasMemoryEmptyDir(volumes []any, name string) bool {
	for _, volume := range volumes {
		volumeMap := mapValue(volume)
		if volumeMap["name"] != name {
			continue
		}
		emptyDir := mapValue(volumeMap["emptyDir"])
		return emptyDir["medium"] == "Memory" && emptyDir["sizeLimit"] != ""
	}
	return false
}

func networkPolicyExceptsCIDR(manifest manifest, cidr string) bool {
	spec := mapValue(manifest.Raw["spec"])
	for _, egress := range listValue(spec["egress"]) {
		for _, target := range listValue(mapValue(egress)["to"]) {
			except := listValue(mapValue(mapValue(target)["ipBlock"])["except"])
			for _, item := range except {
				if item == cidr {
					return true
				}
			}
		}
	}
	return false
}
