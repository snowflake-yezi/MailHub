//go:build linux || darwin || freebsd

package agent

import "syscall"

func DiskUsage(path string) (totalBytes, availableBytes uint64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize)
}
