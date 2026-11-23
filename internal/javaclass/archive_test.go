package javaclass

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"pkg/Main.class":   {Data: append(bytes.Clone(java21), 11)},
		"pkg/Main$1.class": {Data: append(bytes.Clone(java21), 22)},
	}
	var archive bytes.Buffer
	if err := WriteArchive(&archive, files); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchive(archive.Bytes(), "pkg.Main"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchive(archive.Bytes(), "Missing"); !errors.Is(err, ErrInvalid) {
		t.Error("missing main class was accepted", err)
	}
	data := archive.Bytes()
	if at := bytes.Index(data, java21); at >= 0 {
		data[at+len(java21)]++
	}
	if err := ValidateArchive(data, "pkg.Main"); !errors.Is(err, ErrInvalid) {
		t.Error("corrupt class data was accepted", err)
	}
}

func TestArchiveRejectsUnexpectedEntries(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../Helper.class", "pkg/../Helper.class", "META-INF/MANIFEST.MF", "Main.class", "pkg/data.txt"} {
		t.Run(name, func(t *testing.T) {
			var archive bytes.Buffer
			w := zip.NewWriter(&archive)
			for _, file := range []string{"Main.class", name} {
				size := uint64(len(java21))
				entry, err := w.CreateRaw(&zip.FileHeader{
					Name: file, Method: zip.Store, CRC32: crc32.ChecksumIEEE(java21),
					CompressedSize64: size, UncompressedSize64: size,
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write(java21); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ValidateArchive(archive.Bytes(), "Main"); !errors.Is(err, ErrInvalid) {
				t.Error("unexpected entry was accepted", err)
			}
		})
	}
}

func TestArchiveSizeCountAndSymlinkLimits(t *testing.T) {
	t.Parallel()
	bigClass := make([]byte, MaxBytes)
	copy(bigClass, java21)
	if err := WriteArchive(io.Discard, fstest.MapFS{"Main.class": {Data: bigClass}}); !errors.Is(err, ErrLimit) {
		t.Error("archive headers were not included in the byte limit", err)
	}
	files := make(fstest.MapFS, MaxClasses+1)
	for i := range MaxClasses + 1 {
		files[fmt.Sprintf("Class%d.class", i)] = &fstest.MapFile{Data: java21}
	}
	if err := WriteArchive(io.Discard, files); !errors.Is(err, ErrLimit) {
		t.Error("class count limit was ignored", err)
	}
	if err := WriteArchive(io.Discard, fstest.MapFS{"Main.class": {Mode: fs.ModeSymlink}}); !errors.Is(err, ErrInvalid) {
		t.Error("symlink was accepted", err)
	}
	if err := ValidateArchive(make([]byte, MaxBytes+1), "Main"); !errors.Is(err, ErrInvalid) {
		t.Error("oversized archive was accepted", err)
	}
}
