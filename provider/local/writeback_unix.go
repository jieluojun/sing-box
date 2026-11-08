//go:build unix

package provider

import (
	"os"
	"syscall"
)

func preserveLocalOwner(file *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return file.Chown(int(stat.Uid), int(stat.Gid))
}
