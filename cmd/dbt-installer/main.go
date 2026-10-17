// Copyright 2025 Nik Ogura <nik.ogura@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// dbt-installer is a standalone installer for dbt (Dynamic Binary Toolkit).
//
// This binary is designed to be built with organization-specific configuration
// injected at build time via ldflags. Users simply download and run it.
//
// Build example:
//
//	go build -ldflags "-X main.serverURL=https://dbt.example.com \
//	                   -X main.serverName=prod \
//	                   -X main.issuerURL=https://dex.example.com \
//	                   -X main.oidcAudience=https://dbt.example.com \
//	                   -X main.oidcClientID=dbt-ssh \
//	                   -X main.connectorID=ssh \
//	                   -X main.attestationPolicy=required" \
//	    -o dbt-installer ./cmd/dbt-installer
package main

import (
	"github.com/nikogura/dynamic-binary-toolkit/cmd/dbt-installer/installer"
)

// Build-time configuration injected via ldflags.
// Example: go build -ldflags "-X main.serverURL=https://dbt.example.com"
//
//nolint:gochecknoglobals // Required for ldflags injection
var (
	// serverURL is the base URL of the dbt repository server.
	// Required. Example: "https://dbt.example.com" or "s3://bucket-name".
	serverURL string

	// serverName is the alias for this server in the config.
	// Optional, defaults to derived from URL.
	serverName string

	// toolsURL is the URL of the tools repository.
	// Optional, defaults to serverURL + "/dbt-tools" or S3 tools bucket.
	toolsURL string

	// s3Region is the AWS region for S3 buckets.
	// Optional, auto-detected if not provided.
	s3Region string

	// issuerURL is the OIDC issuer URL (e.g., Dex).
	// Required for OIDC authentication.
	issuerURL string

	// oidcAudience is the target audience for OIDC tokens.
	// Optional, defaults to serverURL.
	oidcAudience string

	// oidcClientID is the OAuth2 client ID.
	// Optional, defaults to "dbt-ssh" for SSH-OIDC, "dbt" for device flow.
	oidcClientID string

	// oidcClientSecret is the OAuth2 client secret.
	// Optional.
	oidcClientSecret string

	// connectorID is the OIDC connector ID.
	// Use "ssh" for SSH-OIDC token exchange.
	connectorID string

	// attestationPolicy is the dbt attestation policy to write into the generated
	// config: "required" makes dbt refuse tools lacking signed SBOM + SLSA provenance
	// attestations; empty means optional (verify when present).
	attestationPolicy string

	// version is the installer version.
	version = "dev"
)

func main() {
	config := &installer.Config{
		ServerURL:         serverURL,
		ServerName:        serverName,
		ToolsURL:          toolsURL,
		S3Region:          s3Region,
		IssuerURL:         issuerURL,
		OIDCAudience:      oidcAudience,
		OIDCClientID:      oidcClientID,
		OIDCClientSecret:  oidcClientSecret,
		ConnectorID:       connectorID,
		AttestationPolicy: attestationPolicy,
		Version:           version,
	}

	installer.Run(config)
}
