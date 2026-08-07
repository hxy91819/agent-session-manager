//go:build !linux

package codex

import "os"

func dynamicTitleFileIdentity(os.FileInfo) (string, string, bool) {
	return "", "", false
}
