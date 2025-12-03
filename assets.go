package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Failed to run the command: %v", err)
	}

	var cmdOutput output
	err = json.Unmarshal(out.Bytes(), &cmdOutput)
	if err != nil {
		return "", fmt.Errorf("error unmarshaling the cmd output: %v", err)
	}

	eps := 0.01
	ratio := float64(cmdOutput.Streams[0].Width) / float64(cmdOutput.Streams[0].Height)
	if 16.0/9.0-eps <= ratio && ratio <= 16.0/9.0+eps {
		return "16:9", nil
	} else if 9.0/16.0-eps <= ratio && ratio <= 9.0/16.0+eps {
		return "9:16", nil
	} else {
		return "other", nil
	}
}

func getAssetPath(mediaType string) string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		panic("failed to generate random bytes")
	}
	base64Path := base64.RawURLEncoding.EncodeToString(b)

	ext := mediaTypeToExt(mediaType)
	return fmt.Sprintf("%s%s", string(base64Path), ext)
}

func (cfg apiConfig) getAssetDiskPath(assetPath string) string {
	return filepath.Join(cfg.assetsRoot, assetPath)
}

func (cfg apiConfig) getAssetURL(assetPath string) string {
	return fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, assetPath)
}

func (cfg apiConfig) getVideoURL(key string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
}

func mediaTypeToExt(mediaType string) string {
	parts := strings.Split(mediaType, "/")
	if len(parts) != 2 {
		return ".bin"
	}
	return "." + parts[1]
}

type output struct {
	Streams []struct {
		Width              int    `json:"width,omitempty"`
		Height             int    `json:"height,omitempty"`
    }
}
