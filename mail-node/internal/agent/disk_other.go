//go:build !linux && !darwin && !freebsd

package agent

func DiskUsage(string) (totalBytes, availableBytes uint64) { return 0, 0 }
