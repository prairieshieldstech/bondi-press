package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// appVersion is stamped at release build time (-ldflags "-X main.appVersion=v0.3.1").
// Local/dev builds keep "dev" and never offer updates.
var appVersion = "dev"

const (
	releasesAPI = "https://api.github.com/repos/prairieshieldstech/bondi-press/releases/latest"
	releasesURL = "https://github.com/prairieshieldstech/bondi-press/releases/latest"
)

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`     // release page
	CanAuto   bool   `json:"canAuto"` // one-click install supported here
	assetURL  string
}

func (a *App) GetVersion() string { return appVersion }

// parseVer turns "v1.2.3" into [1 2 3]; ok=false for non-release strings.
func parseVer(s string) (v [3]int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func newer(latest, current [3]int) bool {
	for i := range latest {
		if latest[i] != current[i] {
			return latest[i] > current[i]
		}
	}
	return false
}

func fetchLatest(ctx context.Context) (*UpdateInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", releasesAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bondi-press/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("update check: HTTP %d", resp.StatusCode)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	info := &UpdateInfo{Current: appVersion, Latest: rel.Tag, URL: releasesURL}
	for _, as := range rel.Assets {
		if as.Name == "bondi-press-Windows.zip" {
			info.assetURL = as.URL
		}
	}
	cur, curOK := parseVer(appVersion)
	lat, latOK := parseVer(rel.Tag)
	info.Available = curOK && latOK && newer(lat, cur)
	info.CanAuto = goruntime.GOOS == "windows" && info.assetURL != ""
	return info, nil
}

// CheckUpdate asks GitHub whether a newer release exists.
func (a *App) CheckUpdate() (*UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return fetchLatest(ctx)
}

// InstallUpdate downloads the latest Windows build, swaps it in for the
// running exe (Windows lets a running exe be renamed, not overwritten), and
// relaunches. Any failure rolls back and returns an error; the frontend then
// falls back to opening the release page.
func (a *App) InstallUpdate() error {
	if goruntime.GOOS != "windows" {
		return fmt.Errorf("one-click update is Windows-only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	info, err := fetchLatest(ctx)
	if err != nil {
		return err
	}
	if !info.Available || info.assetURL == "" {
		return fmt.Errorf("already up to date")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", info.assetURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var newExe []byte
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			newExe, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
			break
		}
	}
	if len(newExe) < 1<<20 || string(newExe[:2]) != "MZ" {
		return fmt.Errorf("downloaded update is not a valid executable")
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	oldExe := exe + ".old"
	os.Remove(oldExe)
	if err := os.Rename(exe, oldExe); err != nil {
		return fmt.Errorf("can't replace the app here (%w)", err) // e.g. installed under Program Files
	}
	if err := os.WriteFile(exe, newExe, 0o755); err != nil {
		os.Remove(exe)
		os.Rename(oldExe, exe)
		return err
	}
	cmd := exec.Command(exe)
	if err := cmd.Start(); err != nil {
		os.Remove(exe)
		os.Rename(oldExe, exe)
		return err
	}
	runtime.Quit(a.ctx)
	return nil
}

// cleanupOldExe removes the previous version left behind by InstallUpdate.
func cleanupOldExe() {
	if goruntime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		go func() {
			for i := 0; i < 10; i++ { // old process may still be exiting
				if os.Remove(exe+".old") == nil || !fileExists(exe+".old") {
					return
				}
				time.Sleep(time.Second)
			}
		}()
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
