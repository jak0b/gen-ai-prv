package main

import (
	"fmt"
	"io"
)

func printSummary(w io.Writer, result checkResponse, color bool) {
	detected := false
	for _, item := range result.Results {
		if item.Outcome == "detected" {
			detected = true
		}
	}

	message := "PROVENANCE SIGNAL NOT DETECTED"
	if detected {
		message = "PROVENANCE SIGNAL DETECTED"
	}
	if color {
		colorCode := "32"
		if detected {
			colorCode = "31"
		}
		message = fmt.Sprintf("\033[%sm%s\033[0m", colorCode, message)
	}
	fmt.Fprintln(w, message)

	if detected {
		fmt.Fprintln(w, "Supported OpenAI provenance signal detected.")
	} else {
		fmt.Fprintln(w, "No supported OpenAI provenance signal detected.")
	}
	fmt.Fprintln(w, "This result is not a general-purpose AI detection verdict.")
	for _, item := range result.Results {
		fmt.Fprintf(w, "\n%s: %s", item.Type, item.Outcome)
		if item.ValidationState != nil {
			fmt.Fprintf(w, " (validation: %s)", *item.ValidationState)
		}
		if item.Issuer != nil {
			fmt.Fprintf(w, "\n  issuer: %s", *item.Issuer)
		}
		if item.Model != nil {
			fmt.Fprintf(w, "\n  model: %s", *item.Model)
		}
		if item.GeneratedAt != nil {
			fmt.Fprintf(w, "\n  generated at: %s", *item.GeneratedAt)
		}
		fmt.Fprintln(w)
	}
}
