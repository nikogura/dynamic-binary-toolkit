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

package testfixtures

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// Artifact represents a test artifact with its content, checksum, and signature.
type Artifact struct {
	Binary    []byte
	Checksum  string
	Signature string
}

// ArtifactSet contains artifacts for both test versions.
type ArtifactSet struct {
	OldVersion string // 1.0.0
	NewVersion string // 2.0.0
	Old        Artifact
	New        Artifact
}

// GetDbtArtifacts returns the dbt binary artifacts.
func GetDbtArtifacts() (artifacts ArtifactSet) {
	artifacts = ArtifactSet{
		OldVersion: OldVersion,
		NewVersion: NewVersion,
		Old: Artifact{
			Binary:    Dbt100Binary,
			Checksum:  strings.TrimSpace(Dbt100Checksum),
			Signature: Dbt100Signature,
		},
		New: Artifact{
			Binary:    Dbt200Binary,
			Checksum:  strings.TrimSpace(Dbt200Checksum),
			Signature: Dbt200Signature,
		},
	}
	return artifacts
}

// GetCatalogArtifacts returns the catalog tool artifacts.
func GetCatalogArtifacts() (artifacts ArtifactSet) {
	artifacts = ArtifactSet{
		OldVersion: OldVersion,
		NewVersion: NewVersion,
		Old: Artifact{
			Binary:    Catalog100Binary,
			Checksum:  strings.TrimSpace(Catalog100Checksum),
			Signature: Catalog100Signature,
		},
		New: Artifact{
			Binary:    Catalog200Binary,
			Checksum:  strings.TrimSpace(Catalog200Checksum),
			Signature: Catalog200Signature,
		},
	}
	return artifacts
}

// SetupTestRepo creates the test repository structure in the given directory.
// Returns the truststore content for use in tests.
func SetupTestRepo(tmpDir string) (truststoreContent string, err error) {
	dbtRoot := filepath.Join(tmpDir, "repo", "dbt")
	toolRoot := filepath.Join(tmpDir, "repo", "dbt-tools")

	// Create directory structure using static test versions
	dirs := []string{
		filepath.Join(dbtRoot, OldVersion, "linux", "amd64"),
		filepath.Join(dbtRoot, NewVersion, "linux", "amd64"),
		filepath.Join(dbtRoot, LatestVersion, "linux", "amd64"),
		filepath.Join(toolRoot, "catalog", OldVersion, "linux", "amd64"),
		filepath.Join(toolRoot, "catalog", NewVersion, "linux", "amd64"),
		filepath.Join(toolRoot, "catalog", LatestVersion, "linux", "amd64"),
	}

	for _, dir := range dirs {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			err = errors.Wrapf(err, "failed to create directory %s", dir)
			return truststoreContent, err
		}
	}

	// Write truststore
	truststoreContent = Truststore
	truststorePath := filepath.Join(dbtRoot, "truststore")
	err = os.WriteFile(truststorePath, []byte(truststoreContent), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write truststore")
		return truststoreContent, err
	}

	// Write dbt binaries for all test versions
	err = writeArtifact(dbtRoot, OldVersion, "dbt", Dbt100Binary, Dbt100Checksum, Dbt100Signature)
	if err != nil {
		return truststoreContent, err
	}

	err = writeArtifact(dbtRoot, NewVersion, "dbt", Dbt200Binary, Dbt200Checksum, Dbt200Signature)
	if err != nil {
		return truststoreContent, err
	}

	err = writeArtifact(dbtRoot, LatestVersion, "dbt", Dbt300Binary, Dbt300Checksum, Dbt300Signature)
	if err != nil {
		return truststoreContent, err
	}

	// Latest version carries attestations; older versions are the no-attestation fixtures.
	err = writeAttestationArtifacts(dbtRoot, LatestVersion, "dbt",
		Dbt300Attestation, Dbt300AttestationSig, Dbt300SBOM, Dbt300SBOMSig)
	if err != nil {
		return truststoreContent, err
	}

	// Write catalog binaries for all test versions
	err = writeCatalogArtifact(toolRoot, OldVersion,
		Catalog100Binary, Catalog100Checksum, Catalog100Signature,
		Catalog100Description, Catalog100DescriptionSig)
	if err != nil {
		return truststoreContent, err
	}

	err = writeCatalogArtifact(toolRoot, NewVersion,
		Catalog200Binary, Catalog200Checksum, Catalog200Signature,
		Catalog200Description, Catalog200DescriptionSig)
	if err != nil {
		return truststoreContent, err
	}

	err = writeCatalogArtifact(toolRoot, LatestVersion,
		Catalog300Binary, Catalog300Checksum, Catalog300Signature,
		Catalog300Description, Catalog300DescriptionSig)
	if err != nil {
		return truststoreContent, err
	}

	err = writeAttestationArtifacts(filepath.Join(toolRoot, "catalog"), LatestVersion, "catalog",
		Catalog300Attestation, Catalog300AttestationSig, Catalog300SBOM, Catalog300SBOMSig)
	if err != nil {
		return truststoreContent, err
	}

	// Write install scripts
	err = writeInstallScripts(dbtRoot)
	if err != nil {
		return truststoreContent, err
	}

	return truststoreContent, err
}

func writeArtifact(root, version, name string, binary []byte, checksum, signature string) (err error) {
	dir := filepath.Join(root, version, "linux", "amd64")

	binaryPath := filepath.Join(dir, name)
	err = os.WriteFile(binaryPath, binary, 0755)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", binaryPath)
		return err
	}

	checksumPath := fmt.Sprintf("%s.sha256", binaryPath)
	err = os.WriteFile(checksumPath, []byte(strings.TrimSpace(checksum)), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", checksumPath)
		return err
	}

	signaturePath := fmt.Sprintf("%s.asc", binaryPath)
	err = os.WriteFile(signaturePath, []byte(signature), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", signaturePath)
		return err
	}

	return err
}

// writeAttestationArtifacts writes a tool's provenance attestation (.att.json) and SBOM
// attestation (.sbom.json), each with its detached armored signature, alongside the
// binary — mirroring the repository layout dbt's third verification leg consumes.
func writeAttestationArtifacts(root, version, name string, attestation, attestationSig, sbom, sbomSig string) (err error) {
	dir := filepath.Join(root, version, "linux", "amd64")

	attPath := filepath.Join(dir, fmt.Sprintf("%s.att.json", name))
	err = os.WriteFile(attPath, []byte(attestation), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", attPath)
		return err
	}

	attSigPath := fmt.Sprintf("%s.asc", attPath)
	err = os.WriteFile(attSigPath, []byte(attestationSig), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", attSigPath)
		return err
	}

	sbomPath := filepath.Join(dir, fmt.Sprintf("%s.sbom.json", name))
	err = os.WriteFile(sbomPath, []byte(sbom), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", sbomPath)
		return err
	}

	sbomSigPath := fmt.Sprintf("%s.asc", sbomPath)
	err = os.WriteFile(sbomSigPath, []byte(sbomSig), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", sbomSigPath)
		return err
	}

	return err
}

func writeCatalogArtifact(toolRoot, version string, binary []byte, checksum, signature, description, descriptionSig string) (err error) {
	baseDir := filepath.Join(toolRoot, "catalog", version)
	binaryDir := filepath.Join(baseDir, "linux", "amd64")

	// Write description files
	descPath := filepath.Join(baseDir, "description.txt")
	err = os.WriteFile(descPath, []byte(description), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", descPath)
		return err
	}

	descSigPath := fmt.Sprintf("%s.asc", descPath)
	err = os.WriteFile(descSigPath, []byte(descriptionSig), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", descSigPath)
		return err
	}

	// Write binary
	binaryPath := filepath.Join(binaryDir, "catalog")
	err = os.WriteFile(binaryPath, binary, 0755)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", binaryPath)
		return err
	}

	checksumPath := fmt.Sprintf("%s.sha256", binaryPath)
	err = os.WriteFile(checksumPath, []byte(strings.TrimSpace(checksum)), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", checksumPath)
		return err
	}

	signaturePath := fmt.Sprintf("%s.asc", binaryPath)
	err = os.WriteFile(signaturePath, []byte(signature), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", signaturePath)
		return err
	}

	return err
}

func writeInstallScripts(dbtRoot string) (err error) {
	// install_dbt.sh
	installPath := filepath.Join(dbtRoot, "install_dbt.sh")
	err = os.WriteFile(installPath, InstallDbtScript, 0755)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", installPath)
		return err
	}

	err = os.WriteFile(installPath+".sha256", []byte(strings.TrimSpace(InstallDbtScriptChecksum)), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s.sha256", installPath)
		return err
	}

	err = os.WriteFile(installPath+".asc", []byte(InstallDbtScriptSignature), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s.asc", installPath)
		return err
	}

	// install_dbt_mac_keychain.sh
	macInstallPath := filepath.Join(dbtRoot, "install_dbt_mac_keychain.sh")
	err = os.WriteFile(macInstallPath, InstallDbtMacScript, 0755)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s", macInstallPath)
		return err
	}

	err = os.WriteFile(macInstallPath+".sha256", []byte(strings.TrimSpace(InstallDbtMacScriptChecksum)), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s.sha256", macInstallPath)
		return err
	}

	err = os.WriteFile(macInstallPath+".asc", []byte(InstallDbtMacScriptSignature), 0644)
	if err != nil {
		err = errors.Wrapf(err, "failed to write %s.asc", macInstallPath)
		return err
	}

	return err
}
