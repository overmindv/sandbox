package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"

	"overmindv/sandbox/internal/tsjob"
)

func main() {
	os.Exit(compile(os.Args[1:], os.Stderr))
}

func compile(sources []string, stderr io.Writer) int {
	if len(sources) == 0 || len(sources) > tsjob.MaxFiles {
		return 2
	}
	output, err := os.MkdirTemp(".", "_sandbox_tsc_")
	if err != nil {
		return 2
	}
	argv := []string{
		"--ignoreConfig", "--pretty", "false", "--strict", "--noEmitOnError",
		"--target", "es2022", "--lib", "es2022", "--module", "node16",
		"--skipLibCheck", "--types", "node", "--typeRoots", "/usr/local/share/sandbox-ts/node_modules/@types",
		"--rootDir", ".", "--outDir", output,
	}
	for _, file := range sources {
		if _, ok := tsjob.JSPath(file); !ok {
			return 2
		}
		argv = append(argv, "./"+file)
	}
	cmd := exec.Command("/usr/local/share/sandbox-ts/compiler/lib/tsc", argv...)
	cmd.Env = []string{"LC_ALL=C.UTF-8", "PATH=/usr/bin:/bin", "GOMAXPROCS=2", "GOMEMLIMIT=320MiB"}
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return tsjob.CompileError
		}
		return 2
	}
	var bundle bytes.Buffer
	if err := tsjob.WriteBundle(&bundle, os.DirFS(output)); err != nil {
		if errors.Is(err, tsjob.ErrLimit) {
			return tsjob.ArtifactLimit
		}
		return 2
	}
	if err := os.WriteFile(tsjob.BundleName, bundle.Bytes(), 0o600); err != nil {
		return 2
	}
	return 0
}
