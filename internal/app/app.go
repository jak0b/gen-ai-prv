package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const (
	keyringService = "com.jak0b.gen-ai-prv"
	apiKeyName     = "OPENAI_API_KEY"
)

type apiKeySource string

const (
	apiKeySourceEnvironment apiKeySource = "environment"
	apiKeySourceKeyring     apiKeySource = "keyring"
	apiKeySourcePrompt      apiKeySource = "interactive prompt"
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

// Run executes the command-line application.
func Run() error {
	return newRootCommand().Execute()
}

func newRootCommand() *cobra.Command {
	var jsonOutput bool

	root := &cobra.Command{
		Use:           "gen-ai-prv [flags] <image|audio|pdf>...",
		Short:         "Check files for supported OpenAI provenance signals",
		Long:          "Check files for supported OpenAI provenance signals.\n\nSet OPENAI_API_KEY to your OpenAI API key. Use 'serve' to start the web interface.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, paths []string) error {
			if len(paths) == 0 {
				_ = cmd.Usage()
				return fmt.Errorf("at least one file argument is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, paths []string) error {
			envAPIKey, envSet := os.LookupEnv(apiKeyName)
			apiKey, source, err := resolveAPIKey(envAPIKey, envSet, osAPIKeyStore{}, promptForAPIKey)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), apiKeySourceMessage(source))

			results, err := checkFiles(context.Background(), paths, apiKey, checkFile)
			if err != nil {
				return err
			}

			if jsonOutput {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				var output any = results[0].Response
				if len(results) > 1 {
					output = results
				}
				if err := encoder.Encode(output); err != nil {
					return fmt.Errorf("writing JSON: %w", err)
				}
				return nil
			}

			color := term.IsTerminal(int(os.Stdout.Fd()))
			for i, result := range results {
				if len(results) > 1 {
					fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", result.File)
				}
				printSummary(cmd.OutOrStdout(), result.Response, color)
				if i < len(results)-1 {
					fmt.Fprintln(cmd.OutOrStdout())
				}
			}
			return nil
		},
	}
	root.Flags().BoolVar(&jsonOutput, "json", false, "print the complete API response as JSON")
	root.AddCommand(newServeCommand())
	return root
}

func apiKeySourceMessage(source apiKeySource) string {
	switch source {
	case apiKeySourceEnvironment:
		return fmt.Sprintf("Using API key from environment variable %s.", apiKeyName)
	case apiKeySourceKeyring:
		return "Using API key from OS keyring."
	case apiKeySourcePrompt:
		return "Using interactively entered API key (saved to OS keyring)."
	default:
		return ""
	}
}

type fileCheckResult struct {
	File     string        `json:"file"`
	Response checkResponse `json:"response"`
}

type fileCheckFunc func(context.Context, string, string) (checkResponse, error)

func checkFiles(ctx context.Context, paths []string, apiKey string, check fileCheckFunc) ([]fileCheckResult, error) {
	results := make([]fileCheckResult, 0, len(paths))
	for _, path := range paths {
		response, err := check(ctx, path, apiKey)
		if err != nil {
			return nil, fmt.Errorf("check %q: %w", path, err)
		}
		results = append(results, fileCheckResult{File: path, Response: response})
	}
	return results, nil
}

func resolveAPIKey(envValue string, envSet bool, store apiKeyStore, prompt func() (string, error)) (string, apiKeySource, error) {
	if envSet {
		if apiKey := strings.TrimSpace(envValue); apiKey != "" {
			return apiKey, apiKeySourceEnvironment, nil
		}
	}

	apiKey, err := store.Get(keyringService, apiKeyName)
	if err == nil && strings.TrimSpace(apiKey) != "" {
		return strings.TrimSpace(apiKey), apiKeySourceKeyring, nil
	}
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return "", "", fmt.Errorf("read API key from OS keyring: %w", err)
	}

	apiKey, err = prompt()
	if err != nil {
		return "", "", err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", fmt.Errorf("API key cannot be empty")
	}
	if err := store.Set(keyringService, apiKeyName, apiKey); err != nil {
		return "", "", fmt.Errorf("save API key to OS keyring: %w", err)
	}
	return apiKey, apiKeySourcePrompt, nil
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
