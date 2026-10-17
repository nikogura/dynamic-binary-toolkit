// Copyright © 2020 Nik Ogura <nik.ogura@gmail.com>
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

//nolint:noinlineerr,nosprintfhostport // test file - inline error handling and host:port formatting acceptable
package dbt

import (
	"bytes"
	"fmt"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/nikogura/dynamic-binary-toolkit/pkg/dbt/testfixtures"
	"github.com/phayes/freeport"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// TestPackageGroup Github group owning this codebase.  Used to compile itself as part of it's test suite.  If you're not me, you'll want to change this in your fork.
const TestPackageGroup = "nikogura"

//nolint:gochecknoglobals // test setup requires shared state
var (
	tmpDir            string
	dbtConfig         Config
	s3DbtConfig       Config
	port              int
	repo              DBTRepoServer
	testHost          string
	trustfileContents string
	dbtRoot           string
	toolRoot          string
	trustFile         string
	setup             bool
	oldVersion        = testfixtures.OldVersion
	newVersion        = testfixtures.NewVersion
	latestVersion     = testfixtures.LatestVersion
	testServer        *httptest.Server
	s3Config          *aws.Config
	s3Session         *session.Session
	s3Backend         *s3mem.Backend
	faker             *gofakes3.GoFakeS3
	homeDirRepoServer string
	homeDirS3         string
)

type testFile struct {
	Name     string
	FilePath string
	URLPath  string
	TestURL  string
	Repo     string
}

//nolint:gochecknoglobals // test setup requires shared state
var (
	testFilesA map[string]*testFile
	testFilesB map[string]*testFile
	testFilesC map[string]*testFile
)

func TestMain(m *testing.M) {
	// Set up signal handler to ensure cleanup on timeout/interrupt
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Received interrupt signal, cleaning up...")
		tearDown()
		os.Exit(1)
	}()

	// Ensure cleanup happens even if TestMain exits early
	defer tearDown()

	err := setUp()
	if err != nil {
		log.Fatalf("Setup Failed: %s", err)
	}

	code := m.Run()

	os.Exit(code)
}

//nolint:gocognit // test setup requires multiple initialization steps
func setUp() (err error) {
	dir, mkdirErr := os.MkdirTemp("", "dbt")
	if mkdirErr != nil {
		err = errors.Wrapf(mkdirErr, "Error creating temp dir %q", dir)
		return err
	}

	NOPROGRESS = true

	logrus.SetLevel(logrus.DebugLevel)

	tmpDir = dir
	homeDirRepoServer = fmt.Sprintf("%s/homeDirReposerver", tmpDir)
	homeDirS3 = fmt.Sprintf("%s/homeDirS3", tmpDir)
	fmt.Printf("Temp Dir: %s\n", tmpDir)
	fmt.Printf("Homedir Reposerver: %s\n", homeDirRepoServer)
	fmt.Printf("Homedir S3: %s\n", homeDirS3)

	dbtRoot = fmt.Sprintf("%s/repo/dbt", tmpDir)
	trustFile = fmt.Sprintf("%s/truststore", dbtRoot)
	toolRoot = fmt.Sprintf("%s/repo/dbt-tools", tmpDir)

	freePort, portErr := freeport.GetFreePort()
	if portErr != nil {
		err = errors.Wrapf(portErr, "Error getting a free port")
		return err
	}

	port = freePort

	fmt.Printf("-- Creating Version A Test Files ---\n")
	testFilesA = createTestFilesA(toolRoot, oldVersion, dbtRoot)
	fmt.Printf("--- Created %d Test Files ---\n", len(testFilesA))

	fmt.Printf("-- Creating Version B Test Files ---\n")
	testFilesB = createTestFilesB(toolRoot, newVersion, dbtRoot)
	fmt.Printf("--- Created %d Test Files ---\n", len(testFilesB))

	fmt.Printf("-- Creating Version C Test Files ---\n")
	testFilesC = createTestFilesC(toolRoot, latestVersion, dbtRoot)
	fmt.Printf("--- Created %d Test Files ---\n", len(testFilesC))

	// Dbt config for the built in repo server
	dbtConfig = Config{
		Dbt: DbtConfig{
			Repo:       fmt.Sprintf("http://127.0.0.1:%d/dbt", port),
			TrustStore: fmt.Sprintf("http://127.0.0.1:%d/dbt/truststore", port),
		},
		Tools: ToolsConfig{
			Repo: fmt.Sprintf("http://127.0.0.1:%d/dbt-tools", port),
		},
	}

	// Dbt config for using s3 as a repo
	s3DbtConfig = Config{
		Dbt: DbtConfig{
			Repo:       "https://dbt.s3.us-east-1.amazonaws.com",
			TrustStore: "https://dbt.s3.us-east-1.amazonaws.com/truststore",
		},
		Tools: ToolsConfig{
			Repo: "https://dbt-tools.s3.us-east-1.amazonaws.com",
		},
	}

	//nolint:nestif // test setup requires complex initialization
	if !setup {
		// Setup Fake S3
		s3Backend = s3mem.New()
		faker = gofakes3.New(s3Backend)
		testServer = httptest.NewServer(faker.Server())

		// S3 config
		s3Config = &aws.Config{
			Credentials:      credentials.NewStaticCredentials("foo", "bar", ""),
			Endpoint:         aws.String(testServer.URL),
			Region:           aws.String("us-east-1"),
			DisableSSL:       aws.Bool(true),
			S3ForcePathStyle: aws.Bool(true),
		}
		s3Session, err = session.NewSession(s3Config)
		if err != nil {
			log.Fatalf("failed creating fake aws session: %s", err)
		}

		dbtBucket := "dbt"
		toolsBucket := "dbt-tools"

		s3Client := s3.New(s3Session)
		cparams := &s3.CreateBucketInput{Bucket: aws.String(dbtBucket)}

		_, err = s3Client.CreateBucket(cparams)
		if err != nil {
			err = errors.Wrapf(err, "Failed to create bucket %s", dbtBucket)
			return err
		}

		cparams = &s3.CreateBucketInput{Bucket: aws.String(toolsBucket)}
		_, err = s3Client.CreateBucket(cparams)
		if err != nil {
			err = errors.Wrapf(err, "Failed to create bucket %s", toolsBucket)
			return err
		}

		// Build test repo using pre-built fixtures (fast, no gomason)
		err = buildTestRepo()
		if err != nil {
			err = errors.Wrapf(err, "Error building test repo")
			return err
		}

		// Set up the repo server
		repo = DBTRepoServer{
			Address:    "127.0.0.1",
			Port:       port,
			ServerRoot: fmt.Sprintf("%s/repo", tmpDir),
		}

		// Run test server in the background
		go func() {
			_ = repo.RunRepoServer()
		}()

		// Give things a moment to come up.
		time.Sleep(time.Second)

		testHost = fmt.Sprintf("http://%s:%d", repo.Address, repo.Port)
		fmt.Printf("--- Serving requests on %s ---\n", testHost)

		log.Printf("Sleeping for 1 second for the test artifact server to start up.")
		time.Sleep(time.Second * 1)

		configs := []struct {
			homedir string
			config  string
		}{
			{
				homeDirRepoServer,
				testDbtConfigContents(port),
			},
			{
				homeDirS3,
				testDbtConfigS3Contents(),
			},
		}

		for _, c := range configs {
			err = os.MkdirAll(c.homedir, 0755)
			if err != nil {
				err = errors.Wrapf(err, "Error generating fake home dir")
				return err
			}

			err = GenerateDbtDir(c.homedir, true)
			if err != nil {
				err = errors.Wrapf(err, "Error generating dbt dir")
				return err
			}

			configPath := fmt.Sprintf("%s/%s", c.homedir, ConfigDir)
			fileName := fmt.Sprintf("%s/dbt.json", configPath)

			writeErr := os.WriteFile(fileName, []byte(c.config), 0644)
			if writeErr != nil {
				err = errors.Wrapf(writeErr, "Error writing config file to %s", fileName)
				return err
			}

			// The fake home also carries the fixture truststore: the upgrade flow verifies
			// signatures (binary and attestations) against it.
			trustPath := fmt.Sprintf("%s/%s", c.homedir, TruststorePath)
			writeErr = os.WriteFile(trustPath, []byte(trustfileContents), 0644)
			if writeErr != nil {
				err = errors.Wrapf(writeErr, "Error writing truststore file to %s", trustPath)
				return err
			}

		}

		setup = true
	}
	return err
}

func tearDown() {
	if testServer != nil {
		testServer.Close()
	}
	if tmpDir != "" {
		if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
			log.Printf("Cleaning up test directory: %s", tmpDir)

			// Fix permissions using bulk chmod commands for better performance
			// Make all directories writable and executable by owner
			chmodDirCmd := exec.Command("find", tmpDir, "-type", "d", "-exec", "chmod", "u+rwx", "{}", "+")
			_ = chmodDirCmd.Run() // Ignore errors, try to continue

			// Make all files readable and writable by owner (especially for Go module cache read-only files)
			chmodFileCmd := exec.Command("find", tmpDir, "-type", "f", "-exec", "chmod", "u+rw", "{}", "+")
			_ = chmodFileCmd.Run() // Ignore errors, try to continue

			err = os.RemoveAll(tmpDir)
			if err != nil {
				log.Printf("cleanup failed: %s", err)
			} else {
				log.Printf("Successfully cleaned up test directory")
			}
		}
	}
}

func testDbtConfigContents(port int) (config string) {
	config = fmt.Sprintf(`{
  "dbt": {
    "repository": "http://127.0.0.1:%d/dbt",
    "truststore": "http://127.0.0.1:%d/dbt/truststore"
  },
  "tools": {
    "repository": "http://127.0.0.1:%d/dbt-tools"
  }
}`, port, port, port)
	return config
}

func testDbtConfigS3Contents() (config string) {
	config = `{
  "dbt": {
    "repository": "https://dbt.s3.us-east-1.amazonaws.com",
    "truststore": "https://dbt.s3.us-east-1.amazonaws.com/truststore"
  },
  "tools": {
    "repository": "https://dbt-tools.s3.us-east-1.amazonaws.com"
  }
}`
	return config
}

func testDbtURL(port int) (url string) {
	url = fmt.Sprintf("http://127.0.0.1:%d/dbt", port)
	return url
}

func testToolURL(port int) (url string) {
	url = fmt.Sprintf("http://127.0.0.1:%d/dbt-tools", port)
	return url
}

func createTestFilesA(toolRoot string, version string, dbtRoot string) (testFiles map[string]*testFile) {
	testFiles = make(map[string]*testFile)
	files := []*testFile{
		{
			Name:     "catalog-description.txt",
			FilePath: fmt.Sprintf("%s/catalog/%s/description.txt", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/description.txt", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog-description.txt.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/description.txt.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/description.txt.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "dbt_linux_amd64",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt", version),
			Repo:     "dbt",
		},
		{
			Name:     "dbt_linux_amd64.asc",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt.asc", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt.asc", version),
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt.sh",
			FilePath: fmt.Sprintf("%s/install_dbt.sh", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh",
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt.sh.asc",
			FilePath: fmt.Sprintf("%s/install_dbt.sh.asc", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh.asc",
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt_mac_keychain.sh",
			FilePath: fmt.Sprintf("%s/install_dbt_mac_keychain.sh", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh",
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt_mac_keychain.sh.asc",
			FilePath: fmt.Sprintf("%s/install_dbt_mac_keychain.sh.asc", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh.asc",
			Repo:     "dbt",
		},
	}

	hostname := "127.0.0.1"
	for _, f := range files {
		f.TestURL = fmt.Sprintf("http://%s:%d%s", hostname, port, f.URLPath)
		testFiles[f.Name] = f
	}

	return testFiles
}
func createTestFilesB(toolRoot string, version string, dbtRoot string) (testFiles map[string]*testFile) {
	testFiles = make(map[string]*testFile)
	files := []*testFile{
		{
			Name:     "catalog-description.txt",
			FilePath: fmt.Sprintf("%s/catalog/%s/description.txt", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/description.txt", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog-description.txt.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/description.txt.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/description.txt.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "dbt_linux_amd64",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt", version),
			Repo:     "dbt",
		},
		{
			Name:     "dbt_linux_amd64.asc",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt.asc", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt.asc", version),
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt.sh",
			FilePath: fmt.Sprintf("%s/install_dbt.sh", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh",
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt.sh.asc",
			FilePath: fmt.Sprintf("%s/install_dbt.sh.asc", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh.asc",
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt_mac_keychain.sh",
			FilePath: fmt.Sprintf("%s/install_dbt_mac_keychain.sh", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh",
			Repo:     "dbt",
		},
		{
			Name:     "install_dbt_mac_keychain.sh.asc",
			FilePath: fmt.Sprintf("%s/install_dbt_mac_keychain.sh.asc", dbtRoot),
			URLPath:  "/dbt/install_dbt.sh.asc",
			Repo:     "dbt",
		},
	}

	hostname := "127.0.0.1"
	for _, f := range files {
		f.TestURL = fmt.Sprintf("http://%s:%d%s", hostname, port, f.URLPath)
		testFiles[f.Name] = f
	}

	return testFiles
}

func createTestFilesC(toolRoot string, version string, dbtRoot string) (testFiles map[string]*testFile) {
	testFiles = make(map[string]*testFile)
	files := []*testFile{
		// Attestation files (latest version only — older versions double as
		// the no-attestation fixtures for the policy tests).
		{
			Name:     "catalog_linux_amd64.att.json",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog.att.json", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog.att.json", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64.att.json.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog.att.json.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog.att.json.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64.sbom.json",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog.sbom.json", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog.sbom.json", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64.sbom.json.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog.sbom.json.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog.sbom.json.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "dbt_linux_amd64.att.json",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt.att.json", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt.att.json", version),
			Repo:     "dbt",
		},
		{
			Name:     "dbt_linux_amd64.att.json.asc",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt.att.json.asc", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt.att.json.asc", version),
			Repo:     "dbt",
		},
		{
			Name:     "dbt_linux_amd64.sbom.json",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt.sbom.json", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt.sbom.json", version),
			Repo:     "dbt",
		},
		{
			Name:     "dbt_linux_amd64.sbom.json.asc",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt.sbom.json.asc", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt.sbom.json.asc", version),
			Repo:     "dbt",
		},
		{
			Name:     "catalog-description.txt",
			FilePath: fmt.Sprintf("%s/catalog/%s/description.txt", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/description.txt", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog-description.txt.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/description.txt.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/description.txt.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "catalog_linux_amd64.asc",
			FilePath: fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog.asc", toolRoot, version),
			URLPath:  fmt.Sprintf("/dbt-tools/catalog/%s/linux/amd64/catalog.asc", version),
			Repo:     "dbt-tools",
		},
		{
			Name:     "dbt_linux_amd64",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt", version),
			Repo:     "dbt",
		},
		{
			Name:     "dbt_linux_amd64.asc",
			FilePath: fmt.Sprintf("%s/%s/linux/amd64/dbt.asc", dbtRoot, version),
			URLPath:  fmt.Sprintf("/dbt/%s/linux/amd64/dbt.asc", version),
			Repo:     "dbt",
		},
	}

	hostname := "127.0.0.1"
	for _, f := range files {
		f.TestURL = fmt.Sprintf("http://%s:%d%s", hostname, port, f.URLPath)
		testFiles[f.Name] = f
	}

	return testFiles
}

// buildTestRepo creates the test repository using pre-built fixtures.
// This replaces the slow gomason compilation with static fixtures.
func buildTestRepo() (err error) {
	fmt.Printf("Building test repo using pre-built fixtures\n")

	// Set up the test repository from fixtures
	trustfileContents, err = testfixtures.SetupTestRepo(tmpDir)
	if err != nil {
		err = errors.Wrapf(err, "failed to set up test repo from fixtures")
		return err
	}

	// Upload fixtures to fake S3
	err = uploadFixturesToS3()
	if err != nil {
		err = errors.Wrapf(err, "failed to upload fixtures to S3")
		return err
	}

	fmt.Printf("Test repo built successfully using fixtures\n")
	return err
}

// uploadFixturesToS3 uploads the test fixtures to the fake S3 endpoint.
//
//nolint:gocognit // S3 fixture upload requires multiple sequential operations
func uploadFixturesToS3() (err error) {
	s3Client := s3.New(s3Session)

	// Upload truststore to S3
	_, err = s3Client.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("dbt"),
		Key:    aws.String("truststore"),
		Body:   bytes.NewReader([]byte(trustfileContents)),
	})
	if err != nil {
		err = errors.Wrapf(err, "failed to upload truststore to S3")
		return err
	}

	// Upload all test files to S3
	for _, filesMap := range []map[string]*testFile{testFilesA, testFilesB, testFilesC} {
		for _, f := range filesMap {
			// Read the file from the filesystem (already written by SetupTestRepo)
			content, readErr := os.ReadFile(f.FilePath)
			if readErr != nil {
				// File might not exist if it's a duplicate (like install scripts), skip
				continue
			}

			key := strings.TrimPrefix(f.URLPath, fmt.Sprintf("/%s/", f.Repo))

			// Upload to S3
			_, putErr := s3Client.PutObject(&s3.PutObjectInput{
				Bucket: aws.String(f.Repo),
				Key:    aws.String(key),
				Body:   bytes.NewReader(content),
			})
			if putErr != nil {
				err = errors.Wrapf(putErr, "failed to upload %s to S3", key)
				return err
			}

			// Create directory markers in S3
			dirs, dirErr := DirsForURL(key)
			if dirErr != nil {
				continue
			}

			headSvc := s3.New(s3Session)
			for _, d := range dirs {
				if d != "." {
					path := fmt.Sprintf("%s/", d)
					headOptions := &s3.HeadObjectInput{
						Bucket: aws.String(f.Repo),
						Key:    aws.String(path),
					}

					_, headErr := headSvc.HeadObject(headOptions)
					if headErr != nil {
						// Create the folder marker
						_, _ = s3Client.PutObject(&s3.PutObjectInput{
							Bucket: aws.String(f.Repo),
							Key:    aws.String(path),
						})
					}
				}
			}

			// Upload checksum file
			checksumPath := f.FilePath + ".sha256"
			checksumContent, checksumErr := os.ReadFile(checksumPath)
			if checksumErr == nil {
				_, _ = s3Client.PutObject(&s3.PutObjectInput{
					Bucket: aws.String(f.Repo),
					Key:    aws.String(key + ".sha256"),
					Body:   bytes.NewReader(checksumContent),
				})
			}
		}
	}

	return err
}
