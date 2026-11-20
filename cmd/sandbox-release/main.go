package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	manifest := flag.String("manifest", "", "published Infra release.json")
	phase := flag.String("phase", "", "runtime or worker")
	flag.Parse()
	if err := run(*manifest, *phase, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox release:", err)
		os.Exit(1)
	}
}

func run(manifest, phase string, out io.Writer) error {
	if manifest == "" || (phase != "runtime" && phase != "worker") {
		return errors.New("set -manifest and -phase runtime or worker")
	}
	file, err := os.Open(manifest)
	if err != nil {
		return err
	}
	defer file.Close()
	release, err := readRelease(file)
	if err != nil {
		return err
	}
	head, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != release.source {
		return fmt.Errorf("check out Sandbox at %s before rendering", release.source)
	}
	files := func(path string) ([]byte, error) {
		data, err := exec.Command("git", "show", release.source+":"+path).Output()
		if err != nil {
			return nil, fmt.Errorf("read %s from Sandbox commit: %w", path, err)
		}
		return data, nil
	}
	items, err := release.render(phase, files)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if phase == "worker" {
		return encoder.Encode(items[0])
	}
	return encoder.Encode(object{"apiVersion": "v1", "kind": "List", "items": items})
}
