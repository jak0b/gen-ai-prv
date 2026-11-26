package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

const (
	multipartMemory  = 1 << 20
	maxSingleRequest = maxFileSize + 1 + multipartMemory
)

type serveResult struct {
	File     string        `json:"file"`
	Response checkResponse `json:"response,omitempty"`
	Error    string        `json:"error,omitempty"`
	Detected bool          `json:"detected"`
}

type serveRequestLogContextKey struct{}

type serveRequestLog struct {
	mu       sync.Mutex
	apiCalls []string
}

func (l *serveRequestLog) appendAPICall(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.apiCalls = append(l.apiCalls, call)
}

func (l *serveRequestLog) apiCallSnapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.apiCalls...)
}

func appendServeRequestAPILog(ctx context.Context, call string) bool {
	requestLog, ok := ctx.Value(serveRequestLogContextKey{}).(*serveRequestLog)
	if !ok {
		return false
	}
	requestLog.appendAPICall(call)
	return true
}

func newServeHandler(apiKey string, check fileCheckFunc, webFiles fs.FS) http.Handler {
	webDir, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	staticFiles := http.FileServer(http.FS(webDir))

	mux := http.NewServeMux()
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setServeHeaders(w)
		staticFiles.ServeHTTP(w, r)
	}))
	mux.HandleFunc("POST /api/check", func(w http.ResponseWriter, r *http.Request) {
		setServeHeaders(w)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		result, message, status := checkSingleUploadedFile(w, r, apiKey, check)
		w.WriteHeader(status)
		if message != "" {
			_ = json.NewEncoder(w).Encode(struct {
				Error string `json:"error"`
			}{Error: message})
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestLog := &serveRequestLog{}
		r = r.WithContext(context.WithValue(r.Context(), serveRequestLogContextKey{}, requestLog))
		response := &serveResponseWriter{ResponseWriter: w}
		mux.ServeHTTP(response, r)
		if response.status == 0 {
			response.status = http.StatusOK
		}
		apiCallSnapshot := requestLog.apiCallSnapshot()
		apiCalls, _ := json.Marshal(apiCallSnapshot)
		if len(apiCallSnapshot) > 0 {
			log.Printf("Incoming request: %s %s %d openai_calls=%s", r.Method, r.URL.Path, response.status, apiCalls)
			return
		}
		log.Printf("Incoming request: %s %s %d", r.Method, r.URL.Path, response.status)
	})
}

type serveResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *serveResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *serveResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func checkSingleUploadedFile(w http.ResponseWriter, r *http.Request, apiKey string, check fileCheckFunc) (serveResult, string, int) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSingleRequest)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		return serveResult{}, "Could not read the uploaded file. Please try again with a smaller file.", http.StatusBadRequest
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	files := r.MultipartForm.File["file"]
	if len(r.MultipartForm.File) != 1 || len(files) != 1 {
		return serveResult{}, "Choose exactly one file to check.", http.StatusBadRequest
	}
	return checkUploadedFile(r, files[0], apiKey, check), "", http.StatusOK
}

func checkUploadedFile(r *http.Request, uploaded *multipart.FileHeader, apiKey string, check fileCheckFunc) serveResult {
	result := serveResult{File: filepath.Base(uploaded.Filename)}
	path, err := saveUpload(uploaded)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer os.Remove(path)

	response, err := check(r.Context(), path, apiKey)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Response = response
	for _, item := range response.Results {
		if item.Outcome == "detected" {
			result.Detected = true
			break
		}
	}
	return result
}

func setServeHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; object-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

func saveUpload(upload *multipart.FileHeader) (string, error) {
	ext := filepath.Ext(filepath.Base(upload.Filename))
	file, err := os.CreateTemp("", "gen-ai-prv-*"+ext)
	if err != nil {
		return "", fmt.Errorf("prepare upload: %w", err)
	}
	path := file.Name()
	src, err := upload.Open()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("read upload: %w", err)
	}
	defer src.Close()

	size, copyErr := io.Copy(file, io.LimitReader(src, maxFileSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("save upload: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("save upload: %w", closeErr)
	}
	if size > maxFileSize {
		_ = os.Remove(path)
		return "", fmt.Errorf("file is larger than the 50 MiB upload limit")
	}
	return path, nil
}
