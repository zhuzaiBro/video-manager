package ffmpeg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type TranscodeResult struct {
	OutputDir    string
	M3U8Path     string
	SegmentCount int
}

func TranscodeHLS(ffmpegPath, inputPath, outputDir string) (*TranscodeResult, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	m3u8Path := filepath.Join(outputDir, "index.m3u8")
	cmd := exec.Command(ffmpegPath,
		"-i", inputPath,
		"-c", "copy",
		"-hls_segment_type", "fmp4",
		"-hls_time", "6",
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		m3u8Path,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg transcode failed: %w", err)
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, fmt.Errorf("read output dir: %w", err)
	}

	segmentCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".m4s" {
			segmentCount++
		}
	}

	return &TranscodeResult{
		OutputDir:    outputDir,
		M3U8Path:     m3u8Path,
		SegmentCount: segmentCount,
	}, nil
}
