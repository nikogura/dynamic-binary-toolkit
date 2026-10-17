// Copyright © 2025 Nik Ogura <nik.ogura@gmail.com>
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

// Package testfixtures provides pre-built test artifacts for dbt tests.
// These fixtures use static version numbers (1.0.0, 2.0.0, 3.0.0) that
// never change, decoupled from actual release versions.
package testfixtures

import (
	_ "embed"
)

// Test fixture versions - these are static and never change.
// They exist only to test that dbt correctly identifies and uses
// the "latest" version from a repository.
const (
	OldVersion    = "1.0.0"
	NewVersion    = "2.0.0"
	LatestVersion = "3.0.0"
)

// PublicKey is the GPG test key (public key for signature verification).
//
//go:embed gpg/public-key.asc
var PublicKey string

// Dbt100Binary is the DBT binary for test version 1.0.0.
//
//go:embed repo/dbt/1.0.0/linux/amd64/dbt
var Dbt100Binary []byte

//go:embed repo/dbt/1.0.0/linux/amd64/dbt.sha256
var Dbt100Checksum string

//go:embed repo/dbt/1.0.0/linux/amd64/dbt.asc
var Dbt100Signature string

// Dbt200Binary is the DBT binary for test version 2.0.0.
//
//go:embed repo/dbt/2.0.0/linux/amd64/dbt
var Dbt200Binary []byte

//go:embed repo/dbt/2.0.0/linux/amd64/dbt.sha256
var Dbt200Checksum string

//go:embed repo/dbt/2.0.0/linux/amd64/dbt.asc
var Dbt200Signature string

// Dbt300Binary is the DBT binary for test version 3.0.0.
//
//go:embed repo/dbt/3.0.0/linux/amd64/dbt
var Dbt300Binary []byte

//go:embed repo/dbt/3.0.0/linux/amd64/dbt.sha256
var Dbt300Checksum string

//go:embed repo/dbt/3.0.0/linux/amd64/dbt.asc
var Dbt300Signature string

//go:embed repo/dbt/3.0.0/linux/amd64/dbt.att.json
var Dbt300Attestation string

//go:embed repo/dbt/3.0.0/linux/amd64/dbt.att.json.asc
var Dbt300AttestationSig string

//go:embed repo/dbt/3.0.0/linux/amd64/dbt.sbom.json
var Dbt300SBOM string

//go:embed repo/dbt/3.0.0/linux/amd64/dbt.sbom.json.asc
var Dbt300SBOMSig string

// Catalog100Binary is the catalog binary for test version 1.0.0.
//
//go:embed repo/dbt-tools/catalog/1.0.0/linux/amd64/catalog
var Catalog100Binary []byte

//go:embed repo/dbt-tools/catalog/1.0.0/linux/amd64/catalog.sha256
var Catalog100Checksum string

//go:embed repo/dbt-tools/catalog/1.0.0/linux/amd64/catalog.asc
var Catalog100Signature string

//go:embed repo/dbt-tools/catalog/1.0.0/description.txt
var Catalog100Description string

//go:embed repo/dbt-tools/catalog/1.0.0/description.txt.asc
var Catalog100DescriptionSig string

// Catalog200Binary is the catalog binary for test version 2.0.0.
//
//go:embed repo/dbt-tools/catalog/2.0.0/linux/amd64/catalog
var Catalog200Binary []byte

//go:embed repo/dbt-tools/catalog/2.0.0/linux/amd64/catalog.sha256
var Catalog200Checksum string

//go:embed repo/dbt-tools/catalog/2.0.0/linux/amd64/catalog.asc
var Catalog200Signature string

//go:embed repo/dbt-tools/catalog/2.0.0/description.txt
var Catalog200Description string

//go:embed repo/dbt-tools/catalog/2.0.0/description.txt.asc
var Catalog200DescriptionSig string

// Catalog300Binary is the catalog binary for test version 3.0.0.
//
//go:embed repo/dbt-tools/catalog/3.0.0/linux/amd64/catalog
var Catalog300Binary []byte

//go:embed repo/dbt-tools/catalog/3.0.0/linux/amd64/catalog.sha256
var Catalog300Checksum string

//go:embed repo/dbt-tools/catalog/3.0.0/linux/amd64/catalog.asc
var Catalog300Signature string

//go:embed repo/dbt-tools/catalog/3.0.0/linux/amd64/catalog.att.json
var Catalog300Attestation string

//go:embed repo/dbt-tools/catalog/3.0.0/linux/amd64/catalog.att.json.asc
var Catalog300AttestationSig string

//go:embed repo/dbt-tools/catalog/3.0.0/linux/amd64/catalog.sbom.json
var Catalog300SBOM string

//go:embed repo/dbt-tools/catalog/3.0.0/linux/amd64/catalog.sbom.json.asc
var Catalog300SBOMSig string

//go:embed repo/dbt-tools/catalog/3.0.0/description.txt
var Catalog300Description string

//go:embed repo/dbt-tools/catalog/3.0.0/description.txt.asc
var Catalog300DescriptionSig string

// Truststore is the GPG truststore (same as public key).
//
//go:embed repo/dbt/truststore
var Truststore string

// InstallDbtScript is the dbt installation script.
//
//go:embed repo/dbt/install_dbt.sh
var InstallDbtScript []byte

//go:embed repo/dbt/install_dbt.sh.sha256
var InstallDbtScriptChecksum string

//go:embed repo/dbt/install_dbt.sh.asc
var InstallDbtScriptSignature string

//go:embed repo/dbt/install_dbt_mac_keychain.sh
var InstallDbtMacScript []byte

//go:embed repo/dbt/install_dbt_mac_keychain.sh.sha256
var InstallDbtMacScriptChecksum string

//go:embed repo/dbt/install_dbt_mac_keychain.sh.asc
var InstallDbtMacScriptSignature string
