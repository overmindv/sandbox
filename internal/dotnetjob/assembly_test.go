package dotnetjob

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"testing"
)

func TestAssemblyHeaders(t *testing.T) {
	t.Parallel()
	data := managedFixture(t)
	if !IsAssembly(data) {
		t.Fatal("expected a managed assembly header")
	}
	for name, change := range map[string]func([]byte) []byte{
		"truncated":       func(data []byte) []byte { return data[:555] },
		"oversized":       func(_ []byte) []byte { return make([]byte, MaxBytes+1) },
		"wrong signature": func(data []byte) []byte { data[0x80] = 0; return data },
		"native flags":    func(data []byte) []byte { data[512+16] = 0x10; return data },
		"no entrypoint":   func(data []byte) []byte { data[512+20] = 0; return data },
		"invalid metadata": func(data []byte) []byte {
			data[640] = 0
			return data
		},
	} {
		t.Run(name, func(t *testing.T) {
			if IsAssembly(change(bytes.Clone(data))) {
				t.Error("accepted unexpected assembly format")
			}
		})
	}
}

func managedFixture(t *testing.T) []byte {
	t.Helper()
	data := make([]byte, 1024)
	copy(data, "MZ")
	binary.LittleEndian.PutUint32(data[0x3c:], 0x80)
	copy(data[0x80:], "PE\x00\x00")
	header := pe.FileHeader{Machine: pe.IMAGE_FILE_MACHINE_I386, NumberOfSections: 1, SizeOfOptionalHeader: 224}
	optional := pe.OptionalHeader32{Magic: 0x10b, NumberOfRvaAndSizes: 16}
	optional.DataDirectory[14] = pe.DataDirectory{VirtualAddress: 0x2000, Size: 72}
	section := pe.SectionHeader32{VirtualAddress: 0x2000, PointerToRawData: 512, SizeOfRawData: 512}
	managed := clrHeader{Size: 72, Metadata: pe.DataDirectory{VirtualAddress: 0x2080, Size: 16}, Flags: 1, EntryPoint: 0x06000001}
	for _, part := range []struct {
		offset int
		value  any
	}{{0x84, header}, {0x98, optional}, {0x98 + 224, section}, {512, managed}} {
		if _, err := binary.Encode(data[part.offset:], binary.LittleEndian, part.value); err != nil {
			t.Fatal(err)
		}
	}
	copy(data[640:], "BSJB")
	return data
}
