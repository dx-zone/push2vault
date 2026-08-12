# HashiCorp Vault Setup Guide for push2vault

This guide walks through configuring HashiCorp Vault for use with `push2vault`, including defining access policies, configuring AppRole authentication, mounting a KV v2 secrets engine, and executing verification tests.

### Choose Your Setup & Testing Approach

To make integration as flexible as possible, this guide offers multiple paths for provisioning and testing your setup. **Pick the approach that best suits your needs:**

* **Automated via Makefile (`make`)** *(Recommended for local dev)*  
  The quickest way to get up and running. Runs convenience wrappers over the harness script to provision, test, or tear down Vault with single commands.
* **Direct Script Execution (`vault-manager.sh`)** *(Best for CI/CD pipelines & custom flags)*  
  Execute the automated harness script directly to pass explicit parameters (such as `--vault-addr`) or integrate directly into automated build steps.
* **Manual Step-by-Step Configuration** *(Best for existing infrastructure & security reviews)*  
  Walk through each individual `vault` CLI command step-by-step. Ideal if you are deploying to an existing shared Vault cluster, applying custom policy paths, or auditing what changes are made.
---

## Prerequisites

Before configuring Vault, ensure you have:

* **HashiCorp Vault Server**: An operational, unsealed Vault instance (v1.12+ recommended).
* **Vault CLI**: Installed locally and accessible in your `$PATH`.
* **Admin Access**: A token (`VAULT_TOKEN`) with administrative privileges to manage policies, enable auth methods, and mount secrets engines.
* **Engine & Auth Path Availability**: This guide assumes the path `certs` (KV v2) and the `approle` authentication method are available in your Vault cluster. If these paths are already in use, adjust the commands accordingly.

> **Note**: This documentation uses `https://vault.example.com:8200` as a placeholder. Replace it with your actual Vault cluster URL.

Set your administrative connection environment variables:

```bash
export VAULT_ADDR="vault.example.com:8200"
export VAULT_TOKEN="your-admin-root-token"
```

---

### Quick Setup via Makefile (Automated)

The repository includes an automated harness script (`scripts/vault-manager.sh`) that provisions the policy, enables the KV-v2 engine and AppRole authentication, configures the role, and outputs the required `P2V_*` environment variables.

> **Behavior Note**: If `VAULT_ADDR` is not set, commands will fall back to `http://127.0.0.1:8200` or fail. You can either export `VAULT_ADDR` in your shell once, or append `--vault-addr <URL>` to any individual direct script command.

#### 1. Export your Vault server address once:
```bash
export VAULT_ADDR="https://vault.example.com:8200"
```

#### 2. Run commands using `make` (optional if you want to run the script directly):

```bash
# Provision environment
make dev-vault

# Run end-to-end integration test
make dev-vault-test

# Clean up all test resources
make dev-vault-clean
```

#### Alternative: Running the script directly

If you prefer not using `make`, execute the script directly:

```bash
# Provision environment
./scripts/vault-manager.sh setup
# Or specify your Vault's URL explicitly:
# ./scripts/vault-manager.sh setup --vault-addr [https://vault.example.com:8200](https://vault.example.com:8200)

# Run end-to-end integration test
./scripts/vault-manager.sh test
# Or specify your Vault's URL explicitly:
# ./scripts/vault-manager.sh test --vault-addr [https://vault.example.com:8200](https://vault.example.com:8200)

# Clean up all test resources
./scripts/vault-manager.sh cleanup
# Or specify your Vault's URL explicitly:
# ./scripts/vault-manager.sh cleanup --vault-addr [https://vault.example.com:8200](https://vault.example.com:8200)
```

---

### Manual Step-by-Step Configuration

If you prefer to configure Vault manually or integrate `push2vault` into existing infrastructure, ensure your local shell has admin access (`export VAULT_ADDR="..."` and `export VAULT_TOKEN="..."`), then follow these steps.

#### 0. Clone the Repository Clone the `push2vault` repository

Clone the `push2vault` repository and step into the project directory:

```bash
# Clone the repository
git clone https://github.com/dx-zone/push2vault.git

# Step into the push2vault directory
cd push2vault
```

#### 1. Create the Vault Policy
Define a policy named `push2vault-policy` that grants `create` and `update` capabilities on the KV-v2 data path:

```bash
echo "Writing policy 'push2vault-policy'..."
vault policy write push2vault-policy - >/dev/null <<EOF
path "certs/data/*" {
  capabilities = ["create", "update"]
}
EOF
```

### 2. Enable & Configure KV v2 Engine:

Mount the KV v2 secrets engine at `certs`:

```bash
vault secrets enable -path=certs kv-v2
```

### 3. Enable & Setup AppRole Authentication:

Enable AppRole and bind `push2vault-policy` to the role:

```bash
vault auth enable approle 2>/dev/null || true

vault write auth/approle/role/push2vault-role \
    secret_id_ttl=0 \
    token_ttl=1h \
    token_max_ttl=4h \
    policies="push2vault-policy"
```

### 4. Retrieve Role ID and Secret ID:

Fetch the `Role ID` and generate a `Secret ID` for testing:

```bash
# Read Role ID
export P2V_VAULT_ROLE_ID="$(vault read -field=role_id auth/approle/role/push2vault-role/role-id)"

# Generate Secret ID
export P2V_VAULT_SECRET_ID="$(vault write -f -field=secret_id auth/approle/role/push2vault-role/secret-id)"
```

### 5. Test push2vault CLI:

Create a mock cert and test the upload using the generated credentials:

```bash
# Create temporary mock cert
mkdir -p /tmp/mock-cert
cat <<EOF> /tmp/mock-cert/cert.pem
-----BEGIN CERTIFICATE-----
mock_cert_data
-----END CERTIFICATE-----
EOF

# Export variables for push2vault
export P2V_VAULT_ADDR="vault.example.com:8200"
export P2V_VAULT_KV_MOUNT="certs"
export P2V_VAULT_DEST_PATH="domains/test.local"
export P2V_CERT_SOURCE_PATH="/tmp/mock-cert"

```

### 6. Build & Run

Build the binary and execute `push2vault`:

```bash
# Build binary
make build

# Execute CLI
./bin/push2vault --env-prefix P2V_
```

## Verifying & Cleaning Up

### Verify Written Secrets

Confirm that the payload was written to Vault's KV v2 engine:

```bash
vault kv get certs/domains/test.local
```

### Clean Up Test Data

Delete the test secret from Vault and remove local mock files:

```bash
# 1. Delete complete secret metadata and key versions from Vault
vault kv metadata delete certs/domains/test.local

# 2. Remove local mock directory
rm -rf /tmp/mock-cert

# 3. Delete the AppRole role
vault delete auth/approle/role/push2vault-role

# 4. Disable AppRole auth method (Optional: skip if used elsewhere)
vault auth disable approle

# 5. Delete the policy
vault policy delete push2vault-policy

# 6. Disable the KV mount (Optional: skip if used elsewhere)
vault secrets disable certs

# 7. Clear environment variables from current shell session
unset P2V_VAULT_ADDR P2V_VAULT_ROLE_ID P2V_VAULT_SECRET_ID P2V_VAULT_KV_MOUNT P2V_VAULT_DEST_PATH P2V_CERT_SOURCE_PATH
```

---

## Next Steps

Now that you have verified Vault configuration and authenticated via AppRole, you can integrate `push2vault` into your workflows:

* **Production Deployment**: Review the [push2vault Usage Documentation](./README.md#usage) to configure environment variables or CLI flags in your deployment pipelines.
* **Troubleshooting**: If you encounter authentication or permission issues, verify policy capability paths against Vault audit logs (`vault read sys/internal/ui/mounts`).
* **Support & Feedback**: File issues or feature requests on the [push2vault GitHub repository](https://github.com/dx-zone/push2vault/issues).