package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx     context.Context
	logoSVG []byte
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// GetLogo returns the Bondi Press SVG logo for the about/dialog views.
func (a *App) GetLogo() string {
	if a.logoSVG == nil {
		return ""
	}
	return string(a.logoSVG)
}

var formats = []string{"pdf", "docx", "doc", "odt", "rtf", "txt", "png", "jpg", "svg", "html", "odg"}

type formatSpec struct {
	ext  string
	chain string // "draw" = single soffice call; "pdf2docx" = pub→pdf→docx→target
}

var formatMap = map[string]formatSpec{
	"pdf":  {ext: "pdf",  chain: "draw"},
	"png":  {ext: "png",  chain: "draw"},
	"jpg":  {ext: "jpg",  chain: "draw"},
	"svg":  {ext: "svg",  chain: "draw"},
	"html": {ext: "html", chain: "draw"},
	"odg":  {ext: "odg",  chain: "draw"},
	"docx": {ext: "docx", chain: "pdf2docx"},
	"doc":  {ext: "doc",  chain: "pdf2docx"},
	"odt":  {ext: "odt",  chain: "pdf2docx"},
	"rtf":  {ext: "rtf",  chain: "pdf2docx"},
	"txt":  {ext: "txt",  chain: "pdf2docx"},
}

func (a *App) Formats() []string { return formats }

func (a *App) SelectPubFile() (string, error) {
	dlg := runtime.OpenDialogOptions{
		Title: "Select a Publisher file",
		Filters: []runtime.FileFilter{
			{DisplayName: "Publisher files", Pattern: "*.pub"},
		},
	}
	return runtime.OpenFileDialog(a.ctx, dlg)
}

func (a *App) SelectSavePath(pubPath, format string) (string, error) {
	ext := format
	if ext == "jpg" {
		ext = "jpg"
	}
	base := strings.TrimSuffix(filepath.Base(pubPath), filepath.Ext(pubPath))
	dlg := runtime.SaveDialogOptions{
		Title:           "Save converted file",
		DefaultFilename: base + "." + ext,
		Filters: []runtime.FileFilter{
			{DisplayName: strings.ToUpper(ext) + " files", Pattern: "*." + ext},
		},
	}
	return runtime.SaveFileDialog(a.ctx, dlg)
}

// ConvertResult reports the outcome of one file in a batch.
type ConvertResult struct {
	Input  string `json:"input"`
	Output string `json:"output"`
	Status string `json:"status"` // "ok" | "error"
	Error  string `json:"error,omitempty"`
}

// ConvertBatch converts many .pub files into a shared "Bondi Press exports"
// folder in the user's Downloads directory.
func (a *App) ConvertBatch(pubPaths []string, format string) []ConvertResult {
	if format == "" {
		format = "pdf"
	}
	results := make([]ConvertResult, 0, len(pubPaths))
	if len(pubPaths) == 0 {
		return results
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home, _ = os.Getwd()
	}
	outDir := filepath.Join(home, "Downloads", "Bondi Press exports")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		for _, p := range pubPaths {
			results = append(results, ConvertResult{Input: p, Status: "error", Error: err.Error()})
		}
		return results
	}

	for _, p := range pubPaths {
		outPath := expectedOut(outDir, strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), format)
		r := ConvertResult{Input: p}
		if _, err := a.convertTo(p, outPath, format); err != nil {
			r.Status = "error"
			r.Error = err.Error()
		} else {
			r.Status = "ok"
			r.Output = outPath
		}
		results = append(results, r)
	}
	return results
}

// CheckSoffice returns true if LibreOffice is installed and reachable.
func (a *App) CheckSoffice() (bool, string) {
	p, err := exec.LookPath("soffice")
	if err != nil {
		// Try common macOS path
		candidate := "/Applications/LibreOffice.app/Contents/MacOS/soffice"
		if _, stat := os.Stat(candidate); stat == nil {
			return true, candidate
		}
		return false, ""
	}
	return true, p
}

// CheckPdf2docx returns true if pdf2docx is importable in python3.
func (a *App) CheckPdf2docx() bool {
	cmd := exec.Command("python3", "-c", "from pdf2docx import Converter")
	return cmd.Run() == nil
}

func (a *App) Convert(pubPath, outPath, format string) (string, error) {
	if format == "" {
		format = "pdf"
	}
	if _, ok := formatMap[format]; !ok {
		return "", fmt.Errorf("unsupported format: %s", format)
	}
	if !strings.EqualFold(filepath.Ext(pubPath), ".pub") {
		return "", fmt.Errorf("expected a .pub file, got %s", filepath.Base(pubPath))
	}
	return a.convertTo(pubPath, outPath, format)
}

// convertTo runs the full conversion pipeline for one file into outPath.
func (a *App) convertTo(pubPath, outPath, format string) (string, error) {
	spec, ok := formatMap[format]
	if !ok {
		return "", fmt.Errorf("unsupported format: %s", format)
	}
	workDir := filepath.Dir(outPath)
	baseName := strings.TrimSuffix(filepath.Base(pubPath), filepath.Ext(pubPath))

	if spec.chain == "draw" {
		if err := soffice(pubPath, spec.ext, workDir); err != nil {
			return "", err
		}
		got := expectedOut(workDir, baseName, spec.ext)
		if err := moveOrRename(got, outPath); err != nil {
			return "", err
		}
		return outPath, nil
	}

	// pdf2docx chain: pub → pdf → docx → target
	tmpDir, err := os.MkdirTemp("", "pub-convert-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	// Step 1: pub → pdf
	pdfPath := filepath.Join(tmpDir, baseName+".pdf")
	if err := soffice(pubPath, "pdf", tmpDir); err != nil {
		return "", fmt.Errorf("step 1 (pub→pdf): %w", err)
	}

	// Step 2: pdf → docx via pdf2docx
	docxPath := filepath.Join(tmpDir, baseName+".docx")
	if err := pdf2docx(pdfPath, docxPath); err != nil {
		return "", fmt.Errorf("step 2 (pdf→docx): %w", err)
	}

	// Step 3: docx → target
	if format == "docx" {
		if err := copyFile(docxPath, outPath); err != nil {
			return "", err
		}
		return outPath, nil
	}
	if err := soffice(docxPath, spec.ext, workDir); err != nil {
		return "", fmt.Errorf("step 3 (docx→%s): %w", format, err)
	}
	got := expectedOut(workDir, baseName, spec.ext)
	if err := moveOrRename(got, outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

func soffice(inputPath, targetExt, outDir string) error {
	sofficePath, err := exec.LookPath("soffice")
	if err != nil {
		candidate := "/Applications/LibreOffice.app/Contents/MacOS/soffice"
		if _, stat := os.Stat(candidate); stat == nil {
			sofficePath = candidate
		} else {
			return fmt.Errorf("LibreOffice not found — install it from libreoffice.org")
		}
	}
	cmd := exec.Command(sofficePath,
		"--headless", "--norestore", "--nologo",
		"--convert-to", targetExt,
		"--outdir", outDir,
		inputPath,
	)
	cmd.Dir = outDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("soffice failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func pdf2docx(pdfPath, docxPath string) error {
	script := fmt.Sprintf(
		"from pdf2docx import Converter; c = Converter(%q); c.convert(%q); c.close()",
		pdfPath, docxPath,
	)
	cmd := exec.Command("python3", "-c", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pdf2docx failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func expectedOut(dir, base, ext string) string {
	return filepath.Join(dir, base+"."+ext)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func moveOrRename(src, dst string) error {
	if src == dst {
		return nil
	}
	if err := os.Rename(src, dst); err != nil {
		return copyFile(src, dst)
	}
	return nil
}

func (a *App) CheckStatus() map[string]bool {
	ok, _ := a.CheckSoffice()
	return map[string]bool{
		"soffice":   ok,
		"pdf2docx": a.CheckPdf2docx(),
	}
}

// RevealFile opens the file's location in Finder/Explorer.
func (a *App) RevealFile(path string) error {
	if path == "" {
		return fmt.Errorf("no file")
	}
	if goruntime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command("explorer", "/select,", path).Start()
}
