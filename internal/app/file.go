package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxFileSize = 50 << 20

func openSourceFile(path string) (*os.File, int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, "", fmt.Errorf("open file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, "", fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, "", errors.New("input must be a regular file")
	}
	if info.Size() == 0 {
		_ = file.Close()
		return nil, 0, "", errors.New("input file is empty")
	}
	if info.Size() > maxFileSize {
		_ = file.Close()
		return nil, 0, "", errors.New("file is larger than the 50 MiB upload limit")
	}

	contentType, err := contentTypeFor(path)
	if err != nil {
		_ = file.Close()
		return nil, 0, "", err
	}
	return file, info.Size(), contentType, nil
}

func uploadFilename(path string) string {
	// Use a plain ASCII filename so multipart encoders don't produce a
	// filename* parameter for Unicode paths, which some API parsers reject.
	return "upload" + strings.ToLower(filepath.Ext(path))
}

func contentTypeFor(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", nil
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".webp":
		return "image/webp", nil
	case ".gif":
		return "image/gif", nil
	case ".mp3":
		return "audio/mpeg", nil
	case ".opus":
		return "audio/ogg", nil
	case ".aac":
		return "audio/aac", nil
	case ".flac":
		return "audio/flac", nil
	case ".wav":
		return "audio/wav", nil
	case ".pcm":
		return "audio/pcm", nil
	case ".pdf":
		return "application/pdf", nil
	default:
		return "", fmt.Errorf("unsupported file type %q; supported formats are PNG, JPEG, WebP, GIF, MP3, Opus, AAC, FLAC, WAV, PCM, and PDF", filepath.Ext(path))
	}
}
