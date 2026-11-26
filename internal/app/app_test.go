package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

var testWebFiles = os.DirFS("../..")

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
	command := newRootCommand(testWebFiles)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"--json", "serve", "Check local files and HTTP/HTTPS URLs for supported OpenAI provenance signals"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help output = %q, want it to contain %q", output.String(), want)
		}
	}
}

func TestRootCommandRequiresFileArguments(t *testing.T) {
	command := newRootCommand(testWebFiles)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "at least one file or URL argument is required") {
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

func TestCheckInputsFetchesURLsAndChecksFiles(t *testing.T) {
	inputs := []string{
		"./image.png",
		"https://example.test/remote.png",
		"./document.pdf",
		"http://cdn.example.test/second.jpg",
	}
	var checkedFiles []string
	var fetchedURLs []string
	var uploaded []string

	results, err := checkInputs(
		context.Background(),
		inputs,
		"api-key",
		func(_ context.Context, path, _ string) (checkResponse, error) {
			checkedFiles = append(checkedFiles, path)
			return checkResponse{Object: path}, nil
		},
		func(_ context.Context, rawURL string) (fetchedResource, error) {
			fetchedURLs = append(fetchedURLs, rawURL)
			filename := "remote.png"
			if strings.HasSuffix(rawURL, "second.jpg") {
				filename = "second.jpg"
			}
			return fetchedResource{filename: filename, data: []byte(rawURL)}, nil
		},
		func(_ context.Context, filename string, data []byte, _ string) (checkResponse, error) {
			uploaded = append(uploaded, filename+":"+string(data))
			return checkResponse{Object: filename}, nil
		},
	)
	if err != nil {
		t.Fatalf("checkInputs() error = %v", err)
	}
	if !reflect.DeepEqual(checkedFiles, []string{"./image.png", "./document.pdf"}) {
		t.Errorf("checked files = %v, want both local files", checkedFiles)
	}
	if !reflect.DeepEqual(fetchedURLs, []string{
		"https://example.test/remote.png",
		"http://cdn.example.test/second.jpg",
	}) {
		t.Errorf("fetched URLs = %v, want both original HTTP(S) URLs in order", fetchedURLs)
	}
	if !reflect.DeepEqual(uploaded, []string{
		"remote.png:https://example.test/remote.png",
		"second.jpg:http://cdn.example.test/second.jpg",
	}) {
		t.Errorf("uploaded data = %v, want both fetched resources", uploaded)
	}
	if len(results) != len(inputs) {
		t.Fatalf("got %d results, want %d", len(results), len(inputs))
	}
	for i, input := range inputs {
		if results[i].File != input {
			t.Errorf("result %d label = %q, want original input %q", i, results[i].File, input)
		}
	}
}

func TestCheckInputsReportsFetchErrorWithURL(t *testing.T) {
	const input = "https://example.test/image.png"
	_, err := checkInputs(
		context.Background(),
		[]string{input},
		"api-key",
		nil,
		func(context.Context, string) (fetchedResource, error) {
			return fetchedResource{}, errors.New("download failed")
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), `check "`+input+`": download failed`) {
		t.Fatalf("checkInputs() error = %v, want URL and fetch error", err)
	}
}
