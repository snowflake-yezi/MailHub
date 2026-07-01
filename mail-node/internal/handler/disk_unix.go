//go:build linux || darwin || freebsd

package handler

import "syscall"

// diskInfo 文件系统使用量(字节)。FreeBytes 为普通用户可用额度(扣除 root reserve)。
type diskInfo struct {
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

// diskUsage 返回 path 所在文件系统的总容量 / 已用 / 可用。
// 用于 /internal/stats 的磁盘使用量上报;失败时返回零值,不阻塞 stats 响应。
func diskUsage(path string) diskInfo {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return diskInfo{}
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	avail := stat.Bavail * uint64(stat.Bsize) // non-root 可用
	return diskInfo{
		TotalBytes: total,
		UsedBytes:  total - free,
		FreeBytes:  avail,
	}
}
