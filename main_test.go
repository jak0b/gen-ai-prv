package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

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
