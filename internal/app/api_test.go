package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFileReturnsTooManyRequestsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached","code":"rate_limit_exceeded"}}`))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("image data"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := checkFileAt(context.Background(), path, "api-key", server.URL)
	if err == nil {
		t.Fatal("checkFileAt() returned no error for HTTP 429")
	}
	for _, want := range []string{"429 Too Many Requests", "rate_limit_exceeded", "Rate limit reached"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkFileAt() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestCheckUploadedFileDataSendsInMemoryContent(t *testing.T) {
	wantData := []byte("image data")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer api-key" {
			t.Errorf("Authorization = %q, want Bearer api-key", got)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("NextPart() error = %v", err)
		}
		if got := part.FormName(); got != "file" {
			t.Errorf("multipart field name = %q, want file", got)
		}
		if got := part.FileName(); got != "upload.png" {
			t.Errorf("multipart filename = %q, want upload.png", got)
		}
		if got := part.Header.Get("Content-Type"); got != "image/png" {
			t.Errorf("part Content-Type = %q, want image/png", got)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll(part) error = %v", err)
		}
		if !bytes.Equal(data, wantData) {
			t.Errorf("uploaded content = %q, want %q", data, wantData)
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	if _, err := checkUploadedFileDataAt(context.Background(), "sample.png", wantData, "api-key", server.URL); err != nil {
		t.Fatalf("checkUploadedFileDataAt() error = %v", err)
	}
}
