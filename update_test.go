package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    int
		wantErr bool
	}{
		{current: "v1.2.3", latest: "v1.2.4", want: -1},
		{current: "v1.2.4", latest: "v1.2.4", want: 0},
		{current: "v1.10.0", latest: "v1.9.9", want: 1},
		{current: "1.2.3", latest: "v1.2.3", want: 0},
		{current: "v1.2.3-rc.1", latest: "v1.2.3", want: -1},
		{current: "v1.2.3", latest: "v1.2.3-rc.1", want: 1},
		{current: "v1.2.3-rc.2", latest: "v1.2.3-rc.10", want: -1},
		{current: "v1.2.3+build.1", latest: "v1.2.3+build.2", want: 0},
		{current: "dev", latest: "v1.2.3", wantErr: true},
		{current: "1.2", latest: "v1.2.3", wantErr: true},
		{current: "v1.2.3", latest: "latest", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.current+"->"+tt.latest, func(t *testing.T) {
			got, err := compareVersions(tt.current, tt.latest)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestAssetNameFor(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
		wantErr      bool
	}{
		{goos: "linux", goarch: "amd64", want: "nodeseek-rss-monitor-linux-amd64"},
		{goos: "linux", goarch: "arm64", want: "nodeseek-rss-monitor-linux-arm64"},
		{goos: "darwin", goarch: "arm64", want: "nodeseek-rss-monitor-darwin-arm64"},
		{goos: "windows", goarch: "amd64", want: "nodeseek-rss-monitor-windows-amd64.exe"},
		{goos: "windows", goarch: "arm64", want: "nodeseek-rss-monitor-windows-arm64.exe"},
		{goos: "freebsd", goarch: "amd64", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			got, err := assetNameFor(tt.goos, tt.goarch)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("assetNameFor = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseChecksums(t *testing.T) {
	hash := strings.Repeat("a", 64)
	data := []byte("# comment\n" + hash + "  file-one\n" + hash + " *file-two\ninvalid line\n")
	sums, err := parseChecksums(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sums["file-one"] != hash || sums["file-two"] != hash {
		t.Fatalf("unexpected checksums: %#v", sums)
	}
	if _, err := parseChecksums([]byte("not a checksum")); err == nil {
		t.Fatal("expected error for empty checksums")
	}
}

type updateServerConfig struct {
	tag             string
	binary          []byte
	assetName       string
	checksum        string
	downloadStatus  int
	prerelease      bool
	omitChecksums   bool
	tagLookupStatus int
}

func newUpdateTestServer(t *testing.T, cfg updateServerConfig) *httptest.Server {
	t.Helper()
	if cfg.assetName == "" {
		cfg.assetName = "nodeseek-rss-monitor-linux-amd64"
	}
	if cfg.checksum == "" {
		sum := sha256.Sum256(cfg.binary)
		cfg.checksum = hex.EncodeToString(sum[:])
	}
	if cfg.tag == "" {
		cfg.tag = "v1.2.4"
	}

	var srv *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+updateRepo+"/releases/latest":
			writeReleaseJSON(w, cfg, srv.URL)
		case strings.HasPrefix(r.URL.Path, "/repos/"+updateRepo+"/releases/tags/"):
			if cfg.tagLookupStatus != 0 && cfg.tagLookupStatus != http.StatusOK {
				w.WriteHeader(cfg.tagLookupStatus)
				return
			}
			if path.Base(r.URL.Path) != cfg.tag {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeReleaseJSON(w, cfg, srv.URL)
		case r.URL.Path == "/download/"+cfg.assetName:
			if cfg.downloadStatus != 0 && cfg.downloadStatus != http.StatusOK {
				w.WriteHeader(cfg.downloadStatus)
				return
			}
			_, _ = w.Write(cfg.binary)
		case r.URL.Path == "/download/checksums.txt":
			if cfg.omitChecksums {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(w, "%s  %s\n", cfg.checksum, cfg.assetName)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv = httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func writeReleaseJSON(w http.ResponseWriter, cfg updateServerConfig, base string) {
	assets := []ReleaseAsset{}
	if !cfg.omitChecksums {
		assets = append(assets, ReleaseAsset{
			Name:               checksumsAssetName,
			BrowserDownloadURL: base + "/download/checksums.txt",
		})
	}
	assets = append(assets, ReleaseAsset{
		Name:               cfg.assetName,
		BrowserDownloadURL: base + "/download/" + cfg.assetName,
		Size:               int64(len(cfg.binary)),
	})
	rel := ReleaseInfo{
		TagName:    cfg.tag,
		Name:       cfg.tag,
		HTMLURL:    base + "/release",
		Prerelease: cfg.prerelease,
		Assets:     assets,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rel)
}

func withUpdateTestGlobals(t *testing.T) {
	t.Helper()
	oldAPI := updateAPIBase
	oldClient := updateHTTPClient
	oldExe := executablePath
	oldGOOS, oldGOARCH := currentGOOS, currentGOARCH
	oldVersion := version

	updateHTTPClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() {
		updateAPIBase = oldAPI
		updateHTTPClient = oldClient
		executablePath = oldExe
		currentGOOS, currentGOARCH = oldGOOS, oldGOARCH
		version = oldVersion
		resetPendingUpdates()
	})
}

func setupFakeExecutable(t *testing.T, content string) (dir, exe string) {
	t.Helper()
	dir = t.TempDir()
	exe = filepath.Join(dir, "app")
	if err := os.WriteFile(exe, []byte(content), 0755); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	executablePath = func() (string, error) { return exe, nil }
	return dir, exe
}

func TestFetchLatestRelease(t *testing.T) {
	withUpdateTestGlobals(t)
	srv := newUpdateTestServer(t, updateServerConfig{tag: "v1.2.4", binary: []byte("new")})
	updateAPIBase = srv.URL

	rel, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.TagName != "v1.2.4" {
		t.Fatalf("unexpected tag: %s", rel.TagName)
	}

	prereleaseSrv := newUpdateTestServer(t, updateServerConfig{tag: "v1.2.4", binary: []byte("new"), prerelease: true})
	updateAPIBase = prereleaseSrv.URL
	if _, err := fetchLatestRelease(context.Background()); err == nil {
		t.Fatal("prerelease must be rejected")
	}

	notFoundSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFoundSrv.Close()
	updateAPIBase = notFoundSrv.URL
	if _, err := fetchLatestRelease(context.Background()); err == nil {
		t.Fatal("404 must be reported")
	}
}

func TestPerformUpdate(t *testing.T) {
	withUpdateTestGlobals(t)
	version = "v1.2.3"
	currentGOOS = "linux"
	currentGOARCH = "amd64"
	dir, exe := setupFakeExecutable(t, "old binary")

	srv := newUpdateTestServer(t, updateServerConfig{tag: "v1.2.4", binary: []byte("new binary")})
	updateAPIBase = srv.URL

	result, err := performUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version != "v1.2.4" || result.ManualReplace {
		t.Fatalf("unexpected result: %#v", result)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read replaced executable: %v", err)
	}
	if string(got) != "new binary" {
		t.Fatalf("executable content = %q, want %q", got, "new binary")
	}
	if result.BackupPath == "" {
		t.Fatal("expected a backup path")
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != "old binary" {
		t.Fatalf("backup content = %q, want %q", backup, "old binary")
	}
	if !strings.HasPrefix(filepath.Base(result.BackupPath), ".nodeseek-backup-") {
		t.Fatalf("backup should be a safe temp file, got %s", result.BackupPath)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Fatalf("replaced executable is not executable: %v", info.Mode())
	}

	// No temp download files should remain.
	matches, _ := filepath.Glob(filepath.Join(dir, ".nodeseek-update-*"))
	if len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
}

func TestPerformUpdateRejectsDowngrade(t *testing.T) {
	withUpdateTestGlobals(t)
	version = "v1.2.4"
	currentGOOS = "linux"
	currentGOARCH = "amd64"
	_, exe := setupFakeExecutable(t, "old binary")

	srv := newUpdateTestServer(t, updateServerConfig{tag: "v1.2.3", binary: []byte("older")})
	updateAPIBase = srv.URL

	if _, err := performUpdate(context.Background()); err == nil {
		t.Fatal("expected downgrade to be rejected")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old binary" {
		t.Fatalf("original executable was modified: %q", got)
	}
}

func TestPerformUpdateChecksumFailureKeepsOriginal(t *testing.T) {
	withUpdateTestGlobals(t)
	version = "v1.2.3"
	currentGOOS = "linux"
	currentGOARCH = "amd64"
	dir, exe := setupFakeExecutable(t, "old binary")

	srv := newUpdateTestServer(t, updateServerConfig{
		tag:      "v1.2.4",
		binary:   []byte("new binary"),
		checksum: strings.Repeat("0", 64),
	})
	updateAPIBase = srv.URL

	if _, err := performUpdate(context.Background()); err == nil {
		t.Fatal("expected checksum failure")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old binary" {
		t.Fatalf("original executable was modified: %q", got)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".nodeseek-update-*"))
	if len(matches) != 0 {
		t.Fatalf("leftover temp files after checksum failure: %v", matches)
	}
}

func TestPerformUpdateDownloadFailureKeepsOriginal(t *testing.T) {
	withUpdateTestGlobals(t)
	version = "v1.2.3"
	currentGOOS = "linux"
	currentGOARCH = "amd64"
	_, exe := setupFakeExecutable(t, "old binary")

	srv := newUpdateTestServer(t, updateServerConfig{
		tag:            "v1.2.4",
		binary:         []byte("new binary"),
		downloadStatus: http.StatusInternalServerError,
	})
	updateAPIBase = srv.URL

	if _, err := performUpdate(context.Background()); err == nil {
		t.Fatal("expected download failure")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old binary" {
		t.Fatalf("original executable was modified: %q", got)
	}
}

func TestPerformUpdateMissingChecksums(t *testing.T) {
	withUpdateTestGlobals(t)
	version = "v1.2.3"
	currentGOOS = "linux"
	currentGOARCH = "amd64"
	_, exe := setupFakeExecutable(t, "old binary")

	srv := newUpdateTestServer(t, updateServerConfig{
		tag:           "v1.2.4",
		binary:        []byte("new binary"),
		omitChecksums: true,
	})
	updateAPIBase = srv.URL

	if _, err := performUpdate(context.Background()); err == nil {
		t.Fatal("expected missing checksums to be rejected")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old binary" {
		t.Fatalf("original executable was modified: %q", got)
	}
}

func TestUpdateConfirmationBinding(t *testing.T) {
	withUpdateTestGlobals(t)
	version = "v1.2.3"
	currentGOOS = "linux"
	currentGOARCH = "amd64"

	srv := newUpdateTestServer(t, updateServerConfig{tag: "v1.2.4", binary: []byte("new")})
	updateAPIBase = srv.URL
	rel, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	p, err := prepareUpdateForChat(context.Background(), rel, 123, 42, 123)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if p.Tag != "v1.2.4" || p.AssetName != "nodeseek-rss-monitor-linux-amd64" || len(p.Checksum) != 64 {
		t.Fatalf("unexpected pending update: %#v", p)
	}
	if len("m:upd:"+p.Nonce) > callbackDataMaxLen {
		t.Fatalf("nonce callback data too long: %d", len("m:upd:"+p.Nonce))
	}

	if got := takePendingUpdate(p.Nonce, 999, 42, 123); got != nil {
		t.Fatal("confirmation must not be usable from another chat")
	}
	if got := takePendingUpdate(p.Nonce, 123, 99, 123); got != nil {
		t.Fatal("confirmation must not be usable from another message")
	}
	if got := takePendingUpdate(p.Nonce, 123, 42, 999); got != nil {
		t.Fatal("confirmation must not be usable by another user")
	}
	if got := takePendingUpdate(p.Nonce, 123, 42, 123); got == nil {
		t.Fatal("valid confirmation should be accepted")
	}
	if got := takePendingUpdate(p.Nonce, 123, 42, 123); got != nil {
		t.Fatal("confirmation must be single-use")
	}

	expired := &pendingUpdate{
		Nonce: "expired", ChatID: 123, MessageID: 42, UserID: 123,
		Tag: "v1.2.4", AssetName: "x", Checksum: strings.Repeat("a", 64),
		Expires: time.Now().Add(-time.Minute),
	}
	storePendingUpdate(expired)
	if got := takePendingUpdate("expired", 123, 42, 123); got != nil {
		t.Fatal("expired confirmation must be rejected")
	}
}

func TestInstallPreparedUpdateRejectsChangedChecksum(t *testing.T) {
	withUpdateTestGlobals(t)
	version = "v1.2.3"
	currentGOOS = "linux"
	currentGOARCH = "amd64"
	_, exe := setupFakeExecutable(t, "old binary")

	srv := newUpdateTestServer(t, updateServerConfig{tag: "v1.2.4", binary: []byte("new binary")})
	updateAPIBase = srv.URL

	p := &pendingUpdate{
		Nonce:     "n",
		ChatID:    123,
		MessageID: 42,
		UserID:    123,
		Tag:       "v1.2.4",
		AssetName: "nodeseek-rss-monitor-linux-amd64",
		Checksum:  strings.Repeat("a", 64),
		Expires:   time.Now().Add(time.Minute),
	}
	if _, err := installPreparedUpdate(context.Background(), p); err == nil {
		t.Fatal("expected checksum change to be rejected")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old binary" {
		t.Fatalf("original executable was modified: %q", got)
	}
}

func TestBackupFailureRefusesReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows installBinary saves a .new file instead of creating a backup")
	}
	dir := t.TempDir()
	temp := filepath.Join(dir, "downloaded")
	if err := os.WriteFile(temp, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}

	// Passing a directory as execPath makes createExecutableBackup fail.
	installed, backup, _, err := installBinary(temp, dir)
	if err == nil {
		t.Fatal("expected backup failure")
	}
	if installed || backup != "" {
		t.Fatalf("replacement must be refused, got installed=%v backup=%q", installed, backup)
	}
	if !strings.Contains(err.Error(), "备份") {
		t.Fatalf("error should mention backup: %v", err)
	}
	if _, statErr := os.Stat(temp); statErr != nil {
		t.Fatalf("temp file should be left for the caller to clean up: %v", statErr)
	}
}

func TestRestartRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support in-place replacement")
	}
	dir := t.TempDir()
	execPath := filepath.Join(dir, "app")
	backupPath := filepath.Join(dir, ".nodeseek-backup-test")
	if err := os.WriteFile(backupPath, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}

	rolledBack, err := restartProcess(execPath, backupPath)
	if err == nil {
		t.Fatal("expected exec to fail for a missing executable")
	}
	if !rolledBack {
		t.Fatalf("expected rollback to succeed: %v", err)
	}
	got, readErr := os.ReadFile(execPath)
	if readErr != nil {
		t.Fatalf("rollback did not restore executable: %v", readErr)
	}
	if string(got) != "old binary" {
		t.Fatalf("rollback content = %q", got)
	}
}

func TestUpdateGuard(t *testing.T) {
	updateRunning.Store(false)
	t.Cleanup(func() { updateRunning.Store(false) })

	if !beginUpdate() {
		t.Fatal("first update should start")
	}
	if beginUpdate() {
		t.Fatal("second concurrent update must be rejected")
	}
	endUpdate()
	if !beginUpdate() {
		t.Fatal("update should be startable again")
	}
	endUpdate()
}
