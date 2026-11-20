package gotestjob

import "strconv"

const (
	BundleName     = "_sandbox_go_tests.tar"
	IndexName      = "packages.json"
	MaxPackages    = 64
	MaxIndexBytes  = 32 << 10
	MaxBinaryBytes = 8 << 20
	MaxBundleBytes = 24 << 20
)

type Package struct {
	Dir        string `json:"dir"`
	ImportPath string `json:"import_path"`
}

func BinaryName(index int) string {
	return "package-" + strconv.Itoa(index)
}
