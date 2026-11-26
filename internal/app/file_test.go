package app

import "testing"

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
