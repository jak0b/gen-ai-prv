package app

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestReadServeConfigSupportsIPv6LegacyAddress(t *testing.T) {
	settings := viper.New()
	settings.SetDefault("host", "127.0.0.1")
	settings.SetDefault("port", 8080)
	config, err := readServeConfig(settings, "[::1]:9090", true)
	if err != nil {
		t.Fatalf("readServeConfig() error = %v", err)
	}
	if config.Host != "::1" || config.Port != 9090 {
		t.Errorf("config = %+v, want IPv6 host and port 9090", config)
	}
	if got := config.address(); got != "[::1]:9090" {
		t.Errorf("address() = %q, want [::1]:9090", got)
	}
}

func TestServeConfigAddressSupportsDualStackWildcard(t *testing.T) {
	config := serveConfig{Host: ":", Port: 8080}
	if got, want := config.address(), ":8080"; got != want {
		t.Errorf("address() = %q, want %q", got, want)
	}
}

func TestRunServeCommandValidatesServerSettings(t *testing.T) {
	tests := []struct {
		name   string
		config serveConfig
		want   string
	}{
		{
			name:   "missing host",
			config: serveConfig{Port: 8080},
			want:   "server host cannot be empty",
		},
		{
			name:   "invalid port",
			config: serveConfig{Host: "127.0.0.1", Port: 65536},
			want:   "server port must be between 1 and 65535",
		},
		{
			name:   "TLS certificate without key",
			config: serveConfig{Host: "127.0.0.1", Port: 8080, TLSCert: "server.crt"},
			want:   "both TLS certificate and key must be provided",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := runServeCommand(test.config, testWebFiles, viper.New()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("runServeCommand() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestServeCommandAcceptsFlagsAndEnvironment(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
	}{
		{name: "flag", args: []string{"--port", "0"}},
		{name: "environment", env: "0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.env != "" {
				t.Setenv("GEN_AI_PRV_PORT", test.env)
			}
			command := newServeCommand(testWebFiles, newAppSettings())
			command.SetArgs(test.args)
			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), "server port must be between 1 and 65535") {
				t.Fatalf("Execute() error = %v, want invalid port validation", err)
			}
		})
	}
}

func TestResolveServeAPIKeyUsesViperEnvironment(t *testing.T) {
	t.Setenv(apiKeyName, " serve-key ")

	apiKey, source, err := resolveServeAPIKey(newAppSettings(), true)
	if err != nil {
		t.Fatalf("resolveServeAPIKey() error = %v", err)
	}
	if apiKey != "serve-key" || source != apiKeySourceEnvironment {
		t.Errorf("resolveServeAPIKey() = (%q, %q), want (serve-key, %q)", apiKey, source, apiKeySourceEnvironment)
	}
}

func TestResolveServeAPIKeyRequiresEnvironmentKeyInContainer(t *testing.T) {
	t.Setenv(apiKeyName, "")

	_, _, err := resolveServeAPIKey(newAppSettings(), true)
	if err == nil || !strings.Contains(err.Error(), "must be set in the environment when running in a container") {
		t.Fatalf("resolveServeAPIKey() error = %v, want missing container environment key error", err)
	}
}
