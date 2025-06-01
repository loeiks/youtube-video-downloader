//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Get disk usage statistics for Windows
func getTmpfsUsage() (totalMB, usedMB, availableMB float64, usagePercent float64) {
	total, available := getDiskSpaceWindows(config.TempDir)
	if total == 0 {
		return 0, 0, 0, 0
	}

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
	_, available := getDiskSpaceWindows(config.TempDir)

	if available < requiredBytes {
		return fmt.Errorf("insufficient disk space: need %.1fGB, have %.1fGB",
			float64(requiredBytes)/(1024*1024*1024),
			float64(available)/(1024*1024*1024))
	}
	return nil
}

func getDiskSpaceWindows(path string) (total, available int64) {
	kernel32, err := syscall.LoadLibrary("kernel32.dll")
	if err != nil {
		return 0, 0
	}
	defer syscall.FreeLibrary(kernel32)

	getDiskFreeSpaceEx, err := syscall.GetProcAddress(kernel32, "GetDiskFreeSpaceExW")
	if err != nil {
		return 0, 0
	}

	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64

	ret, _, _ := syscall.Syscall6(getDiskFreeSpaceEx, 4,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)), 0, 0)

	if ret == 0 {
		return 0, 0
	}

	return totalNumberOfBytes, freeBytesAvailable
}
