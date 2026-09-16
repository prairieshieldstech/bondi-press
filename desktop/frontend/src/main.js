import './style.css';

import {Convert, ConvertBatch, Formats, SelectPubFile, SelectSavePath, CheckStatus, RevealFile} from '../wailsjs/go/main/App';
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
        </main>

        <footer class="statusbar">
            <span class="dot" id="status-dot"></span>
            <span id="status-text">Checking engine…</span>
        </footer>
    </div>
`;

function el(id) { return document.getElementById(id); }
['titlebar','brand-btn','btn-min','btn-close','dropzone','filelist','file-count','add-more','file-items',
 'to-format','stage-format','format-count','format-grid','back','convert-btn','stage-progress',
 'progress-title','progress-text','prog-list','stage-done','done-count','done-path','done-list',
 'convert-another','status-dot','status-text'
].forEach(id => { els[id] = document.getElementById(id); });

function showStage(name) {
    ['stage-source','stage-format','stage-progress','stage-done'].forEach(s => {
        document.getElementById(s).classList.toggle('hidden', s !== name);
    });
}

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

async function convert() {
    if (state.pubPaths.length === 0 || state.busy) return;
    state.busy = true;
    showStage('stage-progress');
    renderProgList();

    try {
        const results = await ConvertBatch(state.pubPaths, state.format);
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
    els['prog-list'].innerHTML = state.pubPaths.map(p => `
        <div class="progitem">
            <span class="progitem-dot"></span>
            <span class="progitem-name">${p.split(/[\\/]/).pop()}</span>
        </div>
    `).join('');
}

function renderResults(results) {
    const okCount = results.filter(r => r.status === 'ok').length;
    els['done-count'].textContent = okCount === results.length
        ? `All ${okCount} converted`
        : `${okCount} of ${results.length} converted`;
    els['done-list'].innerHTML = results.map(r => {
        const path = r.status === 'ok' ? r.output : r.input;
        return `
        <div class="resultitem ${r.status}">
            <span class="resultitem-name" title="${path}">
                ${path.split(/[\\/]/).pop()}
            </span>
            <span class="resultitem-state">${r.status === 'ok' ? '✓' : r.error || 'failed'}</span>
        </div>
    `;
    }).join('');
    const outDir = results.find(r => r.status === 'ok');
    els['done-path'].textContent = outDir ? outDir.output.split(/[\\/]/).slice(0, -1).join('/') : '';
    els['done-list'].querySelectorAll('.resultitem').forEach((row) => {
        row.addEventListener('click', () => {
            const name = row.querySelector('.resultitem-name').title || '';
            if (row.classList.contains('ok')) { RevealFile(name); }
        });
    });
    showStage('stage-done');
}

async function checkStatus() {
    const st = await CheckStatus();
    const ok = st && (st['soffice'] || st['pdf2docx']);
    els['status-dot'].className = 'dot ' + (ok ? 'ok' : 'off');
    els['status-text'].textContent = ok ? 'Engine ready' : 'Engine unavailable';
}

// Frameless window controls
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
els['convert-btn'].addEventListener('click', convert);
els['convert-another'].addEventListener('click', () => {
    state.pubPaths = [];
    showStage('stage-source');
});

renderFormats();
showStage('stage-source');
checkStatus();
setInterval(checkStatus, 5000);