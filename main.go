package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AppRoleLoginRequest represents the payload for /v1/auth/approle/login
type AppRoleLoginRequest struct {
	RoleID   string `json:"role_id"`
	SecretID string `json:"secret_id"`
}

// AppRoleLoginResponse captures the nested client_token returned by Vault
type AppRoleLoginResponse struct {
	Auth struct {
		ClientToken   string `json:"client_token"`
		LeaseDuration int    `json:"lease_duration"`
		Renewable     bool   `json:"renewable"`
	} `json:"auth"`
}

// KVv2WriteRequest represents the wrapped body payload for KV v2 engine writes
type KVv2WriteRequest struct {
	Data map[string]string `json:"data"`
}

// Config struct holds the unified configuration values for the application.
type Config struct {
	SourcePath   string `json:"source_path"`
	DestPath     string `json:"dest_path"`
	RoleID       string `json:"role_id"`
	SecretID     string `json:"secret_id"`
	VaultAddr    string `json:"vault_addr"`
	VaultKVMount string `json:"vault_kv_mount"`
}

var ErrMissingConfig = errors.New("missing one or more required configuration values")

func main() {
	// 1. Parse CLI flags and Environment variables into a single Config struct
	cfg, err := loadConfig()
	if err != nil {
		printHelpMessage()
		log.Fatalf("Configuration error: %v", err)
	}

	log.Printf("Loaded Configuration:")
	log.Printf("  Vault Address:     %s", cfg.VaultAddr)
	log.Printf("  Vault Role ID:     %s", maskSecrets(cfg.RoleID))
	log.Printf("  Vault Secret ID:   %s", maskSecrets(cfg.SecretID))
	log.Printf("  Vault KV Mount:    %s", cfg.VaultKVMount)
	log.Printf("  Vault Dest Path:   %s", cfg.DestPath)
	log.Printf("  Cert Source Path:  %s", cfg.SourcePath)

	// 2. Read local certificate files into memory map[filename]content
	log.Println("Reading certificates from disk...")
	certData, err := LoadCertsFromDir(cfg.SourcePath)
	if err != nil {
		log.Fatalf("Failed to load certificates: %v", err)
	}

	log.Printf("Successfully loaded %d certificate file(s) from disk:", len(certData))
	for fileName := range certData {
		log.Printf("  - %s", fileName)
	}

	// 3. Initialize HTTP client with reasonable timeouts
	httpClient := &http.Client{
		Timeout: 15 * time.Second,
	}

	// 4. Authenticate via AppRole
	log.Println("Authenticating with Vault via AppRole...")
	token, err := AuthenticateAppRole(httpClient, cfg.VaultAddr, cfg.RoleID, cfg.SecretID)
	if err != nil {
		log.Fatalf("Authentication failed: %v", err)
	}
	log.Println("AppRole authentication successful! Obtained client token.")

	// 5. Write certificate payload to Vault KV v2
	log.Printf("Writing certificates to Vault path: %s/data/%s...", cfg.VaultKVMount, cfg.DestPath)
	err = WriteKV2Secret(httpClient, cfg.VaultAddr, token, cfg.VaultKVMount, cfg.DestPath, certData)
	if err != nil {
		log.Fatalf("Failed to write secrets to Vault: %v", err)
	}

	log.Println("Successfully pushed certificates to HashiCorp Vault!")
}

// loadConfig unifies CLI flags and Environment variables into a single Config object
func loadConfig() (*Config, error) {
	// Create a new isolated FlagSet instead of registering globally
	fs := flag.NewFlagSet("push2vault", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Suppress automatic flag error outputs during tests

	var (
		port           = fs.Int("port", 8200, "Vault server port")
		vaultAddr      = fs.String("vault-addr", "", "Vault address")
		vaultRoleID    = fs.String("vault-role-id", "", "Vault role ID")
		vaultSecretID  = fs.String("vault-secret-id", "", "Vault secret ID")
		vaultKVMount   = fs.String("vault-kv-mount", "", "Vault KV mount path")
		vaultDestPath  = fs.String("vault-dest-path", "", "Destination path for secrets")
		certSourcePath = fs.String("cert-source-path", "", "Source path for certificates")
		envPrefix      = fs.String("env-prefix", "P2V_", "Prefix for environment variables to read")
	)

	fs.Usage = printHelpMessage

	// Parse flags from os.Args[1:]
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	prefix := *envPrefix

	// Helper to resolve value: CLI flag > Env variable
	getVal := func(flagVal, envName string) string {
		if flagVal != "" {
			return flagVal
		}
		return os.Getenv(prefix + envName)
	}

	addr := getVal(*vaultAddr, "VAULT_ADDR")
	roleID := getVal(*vaultRoleID, "VAULT_ROLE_ID")
	secretID := getVal(*vaultSecretID, "VAULT_SECRET_ID")
	kvMount := getVal(*vaultKVMount, "VAULT_KV_MOUNT")
	destPath := getVal(*vaultDestPath, "VAULT_DEST_PATH")
	sourcePath := getVal(*certSourcePath, "CERT_SOURCE_PATH")

	if addr == "" || roleID == "" || secretID == "" || kvMount == "" || destPath == "" || sourcePath == "" {
		return nil, ErrMissingConfig
	}

	finalAddr := sanitizeAddr(addr, *port)

	return &Config{
		VaultAddr:    finalAddr,
		RoleID:       roleID,
		SecretID:     secretID,
		VaultKVMount: kvMount,
		DestPath:     destPath,
		SourcePath:   sourcePath,
	}, nil
}

// sanitizeAddr ensures the URL has an HTTP/HTTPS scheme and proper port formatting
func sanitizeAddr(addr string, defaultPort int) string {
	finalAddr := addr
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		finalAddr = "https://" + addr
	}
	if strings.Count(finalAddr, ":") == 1 { // Only the colon from http:// or https://
		finalAddr = fmt.Sprintf("%s:%d", finalAddr, defaultPort)
	}
	return strings.TrimRight(finalAddr, "/")
}

// LoadCertsFromDir reads all files from a directory into a map[filename]fileContent
func LoadCertsFromDir(dirPath string) (map[string]string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source directory: %w", err)
	}

	certData := make(map[string]string)

	for _, entry := range entries {
		// Follow symlinks or regular files (ignore directories)
		fullPath := filepath.Join(dirPath, entry.Name())
		info, err := os.Stat(fullPath) // os.Stat resolves symlinks automatically
		if err != nil {
			return nil, fmt.Errorf("failed to stat file %s: %w", fullPath, err)
		}

		if info.IsDir() {
			continue // Skip subdirectories
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", fullPath, err)
		}

		certData[entry.Name()] = string(content)
	}

	if len(certData) == 0 {
		return nil, fmt.Errorf("no certificate files found in %s", dirPath)
	}

	return certData, nil
}

func printHelpMessage() {
	fmt.Println(`
Usage: ` + os.Args[0] + ` [options]

Options:
  --port <port>                 Vault server port (default: 8200)
  --vault-addr <address>        Vault address
  --vault-role-id <role_id>     Vault role ID
  --vault-secret-id <secret_id> Vault secret ID
  --vault-kv-mount <mount>      Vault KV mount path
  --vault-dest-path <path>      Destination path for secrets
  --cert-source-path <path>     Source path for certificates
  --env-prefix <prefix>         Prefix for env vars (default: MY_APP_)

Environment Variables (using MY_APP_ prefix):
  MY_APP_VAULT_ADDR=https://vault.example.com:8200
  MY_APP_VAULT_ROLE_ID=your-role-id
  MY_APP_VAULT_SECRET_ID=your-secret-id
  MY_APP_VAULT_KV_MOUNT=certs
  MY_APP_VAULT_DEST_PATH=letsencrypt/example.com
  MY_APP_CERT_SOURCE_PATH=/etc/letsencrypt/live/example.com
`)
}

func maskSecrets(o string) string {
	if len(o) == 0 {
		return ""
	}
	if len(o) <= 4 {
		return "****"
	}
	return o[:2] + strings.Repeat("*", len(o)-4) + o[len(o)-2:]
}

func AuthenticateAppRole(client *http.Client, vaultAddr, roleID, secretID string) (string, error) {
	loginURL := fmt.Sprintf("%s/v1/auth/approle/login", vaultAddr)

	payload := AppRoleLoginRequest{
		RoleID:   roleID,
		SecretID: secretID,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal login payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, loginURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var loginResp AppRoleLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return "", fmt.Errorf("failed to decode login response: %w", err)
	}

	if loginResp.Auth.ClientToken == "" {
		return "", fmt.Errorf("vault returned an empty client token")
	}

	return loginResp.Auth.ClientToken, nil
}

func WriteKV2Secret(client *http.Client, vaultAddr, vaultToken, mount, destPath string, secretData map[string]string) error {
	writeURL := fmt.Sprintf("%s/v1/%s/data/%s", vaultAddr, mount, destPath)

	payload := KVv2WriteRequest{
		Data: secretData,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal secret payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, writeURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create write request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Vault-Token", vaultToken)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("write failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
