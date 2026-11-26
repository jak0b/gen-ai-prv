package app

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	maxServeFiles    = 10
	multipartMemory  = 1 << 20
	maxServeRequest  = (maxFileSize+1)*maxServeFiles + multipartMemory
	maxSingleRequest = maxFileSize + 1 + multipartMemory
	defaultServeHost = "127.0.0.1"
	defaultServePort = 8080
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

func newServeCommand() *cobra.Command {
	settings := viper.New()
	var configFile string
	var addr string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Start the web interface",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			config, err := readServeConfig(settings, configFile, addr, command.Flags().Changed("addr"))
			if err != nil {
				return err
			}
			return runServeCommand(config)
		},
	}
	command.Flags().StringVar(&configFile, "config", "", "path to YAML server configuration file")
	command.Flags().String("host", defaultServeHost, "host interface to listen on")
	command.Flags().Int("port", defaultServePort, "port to listen on")
	command.Flags().String("tls-cert", "", "TLS certificate file (enables HTTPS with --tls-key)")
	command.Flags().String("tls-key", "", "TLS private key file (enables HTTPS with --tls-cert)")
	command.Flags().StringVar(&addr, "addr", "", "listen address (deprecated; use --host and --port)")
	_ = command.Flags().MarkDeprecated("addr", "use --host and --port instead")

	settings.SetDefault("host", defaultServeHost)
	settings.SetDefault("port", defaultServePort)
	settings.SetDefault("tls-cert", "")
	settings.SetDefault("tls-key", "")
	settings.SetEnvPrefix("GEN_AI_PRV")
	settings.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	settings.AutomaticEnv()
	_ = settings.BindPFlags(command.Flags())
	return command
}

func readServeConfig(settings *viper.Viper, configFile, addr string, addrSet bool) (serveConfig, error) {
	readConfig := configFile != ""
	if configFile != "" {
		settings.SetConfigFile(configFile)
	} else {
		configPaths := []string{"."}
		if home, err := os.UserHomeDir(); err == nil {
			configPaths = append(configPaths,
				filepath.Join(home, ".config", "gen-ai-prv"),
				home,
			)
		}
		for _, directory := range configPaths {
			for _, name := range []string{"gen-ai-prv.yaml", "gen-ai-prv.yml"} {
				path := filepath.Join(directory, name)
				info, err := os.Stat(path)
				if err == nil && !info.IsDir() {
					settings.SetConfigFile(path)
					readConfig = true
					break
				}
				if err != nil && !os.IsNotExist(err) {
					return serveConfig{}, fmt.Errorf("inspect server configuration %q: %w", path, err)
				}
			}
			if readConfig {
				break
			}
		}
	}
	if readConfig {
		if err := settings.ReadInConfig(); err != nil {
			return serveConfig{}, fmt.Errorf("read server configuration: %w", err)
		}
	}

	config := serveConfig{
		Host:    settings.GetString("host"),
		Port:    settings.GetInt("port"),
		TLSCert: settings.GetString("tls-cert"),
		TLSKey:  settings.GetString("tls-key"),
	}
	if addrSet {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return serveConfig{}, fmt.Errorf("invalid --addr %q: %w", addr, err)
		}
		config.Host = host
		config.Port, err = strconv.Atoi(port)
		if err != nil {
			return serveConfig{}, fmt.Errorf("invalid port in --addr %q: %w", addr, err)
		}
	}
	return config, nil
}

type serveConfig struct {
	Host    string
	Port    int
	TLSCert string
	TLSKey  string
}

func (config serveConfig) address() string {
	host := config.Host
	if host == ":" {
		// A colon-only host is commonly used to mean "all interfaces". Passing
		// it through JoinHostPort would produce the invalid address "[:]".
		host = ""
	}
	return net.JoinHostPort(host, strconv.Itoa(config.Port))
}

func runServeCommand(config serveConfig) error {
	if config.Host == "" {
		return fmt.Errorf("server host cannot be empty")
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535")
	}
	if (config.TLSCert == "") != (config.TLSKey == "") {
		return fmt.Errorf("both TLS certificate and key must be provided")
	}

	envAPIKey, envSet := os.LookupEnv(apiKeyName)
	apiKey, source, err := resolveAPIKey(envAPIKey, envSet, osAPIKeyStore{}, func() (string, error) {
		return "", fmt.Errorf("no API key available; set %s or save one in the OS keyring", apiKeyName)
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:    config.address(),
		Handler: newServeHandler(apiKey, checkFile),
	}
	log.Print(apiKeySourceMessage(source))
	scheme := "http"
	if config.TLSCert != "" {
		scheme = "https"
	}
	log.Printf("Serving at %s://%s", scheme, server.Addr)
	var serveErr error
	if config.TLSCert != "" {
		serveErr = server.ListenAndServeTLS(config.TLSCert, config.TLSKey)
	} else {
		serveErr = server.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("start server: %w", serveErr)
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
