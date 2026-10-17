// Copyright © 2026 Nik Ogura <nik.ogura@gmail.com>
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

package dbt

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/pkg/errors"
)

// Attestation support: the third leg of dbt's verification chain.
//
// dbt verifies three things about a tool before it runs: the checksum (the bytes on disk
// are the bytes the repo published), the signature (the bytes were signed by an identity in
// the truststore), and — when present and required by policy — the attestations (the bytes
// carry a signed in-toto statement binding them to their build provenance and SBOM).
//
// This file is the data model and the digest binding. The digest is the spine: every
// attestation names exactly one subject, and that subject's sha256 must equal the local
// tool's checksum. A statement whose subject names some other digest is a statement about
// some other artifact, and it is refused, not downgraded.
//
// The predicate is deliberately opaque (json.RawMessage). dbt binds and authenticates; it
// does not interpret policy. Interpreting the contents of a provenance predicate is the job
// of a policy gate, not a distribution client.

// StatementTypeV1 is the in-toto statement envelope type dbt accepts. Earlier statement
// versions (v0.1) are refused: dbt's producer contract is in-toto Statement v1.
const StatementTypeV1 = "https://in-toto.io/Statement/v1"

// PredicateTypeSLSAProvenanceV1 is the SLSA provenance predicate dbt expects in a
// tool's .att.json provenance attestation.
const PredicateTypeSLSAProvenanceV1 = "https://slsa.dev/provenance/v1"

// PredicateTypeCycloneDXBOM is the predicate type dbt expects in a tool's .sbom.json
// SBOM attestation: an in-toto Statement whose predicate is a CycloneDX BOM. The
// unversioned form matches the in-toto-golang/cosign convention. Versioned forms
// (https://cyclonedx.org/bom/v1.4) are refused — the contract is pinned, and loosening
// it is an explicit, documented change.
const PredicateTypeCycloneDXBOM = "https://cyclonedx.org/bom"

// AttestationPolicyOptional means attestations are verified when present and refused when
// present-but-broken, but a tool with no attestations on the repo still runs. This is the
// default: it is the posture for repos whose tools predate the attestation leg.
const AttestationPolicyOptional = "optional"

// AttestationPolicyRequired means a tool must carry a valid, signed, digest-bound
// provenance attestation and SBOM attestation on the repo, or dbt refuses to run it.
// This is the higher-assurance posture for repos published by a toolchain that produces
// attestations (e.g. gomason with attestations enabled).
const AttestationPolicyRequired = "required"

// validateAttestationPolicy enforces the attestation policy vocabulary fail-closed. An
// unrecognized value is a configuration error, never a silent downgrade to optional — a
// typo'd "requird" must fail loudly at config load, not quietly relax the chain.
// Case-sensitive by design: the values are machine-written config, not human prose.
func validateAttestationPolicy(policy string) (err error) {
	switch policy {
	case "", AttestationPolicyOptional, AttestationPolicyRequired:
		return err
	default:
		err = fmt.Errorf("invalid attestationPolicy %q (want %q or %q)", policy, AttestationPolicyOptional, AttestationPolicyRequired)
		return err
	}
}

// VerifyAttestations runs the third verification leg for a tool on disk. If the
// attestation files sit beside the tool — the provenance attestation (<tool>.att.json)
// and the SBOM attestation (<tool>.sbom.json) — then BOTH must be present, each must
// carry a detached signature by an identity in the truststore, and each must bind by
// sha256 digest to the exact tool bytes.
//
// present=false means no attestation files exist at all — absence is a policy question,
// not a verdict, and the caller decides what it means (optional runs, required refuses).
// present=true with ok=false means the attestations exist but are broken — a
// half-attested or mis-bound tool is a botched publish, and that is always fatal
// regardless of policy. Operational trouble is reported through err; broken-but-
// parseable states are verdicts that carry a diagnostic err as well.
func (dbt *DBT) VerifyAttestations(homedir string, toolPath string, toolName string) (present bool, ok bool, err error) {
	attPath := fmt.Sprintf("%s.att.json", toolPath)
	sbomPath := fmt.Sprintf("%s.sbom.json", toolPath)

	attExists, statErr := fileExists(attPath)
	if statErr != nil {
		err = errors.Wrapf(statErr, "failed to stat %s", attPath)
		return present, ok, err
	}

	sbomExists, statErr := fileExists(sbomPath)
	if statErr != nil {
		err = errors.Wrapf(statErr, "failed to stat %s", sbomPath)
		return present, ok, err
	}

	if !attExists && !sbomExists {
		return present, ok, err
	}

	present = true

	// A half-attested tool is a botched publish: refuse rather than partially verify.
	if !attExists {
		err = fmt.Errorf("attestation %s is missing while %s exists", attPath, sbomPath)
		return present, ok, err
	}

	if !sbomExists {
		err = fmt.Errorf("sbom attestation %s is missing while %s exists", sbomPath, attPath)
		return present, ok, err
	}

	toolSha256, shaErr := FileSha256(toolPath)
	if shaErr != nil {
		err = errors.Wrapf(shaErr, "failed to checksum %s", toolPath)
		return present, ok, err
	}

	ok, err = dbt.verifyAttestationFile(homedir, attPath, toolName, toolSha256, PredicateTypeSLSAProvenanceV1)
	if !ok {
		return present, ok, err
	}

	ok, err = dbt.verifyAttestationFile(homedir, sbomPath, toolName, toolSha256, PredicateTypeCycloneDXBOM)
	if !ok {
		return present, ok, err
	}

	return present, ok, err
}

// verifyAttestationFile checks one attestation file end to end: signature against the
// truststore, shape, and subject binding to the tool's sha256.
func (dbt *DBT) verifyAttestationFile(homedir string, attPath string, toolName string, toolSha256 string, predicateType string) (ok bool, err error) {
	sigOk, sigErr := dbt.VerifyFileSignature(homedir, attPath)
	if sigErr != nil {
		err = errors.Wrapf(sigErr, "error validating signature of %s", attPath)
		return ok, err
	}

	if !sigOk {
		err = fmt.Errorf("signature of %s failed to verify", attPath)
		return ok, err
	}

	stmt, loadErr := LoadAttestationStatement(attPath, predicateType)
	if loadErr != nil {
		err = errors.Wrapf(loadErr, "failed to load attestation %s", attPath)
		return ok, err
	}

	bound, bindErr := stmt.VerifySubjectBinding(toolName, toolSha256)
	if bindErr != nil {
		err = errors.Wrapf(bindErr, "error checking subject binding of %s", attPath)
		return ok, err
	}

	if !bound {
		err = fmt.Errorf("attestation %s is not bound to %s at sha256 %s", attPath, toolName, toolSha256)
		return ok, err
	}

	ok = true

	return ok, err
}

// checkAttestationPolicy applies the third leg at run time. Any error out of
// VerifyAttestations — operational trouble (stat failure, unreadable file) or a broken
// verdict — refuses the run regardless of policy: the attestation check never fails
// open. Only a clean absent verdict is policy-gated, and only under a required policy is
// absence fatal. This is the single gate verifyAndRun calls after checksum and signature
// verification pass.
func (dbt *DBT) checkAttestationPolicy(homedir string, toolPath string, toolName string) (err error) {
	attestationPresent, attestationOk, attErr := dbt.VerifyAttestations(homedir, toolPath, toolName)
	if attErr != nil {
		dbt.VerboseOutput("Attestation verification error: %s", attErr)
		err = fmt.Errorf("attestation verification of %s failed: %w", toolName, attErr)
		return err
	}

	if attestationPresent && !attestationOk {
		// Defensive: a broken verdict always carries a diagnostic error (caught above),
		// but a bare ok=false must never run the tool either.
		err = fmt.Errorf("attestation verification of %s failed", toolName)
		return err
	}

	if dbt.Config.AttestationPolicy == AttestationPolicyRequired && !attestationPresent {
		err = fmt.Errorf("tool %s carries no attestations, but policy requires them", toolName)
		return err
	}

	return err
}

// checkUpgradeAttestations runs the third leg for a dbt self-update. The incoming
// binary is staged in a temp dir; its attestations are fetched beside it, and a
// present-but-broken verdict refuses the upgrade — a new dbt whose provenance or SBOM
// does not verify never moves into place. Required-policy absence is refused by
// FetchAttestations at fetch time.
func (dbt *DBT) checkUpgradeAttestations(homedir string, versionURL string, stagedBinaryPath string) (err error) {
	err = dbt.FetchAttestations(versionURL, stagedBinaryPath, BrandBinary)
	if err != nil {
		return err
	}

	present, ok, attErr := dbt.VerifyAttestations(homedir, stagedBinaryPath, BrandBinary)
	if attErr != nil {
		// Operational trouble or a broken verdict: refuse the upgrade — the attestation
		// check never fails open, in either direction of a self-update.
		err = fmt.Errorf("attestation verification of incoming %s failed: %w", BrandBinary, attErr)
		return err
	}

	if present && !ok {
		// Defensive: see checkAttestationPolicy.
		err = fmt.Errorf("attestation verification of incoming %s failed", BrandBinary)
		return err
	}

	return err
}

// FetchAttestations downloads the provenance attestation (<tool>.att.json), the SBOM
// attestation (<tool>.sbom.json), and each file's detached signature, to sit beside
// localPath — when the repository publishes them. Absence is fine under an optional
// policy; under a required policy a missing file is an error at download time —
// the earliest, clearest place to refuse — rather than at verify time.
func (dbt *DBT) FetchAttestations(toolURL string, localPath string, toolName string) (err error) {
	files := []string{".att.json", ".att.json.asc", ".sbom.json", ".sbom.json.asc"}

	found := make(map[string]bool, len(files))

	for _, suffix := range files {
		localFilePath := fmt.Sprintf("%s%s", localPath, suffix)

		found[suffix], err = dbt.FetchFileIfPresent(fmt.Sprintf("%s%s", toolURL, suffix), localFilePath)
		if err != nil {
			err = errors.Wrapf(err, "failed fetching %s for %s", suffix, toolName)
			return err
		}

		// The repository does not carry this file: remove any stale local copy so a
		// previous version's attestations cannot fail digest binding against this
		// version's bytes. The attestation files are named for the tool, not the
		// version, so a stale copy otherwise survives every version switch.
		if !found[suffix] {
			removeErr := os.Remove(localFilePath)
			if removeErr != nil && !os.IsNotExist(removeErr) {
				err = errors.Wrapf(removeErr, "failed removing stale attestation file %s", localFilePath)
				return err
			}
		}
	}

	if dbt.Config.AttestationPolicy == AttestationPolicyRequired {
		for _, suffix := range files {
			if !found[suffix] {
				err = fmt.Errorf("required attestation file %s%s missing from repository for tool %s", toolName, suffix, toolName)
				return err
			}
		}
	}

	return err
}

// fileExists reports whether path exists, distinguishing absence from stat trouble.
func fileExists(path string) (exists bool, err error) {
	_, statErr := os.Stat(path)
	if statErr == nil {
		exists = true
		return exists, err
	}

	if os.IsNotExist(statErr) {
		return exists, err
	}

	err = errors.Wrapf(statErr, "failed to stat %s", path)

	return exists, err
}

// AttestationStatement is an in-toto Statement as dbt consumes it. Only the envelope is
// modeled; the predicate is preserved verbatim and is never interpreted here.
type AttestationStatement struct {
	Type          string               `json:"_type"`
	PredicateType string               `json:"predicateType"`
	Subject       []AttestationSubject `json:"subject"`
	Predicate     json.RawMessage      `json:"predicate,omitempty"`
}

// AttestationSubject is the single artifact a statement binds its predicate to.
type AttestationSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// LoadAttestationStatement reads an attestation statement from path and validates its shape
// fail-closed: it must be an in-toto Statement v1 carrying exactly the expected
// predicateType, with exactly one subject carrying at least one digest, and a non-empty
// predicate. Shape violations are errors, not verdicts — a malformed statement never
// reaches the binding check to be leniently passed.
func LoadAttestationStatement(path string, expectPredicateType string) (stmt AttestationStatement, err error) {
	bytes, readErr := os.ReadFile(path)
	if readErr != nil {
		err = errors.Wrapf(readErr, "failed to read attestation statement %s", path)
		return stmt, err
	}

	unmarshalErr := json.Unmarshal(bytes, &stmt)
	if unmarshalErr != nil {
		err = errors.Wrapf(unmarshalErr, "failed to parse attestation statement %s", path)
		return stmt, err
	}

	err = stmt.validateShape(expectPredicateType)
	if err != nil {
		return stmt, err
	}

	return stmt, err
}

// validateShape enforces the envelope contract fail-closed.
func (s AttestationStatement) validateShape(expectPredicateType string) (err error) {
	if s.Type != StatementTypeV1 {
		err = fmt.Errorf("attestation statement has unsupported _type %q (want %s)", s.Type, StatementTypeV1)
		return err
	}

	if s.PredicateType != expectPredicateType {
		err = fmt.Errorf("attestation statement has predicateType %q, expected %q", s.PredicateType, expectPredicateType)
		return err
	}

	if len(s.Predicate) == 0 {
		err = errors.New("attestation statement is missing its predicate")
		return err
	}

	if len(s.Subject) != 1 {
		err = fmt.Errorf("attestation statement must have exactly one subject, found %d", len(s.Subject))
		return err
	}

	if len(s.Subject[0].Digest) == 0 {
		err = fmt.Errorf("attestation statement subject %q carries no digest", s.Subject[0].Name)
		return err
	}

	return err
}

// VerifySubjectBinding checks that the statement is bound to the named tool at the given
// sha256: the subject's name must be the tool, and its sha256 digest must match. Mismatches
// are a false verdict (ok=false, nil error) so callers report and refuse; operational
// failures are errors. A missing or empty sha256 in the subject digest is a binding
// failure, not a parse failure — the statement parsed, it simply does not describe these
// bytes.
func (s AttestationStatement) VerifySubjectBinding(toolName string, sha256 string) (ok bool, err error) {
	if s.Subject[0].Name != toolName {
		return ok, err
	}

	digest, found := s.Subject[0].Digest["sha256"]
	if !found || digest == "" {
		return ok, err
	}

	if digest != sha256 {
		return ok, err
	}

	ok = true

	return ok, err
}
