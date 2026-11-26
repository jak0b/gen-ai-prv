package main

import (
	"bytes"
	"strings"
	"testing"
)

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
	})
	got := output.String()
	if !strings.Contains(got, "No supported OpenAI provenance signal detected.") {
		t.Fatalf("summary missing not-detected outcome: %q", got)
	}
	if !strings.Contains(got, "not a general-purpose AI detection verdict") {
		t.Fatalf("summary missing scope warning: %q", got)
	}
	if strings.Contains(got, "human-created") {
		t.Fatalf("summary implies that no signal means human-created: %q", got)
	}
}

func TestPrintSummaryDetected(t *testing.T) {
	var output bytes.Buffer
	printSummary(&output, checkResponse{
		Results: []checkResult{{Type: "synthid", Outcome: "detected"}},
	})
	if !strings.Contains(output.String(), "Supported OpenAI provenance signal detected.") {
		t.Fatalf("summary missing detected outcome: %q", output.String())
	}
}
