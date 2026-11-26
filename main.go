package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	apiEndpoint = "https://api.openai.com/v1/content_provenance_checks"
	maxFileSize = 50 << 20
)

type checkResponse struct {
	Object    string        `json:"object,omitempty"`
	CreatedAt int64         `json:"created_at,omitempty"`
	Results   []checkResult `json:"results"`
}

type checkResult struct {
	Type            string  `json:"type"`
	Outcome         string  `json:"outcome"`
	ValidationState *string `json:"validation_state,omitempty"`
	Issuer          *string `json:"issuer,omitempty"`
	Model           *string `json:"model,omitempty"`
	GeneratedAt     *string `json:"generated_at,omitempty"`
}

type apiErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func main() {
	jsonOutput := flag.Bool("json", false, "print the complete API response as JSON")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags] <image|audio|pdf>\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(flag.CommandLine.Output(), "Check a file for supported OpenAI provenance signals.")
		fmt.Fprintln(flag.CommandLine.Output(), "\nFlags:")
		flag.PrintDefaults()
		fmt.Fprintln(flag.CommandLine.Output(), "\nSet OPENAI_API_KEY to your OpenAI API key.")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: OPENAI_API_KEY is not set")
		os.Exit(1)
	}

	result, err := checkFile(context.Background(), flag.Arg(0), apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "error: writing JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}

	printSummary(os.Stdout, result)
}

func checkFile(ctx context.Context, path, apiKey string) (checkResponse, error) {
	file, err := os.Open(path)
	if err != nil {
		return checkResponse{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return checkResponse{}, fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return checkResponse{}, errors.New("input must be a regular file")
	}
	if info.Size() == 0 {
		return checkResponse{}, errors.New("input file is empty")
	}
	if info.Size() > maxFileSize {
		return checkResponse{}, fmt.Errorf("file is larger than the 50 MiB upload limit")
	}

	contentType, err := contentTypeFor(path)
	if err != nil {
		return checkResponse{}, err
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name":     "file",
			"filename": uploadFilename(path),
		}))
		header.Set("Content-Type", contentType)
		part, createErr := writer.CreatePart(header)
		if createErr == nil {
			_, createErr = io.CopyN(part, file, info.Size())
		}
		if createErr == nil {
			createErr = writer.Close()
		}
		_ = pw.CloseWithError(createErr)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiEndpoint, pr)
	if err != nil {
		_ = pr.Close()
		return checkResponse{}, fmt.Errorf("create API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return checkResponse{}, fmt.Errorf("send API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var apiErr apiErrorResponse
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			if apiErr.Error.Code != "" {
				return checkResponse{}, fmt.Errorf("OpenAI API returned %s (%s): %s", resp.Status, apiErr.Error.Code, apiErr.Error.Message)
			}
			return checkResponse{}, fmt.Errorf("OpenAI API returned %s: %s", resp.Status, apiErr.Error.Message)
		}
		return checkResponse{}, fmt.Errorf("OpenAI API returned %s", resp.Status)
	}

	var result checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return checkResponse{}, fmt.Errorf("decode API response: %w", err)
	}
	return result, nil
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

func printSummary(w io.Writer, result checkResponse) {
	detected := false
	for _, item := range result.Results {
		if item.Outcome == "detected" {
			detected = true
		}
	}
	if detected {
		fmt.Fprintln(w, "Supported OpenAI provenance signal detected.")
	} else {
		fmt.Fprintln(w, "No supported OpenAI provenance signal detected.")
	}
	fmt.Fprintln(w, "This result is not a general-purpose AI detection verdict.")
	for _, item := range result.Results {
		fmt.Fprintf(w, "\n%s: %s", item.Type, item.Outcome)
		if item.ValidationState != nil {
			fmt.Fprintf(w, " (validation: %s)", *item.ValidationState)
		}
		if item.Issuer != nil {
			fmt.Fprintf(w, "\n  issuer: %s", *item.Issuer)
		}
		if item.Model != nil {
			fmt.Fprintf(w, "\n  model: %s", *item.Model)
		}
		if item.GeneratedAt != nil {
			fmt.Fprintf(w, "\n  generated at: %s", *item.GeneratedAt)
		}
		fmt.Fprintln(w)
	}
}
