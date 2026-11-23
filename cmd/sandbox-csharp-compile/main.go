package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"overmindv/sandbox/internal/dotnetjob"
)

const (
	compiler = "/usr/local/share/dotnet/sdk/10.0.404/Roslyn/bincore/csc.dll"
	refDir   = "/usr/local/share/dotnet/packs/Microsoft.NETCore.App.Ref/10.0.15/ref/net10.0"
)

func main() {
	os.Exit(compile(os.Args[1:], os.Stderr))
}

func compile(sources []string, diagnostics io.Writer) int {
	if len(sources) == 0 || len(sources) > dotnetjob.MaxSources {
		return 2
	}
	refs, err := os.ReadDir(refDir)
	if err != nil || len(refs) == 0 {
		return 2
	}
	dir, err := os.MkdirTemp(".", "_sandbox_csc_")
	if err != nil {
		return 2
	}
	output := filepath.Join(dir, dotnetjob.AssemblyName)
	args := []string{
		"exec", compiler, "-nologo", "-noconfig", "-nostdlib+", "-platform:anycpu",
		"-target:exe", "-langversion:14.0", "-optimize+", "-deterministic+", "-utf8output",
		"-out:" + output,
	}
	for _, ref := range refs {
		if ref.Type().IsRegular() && filepath.Ext(ref.Name()) == ".dll" {
			args = append(args, "-reference:"+filepath.Join(refDir, ref.Name()))
		}
	}
	for _, source := range sources {
		if !dotnetjob.ValidSourcePath(source) {
			return 2
		}
		args = append(args, "./"+source)
	}
	cmd := exec.Command(dotnetjob.DotnetPath, args...)
	cmd.Env = []string{
		"LC_ALL=C.UTF-8", "PATH=/usr/bin:/bin", "DOTNET_CLI_HOME=/tmp",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_EnableDiagnostics=0",
		"DOTNET_PROCESSOR_COUNT=2", "DOTNET_GCHeapHardLimit=0x10000000",
	}
	cmd.Stdout, cmd.Stderr = diagnostics, diagnostics
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return dotnetjob.CompileError
		}
		return 2
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return 2
	}
	if info.Size() > dotnetjob.MaxBytes {
		return dotnetjob.ArtifactLimit
	}
	if err := os.Rename(output, dotnetjob.AssemblyName); err != nil {
		return 2
	}
	return 0
}
