package ffprobe

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Result struct {
	Duration float64
	Width    int
	Height   int
	FPS      float64
}

type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType  string `json:"codec_type"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
	} `json:"streams"`
}

func Probe(ffprobePath, inputPath string) (*Result, error) {
	cmd := exec.Command(ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var data ffprobeOutput
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	result := &Result{}
	if data.Format.Duration != "" {
		result.Duration, _ = strconv.ParseFloat(data.Format.Duration, 64)
	}

	for _, stream := range data.Streams {
		if stream.CodecType != "video" {
			continue
		}
		result.Width = stream.Width
		result.Height = stream.Height
		result.FPS = parseFPS(stream.AvgFrameRate)
		if result.FPS == 0 {
			result.FPS = parseFPS(stream.RFrameRate)
		}
		break
	}

	return result, nil
}

func parseFPS(raw string) float64 {
	if raw == "" || raw == "0/0" {
		return 0
	}
	parts := strings.Split(raw, "/")
	if len(parts) == 2 {
		num, _ := strconv.ParseFloat(parts[0], 64)
		den, _ := strconv.ParseFloat(parts[1], 64)
		if den > 0 {
			return num / den
		}
	}
	f, _ := strconv.ParseFloat(raw, 64)
	return f
}
