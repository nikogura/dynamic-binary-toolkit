// Copyright © 2019 Nik Ogura <nik.ogura@gmail.com>
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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/keybase/go-crypto/openpgp"
	"github.com/pkg/errors"
	"golang.org/x/net/html"
	"gopkg.in/cheggaaa/pb.v1"
)

// AWSIDEnvVar is the default env var for AWS access key.
const AWSIDEnvVar = "AWS_ACCESS_KEY_ID"

// AWSSecretEnvVar is the default env var for AWS secret key.
const AWSSecretEnvVar = "AWS_SECRET_ACCESS_KEY"

// AWSRegionEnvVar is the default env var for AWS region.
const AWSRegionEnvVar = "AWS_DEFAULT_REGION"

// NOPROGRESS turns off the progress bar on file fetches.  Primarily used for testing to avoid cluttering up the output and confusing the test harness.
//
//nolint:gochecknoglobals // package-level flag for test/debug control
var NOPROGRESS = false

// ToolExists returns true if a tool of the name input exists in the repository given.
func (dbt *DBT) ToolExists(toolName string) (found bool, err error) {
	var uri string
	var repoURL string

	if toolName == "" {
		repoURL = dbt.Config.Dbt.Repo
		uri = fmt.Sprintf("%s/", repoURL)
	} else {
		repoURL = dbt.Config.Tools.Repo
		uri = fmt.Sprintf("%s/%s/", repoURL, toolName)
	}

	isS3, s3Meta := S3Url(uri)

	if isS3 {
		found, err = dbt.S3ToolExists(s3Meta)
		return found, err
	}

	client := &http.Client{}

	ctx := context.Background()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", uri)
		return found, err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers")
		return found, err
	}

	resp, doErr := client.Do(req)

	if doErr != nil {
		err = errors.Wrap(doErr, fmt.Sprintf("Failed to find tool in repo %q: %s", repoURL, doErr))
		found = false
		return found, err
	}

	if resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			found = false
			return found, err
		}
	} else {
		found = false
		return found, err
	}

	found = true
	return found, err
}

// ToolVersionExists returns true if the specified version of a tool is in the repo.
func (dbt *DBT) ToolVersionExists(tool string, version string) (ok bool, err error) {
	var uri string

	repoURL := dbt.Config.Tools.Repo

	if tool == "" {
		uri = fmt.Sprintf("%s/%s/", repoURL, version)

	} else {
		uri = fmt.Sprintf("%s/%s/%s/", repoURL, tool, version)
	}

	isS3, s3Meta := S3Url(uri)

	if isS3 {
		ok, err = dbt.S3ToolVersionExists(s3Meta)
		return ok, err
	}

	client := &http.Client{}

	ctx := context.Background()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", uri)
		return ok, err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers")
		return ok, err
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = errors.Wrapf(doErr, "Error looking for tool %q version %q in repo %q", tool, version, uri)
		return ok, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ok, err
	}

	ok = true

	return ok, err
}

// FetchToolVersions returns the available versions of a tool.  If tool name is "", fetches versions of dbt itself.
func (dbt *DBT) FetchToolVersions(toolName string) (versions []string, err error) {
	var uri string
	var repoURL string

	if toolName == "" {
		repoURL = dbt.Config.Dbt.Repo
		uri = fmt.Sprintf("%s/", repoURL)
	} else {
		repoURL = dbt.Config.Tools.Repo
		uri = fmt.Sprintf("%s/%s/", repoURL, toolName)
	}

	isS3, s3Meta := S3Url(uri)

	if isS3 {
		versions, err = dbt.S3FetchToolVersions(s3Meta)
		return versions, err
	}

	client := &http.Client{}

	ctx := context.Background()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", uri)
		return versions, err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers")
		return versions, err
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = errors.Wrapf(doErr, "Error looking for versions of tool %q in repo %q", toolName, uri)
		return versions, err
	}

	if resp != nil {
		versions = dbt.ParseVersionResponse(resp)

		defer resp.Body.Close()

	}

	return versions, err
}

// ParseVersionResponse parses an HTML response and returns a list of semantic version links found.
//
//nolint:gocognit // HTML parsing requires nested loops
func (dbt *DBT) ParseVersionResponse(resp *http.Response) (versions []string) {
	parser := html.NewTokenizer(resp.Body)

	for {
		tt := parser.Next()

		//nolint:exhaustive // only handling relevant token types
		switch tt {
		case html.ErrorToken:
			return versions
		case html.StartTagToken:
			t := parser.Token()
			isAnchor := t.Data == "a"
			//nolint:nestif // HTML anchor parsing requires nested attribute checks
			if isAnchor {
				for _, a := range t.Attr {
					if a.Key == "href" {
						if a.Val != "../" {
							// trim the trailing slash so we get actual semantic versions
							version := strings.TrimRight(a.Val, "/")

							// there could be other files, we only want things that look like semantic versions
							semverMatch := regexp.MustCompile(`^\d+\.\d+\.\d+$`)

							if semverMatch.MatchString(version) {
								versions = append(versions, version)

							}
						}
					}
				}
			}
		default:
			// Skip other token types (TextToken, EndTagToken, etc.)
		}
	}
}

// FetchFile fetches a file and places it on the filesystem.
// Does not validate the signature.  That's a different step.
//
//nolint:gocognit,funlen // file download with progress bar requires multiple code paths
func (dbt *DBT) FetchFile(fileURL string, destPath string) (err error) {
	out, createErr := os.Create(destPath)
	if createErr != nil {
		err = createErr
		return err
	}

	err = os.Chmod(destPath, 0755)
	if err != nil {
		return err
	}

	defer out.Close()

	// Check to see if this is an S3 URL
	isS3, s3Meta := S3Url(fileURL)

	if isS3 {
		err = dbt.S3FetchFile(fileURL, s3Meta, out)
		return err
	}

	client := &http.Client{
		Timeout: 300 * time.Second,
	}

	ctx := context.Background()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodHead, fileURL, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", fileURL)
		return err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers")
		return err
	}

	var bar *pb.ProgressBar

	if !NOPROGRESS {
		headResp, doErr := client.Do(req)
		if doErr != nil {
			err = errors.Wrapf(doErr, "error making request to %s", fileURL)
			return err
		}
		defer headResp.Body.Close()

		if headResp.StatusCode > 399 {
			err = fmt.Errorf("unable to request headers for %s: %d %s", fileURL, headResp.StatusCode, headResp.Status)
			return err
		}

		sizeHeader := headResp.Header.Get("Content-Length")
		if sizeHeader == "" {
			sizeHeader = "0"
		}

		size, atoiErr := strconv.Atoi(sizeHeader)
		if atoiErr != nil {
			err = errors.Wrap(atoiErr, "unable to convert Content-Length header to integer")
			return err
		}

		// create and start progress bar
		bar = pb.New(size).SetUnits(pb.U_BYTES)
		bar.Output = os.Stderr
		bar.Start()

	}

	req, reqErr = http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", fileURL)
		return err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers")
		return err
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = errors.Wrap(doErr, fmt.Sprintf("Error fetching file from %q", fileURL))
		return err
	}

	if resp.StatusCode > 399 {
		err = fmt.Errorf("unable to make request to %s: %d %s", fileURL, resp.StatusCode, resp.Status)
		return err
	}

	if resp != nil {
		defer resp.Body.Close()

		if bar != nil {
			// create proxy reader
			reader := bar.NewProxyReader(resp.Body)

			// and copy from pb reader
			_, _ = io.Copy(out, reader)
		}

		_, err = io.Copy(out, resp.Body)

		if err != nil {
			return err
		}
	}

	return err
}

// VerifyFileChecksum verifies the sha256 checksum of a given file against an expected value.
func (dbt *DBT) VerifyFileChecksum(filePath string, expected string) (success bool, err error) {
	checksum, checksumErr := FileSha256(filePath)
	if checksumErr != nil {
		err = checksumErr
		success = false
		return success, err
	}

	dbt.VerboseOutput("  Expected: %s", expected)
	dbt.VerboseOutput("  Actual:   %s", checksum)

	if checksum == expected {
		success = true
		return success, err
	}

	success = false
	return success, err
}

// VerifyFileVersion verifies the version by matching its Sha256 checksum against what the repo says it should be.
func (dbt *DBT) VerifyFileVersion(fileURL string, filePath string) (success bool, err error) {
	uri := fmt.Sprintf("%s.sha256", fileURL)

	// Check to see if this is an S3 URL
	isS3, s3Meta := S3Url(uri)

	if isS3 {
		success, err = dbt.S3VerifyFileVersion(filePath, s3Meta)
		return success, err
	}

	client := &http.Client{}

	ctx := context.Background()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", uri)
		return success, err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers")
		return success, err
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = errors.Wrapf(doErr, "Error fetching checksum from %q", uri)
		return success, err
	}

	if resp != nil {
		defer resp.Body.Close()

		checksumBytes, readErr := io.ReadAll(resp.Body)

		if readErr != nil {
			err = readErr
			success = false
			return success, err
		}

		expected := string(checksumBytes)
		actual, checksumErr := FileSha256(filePath)

		if checksumErr != nil {
			err = checksumErr
			success = false
			return success, err
		}

		if actual == expected {
			success = true
			return success, err
		}

		success = false
		return success, err
	}

	return success, err
}

// VerifyFileSignature verifies the signature on the given file.
func (dbt *DBT) VerifyFileSignature(homedir string, filePath string) (success bool, err error) {
	if homedir == "" {
		homedir, err = GetHomeDir()
		if err != nil {
			err = errors.Wrapf(err, "failed to get homedir")
			return success, err
		}
	}

	sigFile := fmt.Sprintf("%s.asc", filePath)

	truststoreFileName := fmt.Sprintf("%s/%s", homedir, TruststorePath)

	truststore, openErr := os.Open(truststoreFileName)
	if openErr != nil {
		err = errors.Wrap(openErr, "failed to open truststore file")
		success = false
		return success, err
	}

	defer truststore.Close()

	dbt.VerboseOutput("Verifying signature of %q against trusted keys in %q", filePath, truststoreFileName)

	// openpgp.CheckArmoredDetatchedSignature doesn't actually check multiple certs, so we have to split the truststore file
	// and check each cert individually

	endToken := "-----END PGP PUBLIC KEY BLOCK-----"

	certs := make([]string, 0)

	scanner := bufio.NewScanner(truststore)

	cert := ""

	for scanner.Scan() {
		line := scanner.Text()
		cert += fmt.Sprintf("%s\n", line)

		if line == endToken {
			certs = append(certs, cert)
			cert = ""
		}
	}

	for _, cert := range certs {

		entities, readErr := openpgp.ReadArmoredKeyRing(strings.NewReader(cert))
		if readErr != nil {
			err = errors.Wrap(readErr, "failed to read cert from truststore")
			success = false
			return success, err
		}

		signature, sigErr := os.Open(sigFile)
		if sigErr != nil {
			err = errors.Wrap(sigErr, "failed to open signature file")
			success = false
			return success, err
		}

		defer signature.Close()

		target, targetErr := os.Open(filePath)
		if targetErr != nil {
			err = errors.Wrap(targetErr, "failed to open target file")
			success = false
			return success, err
		}

		defer target.Close()

		entity, _ := openpgp.CheckArmoredDetachedSignature(entities, target, signature)
		if entity != nil {
			dbt.VerboseOutput("  Pass!")
			success = true
			return success, err
		}
	}

	err = errors.New("signing entity not in truststore")
	success = false
	return success, err
}

// FindLatestVersion finds the latest version of the tool available in the tool repo.  If the tool name is "", it is expecting to parse versions of dbt itself.
func (dbt *DBT) FindLatestVersion(toolName string) (latest string, err error) {
	toolInRepo, existsErr := dbt.ToolExists(toolName)
	if existsErr != nil {
		err = errors.Wrap(existsErr, fmt.Sprintf("error checking repo for tool %s", toolName))
		return latest, err
	}

	if toolInRepo {
		versions, versionsErr := dbt.FetchToolVersions(toolName)
		if versionsErr != nil {
			err = errors.Wrap(versionsErr, fmt.Sprintf("error getting versions for tool %s", toolName))
			return latest, err
		}

		latest = LatestVersion(versions)
		return latest, err
	}

	err = errors.New("tool not in repo")

	return latest, err
}

// DefaultSession creates a default AWS session from local config path.  Hooks directly into credentials if present, or Credentials Provider if configured.
func DefaultSession(s3meta *S3Meta) (awssession *session.Session, err error) {
	if os.Getenv(AWSIDEnvVar) == "" && os.Getenv(AWSSecretEnvVar) == "" {
		_ = os.Setenv("AWS_SDK_LOAD_CONFIG", "true")
	}

	awssession, err = session.NewSession()
	if err != nil {
		log.Fatalf("Failed to create aws session")
	}

	// Set the s3 region based on the s3metadata derived from the dbt config.
	if s3meta != nil {
		awssession.Config.Region = &s3meta.Region
	}

	return awssession, err
}

// DirsForURL returns a list of path elements suitable for creating directories/folders given a URL.
func DirsForURL(uri string) (dirs []string, err error) {
	dirs = make([]string, 0)

	u, parseErr := url.Parse(uri)
	if parseErr != nil {
		err = errors.Wrapf(parseErr, "failed to parse %s", uri)
		return dirs, err
	}

	dir := path.Dir(u.Path)
	dir = strings.TrimPrefix(dir, "/")
	parts := strings.Split(dir, "/")

	for len(parts) > 0 {
		dirs = append(dirs, strings.Join(parts, "/"))
		parts = parts[:len(parts)-1]
	}

	// Reverse the order, as this will be much easier to use the data to do path creation
	for i := len(dirs)/2 - 1; i >= 0; i-- {
		opp := len(dirs) - 1 - i
		dirs[i], dirs[opp] = dirs[opp], dirs[i]
	}

	return dirs, err
}

// S3Meta is a struct for holding metadata for S3 Objects.
type S3Meta struct {
	Bucket string
	Region string
	Key    string
	URL    string
}

// S3Url returns true, and a metadata struct if the url given appears to be in s3.
func S3Url(url string) (ok bool, meta S3Meta) {
	// Check to see if it's an s3 URL.
	s3Url := regexp.MustCompile(`https?://(.*)\.s3\.(.*)\.amazonaws.com/?(.*)?`)

	matches := s3Url.FindAllStringSubmatch(url, -1)

	if len(matches) == 0 {
		return ok, meta
	}

	match := matches[0]

	if len(match) == 3 {
		meta = S3Meta{
			Bucket: match[1],
			Region: match[2],
			URL:    url,
		}

		ok = true
		return ok, meta

	} else if len(match) == 4 {
		meta = S3Meta{
			Bucket: match[1],
			Region: match[2],
			Key:    match[3],
			URL:    url,
		}

		ok = true
		return ok, meta
	}

	return ok, meta
}

// S3FetchFile fetches a file out of S3 instead of using a normal HTTP GET.
func (dbt *DBT) S3FetchFile(fileURL string, meta S3Meta, outFile *os.File) (err error) {
	headOptions := &s3.HeadObjectInput{
		Bucket: aws.String(meta.Bucket),
		Key:    aws.String(meta.Key),
	}

	headSvc := s3.New(dbt.S3Session)

	fileMeta, headErr := headSvc.HeadObject(headOptions)
	if headErr != nil {
		err = errors.Wrapf(headErr, "failed to get metadata for %s", fileURL)
		return err
	}

	downloader := s3manager.NewDownloader(dbt.S3Session)
	downloadOptions := &s3.GetObjectInput{
		Bucket: aws.String(meta.Bucket),
		Key:    aws.String(meta.Key),
	}

	if !NOPROGRESS {
		// create and start progress bar
		bar := pb.New(int(*fileMeta.ContentLength)).SetUnits(pb.U_BYTES)
		bar.Output = os.Stderr
		bar.Start()

		buf := &aws.WriteAtBuffer{}

		_, err = downloader.Download(buf, downloadOptions)
		if err != nil {
			err = errors.Wrapf(err, "unable to download file from %s", fileURL)
			return err
		}

		// create proxy reader
		reader := bar.NewProxyReader(bytes.NewBuffer(buf.Bytes()))

		// and copy from pb reader
		_, _ = io.Copy(outFile, reader)

		return err
	}

	_, err = downloader.Download(outFile, downloadOptions)
	if err != nil {
		err = errors.Wrapf(err, "download failed")
		return err
	}

	return err
}

// FetchFileIfPresent fetches fileURL to destPath when the file exists on the repository.
// Absence (HTTP 404 or S3 not-found) is reported as found=false and is NOT an error: it is
// how the optional attestation policy discovers a tool published without attestations.
// Any other failure — auth rejection, connection failure, server error — IS an error.
// Confusing a broken repository with a missing artifact would silently weaken the chain
// from "verify what is there" to "verify nothing", so absence and failure are kept
// strictly distinct.
func (dbt *DBT) FetchFileIfPresent(fileURL string, destPath string) (found bool, err error) {
	isS3, s3Meta := S3Url(fileURL)
	if isS3 {
		found, err = dbt.S3FetchFileIfPresent(fileURL, s3Meta, destPath)
		return found, err
	}

	client := &http.Client{
		Timeout: 300 * time.Second,
	}

	ctx := context.Background()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", fileURL)
		return found, err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrap(err, "failed adding auth headers")
		return found, err
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = errors.Wrap(doErr, fmt.Sprintf("error fetching file from %q", fileURL))
		return found, err
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return found, err
	}

	if resp.StatusCode > 399 {
		err = fmt.Errorf("unable to make request to %s: %d %s", fileURL, resp.StatusCode, resp.Status)
		return found, err
	}

	out, createErr := os.Create(destPath)
	if createErr != nil {
		err = createErr
		return found, err
	}

	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return found, err
	}

	found = true

	return found, err
}

// S3FetchFileIfPresent fetches fileURL from S3 to destPath when the object exists.
// A missing object (HeadObject NotFound / GetObject NoSuchKey) is found=false, not an error;
// every other failure is an error.
func (dbt *DBT) S3FetchFileIfPresent(fileURL string, meta S3Meta, destPath string) (found bool, err error) {
	headSvc := s3.New(dbt.S3Session)

	headOptions := &s3.HeadObjectInput{
		Bucket: aws.String(meta.Bucket),
		Key:    aws.String(meta.Key),
	}

	_, headErr := headSvc.HeadObject(headOptions)
	if headErr != nil {
		if isS3NotFoundError(headErr) {
			return found, err
		}

		err = errors.Wrapf(headErr, "failed to get metadata for %s", fileURL)
		return found, err
	}

	out, createErr := os.Create(destPath)
	if createErr != nil {
		err = createErr
		return found, err
	}

	defer out.Close()

	err = dbt.S3FetchFile(fileURL, meta, out)
	if err != nil {
		return found, err
	}

	found = true

	return found, err
}

// isS3NotFoundError reports whether an AWS SDK error is S3 saying the object does not
// exist. HeadObject returns NotFound (404 with no body to parse); GetObject returns
// NoSuchKey. Both mean absent; neither means broken.
func isS3NotFoundError(headErr error) (notFound bool) {
	var awsErr awserr.Error

	if !errors.As(headErr, &awsErr) {
		return notFound
	}

	if awsErr.Code() == "NotFound" || awsErr.Code() == s3.ErrCodeNoSuchKey {
		notFound = true
	}

	return notFound
}

// S3ToolExists detects whether a tool exists in S3 by looking at the top level folder for the tool.
func (dbt *DBT) S3ToolExists(meta S3Meta) (found bool, err error) {
	svc := s3.New(dbt.S3Session)
	options := &s3.ListObjectsInput{
		Bucket:    aws.String(meta.Bucket),
		Prefix:    aws.String(meta.Key),
		Delimiter: aws.String("/"),
	}

	resp, listErr := svc.ListObjects(options)
	if listErr != nil {
		err = errors.Wrapf(listErr, "failed to list objects at %s", meta.Key)
		return found, err
	}

	if len(resp.Contents) > 0 {
		found = true
	}

	return found, err
}

// S3FetchTruststore fetches the truststore out of S3 writing it into the dbt dir on the local disk.
func (dbt *DBT) S3FetchTruststore(homedir string, meta S3Meta) (err error) {
	downloader := s3manager.NewDownloader(dbt.S3Session)
	filePath := fmt.Sprintf("%s/%s", homedir, TruststorePath)
	dbt.VerboseOutput("Writing truststore to %s", filePath)

	file, createErr := os.Create(filePath)
	if createErr != nil {
		err = errors.Wrapf(createErr, "Failed opening truststore file %s", filePath)
		return err
	}
	_, err = downloader.Download(file, &s3.GetObjectInput{
		Bucket: aws.String(meta.Bucket),
		Key:    aws.String(meta.Key),
	})

	if err != nil {
		err = errors.Wrapf(err, "failed to download truststore from %s", meta.URL)
	}

	return err
}

// S3ToolVersionExists returns true if the tool version exists.
func (dbt *DBT) S3ToolVersionExists(meta S3Meta) (ok bool, err error) {
	headOptions := &s3.HeadObjectInput{
		Bucket: aws.String(meta.Bucket),
		Key:    aws.String(meta.Key),
	}

	log.Printf("Looking for %q in %s", meta.Key, meta.Bucket)

	headSvc := s3.New(dbt.S3Session)

	// not found is an error, as opposed to a successful request that has a 404 code.
	_, fetchErr := headSvc.HeadObject(headOptions)
	if fetchErr != nil {
		err = fetchErr
		return ok, err
	}

	ok = true

	return ok, err
}

// S3VerifyFileVersion verifies the version of a file on the filesystem matches the sha256 hash stored in the s3 bucket for that file.
func (dbt *DBT) S3VerifyFileVersion(filePath string, meta S3Meta) (success bool, err error) {
	// get checksum file from s3
	buff := &aws.WriteAtBuffer{}
	downloader := s3manager.NewDownloader(dbt.S3Session)
	_, downloadErr := downloader.Download(buff, &s3.GetObjectInput{
		Bucket: aws.String(meta.Bucket),
		Key:    aws.String(meta.Key),
	})

	if downloadErr != nil {
		err = errors.Wrapf(downloadErr, "failed to download checksum for %s", meta.URL)
		return success, err
	}

	// compare it to what's on the disk
	expected := string(buff.Bytes())
	actual, checksumErr := FileSha256(filePath)

	dbt.VerboseOutput("Verifying checksum of %q against content of %q", filePath, meta.URL)
	dbt.VerboseOutput("  Expected: %s", expected)
	dbt.VerboseOutput("  Actual:   %s", actual)

	if checksumErr != nil {
		err = checksumErr
		success = false
		return success, err
	}

	if actual == expected {
		success = true
		return success, err
	}

	return success, err
}

// DeletePath sends an authenticated DELETE request to the given URL.
func (dbt *DBT) DeletePath(targetURL string) (statusCode int, err error) {
	client := &http.Client{
		Timeout: defaultRequestTimeout,
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
	defer cancel()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create DELETE request for url: %s", targetURL)
		return statusCode, err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers for DELETE")
		return statusCode, err
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = errors.Wrapf(doErr, "error sending DELETE request to %s", targetURL)
		return statusCode, err
	}
	defer resp.Body.Close()

	statusCode = resp.StatusCode

	if statusCode >= 400 {
		err = fmt.Errorf("DELETE %s returned status %d", targetURL, statusCode)
		return statusCode, err
	}

	return statusCode, err
}

// S3FetchToolVersions fetches available versions for a tool from S3.
func (dbt *DBT) S3FetchToolVersions(meta S3Meta) (versions []string, err error) {
	versions = make([]string, 0)
	uniqueVersions := make(map[string]int)
	svc := s3.New(dbt.S3Session)

	options := &s3.ListObjectsInput{
		Bucket: aws.String(meta.Bucket),
		Prefix: aws.String(meta.Key),
	}

	resp, listErr := svc.ListObjects(options)
	if listErr != nil {
		err = errors.Wrapf(listErr, "failed to list objects at %s", meta.Key)
		return versions, err
	}

	dir := regexp.MustCompile(`\d+\.\d+\.\d+/`)
	semver := regexp.MustCompile(`\d+\.\d+\.\d+`)

	for _, k := range resp.Contents {
		//nolint:nestif // S3 key parsing requires nested version extraction
		if dir.MatchString(*k.Key) {
			parts := strings.Split(*k.Key, "/")
			if len(parts) > 0 {
				if semver.MatchString(parts[0]) {
					uniqueVersions[parts[0]] = 1
				} else if len(parts) > 1 {
					if semver.MatchString(parts[1]) {
						uniqueVersions[parts[1]] = 1
					}
				}
			}
		}
	}

	for k := range uniqueVersions {
		versions = append(versions, k)
	}

	return versions, err
}
