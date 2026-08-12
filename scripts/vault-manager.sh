#!/usr/bin/env bash
set -euo pipefail

# Default Configuration
VAULT_ADDR="${VAULT_ADDR:-https://vault.example.com:8200}"
POLICY_NAME="push2vault-policy"
ROLE_NAME="push2vault-role"
KV_MOUNT="certs"
MOCK_DIR="/tmp/mock-cert"
DEST_PATH="domains/test.local"

# Print Usage
usage() {
    cat <<EOF
Usage: $0 [setup|test|cleanup] [options]

Commands:
  setup    Configure Vault policies, KV mount, and AppRole credentials.
  test     Execute full end-to-end build, mock generation, push, and verification.
  cleanup  Teardown test secret, AppRole, policy, KV mount, and temporary files.

Options:
  -a, --vault-addr <url>   Override Vault URL (Default: $VAULT_ADDR)
  -h, --help               Display this help message.
EOF
    exit 1
}

# Parse Optional Flags
COMMAND=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        setup|test|cleanup)
            COMMAND="$1"
            shift
            ;;
        -a|--vault-addr)
            VAULT_ADDR="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Error: Unknown argument '$1'" >&2
            usage
            ;;
    esac
done

if [[ -z "$COMMAND" ]]; then
    usage
fi

# Authenticate & Validate Environment
authenticate() {
    export VAULT_ADDR="$VAULT_ADDR"
    if [[ -z "${VAULT_TOKEN:-}" ]]; then
        if sudo test -f /opt/vault/secrets/vault-root-token.txt; then
            export VAULT_TOKEN="$(sudo cat /opt/vault/secrets/vault-root-token.txt)"
        else
            echo "Error: VAULT_TOKEN not set and /opt/vault/secrets/vault-root-token.txt not found." >&2
            exit 1
        fi
    fi
}

# ---------------------------------------------------------
# SETUP LOGIC
# ---------------------------------------------------------
setup() {
    authenticate
    echo "==> [1/4] Configuring Policy: $POLICY_NAME"
    vault policy write "$POLICY_NAME" - <<EOF
path "$KV_MOUNT/data/*" {
  capabilities = ["create", "update"]
}
EOF

    echo "==> [2/4] Ensuring KV-v2 engine at '$KV_MOUNT'..."
    if ! vault secrets list -format=json | grep -q "\"$KV_MOUNT/\":"; then
        vault secrets enable -path="$KV_MOUNT" kv-v2
    else
        echo "    (Already enabled)"
    fi

    echo "==> [3/4] Configuring AppRole..."
    if ! vault auth list -format=json | grep -q '"approle/":'; then
        vault auth enable approle
    fi
    vault write auth/approle/role/"$ROLE_NAME" \
        secret_id_ttl=0 \
        token_ttl=1h \
        token_max_ttl=4h \
        policies="$POLICY_NAME" >/dev/null

    echo "==> [4/4] Retrieving Credentials..."
    ROLE_ID=$(vault read -field=role_id auth/approle/role/"$ROLE_NAME"/role-id)
    SECRET_ID=$(vault write -f -field=secret_id auth/approle/role/"$ROLE_NAME"/secret-id)

    echo -e "\n--- SETUP COMPLETE. Admin Audit Log: ---"
    echo "Vault Address: $VAULT_ADDR"
    echo -e "\nPolicy Definition:"
    vault policy read "$POLICY_NAME"
    echo -e "\nAuth Method Configuration:"
    vault read auth/approle/role/"$ROLE_NAME"
    echo -e "\nSecrets Engine Status:"
    vault secrets list -detailed | grep "$KV_MOUNT" || true
    
    echo -e "\n========================================================"
    echo " Generated Environment Variables for manual use:"
    echo " export P2V_VAULT_ADDR=\"$VAULT_ADDR\""
    echo " export P2V_VAULT_ROLE_ID=\"$ROLE_ID\""
    echo " export P2V_VAULT_SECRET_ID=\"$SECRET_ID\""
    echo " export P2V_VAULT_KV_MOUNT=\"$KV_MOUNT\""
    echo " export P2V_VAULT_DEST_PATH=\"$DEST_PATH\""
    echo " export P2V_CERT_SOURCE_PATH=\"$MOCK_DIR\""
    echo "========================================================"
}

# ---------------------------------------------------------
# END-TO-END TEST LOGIC
# ---------------------------------------------------------
run_test() {
    authenticate
    echo "==> Starting End-to-End Test..."

    echo "==> [1/5] Running Setup..."
    setup

    echo "==> [2/5] Creating Mock Certificates at $MOCK_DIR..."
    mkdir -p "$MOCK_DIR"
    echo "-----BEGIN CERTIFICATE-----" > "$MOCK_DIR/cert.pem"
    echo "mock_cert_data" >> "$MOCK_DIR/cert.pem"
    echo "-----END CERTIFICATE-----" >> "$MOCK_DIR/cert.pem"

    echo "==> [3/5] Building Go Binary..."
    make build

    echo "==> [4/5] Executing push2vault CLI..."
    export P2V_VAULT_ADDR="$VAULT_ADDR"
    export P2V_VAULT_ROLE_ID="$(vault read -field=role_id auth/approle/role/$ROLE_NAME/role-id)"
    export P2V_VAULT_SECRET_ID="$(vault write -f -field=secret_id auth/approle/role/$ROLE_NAME/secret-id)"
    export P2V_VAULT_KV_MOUNT="$KV_MOUNT"
    export P2V_VAULT_DEST_PATH="$DEST_PATH"
    export P2V_CERT_SOURCE_PATH="$MOCK_DIR"

    ./bin/push2vault --env-prefix P2V_

    echo "==> [5/5] Verifying Secret Payload in Vault..."
    vault kv get "$KV_MOUNT/$DEST_PATH"

    echo -e "\n==> End-to-End Test Completed Successfully!"
}

# ---------------------------------------------------------
# CLEANUP LOGIC
# ---------------------------------------------------------
cleanup() {
    authenticate
    echo "==> Cleaning up Vault configuration and local files..."
    
    set +e
    echo "--> Destroying secret version..."
    vault kv destroy -versions=1 "$KV_MOUNT/$DEST_PATH" >/dev/null 2>&1
    
    echo "--> Removing mock directories..."
    rm -rf "$MOCK_DIR"

    echo "--> Revoking AppRole role..."
    vault delete auth/approle/role/"$ROLE_NAME" >/dev/null 2>&1

    echo "--> Disabling AppRole auth..."
    vault auth disable approle >/dev/null 2>&1

    echo "--> Deleting policy..."
    vault policy delete "$POLICY_NAME" >/dev/null 2>&1

    echo "--> Disabling secrets engine mount..."
    vault secrets disable "$KV_MOUNT" >/dev/null 2>&1

    echo "--> Unsetting local environment variables..."
    unset P2V_VAULT_ADDR P2V_VAULT_ROLE_ID P2V_VAULT_SECRET_ID P2V_VAULT_KV_MOUNT P2V_VAULT_DEST_PATH P2V_CERT_SOURCE_PATH
    set -e
    
    echo -e "\n--- CLEANUP COMPLETE. Admin Audit Verification: ---"
    echo "    - AppRole Auth: $(vault auth list -format=json | grep -q '"approle/":' && echo "Active" || echo "Disabled/Removed")"
    echo "    - Policy:      $(vault policy list | grep -q "$POLICY_NAME" && echo "Present" || echo "Cleaned")"
    echo "    - Engine:      $(vault secrets list -format=json | grep -q "\"$KV_MOUNT/\":" && echo "Present" || echo "Cleaned")"
    echo "    - Mock Dir:    $([[ -d "$MOCK_DIR" ]] && echo "Present" || echo "Removed")"
}

# ---------------------------------------------------------
# EXECUTION ROUTER
# ---------------------------------------------------------
case "$COMMAND" in
    setup)   setup ;;
    test)    run_test ;;
    cleanup) cleanup ;;
    *)       usage ;;
esac