#!/usr/bin/env python3
import cgi
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = 8080
MAX_UPLOAD_SIZE = 50 * 1024 * 1024
CONVERT_TIMEOUT = 120
OUT_DIR = "/tmp/out"
HOST = "0.0.0.0"

BASE_HEADERS = {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "POST, GET, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type",
}


FORMATS = {
    "pdf":   {"filter": "pdf",              "ext": "pdf",  "mime": "application/pdf",  "chain": "draw"},
    "png":   {"filter": "png",              "ext": "png",  "mime": "image/png",        "chain": "draw"},
    "jpg":   {"filter": "jpg",              "ext": "jpg",  "mime": "image/jpeg",       "chain": "draw"},
    "svg":   {"filter": "svg",              "ext": "svg",  "mime": "image/svg+xml",    "chain": "draw"},
    "html":  {"filter": "html",             "ext": "html", "mime": "text/html",        "chain": "draw"},
    "odg":   {"filter": "odg",              "ext": "odg",  "mime": "application/vnd.oasis.opendocument.graphics", "chain": "draw"},
    "docx":  {"filter": "docx",             "ext": "docx", "mime": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "chain": "pdf2docx"},
    "doc":   {"filter": "doc",              "ext": "doc",  "mime": "application/msword", "chain": "pdf2docx"},
    "odt":   {"filter": "odt",              "ext": "odt",  "mime": "application/vnd.oasis.opendocument.text", "chain": "pdf2docx"},
    "rtf":   {"filter": "rtf",              "ext": "rtf",  "mime": "application/rtf",  "chain": "pdf2docx"},
    "txt":   {"filter": "txt:Text (encoded):UTF8", "ext": "txt", "mime": "text/plain", "chain": "pdf2docx"},
}

DEFAULT_FORMAT = "pdf"


def run_soffice(input_path, filter_spec, out_dir):
    try:
        return subprocess.run(
            [
                "soffice",
                "--headless",
                "--convert-to",
                filter_spec,
                "--outdir",
                out_dir,
                input_path,
            ],
            capture_output=True,
            timeout=CONVERT_TIMEOUT,
        )
    except subprocess.TimeoutExpired:
        raise TimeoutError


def expected_outpath(input_path, out_dir, ext):
    return os.path.join(out_dir, os.path.splitext(os.path.basename(input_path))[0] + "." + ext)


def convert_pub(pub_path, fmt):
    fmt = (fmt or DEFAULT_FORMAT).lower()
    if fmt not in FORMATS:
        raise ValueError(f"Unsupported format: {fmt}")

    spec = FORMATS[fmt]
    os.makedirs(OUT_DIR, exist_ok=True)

    try:
        src = pub_path
        if spec["chain"] == "pdf2docx":
            src = _docx_midstep(pub_path)
            if fmt == "docx":
                return src

        result = run_soffice(src, spec["filter"], OUT_DIR)
        if result.returncode != 0:
            stderr = result.stderr.decode("utf-8", errors="replace")
            raise RuntimeError(f"soffice failed with exit code {result.returncode}: {stderr}")

        out = expected_outpath(src, OUT_DIR, spec["ext"])
        if spec["ext"] == "jpg":
            jpeg = expected_outpath(src, OUT_DIR, "jpeg")
            if os.path.exists(jpeg) and not os.path.exists(out):
                out = jpeg
        if not os.path.exists(out):
            raise RuntimeError(f"soffice produced no .{spec['ext']} output")
        return out
    finally:
        _cleanup_pdf_midstep()


def _docx_midstep(pub_path):
    """.pub -> PDF -> DOCX. Returns the DOCX path (the writer-format backbone)."""
    pdf_path = _pdf_midstep(pub_path)
    try:
        return _pdf_to_docx(pdf_path)
    finally:
        _cleanup_pdf_midstep()


def _pdf_midstep(pub_path):
    """Convert .pub to PDF (the shared backbone for writer formats)."""
    os.makedirs(MID_DIR, exist_ok=True)
    work_dir = tempfile.mkdtemp(prefix="pub-pdf-")
    _MID_STATE["dir"] = work_dir
    result = run_soffice(pub_path, "pdf", work_dir)
    if result.returncode != 0:
        stderr = result.stderr.decode("utf-8", errors="replace")
        raise RuntimeError(f"soffice->pdf failed with exit code {result.returncode}: {stderr}")
    out = expected_outpath(pub_path, work_dir, "pdf")
    if not os.path.exists(out):
        raise RuntimeError("soffice->pdf produced no output")
    return out


def _pdf_to_docx(pdf_path):
    """PDF -> DOCX via pdf2docx (open-source reflow of the PDF layout)."""
    docx_path = expected_outpath(pdf_path, OUT_DIR, "docx")
    try:
        from pdf2docx import Converter
        cv = Converter(pdf_path)
        try:
            cv.convert(docx_path)
        finally:
            cv.close()
    except Exception as e:
        raise RuntimeError(f"pdf2docx failed: {e}")
    if not os.path.exists(docx_path):
        raise RuntimeError("pdf2docx produced no output")
    return docx_path


MID_DIR = "/tmp/mid"
_MID_STATE = {"dir": None}


def _cleanup_pdf_midstep():
    d = _MID_STATE.get("dir")
    if d and os.path.isdir(d):
        shutil.rmtree(d, ignore_errors=True)
        _MID_STATE["dir"] = None


class Handler(BaseHTTPRequestHandler):
    server_version = "PubConverter/1.0"

    def _send(self, status, content_type, body, extra_headers=None):
        self.send_response(status)
        for key, value in BASE_HEADERS.items():
            self.send_header(key, value)
        if extra_headers:
            for key, value in extra_headers.items():
                self.send_header(key, value)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _json(self, status, obj):
        body = json.dumps(obj).encode("utf-8")
        self._send(status, "application/json", body)

    def do_OPTIONS(self):
        self.send_response(204)
        for key, value in BASE_HEADERS.items():
            self.send_header(key, value)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self):
        if self.path == "/health" or self.path == "/health/":
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": "Not found"})

    def do_POST(self):
        if self.path != "/convert":
            self._json(404, {"error": "Not found"})
            return

        content_type = self.headers.get("Content-Type", "")
        content_length = self.headers.get("Content-Length")

        if content_length:
            try:
                if int(content_length) > MAX_UPLOAD_SIZE:
                    self._json(413, {"error": "File too large (max 50MB)"})
                    return
            except ValueError:
                pass

        if not content_type.startswith("multipart/form-data"):
            self._json(400, {"error": "Expected multipart/form-data with a 'file' field"})
            return

        form = cgi.FieldStorage(
            fp=self.rfile,
            headers=self.headers,
            environ={
                "REQUEST_METHOD": "POST",
                "CONTENT_TYPE": content_type,
            },
        )
        file_field = form["file"]
        if not file_field.filename:
            self._json(400, {"error": "No file uploaded"})
            return

        fmt = DEFAULT_FORMAT
        if "format" in form:
            fmt = form["format"].value or DEFAULT_FORMAT

        filename = os.path.basename(file_field.filename)
        if not filename.lower().endswith(".pub"):
            self._json(400, {"error": "Invalid file type: expected a .pub file"})
            return

        if fmt not in FORMATS:
            self._json(
                400,
                {
                    "error": f"Unsupported format: {fmt}",
                    "formats": sorted(FORMATS.keys()),
                },
            )
            return

        data = file_field.file.read()
        if len(data) > MAX_UPLOAD_SIZE:
            self._json(413, {"error": "File too large (max 50MB)"})
            return

        tmp_dir = tempfile.mkdtemp(prefix="pub-upload-")
        out_pdf = None
        try:
            pub_path = os.path.join(tmp_dir, filename)
            with open(pub_path, "wb") as f:
                f.write(data)

            spec = FORMATS[fmt]
            try:
                out_pdf = convert_pub(pub_path, fmt)
            except TimeoutError:
                self._json(504, {"error": "Conversion timed out"})
                return
            except ValueError as e:
                self._json(400, {"error": str(e)})
                return
            except RuntimeError as e:
                self._json(500, {"error": "Conversion failed", "detail": str(e)})
                return

            if not os.path.exists(out_pdf):
                self._json(500, {"error": "Conversion failed: no output was produced"})
                return

            with open(out_pdf, "rb") as f:
                out_data = f.read()

            base = os.path.splitext(filename)[0]
            self._send(
                200,
                spec["mime"],
                out_data,
                extra_headers={"Content-Disposition": f'attachment; filename="{base}.{spec["ext"]}"'},
            )
        finally:
            shutil.rmtree(tmp_dir, ignore_errors=True)
            if out_pdf and os.path.exists(out_pdf):
                try:
                    os.remove(out_pdf)
                except OSError:
                    pass

    def log_message(self, format, *args):
        sys.stderr.write("%s - - [%s] %s\n" % (self.address_string(), self.log_date_time_string(), format % args))


def main():
    server = ThreadingHTTPServer((HOST, PORT), Handler)

    def shutdown(signum, frame):
        server.shutdown()
        server.server_close()

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)

    print(f"PubConverter listening on {HOST}:{PORT}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()