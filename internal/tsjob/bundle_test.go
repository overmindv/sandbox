package tsjob

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestBundleRoundTrip(t *testing.T) {
	t.Parallel()
	var bundle bytes.Buffer
	if err := WriteBundle(&bundle, fstest.MapFS{
		"src/main.js": {Data: []byte("console.log('ok')")},
		"src/math.js": {},
	}); err != nil {
		t.Fatal(err)
	}
	files, err := ReadBundle(bundle.Bytes(), "src/main.js")
	if err != nil || len(files) != 2 || files[0].Path != "src/main.js" || string(files[0].Content) != "console.log('ok')" {
		t.Fatal("invalid round trip", files, err)
	}
	if _, err := ReadBundle(bundle.Bytes(), "missing.js"); !errors.Is(err, ErrBundle) {
		t.Error("accepted missing entrypoint", err)
	}
	if _, err := ReadBundle(append(bundle.Bytes(), 0), "src/main.js"); !errors.Is(err, ErrBundle) {
		t.Error("accepted trailing data", err)
	}
}

func TestBundleRejectsUnexpectedEntries(t *testing.T) {
	t.Parallel()
	for _, header := range []tar.Header{
		{Name: "main.js", Typeflag: tar.TypeReg},
		{Name: "src/../extra.js", Typeflag: tar.TypeReg},
		{Name: "helper.ts", Typeflag: tar.TypeReg},
		{Name: "package.json", Typeflag: tar.TypeReg},
		{Name: "link.js", Typeflag: tar.TypeSymlink, Linkname: "main.js"},
	} {
		t.Run(header.Name, func(t *testing.T) {
			var bundle bytes.Buffer
			w := tar.NewWriter(&bundle)
			if err := w.WriteHeader(&tar.Header{Name: "main.js", Mode: 0o600, Format: tar.FormatUSTAR}); err != nil {
				t.Fatal(err)
			}
			header.Mode, header.Format = 0o600, tar.FormatUSTAR
			if err := w.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadBundle(bundle.Bytes(), "main.js"); !errors.Is(err, ErrBundle) {
				t.Error("accepted unexpected entry", err)
			}
		})
	}
}

func TestBundleBoundsAndFileType(t *testing.T) {
	t.Parallel()
	if err := WriteBundle(io.Discard, fstest.MapFS{"main.js": {Data: make([]byte, MaxBundleBytes)}}); !errors.Is(err, ErrLimit) {
		t.Error("bundle byte cap did not include archive overhead", err)
	}
	if err := WriteBundle(io.Discard, fstest.MapFS{"main.js": {Mode: fs.ModeSymlink}}); !errors.Is(err, ErrBundle) {
		t.Error("bundle accepted a symlink", err)
	}
	files := make(fstest.MapFS, MaxFiles+1)
	for i := range MaxFiles + 1 {
		files[fmt.Sprintf("file%d.js", i)] = &fstest.MapFile{}
	}
	if err := WriteBundle(io.Discard, files); !errors.Is(err, ErrLimit) {
		t.Error("bundle accepted too many files", err)
	}
}
