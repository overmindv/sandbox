package main

import (
	"io"
	"os"
	"path"
	"strconv"
	"syscall"

	"overmindv/sandbox/internal/tsjob"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = io.WriteString(os.Stderr, "could not start compiled TypeScript\n")
	}
	os.Exit(125)
}

func run(argv []string) error {
	if len(argv) != 2 {
		return tsjob.ErrBundle
	}
	entry, heap := argv[0], argv[1]
	mb, err := strconv.Atoi(heap)
	if err != nil || mb < tsjob.MinMemoryBytes/(2<<20) || mb > tsjob.MaxHeapMiB || strconv.Itoa(mb) != heap {
		return tsjob.ErrBundle
	}
	data, err := readBundleFile()
	if err != nil {
		return err
	}
	files, err := tsjob.ReadBundle(data, entry)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(".", "_sandbox_js_")
	if err != nil {
		return err
	}
	if err := unpack(dir, files); err != nil {
		return err
	}
	return syscall.Exec(tsjob.NodePath, []string{
		tsjob.NodePath, "--max-old-space-size=" + heap, "--", "./" + path.Join(dir, entry),
	}, []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LC_ALL=C.UTF-8"})
}

func readBundleFile() ([]byte, error) {
	f, err := os.Open(tsjob.BundleName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, tsjob.MaxBundleBytes+1))
}

func unpack(dir string, files []tsjob.File) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, file := range files {
		if err := root.MkdirAll(path.Dir(file.Path), 0o700); err != nil {
			return err
		}
		if err := root.WriteFile(file.Path, file.Content, 0o600); err != nil {
			return err
		}
	}
	return nil
}
