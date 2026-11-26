package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/jak0b/gen-ai-prv/internal/app"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	if err := app.Run(webFiles); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
