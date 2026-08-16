package config

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvironmentTarget(t *testing.T) {
	t.Setenv("ADGUARD_TARGET_1_NAME", "home")
	t.Setenv("ADGUARD_TARGET_1_URL", "http://adguardhome:3000/")
	t.Setenv("ADGUARD_TARGET_1_USERNAME", "admin")
	t.Setenv("ADGUARD_TARGET_1_PASSWORD", "password")

	config, err := Load("")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	target := config.Targets[0]
	if target.URL != "http://adguardhome:3000" {
		t.Errorf("URL = %q, want trailing slash removed", target.URL)
	}
	if target.Name != "home" {
		t.Errorf("Name = %q, want home", target.Name)
	}
}

func TestLoadRejectsInvalidEnvironmentTargets(t *testing.T) {
	tests := []struct {
		name   string
		setEnv func(*testing.T)
		want   string
	}{
		{
			name: "non-contiguous indexes",
			setEnv: func(t *testing.T) {
				t.Setenv("ADGUARD_TARGET_2_NAME", "second")
				t.Setenv("ADGUARD_TARGET_2_URL", "http://second.example")
			},
			want: "indexes must be contiguous",
		},
		{
			name: "unsupported URL scheme",
			setEnv: func(t *testing.T) {
				t.Setenv("ADGUARD_TARGET_1_NAME", "home")
				t.Setenv("ADGUARD_TARGET_1_URL", "ftp://adguard.example")
			},
			want: "must use HTTP or HTTPS",
		},
		{
			name: "URL contains credentials",
			setEnv: func(t *testing.T) {
				t.Setenv("ADGUARD_TARGET_1_NAME", "home")
				t.Setenv("ADGUARD_TARGET_1_URL", "https://admin:secret@adguard.example")
			},
			want: "invalid URL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ADGUARD_TARGET_1_NAME", "")
			t.Setenv("ADGUARD_TARGET_1_URL", "")
			t.Setenv("ADGUARD_TARGET_2_NAME", "")
			t.Setenv("ADGUARD_TARGET_2_URL", "")
			test.setEnv(t)

			_, err := Load("")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadYAMLRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{
			name:     "unknown field",
			contents: "targets:\n  - name: home\n    url: http://adguardhome:3000\nunknown: true\n",
			want:     "field unknown not found",
		},
		{
			name:     "duplicate names",
			contents: "targets:\n  - name: home\n    url: http://one.example\n  - name: home\n    url: http://two.example\n",
			want:     "duplicate target name",
		},
		{
			name:     "invalid timeout",
			contents: "web:\n  timeout: 0s\ntargets:\n  - name: home\n    url: http://adguardhome:3000\n",
			want:     "timeout must be positive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTLSConfig(t *testing.T) {
	t.Run("secure defaults return nil", func(t *testing.T) {
		result, err := TLSConfig(Target{})
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			t.Errorf("TLS config = %#v, want nil", result)
		}
	})

	t.Run("insecure verification is explicit", func(t *testing.T) {
		result, err := TLSConfig(Target{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		if !result.InsecureSkipVerify || result.MinVersion != tls.VersionTLS12 {
			t.Errorf("unexpected TLS config: %#v", result)
		}
	})

	t.Run("invalid CA file is rejected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := TLSConfig(Target{CAFile: path}); err == nil {
			t.Fatal("TLSConfig returned no error")
		}
	})
}

func TestLoadSecretFiles(t *testing.T) {
	directory := t.TempDir()
	usernameFile := filepath.Join(directory, "username")
	passwordFile := filepath.Join(directory, "password")

	if err := os.WriteFile(usernameFile, []byte("admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte("secret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ADGUARD_TARGET_1_NAME", "home")
	t.Setenv("ADGUARD_TARGET_1_URL", "https://adguard.example")
	t.Setenv("ADGUARD_TARGET_1_USERNAME_FILE", usernameFile)
	t.Setenv("ADGUARD_TARGET_1_PASSWORD_FILE", passwordFile)

	config, err := Load("")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if config.Targets[0].Username != "admin" {
		t.Errorf("Username = %q, want admin", config.Targets[0].Username)
	}
	if config.Targets[0].Password != "secret" {
		t.Errorf("Password was not trimmed correctly")
	}
}

func TestLoadYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := `
web:
  listen_address: ":9999"
  timeout: 3s
targets:
  - name: home
    url: http://adguardhome:3000
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if config.Web.Timeout.String() != "3s" {
		t.Errorf("Timeout = %s, want 3s", config.Web.Timeout)
	}
}

func TestLoadRejectsPartialCredentials(t *testing.T) {
	t.Setenv("ADGUARD_TARGET_1_NAME", "home")
	t.Setenv("ADGUARD_TARGET_1_URL", "http://adguardhome:3000")
	t.Setenv("ADGUARD_TARGET_1_USERNAME", "admin")

	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "both username and password") {
		t.Fatalf("Load error = %v, want partial credentials error", err)
	}
}

func TestLoadCommonEnvironment(t *testing.T) {
	t.Setenv("ADGUARD_TARGET_1_NAME", "home")
	t.Setenv("ADGUARD_TARGET_1_URL", "https://adguard.example")
	t.Setenv("ADGUARD_EXPORTER_LISTEN_ADDRESS", "127.0.0.1:9618")
	t.Setenv("ADGUARD_EXPORTER_TIMEOUT", "3s")
	t.Setenv("ADGUARD_EXPORTER_POLL_INTERVAL", "20s")
	t.Setenv("ADGUARD_EXPORTER_LOG_LEVEL", "debug")
	t.Setenv("ADGUARD_EXPORTER_LOG_FORMAT", "json")
	t.Setenv("ADGUARD_EXPORTER_TOP_ENABLED", "true")
	t.Setenv("ADGUARD_EXPORTER_TOP_LIMIT", "20")

	config, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if config.Web.ListenAddress != "127.0.0.1:9618" || config.Web.Timeout.String() != "3s" {
		t.Errorf("unexpected web configuration: %#v", config.Web)
	}
	if config.Log.Level != "debug" || config.Log.Format != "json" {
		t.Errorf("unexpected log configuration: %#v", config.Log)
	}
	if !config.Collectors.TopEnabled || config.Collectors.TopLimit != 20 || config.Collectors.PollInterval.String() != "20s" {
		t.Errorf("unexpected collector configuration: %#v", config.Collectors)
	}
}

func TestValidateCommonConfig(t *testing.T) {
	tests := []struct {
		name   string
		update func(*Config)
		want   string
	}{
		{name: "empty address", update: func(config *Config) { config.Web.ListenAddress = "" }, want: "listen_address"},
		{name: "invalid top limit", update: func(config *Config) { config.Collectors.TopLimit = 101 }, want: "top_limit"},
		{name: "invalid poll interval", update: func(config *Config) { config.Collectors.PollInterval = 0 }, want: "poll_interval"},
		{name: "invalid log level", update: func(config *Config) { config.Log.Level = "trace" }, want: "log.level"},
		{name: "invalid log format", update: func(config *Config) { config.Log.Format = "console" }, want: "log.format"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Default()
			test.update(&config)
			err := validateCommonConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadSecretRejectsConflictingSources(t *testing.T) {
	value := "admin"
	path := "/run/secrets/admin"
	if err := readSecret(&value, &path, "username", "home"); err == nil {
		t.Fatal("readSecret returned no error")
	}
}
