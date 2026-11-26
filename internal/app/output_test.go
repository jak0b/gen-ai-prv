package app

import (
	"bytes"
	"strings"
	"testing"
)

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
