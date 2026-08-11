// main_test.go
package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	// Discard standard log outputs during tests to keep console clean
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// TestLoadConfig_EnvVars tests config loading strictly from environment variables
func TestLoadConfig_EnvVars(t *testing.T) {
	prefix := "TEST_ENV_"

	t.Setenv(prefix+"VAULT_ADDR", "http://localhost:8200")
	t.Setenv(prefix+"VAULT_ROLE_ID", "role-123")
	t.Setenv(prefix+"VAULT_SECRET_ID", "secret-456")
	t.Setenv(prefix+"VAULT_KV_MOUNT", "secret")
	t.Setenv(prefix+"VAULT_DEST_PATH", "certs/test")
	t.Setenv(prefix+"CERT_SOURCE_PATH", "/tmp/certs")

	// Inject the test prefix via flag args
	os.Args = []string{"cmd", "--env-prefix", prefix}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.VaultAddr != "http://localhost:8200" {
		t.Errorf("Expected VaultAddr http://localhost:8200, got %s", cfg.VaultAddr)
	}
	if cfg.RoleID != "role-123" {
		t.Errorf("Expected RoleID role-123, got %s", cfg.RoleID)
	}
}

// TestLoadConfig_Missing verifies that ErrMissingConfig is returned when required settings are omitted
func TestLoadConfig_Missing(t *testing.T) {
	prefix := "TEST_MISSING_"
	t.Setenv(prefix+"VAULT_ADDR", "http://localhost:8200")

	os.Args = []string{"cmd", "--env-prefix", prefix}

	_, err := loadConfig()
	if err == nil {
		t.Fatal("Expected error for missing configuration, got nil")
	}

	if !errors.Is(err, ErrMissingConfig) {
		t.Errorf("Expected ErrMissingConfig, got %v", err)
	}
}

// TestSanitizeAddr tests edge cases for Vault URL formatting
func TestSanitizeAddr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		port     int
		expected string
	}{
		{"Bare hostname", "vault.example.com", 8200, "https://vault.example.com:8200"},
		{"IP without scheme or port", "127.0.0.1", 8200, "https://127.0.0.1:8200"},
		{"Scheme provided, missing port", "http://localhost", 8200, "http://localhost:8200"},
		{"Complete HTTPS address", "https://vault.internal:8200", 8200, "https://vault.internal:8200"},
		{"Trailing slash cleanup", "https://vault.internal:8200/", 8200, "https://vault.internal:8200"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeAddr(tt.input, tt.port)
			if result != tt.expected {
				t.Errorf("sanitizeAddr(%q, %d) = %q; want %q", tt.input, tt.port, result, tt.expected)
			}
		})
	}
}

// TestLoadCertsFromDir tests reading regular files and following symlinks using a temp directory
func TestLoadCertsFromDir(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a regular certificate file
	certFile := filepath.Join(tempDir, "cert.pem")
	if err := os.WriteFile(certFile, []byte("CERT_DATA"), 0644); err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	// 2. Create a target file in an archive directory and symlink to it (simulating Certbot layout)
	archiveDir := filepath.Join(tempDir, "archive")
	if err := os.Mkdir(archiveDir, 0755); err != nil {
		t.Fatalf("Failed to create archive directory: %v", err)
	}

	targetKey := filepath.Join(archiveDir, "privkey1.pem")
	if err := os.WriteFile(targetKey, []byte("KEY_DATA"), 0600); err != nil {
		t.Fatalf("Failed to write key archive file: %v", err)
	}

	symlinkKey := filepath.Join(tempDir, "privkey.pem")
	if err := os.Symlink(targetKey, symlinkKey); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// 3. Read directory contents
	certMap, err := LoadCertsFromDir(tempDir)
	if err != nil {
		t.Fatalf("LoadCertsFromDir failed: %v", err)
	}

	// 4. Assertions
	if len(certMap) != 2 {
		t.Errorf("Expected 2 files in map, got %d", len(certMap))
	}

	if certMap["cert.pem"] != "CERT_DATA" {
		t.Errorf("Expected 'CERT_DATA', got %q", certMap["cert.pem"])
	}

	if certMap["privkey.pem"] != "KEY_DATA" {
		t.Errorf("Symlink resolution failed! Expected 'KEY_DATA', got %q", certMap["privkey.pem"])
	}
}

// TestAuthenticateAppRole verifies HTTP body encoding, request headers, and client_token parsing
func TestAuthenticateAppRole(t *testing.T) {
	// Create mock Vault HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST method, got %s", r.Method)
		}
		if r.URL.Path != "/v1/auth/approle/login" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}

		// Verify incoming body
		var body AppRoleLoginRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Failed to decode mock body: %v", err)
		}

		if body.RoleID != "my-role" || body.SecretID != "my-secret" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Return valid mock client token
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"auth": {"client_token": "mock-token-xyz"}}`))
	}))
	defer ts.Close()

	token, err := AuthenticateAppRole(ts.Client(), ts.URL, "my-role", "my-secret")
	if err != nil {
		t.Fatalf("AuthenticateAppRole failed: %v", err)
	}

	if token != "mock-token-xyz" {
		t.Errorf("Expected 'mock-token-xyz', got %q", token)
	}
}

// TestWriteKV2Secret verifies KV v2 endpoint paths, X-Vault-Token headers, and JSON structure
func TestWriteKV2Secret(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		// Ensure KV v2 URL path convention `/v1/{mount}/data/{destPath}`
		expectedPath := "/v1/secret/data/my-certs"
		if r.URL.Path != expectedPath {
			t.Errorf("Expected path %s, got %s", expectedPath, r.URL.Path)
		}

		// Check Auth Header
		if r.Header.Get("X-Vault-Token") != "test-token" {
			t.Errorf("Expected X-Vault-Token header 'test-token', got %q", r.Header.Get("X-Vault-Token"))
		}

		// Verify body payload structure
		var payload KVv2WriteRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Failed to decode request body: %v", err)
		}

		if payload.Data["tls.crt"] != "CERT_CONTENT" {
			t.Errorf("Expected 'CERT_CONTENT', got %q", payload.Data["tls.crt"])
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	secretData := map[string]string{
		"tls.crt": "CERT_CONTENT",
	}

	err := WriteKV2Secret(ts.Client(), ts.URL, "test-token", "secret", "my-certs", secretData)
	if err != nil {
		t.Fatalf("WriteKV2Secret failed: %v", err)
	}
}

// TestMaskSecrets tests sensitive string masking functionality
func TestMaskSecrets(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"abc", "****"},
		{"1234", "****"},
		{"role-id-12345", "ro*********45"},
	}

	for _, tt := range tests {
		result := maskSecrets(tt.input)
		if result != tt.expected {
			t.Errorf("maskSecrets(%q) = %q; want %q", tt.input, result, tt.expected)
		}
	}
}
