import './style.css';
import { PdfEditor } from './pdf-editor';
import { docxToHtml, htmlToDocxBytes, layoutToDocxBytes } from './docx-editor';

import {Convert, ConvertBatch, Formats, SelectPubFile, SelectSavePath, CheckStatus, RevealFile, ReadConvertedFile, SaveConvertedFile, ReadBinaryFile, WriteBinaryFile, PreparePreview, CleanupPreview, RenderPdfToImages, PreviewPub, ConvertToDocxBytes, SaveDocxEditAs, ToggleFullscreen, ExtractPubLayout, ExportPathFor, InstallEngine} from '../wailsjs/go/main/App';
import {EventsOn} from '../wailsjs/runtime/runtime';
import {WindowMinimise, Quit, OnFileDrop} from '../wailsjs/runtime/runtime';
import logoUrl from './assets/logo.svg';

const state = {
    pubPaths: [],
    format: 'pdf',
    busy: false,
};

const FORMATS = {
    pdf:  ['PDF',          'Fixed layout, universal'],
    docx: ['DOCX (Word)',  'Editable Word document'],
    doc:  ['DOC',          'Legacy Word document'],
    odt:  ['ODT',          'OpenDocument text'],
    rtf:  ['RTF',          'Rich text'],
    txt:  ['Text',         'Plain text'],
    png:  ['PNG',          'Raster image'],
    jpg:  ['JPG',          'Raster image'],
    svg:  ['SVG',          'Vector image'],
    html: ['HTML',         'Web page'],
    odg:  ['ODG',          'Vector drawing'],
};

const els = {};

document.querySelector('#app').innerHTML = `
    <div class="win" id="win">
        <header class="titlebar" id="titlebar">
            <button class="brand" id="brand-btn" title="About">
                <img class="brand-logo" src="${logoUrl}" alt="Bondi Press"/>
                <span class="brand-name">Bondi Press</span>
            </button>
            <div class="win-controls">
                <button class="wc" id="btn-fullscreen" title="Toggle fullscreen">⛶</button>
                <button class="wc" id="btn-min" title="Minimise">–</button>
                <button class="wc wc-close" id="btn-close" title="Close">✕</button>
            </div>
        </header>

        <main class="body">
            <section class="stage" id="stage-source">
                <div class="eyebrow">Step 1 · Choose</div>
                <h2 class="stage-title">Add Publisher files</h2>
                <p class="stage-sub">One or many — batch convert them all in one go.</p>

                <div class="dropzone" id="dropzone" tabindex="0">
                    <div class="dz-inner">
                        <svg class="dz-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M12 16V4m0 0L7 9m5-5 5 5"/><path d="M4 19a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2"/>
                        </svg>
                        <p class="dz-title">Drop <strong>.pub</strong> files here</p>
                        <p class="dz-sub">or click to browse · add as many as you like</p>
                    </div>
                </div>

                <div class="filelist hidden" id="filelist">
                    <div class="filelist-head">
                        <span class="filelist-count" id="file-count"></span>
                        <button class="text-btn" id="add-more">Add more</button>
                    </div>
                    <ul class="filelist-items" id="file-items"></ul>
                </div>

                <div class="stage-actions">
                    <button class="btn btn-primary hidden" id="to-format">Next — pick format</button>
                </div>
            </section>

            <section class="stage hidden" id="stage-format">
                <div class="eyebrow">Step 2 · Output</div>
                <h2 class="stage-title">Choose a format</h2>
                <p class="stage-sub" id="format-count"></p>
                <div class="format-grid" id="format-grid"></div>
                <div class="stage-actions">
                    <button class="btn btn-ghost" id="back">Back</button>
                    <button class="btn btn-primary" id="convert-btn">Convert all</button>
                </div>
            </section>

            <section class="stage hidden" id="stage-pubpreview">
                <div class="eyebrow">Step 3 · Preview</div>
                <h2 class="stage-title">Check the layout</h2>
                <p class="stage-sub" id="pvw-sub"></p>
                <div class="pdfedit-toolbar">
                    <button class="etb" id="pvw-prevfile" title="Previous file">⟨ file</button>
                    <span class="pdfedit-page" id="pvw-filecount"></span>
                    <button class="etb" id="pvw-nextfile" title="Next file">file ⟩</button>
                    <span class="etb-sep"></span>
                    <span class="pdfedit-page" id="pvw-page">1 / 1</span>
                    <span class="etb-sep"></span>
                    <button class="etb" id="pvw-prev" title="Previous page">‹</button>
                    <button class="etb" id="pvw-next" title="Next page">›</button>
                </div>
                <div class="pdfedit-canvaswrap" id="pvw-canvaswrap">
                    <div class="pdfedit-pagescroll">
                        <div class="pdfedit-pageitem">
                            <img class="pdfedit-pageimg" id="pvw-pageimg" alt="Rendering preview…" />
                        </div>
                    </div>
                </div>
                <div class="stage-actions">
                    <button class="btn btn-ghost" id="pvw-back">Back</button>
                    <button class="btn btn-primary" id="pvw-confirm">Looks good — convert &amp; save</button>
                </div>
            </section>

            <section class="stage hidden" id="stage-progress">
                <div class="spinner-ring"></div>
                <h2 class="stage-title" id="progress-title">Converting…</h2>
                <p class="stage-sub" id="progress-text">Getting started…</p>
                <div class="prog-list" id="prog-list"></div>
            </section>

            <section class="stage hidden" id="stage-done">
                <svg class="done-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 13l4 4L19 7"/></svg>
                <h2 class="stage-title" id="done-count">Done</h2>
                <p class="done-path" id="done-path"></p>
                <div class="done-list" id="done-list"></div>
                <div class="stage-actions">
                    <button class="btn btn-primary" id="convert-another">Convert more</button>
                </div>
            </section>

            <section class="stage hidden" id="stage-edit">
                <div class="eyebrow">Review &amp; edit</div>
                <h2 class="stage-title" id="edit-title">Edit</h2>
                <p class="stage-sub" id="edit-sub"></p>
                <div class="edit-toolbar hidden" id="edit-toolbar">
                    <button class="etb" data-cmd="bold" title="Bold"><strong>B</strong></button>
                    <button class="etb" data-cmd="italic" title="Italic"><em>I</em></button>
                    <button class="etb" data-cmd="underline" title="Underline"><u>U</u></button>
                    <span class="etb-sep"></span>
                    <button class="etb" data-cmd="formatBlock" data-val="H2" title="Heading">H2</button>
                    <button class="etb" data-cmd="formatBlock" data-val="P" title="Paragraph">¶</button>
                    <span class="etb-sep"></span>
                    <button class="etb" data-cmd="insertUnorderedList" title="Bullet list">•≡</button>
                    <button class="etb" data-cmd="insertOrderedList" title="Numbered list">1≡</button>
                </div>
                <div class="edit-split hidden" id="edit-split">
                    <div class="edit-codepane">
                        <div class="edit-codelabel">HTML</div>
                        <textarea class="edit-code" id="edit-code" spellcheck="false" placeholder="Type HTML here..."></textarea>
                    </div>
                    <div class="edit-previewpane">
                        <div class="edit-codelabel">Preview</div>
                        <iframe class="edit-previewframe" id="edit-previewframe" sandbox="allow-same-origin allow-scripts allow-forms"></iframe>
                    </div>
                </div>
                <div class="edit-sheet hidden" id="edit-sheet" contenteditable="true" spellcheck="false"></div>
                <textarea class="edit-text hidden" id="edit-text" spellcheck="false"></textarea>
                <div class="stage-actions">
                    <button class="btn btn-ghost" id="edit-preview">Preview full</button>
                    <button class="btn btn-ghost" id="edit-back">Back</button>
                    <button class="btn btn-primary" id="edit-save">Save</button>
                </div>
            </section>

            <section class="stage hidden" id="stage-pdfedit">
                <div class="eyebrow">Edit PDF</div>
                <h2 class="stage-title" id="pdfedit-title">Edit PDF</h2>
                <p class="stage-sub" id="pdfedit-sub"></p>
                <div class="pdfedit-toolbar">
                    <button class="etb" data-to="" id="pdf-add-text" title="Add text">T</button>
                    <button class="etb" data-to="" id="pdf-zoom-in" title="Zoom in">＋</button>
                    <button class="etb" data-to="" id="pdf-zoom-out" title="Zoom out">－</button>
                    <span class="etb-sep"></span>
                    <span class="pdfedit-page" id="pdfedit-page">1 / 1</span>
                    <span class="etb-sep"></span>
                    <button class="etb" id="pdf-prev" title="Previous page">‹</button>
                    <button class="etb" id="pdf-next" title="Next page">›</button>
                </div>
                <div class="pdfedit-canvaswrap" id="pdfedit-canvaswrap">
                    <div class="pdfedit-pagescroll" id="pdfedit-pagescroll">
                        <div class="pdfedit-pageitem" id="pdfedit-pageitem">
                            <img class="pdfedit-pageimg" id="pdfedit-pageimg" alt="page" />
                            <div class="pdfedit-overlay" id="pdfedit-overlay"></div>
                        </div>
                    </div>
                </div>
                <div class="stage-actions">
                    <button class="btn btn-ghost" id="pdfedit-back">Back</button>
                    <button class="btn btn-primary" id="pdfedit-save">Save &amp; export</button>
                </div>
            </section>

            <section class="stage hidden" id="stage-docxedit">
                <div class="eyebrow">Edit DOCX</div>
                <h2 class="stage-title">Edit document</h2>
                <p class="stage-sub" id="docxedit-sub"></p>
                <div class="pdfedit-toolbar">
                    <span class="pdfedit-page" id="docxedit-page">1 / 1</span>
                    <span class="etb-sep"></span>
                    <button class="etb" id="docxedit-prev" title="Previous page">‹</button>
                    <button class="etb" id="docxedit-next" title="Next page">›</button>
                </div>
                <div class="pdfedit-canvaswrap" id="docxedit-canvaswrap">
                    <div class="pdfedit-pagescroll" id="docxedit-pagescroll"></div>
                </div>
                <div class="stage-actions">
                    <button class="btn btn-ghost" id="docxedit-back">Back</button>
                    <button class="btn btn-primary" id="docxedit-save">Save &amp; export</button>
                </div>
            </section>

            <section class="preview" id="preview" hidden>
                <div class="preview-top">
                    <button class="btn btn-ghost" id="preview-close">‹ Back to edit</button>
                    <span class="preview-title" id="preview-title"></span>
                    <button class="btn btn-primary" id="preview-print">Print…</button>
                </div>
                <iframe class="preview-sheet" id="preview-sheet" sandbox="allow-same-origin allow-scripts allow-forms"></iframe>
            </section>
        </main>

        <footer class="statusbar">
            <span class="dot" id="status-dot"></span>
            <span id="status-text">Checking engine…</span>
            <button id="status-setup" class="status-setup" hidden>Set up engine</button>
        </footer>
    </div>
`;

function el(id) { return document.getElementById(id); }
['win','titlebar','brand-btn','btn-fullscreen','btn-min','btn-close','dropzone','filelist','file-count','add-more','file-items',
 'to-format','stage-format','format-count','format-grid','back','convert-btn','stage-progress',
 'stage-pubpreview','pvw-sub','pvw-prevfile','pvw-filecount','pvw-nextfile','pvw-page','pvw-prev','pvw-next',
 'pvw-pageimg','pvw-back','pvw-confirm',
 'progress-title','progress-text','prog-list','stage-done','done-count','done-path','done-list',
 'convert-another','status-dot','status-text','status-setup',
 'stage-edit','edit-title','edit-sub','edit-toolbar','edit-split','edit-code','edit-previewframe',
 'edit-sheet','edit-text','edit-back','edit-save','edit-preview',
 'stage-pdfedit','pdfedit-title','pdfedit-sub','pdf-add-text','pdf-zoom-in','pdf-zoom-out','pdf-prev','pdf-next',
 'pdfedit-page','pdfedit-canvaswrap','pdfedit-pagescroll','pdfedit-pageitem','pdfedit-pageimg','pdfedit-overlay',
 'pdfedit-back','pdfedit-save',
 'stage-docxedit','docxedit-sub','docxedit-page','docxedit-prev','docxedit-next','docxedit-canvaswrap',
 'docxedit-pagescroll','docxedit-back','docxedit-save',
 'preview','preview-close','preview-title','preview-print','preview-sheet'
].forEach(id => { els[id] = document.getElementById(id); });

function showStage(name) {
    ['stage-source','stage-format','stage-pubpreview','stage-progress','stage-done','stage-edit','stage-pdfedit','stage-docxedit'].forEach(s => {
        document.getElementById(s).classList.toggle('hidden', s !== name);
    });
}

const editState = { path: '', kind: 'txt', original: '' }; // html/txt editor

// ---- PDF editor state (per-open) ----
const pdfState = {
    path: null,
    editor: null,       // PdfEditor instance
    currentPage: 1,
    base64: null,
    scale: 1,
    fileBytes: null,    // original bytes (ArrayBuffer) for preview round-trip
};

async function pickFiles() {
    const path = await SelectPubFile();
    if (!path) return;
    addFiles([path]);
}

function addFiles(paths) {
    const added = paths.filter(p => p && p.toLowerCase().endsWith('.pub'));
    const seen = new Set(state.pubPaths);
    added.forEach(p => { if (!seen.has(p)) { state.pubPaths.push(p); seen.add(p); } });
    renderFileList();
}

function removeFile(path) {
    state.pubPaths = state.pubPaths.filter(p => p !== path);
    renderFileList();
}

function renderFileList() {
    const has = state.pubPaths.length > 0;
    els['filelist'].classList.toggle('hidden', !has);
    els['to-format'].classList.toggle('hidden', !has);
    els['file-count'].textContent = `${state.pubPaths.length} file${state.pubPaths.length === 1 ? '' : 's'}`;
    els['file-items'].innerHTML = state.pubPaths.map((p, i) => `
        <li class="fileitem">
            <span class="fileitem-idx">${i + 1}</span>
            <span class="fileitem-name" title="${p}">${p.split(/[\\/]/).pop()}</span>
            <button class="fileitem-x" data-i="${i}" title="Remove">✕</button>
        </li>
    `).join('');
    els['file-items'].querySelectorAll('.fileitem-x').forEach(b => {
        b.addEventListener('click', () => removeFile(state.pubPaths[+b.dataset.i]));
    });
}

el('to-format').addEventListener('click', () => {
    if (state.pubPaths.length === 0) return;
    showStage('stage-format');
    renderFormats();
});

function renderFormats() {
    els['format-count'].textContent = `${state.pubPaths.length} file${state.pubPaths.length === 1 ? '' : 's'} → ` +
        FORMATS[state.format][0] + ` into “Downloads/Bondi Press exports/”`;
    if (els['format-grid'].children.length > 0) { highlightSelected(); return; }
    els['format-grid'].innerHTML = Object.entries(FORMATS).map(([key, [label, desc]]) => `
        <button class="fbtn" data-f="${key}">
            <span class="fbtn-label">${label}</span>
            <span class="fbtn-desc">${desc}</span>
        </button>
    `).join('');
    els['format-grid'].querySelectorAll('.fbtn').forEach(b => {
        b.addEventListener('click', () => { state.format = b.dataset.f; renderFormats(); });
    });
    highlightSelected();
}

function highlightSelected() {
    els['format-grid'].querySelectorAll('.fbtn').forEach(b => {
        b.classList.toggle('sel', b.dataset.f === state.format);
    });
}

// ---- Pre-save preview: faithful render of the ORIGINAL .pub(s), shown
// before the actual conversion/save runs, since the chosen export format
// (e.g. DOCX) may re-flow content and can't be trusted to "look right" yet.
// Also reused (single-file, read-only) from the results list so you can
// re-check a file's true layout after the fact.
const pvwState = { mode: 'batch', paths: [], fileIdx: 0, pageIdx: 0, returnStage: 'stage-format', imagesByFile: {} };

function currentPreviewPath() { return pvwState.paths[pvwState.fileIdx]; }

async function openBatchPreview() {
    if (state.pubPaths.length === 0 || state.busy) return;
    pvwState.mode = 'batch';
    pvwState.paths = state.pubPaths;
    pvwState.returnStage = 'stage-format';
    els['pvw-confirm'].classList.remove('hidden');
    showStage('stage-pubpreview');
    await loadPreviewFile(0);
}

async function openResultPreview(pubPath) {
    pvwState.mode = 'single';
    pvwState.paths = [pubPath];
    pvwState.returnStage = 'stage-done';
    els['pvw-confirm'].classList.add('hidden');
    showStage('stage-pubpreview');
    await loadPreviewFile(0);
}

async function loadPreviewFile(idx) {
    pvwState.fileIdx = idx;
    pvwState.pageIdx = 0;
    const path = currentPreviewPath();
    els['pvw-sub'].textContent = path.split(/[\\/]/).pop();
    const multi = pvwState.paths.length > 1;
    els['pvw-filecount'].textContent = multi ? `file ${idx + 1} / ${pvwState.paths.length}` : '';
    els['pvw-prevfile'].classList.toggle('hidden', !multi);
    els['pvw-nextfile'].classList.toggle('hidden', !multi);
    els['pvw-prevfile'].disabled = idx === 0;
    els['pvw-nextfile'].disabled = idx === pvwState.paths.length - 1;
    if (!pvwState.imagesByFile[path]) {
        els['pvw-pageimg'].removeAttribute('src');
        els['pvw-page'].textContent = 'Rendering…';
        try {
            pvwState.imagesByFile[path] = await PreviewPub(path);
        } catch (err) {
            alert('Could not render preview: ' + String(err));
            pvwState.imagesByFile[path] = [];
        }
    }
    renderPreviewPage();
}

function renderPreviewPage() {
    const imgs = pvwState.imagesByFile[currentPreviewPath()] || [];
    els['pvw-page'].textContent = imgs.length ? `${pvwState.pageIdx + 1} / ${imgs.length}` : '0 / 0';
    if (imgs[pvwState.pageIdx]) els['pvw-pageimg'].src = imgs[pvwState.pageIdx];
    els['pvw-prev'].disabled = pvwState.pageIdx === 0;
    els['pvw-next'].disabled = pvwState.pageIdx >= imgs.length - 1;
}

els['pvw-prevfile'].addEventListener('click', () => { if (pvwState.fileIdx > 0) loadPreviewFile(pvwState.fileIdx - 1); });
els['pvw-nextfile'].addEventListener('click', () => { if (pvwState.fileIdx < pvwState.paths.length - 1) loadPreviewFile(pvwState.fileIdx + 1); });
els['pvw-prev'].addEventListener('click', () => { if (pvwState.pageIdx > 0) { pvwState.pageIdx--; renderPreviewPage(); } });
els['pvw-next'].addEventListener('click', () => {
    const imgs = pvwState.imagesByFile[currentPreviewPath()] || [];
    if (pvwState.pageIdx < imgs.length - 1) { pvwState.pageIdx++; renderPreviewPage(); }
});
els['pvw-back'].addEventListener('click', () => showStage(pvwState.returnStage));
els['pvw-confirm'].addEventListener('click', () => convert());

async function convert() {
    if (state.pubPaths.length === 0 || state.busy) return;
    state.busy = true;
    showStage('stage-progress');
    renderProgList();

    try {
        const results = state.format === 'docx'
            ? await convertBatchFaithfulDocx(state.pubPaths)
            : await ConvertBatch(state.pubPaths, state.format);
        renderResults(results);
    } catch (err) {
        els['progress-title'].textContent = 'Conversion error';
        els['progress-text'].textContent = String(err) || 'Something went wrong.';
        els['prog-list'].innerHTML = '';
        els['to-format'].classList.remove('hidden');
        setTimeout(() => showStage('stage-source'), 2500);
    } finally {
        state.busy = false;
    }
}

function renderProgList() {
    els['prog-list'].innerHTML = state.pubPaths.map((p, i) => `
        <div class="progitem" data-idx="${i}">
            <span class="progitem-dot"></span>
            <span class="progitem-name">${p.split(/[\\/]/).pop()}</span>
            <span class="progitem-state"></span>
        </div>
    `).join('');
}

function updateProgItem(e) {
    const item = document.querySelector(`.progitem[data-idx="${e.index}"]`);
    if (!item) return;
    const dot = item.querySelector('.progitem-dot');
    const st = item.querySelector('.progitem-state');
    if (e.status === 'start') {
        dot.classList.add('working');
        st.textContent = 'Converting…';
    } else {
        dot.classList.remove('working');
        dot.classList.add(e.status === 'ok' ? 'done' : 'fail');
        st.textContent = e.status === 'ok' ? '✓' : e.error || 'failed';
        els['progress-text'].textContent = `${e.index + 1} of ${e.total} done`;
    }
}

EventsOn('conv:file', updateProgItem);

// DOCX takes a different path than ConvertBatch: it needs docx.js (a
// browser-only library) to build the bytes from ExtractPubLayout's geometry,
// so it can't run purely in Go like the other formats. See docx-editor.js
// layoutToDocxBytes for why this replaces the pdf2docx reflow for DOCX.
async function convertBatchFaithfulDocx(pubPaths) {
    const results = [];
    for (let i = 0; i < pubPaths.length; i++) {
        const p = pubPaths[i];
        updateProgItem({ index: i, total: pubPaths.length, status: 'start' });
        try {
            const outPath = await ExportPathFor(p, 'docx');
            try {
                // Needs libmspub (pub2xhtml) on the system — not bundled,
                // not installed on a normal user's machine today. Never let
                // its absence break DOCX conversion: fall back to the
                // always-available pdf2docx pipeline (Convert), which this
                // app has shipped and relied on since before today.
                const layoutJson = await ExtractPubLayout(p);
                const layout = JSON.parse(layoutJson);
                const bytes = await layoutToDocxBytes(layout);
                await WriteBinaryFile(outPath, arrayBufferToB64(bytes));
            } catch (layoutErr) {
                await Convert(p, outPath, 'docx');
            }
            results.push({ input: p, output: outPath, status: 'ok' });
            updateProgItem({ index: i, total: pubPaths.length, status: 'ok' });
        } catch (err) {
            results.push({ input: p, status: 'error', error: String(err) });
            updateProgItem({ index: i, total: pubPaths.length, status: 'error', error: String(err) });
        }
    }
    return results;
}

const EDITABLE_EXTS = new Set(['pdf', 'html', 'txt', 'rtf', 'svg', 'docx', 'doc', 'odt']);
const PREVIEWABLE_EXTS = new Set(['png', 'jpg']);

function renderResults(results) {
    const okCount = results.filter(r => r.status === 'ok').length;
    els['done-count'].textContent = okCount === results.length
        ? `All ${okCount} converted`
        : `${okCount} of ${results.length} converted`;
    els['done-list'].innerHTML = results.map(r => {
        const path = r.status === 'ok' ? r.output : r.input;
        const ext = (path.split('.').pop() || '').toLowerCase();
        const editable = r.status === 'ok' && EDITABLE_EXTS.has(ext);
        const previewable = r.status === 'ok' && PREVIEWABLE_EXTS.has(ext);
        // "Preview" always renders the ORIGINAL .pub's true layout (faithful,
        // format-independent) so you can check it before trusting a re-flowed
        // export like DOCX. The raw-file preview stays for PNG/JPG since
        // that IS the final output.
        const actions = [
            r.status === 'ok' ? `<button class="resultitem-edit" data-pubpreview="${r.input}">Preview</button>` : '',
            editable ? `<button class="resultitem-edit" data-edit="${path}" data-srcpub="${r.input}">Edit</button>` : '',
            previewable ? `<button class="resultitem-edit" data-preview="${path}">Raw file</button>` : '',
        ].join('');
        return `
        <div class="resultitem ${r.status}">
            <span class="resultitem-name" title="${path}">
                ${path.split(/[\\/]/).pop()}
            </span>
            ${actions}
            <span class="resultitem-state" title="${r.status === 'ok' ? '' : (r.error || 'failed')}">${r.status === 'ok' ? '✓' : r.error || 'failed'}</span>
        </div>
    `;
    }).join('');
    const outDir = results.find(r => r.status === 'ok');
    els['done-path'].textContent = outDir ? outDir.output.split(/[\\/]/).slice(0, -1).join('/') : '';
    els['done-list'].querySelectorAll('.resultitem').forEach((row) => {
        row.querySelectorAll('.resultitem-edit').forEach(btn => {
            btn.addEventListener('click', (e) => {
                e.stopPropagation();
                if (btn.dataset.pubpreview) openResultPreview(btn.dataset.pubpreview);
                else if (btn.dataset.preview) openImagePreview(btn.dataset.preview);
                else openEditor(btn.dataset.edit, btn.dataset.srcpub);
            });
        });
        row.addEventListener('click', () => {
            const name = row.querySelector('.resultitem-name').title || '';
            if (row.classList.contains('ok')) { RevealFile(name); }
        });
    });
    showStage('stage-done');
}

const RICHDOC_EXTS = new Set(['docx', 'doc', 'odt']);
const CODE_PREVIEW_EXTS = new Set(['html', 'svg']);
const EDIT_TITLES = { html: 'Edit HTML', svg: 'Edit SVG', txt: 'Edit text', rtf: 'Edit RTF', docx: 'Edit DOCX', doc: 'Edit DOC', odt: 'Edit ODT' };

async function openEditor(path, srcPub) {
    const ext = (path.split('.').pop() || '').toLowerCase();
    if (ext === 'pdf') { openPdfEditor(path); return; }
    if (ext === 'docx' && srcPub) { openDocxCanvasEditor(srcPub, path); return; }
    if (RICHDOC_EXTS.has(ext)) { openRichDocEditor(path, ext); return; }

    editState.path = path;
    editState.kind = CODE_PREVIEW_EXTS.has(ext) ? 'html' : 'txt';
    els['edit-title'].textContent = EDIT_TITLES[ext] || 'Edit text';
    els['edit-sub'].textContent = path.split(/[\\/]/).pop();
    els['edit-preview'].classList.toggle('hidden', editState.kind !== 'html');
    try {
        const content = await ReadConvertedFile(path);
        editState.original = content;
        els['edit-toolbar'].classList.add('hidden');
        els['edit-sheet'].classList.add('hidden');
        els['edit-text'].classList.add('hidden');
        els['edit-split'].classList.add('hidden');
        if (editState.kind === 'html') {
            els['edit-split'].classList.remove('hidden');
            els['edit-code'].value = content;
            els['edit-code'].oninput = () => debouncePreview(els['edit-code'].value);
            refreshHtmlPreview(content);
        } else {
            els['edit-text'].classList.remove('hidden');
            els['edit-text'].value = content;
        }
        showStage('stage-edit');
    } catch (err) {
        alert('Could not open file for editing: ' + String(err));
    }
}

// ---- DOCX / DOC / ODT rich-text editor ----
// DOC/ODT are bridged through soffice (Go side) to a temp DOCX so mammoth can
// read them; on save the freshly built DOCX is bridged back to the original
// format, so the file on disk keeps its original extension.
async function openRichDocEditor(path, ext) {
    editState.path = path;
    editState.kind = 'richdoc';
    els['edit-title'].textContent = EDIT_TITLES[ext] || 'Edit document';
    els['edit-sub'].textContent = path.split(/[\\/]/).pop();
    els['edit-toolbar'].classList.add('hidden');
    els['edit-sheet'].classList.add('hidden');
    els['edit-text'].classList.add('hidden');
    els['edit-split'].classList.add('hidden');
    els['edit-preview'].classList.add('hidden');
    showStage('stage-edit');
    els['edit-sheet'].innerHTML = '<p>Loading…</p>';
    els['edit-sheet'].classList.remove('hidden');
    try {
        const b64 = await ConvertToDocxBytes(path);
        const html = await docxToHtml(b64ToArrayBuffer(b64));
        els['edit-sheet'].innerHTML = html || '<p></p>';
        els['edit-toolbar'].classList.remove('hidden');
    } catch (err) {
        alert('Could not open document for editing: ' + String(err));
        showStage('stage-done');
    }
}

let _previewTimer = null;
function debouncePreview(html) {
    clearTimeout(_previewTimer);
    _previewTimer = setTimeout(() => refreshHtmlPreview(html), 350);
}

// Loads the edited HTML into an iframe pointed at a temp copy that sits next
// to the original's asset folder, so images / CSS / fonts resolve on disk.
async function refreshHtmlPreview(html, immediate = false) {
    const iframe = els['edit-previewframe'];
    if (immediate && !html.trim()) { iframe.srcdoc = ''; return; }
    try {
        const url = await PreparePreview(editState.path, html);
        // Set src (not srcdoc) so relative asset paths resolve against the temp dir.
        if (iframe.getAttribute('src') !== url) iframe.src = url;
    } catch (err) {
        // Fallback: render inline (no external assets available).
        const doc = iframe.contentDocument || iframe.contentWindow.document;
        doc.open(); doc.write(html); doc.close();
    }
}

async function saveEditor() {
    els['edit-save'].textContent = 'Saving…';
    els['edit-save'].disabled = true;
    try {
        if (editState.kind === 'richdoc') {
            const bytes = await htmlToDocxBytes(els['edit-sheet']);
            await SaveDocxEditAs(editState.path, arrayBufferToB64(bytes));
        } else {
            const content = editState.kind === 'html' ? els['edit-code'].value : els['edit-text'].value;
            await SaveConvertedFile(editState.path, content);
        }
        els['edit-save'].textContent = 'Saved ✓';
    } catch (err) {
        alert('Could not save: ' + String(err));
        els['edit-save'].textContent = 'Save';
    } finally {
        els['edit-save'].disabled = false;
        setTimeout(() => { els['edit-save'].textContent = 'Save'; }, 1400);
    }
}

els['edit-toolbar'].addEventListener('click', (e) => {
    const btn = e.target.closest('.etb');
    if (!btn) return;
    const cmd = btn.dataset.cmd;
    const val = btn.dataset.val || null;
    els['edit-sheet'].focus();
    document.execCommand(cmd, false, val);
});

// ---- PDF editor ----
function b64ToArrayBuffer(b64) {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return bytes.buffer;
}

function arrayBufferToB64(buf) {
    const bytes = new Uint8Array(buf);
    let bin = '';
    const chunk = 0x8000;
    for (let i = 0; i < bytes.length; i += chunk) {
        bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
    }
    return btoa(bin);
}

async function openPdfEditor(path) {
    pdfState.path = path;
    pdfState.currentPage = 1;
    els['pdfedit-title'].textContent = 'Edit PDF';
    els['pdfedit-sub'].textContent = path.split(/[\\/]/).pop();
    showStage('stage-pdfedit');
    els['pdfedit-overlay'].innerHTML = '';
    try {
        const b64 = await ReadBinaryFile(path);
        pdfState.base64 = b64;
        pdfState.fileBytes = b64ToArrayBuffer(b64);
        pdfState.editor = new PdfEditor();
        // Server-render every page to a PNG (full fidelity) via PyMuPDF,
        // served over loopback HTTP — no PDF.js worker needed.
        pdfState.images = await RenderPdfToImages(path, 900);
        const sizes = await pdfState.editor.getPageSizes(pdfState.fileBytes);
        const pages = pdfState.editor.init(pdfState.fileBytes, sizes);
        pdfState.naturalWidth = pdfState.images.length ? 900 : 0;
        els['pdfedit-page'].textContent = `1 / ${pages}`;
        await renderPdfPage();
    } catch (err) {
        alert('Could not open PDF: ' + String(err));
        showStage('stage-done');
    }
}

async function renderPdfPage() {
    if (!pdfState.editor) return;
    const pageNum = pdfState.currentPage;
    const img = els['pdfedit-pageimg'];
    const url = pdfState.images[pageNum - 1];
    if (!url) return;
    if (img.src !== url) {
        img.src = url;
        await new Promise(res => { img.onload = res; });
    }
    refitPdfPage();
}

// Recomputes the page image's on-screen size (and overlay alignment) against
// the CURRENT canvas width — called after the initial page load and again on
// window resize/fullscreen toggle, since .pdfedit-canvaswrap grows/shrinks
// with the window (see style.css vw/vh sizing).
function refitPdfPage() {
    const img = els['pdfedit-pageimg'];
    if (!pdfState.editor || !img.naturalWidth) return;
    const page = pdfState.editor.pages[pdfState.currentPage - 1];
    if (!page) return;
    const avail = els['pdfedit-canvaswrap'].clientWidth - 20; // inner breathing room
    const scale = Math.min(avail / img.naturalWidth, 1);
    pdfState.scale = scale;
    // PDF points -> displayed px factor, for overlay placement/export.
    pdfState.editor.ptFactor = page.widthPt / img.naturalWidth;
    img.style.width = `${img.naturalWidth * scale}px`;
    img.style.height = `${img.naturalHeight * scale}px`;
    els['pdfedit-pageitem'].style.width = `${img.naturalWidth * scale}px`;
    renderOverlays(pdfState.currentPage);
}

window.addEventListener('resize', () => {
    if (!els['stage-pdfedit'].classList.contains('hidden')) refitPdfPage();
});

function renderOverlays(pageNum) {
    const scale = pdfState.scale || 1;
    const ptFactor = pdfState.editor.ptFactor || 1; // points per natural px
    const wrap = els['pdfedit-overlay'];
    wrap.innerHTML = '';
    const img = els['pdfedit-pageimg'];
    wrap.style.width  = `${img.naturalWidth * scale}px`;
    wrap.style.height = `${img.naturalHeight * scale}px`;
    const toDisplay = (pt) => pt / ptFactor; // points -> natural px; *scale -> css px
    const overlays = pdfState.editor.getOverlaysForPage(pageNum);
    overlays.forEach(o => {
        const div = document.createElement('div');
        div.className = 'pdfoverlay ' + o.type;
        div.style.left   = `${toDisplay(o.x) * scale}px`;
        div.style.top    = `${toDisplay(o.y) * scale}px`;
        div.style.width  = `${toDisplay(o.w) * scale}px`;
        div.style.height = `${toDisplay(o.h || 26) * scale}px`;
        if (o.type === 'text') {
            div.contentEditable = 'true';
            div.textContent = o.content;
            div.style.fontSize   = `${toDisplay(o.style.fontSize || 14) * scale}px`;
            div.style.fontWeight = o.style.bold ? '700' : '';
            div.style.fontStyle  = o.style.italic ? 'italic' : '';
            div.addEventListener('input', (e) => {
                pdfState.editor.updateOverlay(o.id, { content: e.target.textContent });
            });
            div.addEventListener('click', (e) => e.stopPropagation());
            div.addEventListener('contextmenu', (e) => {
                e.preventDefault();
                if (confirm('Remove this overlay?')) {
                    pdfState.editor.removeOverlay(o.id);
                    renderOverlays(pdfState.currentPage);
                }
            });
        }
        wrap.appendChild(div);
    });
}

els['pdf-add-text'].addEventListener('click', async () => {
    if (!pdfState.editor) return;
    const ptFactor = pdfState.editor.ptFactor || 1; // points per natural px
    const scale = pdfState.scale || 1;
    // Place at ~12pt in from the top-left, in PDF point space.
    const xPt = 12;
    const yPt = 12;
    pdfState.editor.addTextOverlay(pdfState.currentPage, xPt, yPt, 'New text', { fontSize: 14 });
    renderOverlays(pdfState.currentPage);
});

els['pdf-prev'].addEventListener('click', () => {
    if (pdfState.currentPage > 1) {
        pdfState.currentPage--;
        els['pdfedit-page'].textContent = `${pdfState.currentPage} / ${pdfState.editor.pages.length}`;
        renderPdfPage();
    }
});

els['pdf-next'].addEventListener('click', () => {
    if (pdfState.currentPage < pdfState.editor.pages.length) {
        pdfState.currentPage++;
        els['pdfedit-page'].textContent = `${pdfState.currentPage} / ${pdfState.editor.pages.length}`;
        renderPdfPage();
    }
});

els['pdfedit-back'].addEventListener('click', () => showStage('stage-done'));

els['pdfedit-save'].addEventListener('click', async () => {
    if (!pdfState.editor) { alert('No PDF loaded'); return; }
    els['pdfedit-save'].textContent = 'Exporting…';
    els['pdfedit-save'].disabled = true;
    try {
        const bytes = await pdfState.editor.exportPdf();
        const b64 = arrayBufferToB64(bytes);
        await WriteBinaryFile(pdfState.path, b64);
        els['pdfedit-save'].textContent = 'Saved ✓';
        setTimeout(() => {
            els['pdfedit-save'].textContent = 'Save & export';
            els['pdfedit-save'].disabled = false;
        }, 1600);
    } catch (err) {
        alert('Could not save PDF: ' + String(err));
        els['pdfedit-save'].textContent = 'Save & export';
        els['pdfedit-save'].disabled = false;
    }
});

// ---- DOCX WYSIWYG canvas editor ----
// The old rich-text editor (openRichDocEditor) round-trips through mammoth,
// which flattens everything into a plain flowing document — it can't show
// the same faithful, absolutely-positioned layout the DOCX file itself now
// has (see docx-editor.js layoutToDocxBytes). This editor instead renders
// the SAME geometry data used to generate the DOCX — real page size, real
// frame positions from libmspub via ExtractPubLayout — as a page-shaped
// canvas of editable, absolutely-positioned text boxes and images. What you
// see while editing is what actually comes out.
const docxCanvasState = { path: null, layout: null, pageIdx: 0 };

async function openDocxCanvasEditor(pubPath, docxPath) {
    docxCanvasState.path = docxPath;
    docxCanvasState.pageIdx = 0;
    els['docxedit-sub'].textContent = docxPath.split(/[\\/]/).pop();
    showStage('stage-docxedit');
    els['docxedit-pagescroll'].innerHTML = '<p style="padding:20px">Loading…</p>';
    try {
        const layoutJson = await ExtractPubLayout(pubPath);
        docxCanvasState.layout = JSON.parse(layoutJson);
        if (!docxCanvasState.layout.pages?.length) throw new Error('no pages extracted');
        renderDocxCanvasPage();
    } catch (err) {
        // No real frame data available (libmspub missing on this machine, or
        // this specific file timed out) — fall back to the older flowing
        // editor rather than leaving the user with a dead screen.
        openRichDocEditor(docxPath, 'docx');
    }
}

function renderDocxCanvasPage() {
    const layout = docxCanvasState.layout;
    const page = layout.pages[docxCanvasState.pageIdx];
    els['docxedit-page'].textContent = `${docxCanvasState.pageIdx + 1} / ${layout.pages.length}`;
    const multi = layout.pages.length > 1;
    els['docxedit-prev'].classList.toggle('hidden', !multi);
    els['docxedit-next'].classList.toggle('hidden', !multi);
    els['docxedit-prev'].disabled = docxCanvasState.pageIdx === 0;
    els['docxedit-next'].disabled = docxCanvasState.pageIdx >= layout.pages.length - 1;

    const wrap = els['docxedit-canvaswrap'];
    const availWidth = Math.max(wrap.clientWidth - 20, 200);
    const scale = Math.min(availWidth / page.widthPt, 1.2);

    const outer = document.createElement('div');
    outer.className = 'docxcanvas-outer';
    outer.style.width = `${page.widthPt * scale}px`;
    outer.style.height = `${page.heightPt * scale}px`;

    const content = document.createElement('div');
    content.className = 'docxcanvas-page';
    content.style.width = `${page.widthPt}px`;
    content.style.height = `${page.heightPt}px`;
    content.style.transform = `scale(${scale})`;

    page.items.forEach((item, idx) => {
        if (item.type === 'image') {
            const img = document.createElement('img');
            img.className = 'docxcanvas-image';
            img.src = item.href;
            img.style.left = `${item.x}px`;
            img.style.top = `${item.y}px`;
            img.style.width = `${item.w}px`;
            img.style.height = `${item.h}px`;
            content.appendChild(img);
        } else if (item.type === 'text') {
            const div = document.createElement('div');
            div.className = 'docxcanvas-frame';
            div.contentEditable = 'true';
            div.spellcheck = false;
            div.dataset.itemIdx = String(idx);
            div.style.left = `${item.x}px`;
            div.style.top = `${item.y}px`;
            div.style.width = `${item.w}px`;
            div.style.height = `${item.h}px`;
            (item.paragraphs.length ? item.paragraphs : [{ align: 'left', runs: [{ text: '' }] }]).forEach(p => {
                const pEl = document.createElement('p');
                const style = p.runs[0] || {};
                pEl.style.textAlign = p.align || 'left';
                pEl.style.fontFamily = style.fontFamily || 'Arial';
                pEl.style.fontSize = `${style.sizePt || 10}pt`;
                pEl.style.color = style.color || '#000000';
                pEl.style.fontWeight = style.bold ? 'bold' : 'normal';
                pEl.style.fontStyle = style.italic ? 'italic' : 'normal';
                pEl.textContent = p.runs.map(r => r.text).join('');
                div.appendChild(pEl);
            });
            content.appendChild(div);
        }
    });

    outer.appendChild(content);
    wrap.innerHTML = '';
    wrap.appendChild(outer);
}

// Reads edits back out of the DOM into the layout model. Each paragraph's
// ORIGINAL style (font/size/color/bold/italic/align) is kept and just gets
// its text replaced — this editor changes what the text says, not how a
// given paragraph looks. Extra lines a user types reuse the frame's last
// known paragraph style as a reasonable default.
function syncDocxCanvasEdits() {
    const page = docxCanvasState.layout.pages[docxCanvasState.pageIdx];
    els['docxedit-pagescroll'].querySelectorAll('.docxcanvas-frame').forEach(div => {
        const item = page.items[+div.dataset.itemIdx];
        if (!item) return;
        const origParagraphs = item.paragraphs.length ? item.paragraphs : [{ align: 'left', runs: [{ fontFamily: 'Arial', sizePt: 10, color: '#000000' }] }];
        const lines = Array.from(div.children);
        item.paragraphs = lines.map((pEl, i) => {
            const text = pEl.textContent;
            const orig = origParagraphs[i] || origParagraphs[origParagraphs.length - 1];
            const style = orig.runs[0] || {};
            return {
                align: orig.align || 'left',
                lineHeightPct: orig.lineHeightPct,
                runs: text ? [{
                    text,
                    fontFamily: style.fontFamily || 'Arial',
                    sizePt: style.sizePt || 10,
                    bold: !!style.bold,
                    italic: !!style.italic,
                    color: style.color || '#000000',
                }] : [],
            };
        }).filter(p => p.runs.length);
    });
}

els['docxedit-prev'].addEventListener('click', () => {
    if (docxCanvasState.pageIdx > 0) {
        syncDocxCanvasEdits();
        docxCanvasState.pageIdx--;
        renderDocxCanvasPage();
    }
});
els['docxedit-next'].addEventListener('click', () => {
    if (docxCanvasState.pageIdx < docxCanvasState.layout.pages.length - 1) {
        syncDocxCanvasEdits();
        docxCanvasState.pageIdx++;
        renderDocxCanvasPage();
    }
});
els['docxedit-back'].addEventListener('click', () => showStage('stage-done'));
window.addEventListener('resize', () => {
    if (!els['stage-docxedit'].classList.contains('hidden') && docxCanvasState.layout) renderDocxCanvasPage();
});

els['docxedit-save'].addEventListener('click', async () => {
    els['docxedit-save'].textContent = 'Saving…';
    els['docxedit-save'].disabled = true;
    try {
        syncDocxCanvasEdits();
        const bytes = await layoutToDocxBytes(docxCanvasState.layout);
        await WriteBinaryFile(docxCanvasState.path, arrayBufferToB64(bytes));
        els['docxedit-save'].textContent = 'Saved ✓';
    } catch (err) {
        alert('Could not save: ' + String(err));
        els['docxedit-save'].textContent = 'Save & export';
    } finally {
        els['docxedit-save'].disabled = false;
        setTimeout(() => { els['docxedit-save'].textContent = 'Save & export'; }, 1600);
    }
});

let engineSetupRunning = false;

async function checkStatus() {
    if (engineSetupRunning) return;
    const st = await CheckStatus();
    // Both are required: LibreOffice reads .pub, Python (pdf2docx/PyMuPDF) renders pages + DOCX.
    const ok = st && st['soffice'] && st['pdf2docx'] && st['tools'];
    els['status-dot'].className = 'dot ' + (ok ? 'ok' : 'off');
    els['status-text'].textContent = ok ? 'Engine ready'
        : 'Engine not set up' + (st && st['soffice'] ? ' (missing Python libraries)' : '');
    els['status-setup'].hidden = !!ok;
}

EventsOn('engine:progress', msg => { els['status-text'].textContent = msg; });

els['status-setup'].addEventListener('click', async () => {
    engineSetupRunning = true;
    els['status-setup'].hidden = true;
    els['status-dot'].className = 'dot off';
    els['status-text'].textContent = 'Setting up engine…';
    try {
        await InstallEngine();
    } catch (err) {
        alert('Engine setup failed:\n' + String(err));
    } finally {
        engineSetupRunning = false;
        checkStatus();
    }
});

// Frameless window controls
els['btn-fullscreen'].addEventListener('click', async () => {
    const isFs = await ToggleFullscreen();
    els['win'].classList.toggle('is-fullscreen', isFs);
    els['btn-fullscreen'].textContent = isFs ? '⤡' : '⛶';
    els['btn-fullscreen'].title = isFs ? 'Exit fullscreen' : 'Toggle fullscreen';
    // Re-fit whichever page image is currently shown to the new canvas size.
    window.dispatchEvent(new Event('resize'));
});
els['btn-min'].addEventListener('click', () => WindowMinimise());
els['btn-close'].addEventListener('click', () => Quit());

// drag & drop (Wails native — resolves absolute paths incl. multiple files)
OnFileDrop((x, y, paths) => {
    const pubs = (paths || []).filter(p => p && p.toLowerCase().endsWith('.pub'));
    if (pubs.length) addFiles(pubs);
    else if (paths && paths.length) alert('Bondi Press converts .pub files only.');
}, false);
els['dropzone'].addEventListener('click', pickFiles);
els['add-more'].addEventListener('click', pickFiles);
els['back'].addEventListener('click', () => showStage('stage-source'));
els['convert-btn'].addEventListener('click', openBatchPreview);
els['edit-back'].addEventListener('click', () => { CleanupPreview(); showStage('stage-done'); });
els['edit-preview'].addEventListener('click', async () => {
    // Full-screen preview of the current edited HTML, with on-disk assets.
    els['preview-title'].textContent = editState.path.split(/[\\/]/).pop();
    els['preview-print'].classList.remove('hidden');
    els['preview'].hidden = false;
    const iframe = els['preview-sheet'];
    try {
        const url = await PreparePreview(editState.path, els['edit-code'].value);
        iframe.src = url;
    } catch (err) {
        const doc = iframe.contentDocument || iframe.contentWindow.document;
        doc.open(); doc.write(els['edit-code'].value); doc.close();
    }
});
// Read-only preview for formats we don't edit in-app (PNG/JPG rasters).
async function openImagePreview(path) {
    const ext = (path.split('.').pop() || '').toLowerCase();
    els['preview-title'].textContent = path.split(/[\\/]/).pop();
    els['preview-print'].classList.add('hidden');
    els['preview'].hidden = false;
    try {
        const b64 = await ReadBinaryFile(path);
        const mime = ext === 'jpg' ? 'image/jpeg' : 'image/png';
        els['preview-sheet'].src = `data:${mime};base64,${b64}`;
    } catch (err) {
        alert('Could not open file for preview: ' + String(err));
        els['preview'].hidden = true;
    }
}

els['preview-close'].addEventListener('click', () => {
    els['preview'].hidden = true;
    els['preview-sheet'].removeAttribute('src');
});
els['preview-print'].addEventListener('click', () => {
    const f = els['preview-sheet'];
    if (f && f.contentWindow) f.contentWindow.focus();
    if (f && f.contentWindow.print) f.contentWindow.print();
});
els['edit-save'].addEventListener('click', saveEditor);
els['convert-another'].addEventListener('click', () => {
    state.pubPaths = [];
    showStage('stage-source');
});

renderFormats();
showStage('stage-source');
checkStatus();
setInterval(checkStatus, 5000);