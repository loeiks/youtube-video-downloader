# YouTube Video Downloader

A simple HTTP server that downloads YouTube videos in the highest available quality. The server downloads both video and audio streams separately and merges them using FFmpeg for the best quality output.

## Features

- Single endpoint for video downloads
- Downloads highest quality video and audio streams
- Automatically merges streams using FFmpeg
- Docker support with health monitoring
- Automatic cleanup of temporary files
- Simple to use with any web browser

## Prerequisites

- Go 1.x
- FFmpeg installed and available in PATH
- Docker and Docker Compose (optional, for containerized deployment)

## Installation

1. Clone the repository:
```bash
git clone <repository-url>
cd youtube-video-downloader
```

2. Install dependencies:
```bash
go mod download
```

## Usage

### Running Locally

1. Start the server:
```bash
go run main.go
```

2. The server will start on port 7839 by default

3. To download a video, open your web browser and visit:
```
http://localhost:7839/download?url=<youtube-url>
```
Replace `<youtube-url>` with the actual YouTube video URL.

### Running with Docker

1. Build and start the container:
```bash
docker-compose up -d
```

2. The server will be available at `http://localhost:7839`

3. Use the same URL format as above to download videos

## API Endpoints

### GET /download

Downloads a YouTube video in the highest available quality.

**Parameters:**
- `url` (required) - The YouTube video URL

**Response:**
- Content-Type: video/mp4
- The video file as a download

### GET /health

Health check endpoint for Docker container monitoring.

**Response:**
- 200 OK if the server is running

## Error Handling

The server handles various error cases:
- Invalid or missing YouTube URL
- Video not available
- FFmpeg not installed
- Network issues during download
- Invalid video formats

## Technical Details

### Video Processing

1. The server accepts a YouTube URL via the `/download` endpoint
2. Downloads the highest quality video stream available
3. Downloads the highest quality audio stream available
4. Uses FFmpeg to merge the streams into a single MP4 file
5. Streams the merged file to the client
6. Automatically cleans up temporary files

### Temporary Files

- All temporary files are stored in the system's temp directory
- Files are automatically cleaned up after download
- Old temporary files (>1 hour) are removed to prevent disk space issues

### Docker Support

- Includes Dockerfile and docker-compose.yml
- Built-in health monitoring
- Automatic container restart on failure
- Exposed on port 7839

## Architecture

- Uses `github.com/kkdai/youtube/v2` for YouTube video processing
- Downloads video and audio streams separately for best quality
- Uses FFmpeg for merging streams
- Implements automatic cleanup of temporary files
- Provides Docker health monitoring