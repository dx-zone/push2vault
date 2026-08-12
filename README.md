# push2vault (`p2v`)

[![CI & Release](https://github.com/dx-zone/push2vault/actions/workflows/ci.yml/badge.svg)](https://github.com/dx-zone/push2vault/actions)
![Go Version](https://img.shields.io/badge/go-1.26%2B-00ADD8.svg?style=flat-square&logo=go)
[![Latest Release](https://img.shields.io/github/v/release/dx-zone/push2vault?style=flat-square&color=blue)](https://github.com/dx-zone/push2vault/releases/latest)
![License](https://img.shields.io/badge/license-MIT-blue.svg?style=flat-square)

`push2vault` is a small Go CLI tool for securely copying certificates and other secret files from a local machine into HashiCorp Vault.

It was primarily built to work with certificates issued and managed by **Certbot / Let's Encrypt**. The tool reads certificate files from the local Certbot directory, authenticates to Vault using **AppRole**, and stores the files in a **KV v2** secret path.

This makes it useful for environments where certificates are renewed on one server but need to be securely distributed to other applications or systems through Vault. It can also be used as a **Certbot deploy hook**, so newly issued or renewed certificates are automatically pushed to Vault.

The tool is provided as a single, lightweight Go binary with no external runtime dependencies.

---

## Features

- **Zero External Dependencies**: Built entirely using Go's standard library (`net/http`, `encoding/json`, `flag`, `os`).
- **AppRole Authentication**: Automatically exchanges `RoleID` and `SecretID` for a temporary `X-Vault-Token`.
- **KV Version 2 Engine**: Writes data using modern Vault key-value payload formatting (`/v1/{mount}/data/{path}`).
- **Certbot Symlink Support**: Resolves symlinked certificate files cleanly (e.g., `/etc/letsencrypt/live/`).
- **Flexible Configuration**: Reads settings from CLI flags or environment variables with clean precedence (`CLI > Env`).
- **Log Sanitization**: Automatically masks sensitive string values (`RoleID` and `SecretID`) in output logs.

---

## Installation & Build

### Option 1: Quick Download (Pre-compiled Binaries)

Pre-compiled binaries for Linux and macOS (x86_64 / ARM64) are available on the [Releases Page](https://github.com/dx-zone/push2vault/releases/latest).

```bash
# Download latest Linux x86_64 binary
curl -sL https://github.com/dx-zone/push2vault/releases/latest/download/push2vault-linux-amd64 -o push2vault

# Make executable and move to system path
chmod +x push2vault
sudo mv push2vault /usr/local/bin/
```

### Option 2: Build from Source

#### Prerequisites

- Go 1.22+ installed on your system.

```bash
# Clone using your SSH host alias
git clone git@github-dxzone-push2vault:dx-zone/push2vault.git
cd push2vault

# Run unit tests
go test -v ./...

# Build binary
go build -o push2vault main.go
```

## Configuration

`push2vault` can be configured using command-line arguments or environment variables. Command-line flags always take precedence over environment variables.

### Environment Variables

Default environment variable prefix is `P2V_` (customizable via `--env-prefix`).

| **Variable Name**      | **Description**                         | **Example**                                                  |
| ---------------------- | --------------------------------------- | ------------------------------------------------------------ |
| `P2V_VAULT_ADDR`       | Base URL of your Vault instance         | `[https://vault.example.com:8200](https://vault.example.com:8200)` |
| `P2V_VAULT_ROLE_ID`    | AppRole Role ID                         | `a1b2c3d4-e5f6-...`                                          |
| `P2V_VAULT_SECRET_ID`  | AppRole Secret ID                       | `z9y8x7w6-v5u4-...`                                          |
| `P2V_VAULT_KV_MOUNT`   | KV engine mount name                    | `secret` or `certs`                                          |
| `P2V_VAULT_DEST_PATH`  | Destination secret path in Vault        | `letsencrypt/example.com`                                    |
| `P2V_CERT_SOURCE_PATH` | Local directory containing certificates | `/etc/letsencrypt/live/example.com`                          |

## Usage Examples

### 1. Using Environment Variables (Recommended for Automation)

```bash
export P2V_VAULT_ADDR="https://vault.internal.net:8200"
export P2V_VAULT_ROLE_ID="your-approle-role-id"
export P2V_VAULT_SECRET_ID="your-approle-secret-id"
export P2V_VAULT_KV_MOUNT="certs"
export P2V_VAULT_DEST_PATH="domains/example.com"
export P2V_CERT_SOURCE_PATH="/etc/letsencrypt/live/example.com"

./push2vault
```

### 2. Using CLI Flags

```bash
./push2vault \
  --vault-addr="https://vault.internal.net:8200" \
  --vault-role-id="your-approle-role-id" \
  --vault-secret-id="your-approle-secret-id" \
  --vault-kv-mount="certs" \
  --vault-dest-path="domains/example.com" \
  --cert-source-path="/etc/letsencrypt/live/example.com"
```

### 3. Certbot Deploy Hook Integration

To automatically sync certificates to Vault upon renewal, place a script in Certbot's renewal hook directory:

`/etc/letsencrypt/renewal-hooks/deploy/push2vault.sh`:

```bash
#!/usr/bin/env bash
set -e

export P2V_VAULT_ADDR="https://vault.example.com:8200"
export P2V_VAULT_ROLE_ID="your-approle-role-id"
export P2V_VAULT_SECRET_ID="your-approle-secret-id"
export P2V_VAULT_KV_MOUNT="certs"
export P2V_VAULT_DEST_PATH="domains/${RENEWED_LINEAGE##*/}"
export P2V_CERT_SOURCE_PATH="${RENEWED_LINEAGE}"

/usr/local/bin/push2vault
```

Make it executable:

```
chmod +x /etc/letsencrypt/renewal-hooks/deploy/push2vault.sh
```

## Vault Policy Requirement

Your AppRole's policy must have `create` and `update` permissions on the targeted KV v2 path.

Example policy:

```Terraform
path "certs/data/domains/*" {
  capabilities = ["create", "update"]
}
```

## Testing

Run the full suite of unit tests, including mock HTTP server validations and symlink resolution checks:

```bash
go test -v ./...
```

For a complete step-by-step guide on setting up policies, AppRole authentication, and testing locally against Vault, see [VAULT_SETUP.md](VAULT_SETUP.md).

## License

Distributed under the MIT License. See [LICENSE](https://www.google.com/search?q=LICENSE) for details.