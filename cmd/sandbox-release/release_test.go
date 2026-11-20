package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"overmindv/sandbox/internal/config"
)

func TestReadReleaseRejectsMutableOrUnrelatedImages(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*publishedManifest)
	}{
		{"tag", func(m *publishedManifest) { m.Images["gojudge"] = m.Plan.Registry + "/gojudge:latest" }},
		{"wrong repository", func(m *publishedManifest) { m.Images["gojudge"] = m.Images["sandbox"] }},
		{"wrong registry", func(m *publishedManifest) { m.Plan.Registry = "registry.test/foreign" }},
		{"branch instead of commit", func(m *publishedManifest) { m.Plan.Sources["sandbox"] = "main" }},
		{"wrong platform", func(m *publishedManifest) { m.Plan.Platform = "linux/arm64" }},
		{"missing image", func(m *publishedManifest) { delete(m.Images, "gojudge") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := manifestFixture("a", "b")
			tc.edit(&input)
			if _, err := readRelease(strings.NewReader(encodeFixture(t, input))); err == nil {
				t.Error("invalid release was accepted")
			}
		})
	}
}

func TestRenderUsesOneReleasePerWorker(t *testing.T) {
	first := releaseFixture(t, "a", "b")
	second := releaseFixture(t, "a", "c")
	if first.id == second.id {
		t.Fatal("runtime change did not change the release ID")
	}
	source := func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
	}
	for _, release := range []releaseSpec{first, second} {
		items, err := release.render("runtime", source)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 7 {
			t.Fatal("expected services, deployments, language manifest, and network policies")
		}
		for index, role := range []string{"gojudge", "gojudge-compiler"} {
			service, deployment := items[2*index], items[2*index+1]
			name := role + "-" + release.id
			if mapAt(service, "metadata")["name"] != name || mapAt(deployment, "metadata")["name"] != name {
				t.Error("runtime objects share an unversioned name", role)
			}
			pod := mapAt(deployment, "spec", "template", "spec")
			if singleContainer(pod, "gojudge")["image"] != release.runtime {
				t.Error("runtime image is not pinned", role)
			}
			serviceLabels := mapAt(service, "spec", "selector")
			podLabels := mapAt(deployment, "spec", "template", "metadata", "labels")
			if serviceLabels[appNameLabel] != name || podLabels[appNameLabel] != name ||
				serviceLabels[releaseLabel] != release.id || podLabels[releaseLabel] != release.id {
				t.Error("runtime Service can select another release", role)
			}
			for k, v := range mapAt(deployment, "spec", "selector", "matchLabels") {
				if podLabels[k] != v {
					t.Error("Deployment selector does not match its pod", role, k)
				}
			}
		}
		assertPinnedLanguages(t, items[4], release)
		ingress, egress := mapAt(items[5], "spec"), mapAt(items[6], "spec")
		allowedFrom := ingress["ingress"].([]object)[0]["from"].([]object)[0]
		allowedTo := egress["egress"].([]object)[0]["to"].([]object)[0]
		if mapAt(ingress, "podSelector", "matchLabels")[componentLabel] != "sandbox-runtime" ||
			len(ingress["egress"].([]object)) != 0 ||
			mapAt(allowedFrom, "podSelector", "matchLabels")[releaseLabel] != release.id ||
			mapAt(allowedTo, "podSelector", "matchLabels")[releaseLabel] != release.id {
			t.Error("network policy does not isolate the runtime by release")
		}
		assertWorker(t, release, source)
	}
}

func assertPinnedLanguages(t *testing.T, item object, release releaseSpec) {
	t.Helper()
	var manifest config.LanguageRegistryConfig
	raw, _ := mapAt(item, "data")["languages.yaml"].(string)
	if err := yaml.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatal(err)
	}
	if item["immutable"] != true || mapAt(item, "metadata")["name"] != "sandbox-languages-"+release.id {
		t.Error("expected a release-specific immutable ConfigMap")
	}
	found := 0
	for _, language := range manifest.Languages {
		if !language.Enabled {
			continue
		}
		found++
		version := language.Versions[0]
		if version.RuntimeImage != release.runtime || version.RuntimeVersion == "" {
			t.Error("runtime pin is missing", language.ID)
		}
	}
	if found != 2 {
		t.Error("expected exact Python and Go pins")
	}
}

func assertWorker(t *testing.T, release releaseSpec, source readSource) {
	t.Helper()
	workers, err := release.render("worker", source)
	if err != nil {
		t.Fatal(err)
	}
	item := workers[0]
	spec := mapAt(item, "spec")
	pod := mapAt(spec, "template", "spec")
	container := singleContainer(pod, "sandbox-worker")
	if container["image"] != release.worker || mapAt(item, "metadata")["name"] != "sandbox-worker" ||
		mapAt(spec, "template", "metadata", "labels")[releaseLabel] != release.id {
		t.Error("worker revision does not match the runtime")
	}
	if _, ownsReplicas := spec["replicas"]; ownsReplicas {
		t.Error("release would reset the HPA replica count")
	}
	env := object{}
	for _, raw := range container["env"].([]any) {
		entry := raw.(object)
		env[entry["name"].(string)] = entry["value"]
	}
	if env["SANDBOX_GOJUDGE_BASE_URL"] != serviceURL("gojudge", release.id) ||
		env["SANDBOX_GOJUDGE_COMPILER_URL"] != serviceURL("gojudge-compiler", release.id) {
		t.Error("worker is not bound to both endpoints")
	}
	if env["SANDBOX_SHUTDOWN_TIMEOUT_MS"] != "150000" || pod["terminationGracePeriodSeconds"] != 165 {
		t.Error("worker lacks time to finish bounded executions during drain")
	}
	volumes := pod["volumes"].([]any)
	if mapAt(volumes[len(volumes)-1].(object), "configMap")["name"] != "sandbox-languages-"+release.id {
		t.Error("worker is missing its pinned language manifest")
	}
}

func manifestFixture(worker, runtime string) publishedManifest {
	var input publishedManifest
	input.Plan.Platform = "linux/amd64"
	input.Plan.Registry = "ghcr.io/overmindv"
	input.Plan.Sources = map[string]string{"sandbox": strings.Repeat("b", 40)}
	input.Images = map[string]string{
		"sandbox": input.Plan.Registry + "/sandbox@sha256:" + strings.Repeat(worker, 64),
		"gojudge": input.Plan.Registry + "/gojudge@sha256:" + strings.Repeat(runtime, 64),
	}
	return input
}

func encodeFixture(t *testing.T, manifest publishedManifest) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func releaseFixture(t *testing.T, worker, runtime string) releaseSpec {
	t.Helper()
	spec, err := readRelease(strings.NewReader(encodeFixture(t, manifestFixture(worker, runtime))))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
