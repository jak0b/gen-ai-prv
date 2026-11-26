package main

import (
	"fmt"
	"io"
)

func printSummary(w io.Writer, result checkResponse) {
	detected := false
	for _, item := range result.Results {
		if item.Outcome == "detected" {
			detected = true
		}
	}
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
