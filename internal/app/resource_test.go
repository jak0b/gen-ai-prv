package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestValidateResourceURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "https", url: "https://example.com/image.png"},
		{name: "uppercase scheme", url: "HTTPS://example.com/image.png"},
		{name: "http", url: "http://example.com:80/audio.mp3"},
		{name: "unsupported scheme", url: "file:///etc/passwd", want: "only HTTP and HTTPS"},
		{name: "private address", url: "http://127.0.0.1/image.png", want: "private or local"},
		{name: "private IPv6 address", url: "http://[::1]/image.png", want: "private or local"},
		{name: "non-standard port", url: "https://example.com:8443/image.png", want: "standard"},
		{name: "credentials", url: "https://user:pass@example.com/image.png", want: "credentials"},
		{name: "missing host", url: "https:///image.png", want: "valid HTTP or HTTPS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateResourceURL(test.url)
			if test.want == "" && err != nil {
				t.Fatalf("validateResourceURL() error = %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validateResourceURL() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestIsPublicResourceIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{ip: "8.8.8.8", want: true},
		{ip: "127.0.0.1"},
		{ip: "10.0.0.1"},
		{ip: "100.64.0.1"},
		{ip: "192.0.2.1"},
		{ip: "169.254.1.1"},
		{ip: "::1"},
		{ip: "2001:db8::1"},
		{ip: "2606:4700:4700::1111", want: true},
	}
	for _, test := range tests {
		t.Run(test.ip, func(t *testing.T) {
			got := isPublicResourceIP(netip.MustParseAddr(test.ip))
			if got != test.want {
				t.Errorf("isPublicResourceIP(%s) = %t, want %t", test.ip, got, test.want)
			}
		})
	}
}

func TestValidateFetchedResource(t *testing.T) {
	validPNG := []byte("\x89PNG\r\n\x1a\npayload")
	if contentType, err := validateFetchedResource("image.png", "image/png; charset=binary", validPNG); err != nil || contentType != "image/png" {
		t.Fatalf("validateFetchedResource() = (%q, %v), want image/png", contentType, err)
	}
	for _, test := range []struct {
		name        string
		filename    string
		contentType string
		data        []byte
		want        string
	}{
		{name: "unsupported extension", filename: "notes.txt", contentType: "text/plain", data: []byte("text"), want: "unsupported file type"},
		{name: "mismatched content type", filename: "image.png", contentType: "image/jpeg", data: validPNG, want: "does not match"},
		{name: "invalid image signature", filename: "image.png", contentType: "image/png", data: []byte("not an image"), want: "not a valid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateFetchedResource(test.filename, test.contentType, test.data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateFetchedResource() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFetchEndpointReturnsValidatedResourceInMemory(t *testing.T) {
	wantData := []byte("\x89PNG\r\n\x1a\npayload")
	handler := newServeHandlerWithFetcher("api-key", func(context.Context, string, []byte, string) (checkResponse, error) {
		t.Fatal("check called during resource fetch")
		return checkResponse{}, nil
	}, func(_ context.Context, rawURL string) (fetchedResource, error) {
		if rawURL != "https://example.test/path/sample.png?download=1" {
			t.Errorf("fetch URL = %q", rawURL)
		}
		return fetchedResource{filename: "sample.png", contentType: "image/png", data: wantData}, nil
	}, testWebFiles)

	body, err := json.Marshal(map[string]string{"url": "https://example.test/path/sample.png?download=1"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/fetch", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/fetch status = %d, body = %q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Resource-Name") != "sample.png" || response.Header().Get("Content-Type") != "image/png" {
		t.Errorf("fetch response headers = %v", response.Header())
	}
	if !bytes.Equal(response.Body.Bytes(), wantData) {
		t.Errorf("fetch response body = %q, want in-memory resource bytes", response.Body.Bytes())
	}
}
