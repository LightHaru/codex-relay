// router-updater downloads and installs a verified source release for the
// Windows Codex Relay. It is intentionally a small, separate
// executable: the running Router app can quit before its managed files are
// replaced, while this helper lives outside the managed app directory.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	manifestSchema       = 1
	productName          = "codex-subscription-router"
	maxManifestBytes     = 1 << 20
	maxDownloadBytes     = 512 << 20
	maxExtractedBytes    = 1 << 30
	maxExtractedFiles    = 50_000
	parentWaitTimeout    = 2 * time.Minute
	parentPollInterval   = 500 * time.Millisecond
	installerTimeout     = 30 * time.Minute
	defaultManifestURL   = "https://github.com/LightHaru/codex-relay/releases/latest/download/windows-update.json"
	updaterDirectoryName = "Codex Relay Updater"
)

var allowedHosts = map[string]struct{}{
	"github.com":                           {},
	"objects.githubusercontent.com":        {},
	"release-assets.githubusercontent.com": {},
	"raw.githubusercontent.com":            {},
}

type manifest struct {
	Schema       int    `json:"schema"`
	Product      string `json:"product"`
	Version      string `json:"version"`
	SourceURL    string `json:"sourceUrl"`
	SourceSHA256 string `json:"sourceSha256"`
	ReleaseURL   string `json:"releaseUrl"`
	Notes        string `json:"notes"`
}

type options struct {
	manifestURL string
	installRoot string
	profile     string
	parentPID   int
	current     string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "router-updater:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("router-updater", flag.ContinueOnError)
	manifestURL := flags.String("manifest-url", defaultManifestURL, "HTTPS update manifest URL")
	installRoot := flags.String("install-root", "", "managed Router install root")
	profile := flags.String("profile", "", "Router Electron profile directory")
	parentPID := flags.Int("parent-pid", 0, "PID of the Router process that is quitting")
	current := flags.String("current-version", "0.0.0", "currently running Router version")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("the Windows updater cannot run on %s", runtime.GOOS)
	}
	if strings.TrimSpace(*installRoot) == "" || strings.TrimSpace(*profile) == "" {
		return errors.New("--install-root and --profile are required")
	}
	return update(options{
		manifestURL: strings.TrimSpace(*manifestURL),
		installRoot: *installRoot,
		profile:     *profile,
		parentPID:   *parentPID,
		current:     *current,
	})
}

func update(opts options) error {
	root, err := filepath.Abs(opts.installRoot)
	if err != nil {
		return fmt.Errorf("resolve install root: %w", err)
	}
	profile, err := filepath.Abs(opts.profile)
	if err != nil {
		return fmt.Errorf("resolve profile: %w", err)
	}
	if err := validateHTTPSURL(opts.manifestURL, true); err != nil {
		return fmt.Errorf("manifest URL: %w", err)
	}
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return errors.New("too many update redirects")
			}
			return validateHTTPSURL(req.URL.String(), true)
		},
	}
	body, err := downloadLimited(client, opts.manifestURL, maxManifestBytes)
	if err != nil {
		return fmt.Errorf("download update manifest: %w", err)
	}
	var release manifest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		return fmt.Errorf("decode update manifest: %w", err)
	}
	if err := validateManifest(release); err != nil {
		return err
	}
	newer, err := compareVersions(release.Version, opts.current)
	if err != nil {
		return fmt.Errorf("compare versions: %w", err)
	}
	if !newer {
		return nil
	}

	if opts.parentPID > 0 {
		if err := waitForProcessExit(opts.parentPID, parentWaitTimeout); err != nil {
			return err
		}
	}

	working, err := os.MkdirTemp("", "codex-mux-update-")
	if err != nil {
		return fmt.Errorf("create update staging directory: %w", err)
	}
	defer os.RemoveAll(working)
	archivePath := filepath.Join(working, "source.zip")
	if _, err := downloadToFile(client, release.SourceURL, archivePath, maxDownloadBytes); err != nil {
		return fmt.Errorf("download source release: %w", err)
	}
	if got, err := fileSHA256(archivePath); err != nil {
		return fmt.Errorf("hash source release: %w", err)
	} else if !strings.EqualFold(got, release.SourceSHA256) {
		return fmt.Errorf("source release hash mismatch: got %s, expected %s", got, release.SourceSHA256)
	}
	extracted := filepath.Join(working, "source")
	if err := extractZipSafe(archivePath, extracted); err != nil {
		return fmt.Errorf("extract source release: %w", err)
	}
	sourceRoot, err := findSourceRoot(extracted)
	if err != nil {
		return err
	}
	if err := runInstaller(sourceRoot, opts.installRoot, profile); err != nil {
		// A failed update must leave the user with a usable Router window.
		_ = launchCurrent(root, profile)
		return err
	}
	return nil
}

func validateManifest(value manifest) error {
	if value.Schema != manifestSchema {
		return fmt.Errorf("unsupported update manifest schema %d", value.Schema)
	}
	if value.Product != productName {
		return fmt.Errorf("unexpected update product %q", value.Product)
	}
	if _, err := parseVersion(value.Version); err != nil {
		return fmt.Errorf("invalid update version: %w", err)
	}
	if err := validateHTTPSURL(value.SourceURL, false); err != nil {
		return fmt.Errorf("source URL: %w", err)
	}
	if err := validateHTTPSURL(value.ReleaseURL, true); err != nil {
		return fmt.Errorf("release URL: %w", err)
	}
	decoded, err := hex.DecodeString(value.SourceSHA256)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("sourceSha256 must be a 64-character hexadecimal SHA-256 value")
	}
	if len(value.Notes) > 16_384 {
		return errors.New("update notes are too large")
	}
	return nil
}

func validateHTTPSURL(raw string, allowManifestPath bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
		return errors.New("must be an HTTPS URL without credentials")
	}
	host := strings.ToLower(parsed.Hostname())
	if _, ok := allowedHosts[host]; !ok {
		return fmt.Errorf("host %q is not an approved GitHub host", host)
	}
	if parsed.RawQuery != "" && !allowManifestPath {
		// GitHub release asset URLs may carry a short-lived query signature.
		// They remain safe because the hostname is still allow-listed.
	}
	return nil
}

func downloadLimited(client *http.Client, rawURL string, limit int64) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned %s", response.Status)
	}
	reader := io.LimitReader(response.Body, limit+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func downloadToFile(client *http.Client, rawURL, destination string, limit int64) (int64, error) {
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("server returned %s", response.Status)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	written, err := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return written, err
	}
	if written > limit {
		return written, fmt.Errorf("download exceeds %d bytes", limit)
	}
	return written, file.Sync()
}

func extractZipSafe(archivePath, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	var total int64
	for index, entry := range archive.File {
		if index >= maxExtractedFiles {
			return fmt.Errorf("source archive contains more than %d files", maxExtractedFiles)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source archive contains a symlink: %s", entry.Name)
		}
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe source archive path: %s", entry.Name)
		}
		target := filepath.Join(root, name)
		if !isWithinPath(target, root) {
			return fmt.Errorf("unsafe source archive path: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(maxExtractedBytes-total) {
			return errors.New("source archive expands beyond the safety limit")
		}
		total += int64(entry.UncompressedSize64)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
		if err != nil {
			reader.Close()
			return err
		}
		_, copyErr := io.CopyN(file, reader, int64(entry.UncompressedSize64))
		closeErr := errors.Join(file.Close(), reader.Close())
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func findSourceRoot(extracted string) (string, error) {
	root, err := filepath.Abs(extracted)
	if err != nil {
		return "", err
	}
	var matches []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || info.Name() != "install_windows.ps1" {
			return nil
		}
		candidate := filepath.Dir(filepath.Dir(path))
		if filepath.Base(candidate) == "scripts" {
			candidate = filepath.Dir(candidate)
		}
		if isWithinPath(candidate, root) {
			matches = append(matches, candidate)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inspect source release: %w", err)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("source release must contain exactly one scripts/install_windows.ps1 (found %d)", len(matches))
	}
	return matches[0], nil
}

func runInstaller(sourceRoot, installRoot, profile string) error {
	logPath := filepath.Join(updaterDirectory(), "update.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	if runtime.GOOS != "windows" {
		return errors.New("PowerShell installer is available only on Windows")
	}
	args := []string{
		"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass", "-File", filepath.Join(sourceRoot, "scripts", "install_windows.ps1"),
	}
	command := exec.Command("powershell.exe", args...)
	command.Dir = sourceRoot
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = append(os.Environ(), "CODEX_MUX_UPDATE_PROFILE="+profile)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Windows installer: %w", err)
	}
	if err := waitCommand(command, installerTimeout); err != nil {
		return fmt.Errorf("Windows installer failed: %w", err)
	}
	return nil
}

func waitCommand(command *exec.Cmd, timeout time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- command.Wait() }()
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		_ = command.Process.Kill()
		return errors.New("timed out")
	}
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return nil
		}
		time.Sleep(parentPollInterval)
	}
	return fmt.Errorf("Router process %d did not exit before the update timeout", pid)
}

func processExists(pid int) bool {
	if runtime.GOOS == "windows" {
		output, err := exec.Command("tasklist.exe", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return false
		}
		text := string(output)
		return strings.Contains(text, `"`+strconv.Itoa(pid)+`"`)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func launchCurrent(root, profile string) error {
	// The Electron main process passes the managed app directory (the parent
	// of resources), not the outer `%LOCALAPPDATA%\\Codex Relay`
	// directory.  Keeping that contract explicit avoids ever launching the
	// Microsoft Store copy after a failed update.
	executable := filepath.Join(root, "ChatGPT.exe")
	if _, err := os.Stat(executable); err != nil {
		return err
	}
	command := exec.Command(executable, "--user-data-dir="+profile)
	command.Dir = filepath.Dir(executable)
	return command.Start()
}

func updaterDirectory() string {
	base := os.Getenv("LOCALAPPDATA")
	if strings.TrimSpace(base) == "" {
		base = filepath.Join(os.TempDir(), "codex-relay")
	}
	return filepath.Join(base, updaterDirectoryName)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func isWithinPath(path, parent string) bool {
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolvedParent, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedParent, resolvedPath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

type parsedVersion struct{ major, minor, patch int }

func parseVersion(value string) (parsedVersion, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return parsedVersion{}, fmt.Errorf("%q is not x.y.z", value)
	}
	parsed := [3]int{}
	for index, part := range parts {
		if part == "" {
			return parsedVersion{}, fmt.Errorf("%q contains an empty component", value)
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return parsedVersion{}, fmt.Errorf("%q contains a non-numeric component", value)
			}
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return parsedVersion{}, fmt.Errorf("%q contains an invalid component", part)
		}
		parsed[index] = value
	}
	return parsedVersion{parsed[0], parsed[1], parsed[2]}, nil
}

func compareVersions(left, right string) (bool, error) {
	a, err := parseVersion(left)
	if err != nil {
		return false, err
	}
	b, err := parseVersion(right)
	if err != nil {
		return false, err
	}
	if a.major != b.major {
		return a.major > b.major, nil
	}
	if a.minor != b.minor {
		return a.minor > b.minor, nil
	}
	return a.patch > b.patch, nil
}
