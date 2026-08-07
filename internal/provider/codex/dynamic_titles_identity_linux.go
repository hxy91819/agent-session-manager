//go:build linux

package codex

import (
	"fmt"
	"os"
	"syscall"
)

func dynamicTitleFileIdentity(info os.FileInfo) (string, string, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", false
	}
	fileID := fmt.Sprintf("%x:%x", stat.Dev, stat.Ino)
	changeID := fmt.Sprintf("%x:%x", stat.Ctim.Sec, stat.Ctim.Nsec)
	return fileID, changeID, true
}
