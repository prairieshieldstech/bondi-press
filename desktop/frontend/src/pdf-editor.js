import { PDFDocument, rgb, StandardFonts } from 'pdf-lib';
import fontkit from '@pdf-lib/fontkit';

// Page images are rendered server-side via PyMuPDF and served over loopback
// HTTP (full fidelity). This class only manages the edit overlays + export.

export class PdfEditor {
  constructor() {
    this.pages = [];       // {num, widthPt, heightPt}
    this.overlays = [];    // {pageNum, type, x, y, w, h, content, style}
    this.ptFactor = 1;     // display px -> PDF point conversion
  }

  // data: original PDF bytes (for faithful re-export), pageSizes from RenderPdfToImages
  init(pdfBytes, pageSizes) {
    this.pdfBytes = pdfBytes;
    this.overlays = [];
    this.pages = pageSizes.map((s, i) => ({
      num: i + 1,
      widthPt: s ? s.widthPt : 612,
      heightPt: s ? s.heightPt : 792
    }));
    return this.pages.length;
  }

  // Read page dimensions (in PDF points) from the original bytes via pdf-lib.
  async getPageSizes(pdfBytes) {
    const pdfDoc = await PDFDocument.load(pdfBytes, { ignoreEncryption: true });
    return pdfDoc.getPages().map(p => ({ widthPt: p.getWidth(), heightPt: p.getHeight() }));
  }

  addTextOverlay(pageNum, x, y, text = 'Click to edit', style = {}) {
    const overlay = {
      id: Date.now() + Math.random(),
      pageNum,
      type: 'text',
      x, y, // display px in page-image space
      w: style.w || 140,
      h: style.h || 30,
      content: text,
      style: {
        fontSize: style.fontSize || 14,
        fontFamily: style.fontFamily || 'Helvetica',
        color: style.color || '#000000',
        bold: style.bold || false,
        italic: style.italic || false,
        ...style
      }
    };
    this.overlays.push(overlay);
    return overlay;
  }

  removeOverlay(id) {
    this.overlays = this.overlays.filter(o => o.id !== id);
  }

  updateOverlay(id, changes) {
    const overlay = this.overlays.find(o => o.id === id);
    if (overlay) Object.assign(overlay, changes);
  }

  getOverlaysForPage(pageNum) {
    return this.overlays.filter(o => o.pageNum === pageNum);
  }

  // Export the ORIGINAL PDF re-drawn with the text overlays baked in.
  async exportPdf() {
    const pdfDoc = await PDFDocument.load(this.pdfBytes, { ignoreEncryption: true });
    pdfDoc.registerFontkit(fontkit);

    const fontCache = {};
    const getFont = async (style) => {
      let name = StandardFonts.Helvetica;
      if (style.bold && style.italic) name = StandardFonts.HelveticaBoldOblique;
      else if (style.bold) name = StandardFonts.HelveticaBold;
      else if (style.italic) name = StandardFonts.HelveticaOblique;
      if (!fontCache[name]) fontCache[name] = await pdfDoc.embedFont(name);
      return fontCache[name];
    };

    for (const overlay of this.overlays) {
      if (overlay.type !== 'text') continue;
      const page = pdfDoc.getPage(overlay.pageNum - 1);
      const ptH = page.getHeight();
      // overlay.x/y and fontSize are stored in PDF point space.
      const fontSize = overlay.style.fontSize || 14;
      const x = overlay.x;
      const y = ptH - overlay.y - fontSize; // PDF y is bottom-up
      const color = this.hexToRgb(overlay.style.color || '#000000');
      const font = await getFont(overlay.style);
      page.drawText(overlay.content, {
        x, y, size: fontSize, font,
        color: rgb(color.r / 255, color.g / 255, color.b / 255)
      });
    }

    return pdfDoc.save();
  }

  hexToRgb(hex) {
    const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
    return result ? {
      r: parseInt(result[1], 16),
      g: parseInt(result[2], 16),
      b: parseInt(result[3], 16)
    } : { r: 0, g: 0, b: 0 };
  }
}