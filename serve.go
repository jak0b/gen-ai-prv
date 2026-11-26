package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	maxServeFiles    = 10
	multipartMemory  = 1 << 20
	maxServeRequest  = (maxFileSize+1)*maxServeFiles + multipartMemory
	maxSingleRequest = maxFileSize + 1 + multipartMemory
	defaultServeAddr = "127.0.0.1:8080"
)

//go:embed web/*
var webFiles embed.FS

type serveResult struct {
	File     string        `json:"file"`
	Response checkResponse `json:"response,omitempty"`
	Error    string        `json:"error,omitempty"`
	Detected bool          `json:"detected"`
}

type servePage struct {
	Results []serveResult `json:"results,omitempty"`
	Error   string        `json:"error,omitempty"`
}

var resultTemplate = template.Must(template.ParseFS(webFiles, "web/result.html"))

func newServeCommand() *cobra.Command {
	addr := defaultServeAddr
	command := &cobra.Command{
		Use:   "serve",
		Short: "Start the web interface",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runServeCommand(addr)
		},
	}
	command.Flags().StringVar(&addr, "addr", defaultServeAddr, "address to listen on")
	return command
}

func runServeCommand(addr string) error {
	envAPIKey, envSet := os.LookupEnv(apiKeyName)
	apiKey, source, err := resolveAPIKey(envAPIKey, envSet, osAPIKeyStore{}, func() (string, error) {
		return "", fmt.Errorf("no API key available; set %s or save one in the OS keyring", apiKeyName)
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:    addr,
		Handler: newServeHandler(apiKey, checkFile),
	}
	log.Print(apiKeySourceMessage(source))
	log.Printf("Serving at http://%s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("start server: %w", err)
	}
	return nil
}

func newServeHandler(apiKey string, check fileCheckFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		setServeHeaders(w)
		http.ServeFileFS(w, r, webFiles, "web/index.html")
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		setServeHeaders(w)
		http.ServeFileFS(w, r, webFiles, "web/style.css")
	})
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		setServeHeaders(w)
		http.ServeFileFS(w, r, webFiles, "web/app.js")
	})
	mux.HandleFunc("POST /check", func(w http.ResponseWriter, r *http.Request) {
		setServeHeaders(w)
		page, status := checkUploadedFiles(w, r, apiKey, check)
		writeServePage(w, page, status)
	})
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
	return mux
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

func checkUploadedFiles(w http.ResponseWriter, r *http.Request, apiKey string, check fileCheckFunc) (servePage, int) {
	r.Body = http.MaxBytesReader(w, r.Body, maxServeRequest)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		return servePage{Error: "Could not read the uploaded files. Please try again with smaller files."}, http.StatusBadRequest
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		return servePage{Error: "Choose at least one file to check."}, http.StatusBadRequest
	}
	if len(files) > maxServeFiles {
		return servePage{Error: fmt.Sprintf("Choose no more than %d files at a time.", maxServeFiles)}, http.StatusBadRequest
	}

	page := servePage{Results: make([]serveResult, 0, len(files))}
	for _, uploaded := range files {
		page.Results = append(page.Results, checkUploadedFile(r, uploaded, apiKey, check))
	}
	return page, http.StatusOK
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

func writeServePage(w http.ResponseWriter, page servePage, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := resultTemplate.Execute(w, page); err != nil {
		// The response may already have started; logging is unnecessary for this
		// local interface, but avoid turning template errors into a panic.
		return
	}
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
