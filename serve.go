package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxServeFiles    = 10
	multipartMemory  = 1 << 20
	maxServeRequest  = (maxFileSize+1)*maxServeFiles + multipartMemory
	defaultServeAddr = "127.0.0.1:8080"
)

//go:embed web/*
var webFiles embed.FS

type serveResult struct {
	File     string
	Response checkResponse
	Error    string
	Detected bool
}

type servePage struct {
	Results []serveResult
	Error   string
}

var resultTemplate = template.Must(template.ParseFS(webFiles, "web/result.html"))

func serveCommand(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", defaultServeAddr, "address to listen on")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	envAPIKey, envSet := os.LookupEnv(apiKeyName)
	apiKey, _, err := resolveAPIKey(envAPIKey, envSet, osAPIKeyStore{}, func() (string, error) {
		return "", fmt.Errorf("no API key available; set %s or save one in the OS keyring", apiKeyName)
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:    *addr,
		Handler: newServeHandler(apiKey, checkFile),
	}
	fmt.Fprintf(os.Stderr, "Serving at http://%s\n", *addr)
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
	mux.HandleFunc("POST /check", func(w http.ResponseWriter, r *http.Request) {
		setServeHeaders(w)
		r.Body = http.MaxBytesReader(w, r.Body, maxServeRequest)
		if err := r.ParseMultipartForm(multipartMemory); err != nil {
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
			writeServePage(w, servePage{Error: "Could not read the uploaded files. Please try again with smaller files."}, http.StatusBadRequest)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}

		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			writeServePage(w, servePage{Error: "Choose at least one file to check."}, http.StatusBadRequest)
			return
		}
		if len(files) > maxServeFiles {
			writeServePage(w, servePage{Error: fmt.Sprintf("Choose no more than %d files at a time.", maxServeFiles)}, http.StatusBadRequest)
			return
		}

		page := servePage{Results: make([]serveResult, 0, len(files))}
		for _, uploaded := range files {
			result := serveResult{File: filepath.Base(uploaded.Filename)}
			path, err := saveUpload(uploaded)
			if err != nil {
				result.Error = err.Error()
			} else {
				response, checkErr := check(r.Context(), path, apiKey)
				if checkErr != nil {
					result.Error = checkErr.Error()
				} else {
					result.Response = response
					for _, item := range response.Results {
						if item.Outcome == "detected" {
							result.Detected = true
							break
						}
					}
				}
				_ = os.Remove(path)
			}
			page.Results = append(page.Results, result)
		}
		writeServePage(w, page, http.StatusOK)
	})
	return mux
}

func setServeHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'none'; object-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
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
