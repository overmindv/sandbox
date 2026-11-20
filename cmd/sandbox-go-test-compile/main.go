package main

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"overmindv/sandbox/internal/gotestjob"
)

const goTool = "/usr/local/go/bin/go"

var errBuild = errors.New("could not compile Go tests")

func main() {
	if err := compile(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errBuild) {
			os.Exit(1)
		}
		os.Exit(2)
	}
}

func compile() error {
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(gotestjob.BundleName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = buildTests(workspace, goTool, out, os.Stdout, os.Stderr)
	return errors.Join(err, out.Close())
}

type listedPackage struct {
	Dir          string
	ImportPath   string
	TestGoFiles  []string
	XTestGoFiles []string
}

func listPackages(tool, workspace string, stderr io.Writer) ([]listedPackage, error) {
	cmd := exec.Command(tool, "list", "-mod=readonly", "-json", "./...")
	cmd.Dir = workspace
	cmd.Stderr = stderr
	stream, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	bounded := &io.LimitedReader{R: stream, N: 1 << 20}
	list, readErr := decodePackageList(bounded)
	if bounded.N == 0 {
		readErr = fmt.Errorf("%w: package list exceeds the size limit", errBuild)
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
		return nil, errors.Join(readErr, cmd.Wait())
	}
	if err := cmd.Wait(); err != nil {
		return nil, goCommandError(err)
	}
	return list, nil
}

func decodePackageList(stream io.Reader) ([]listedPackage, error) {
	decoder := json.NewDecoder(stream)
	var packages []listedPackage
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			return packages, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: invalid package list", errBuild)
		}
		if len(packages) >= gotestjob.MaxPackages {
			return nil, fmt.Errorf("%w: too many Go packages", errBuild)
		}
		packages = append(packages, pkg)
	}
}

func buildTests(workspace, tool string, out, stdout, stderr io.Writer) error {
	list, err := listPackages(tool, workspace, stderr)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return fmt.Errorf("%w: no packages matched ./...", errBuild)
	}
	binary, err := os.CreateTemp("", "sandbox-go-test-*")
	if err != nil {
		return err
	}
	name := binary.Name()
	defer os.Remove(name)
	if err := binary.Close(); err != nil {
		return err
	}

	archive := tar.NewWriter(out)
	packages := make([]gotestjob.Package, 0, len(list))
	var total int64
	for _, pkg := range list {
		dir, err := packageDir(workspace, pkg.Dir)
		if err != nil {
			return err
		}
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		target := "."
		if dir != "." {
			target = "./" + dir
		}
		cmd := exec.Command(tool, "test", "-c", "-mod=readonly", "-trimpath", "-p=2", "-ldflags=-s -w", "-o", name, target)
		cmd.Dir, cmd.Stdout, cmd.Stderr = workspace, stdout, stderr
		if err := cmd.Run(); err != nil {
			return goCommandError(err)
		}
		if len(pkg.TestGoFiles)+len(pkg.XTestGoFiles) == 0 {
			continue
		}
		size, err := appendBinary(archive, name, gotestjob.BinaryName(len(packages)), total)
		if err != nil {
			return err
		}
		total += size
		packages = append(packages, gotestjob.Package{Dir: dir, ImportPath: pkg.ImportPath})
	}
	index, err := json.Marshal(packages)
	if err != nil {
		return err
	}
	if len(index) > gotestjob.MaxIndexBytes {
		return fmt.Errorf("%w: test package index is too large", errBuild)
	}
	if err := archive.WriteHeader(header(gotestjob.IndexName, int64(len(index)))); err != nil {
		return err
	}
	if _, err := archive.Write(index); err != nil {
		return err
	}
	return archive.Close()
}

func appendBinary(archive *tar.Writer, filename, entry string, total int64) (int64, error) {
	binary, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer binary.Close()
	info, err := binary.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	const metadata = gotestjob.MaxPackages*1024 + gotestjob.MaxIndexBytes + 2048
	if size <= 0 || size > gotestjob.MaxBinaryBytes || total+size+metadata > gotestjob.MaxBundleBytes {
		return 0, fmt.Errorf("%w: test binaries exceed the artifact limit", errBuild)
	}
	if err := archive.WriteHeader(header(entry, size)); err != nil {
		return 0, err
	}
	if _, err := io.CopyN(archive, binary, size); err != nil {
		return 0, err
	}
	return size, nil
}

func header(name string, size int64) *tar.Header {
	return &tar.Header{Name: name, Mode: 0o600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
}

func packageDir(workspace, dir string) (string, error) {
	rel, err := filepath.Rel(workspace, dir)
	rel = filepath.ToSlash(rel)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%w: package outside workspace", errBuild)
	}
	return rel, nil
}

func goCommandError(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("%w: go command exited with status %d", errBuild, exit.ExitCode())
	}
	return err
}
