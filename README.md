# Bondi Press

Convert your Microsoft Publisher archives to modern formats — free desktop app.

Microsoft retires Publisher from Microsoft 365 on **October 1, 2026**. Bondi Press saves
your `.pub` files before the format goes dark: drag in your library, pick a format, and
get clean PDFs, Word documents, or images on your Mac, Windows, or Linux machine.
Everything runs locally — nothing to install, nothing to host, nothing to pay for.

## Supported formats

PDF, PNG, JPG, SVG, HTML, ODG, DOCX, DOC, ODT, RTF, TXT

## Requirements

- **macOS / Windows / Linux** — native desktop app built with [Wails v2](https://wails.io)
- [LibreOffice](https://www.libreoffice.org) installed (used as the conversion engine)
- Python 3 with [pdf2docx](https://github.com/dothinking/pdf2docx) for Word output

## Building from source

```sh
cd desktop
wails build            # produces build/bin/bondi-press
```

## Releases

Prebuilt installers for macOS, Windows, and Linux are attached to
[GitHub Releases](https://github.com/prairieshieldstech/bondi-press/releases).
They are built automatically by GitHub Actions on every tag.

## Website

The marketing site lives in the repo root (`src/`), an [Astro](https://astro.build)
single-page build. `pnpm build` outputs static files to `dist/`.

## License

MIT — see [LICENSE](LICENSE).