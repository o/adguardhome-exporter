package config

import (
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
