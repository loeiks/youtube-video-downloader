//go:build unix || linux || darwin

package main

import (
	"fmt"
	"log"

	"golang.org/x/sys/unix"
)

// Get filesystem usage statistics for Unix systems
func getTmpfsUsage() (totalMB, usedMB, availableMB float64, usagePercent float64) {
	var stat unix.Statfs_t
	if err := unix.Statfs(config.TempDir, &stat); err != nil {
		log.Printf("[WARN] Failed to get filesystem stats: %v", err)
		return 0, 0, 0, 0
	}

	total := stat.Blocks * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	used := total - available

	totalMB = float64(total) / (1024 * 1024)
	usedMB = float64(used) / (1024 * 1024)
	availableMB = float64(available) / (1024 * 1024)

	if total > 0 {
		usagePercent = float64(used) / float64(total) * 100
	}

	return totalMB, usedMB, availableMB, usagePercent
}

func checkDiskSpace(requiredBytes int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(config.TempDir, &stat); err != nil {
		return fmt.Errorf("failed to check disk space: %w", err)
	}

	available := int64(stat.Bavail * uint64(stat.Bsize))
	if available < requiredBytes {
		return fmt.Errorf("insufficient disk space: need %.1fGB, have %.1fGB",
			float64(requiredBytes)/(1024*1024*1024),
			float64(available)/(1024*1024*1024))
	}
	return nil
}
