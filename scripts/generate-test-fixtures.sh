#!/bin/bash
# Generate test fixtures for dbt tests
# Run this once, commit the results
#
# This script generates pre-built test artifacts to avoid slow gomason compilation
# during tests. The artifacts are signed with a test GPG key.
#
# IMPORTANT: These fixtures use static version numbers (1.0.0, 2.0.0, 3.0.0)
# that never change. They are decoupled from actual release versions.
# Tests use these to verify the "find latest" logic works correctly.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
FIXTURE_DIR="$PROJECT_ROOT/pkg/dbt/testfixtures"
GPG_DIR="$FIXTURE_DIR/gpg"
REPO_DIR="$FIXTURE_DIR/repo"

# Static test fixture versions - these never change
OLD_VERSION="1.0.0"
NEW_VERSION="2.0.0"
LATEST_VERSION="3.0.0"

# Create directories
mkdir -p "$GPG_DIR"
mkdir -p "$REPO_DIR/dbt/$OLD_VERSION/linux/amd64"
mkdir -p "$REPO_DIR/dbt/$NEW_VERSION/linux/amd64"
mkdir -p "$REPO_DIR/dbt/$LATEST_VERSION/linux/amd64"
mkdir -p "$REPO_DIR/dbt-tools/catalog/$OLD_VERSION/linux/amd64"
mkdir -p "$REPO_DIR/dbt-tools/catalog/$NEW_VERSION/linux/amd64"
mkdir -p "$REPO_DIR/dbt-tools/catalog/$LATEST_VERSION/linux/amd64"

# Create a temporary GNUPGHOME to avoid polluting user's keyring
export GNUPGHOME=$(mktemp -d)
chmod 700 "$GNUPGHOME"

cleanup() {
    rm -rf "$GNUPGHOME"
}
trap cleanup EXIT

echo "=== Generating GPG test keys ==="

# Generate GPG key batch file
cat > "$GNUPGHOME/keygen.batch" <<EOF
%echo Generating test key for dbt
%no-protection
%transient-key
Key-Type: RSA
Key-Length: 2048
Subkey-Type: RSA
Subkey-Length: 2048
Name-Real: DBT Test Key
Name-Comment: Test signing key
Name-Email: tester@nikogura.com
Expire-Date: 0
%commit
%echo Done
EOF

# Generate the key
gpg --batch --gen-key "$GNUPGHOME/keygen.batch"

# Export public key
gpg --armor --export tester@nikogura.com > "$GPG_DIR/public-key.asc"
cp "$GPG_DIR/public-key.asc" "$REPO_DIR/dbt/truststore"

echo "=== Generating dummy binaries ==="

# generate_attestations writes and signs the SLSA provenance attestation (.att.json) and
# the CycloneDX SBOM attestation (.sbom.json) for a binary, following the same
# convention as the checksum (.sha256) and signature (.asc) files. These are the artifacts
# dbt's third verification leg consumes.
generate_attestations() {
    local binary="$1"
    local name
    name=$(basename "$binary")
    local checksum
    checksum=$(sha256sum "$binary" | cut -d' ' -f1)

    local att="$binary.att.json"
    cat > "$att" <<EOF
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {
      "name": "$name",
      "digest": {"sha256": "$checksum"}
    }
  ],
  "predicateType": "https://slsa.dev/provenance/v1",
  "predicate": {
    "buildDefinition": {
      "buildType": "https://gomason.nikogura.com/build/v1",
      "externalParameters": {
        "package": "github.com/nikogura/dynamic-binary-toolkit",
        "version": "test"
      },
      "resolvedDependencies": []
    },
    "runDetails": {
      "builder": {"id": "tester@nikogura.com"}
    }
  }
}
EOF
    gpg --batch --yes --armor --detach-sign --local-user tester@nikogura.com "$att"

    local sbom="$binary.sbom.json"
    cat > "$sbom" <<EOF
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {
      "name": "$name",
      "digest": {"sha256": "$checksum"}
    }
  ],
  "predicateType": "https://cyclonedx.org/bom",
  "predicate": {
    "bomFormat": "CycloneDX",
    "specVersion": "1.6",
    "version": 1,
    "components": [
      {
        "type": "library",
        "name": "github.com/spf13/cobra",
        "version": "v1.8.1",
        "purl": "pkg:golang/github.com/spf13/cobra@v1.8.1"
      }
    ]
  }
}
EOF
    gpg --batch --yes --armor --detach-sign --local-user tester@nikogura.com "$sbom"

    echo "  Created attestations for $name ($checksum)"
}

# Generate dbt binaries for each test version
for VERSION in "$OLD_VERSION" "$NEW_VERSION" "$LATEST_VERSION"; do
    BINARY="$REPO_DIR/dbt/$VERSION/linux/amd64/dbt"

    # Create a unique binary for each version (just text, tests only check checksums)
    cat > "$BINARY" <<EOF
#!/bin/bash
# DBT Test Binary v$VERSION
# This is a test fixture, not a real executable
echo "dbt version $VERSION"
EOF
    chmod 755 "$BINARY"

    # Generate checksum
    sha256sum "$BINARY" | cut -d' ' -f1 > "$BINARY.sha256"

    # Sign the binary
    gpg --batch --yes --armor --detach-sign --local-user tester@nikogura.com "$BINARY"

    echo "Created dbt $VERSION binary with checksum and signature"

    # Only the latest version carries attestations: older versions double as the
    # "published without attestations" fixtures for dbt's policy tests.
    if [ "$VERSION" = "$LATEST_VERSION" ]; then
        generate_attestations "$BINARY"
    fi
done

# Generate catalog binaries for each test version
for VERSION in "$OLD_VERSION" "$NEW_VERSION" "$LATEST_VERSION"; do
    CATALOG_DIR="$REPO_DIR/dbt-tools/catalog/$VERSION"
    BINARY="$CATALOG_DIR/linux/amd64/catalog"
    DESC="$CATALOG_DIR/description.txt"

    # Create catalog description (no trailing newline)
    printf "Tool for showing available DBT tools." > "$DESC"

    # Sign description
    gpg --batch --yes --armor --detach-sign --local-user tester@nikogura.com "$DESC"

    # Create catalog binary
    cat > "$BINARY" <<EOF
#!/bin/bash
# Catalog Test Binary v$VERSION
# This is a test fixture, not a real executable
echo "catalog version $VERSION"
EOF
    chmod 755 "$BINARY"

    # Generate checksum
    sha256sum "$BINARY" | cut -d' ' -f1 > "$BINARY.sha256"

    # Sign the binary
    gpg --batch --yes --armor --detach-sign --local-user tester@nikogura.com "$BINARY"

    echo "Created catalog $VERSION binary with checksum and signature"

    # Only the latest version carries attestations (see dbt loop above).
    if [ "$VERSION" = "$LATEST_VERSION" ]; then
        generate_attestations "$BINARY"
    fi
done

# Generate install scripts
echo "=== Generating install scripts ==="

# install_dbt.sh
cat > "$REPO_DIR/dbt/install_dbt.sh" <<'EOF'
#!/bin/bash
# DBT Installation Script (Test Fixture)
echo "This is a test fixture install script"
exit 0
EOF
chmod 755 "$REPO_DIR/dbt/install_dbt.sh"
sha256sum "$REPO_DIR/dbt/install_dbt.sh" | cut -d' ' -f1 > "$REPO_DIR/dbt/install_dbt.sh.sha256"
gpg --batch --yes --armor --detach-sign --local-user tester@nikogura.com "$REPO_DIR/dbt/install_dbt.sh"

# install_dbt_mac_keychain.sh
cat > "$REPO_DIR/dbt/install_dbt_mac_keychain.sh" <<'EOF'
#!/bin/bash
# DBT Mac Keychain Installation Script (Test Fixture)
echo "This is a test fixture install script for macOS keychain"
exit 0
EOF
chmod 755 "$REPO_DIR/dbt/install_dbt_mac_keychain.sh"
sha256sum "$REPO_DIR/dbt/install_dbt_mac_keychain.sh" | cut -d' ' -f1 > "$REPO_DIR/dbt/install_dbt_mac_keychain.sh.sha256"
gpg --batch --yes --armor --detach-sign --local-user tester@nikogura.com "$REPO_DIR/dbt/install_dbt_mac_keychain.sh"

echo "=== Test fixtures generated successfully ==="
echo ""
echo "Test fixture versions (static, never change):"
echo "  OLD_VERSION:    $OLD_VERSION"
echo "  NEW_VERSION:    $NEW_VERSION"
echo "  LATEST_VERSION: $LATEST_VERSION"
echo ""
echo "Generated files:"
find "$FIXTURE_DIR" -type f | sort
