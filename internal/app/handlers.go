package app

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"sync"
)

const (
	multipartOverheadLimit = 1 << 20
	maxSingleRequest       = maxFileSize + 1 + multipartOverheadLimit
	maxResourceURLRequest  = 8 << 10
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

type serveHandler struct {
	apiKey      string
	check       uploadedFileCheckFunc
	fetch       resourceFetchFunc
	staticFiles http.Handler
	mux         *http.ServeMux
}

func newServeHandler(apiKey string, check uploadedFileCheckFunc, webFiles fs.FS) http.Handler {
	return newServeHandlerWithFetcher(apiKey, check, fetchRemoteResource, webFiles)
}

func newServeHandlerWithFetcher(apiKey string, check uploadedFileCheckFunc, fetch resourceFetchFunc, webFiles fs.FS) http.Handler {
	webDir, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}

	handler := &serveHandler{
		apiKey:      apiKey,
		check:       check,
		fetch:       fetch,
		staticFiles: http.FileServer(http.FS(webDir)),
		mux:         http.NewServeMux(),
	}
	handler.mux.Handle("GET /", http.HandlerFunc(handler.serveStatic))
	handler.mux.HandleFunc("POST /api/check", handler.checkUpload)
	handler.mux.HandleFunc("POST /api/fetch", handler.fetchURL)

	return handler
}

func (h *serveHandler) serveStatic(w http.ResponseWriter, r *http.Request) {
	setServeHeaders(w)
	h.staticFiles.ServeHTTP(w, r)
}

func (h *serveHandler) checkUpload(w http.ResponseWriter, r *http.Request) {
	setServeHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	result, message, status := checkSingleUploadedFile(w, r, h.apiKey, h.check)
	w.WriteHeader(status)
	if message != "" {
		_ = json.NewEncoder(w).Encode(struct {
			Error string `json:"error"`
		}{Error: message})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (h *serveHandler) fetchURL(w http.ResponseWriter, r *http.Request) {
	setServeHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	r.Body = http.MaxBytesReader(w, r.Body, maxResourceURLRequest)
	var request struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeServeError(w, "Enter a valid HTTP or HTTPS URL.", http.StatusBadRequest)
		return
	}
	resource, err := h.fetch(r.Context(), request.URL)
	if err != nil {
		writeServeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// A filename is returned as a header only after URL parsing and format
	// validation, and controls and path separators have already been removed.
	w.Header().Set("Content-Type", resource.contentType)
	w.Header().Set("X-Resource-Name", resource.filename)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resource.data)
}

func writeServeError(w http.ResponseWriter, message string, status int) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}

func (h *serveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestLog := &serveRequestLog{}
	r = r.WithContext(context.WithValue(r.Context(), serveRequestLogContextKey{}, requestLog))
	response := &serveResponseWriter{ResponseWriter: w}
	h.mux.ServeHTTP(response, r)
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

func checkSingleUploadedFile(w http.ResponseWriter, r *http.Request, apiKey string, check uploadedFileCheckFunc) (serveResult, string, int) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSingleRequest)
	filename, data, err := readUploadedFile(r)
	if err != nil {
		return serveResult{}, "Could not read the uploaded file. Please try again with a smaller file.", http.StatusBadRequest
	}
	if filename == "" {
		return serveResult{}, "Choose exactly one file to check.", http.StatusBadRequest
	}
	return checkUploadedFile(r, filename, data, apiKey, check), "", http.StatusOK
}

func readUploadedFile(r *http.Request) (string, []byte, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return "", nil, err
	}

	var filename string
	var data []byte
	fileCount := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}

		partName, partFilename := part.FormName(), part.FileName()
		if partFilename == "" {
			_, err = io.Copy(io.Discard, part)
			_ = part.Close()
			if err != nil {
				return "", nil, err
			}
			continue
		}

		fileCount++
		if partName != "file" || fileCount != 1 {
			_, err = io.Copy(io.Discard, part)
			_ = part.Close()
			if err != nil {
				return "", nil, err
			}
			continue
		}

		data, err = io.ReadAll(io.LimitReader(part, maxFileSize+1))
		_ = part.Close()
		if err != nil {
			return "", nil, err
		}
		filename = partFilename
	}

	if fileCount != 1 || filename == "" {
		return "", nil, nil
	}
	return filename, data, nil
}

func checkUploadedFile(r *http.Request, filename string, data []byte, apiKey string, check uploadedFileCheckFunc) serveResult {
	result := serveResult{File: filepath.Base(filename)}
	response, err := check(r.Context(), filename, data, apiKey)
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
