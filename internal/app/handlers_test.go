package app

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeHandlerEmbedsFrontend(t *testing.T) {
	handler := newServeHandler("api-key", func(context.Context, string, []byte, string) (checkResponse, error) {
		t.Fatal("check function called for frontend request")
		return checkResponse{}, nil
	}, testWebFiles)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "Drag files here") {
		t.Fatal("embedded frontend is missing the drag-and-drop file chooser")
	}
	if !strings.Contains(response.Body.String(), `src="/app.js"`) {
		t.Fatal("embedded frontend is missing the file summary script")
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "style-src 'self'") || !strings.Contains(got, "script-src 'self'") {
		t.Errorf("Content-Security-Policy = %q, want local styles and scripts only", got)
	}

	scriptResponse := httptest.NewRecorder()
	handler.ServeHTTP(scriptResponse, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if scriptResponse.Code != http.StatusOK || !strings.Contains(scriptResponse.Body.String(), "formatSize") {
		t.Errorf("GET /app.js status = %d, body = %q", scriptResponse.Code, scriptResponse.Body.String())
	}
}

func TestServeHandlerDoesNotRegisterLegacyCheckRoute(t *testing.T) {
	handler := newServeHandler("api-key", func(context.Context, string, []byte, string) (checkResponse, error) {
		t.Fatal("check function called for legacy /check route")
		return checkResponse{}, nil
	}, testWebFiles)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/check", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /check status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestServeAPIRequiresExactlyOneFile(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, name := range []string{"first.png", "second.png"} {
		part, err := form.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("image-data")); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/check", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	newServeHandler("api-key", func(context.Context, string, []byte, string) (checkResponse, error) {
		t.Fatal("check function called for a request containing multiple files")
		return checkResponse{}, nil
	}, testWebFiles).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/check status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode API error response: %v", err)
	}
	if !strings.Contains(payload.Error, "exactly one file") {
		t.Errorf("API error = %q, want a single-file validation message", payload.Error)
	}
}

func TestServeAPIChecksOneUploadedFile(t *testing.T) {
	var checkedFilename string
	var checkedData []byte
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("image-data")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/check", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	newServeHandler("api-key", func(_ context.Context, filename string, data []byte, apiKey string) (checkResponse, error) {
		checkedFilename = filename
		checkedData = append([]byte(nil), data...)
		if apiKey != "api-key" {
			t.Errorf("API key = %q, want api-key", apiKey)
		}
		return checkResponse{Results: []checkResult{{Type: "c2pa", Outcome: "detected"}}}, nil
	}, testWebFiles).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/check status = %d, body = %q", response.Code, response.Body.String())
	}
	var result serveResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode API result: %v", err)
	}
	if result.File != "sample.png" || !result.Detected || len(result.Response.Results) != 1 {
		t.Errorf("API result = %+v, want detected result for sample.png", result)
	}
	if checkedFilename != "sample.png" || !bytes.Equal(checkedData, []byte("image-data")) {
		t.Errorf("checked upload = (%q, %q), want sample.png and in-memory file contents", checkedFilename, checkedData)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}
