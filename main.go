package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const (
	keyringService = "com.jak0b.gen-ai-prv"
	apiKeyName     = "OPENAI_API_KEY"
)

type apiKeyStore interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
}

type osAPIKeyStore struct{}

func (osAPIKeyStore) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osAPIKeyStore) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func main() {
	jsonOutput := flag.Bool("json", false, "print the complete API response as JSON")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags] <image|audio|pdf>\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(flag.CommandLine.Output(), "Check a file for supported OpenAI provenance signals.")
		fmt.Fprintln(flag.CommandLine.Output(), "\nFlags:")
		flag.PrintDefaults()
		fmt.Fprintln(flag.CommandLine.Output(), "\nSet OPENAI_API_KEY to your OpenAI API key.")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	envAPIKey, envSet := os.LookupEnv(apiKeyName)
	apiKey, err := resolveAPIKey(envAPIKey, envSet, osAPIKeyStore{}, promptForAPIKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	result, err := checkFile(context.Background(), flag.Arg(0), apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "error: writing JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}

	printSummary(os.Stdout, result)
}

func resolveAPIKey(envValue string, envSet bool, store apiKeyStore, prompt func() (string, error)) (string, error) {
	if envSet {
		if apiKey := strings.TrimSpace(envValue); apiKey != "" {
			return apiKey, nil
		}
	}

	apiKey, err := store.Get(keyringService, apiKeyName)
	if err == nil && strings.TrimSpace(apiKey) != "" {
		return strings.TrimSpace(apiKey), nil
	}
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("read API key from OS keyring: %w", err)
	}

	apiKey, err = prompt()
	if err != nil {
		return "", err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("API key cannot be empty")
	}
	if err := store.Set(keyringService, apiKeyName, apiKey); err != nil {
		return "", fmt.Errorf("save API key to OS keyring: %w", err)
	}
	return apiKey, nil
}

func promptForAPIKey() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("%s is not set, no API key is saved in the OS keyring, and stdin is not a terminal", apiKeyName)
	}

	fmt.Fprint(os.Stderr, "OpenAI API key: ")
	key, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read API key: %w", err)
	}
	return string(key), nil
}
