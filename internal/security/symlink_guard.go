package security

import "os"

func RejectSymlinkCopyOut(copyOutPath string, mode os.FileMode, policy Policy) error {
	policy = policy.withDefaults()
	if policy.AllowSymlinks {
		return nil
	}
	if mode&os.ModeSymlink != 0 {
		return violation(CodeCopyOutSymlink, copyOutPath, "copy-out path resolved to a symlink")
	}
	return nil
}
