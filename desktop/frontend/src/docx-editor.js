import mammoth from 'mammoth';
import { Document, Packer, Paragraph, TextRun, HeadingLevel, ImageRun, FrameAnchorType, HeightRule, HorizontalPositionRelativeFrom, VerticalPositionRelativeFrom, AlignmentType, LineRuleType } from 'docx';

// DOCX/DOC/ODT editor: mammoth extracts rich text + images to HTML for
// editing in the existing contenteditable sheet; on save we walk that HTML
// back into a DOCX document (paragraphs/headings/bold/italic/underline/
// lists) and hand the bytes back to Go, which bridges DOC/ODT through
// soffice so the file on disk keeps its original format.

// bytes: ArrayBuffer of DOCX content (already bridged from DOC/ODT by Go).
export async function docxToHtml(bytes) {
  const result = await mammoth.convertToHtml(
    { arrayBuffer: bytes },
    { convertImage: mammoth.images.imgElement(async (image) => {
        const b64 = await image.read('base64');
        return { src: `data:${image.contentType};base64,${b64}` };
      })
    }
  );
  return result.value;
}

function runsFromInline(node, state = { bold: false, italic: false, underline: false }) {
  const runs = [];
  node.childNodes.forEach((child) => {
    if (child.nodeType === Node.TEXT_NODE) {
      const text = child.textContent;
      if (!text) return;
      runs.push(new TextRun({ text, bold: state.bold, italics: state.italic, underline: state.underline ? {} : undefined }));
      return;
    }
    if (child.nodeType !== Node.ELEMENT_NODE) return;
    const tag = child.tagName.toLowerCase();
    if (tag === 'br') { runs.push(new TextRun({ text: '', break: 1 })); return; }
    const next = {
      bold: state.bold || tag === 'strong' || tag === 'b',
      italic: state.italic || tag === 'em' || tag === 'i',
      underline: state.underline || tag === 'u',
    };
    runs.push(...runsFromInline(child, next));
  });
  return runs;
}

const HEADING_MAP = {
  h1: HeadingLevel.HEADING_1, h2: HeadingLevel.HEADING_2, h3: HeadingLevel.HEADING_3,
  h4: HeadingLevel.HEADING_4, h5: HeadingLevel.HEADING_5, h6: HeadingLevel.HEADING_6,
};

function imageDimensions(src) {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve({ width: img.naturalWidth || 300, height: img.naturalHeight || 200 });
    img.onerror = () => resolve({ width: 300, height: 200 });
    img.src = src;
  });
}

// Re-embeds an <img src="data:..."> as a docx ImageRun, scaled to fit a page.
async function imageParagraph(imgEl) {
  const src = imgEl.getAttribute('src') || '';
  if (!src.startsWith('data:')) return new Paragraph({});
  const res = await fetch(src);
  const data = await res.arrayBuffer();
  let { width, height } = await imageDimensions(src);
  const maxW = 500;
  if (width > maxW) { height = Math.round(height * (maxW / width)); width = maxW; }
  return new Paragraph({ children: [new ImageRun({ data, transformation: { width, height } })] });
}

async function paragraphsFromBlock(el) {
  const tag = el.tagName ? el.tagName.toLowerCase() : '';
  if (tag === 'ul' || tag === 'ol') {
    return Array.from(el.children).filter(li => li.tagName.toLowerCase() === 'li').map((li) => new Paragraph({
      children: runsFromInline(li),
      bullet: tag === 'ul' ? { level: 0 } : undefined,
      numbering: tag === 'ol' ? { reference: 'ordered-list', level: 0 } : undefined,
    }));
  }
  if (HEADING_MAP[tag]) {
    return [new Paragraph({ heading: HEADING_MAP[tag], children: runsFromInline(el) })];
  }
  if (tag === 'img') {
    return [await imageParagraph(el)];
  }
  // A <p> that wraps a single image (mammoth's typical shape for figures).
  if ((tag === 'p' || tag === 'div') && el.children.length === 1 && el.children[0].tagName.toLowerCase() === 'img') {
    return [await imageParagraph(el.children[0])];
  }
  // p, div, or bare text — treat as a normal paragraph.
  return [new Paragraph({ children: runsFromInline(el) })];
}

// root: the contenteditable element holding the edited rich text.
// Returns an ArrayBuffer of a freshly built DOCX.
export async function htmlToDocxBytes(root) {
  const paragraphs = [];
  for (const node of Array.from(root.childNodes)) {
    if (node.nodeType === Node.TEXT_NODE) {
      if (node.textContent.trim()) paragraphs.push(new Paragraph({ children: [new TextRun(node.textContent)] }));
      continue;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) continue;
    paragraphs.push(...(await paragraphsFromBlock(node)));
  }
  if (paragraphs.length === 0) paragraphs.push(new Paragraph({}));

  const doc = new Document({
    numbering: {
      config: [{
        reference: 'ordered-list',
        levels: [{ level: 0, format: 'decimal', text: '%1.', alignment: 'start' }],
      }],
    },
    sections: [{ children: paragraphs }],
  });
  const blob = await Packer.toBlob(doc);
  return blob.arrayBuffer();
}

// ---- Layout-faithful DOCX (from ExtractPubLayout's geometry JSON) ----
// Unlike htmlToDocxBytes (hand-edited HTML -> flowing paragraphs) and unlike
// the pdf2docx path (guesses paragraph/column structure from a flattened
// PDF), this places every text run and image at its TRUE page position —
// read straight from the .pub file's own object model via libmspub — using
// absolutely positioned Word FRAMES (w:framePr) and floating pictures.
//
// NOTE on Textbox vs Frame: docx.js also ships a `Textbox` (legacy VML
// v:shape) API, which looked like the obvious tool for this. It silently
// fails to render in LibreOffice — verified with a minimal single-textbox
// document: the shape's XML is well-formed but produces zero visible text
// after a docx->pdf round trip. `Paragraph({ frame: {...} })` (the older,
// far more universally supported w:framePr positioning) renders correctly
// and was verified to land at the exact requested coordinates. Since this
// app previews/bridges everything through LibreOffice, frame is the only
// option that actually works here, not just in theory.
const PT_TO_PX = 96 / 72;   // ImageRun.transformation is in px @ 96dpi
const PT_TO_TWIP = 20;      // frame/page geometry is in twips (1pt = 20 twips)

const ALIGN_MAP = {
  left: AlignmentType.LEFT,
  center: AlignmentType.CENTER,
  right: AlignmentType.RIGHT,
  justify: AlignmentType.JUSTIFIED,
};

async function imageItemToRun(item) {
  const res = await fetch(item.href);
  const data = await res.arrayBuffer();
  return new ImageRun({
    data,
    transformation: { width: Math.round(item.w * PT_TO_PX), height: Math.round(item.h * PT_TO_PX) },
    floating: {
      horizontalPosition: { relative: HorizontalPositionRelativeFrom.PAGE, offset: Math.round(item.x * 12700) },
      verticalPosition: { relative: VerticalPositionRelativeFrom.PAGE, offset: Math.round(item.y * 12700) },
      allowOverlap: true,
    },
  });
}

// item.x/y/w/h here are the REAL frame rectangle read from the .pub file's
// own object model (libmspub's startTextObject, via pub2raw — see
// ExtractPubLayout) — not a guess. Every paragraph in the frame shares the
// same frame anchor, which is how Word links them into one continuous
// floating text box instead of separate unrelated frames.
//
// Publisher insets text from the frame's own edges (fo:padding-*); Word's
// w:framePr has no separate padding concept, so the same effect is achieved
// by shrinking the frame rectangle itself by the real padding amounts —
// text still starts at the correct absolute position, it's just that the
// frame we hand Word is the INNER (post-padding) box rather than the outer
// one libmspub reported.
function textItemToFrames(item) {
  const padLeft = item.padLeft || 0, padRight = item.padRight || 0;
  const padTop = item.padTop || 0, padBottom = item.padBottom || 0;
  const innerX = item.x + padLeft;
  const innerY = item.y + padTop;
  const innerW = Math.max(item.w - padLeft - padRight, 4);
  const innerH = Math.max(item.h - padTop - padBottom, 4);
  const frameProps = {
    type: 'absolute',
    position: { x: Math.round(innerX * PT_TO_TWIP), y: Math.round(innerY * PT_TO_TWIP) },
    width: Math.round(innerW * PT_TO_TWIP),
    height: Math.round(innerH * PT_TO_TWIP),
    rule: HeightRule.AUTO, // real height from the source; AUTO only guards against rare overflow
    anchor: { horizontal: FrameAnchorType.PAGE, vertical: FrameAnchorType.PAGE },
  };
  const paragraphs = item.paragraphs
    .map(p => ({
      ...p,
      runs: p.runs.filter(r => r.text && r.text.trim()),
    }))
    .filter(p => p.runs.length);
  if (paragraphs.length === 0) return [];
  return paragraphs.map(p => new Paragraph({
    frame: frameProps,
    alignment: ALIGN_MAP[p.align] || AlignmentType.LEFT,
    // pub2raw reports line-height as a % of single-spacing; docx's `line`
    // is in 240ths where 240 = 100% under lineRule "auto".
    spacing: p.lineHeightPct
      ? { line: Math.round((p.lineHeightPct / 100) * 240), lineRule: LineRuleType.AUTO }
      : undefined,
    children: p.runs.map(r => new TextRun({
      text: r.text,
      bold: r.bold,
      italics: r.italic,
      font: r.fontFamily,
      size: Math.round((r.sizePt || 10) * 2), // half-points
      color: (r.color || '#000000').replace('#', ''),
    })),
  }));
}

// layout: the parsed {pages:[{widthPt,heightPt,items:[...]}]} from
// ExtractPubLayout. Returns an ArrayBuffer of the generated DOCX.
export async function layoutToDocxBytes(layout) {
  const sections = [];
  for (const page of layout.pages) {
    const children = [];
    for (const item of page.items) {
      if (item.type === 'text') children.push(...textItemToFrames(item));
      else if (item.type === 'image') children.push(new Paragraph({ children: [await imageItemToRun(item)] }));
    }
    sections.push({
      properties: {
        page: {
          size: {
            width: Math.round(page.widthPt * PT_TO_TWIP),
            height: Math.round(page.heightPt * PT_TO_TWIP),
          },
          margin: { top: 0, bottom: 0, left: 0, right: 0 },
        },
      },
      children: children.length ? children : [new Paragraph({})],
    });
  }
  const doc = new Document({ sections });
  const blob = await Packer.toBlob(doc);
  return blob.arrayBuffer();
}
