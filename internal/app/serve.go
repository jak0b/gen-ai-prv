package app

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	defaultServeHost = "127.0.0.1"
	defaultServePort = 8080
)

func newServeCommand(webFiles fs.FS) *cobra.Command {
	settings := viper.New()
	var configFile string
	var addr string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Start the web interface",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			config, err := readServeConfig(settings, configFile, addr, command.Flags().Changed("addr"))
			if err != nil {
				return err
			}
			return runServeCommand(config, webFiles)
		},
	}
	command.Flags().StringVar(&configFile, "config", "", "path to YAML server configuration file")
	command.Flags().String("host", defaultServeHost, "host interface to listen on")
	command.Flags().Int("port", defaultServePort, "port to listen on")
	command.Flags().String("tls-cert", "", "TLS certificate file (enables HTTPS with --tls-key)")
	command.Flags().String("tls-key", "", "TLS private key file (enables HTTPS with --tls-cert)")
	command.Flags().StringVar(&addr, "addr", "", "listen address (deprecated; use --host and --port)")
	_ = command.Flags().MarkDeprecated("addr", "use --host and --port instead")

	settings.SetDefault("host", defaultServeHost)
	settings.SetDefault("port", defaultServePort)
	settings.SetDefault("tls-cert", "")
	settings.SetDefault("tls-key", "")
	settings.SetEnvPrefix("GEN_AI_PRV")
	settings.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	settings.AutomaticEnv()
	_ = settings.BindPFlags(command.Flags())
	return command
}

func readServeConfig(settings *viper.Viper, configFile, addr string, addrSet bool) (serveConfig, error) {
	readConfig := configFile != ""
	if configFile != "" {
		settings.SetConfigFile(configFile)
	} else {
		configPaths := []string{"."}
		if home, err := os.UserHomeDir(); err == nil {
			configPaths = append(configPaths,
				filepath.Join(home, ".config", "gen-ai-prv"),
				home,
			)
		}
		for _, directory := range configPaths {
			for _, name := range []string{"gen-ai-prv.yaml", "gen-ai-prv.yml"} {
				path := filepath.Join(directory, name)
				info, err := os.Stat(path)
				if err == nil && !info.IsDir() {
					settings.SetConfigFile(path)
					readConfig = true
					break
				}
				if err != nil && !os.IsNotExist(err) {
					return serveConfig{}, fmt.Errorf("inspect server configuration %q: %w", path, err)
				}
			}
			if readConfig {
				break
			}
		}
	}
	if readConfig {
		if err := settings.ReadInConfig(); err != nil {
			return serveConfig{}, fmt.Errorf("read server configuration: %w", err)
		}
	}

	config := serveConfig{
		Host:    settings.GetString("host"),
		Port:    settings.GetInt("port"),
		TLSCert: settings.GetString("tls-cert"),
		TLSKey:  settings.GetString("tls-key"),
	}
	if addrSet {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return serveConfig{}, fmt.Errorf("invalid --addr %q: %w", addr, err)
		}
		config.Host = host
		config.Port, err = strconv.Atoi(port)
		if err != nil {
			return serveConfig{}, fmt.Errorf("invalid port in --addr %q: %w", addr, err)
		}
	}
	return config, nil
}

type serveConfig struct {
	Host    string
	Port    int
	TLSCert string
	TLSKey  string
}

func (config serveConfig) address() string {
	host := config.Host
	if host == ":" {
		// A colon-only host is commonly used to mean "all interfaces". Passing
		// it through JoinHostPort would produce the invalid address "[:]".
		host = ""
	}
	return net.JoinHostPort(host, strconv.Itoa(config.Port))
}

func runServeCommand(config serveConfig, webFiles fs.FS) error {
	if config.Host == "" {
		return fmt.Errorf("server host cannot be empty")
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535")
	}
	if (config.TLSCert == "") != (config.TLSKey == "") {
		return fmt.Errorf("both TLS certificate and key must be provided")
	}

	envAPIKey, envSet := os.LookupEnv(apiKeyName)
	apiKey, source, err := resolveAPIKey(envAPIKey, envSet, osAPIKeyStore{}, func() (string, error) {
		return "", fmt.Errorf("no API key available; set %s or save one in the OS keyring", apiKeyName)
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:    config.address(),
		Handler: newServeHandler(apiKey, checkFile, webFiles),
	}
	log.Print(apiKeySourceMessage(source))
	scheme := "http"
	if config.TLSCert != "" {
		scheme = "https"
	}
	log.Printf("Serving at %s://%s", scheme, server.Addr)
	var serveErr error
	if config.TLSCert != "" {
		serveErr = server.ListenAndServeTLS(config.TLSCert, config.TLSKey)
	} else {
		serveErr = server.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("start server: %w", serveErr)
	}
	return nil
}
