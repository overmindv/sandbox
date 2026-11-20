package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/languages"
)

const maxManifestBytes = 4 << 20

var (
	sourcePattern   = regexp.MustCompile(`^[a-f0-9]{40}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	registryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*(?::[0-9]+)?(?:/[a-z0-9][a-z0-9._-]*)+$`)
)

type publishedManifest struct {
	Plan struct {
		Platform string            `json:"platform"`
		Registry string            `json:"registry"`
		Sources  map[string]string `json:"sources"`
	} `json:"plan"`
	Images map[string]string `json:"images"`
}

type releaseSpec struct {
	id      string
	source  string
	worker  string
	runtime string
}

func readRelease(input io.Reader) (releaseSpec, error) {
	data, err := io.ReadAll(io.LimitReader(input, maxManifestBytes+1))
	if err != nil {
		return releaseSpec{}, err
	}
	if len(data) > maxManifestBytes {
		return releaseSpec{}, errors.New("release manifest is too large")
	}
	var manifest publishedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return releaseSpec{}, fmt.Errorf("decode release manifest: %w", err)
	}
	registry := manifest.Plan.Registry
	spec := releaseSpec{source: manifest.Plan.Sources["sandbox"], worker: manifest.Images["sandbox"], runtime: manifest.Images["gojudge"]}
	if manifest.Plan.Platform != "linux/amd64" || !sourcePattern.MatchString(spec.source) || !registryPattern.MatchString(registry) {
		return releaseSpec{}, errors.New("expected linux/amd64, a full Sandbox commit, and a release registry")
	}
	for name, image := range map[string]string{"sandbox": spec.worker, "gojudge": spec.runtime} {
		digest, ok := strings.CutPrefix(image, registry+"/"+name+"@")
		if !ok || !digestPattern.MatchString(digest) {
			return releaseSpec{}, fmt.Errorf("%s image must use its release repository and a SHA-256 digest", name)
		}
	}
	sum := sha256.Sum256([]byte(spec.source + "\n" + spec.worker + "\n" + spec.runtime))
	spec.id = "r" + hex.EncodeToString(sum[:8])
	return spec, nil
}

func (r releaseSpec) pinnedLanguages(data []byte) ([]byte, error) {
	var manifest config.LanguageRegistryConfig
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if err := languages.ValidateManifest(manifest); err != nil {
		return nil, err
	}
	found := make(map[string]bool, 2)
	for i := range manifest.Languages {
		language := &manifest.Languages[i]
		if !language.Enabled {
			continue
		}
		if language.ID != "python" && language.ID != "go" {
			return nil, fmt.Errorf("%s does not have a runtime in the release image", language.ID)
		}
		if len(language.Versions) != 1 || language.Versions[0].RuntimeVersion == "" {
			return nil, fmt.Errorf("%s must have one exact runtime version", language.ID)
		}
		language.Versions[0].RuntimeImage = r.runtime
		found[language.ID] = true
	}
	if !found["python"] || !found["go"] {
		return nil, errors.New("release requires runtimes for Python and Go")
	}
	return yaml.Marshal(manifest)
}
