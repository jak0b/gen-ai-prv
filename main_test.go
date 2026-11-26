package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"
)

type fakeAPIKeyStore struct {
	key       string
	getErr    error
	setErr    error
	setCalled bool
	service   string
	account   string
}

func (s *fakeAPIKeyStore) Get(service, account string) (string, error) {
	s.service = service
	s.account = account
	return s.key, s.getErr
}

func (s *fakeAPIKeyStore) Set(service, account, key string) error {
	s.setCalled = true
	s.service = service
	s.account = account
	s.key = key
	return s.setErr
}

func TestRootCommandHelpIncludesFlagsAndServeCommand(t *testing.T) {
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"--json", "serve", "Check files for supported OpenAI provenance signals"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help output = %q, want it to contain %q", output.String(), want)
		}
	}
}

func TestRootCommandRequiresFileArguments(t *testing.T) {
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "at least one file argument is required") {
		t.Fatalf("Execute() error = %v, want missing-file argument error", err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Errorf("usage output = %q, want usage to be shown", output.String())
	}
}

func TestResolveAPIKeyPrefersEnvironment(t *testing.T) {
	store := &fakeAPIKeyStore{key: "keyring-key"}
	promptCalled := false

	got, source, err := resolveAPIKey(" env-key ", true, store, func() (string, error) {
		promptCalled = true
		return "", nil
	})
	if err != nil {
		t.Fatalf("resolveAPIKey() error = %v", err)
	}
	if got != "env-key" {
		t.Errorf("resolveAPIKey() = %q, want env-key", got)
	}
	if source != apiKeySourceEnvironment {
		t.Errorf("resolveAPIKey() source = %q, want %q", source, apiKeySourceEnvironment)
	}
	if promptCalled || store.setCalled {
		t.Fatal("environment key should bypass prompting and keyring writes")
	}
}

func TestResolveAPIKeyUsesSavedKey(t *testing.T) {
	store := &fakeAPIKeyStore{key: " saved-key "}
	promptCalled := false

	got, source, err := resolveAPIKey("", false, store, func() (string, error) {
		promptCalled = true
		return "", nil
	})
	if err != nil {
		t.Fatalf("resolveAPIKey() error = %v", err)
	}
	if got != "saved-key" {
		t.Errorf("resolveAPIKey() = %q, want saved-key", got)
	}
	if source != apiKeySourceKeyring {
		t.Errorf("resolveAPIKey() source = %q, want %q", source, apiKeySourceKeyring)
	}
	if promptCalled {
		t.Fatal("prompt called despite a saved key")
	}
}

func TestResolveAPIKeyPromptsAndSaves(t *testing.T) {
	store := &fakeAPIKeyStore{getErr: keyring.ErrNotFound}
	got, source, err := resolveAPIKey("", false, store, func() (string, error) {
		return " prompted-key ", nil
	})
	if err != nil {
		t.Fatalf("resolveAPIKey() error = %v", err)
	}
	if got != "prompted-key" || store.key != "prompted-key" || !store.setCalled {
		t.Fatalf("resolveAPIKey() = %q, saved key = %q, setCalled = %v", got, store.key, store.setCalled)
	}
	if source != apiKeySourcePrompt {
		t.Errorf("resolveAPIKey() source = %q, want %q", source, apiKeySourcePrompt)
	}
	if store.service != "com.jak0b.gen-ai-prv" || store.account != "OPENAI_API_KEY" {
		t.Errorf("keyring coordinates = (%q, %q), want (com.jak0b.gen-ai-prv, OPENAI_API_KEY)", store.service, store.account)
	}
}

func TestAPIKeySourceMessage(t *testing.T) {
	tests := []struct {
		source apiKeySource
		want   string
	}{
		{apiKeySourceEnvironment, "Using API key from environment variable OPENAI_API_KEY."},
		{apiKeySourceKeyring, "Using API key from OS keyring."},
		{apiKeySourcePrompt, "Using interactively entered API key (saved to OS keyring)."},
	}
	for _, test := range tests {
		if got := apiKeySourceMessage(test.source); got != test.want {
			t.Errorf("apiKeySourceMessage(%q) = %q, want %q", test.source, got, test.want)
		}
	}
}

func TestContentTypeFor(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"image.PNG", "image/png"},
		{"photo.jpeg", "image/jpeg"},
		{"clip.opus", "audio/ogg"},
		{"sound.mp3", "audio/mpeg"},
		{"report.pdf", "application/pdf"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			got, err := contentTypeFor(test.path)
			if err != nil {
				t.Fatalf("contentTypeFor() error = %v", err)
			}
			if got != test.want {
				t.Errorf("contentTypeFor() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContentTypeForRejectsUnsupportedExtension(t *testing.T) {
	if _, err := contentTypeFor("notes.txt"); err == nil {
		t.Fatal("contentTypeFor() accepted a text file")
	}
}

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

func TestCheckFilesChecksEachPath(t *testing.T) {
	paths := []string{"image.png", "audio.mp3"}
	var checked []string
	results, err := checkFiles(context.Background(), paths, "api-key", func(_ context.Context, path, apiKey string) (checkResponse, error) {
		checked = append(checked, path)
		if apiKey != "api-key" {
			t.Errorf("API key = %q, want api-key", apiKey)
		}
		return checkResponse{Object: path}, nil
	})
	if err != nil {
		t.Fatalf("checkFiles() error = %v", err)
	}
	if !reflect.DeepEqual(checked, paths) {
		t.Errorf("checked paths = %v, want %v", checked, paths)
	}
	if len(results) != len(paths) {
		t.Fatalf("got %d results, want %d", len(results), len(paths))
	}
	for i, path := range paths {
		if results[i].File != path || results[i].Response.Object != path {
			t.Errorf("result %d = %+v, want file and response object %q", i, results[i], path)
		}
	}
}

func TestCheckFilesReportsFailingPath(t *testing.T) {
	_, err := checkFiles(context.Background(), []string{"image.png", "missing.pdf"}, "api-key", func(_ context.Context, path, _ string) (checkResponse, error) {
		if path == "missing.pdf" {
			return checkResponse{}, errors.New("file not found")
		}
		return checkResponse{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), `check "missing.pdf": file not found`) {
		t.Fatalf("checkFiles() error = %v, want failing path and cause", err)
	}
}

func TestUploadFilenameIsASCIIAndPreservesExtension(t *testing.T) {
	got := uploadFilename("folder/powiatowy urza\u0328d pracy pup.JPG")
	if got != "upload.jpg" {
		t.Errorf("uploadFilename() = %q, want %q", got, "upload.jpg")
	}
}

func TestPrintSummaryDoesNotCallUndetectedHumanCreated(t *testing.T) {
	var output bytes.Buffer
	printSummary(&output, checkResponse{
		Results: []checkResult{{Type: "c2pa", Outcome: "not_detected"}},
	}, false)
	got := output.String()
	if !strings.HasPrefix(got, "PROVENANCE SIGNAL NOT DETECTED\n") ||
		!strings.Contains(got, "No supported OpenAI provenance signal detected.") ||
		!strings.Contains(got, "This result is not a general-purpose AI detection verdict.") {
		t.Fatalf("summary missing new or original not-detected message: %q", got)
	}
}

func TestPrintSummaryDetected(t *testing.T) {
	var output bytes.Buffer
	printSummary(&output, checkResponse{
		Results: []checkResult{{Type: "synthid", Outcome: "detected"}},
	}, false)
	if !strings.HasPrefix(output.String(), "PROVENANCE SIGNAL DETECTED\n") ||
		!strings.Contains(output.String(), "Supported OpenAI provenance signal detected.") {
		t.Fatalf("summary missing new or original detected message: %q", output.String())
	}
}

func TestPrintSummaryUsesResultColors(t *testing.T) {
	tests := []struct {
		name    string
		outcome string
		want    string
	}{
		{"not detected is green", "not_detected", "\033[32mPROVENANCE SIGNAL NOT DETECTED\033[0m\n"},
		{"detected is red", "detected", "\033[31mPROVENANCE SIGNAL DETECTED\033[0m\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			printSummary(&output, checkResponse{
				Results: []checkResult{{Type: "c2pa", Outcome: test.outcome}},
			}, true)
			if !strings.HasPrefix(output.String(), test.want) {
				t.Errorf("summary = %q, want colored prefix %q", output.String(), test.want)
			}
		})
	}
}

func TestServeHandlerEmbedsFrontend(t *testing.T) {
	handler := newServeHandler("api-key", func(context.Context, string, string) (checkResponse, error) {
		t.Fatal("check function called for frontend request")
		return checkResponse{}, nil
	})
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

func TestReadServeConfigFromFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "server.yaml")
	configData := []byte("host: 0.0.0.0\nport: 9443\ntls-cert: server.crt\ntls-key: server.key\n")
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}

	settings := viper.New()
	settings.SetDefault("host", defaultServeHost)
	settings.SetDefault("port", defaultServePort)
	config, err := readServeConfig(settings, configPath, "", false)
	if err != nil {
		t.Fatalf("readServeConfig() error = %v", err)
	}
	if config != (serveConfig{Host: "0.0.0.0", Port: 9443, TLSCert: "server.crt", TLSKey: "server.key"}) {
		t.Errorf("readServeConfig() = %+v, want values from config file", config)
	}
	if got := config.address(); got != "0.0.0.0:9443" {
		t.Errorf("address() = %q, want 0.0.0.0:9443", got)
	}
}

func TestReadServeConfigIgnoresExtensionlessExecutable(t *testing.T) {
	dir := t.TempDir()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(workingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()
	t.Setenv("HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "gen-ai-prv"), []byte{0xff, 0xfe, 0x00}, 0700); err != nil {
		t.Fatal(err)
	}

	settings := viper.New()
	settings.SetDefault("host", defaultServeHost)
	settings.SetDefault("port", defaultServePort)
	config, err := readServeConfig(settings, "", "", false)
	if err != nil {
		t.Fatalf("readServeConfig() error = %v", err)
	}
	if config.Host != defaultServeHost || config.Port != defaultServePort {
		t.Errorf("config = %+v, want default host and port", config)
	}
}

func TestReadServeConfigSupportsIPv6LegacyAddress(t *testing.T) {
	settings := viper.New()
	settings.SetDefault("host", defaultServeHost)
	settings.SetDefault("port", defaultServePort)
	configPath := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := readServeConfig(settings, configPath, "[::1]:9090", true)
	if err != nil {
		t.Fatalf("readServeConfig() error = %v", err)
	}
	if config.Host != "::1" || config.Port != 9090 {
		t.Errorf("config = %+v, want IPv6 host and port 9090", config)
	}
	if got := config.address(); got != "[::1]:9090" {
		t.Errorf("address() = %q, want [::1]:9090", got)
	}
}

func TestServeConfigAddressSupportsDualStackWildcard(t *testing.T) {
	config := serveConfig{Host: ":", Port: defaultServePort}
	if got, want := config.address(), ":8080"; got != want {
		t.Errorf("address() = %q, want %q", got, want)
	}
}

func TestRunServeCommandValidatesServerSettings(t *testing.T) {
	tests := []struct {
		name   string
		config serveConfig
		want   string
	}{
		{
			name:   "missing host",
			config: serveConfig{Port: defaultServePort},
			want:   "server host cannot be empty",
		},
		{
			name:   "invalid port",
			config: serveConfig{Host: defaultServeHost, Port: 65536},
			want:   "server port must be between 1 and 65535",
		},
		{
			name:   "TLS certificate without key",
			config: serveConfig{Host: defaultServeHost, Port: defaultServePort, TLSCert: "server.crt"},
			want:   "both TLS certificate and key must be provided",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := runServeCommand(test.config); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("runServeCommand() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestServeCommandUsesViperBoundFlags(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := newServeCommand()
	command.SetArgs([]string{"--config", configPath, "--host", "localhost", "--port", "0"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "server port must be between 1 and 65535") {
		t.Fatalf("Execute() error = %v, want invalid port validation", err)
	}
}

func TestServeHandlerChecksUploadedFiles(t *testing.T) {
	var checkedPath string
	handler := newServeHandler("api-key", func(_ context.Context, path, apiKey string) (checkResponse, error) {
		checkedPath = path
		if apiKey != "api-key" {
			t.Errorf("API key = %q, want api-key", apiKey)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("uploaded temporary file does not exist: %v", err)
		}
		return checkResponse{Results: []checkResult{{Type: "c2pa", Outcome: "detected"}}}, nil
	})

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("files", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("image-data")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/check", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /check status = %d, body = %q", response.Code, response.Body.String())
	}
	if checkedPath == "" {
		t.Fatal("check function was not called")
	}
	if _, err := os.Stat(checkedPath); !os.IsNotExist(err) {
		t.Errorf("temporary upload still exists after request: stat error = %v", err)
	}
	if !strings.Contains(response.Body.String(), "PROVENANCE SIGNAL DETECTED") {
		t.Errorf("response missing detected result: %q", response.Body.String())
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
	newServeHandler("api-key", func(context.Context, string, string) (checkResponse, error) {
		t.Fatal("check function called for a request containing multiple files")
		return checkResponse{}, nil
	}).ServeHTTP(response, request)

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
	var checkedPath string
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
	newServeHandler("api-key", func(_ context.Context, path, apiKey string) (checkResponse, error) {
		checkedPath = path
		if apiKey != "api-key" {
			t.Errorf("API key = %q, want api-key", apiKey)
		}
		return checkResponse{Results: []checkResult{{Type: "c2pa", Outcome: "detected"}}}, nil
	}).ServeHTTP(response, request)

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
	if _, err := os.Stat(checkedPath); !os.IsNotExist(err) {
		t.Errorf("temporary upload still exists after request: stat error = %v", err)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestServeHandlerRequiresUploadedFiles(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/check", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	newServeHandler("api-key", checkFile).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Choose at least one file") {
		t.Errorf("POST /check response = %d %q, want missing-file error", response.Code, response.Body.String())
	}
}

func TestServeHandlerDisplaysCheckError(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("files", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("image-data")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/check", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	checkErr := errors.New("OpenAI API returned 429 Too Many Requests (rate_limit_exceeded): Rate limit reached")
	newServeHandler("api-key", func(context.Context, string, string) (checkResponse, error) {
		return checkResponse{}, checkErr
	}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /check status = %d, body = %q", response.Code, response.Body.String())
	}
	for _, want := range []string{"Could not check this file.", "429 Too Many Requests", "Rate limit reached"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("response = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}
