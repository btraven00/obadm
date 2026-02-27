package sshconn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
)

const (
	githubRepo   = "btraven00/obadm"
	githubTag    = "nightly"
	agentAsset   = "obadm-agent_linux_amd64"
)

// Deploy uploads localBinary to agentPath on the remote host via SFTP.
// cfg must have Host set; User and IdentityFile are resolved from ~/.ssh/config.
func Deploy(ctx context.Context, cfg Config, localBinary string) error {
	r := resolve(cfg)
	if r.identity == "" {
		return fmt.Errorf("no identity file: set --identity or add IdentityFile to ~/.ssh/config for %q", cfg.Host)
	}
	signer, err := loadKey(ctx, r.identity)
	if err != nil {
		return err
	}
	sshClient, jumpClient, err := openSSHClient(ctx, r, signer)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	if jumpClient != nil {
		defer jumpClient.Close()
	}

	sc, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()

	rpath, err := expandRemoteTilde(sc, r.agentPath)
	if err != nil {
		return err
	}
	return uploadViaFTP(sc, localBinary, rpath)
}

// ensureAgent checks whether obadm-agent exists at agentPath on the remote
// host. If not, it downloads the latest linux/amd64 release from GitHub and
// uploads via SFTP. Returns the resolved agent path and resolved remoteFile path
// (both with ~ expanded using the remote home directory).
func ensureAgent(ctx context.Context, sshClient *gossh.Client, agentPath, remoteFile string) (agentRPath, fileRPath string, err error) {
	sc, err := sftp.NewClient(sshClient)
	if err != nil {
		return "", "", fmt.Errorf("sftp client: %w", err)
	}
	defer sc.Close()

	rpath, err := expandRemoteTilde(sc, agentPath)
	if err != nil {
		return "", "", err
	}

	fileRPath, err = expandRemoteTilde(sc, remoteFile)
	if err != nil {
		return "", "", err
	}

	if _, err := sc.Stat(rpath); err == nil {
		log.Printf("agent found at %s", rpath)
		return rpath, fileRPath, nil
	}

	log.Printf("agent not found at %s — downloading...", rpath)

	tmpPath, err := downloadLatestAgent(ctx)
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmpPath)

	if err := uploadViaFTP(sc, tmpPath, rpath); err != nil {
		return "", "", err
	}

	return rpath, fileRPath, nil
}

// expandRemoteTilde replaces a leading ~/ with the SFTP working directory
// (which openssh sets to the user's home directory).
func expandRemoteTilde(sc *sftp.Client, p string) (string, error) {
	if !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := sc.Getwd()
	if err != nil {
		return "", fmt.Errorf("sftp getwd: %w", err)
	}
	return home + p[1:], nil
}

type githubRelease struct {
	Assets []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func downloadLatestAgent(ctx context.Context) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", githubRepo, githubTag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github releases API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases API %s (repo: %s, tag: %s)", resp.Status, githubRepo, githubTag)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("decode release: %w", err)
	}

	var dlURL string
	for _, a := range rel.Assets {
		if a.Name == agentAsset {
			dlURL = a.BrowserDownloadURL
			break
		}
	}
	if dlURL == "" {
		return "", fmt.Errorf("asset %q not found in latest release", agentAsset)
	}

	log.Printf("downloading %s...", dlURL)
	return downloadToTemp(ctx, dlURL)
}

func downloadToTemp(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}

	f, err := os.CreateTemp("", "obadm-agent-*")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write temp: %w", err)
	}

	return f.Name(), nil
}

func uploadViaFTP(sc *sftp.Client, localPath, remotePath string) error {
	if err := sc.MkdirAll(path.Dir(remotePath)); err != nil {
		return fmt.Errorf("mkdir %s: %w", path.Dir(remotePath), err)
	}

	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := sc.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("sftp create %s: %w", remotePath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("sftp copy: %w", err)
	}

	if err := sc.Chmod(remotePath, 0o755); err != nil {
		return fmt.Errorf("chmod +x: %w", err)
	}

	log.Printf("uploaded obadm-agent → %s", remotePath)
	return nil
}
