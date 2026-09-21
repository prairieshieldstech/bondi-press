package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// One-time engine setup. Bondi Press needs LibreOffice (pub import) plus a
// Python with pdf2docx/PyMuPDF (page render + DOCX). Rather than bloating the
// download by bundling them, we install whatever is missing on first run via
// winget (built into Windows 10 1809+/11) and pip.

var engineMu sync.Mutex

func (a *App) engineProgress(msg string) {
	runtime.EventsEmit(a.ctx, "engine:progress", msg)
}

// realPythonCandidates returns python.exe locations for a per-user or system
// python.org install (winget's Python.Python.3.x installs per-user, and the
// running app's PATH won't include it until restart).
func realPythonCandidates() []string {
	var out []string
	if goruntime.GOOS != "windows" {
		return out
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		m, _ := filepath.Glob(filepath.Join(base, "Programs", "Python", "Python3*", "python.exe"))
		out = append(out, m...)
	}
	for _, env := range []string{"ProgramFiles", "ProgramW6432"} {
		if base := os.Getenv(env); base != "" {
			m, _ := filepath.Glob(filepath.Join(base, "Python3*", "python.exe"))
			out = append(out, m...)
		}
	}
	return out
}

func runLogged(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	hideConsole(cmd)
	err := cmd.Run()
	return buf.String(), err
}

func wingetInstall(ctx context.Context, id string) error {
	out, err := runLogged(ctx, "winget", "install", "-e", "--id", id,
		"--silent", "--accept-package-agreements", "--accept-source-agreements")
	if err != nil {
		// winget exits non-zero when the package is already installed/up to date.
		l := strings.ToLower(out)
		if strings.Contains(l, "already installed") || strings.Contains(l, "no newer package") {
			return nil
		}
		return fmt.Errorf("winget could not install %s: %s", id, strings.TrimSpace(tail(out, 400)))
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// InstallEngine installs whatever conversion components are missing. Windows
// only; other platforms return instructions. Emits "engine:progress" events.
func (a *App) InstallEngine() error {
	if !engineMu.TryLock() {
		return fmt.Errorf("setup is already running")
	}
	defer engineMu.Unlock()

	if goruntime.GOOS != "windows" {
		return fmt.Errorf("automatic setup is Windows-only. Install LibreOffice (libreoffice.org) and run: pip3 install pdf2docx pymupdf")
	}
	if _, err := exec.LookPath("winget"); err != nil {
		return fmt.Errorf("Windows Package Manager (winget) is missing. Update 'App Installer' from the Microsoft Store, or install LibreOffice from libreoffice.org and Python from python.org manually")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	if _, err := resolveSoffice(); err != nil {
		a.engineProgress("Installing LibreOffice (approve the Windows prompt)…")
		if err := wingetInstall(ctx, "TheDocumentFoundation.LibreOffice"); err != nil {
			return err
		}
		if _, err := resolveSoffice(); err != nil {
			return fmt.Errorf("LibreOffice installed but not found afterwards; restart Bondi Press")
		}
	}

	if !a.CheckPdf2docx() {
		py := ""
		for _, c := range append(realPythonCandidates(), pythonPathCandidates()...) {
			if pythonRuns(c) {
				py = c
				break
			}
		}
		if py == "" {
			a.engineProgress("Installing Python…")
			if err := wingetInstall(ctx, "Python.Python.3.12"); err != nil {
				return err
			}
			for _, c := range realPythonCandidates() {
				if pythonRuns(c) {
					py = c
					break
				}
			}
			if py == "" {
				return fmt.Errorf("Python installed but not found afterwards; restart Bondi Press")
			}
		}
		a.engineProgress("Installing conversion libraries (a few minutes)…")
		if out, err := runLogged(ctx, py, "-m", "pip", "install", "--disable-pip-version-check", "pdf2docx", "pymupdf"); err != nil {
			return fmt.Errorf("pip install failed: %s", strings.TrimSpace(tail(out, 400)))
		}
		resetPythonCache()
		if !a.CheckPdf2docx() {
			return fmt.Errorf("libraries installed but Python still can't import them; restart Bondi Press")
		}
	}
	if !toolsInstalled() {
		a.engineProgress("Installing Publisher helper tools…")
		if err := installTools(ctx); err != nil {
			return err
		}
	}
	a.engineProgress("Engine ready")
	return nil
}

// pythonRuns reports whether path is a working interpreter (rejects the
// Microsoft Store "python.exe" stub, which exits non-zero without Python).
func pythonRuns(path string) bool {
	cmd := exec.Command(path, "-c", "import sys; sys.exit(0)")
	hideConsole(cmd)
	return cmd.Run() == nil
}

func pythonPathCandidates() []string {
	var out []string
	for _, n := range pythonNames() {
		if p, err := exec.LookPath(n); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// toolsDir is where the Windows libmspub/librsvg helper tools are installed
// (downloaded once by InstallEngine; not part of the app download).
func toolsDir() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" && goruntime.GOOS == "windows" {
		return filepath.Join(base, "BondiPress", "tools")
	}
	return ""
}

// findTool locates a helper CLI on PATH, in the downloaded tools dir, then in
// the given fixed fallbacks. Not cached, so a fresh install is picked up live.
func findTool(name string, fallbacks ...string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	cands := fallbacks
	if d := toolsDir(); d != "" {
		cands = append([]string{filepath.Join(d, name+".exe")}, cands...)
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func toolMissingErr(what, brew string) error {
	if goruntime.GOOS == "windows" {
		return fmt.Errorf("%s not installed — click 'Set up engine' in the status bar", what)
	}
	return fmt.Errorf("%s not found — install it with '%s'", what, brew)
}

// toolEnv points fontconfig (used by rsvg-convert) at the fonts config shipped
// beside the downloaded tools, so text renders with the system's fonts.
func toolEnv() []string {
	env := os.Environ()
	if d := toolsDir(); d != "" {
		conf := filepath.Join(d, "etc", "fonts")
		env = append(env, "FONTCONFIG_PATH="+conf, "FONTCONFIG_FILE="+filepath.Join(conf, "fonts.conf"))
	}
	return env
}

const toolsURL = "https://github.com/prairieshieldstech/bondi-press/releases/latest/download/bondi-tools-windows.zip"

func toolsInstalled() bool {
	return findTool("pub2xhtml") != "" && findTool("rsvg-convert") != "" && findTool("pub2raw") != ""
}

// installTools downloads and unpacks the helper tools zip (a few MB).
func installTools(ctx context.Context) error {
	dir := toolsDir()
	if dir == "" {
		return fmt.Errorf("no LOCALAPPDATA")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", toolsURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download tools: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download tools: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root := filepath.Clean(dir) + string(os.PathSeparator)
	for _, f := range zr.File {
		dst := filepath.Join(dir, f.Name)
		if !strings.HasPrefix(dst, root) { // zip-slip guard
			continue
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(dst, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(dst), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(dst)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
