package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kkdai/youtube/v2"
)

// Configuration struct
type Config struct {
	MaxVideoHeight  int           `json:"max_video_height"`
	BufferSize      int           `json:"buffer_size"`
	DownloadTimeout time.Duration `json:"download_timeout"`
	MaxConcurrent   int           `json:"max_concurrent"`
	CleanupInterval time.Duration `json:"cleanup_interval"`
	TempDir         string        `json:"temp_dir"`
	FFmpegPreset    string        `json:"ffmpeg_preset"`
	MaxFileAge      time.Duration `json:"max_file_age"`
	ServerPort      string        `json:"server_port"`
	MinDiskSpaceGB  int64         `json:"min_disk_space_gb"`
}

// Metrics tracking
type Metrics struct {
	TotalDownloads      int64     `json:"total_downloads"`
	SuccessfulDownloads int64     `json:"successful_downloads"`
	FailedDownloads     int64     `json:"failed_downloads"`
	TotalBytesServed    int64     `json:"total_bytes_served"`
	AverageFileSize     float64   `json:"average_file_size"`
	UptimeStart         time.Time `json:"uptime_start"`
	mutex               sync.RWMutex
}

func (m *Metrics) RecordDownload(success bool, bytes int64) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.TotalDownloads++
	if success {
		m.SuccessfulDownloads++
		m.TotalBytesServed += bytes
		if m.SuccessfulDownloads > 0 {
			m.AverageFileSize = float64(m.TotalBytesServed) / float64(m.SuccessfulDownloads)
		}
	} else {
		m.FailedDownloads++
	}
}

func (m *Metrics) GetStats() map[string]interface{} {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	successRate := float64(0)
	if m.TotalDownloads > 0 {
		successRate = float64(m.SuccessfulDownloads) / float64(m.TotalDownloads) * 100
	}

	return map[string]interface{}{
		"total_downloads":      m.TotalDownloads,
		"successful_downloads": m.SuccessfulDownloads,
		"failed_downloads":     m.FailedDownloads,
		"success_rate_percent": successRate,
		"total_bytes_served":   m.TotalBytesServed,
		"average_file_size_mb": m.AverageFileSize / (1024 * 1024),
		"uptime_hours":         time.Since(m.UptimeStart).Hours(),
	}
}

// Global variables
var (
	config = Config{
		MaxVideoHeight:  1080,
		BufferSize:      128 * 1024,
		DownloadTimeout: 15 * time.Minute,
		MaxConcurrent:   3,
		CleanupInterval: 15 * time.Minute,
		TempDir:         filepath.Join(os.TempDir(), "youtube-downloads"),
		FFmpegPreset:    "veryfast",
		MaxFileAge:      30 * time.Minute,
		ServerPort:      "7839",
		MinDiskSpaceGB:  2,
	}

	metrics           = &Metrics{UptimeStart: time.Now()}
	downloadSemaphore chan struct{}
	httpClient        = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 10,
		},
	}
)

func loadConfigFromEnv() {
	if port := os.Getenv("SERVER_PORT"); port != "" {
		config.ServerPort = port
	}

	if maxHeight := os.Getenv("MAX_VIDEO_HEIGHT"); maxHeight != "" {
		if height, err := strconv.Atoi(maxHeight); err == nil {
			config.MaxVideoHeight = height
		}
	}

	if maxConc := os.Getenv("MAX_CONCURRENT"); maxConc != "" {
		if conc, err := strconv.Atoi(maxConc); err == nil {
			config.MaxConcurrent = conc
		}
	}

	if preset := os.Getenv("FFMPEG_PRESET"); preset != "" {
		config.FFmpegPreset = preset
	}

	if tempDir := os.Getenv("TEMP_DIR"); tempDir != "" {
		config.TempDir = tempDir
	}

	if timeout := os.Getenv("DOWNLOAD_TIMEOUT"); timeout != "" {
		if dur, err := time.ParseDuration(timeout); err == nil {
			config.DownloadTimeout = dur
		}
	}

	if interval := os.Getenv("CLEANUP_INTERVAL"); interval != "" {
		if dur, err := time.ParseDuration(interval); err == nil {
			config.CleanupInterval = dur
		}
	}

	if diskSpace := os.Getenv("MIN_DISK_SPACE_GB"); diskSpace != "" {
		if space, err := strconv.ParseInt(diskSpace, 10, 64); err == nil {
			config.MinDiskSpaceGB = space
		}
	}
}

// Get tmpfs usage statistics
func getTmpfsUsage() (totalMB, usedMB, availableMB float64, usagePercent float64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(config.TempDir, &stat); err != nil {
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

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)

	loadConfigFromEnv()
	downloadSemaphore = make(chan struct{}, config.MaxConcurrent)

	if err := os.MkdirAll(config.TempDir, 0755); err != nil {
		log.Fatalf("[ERROR] Failed to create temp directory: %v", err)
	}

	if err := checkDiskSpace(config.MinDiskSpaceGB * 1024 * 1024 * 1024); err != nil {
		log.Fatalf("[ERROR] %v", err)
	}

	// Log initial tmpfs usage
	totalMB, usedMB, availableMB, usagePercent := getTmpfsUsage()
	log.Printf("[INFO] tmpfs Status: %.1fMB total, %.1fMB used (%.1f%%), %.1fMB available", 
		totalMB, usedMB, usagePercent, availableMB)

	go startCleanupRoutine()

	http.HandleFunc("/download", downloadHandler)
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/metrics", metricsHandler)
	http.HandleFunc("/config", configHandler)
	http.HandleFunc("/tmpfs", tmpfsHandler)

	log.Printf("[INFO] YouTube Downloader Server starting on port %s", config.ServerPort)
	log.Printf("[INFO] Config: Max Quality: %dp, Concurrent: %d, Preset: %s",
		config.MaxVideoHeight, config.MaxConcurrent, config.FFmpegPreset)

	if err := http.ListenAndServe(":"+config.ServerPort, nil); err != nil {
		log.Fatalf("[ERROR] Server failed to start: %v", err)
	}
}

func startCleanupRoutine() {
	ticker := time.NewTicker(config.CleanupInterval)
	defer ticker.Stop()

	cleanupTempFiles() // Initial cleanup

	for range ticker.C {
		cleanupTempFiles()
	}
}

func checkDiskSpace(requiredBytes int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(config.TempDir, &stat); err != nil {
		return fmt.Errorf("failed to check disk space: %w", err)
	}

	available := int64(stat.Bavail * uint64(stat.Bsize))
	if available < requiredBytes {
		return fmt.Errorf("insufficient disk space: need %.1fGB, have %.1fGB",
			float64(requiredBytes)/(1024*1024*1024), float64(available)/(1024*1024*1024))
	}
	return nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := checkDiskSpace(config.MinDiskSpaceGB * 1024 * 1024 * 1024); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "UNHEALTHY: %v", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics.GetStats())
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}

// tmpfs monitoring endpoint
func tmpfsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	totalMB, usedMB, availableMB, usagePercent := getTmpfsUsage()

	stats := map[string]interface{}{
		"tmpfs_total_mb":     totalMB,
		"tmpfs_used_mb":      usedMB,
		"tmpfs_available_mb": availableMB,
		"tmpfs_usage_percent": usagePercent,
		"is_tmpfs_available": totalMB > 0,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func cleanupTempFiles() {
	log.Printf("[INFO] Starting cleanup of files older than %v", config.MaxFileAge)

	// Log tmpfs usage before cleanup
	totalMB, usedMB, availableMB, usagePercent := getTmpfsUsage()
	log.Printf("[INFO] tmpfs Before cleanup: %.1fMB used (%.1f%%), %.1fMB available",
		usedMB, usagePercent, availableMB)

	var cleanedCount int
	var totalSize int64

	err := filepath.Walk(config.TempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		if time.Since(info.ModTime()) > config.MaxFileAge {
			fileName := filepath.Base(path)
			sizeMB := float64(info.Size()) / (1024 * 1024)

			if err := os.Remove(path); err == nil {
				totalSize += info.Size()
				cleanedCount++
				log.Printf("[INFO] Cleaned: %s (%.2f MB)", fileName, sizeMB)
			} else {
				log.Printf("[WARN] Failed to remove %s: %v", fileName, err)
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("[WARN] Cleanup error: %v", err)
		return
	}

	// Log cleanup results and tmpfs usage after cleanup
	totalMB, usedMB, availableMB, usagePercent = getTmpfsUsage()
	if cleanedCount == 0 {
		log.Printf("[INFO] Cleanup completed: No files to remove")
	} else {
		totalCleanedMB := float64(totalSize) / (1024 * 1024)
		log.Printf("[INFO] Cleanup completed: %d files removed (%.2f MB freed)", cleanedCount, totalCleanedMB)
	}
	log.Printf("[INFO] tmpfs After cleanup: %.1fMB used (%.1f%%), %.1fMB available",
		usedMB, usagePercent, availableMB)
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	url := r.URL.Query().Get("url")
	if url == "" {
		http.Error(w, "Missing required parameter: url", http.StatusBadRequest)
		metrics.RecordDownload(false, 0)
		return
	}

	// Rate limiting
	select {
	case downloadSemaphore <- struct{}{}:
		defer func() { <-downloadSemaphore }()
	case <-time.After(30 * time.Second):
		http.Error(w, "Server too busy, try again later", http.StatusServiceUnavailable)
		metrics.RecordDownload(false, 0)
		return
	}

	// Log tmpfs usage before download
	totalMB, usedMB, availableMB, usagePercent := getTmpfsUsage()
	log.Printf("[INFO] tmpfs Before download: %.1fMB used (%.1f%%), %.1fMB available",
		usedMB, usagePercent, availableMB)

	log.Printf("[INFO] Processing download: %s", url)

	if err := checkDiskSpace(config.MinDiskSpaceGB * 1024 * 1024 * 1024); err != nil {
		http.Error(w, fmt.Sprintf("Server storage full: %v", err), http.StatusInsufficientStorage)
		metrics.RecordDownload(false, 0)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), config.DownloadTimeout)
	defer cancel()

	outputFile, filename, tempFiles, err := processDownload(ctx, url)
	if err != nil {
		log.Printf("[ERROR] Download failed: %v", err)
		cleanupFiles(tempFiles...)
		if outputFile != "" {
			os.Remove(outputFile)
		}
		http.Error(w, fmt.Sprintf("Download failed: %v", err), http.StatusInternalServerError)
		metrics.RecordDownload(false, 0)
		return
	}

	var fileSize int64
	if stat, err := os.Stat(outputFile); err == nil {
		fileSize = stat.Size()
	}

	defer func() {
		cleanupFiles(append(tempFiles, outputFile)...)
		
		// Log tmpfs usage after cleanup
		_, usedMB, availableMB, usagePercent := getTmpfsUsage()
		log.Printf("[INFO] tmpfs After cleanup: %.1fMB used (%.1f%%), %.1fMB available",
			usedMB, usagePercent, availableMB)
		log.Printf("[INFO] Cleaned up temp files for: %s", filename)
	}()

	if err := streamFileToClient(w, outputFile, filename); err != nil {
		log.Printf("[ERROR] Streaming failed: %v", err)
		metrics.RecordDownload(false, 0)
	} else {
		metrics.RecordDownload(true, fileSize)
		log.Printf("[INFO] Successfully served: %s (%.2f MB)", filename, float64(fileSize)/(1024*1024))
	}
}

func processDownload(ctx context.Context, url string) (outputFile, filename string, tempFiles []string, err error) {
	client := youtube.Client{HTTPClient: httpClient}

	video, err := client.GetVideoContext(ctx, url)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to get video info: %w", err)
	}

	log.Printf("[INFO] Processing: %s", video.Title)

	bestVideo := findBestVideoFormat(video.Formats)
	bestAudio := findBestAudioFormat(video.Formats)

	if bestVideo == nil || bestAudio == nil {
		return "", "", nil, fmt.Errorf("could not find required video/audio formats")
	}

	// Check disk space for estimated download
	estimatedSize := int64(bestVideo.ContentLength + bestAudio.ContentLength)
	if estimatedSize > 0 {
		if err := checkDiskSpace(estimatedSize * 3); err != nil {
			return "", "", nil, fmt.Errorf("insufficient space: %w", err)
		}
		log.Printf("[INFO] Estimated download size: %.2f MB", float64(estimatedSize)/(1024*1024))
	}

	tempID := fmt.Sprintf("%d", time.Now().UnixNano())
	videoFile := filepath.Join(config.TempDir, fmt.Sprintf("%s_video.tmp", tempID))
	audioFile := filepath.Join(config.TempDir, fmt.Sprintf("%s_audio.tmp", tempID))
	outputFile = filepath.Join(config.TempDir, fmt.Sprintf("%s_final.mp4", tempID))

	tempFiles = []string{videoFile, audioFile}

	var wg sync.WaitGroup
	var videoErr, audioErr error

	wg.Add(2)

	go func() {
		defer wg.Done()
		videoErr = downloadStream(ctx, &client, video, bestVideo, videoFile)
	}()

	go func() {
		defer wg.Done()
		audioErr = downloadStream(ctx, &client, video, bestAudio, audioFile)
	}()

	wg.Wait()

	if videoErr != nil {
		return "", "", tempFiles, fmt.Errorf("video download failed: %w", videoErr)
	}
	if audioErr != nil {
		return "", "", tempFiles, fmt.Errorf("audio download failed: %w", audioErr)
	}

	// Log tmpfs usage during processing (after downloads, before merge)
	_, usedMB, availableMB, usagePercent := getTmpfsUsage()
	log.Printf("[INFO] tmpfs During processing: %.1fMB used (%.1f%%), %.1fMB available",
		usedMB, usagePercent, availableMB)

	if err = mergeStreams(ctx, videoFile, audioFile, outputFile); err != nil {
		return "", "", tempFiles, fmt.Errorf("merge failed: %w", err)
	}

	filename = sanitizeFilename(video.Title) + ".mp4"
	return outputFile, filename, tempFiles, nil
}

func streamFileToClient(w http.ResponseWriter, filepath, filename string) error {
	stat, err := os.Stat(filepath)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	file, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))

	_, err = io.CopyBuffer(w, file, make([]byte, config.BufferSize))
	return err
}

func cleanupFiles(files ...string) {
	for _, file := range files {
		if file != "" {
			os.Remove(file)
		}
	}
}

func findBestVideoFormat(formats youtube.FormatList) *youtube.Format {
	var bestFormat *youtube.Format
	var bestQuality int

	for _, format := range formats {
		if format.Height > 0 && strings.Contains(format.MimeType, "video") &&
			format.Height <= config.MaxVideoHeight && format.Height > bestQuality {
			bestQuality = format.Height
			bestFormat = &format
		}
	}
	return bestFormat
}

func findBestAudioFormat(formats youtube.FormatList) *youtube.Format {
	var bestFormat *youtube.Format
	var bestBitrate int

	for _, format := range formats {
		if strings.Contains(format.MimeType, "audio") && format.Bitrate > bestBitrate {
			bestBitrate = format.Bitrate
			bestFormat = &format
		}
	}
	return bestFormat
}

func downloadStream(ctx context.Context, client *youtube.Client, video *youtube.Video, format *youtube.Format, filename string) error {
	stream, _, err := client.GetStreamContext(ctx, video, format)
	if err != nil {
		return err
	}
	defer stream.Close()

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer := make([]byte, config.BufferSize)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, err := stream.Read(buffer)
			if n > 0 {
				if _, writeErr := file.Write(buffer[:n]); writeErr != nil {
					return writeErr
				}
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
}

func mergeStreams(ctx context.Context, videoFile, audioFile, outputFile string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH")
	}

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoFile,
		"-i", audioFile,
		"-c:v", "copy",
		"-c:a", "aac",
		"-preset", config.FFmpegPreset,
		"-movflags", "+faststart",
		"-y",
		outputFile,
	)

	return cmd.Run()
}

func sanitizeFilename(filename string) string {
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\n", "\r", "\t"}
	for _, char := range invalid {
		filename = strings.ReplaceAll(filename, char, "_")
	}

	for strings.Contains(filename, "__") {
		filename = strings.ReplaceAll(filename, "__", "_")
	}

	filename = strings.TrimSpace(strings.Trim(filename, "."))

	if len(filename) > 200 {
		filename = filename[:200]
	}

	if filename == "" {
		filename = "video"
	}

	return filename
}
