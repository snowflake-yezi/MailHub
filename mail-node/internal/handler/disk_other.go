//go:build !linux && !darwin && !freebsd

package handler

// diskInfo 文件系统使用量(字节)。非 Unix 平台为 stub——mail-node 生产部署在 Linux,
// 此文件仅保证 Windows 等平台可编译/跑单元测试,disk 字段返回零值。
type diskInfo struct {
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

func diskUsage(path string) diskInfo {
	return diskInfo{}
}
