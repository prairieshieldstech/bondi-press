package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx        context.Context
	logoSVG    []byte
	previewDir string
	renderDir  string

	previewMu    sync.Mutex
	previewSrv   *http.Server
	previewSrvLn net.Listener
	previewPort  int
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	cleanupOldExe()
	// Pre-warm LibreOffice's profile + font cache in the background so the
	// first real conversion isn't stuck waiting ~40s on one-time init.
	go func() {
		if p, err := resolveSoffice(); err == nil {
			cmd := exec.Command(p, "--headless", "--norestore", "--nologo", "--terminate_after_init")
			hideConsole(cmd)
			cmd.Run()
		}
	}()
}

// ToggleFullscreen switches the window between its compact default size and
// maximised, returning the resulting state. The preview/editor screens use
// CSS vw/vh sizing that grows and shrinks with the actual window size, so
// maximising gives a materially bigger, clearer view.
//
// Uses WindowMaximise/Unmaximise (a bounds change) rather than native
// WindowFullscreen: this window is Frameless + AlwaysOnTop, and macOS's real
// fullscreen space transition does not engage for that window style — it
// silently no-ops. Maximise works regardless since it's just geometry.
func (a *App) ToggleFullscreen() bool {
	if runtime.WindowIsMaximised(a.ctx) {
		runtime.WindowUnmaximise(a.ctx)
		return false
	}
	runtime.WindowMaximise(a.ctx)
	return true
}

// GetLogo returns the Bondi Press SVG logo for the about/dialog views.
func (a *App) GetLogo() string {
	if a.logoSVG == nil {
		return ""
	}
	return string(a.logoSVG)
}

// PreparePreview writes edited content to a temp preview file, copies any
// adjacent asset folders, and serves the folder over a loopback HTTP server.
// WKWebView blocks file:// URLs inside the app, so we use http://127.0.0.1.
// Returns an http URL the webview iframe can load.
func (a *App) PreparePreview(originalPath, content string) (string, error) {
	ext := strings.ToLower(filepath.Ext(originalPath))
	if ext != ".html" && ext != ".txt" && ext != ".svg" {
		return "", fmt.Errorf("preview only supports HTML, TXT and SVG")
	}
	a.CleanupPreview()

	tmpDir, err := os.MkdirTemp("", "bondi-preview-*")
	if err != nil {
		return "", err
	}
	a.previewDir = tmpDir

	baseName := filepath.Base(originalPath)
	previewFile := filepath.Join(tmpDir, baseName)
	if err := os.WriteFile(previewFile, []byte(content), 0o644); err != nil {
		os.RemoveAll(tmpDir)
		a.previewDir = ""
		return "", err
	}

	if ext == ".html" {
		srcDir := filepath.Dir(originalPath)
		assetDir := strings.TrimSuffix(baseName, ext) + "_files"
		srcAssets := filepath.Join(srcDir, assetDir)
		// Copy the adjacent asset folder next to the temp file so relative
		// image / CSS / font paths in the HTML resolve when previewed.
		if info, err := os.Stat(srcAssets); err == nil && info.IsDir() {
			dstAssets := filepath.Join(tmpDir, assetDir)
			copyDir(srcAssets, dstAssets)
		}
	}

	if err := a.servePreviewDir(tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		a.previewDir = ""
		return "", err
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(tmpDir)))
	a.previewSrv.Handler = mux

	return fmt.Sprintf("http://127.0.0.1:%d/%s", a.previewPort, baseName), nil
}

// servePreviewDir starts (once) a loopback HTTP server rooted at dir.
func (a *App) servePreviewDir(dir string) error {
	a.previewMu.Lock()
	defer a.previewMu.Unlock()
	if a.previewSrv == nil {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		a.previewSrv = &http.Server{}
		a.previewSrvLn = ln
		a.previewPort = ln.Addr().(*net.TCPAddr).Port
		go a.previewSrv.Serve(ln)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(dir)))
	a.previewSrv.Handler = mux
	return nil
}

// CleanupPreview immediately replaces the handler so the served dir is blocked
// and removes the temporary preview directory. The loopback server is kept
// alive but serves 404 until the next preview.
func (a *App) CleanupPreview() {
	a.previewMu.Lock()
	if a.previewSrv != nil {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
		a.previewSrv.Handler = mux
	}
	a.previewMu.Unlock()

	if a.previewDir != "" {
		os.RemoveAll(a.previewDir)
		a.previewDir = ""
	}
}

// renderPdfPagesToPNGs rasterizes every page of pdfPath via PyMuPDF into PNG
// files inside a fresh temp directory, returned as absolute paths IN PAGE
// ORDER (page order comes from the script's own stdout, not a directory
// listing — os.ReadDir sorts lexically, which puts "page-10.png" before
// "page-2.png" once a document passes 9 pages). Caller owns cleanup of dir.
func renderPdfPagesToPNGs(pdfPath string, targetW int) (dir string, files []string, err error) {
	py, err := findPythonWithPdf2docx()
	if err != nil {
		return "", nil, err
	}

	tmpDir, err := os.MkdirTemp("", "bondi-pdfimg-*")
	if err != nil {
		return "", nil, err
	}

	script := `
import sys, os, warnings
warnings.filterwarnings("ignore")
import fitz
doc = fitz.open(sys.argv[1])
out = sys.argv[2]
target = float(sys.argv[3])
for i, page in enumerate(doc):
    r = page.rect
    scale = target / r.width if target > 0 else 1.5
    mat = fitz.Matrix(scale, scale)
    pix = page.get_pixmap(matrix=mat, alpha=False)
    fname = f"page-{i+1}.png"
    pix.save(os.path.join(out, fname))
    print(fname)
`
	ctx, cancel := context.WithTimeout(context.Background(), sofficeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-c", script, pdfPath, tmpDir, fmt.Sprintf("%d", targetW))
	hideConsole(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("render pdf: %s", strings.TrimSpace(stderr.String()))
	}
	// PyMuPDF prints its own "fitz API deprecated" notice straight to stdout
	// on import (not gated by warnings.filterwarnings, not on stderr), ahead
	// of our page filenames — filter to the exact pattern we print so that
	// noise never gets mistaken for a page file.
	pageLine := regexp.MustCompile(`^page-\d+\.png$`)
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		line = strings.TrimSpace(line)
		if !pageLine.MatchString(line) {
			continue
		}
		files = append(files, filepath.Join(tmpDir, line))
	}
	return tmpDir, files, nil
}

// RenderPdfToImages rasterizes every page of a PDF into PNGs, serves them
// over the loopback HTTP server, and returns the list of page image URLs
// (page order preserved). This preserves full fidelity (fonts, images,
// styling, positioning) without relying on a PDF.js web worker, which
// WKWebView blocks inside the app.
func (a *App) RenderPdfToImages(pdfPath string, targetW int) ([]string, error) {
	tmpDir, files, err := renderPdfPagesToPNGs(pdfPath, targetW)
	if err != nil {
		return nil, err
	}

	// servePreviewDir lazily creates the loopback server if this is the first
	// preview of the session (e.g. opening the PDF editor directly, without
	// ever having opened an HTML/TXT editor first — a.previewSrv is nil then).
	if err := a.servePreviewDir(tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}
	a.renderDir = tmpDir

	urls := make([]string, len(files))
	for i, f := range files {
		urls[i] = fmt.Sprintf("http://127.0.0.1:%d/%s", a.previewPort, filepath.Base(f))
	}
	return urls, nil
}

// generateFaithfulHTML builds a self-contained HTML document by rasterizing
// the ORIGINAL .pub's pages (the same pub->pdf->PyMuPDF-PNG pipeline used for
// the pixel-faithful preview/PDF editor) and embedding each page as a base64
// image. This replaces LibreOffice's draw_html_Export filter, which was
// confirmed to emit ZERO image references on real .pub files and reflows
// everything into plain unpositioned paragraphs — there is no folder or data
// URI to fix on that path, the filter simply drops images.
func generateFaithfulHTML(pubPath, outPath string) error {
	tmpDir, err := os.MkdirTemp("", "bondi-htmlgen-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	pdfPath, err := pubToPdf(pubPath, tmpDir)
	if err != nil {
		return fmt.Errorf("could not render pages: %w", err)
	}

	pngDir, files, err := renderPdfPagesToPNGs(pdfPath, 1400)
	if err != nil {
		return err
	}
	defer os.RemoveAll(pngDir)

	base := strings.TrimSuffix(filepath.Base(pubPath), filepath.Ext(pubPath))
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\">\n<title>")
	b.WriteString(html.EscapeString(base))
	b.WriteString("</title>\n<style>\n")
	b.WriteString("body{margin:0;background:#e8e8e8;display:flex;flex-direction:column;align-items:center;gap:24px;padding:24px 0;}\n")
	b.WriteString("img{max-width:100%;height:auto;box-shadow:0 2px 16px rgba(0,0,0,0.15);background:#fff;}\n")
	b.WriteString("</style>\n</head><body>\n")
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		b.WriteString(`<img src="data:image/png;base64,`)
		b.WriteString(base64.StdEncoding.EncodeToString(data))
		b.WriteString("\">\n")
	}
	b.WriteString("</body></html>\n")
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

// PreviewPub renders the ORIGINAL .pub file's true layout as page images,
// independent of whichever export format the user picked. pub->pdf (soffice)
// is pixel-faithful (see RenderPdfToImages), so this gives an accurate "what
// it really looks like" preview even when the chosen output format (e.g.
// DOCX, which re-flows content through pdf2docx) can't preserve the exact
// layout.
func (a *App) PreviewPub(pubPath string) ([]string, error) {
	tmpDir, err := os.MkdirTemp("", "bondi-pubpreview-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	pdfPath, err := pubToPdf(pubPath, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("could not render preview: %w", err)
	}
	return a.RenderPdfToImages(pdfPath, 900)
}

// resolvePub2Xhtml locates libmspub's pub2xhtml tool (cached). It reads the
// Publisher file's OWN object model directly (via libmspub, the same parser
// LibreOffice uses internally) and emits per-shape SVG with real coordinates
// — unlike soffice's draw_html_Export (no images at all) or pdf2docx (guesses
// paragraph structure from flattened PDF geometry).
func resolvePub2Xhtml() (string, error) {
	if p := findTool("pub2xhtml", "/opt/homebrew/opt/libmspub/bin/pub2xhtml", "/usr/local/opt/libmspub/bin/pub2xhtml"); p != "" {
		return p, nil
	}
	return "", toolMissingErr("libmspub (pub2xhtml)", "brew install libmspub")
}

// extractLayoutScript parses pub2raw's output — the sequence of librevenge
// drawing-interface calls libmspub makes internally, BEFORE any SVG/HTML
// flattening — into a page/item geometry JSON.
//
// This replaced an earlier version that parsed pub2xhtml's SVG output. That
// approach had no real frame width/height for text (SVG text is just
// positioned glyph runs, not a bounded box), forcing width/height to be
// heuristically guessed from neighboring items — which produced real
// overlapping-text bugs no amount of heuristic tuning fully resolved.
// pub2raw's startTextObject(...) calls carry the ACTUAL frame rectangle
// (svg:x/y/width/height, all in inches) libmspub read from the file, so
// text now gets placed and sized exactly like the original, no guessing.
const extractLayoutScript = `
import sys, re, json

# pub2raw's own pretty-printer wraps long calls across multiple lines — the
# closing ")" of a call can land on its own line, or even several lines down
# (confirmed on real files: ` + "`" + `insertText (About Bondi` + "`" + `, closing ` + "`" + `)` + "`" + ` on the
# next line; also confirmed with a NESTED paren before the wrap, e.g.
# ` + "`" + `insertText (Robert I. I. (Bob) Bondi` + "`" + ` then ` + "`" + `)` + "`" + ` on the next line). A line-by-line
# parser (find "(", rfind ")" ON THAT LINE) silently drops such a call's
# text entirely when no ")" is on the same line — real nav/heading text was
# going missing on real customer files this way, not a rare edge case.
#
# Two simpler fixes were tried and reverted before this one:
#   - A whole-file paren-DEPTH counter handles the wrapping fine, but plain
#     text routinely contains its OWN unmatched parens ("1) ... 2) ... 3)"),
#     and a depth counter treats every stray ")" as closing the call —
#     truncating text a same-line rfind would have gotten right.
#   - Falling back to reading more lines only when a line has an unmatched
#     OPEN paren and NO ")" at all misses the case above: a line can have a
#     ")" (closing an unrelated nested "(Bob)") while the call's OWN closing
#     paren is still further down.
#
# What actually distinguishes a real call boundary from a stray paren in
# text is the call NAME itself: it's one of a small, fixed vocabulary
# librevenge's generator emits, which real document text never collides
# with. So: find every line that starts with a known call name, and treat
# everything from there up to the START of the next such line as that
# call's own (possibly multi-line, paren-messy) content — then just take
# everything after its first "(" and before its last ")" as args, with no
# paren-balancing needed at all since the NEXT call's start line already
# marks where this one's content ends.
CALL_NAMES = {
    "startDocument", "endDocument", "setDocumentMetaData",
    "startPage", "endPage", "startLayer", "endLayer",
    "startEmbeddedGraphics", "endEmbeddedGraphics", "openGroup", "closeGroup",
    "setStyle",
    "drawRectangle", "drawEllipse", "drawPolyline", "drawPolygon", "drawPath",
    "drawGraphicObject", "drawConnector",
    "startTextObject", "endTextObject",
    "startTableObject", "endTableObject",
    "openTableRow", "closeTableRow", "openTableCell", "closeTableCell",
    "insertCoveredTableCell",
    "openParagraph", "closeParagraph", "openSpan", "closeSpan",
    "openLink", "closeLink",
    "insertTab", "insertSpace", "insertLineBreak", "insertField", "insertText",
    "openOrderedListLevel", "closeOrderedListLevel",
    "openUnorderedListLevel", "closeUnorderedListLevel",
    "openListElement", "closeListElement",
    "defineEmbeddedFont", "definePageStyle", "defineParagraphStyle",
    "defineCharacterStyle", "defineSectionStyle", "openSection", "closeSection",
}

def scan_calls(text):
    lines = text.split("\n")
    starts = []
    ident = re.compile(r"^([A-Za-z_][A-Za-z0-9_]*)")
    for idx, line in enumerate(lines):
        s = line.strip()
        if not s:
            continue
        m = ident.match(s)
        if m and m.group(1) in CALL_NAMES:
            starts.append((idx, m.group(1)))
    calls = []
    for n, (idx, name) in enumerate(starts):
        end_idx = starts[n + 1][0] if n + 1 < len(starts) else len(lines)
        chunk = "\n".join(lines[idx:end_idx])
        i = chunk.find("(")
        if i == -1:
            calls.append((name, ""))
            continue
        j = chunk.rfind(")")
        args = chunk[i + 1:j] if j > i else chunk[i + 1:]
        calls.append((name, args.replace("\n", " ")))
    return calls

def parse_flat_kv(args):
    out = {}
    for m in re.finditer(r"([\w:.\-]+):\s*([^,]+?)(?=,\s*[\w:.\-]+:|$)", args):
        out[m.group(1)] = m.group(2).strip()
    return out

def in_to_pt(s):
    try:
        return round(float(s.replace("in", "").strip()) * 72, 2)
    except (TypeError, ValueError):
        return 0.0

def bbox_from_points_pt(args):
    xs = [float(x) * 72 for x in re.findall(r"svg:x:\s*([\d.]+)in", args)]
    ys = [float(y) * 72 for y in re.findall(r"svg:y:\s*([\d.]+)in", args)]
    if not xs or not ys:
        return 0.0, 0.0, 0.0, 0.0
    return min(xs), min(ys), max(xs), max(ys)

ALIGN_MAP = {"left": "left", "center": "center", "right": "right", "justify": "justify"}

def sniff_mime(b64_head):
    if b64_head.startswith("/9j/"):
        return "image/jpeg"
    return "image/png"

def main():
    pages = []
    cur_page = None
    pending_fill_b64 = None
    text_stack = []   # frames currently open (rare to nest, but handle it)
    para_stack = []   # paragraphs currently open
    span_stack = []   # run-attribute dicts currently open

    with open(sys.argv[1], "r", encoding="utf-8", errors="replace") as f:
        text = f.read()

    for name, args in scan_calls(text):
            if name == "startPage":
                kv = parse_flat_kv(args)
                cur_page = {
                    "widthPt": in_to_pt(kv.get("svg:width", "8.5in")),
                    "heightPt": in_to_pt(kv.get("svg:height", "11in")),
                    "items": [],
                }
            elif name == "endPage":
                if cur_page:
                    pages.append(cur_page)
                cur_page = None
            elif name == "setStyle":
                if "draw:fill: bitmap" in args and "draw:fill-image:" in args:
                    # base64 only contains [A-Za-z0-9+/=] — bound the match to
                    # that so trailing attributes on the same setStyle call
                    # (draw:mime-type, style:repeat, ...) aren't swept in too.
                    b64_m = re.search(r"draw:fill-image:\s*([A-Za-z0-9+/=]+)", args)
                    pending_fill_b64 = b64_m.group(1) if b64_m else None
                else:
                    pending_fill_b64 = None
            elif name in ("drawPolygon", "drawRectangle"):
                if pending_fill_b64 and cur_page is not None:
                    x0, y0, x1, y1 = bbox_from_points_pt(args)
                    if x1 > x0 and y1 > y0:
                        href = "data:%s;base64,%s" % (sniff_mime(pending_fill_b64[:12]), pending_fill_b64)
                        cur_page["items"].append({
                            "type": "image", "x": x0, "y": y0,
                            "w": x1 - x0, "h": y1 - y0, "href": href,
                        })
                pending_fill_b64 = None
            elif name == "startTextObject":
                kv = parse_flat_kv(args)
                text_stack.append({
                    "x": in_to_pt(kv.get("svg:x", "0in")),
                    "y": in_to_pt(kv.get("svg:y", "0in")),
                    "w": in_to_pt(kv.get("svg:width", "1in")),
                    "h": in_to_pt(kv.get("svg:height", "0.2in")),
                    "padLeft": in_to_pt(kv.get("fo:padding-left", "0in")),
                    "padRight": in_to_pt(kv.get("fo:padding-right", "0in")),
                    "padTop": in_to_pt(kv.get("fo:padding-top", "0in")),
                    "padBottom": in_to_pt(kv.get("fo:padding-bottom", "0in")),
                    "paragraphs": [],
                })
            elif name == "endTextObject":
                if text_stack:
                    frame = text_stack.pop()
                    if any(p["runs"] for p in frame["paragraphs"]):
                        item = dict(frame)
                        item["type"] = "text"
                        if cur_page is not None:
                            cur_page["items"].append(item)
            elif name == "openParagraph":
                kv = parse_flat_kv(args)
                lh_m = re.match(r"([\d.]+)%", kv.get("fo:line-height", ""))
                para_stack.append({
                    "align": ALIGN_MAP.get(kv.get("fo:text-align", "left"), "left"),
                    "lineHeightPct": float(lh_m.group(1)) if lh_m else None,
                    "runs": [],
                })
            elif name == "closeParagraph":
                if para_stack:
                    p = para_stack.pop()
                    if text_stack:
                        text_stack[-1]["paragraphs"].append(p)
            elif name == "openSpan":
                kv = parse_flat_kv(args)
                span_stack.append({
                    "fontFamily": kv.get("style:font-name", "Arial"),
                    "sizePt": in_to_pt(kv.get("fo:font-size", "0.14in")),
                    "bold": kv.get("fo:font-weight") == "bold",
                    "italic": kv.get("fo:font-style") == "italic",
                    "color": kv.get("fo:color", "#000000"),
                    "uppercase": kv.get("fo:text-transform") == "uppercase",
                })
            elif name == "closeSpan":
                if span_stack:
                    span_stack.pop()
            elif name == "insertText":
                if span_stack and para_stack:
                    attrs = span_stack[-1]
                    para_stack[-1]["runs"].append({
                        "text": args.upper() if attrs["uppercase"] else args,
                        "fontFamily": attrs["fontFamily"],
                        "sizePt": attrs["sizePt"],
                        "bold": attrs["bold"],
                        "italic": attrs["italic"],
                        "color": attrs["color"],
                    })

    print(json.dumps({"pages": pages}))

main()
`

// resolvePub2Raw locates libmspub's pub2raw tool (cached). Unlike pub2xhtml
// (used only for the PDF-render fallback, see pubToPdfFallback), pub2raw
// dumps the raw librevenge drawing-interface calls libmspub makes — real
// frame rectangles for text boxes, not just positioned glyph runs — which is
// what ExtractPubLayout needs for accurate placement.
func resolvePub2Raw() (string, error) {
	if p := findTool("pub2raw", "/opt/homebrew/opt/libmspub/bin/pub2raw", "/usr/local/opt/libmspub/bin/pub2raw"); p != "" {
		return p, nil
	}
	return "", toolMissingErr("libmspub (pub2raw)", "brew install libmspub")
}

// ExtractPubLayout reads the ORIGINAL .pub file's real object model (via
// libmspub's pub2raw) and returns a JSON geometry description — pages, each
// with text frames (their TRUE rectangle, paragraphs, and runs) and images
// at their true page-coordinate positions. Intended as the input to a
// frontend DOCX builder that places content with absolutely positioned text
// boxes/pictures, instead of pdf2docx's flattened-paragraph reconstruction
// (see docx-editor.js).
func (a *App) ExtractPubLayout(pubPath string) (string, error) {
	return extractPubLayout(pubPath)
}

// extractPubLayout is the free-function core of ExtractPubLayout, split out
// so pubToPdfFallback can also call it (real per-frame geometry is exactly
// what that renderer needs to word-wrap text correctly — see its doc comment).
func extractPubLayout(pubPath string) (string, error) {
	pub2raw, err := resolvePub2Raw()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sofficeTimeout)
	defer cancel()
	var rawOut bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, pub2raw, pubPath)
	hideConsole(cmd)
	cmd.Stdout = &rawOut
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("pub2raw timed out after %s on %s", sofficeTimeout, filepath.Base(pubPath))
		}
		return "", fmt.Errorf("pub2raw failed: %s", strings.TrimSpace(stderr.String()))
	}

	tmpDir, err := os.MkdirTemp("", "bondi-layout-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	rawPath := filepath.Join(tmpDir, "doc.raw")
	if err := os.WriteFile(rawPath, rawOut.Bytes(), 0o644); err != nil {
		return "", err
	}

	py, err := findPythonWithPdf2docx()
	if err != nil {
		// This parsing script only needs the stdlib, so fall back to any
		// python3 on PATH if the pdf2docx-specific one isn't available.
		if p, lookErr := lookPython(); lookErr == nil {
			py = p
		} else {
			return "", err
		}
	}
	var stdout bytes.Buffer
	stderr.Reset()
	pyCmd := exec.CommandContext(ctx, py, "-c", extractLayoutScript, rawPath)
	hideConsole(pyCmd)
	pyCmd.Stdout = &stdout
	pyCmd.Stderr = &stderr
	if err := pyCmd.Run(); err != nil {
		return "", fmt.Errorf("layout extraction failed: %s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// resolveRsvgConvert locates librsvg's rasterizer (cached). Used only by the
// libmspub PDF fallback below — MuPDF's own built-in SVG renderer was tried
// first and silently drops both pattern-fills (renders solid black) and all
// text, so librsvg (a much more complete/correct SVG implementation) is used
// instead to rasterize pub2xhtml's SVG pages.
func resolveRsvgConvert() (string, error) {
	if p := findTool("rsvg-convert", "/opt/homebrew/opt/librsvg/bin/rsvg-convert", "/usr/local/opt/librsvg/bin/rsvg-convert"); p != "" {
		return p, nil
	}
	return "", toolMissingErr("librsvg (rsvg-convert)", "brew install librsvg")
}

// pubFallbackScript rasterizes pub2xhtml's per-page SVG into a background
// image (shapes, fills, bitmap-pattern images — everything except text), then
// draws real, correctly word-wrapped text on top of each page from the
// layout JSON (see extractPubLayout/extractLayoutScript). Used for files
// where soffice's own Publisher import filter fails outright (real case
// found: a large multi-object "website mockup" .pub that soffice can't even
// load, while libmspub parses it fine — different import code paths).
//
// Text is rendered separately from the SVG background, rather than left in
// it, because pub2xhtml's SVG generator emits every paragraph/run of a text
// frame as bare <svg:tspan> elements with NO x/y/dy positions at all — so an
// unrelated heading and the body paragraph below it render concatenated onto
// one baseline with no space or line break between them ("About Us:We are
// an organization..."), running off the page edge. It carries no frame
// width either, so there is nothing in the SVG itself to safely wrap
// against. extractPubLayout (via the OTHER libmspub tool, pub2raw) reads the
// same underlying document and keeps exactly what's missing here — each
// frame's real rectangle, and its paragraphs/runs in order — which is what
// this script uses to lay the text out itself, word-wrapped to the real
// frame width, before drawing it as real (not rasterized) PDF text.
//
// One fixup is required for the background first: pub2xhtml emits font-size
// as a bare number intended as INCHES (matching how x/y/width/height are
// also inches-as-bare-numbers over a points-based viewBox), but the SVG spec
// says an unrooted number is a "user unit" (≈px) — so standard renderers
// (confirmed on both MuPDF and librsvg) render leftover text at ~1/72 the
// intended size, invisibly thin. This no longer matters for legibility since
// text is stripped from the background entirely, but the multiplier is kept
// so any font-size-dependent layout in the source SVG (rare, but seen in
// generated markers) stays proportioned correctly.
const pubFallbackScript = `
import sys, re, os, json, subprocess

def fix_font_size(svg_text):
    def repl(m):
        return 'font-size="%.2f"' % (float(m.group(1)) * 72)
    return re.sub(r'font-size="([\d.]+)"', repl, svg_text)

FONT_FAMILIES = {
    "ti": ("tiro", "tibo", "tiit", "tibi"),   # Times-like
    "co": ("cour", "cobo", "coit", "cobi"),   # monospace
    "he": ("helv", "hebo", "heit", "hebi"),   # everything else (Arial/Franklin Gothic/Calibri/...)
}

def pick_font(family, bold, italic):
    f = (family or "").lower()
    if any(k in f for k in ("times", "georgia", "garamond", "cambria", "book antiqua", "palatino", "minion")):
        base = "ti"
    elif any(k in f for k in ("courier", "consolas", "mono")):
        base = "co"
    else:
        base = "he"
    regular, bo, it, bi = FONT_FAMILIES[base]
    if bold and italic:
        return bi
    if bold:
        return bo
    if italic:
        return it
    return regular

def hex_to_rgb(h):
    h = (h or "#000000").lstrip("#")
    if len(h) != 6:
        return (0.0, 0.0, 0.0)
    try:
        return tuple(int(h[i:i+2], 16) / 255.0 for i in (0, 2, 4))
    except ValueError:
        return (0.0, 0.0, 0.0)

def tokenize(runs, scale=1.0):
    # (text, font, size, color) per whitespace-or-word chunk, spanning every
    # run in the paragraph so wrapping can break at any word regardless of
    # which run (bold/italic/size change) it falls in.
    out = []
    for run in runs:
        font = pick_font(run.get("fontFamily"), run.get("bold"), run.get("italic"))
        size = (run.get("sizePt") or 10.0) * scale
        color = hex_to_rgb(run.get("color"))
        for tok in re.findall(r"\S+|\s+", run.get("text", "")):
            out.append((tok, font, size, color))
    return out

def wrap_lines(tokens, max_w):
    import fitz
    lines, cur, cur_w = [], [], 0.0
    for tok, font, size, color in tokens:
        w = fitz.get_text_length(tok, fontname=font, fontsize=size)
        if not tok.strip() and not cur:
            continue  # never start a line with whitespace
        if cur and cur_w + w > max_w:
            lines.append((cur, cur_w))
            cur, cur_w = ([], 0.0) if not tok.strip() else ([(tok, font, size, color)], w)
            continue
        cur.append((tok, font, size, color))
        cur_w += w
    if cur:
        lines.append((cur, cur_w))
    return lines

def layout_paragraphs(frame, x0, x1, max_w, scale):
    out, total_h = [], 0.0
    for para in frame.get("paragraphs", []):
        tokens = tokenize(para.get("runs", []), scale)
        lines = wrap_lines(tokens, max_w) if tokens else [([], 0.0)]
        base_size = max([t[2] for t in tokens], default=10.0 * scale)
        line_h = base_size * ((para.get("lineHeightPct") or 115) / 100.0)
        out.append((lines, line_h))
        total_h += line_h * len(lines)
    return out, total_h

def draw_frame(page, frame):
    import fitz
    x0 = frame["x"] + frame.get("padLeft", 0)
    x1 = frame["x"] + frame["w"] - frame.get("padRight", 0)
    max_w = max(1.0, x1 - x0)
    avail_h = max(1.0, frame["h"] - frame.get("padTop", 0) - frame.get("padBottom", 0))

    scale = 1.0
    layout, total_h = layout_paragraphs(frame, x0, x1, max_w, scale)
    # Publisher's "Shrink text on overflow" autofit: many text boxes (most
    # visibly single-line headings) shrink their whole font size to keep
    # content on the lines the box actually has room for, rather than
    # wrapping to another line and having it clipped. pub2raw doesn't expose
    # whether autofit is on for a given box, so this approximates it for
    # every frame that overflows — safe even where Publisher wasn't actually
    # autofitting, since the alternative (silently dropping the overflow
    # entirely, as before) is strictly worse.
    while total_h > avail_h and scale > 0.35:
        scale -= 0.05
        layout, total_h = layout_paragraphs(frame, x0, x1, max_w, scale)

    y = frame["y"] + frame.get("padTop", 0)
    y_limit = frame["y"] + frame["h"]
    for para, (lines, line_h) in zip(frame.get("paragraphs", []), layout):
        for line_tokens, line_w in lines:
            y += line_h
            if y - line_h > y_limit + 0.5:
                return  # still overflows even at min scale; clip like Publisher would
            align = para.get("align", "left")
            if align == "right":
                cursor = x1 - line_w
            elif align == "center":
                cursor = x0 + (max_w - line_w) / 2.0
            else:  # left, justify (justify approximated as left)
                cursor = x0
            for tok, font, size, color in line_tokens:
                if tok.strip():
                    page.insert_text((cursor, y), tok, fontname=font, fontsize=size, color=color)
                cursor += fitz.get_text_length(tok, fontname=font, fontsize=size)

def main():
    xhtml_path, rsvg, out_pdf, layout_path = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    tmp_dir = os.path.dirname(out_pdf)
    with open(xhtml_path, "r", encoding="utf-8", errors="replace") as f:
        raw = f.read()
    body = raw[raw.find("<body>") + 6 : raw.rfind("</body>")]
    body = re.sub(r"<!--.*?-->", "", body, flags=re.S)
    body = re.sub(r"<\?import[^>]*\?>", "", body)

    page_chunks = re.findall(r"<svg:svg[^>]*>.*?</svg:svg>", body, re.S)
    if not page_chunks:
        print("no pages found in pub2xhtml output", file=sys.stderr)
        sys.exit(1)

    layout_pages = []
    try:
        with open(layout_path, "r", encoding="utf-8") as f:
            layout_pages = json.load(f).get("pages", [])
    except Exception as e:
        print("layout JSON unavailable, background-only render: %s" % e, file=sys.stderr)

    import fitz
    doc = fitz.open()
    for i, chunk in enumerate(page_chunks):
        vb_m = re.search(r'viewBox="0 0 ([\d.]+) ([\d.]+)"', chunk)
        page_w, page_h = (float(vb_m.group(1)), float(vb_m.group(2))) if vb_m else (612.0, 792.0)
        # Text is dropped from the background: it's drawn for real afterward
        # from layout_pages (see module docstring above for why).
        chunk_no_text = re.sub(r"<svg:text[^>]*>.*?</svg:text>", "", chunk, flags=re.S)
        svg_text = fix_font_size('<?xml version="1.0" encoding="UTF-8"?>\n' + chunk_no_text)
        svg_path = os.path.join(tmp_dir, "page-%d.svg" % i)
        png_path = os.path.join(tmp_dir, "page-%d.png" % i)
        with open(svg_path, "w", encoding="utf-8") as f:
            f.write(svg_text)
        # 3x page points-per-inch (72) = 216 DPI. This path is now the
        # default renderer for the PDF export and preview, not just a rare
        # fallback, so it needs to hold up at print/zoom quality, not just
        # look fine as a thumbnail.
        subprocess.run([rsvg, "-w", str(int(page_w * 3)), "-h", str(int(page_h * 3)),
                         "-o", png_path, svg_path], check=True,
                        capture_output=True)
        page = doc.new_page(width=page_w, height=page_h)
        page.insert_image(fitz.Rect(0, 0, page_w, page_h), filename=png_path)
        if i < len(layout_pages):
            for item in layout_pages[i].get("items", []):
                if item.get("type") == "text":
                    try:
                        draw_frame(page, item)
                    except Exception as e:
                        print("text frame render failed (page %d): %s" % (i, e), file=sys.stderr)
    # Without deflate/deflate_images, PyMuPDF re-embeds inserted images
    # uncompressed — a single 390KB page PNG turned into a 31MB PDF page
    # without these flags (confirmed while building this).
    doc.save(out_pdf, deflate=True, deflate_images=True, garbage=4)
    print(len(page_chunks))

main()
`

// pubToPdfFallback renders pubPath to outPdfPath using libmspub + librosvg,
// entirely independent of soffice. See pubFallbackScript for why this is
// needed rather than just fixing the soffice call.
func pubToPdfFallback(pubPath, outPdfPath string) error {
	pub2xhtml, err := resolvePub2Xhtml()
	if err != nil {
		return err
	}
	rsvg, err := resolveRsvgConvert()
	if err != nil {
		return err
	}
	py, err := findPythonWithPdf2docx()
	if err != nil {
		if p, lookErr := lookPython(); lookErr == nil {
			py = p
		} else {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), sofficeTimeout)
	defer cancel()

	var svgOut, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, pub2xhtml, pubPath)
	hideConsole(cmd)
	cmd.Stdout = &svgOut
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pub2xhtml failed: %s", strings.TrimSpace(stderr.String()))
	}

	tmpDir, err := os.MkdirTemp("", "bondi-pubfallback-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	xhtmlPath := filepath.Join(tmpDir, "doc.xhtml")
	if err := os.WriteFile(xhtmlPath, svgOut.Bytes(), 0o644); err != nil {
		return err
	}

	// Real per-frame geometry (real widths, so text can be word-wrapped
	// correctly) from the OTHER libmspub tool — see pubFallbackScript's doc
	// comment for why pub2xhtml's own SVG output can't be used for this. A
	// failure here isn't fatal: the script falls back to a background-only
	// render (still strictly better than today's un-wrapped text) rather
	// than failing the whole conversion over it.
	layoutPath := filepath.Join(tmpDir, "layout.json")
	if layoutJSON, layoutErr := extractPubLayout(pubPath); layoutErr == nil {
		os.WriteFile(layoutPath, []byte(layoutJSON), 0o644)
	} else {
		os.WriteFile(layoutPath, []byte(`{"pages":[]}`), 0o644)
	}

	tmpPdf := filepath.Join(tmpDir, "out.pdf")
	stderr.Reset()
	pyCmd := exec.CommandContext(ctx, py, "-c", pubFallbackScript, xhtmlPath, rsvg, tmpPdf, layoutPath)
	pyCmd.Env = toolEnv()
	hideConsole(pyCmd)
	pyCmd.Stderr = &stderr
	if err := pyCmd.Run(); err != nil {
		return fmt.Errorf("libmspub fallback render failed: %s", strings.TrimSpace(stderr.String()))
	}
	return moveOrRename(tmpPdf, outPdfPath)
}

// pubToPdf converts pubPath to a PDF in outDir, trying soffice first (the
// common case: fast, and produces a real text layer that the pdf2docx chain
// needs) and falling back to the libmspub+librsvg path (pubToPdfFallback) if
// soffice's own Publisher import filter fails to even load the file —
// confirmed to happen on legitimate, non-corrupt .pub files that are just
// unusually complex (many small objects, e.g. website-mockup-style layouts),
// which libmspub parses fine.
//
// NOTE: pubToPdfFallback's renderer does not word-wrap text to its frame's
// width (see pubFallbackScript) — every .pub file that reaches it renders
// with paragraphs run onto a single overflowing line. Do not prefer this
// path over soffice for files soffice can actually load; it is a
// last-resort fallback, not a higher-fidelity alternative, until that's fixed.
func pubToPdf(pubPath, outDir string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(pubPath), filepath.Ext(pubPath))
	outPath := expectedOut(outDir, base, "pdf")
	sofficeErr := soffice(pubPath, "pdf", outDir)
	if sofficeErr == nil {
		return outPath, nil
	}
	if fallbackErr := pubToPdfFallback(pubPath, outPath); fallbackErr != nil {
		return "", fmt.Errorf("%w (fallback also failed: %s)", sofficeErr, fallbackErr)
	}
	return outPath, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

var formats = []string{"pdf", "docx", "doc", "odt", "rtf", "txt", "png", "jpg", "svg", "html", "odg"}

type formatSpec struct {
	ext   string
	chain string // "draw" = single soffice call; "pdf2docx" = pub→pdf→docx→target
}

var formatMap = map[string]formatSpec{
	"pdf":  {ext: "pdf", chain: "draw"},
	"png":  {ext: "png", chain: "draw"},
	"jpg":  {ext: "jpg", chain: "draw"},
	"svg":  {ext: "svg", chain: "draw"},
	"html": {ext: "html", chain: "draw"},
	"odg":  {ext: "odg", chain: "draw"},
	"docx": {ext: "docx", chain: "pdf2docx"},
	"doc":  {ext: "doc", chain: "pdf2docx"},
	"odt":  {ext: "odt", chain: "pdf2docx"},
	"rtf":  {ext: "rtf", chain: "pdf2docx"},
	"txt":  {ext: "txt", chain: "pdf2docx"},
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

// exportDir returns (creating if needed) the shared "Bondi Press Exports"
// folder under $HOME. A plain top-level folder, deliberately NOT inside
// Desktop/Documents/Downloads: those are macOS TCC-protected, and an ad-hoc/
// dev-signed build's access to them is unreliable and can flip across
// rebuilds (see handoff notes — this caused hard-to-diagnose "source file
// could not be loaded" / "impl_store failed" errors that were really EPERM
// in disguise).
func exportDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home, _ = os.Getwd()
	}
	outDir := filepath.Join(home, "Bondi Press Exports")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	return outDir, nil
}

// ExportPathFor computes the destination path for pubPath converted to
// format inside the shared exports folder, creating that folder if needed.
// Used by the frontend-driven DOCX layout pipeline (see docx-editor.js
// layoutToDocxBytes), which can't go through ConvertBatch/convertTo since it
// needs docx.js (a browser-only library) to build the bytes.
func (a *App) ExportPathFor(pubPath, format string) (string, error) {
	outDir, err := exportDir()
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(filepath.Base(pubPath), filepath.Ext(pubPath))
	return expectedOut(outDir, base, format), nil
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

	outDir, err := exportDir()
	if err != nil {
		for _, p := range pubPaths {
			results = append(results, ConvertResult{Input: p, Status: "error", Error: err.Error()})
		}
		return results
	}

	for i, p := range pubPaths {
		name := filepath.Base(p)
		runtime.EventsEmit(a.ctx, "conv:file", map[string]any{"index": i, "total": len(pubPaths), "name": name, "status": "start"})
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
		runtime.EventsEmit(a.ctx, "conv:file", map[string]any{"index": i, "total": len(pubPaths), "name": name, "status": r.Status})
	}
	return results
}

// CheckSoffice returns true if LibreOffice is installed and reachable.
func (a *App) CheckSoffice() (bool, string) {
	p, err := resolveSoffice()
	return err == nil, p
}

// CheckPdf2docx returns true if pdf2docx is importable in python3.
func (a *App) CheckPdf2docx() bool {
	_, err := findPythonWithPdf2docx()
	return err == nil
}

var (
	pythonMu   sync.Mutex
	pythonPath string
)

func resetPythonCache() {
	pythonMu.Lock()
	pythonPath = ""
	pythonMu.Unlock()
}

// findPythonWithPdf2docx returns a python3 binary that has pdf2docx installed.
// GUI-launched apps don't inherit the user's shell PATH, so we must probe the
// common Homebrew locations explicitly alongside PATH lookup. The result is
// cached: probing spawns python + imports pdf2docx (~1s), which is too slow to
// repeat on every conversion or the 5s status poll.
func pythonNames() []string {
	if goruntime.GOOS == "windows" {
		// python.org installs python.exe + the py launcher; there is no python3.exe
		// (the Store stub of that name doesn't run real code).
		return []string{"python", "py"}
	}
	return []string{"python3"}
}

func lookPython() (string, error) {
	for _, n := range pythonNames() {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python not found")
}

func findPythonWithPdf2docx() (string, error) {
	pythonMu.Lock()
	defer pythonMu.Unlock()
	if pythonPath != "" {
		return pythonPath, nil
	}
	candidates := pythonPathCandidates()
	candidates = append(candidates, realPythonCandidates()...)
	candidates = append(candidates,
		"/opt/homebrew/bin/python3",
		"/usr/local/bin/python3",
		"/opt/homebrew/bin/python3.13",
		"/opt/homebrew/bin/python3.12",
	)
	for _, c := range candidates {
		cmd := exec.Command(c, "-c", "import pdf2docx, fitz")
		hideConsole(cmd)
		if cmd.Run() == nil {
			pythonPath = c
			return c, nil
		}
	}
	return "", fmt.Errorf("pdf2docx/PyMuPDF not found in any python — run setup from the status bar")
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
		if format == "html" {
			// LibreOffice's draw_html_Export emits zero image references and
			// reflows content into plain paragraphs — use the faithful
			// page-image renderer instead (see generateFaithfulHTML).
			if err := generateFaithfulHTML(pubPath, outPath); err != nil {
				return "", err
			}
			return outPath, nil
		}
		if format == "pdf" {
			// pubToPdf falls back to the libmspub+librsvg pipeline if
			// soffice's own Publisher import fails outright (see pubToPdf).
			got, err := pubToPdf(pubPath, workDir)
			if err != nil {
				return "", err
			}
			if err := moveOrRename(got, outPath); err != nil {
				return "", err
			}
			return outPath, nil
		}
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

	// Step 1: pub → pdf (soffice, falling back to libmspub+librsvg if soffice
	// can't even load the file — see pubToPdf)
	pdfPath, err := pubToPdf(pubPath, tmpDir)
	if err != nil {
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

var (
	sofficeMu   sync.Mutex
	sofficePath string
)

// sofficeCandidates lists where LibreOffice lives per-OS. GUI-launched apps
// don't inherit the shell PATH, and the Windows installer does not add
// LibreOffice to PATH at all, so PATH lookup alone misses most installs.
func sofficeCandidates() []string {
	switch goruntime.GOOS {
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "LOCALAPPDATA"} {
			if base := os.Getenv(env); base != "" {
				out = append(out, filepath.Join(base, "LibreOffice", "program", "soffice.exe"))
				out = append(out, filepath.Join(base, "Programs", "LibreOffice", "program", "soffice.exe"))
			}
		}
		out = append(out,
			`C:\Program Files\LibreOffice\program\soffice.exe`,
			`C:\Program Files (x86)\LibreOffice\program\soffice.exe`)
		return out
	case "darwin":
		return []string{"/Applications/LibreOffice.app/Contents/MacOS/soffice"}
	default:
		return []string{"/usr/bin/soffice", "/usr/local/bin/soffice", "/opt/libreoffice/program/soffice", "/snap/bin/libreoffice"}
	}
}

// resolveSoffice locates the LibreOffice binary (cached only on success, so
// installing LibreOffice while the app is open is picked up without a restart).
func resolveSoffice() (string, error) {
	sofficeMu.Lock()
	defer sofficeMu.Unlock()
	if sofficePath != "" {
		return sofficePath, nil
	}
	if p, err := exec.LookPath("soffice"); err == nil {
		sofficePath = p
		return p, nil
	}
	for _, c := range sofficeCandidates() {
		if _, err := os.Stat(c); err == nil {
			sofficePath = c
			return c, nil
		}
	}
	return "", fmt.Errorf("LibreOffice not found — install it from libreoffice.org (Windows: 'winget install TheDocumentFoundation.LibreOffice'), then reopen Bondi Press")
}

// sofficeTimeout bounds every soffice invocation. Without it, a file that
// makes soffice hang (e.g. a password-protected or malformed document
// silently waiting on a dialog headless mode can't show) blocks forever —
// the calling Wails method's promise never resolves and the UI just sits on
// "Rendering preview…" / "Converting…" with no error, indefinitely.
const sofficeTimeout = 120 * time.Second

func soffice(inputPath, targetExt, outDir string) error {
	sofficePath, err := resolveSoffice()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sofficeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		sofficePath,
		"--headless", "--norestore", "--nologo",
		"--convert-to", targetExt,
		"--outdir", outDir,
		inputPath,
	)
	hideConsole(cmd)
	// --outdir and inputPath are both absolute, so cmd.Dir has no bearing on
	// where files are read/written — but it still matters to soffice/macOS.
	// outDir can be inside a TCC-protected folder (e.g. ~/Downloads); if the
	// process lacks access there, soffice fails to even load the source file
	// with a misleading "source file could not be loaded" error. Run from a
	// neutral, always-accessible directory instead.
	cmd.Dir = os.TempDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("soffice timed out after %s converting %s — the file may be corrupt, password-protected, or otherwise stuck", sofficeTimeout, filepath.Base(inputPath))
		}
		return fmt.Errorf("soffice failed: %s", cleanSofficeStderr(stderr.String()))
	}
	return nil
}

// cleanSofficeStderr strips soffice's routine, harmless "Fontconfig warning:
// ..." startup noise so the actual error (if any) isn't buried behind it —
// confirmed to matter in practice: with these left in, a real error can be
// pushed entirely past the UI's truncated error display.
func cleanSofficeStderr(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "Fontconfig") {
			continue
		}
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return "unknown error (no output)"
	}
	return strings.Join(kept, "; ")
}

func pdf2docx(pdfPath, docxPath string) error {
	script := fmt.Sprintf(
		"from pdf2docx import Converter; c = Converter(%q); c.convert(%q); c.close()",
		pdfPath, docxPath,
	)
	py, err := findPythonWithPdf2docx()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sofficeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-c", script)
	hideConsole(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("pdf2docx timed out after %s", sofficeTimeout)
		}
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
		"soffice":  ok,
		"pdf2docx": a.CheckPdf2docx(),
		"tools":    goruntime.GOOS != "windows" || toolsInstalled(),
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

// ReadBinaryFile returns a file's bytes base64-encoded so the frontend can
// hand them to PDF.js / canvas APIs (offline, no fetch of file:// URLs).
func (a *App) ReadBinaryFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// WriteBinaryFile writes base64 bytes back to an output file (e.g. edited PDF).
func (a *App) WriteBinaryFile(path string, b64 string) error {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

var textEditExts = map[string]bool{".html": true, ".txt": true, ".rtf": true, ".svg": true}

// ReadConvertedFile returns the text content of a previously converted
// HTML/TXT/RTF/SVG file so it can be reviewed and edited in-app.
func (a *App) ReadConvertedFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if !textEditExts[ext] {
		return "", fmt.Errorf("only HTML, TXT, RTF and SVG files can be edited as text in-app")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveConvertedFile writes edited content back over a converted text-based file.
func (a *App) SaveConvertedFile(path, content string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if !textEditExts[ext] {
		return fmt.Errorf("only HTML, TXT, RTF and SVG files can be edited as text in-app")
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ConvertToDocxBytes returns base64-encoded DOCX bytes for a converted Word
// file so the frontend can extract rich text (via mammoth) for editing.
// DOCX files are read directly; legacy DOC/ODT are bridged through soffice
// since mammoth only understands the DOCX zip format.
func (a *App) ConvertToDocxBytes(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".docx" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(data), nil
	}
	if ext != ".doc" && ext != ".odt" {
		return "", fmt.Errorf("only DOCX, DOC and ODT files can be edited as rich text in-app")
	}
	tmpDir, err := os.MkdirTemp("", "bondi-docxbridge-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	if err := soffice(path, "docx", tmpDir); err != nil {
		return "", fmt.Errorf("could not prepare %s for editing: %w", ext, err)
	}
	base := strings.TrimSuffix(filepath.Base(path), ext)
	data, err := os.ReadFile(filepath.Join(tmpDir, base+".docx"))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// SaveDocxEditAs writes edited rich-text content back to path. docxB64 is a
// freshly generated DOCX (from the frontend's HTML->DOCX writer). If the
// original file is DOCX it's written directly; DOC/ODT are bridged back
// through soffice so the file on disk keeps its original format.
func (a *App) SaveDocxEditAs(path string, docxB64 string) error {
	ext := strings.ToLower(filepath.Ext(path))
	data, err := base64.StdEncoding.DecodeString(docxB64)
	if err != nil {
		return err
	}
	if ext == ".docx" {
		return os.WriteFile(path, data, 0o644)
	}
	if ext != ".doc" && ext != ".odt" {
		return fmt.Errorf("only DOCX, DOC and ODT files can be edited as rich text in-app")
	}
	tmpDir, err := os.MkdirTemp("", "bondi-docxbridge-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	tmpDocx := filepath.Join(tmpDir, base+".docx")
	if err := os.WriteFile(tmpDocx, data, 0o644); err != nil {
		return err
	}
	targetExt := strings.TrimPrefix(ext, ".")
	if err := soffice(tmpDocx, targetExt, tmpDir); err != nil {
		return fmt.Errorf("could not save as %s: %w", ext, err)
	}
	return moveOrRename(filepath.Join(tmpDir, base+"."+targetExt), path)
}
