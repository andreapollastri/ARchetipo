(function () {
    'use strict';

    // DOM element ids are kept with their original "story-" naming for HTML
    // and CSS stability. The data model exposed by the API is "spec", which
    // is reflected in variable names, payloads and envelope keys.
    const boardEl = document.getElementById('board');
    const refreshBtn = document.getElementById('refresh-btn');
    const modal = document.getElementById('modal-root');
    const modalClose = document.getElementById('modal-close');
    const modalTitle = document.getElementById('story-editor-title');
    const tabs = modal.querySelectorAll('.tab');
    const panels = modal.querySelectorAll('.tab-panel');
    const specForm = document.getElementById('story-form');
    const planForm = document.getElementById('plan-form');
    const specStatus = document.getElementById('story-status');
    const planStatus = document.getElementById('plan-status');
    const specView = document.getElementById('story-view');
    const specViewMeta = document.getElementById('story-view-meta');
    const specViewTitle = document.getElementById('story-view-title');
    const specBodyView = document.getElementById('story-body-view');
    const specEditBtn = document.getElementById('story-edit-btn');
    const specCancelBtn = document.getElementById('story-cancel-btn');
    const planView = document.getElementById('plan-view');
    const planBodyView = document.getElementById('plan-body-view');
    const planTasksView = document.getElementById('plan-tasks-view');
    const planEditBtn = document.getElementById('plan-edit-btn');
    const planCancelBtn = document.getElementById('plan-cancel-btn');
    const tasksTbody = document.getElementById('tasks-tbody');
    const addTaskBtn = document.getElementById('add-task-btn');
    const toast = document.getElementById('toast');
    const prdBtn = document.getElementById('prd-btn');
    const prdModal = document.getElementById('prd-modal');
    const prdModalClose = document.getElementById('prd-modal-close');
    const prdView = document.getElementById('prd-view');
    const prdBodyView = document.getElementById('prd-body-view');
    const prdEditBtn = document.getElementById('prd-edit-btn');
    const prdCancelBtn = document.getElementById('prd-cancel-btn');
    const prdForm = document.getElementById('prd-form');
    const prdStatus = document.getElementById('prd-status');
    const mockupsBtn = document.getElementById('mockups-btn');
    const mockupsMenu = document.getElementById('mockups-menu');
    const mockupsDropdown = document.getElementById('mockups-dropdown');
    const themeToggle = document.getElementById('theme-toggle');
    const statTotal = document.getElementById('stat-total');
    const statProgress = document.getElementById('stat-progress');
    const statDone = document.getElementById('stat-done');
    const reviewTab = document.getElementById('review-tab');
    const reviewBranch = document.getElementById('review-branch');
    const reviewDiff = document.getElementById('review-diff');
    const reviewStatus = document.getElementById('review-status');
    const reviewRequestBtn = document.getElementById('review-request-btn');
    const reviewIntegrateBtn = document.getElementById('review-integrate-btn');

    const THEME_KEY = 'archetipo.theme';

    function setTheme(theme, persist) {
        const next = theme === 'light' ? 'light' : 'dark';
        document.documentElement.dataset.theme = next;
        themeToggle.setAttribute('aria-label', next === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
        if (persist) {
            try { localStorage.setItem(THEME_KEY, next); } catch (_) { /* ignore */ }
        }
    }

    function toggleTheme() {
        const current = document.documentElement.dataset.theme === 'light' ? 'light' : 'dark';
        setTheme(current === 'dark' ? 'light' : 'dark', true);
    }

    setTheme(document.documentElement.dataset.theme, false);
    themeToggle.addEventListener('click', toggleTheme);

    const editorToolbar = [
        'bold', 'italic', 'heading', '|',
        'unordered-list', 'ordered-list', 'quote', 'code', '|',
        'link', 'image', '|',
        'preview', 'side-by-side', 'fullscreen', '|',
        'guide',
    ];
    const specEditor = new EasyMDE({
        element: specForm.body,
        spellChecker: false,
        status: false,
        autoDownloadFontAwesome: true,
        previewRender: (plainText) => marked.parse(plainText),
        toolbar: editorToolbar,
        minHeight: '320px',
    });
    const planEditor = new EasyMDE({
        element: planForm.plan_body,
        spellChecker: false,
        status: false,
        autoDownloadFontAwesome: true,
        previewRender: (plainText) => marked.parse(plainText),
        toolbar: editorToolbar,
        minHeight: '240px',
    });
    const prdEditor = new EasyMDE({
        element: prdForm.prd_body,
        spellChecker: false,
        status: false,
        autoDownloadFontAwesome: true,
        previewRender: (plainText) => marked.parse(plainText),
        toolbar: editorToolbar,
        minHeight: '360px',
    });

    let currentSpecCode = null;
    let reviewComments = []; // inline comments for the spec currently under review
    let reviewLoaded = false; // whether the review tab has been loaded for this spec
    let currentSpecSnapshot = null; // last loaded spec (for cancel + re-render after save)
    let currentPlanSnapshot = null; // last loaded plan (for cancel + re-render after save)
    let boardSnapshot = null; // last loaded board (for undo on failed drag)
    let currentPrdSnapshot = ''; // last loaded PRD body
    let mockupsCache = []; // cached list of mockups (refreshed lazily)

    refreshBtn.addEventListener('click', loadBoard);
    modalClose.addEventListener('click', closeModal);
    modal.addEventListener('click', (e) => {
        if (e.target === modal) closeModal();
    });
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && !modal.classList.contains('hidden')) closeModal();
    });
    tabs.forEach((t) => t.addEventListener('click', () => activateTab(t.dataset.tab)));
    specForm.addEventListener('submit', onSaveSpec);
    planForm.addEventListener('submit', onSavePlan);
    specEditBtn.addEventListener('click', () => enterSpecEditMode());
    specCancelBtn.addEventListener('click', () => exitSpecEditMode());
    planEditBtn.addEventListener('click', () => enterPlanEditMode());
    planCancelBtn.addEventListener('click', () => exitPlanEditMode());
    addTaskBtn.addEventListener('click', () => addTaskRow());
    reviewRequestBtn.addEventListener('click', onRequestChanges);
    reviewIntegrateBtn.addEventListener('click', onIntegrate);

    prdBtn.addEventListener('click', openPRD);
    prdModalClose.addEventListener('click', closePRD);
    prdModal.addEventListener('click', (e) => { if (e.target === prdModal) closePRD(); });
    prdEditBtn.addEventListener('click', enterPrdEditMode);
    prdCancelBtn.addEventListener('click', exitPrdEditMode);
    prdForm.addEventListener('submit', onSavePRD);
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && !prdModal.classList.contains('hidden')) closePRD();
    });

    mockupsBtn.addEventListener('click', toggleMockupsMenu);
    document.addEventListener('click', (e) => {
        if (!mockupsDropdown.contains(e.target)) mockupsMenu.classList.add('hidden');
    });

    // Global single-key shortcuts (ignored while typing in inputs / editors).
    document.addEventListener('keydown', (e) => {
        if (e.metaKey || e.ctrlKey || e.altKey) return;
        const tag = (e.target && e.target.tagName) || '';
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
        if (e.target && e.target.isContentEditable) return;
        const k = e.key.toLowerCase();
        if (k === 't') { e.preventDefault(); toggleTheme(); }
        else if (k === 'r') { e.preventDefault(); loadBoard(); }
    });

    loadBoard();
    loadMockups();
    connectBoardStream();

    let boardReloadTimer = null;
    function scheduleBoardReload() {
        clearTimeout(boardReloadTimer);
        boardReloadTimer = setTimeout(() => {
            // Skip while a modal is open: reloading would discard the user's
            // in-progress edits. The next event after the modal closes will
            // bring the board back in sync.
            if (!modal.classList.contains('hidden')) return;
            if (!prdModal.classList.contains('hidden')) return;
            loadBoard();
        }, 150);
    }

    function connectBoardStream() {
        if (typeof EventSource === 'undefined') return;
        const es = new EventSource('/api/board/stream');
        es.addEventListener('board_changed', scheduleBoardReload);
        // EventSource reconnects automatically on transient errors; no log to avoid noise.
    }

    async function loadBoard() {
        boardEl.innerHTML = '<div class="empty-board">Loading…</div>';
        try {
            const view = await apiGet('/api/board');
            renderBoard(view);
            updateStats(view);
            boardSnapshot = view;
        } catch (err) {
            boardEl.innerHTML = `<div class="empty-board">Error: ${escapeHtml(err.message || err)}</div>`;
        }
    }

    function updateStats(view) {
        const cols = view.columns || [];
        let total = 0, progress = 0, done = 0;
        cols.forEach((c) => {
            const n = (c.specs || []).length;
            total += n;
            if (c.id === 'in_progress' || c.id === 'review') progress += n;
            if (c.id === 'done') done += n;
        });
        if (statTotal) statTotal.textContent = total;
        if (statProgress) statProgress.textContent = progress;
        if (statDone) statDone.textContent = done;
    }

    function renderBoard(view) {
        boardEl.innerHTML = '';
        if (!view.columns || view.columns.length === 0) {
            boardEl.innerHTML = '<div class="empty-board">No backlog yet — run <code>archetipo init</code> to begin.</div>';
            return;
        }
        view.columns.forEach((col) => {
            const columnEl = document.createElement('section');
            columnEl.className = 'column';
            columnEl.dataset.id = col.id;
            columnEl.dataset.status = col.status;

            const header = document.createElement('header');
            header.className = 'column-header';
            const count = (col.specs || []).length;
            header.innerHTML = `
                <span class="column-title"><span class="column-dot"></span>${escapeHtml(col.title || col.id)}</span>
                <span class="column-count">${count}</span>
            `;
            columnEl.appendChild(header);

            const body = document.createElement('div');
            body.className = 'column-body';
            body.dataset.columnId = col.id;
            (col.specs || []).forEach((s) => body.appendChild(renderCard(s)));
            if (!col.specs || col.specs.length === 0) {
                body.appendChild(emptyHint());
            }
            columnEl.appendChild(body);
            boardEl.appendChild(columnEl);

            Sortable.create(body, {
                group: 'kanban',
                animation: 140,
                ghostClass: 'sortable-ghost',
                dragClass: 'sortable-drag',
                onAdd: onDragMove,
                onUpdate: onDragMove,
            });
        });
    }

    function renderCard(spec) {
        const el = document.createElement('article');
        el.className = 'card';
        if (spec.priority) el.classList.add('prio-' + spec.priority);
        el.dataset.code = spec.code;
        const epicCode = spec.epic && spec.epic.code ? spec.epic.code : '';
        const epicTooltip = spec.epic && spec.epic.title ? `${epicCode} — ${spec.epic.title}` : epicCode;
        el.innerHTML = `
            <div class="card-top">
                <span class="card-code">${escapeHtml(spec.code)}</span>
                ${spec.priority ? `<span class="priority-badge priority-${escapeHtml(spec.priority)}">${escapeHtml(spec.priority)}</span>` : ''}
            </div>
            <div class="card-title">${escapeHtml(spec.title || '(untitled)')}</div>
            <div class="card-meta">
                <span class="card-epic" title="${escapeHtml(epicTooltip)}">${escapeHtml(epicCode)}</span>
                <span class="card-points">${Number.isFinite(spec.points) ? spec.points + ' pt' : ''}</span>
            </div>
            ${spec.branch ? `<div class="card-branch" title="git branch">⎇ ${escapeHtml(spec.branch)}</div>` : ''}
        `;
        el.addEventListener('click', () => openEditor(spec.code));
        return el;
    }

    function emptyHint() {
        const e = document.createElement('div');
        e.className = 'empty-column';
        e.textContent = 'drop a card here';
        return e;
    }

    async function onDragMove(evt) {
        const code = evt.item.dataset.code;
        const targetColumn = evt.to.dataset.columnId;
        // Determine anchor based on the card now next to the dragged item.
        let anchor = {};
        const cards = Array.from(evt.to.querySelectorAll('.card'));
        const idx = cards.findIndex((c) => c === evt.item);
        if (idx === -1) {
            anchor = {};
        } else if (idx < cards.length - 1) {
            anchor = { before: cards[idx + 1].dataset.code };
        } else if (idx > 0) {
            anchor = { after: cards[idx - 1].dataset.code };
        }
        try {
            await apiPost('/api/board/move', { code, to: targetColumn, ...anchor });
            showToast(`${code} moved to ${targetColumn}`, 'ok');
            await loadBoard();
        } catch (err) {
            showToast(`Move failed: ${err.message || err}`, 'err');
            // revert the optimistic DOM change by reloading the last known good board.
            if (boardSnapshot) renderBoard(boardSnapshot);
        }
    }

    async function openEditor(code) {
        currentSpecCode = code;
        modalTitle.textContent = `Spec ${code}`;
        modal.classList.remove('hidden');
        activateTab('story');
        specStatus.textContent = 'Loading...';
        planStatus.textContent = '';
        showSpecView();
        showPlanView();
        reviewLoaded = false;
        reviewComments = [];
        try {
            const detail = await apiGet(`/api/spec/${encodeURIComponent(code)}`);
            currentSpecSnapshot = detail.spec || {};
            currentPlanSnapshot = { plan_body: detail.plan_body || '', tasks: detail.tasks || [] };
            fillSpecView(currentSpecSnapshot);
            fillSpecForm(currentSpecSnapshot);
            fillPlanView(currentPlanSnapshot.plan_body, currentPlanSnapshot.tasks);
            fillPlanForm(currentPlanSnapshot.plan_body, currentPlanSnapshot.tasks);
            updateReviewTabVisibility(currentSpecSnapshot);
            specStatus.textContent = '';
        } catch (err) {
            specStatus.textContent = `Load failed: ${err.message || err}`;
            specStatus.className = 'status-msg err';
        }
    }

    function fillSpecView(s) {
        specViewTitle.textContent = s.title || '(untitled)';
        const metaParts = [];
        if (s.priority) metaParts.push(`<span class="priority-badge priority-${escapeHtml(s.priority)}">${escapeHtml(s.priority)}</span>`);
        if (Number.isFinite(s.points) && s.points > 0) metaParts.push(`<span class="meta-chip">${s.points} pt</span>`);
        if (s.epic && s.epic.code) {
            const epicText = s.epic.title ? `${s.epic.code} — ${s.epic.title}` : s.epic.code;
            metaParts.push(`<span class="meta-chip">${escapeHtml(epicText)}</span>`);
        }
        if (s.scope) metaParts.push(`<span class="meta-chip">${escapeHtml(s.scope)}</span>`);
        if (s.blocked_by && s.blocked_by.length) metaParts.push(`<span class="meta-chip blocked">blocked by ${escapeHtml(s.blocked_by.join(', '))}</span>`);
        const mockup = findMockupForSpec(s.code);
        if (mockup) metaParts.push(`<a class="meta-chip mockup-link" href="${escapeHtml(mockup.url)}" target="_blank" rel="noopener">↗ mockup</a>`);
        specViewMeta.innerHTML = metaParts.join('');
        specBodyView.innerHTML = marked.parse(s.body || '*(no description)*');
    }

    function findMockupForSpec(code) {
        if (!code) return null;
        return mockupsCache.find((m) => m.spec_code === code) || null;
    }

    function fillSpecForm(s) {
        specForm.title.value = s.title || '';
        specForm.priority.value = s.priority || 'MEDIUM';
        specForm.story_points.value = s.points || 0;
        specForm.scope.value = s.scope || '';
        specForm.blocked_by.value = (s.blocked_by || []).join(', ');
        specEditor.value(s.body || '');
    }

    function fillPlanForm(body, tasks) {
        planEditor.value(body || '');
        tasksTbody.innerHTML = '';
        (tasks || []).forEach((t) => addTaskRow(t));
    }

    function showSpecView() {
        specView.classList.remove('hidden');
        specForm.classList.add('hidden');
    }

    function enterSpecEditMode() {
        specView.classList.add('hidden');
        specForm.classList.remove('hidden');
        specStatus.textContent = '';
        specStatus.className = 'status-msg';
        // CodeMirror needs a refresh after being unhidden, otherwise it measures 0 height.
        setTimeout(() => specEditor.codemirror.refresh(), 0);
    }

    function exitSpecEditMode() {
        if (currentSpecSnapshot) fillSpecForm(currentSpecSnapshot);
        showSpecView();
    }

    function fillPlanView(body, tasks) {
        planBodyView.innerHTML = marked.parse(body || '*(no plan)*');
        planTasksView.innerHTML = '';
        (tasks || []).forEach((t) => {
            const tr = document.createElement('tr');
            tr.innerHTML = `
                <td>${escapeHtml(t.id || '')}</td>
                <td>${escapeHtml(t.title || '')}</td>
                <td>${escapeHtml(t.type || '')}</td>
                <td>${escapeHtml(t.status || '')}</td>
                <td>${escapeHtml((t.dependencies || []).join(', '))}</td>
            `;
            planTasksView.appendChild(tr);
        });
        if (!tasks || tasks.length === 0) {
            const tr = document.createElement('tr');
            tr.innerHTML = '<td colspan="5" class="empty-cell">No tasks</td>';
            planTasksView.appendChild(tr);
        }
    }

    function showPlanView() {
        planView.classList.remove('hidden');
        planForm.classList.add('hidden');
    }

    function enterPlanEditMode() {
        planView.classList.add('hidden');
        planForm.classList.remove('hidden');
        planStatus.textContent = '';
        planStatus.className = 'status-msg';
        setTimeout(() => planEditor.codemirror.refresh(), 0);
    }

    function exitPlanEditMode() {
        if (currentPlanSnapshot) fillPlanForm(currentPlanSnapshot.plan_body, currentPlanSnapshot.tasks);
        showPlanView();
    }

    function addTaskRow(task) {
        const t = task || { id: nextTaskID(), title: '', type: 'Impl', status: 'TODO', dependencies: [] };
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td><input type="text" class="task-id" value="${escapeHtml(t.id || '')}" /></td>
            <td><input type="text" class="task-title" value="${escapeHtml(t.title || '')}" /></td>
            <td>
                <select class="task-type">
                    <option value="Impl">Impl</option>
                    <option value="Test">Test</option>
                </select>
            </td>
            <td>
                <select class="task-status">
                    <option>TODO</option>
                    <option>PLANNED</option>
                    <option>IN PROGRESS</option>
                    <option>REVIEW</option>
                    <option>DONE</option>
                </select>
            </td>
            <td><input type="text" class="task-deps" value="${escapeHtml((t.dependencies || []).join(', '))}" placeholder="TASK-01" /></td>
            <td><button type="button" class="remove-task" aria-label="Remove">&times;</button></td>
        `;
        tr.querySelector('.task-type').value = t.type || 'Impl';
        tr.querySelector('.task-status').value = t.status || 'TODO';
        tr.querySelector('.remove-task').addEventListener('click', () => tr.remove());
        tasksTbody.appendChild(tr);
    }

    function nextTaskID() {
        const ids = Array.from(tasksTbody.querySelectorAll('.task-id'))
            .map((i) => parseInt((i.value.match(/(\d+)$/) || [0, 0])[1], 10))
            .filter((n) => Number.isFinite(n));
        const next = (ids.length ? Math.max(...ids) : 0) + 1;
        return 'TASK-' + String(next).padStart(2, '0');
    }

    async function onSaveSpec(e) {
        e.preventDefault();
        if (!currentSpecCode) return;
        const blocked = specForm.blocked_by.value
            .split(',')
            .map((s) => s.trim())
            .filter(Boolean);
        const patch = {
            title: specForm.title.value,
            priority: specForm.priority.value,
            points: parseInt(specForm.story_points.value, 10) || 0,
            scope: specForm.scope.value,
            blocked_by: blocked,
            body: specEditor.value(),
        };
        specStatus.textContent = 'Saving...';
        specStatus.className = 'status-msg';
        try {
            await apiPut(`/api/spec/${encodeURIComponent(currentSpecCode)}`, patch);
            specStatus.textContent = 'Saved';
            specStatus.className = 'status-msg ok';
            showToast(`${currentSpecCode} updated`, 'ok');
            currentSpecSnapshot = { ...(currentSpecSnapshot || {}), ...patch };
            fillSpecView(currentSpecSnapshot);
            showSpecView();
            await loadBoard();
        } catch (err) {
            specStatus.textContent = `Save failed: ${err.message || err}`;
            specStatus.className = 'status-msg err';
        }
    }

    async function onSavePlan(e) {
        e.preventDefault();
        if (!currentSpecCode) return;
        const rows = Array.from(tasksTbody.querySelectorAll('tr'));
        const tasks = rows
            .map((tr) => {
                const deps = tr.querySelector('.task-deps').value
                    .split(',').map((s) => s.trim()).filter(Boolean);
                return {
                    id: tr.querySelector('.task-id').value.trim(),
                    title: tr.querySelector('.task-title').value.trim(),
                    type: tr.querySelector('.task-type').value,
                    status: tr.querySelector('.task-status').value,
                    dependencies: deps,
                };
            })
            .filter((t) => t.id !== '');
        const payload = {
            plan_body: planEditor.value(),
            tasks,
        };
        planStatus.textContent = 'Saving...';
        planStatus.className = 'status-msg';
        try {
            await apiPut(`/api/spec/${encodeURIComponent(currentSpecCode)}/plan`, payload);
            planStatus.textContent = 'Saved';
            planStatus.className = 'status-msg ok';
            showToast(`${currentSpecCode} plan updated`, 'ok');
            currentPlanSnapshot = { plan_body: payload.plan_body, tasks: payload.tasks };
            fillPlanView(currentPlanSnapshot.plan_body, currentPlanSnapshot.tasks);
            showPlanView();
        } catch (err) {
            planStatus.textContent = `Save failed: ${err.message || err}`;
            planStatus.className = 'status-msg err';
        }
    }

    function activateTab(name) {
        tabs.forEach((t) => {
            const active = t.dataset.tab === name;
            t.classList.toggle('active', active);
            t.setAttribute('aria-selected', active ? 'true' : 'false');
        });
        panels.forEach((p) => {
            p.classList.toggle('active', p.dataset.panel === name);
        });
        // CodeMirror instances mounted inside hidden panels need a refresh once visible.
        if (name === 'plan' && !planForm.classList.contains('hidden')) {
            setTimeout(() => planEditor.codemirror.refresh(), 0);
        }
        if (name === 'story' && !specForm.classList.contains('hidden')) {
            setTimeout(() => specEditor.codemirror.refresh(), 0);
        }
        if (name === 'review' && !reviewLoaded) {
            loadReview();
        }
    }

    function closeModal() {
        modal.classList.add('hidden');
        currentSpecCode = null;
        currentSpecSnapshot = null;
        currentPlanSnapshot = null;
        reviewComments = [];
        reviewLoaded = false;
        reviewTab.classList.add('hidden');
    }

    function showToast(msg, kind) {
        toast.textContent = msg;
        toast.classList.remove('hidden', 'ok', 'err');
        if (kind) toast.classList.add(kind);
        clearTimeout(showToast._t);
        showToast._t = setTimeout(() => toast.classList.add('hidden'), 2200);
    }

    // ---- Review (diff + inline comments) -----------------------------------

    function updateReviewTabVisibility(spec) {
        const inReview = spec && spec.status === 'REVIEW';
        reviewTab.classList.toggle('hidden', !inReview);
        if (!inReview && reviewTab.classList.contains('active')) {
            activateTab('story');
        }
    }

    function commentKey(file, side, line) {
        return `${file}|${side}|${line}`;
    }

    async function loadReview() {
        reviewLoaded = true;
        reviewStatus.textContent = '';
        reviewStatus.className = 'status-msg';
        reviewDiff.innerHTML = '<div class="review-empty">Loading diff…</div>';
        try {
            const [diff, review] = await Promise.all([
                apiGet(`/api/spec/${encodeURIComponent(currentSpecCode)}/diff`),
                apiGet(`/api/spec/${encodeURIComponent(currentSpecCode)}/review`),
            ]);
            reviewComments = (review && review.comments) || [];
            renderReviewBranch(diff);
            renderDiff(diff);
        } catch (err) {
            reviewLoaded = false;
            reviewDiff.innerHTML = `<div class="review-empty">Error: ${escapeHtml(err.message || err)}</div>`;
        }
    }

    function renderReviewBranch(diff) {
        const parts = [];
        if (diff.branch) parts.push(`<span class="review-chip">⎇ ${escapeHtml(diff.branch)}</span>`);
        parts.push(`<span class="review-chip">base ${escapeHtml(diff.base || '')}</span>`);
        if (diff.branch) parts.push(`<span class="review-chip">+${diff.ahead || 0} / −${diff.behind || 0}</span>`);
        reviewBranch.innerHTML = parts.join('');
    }

    function renderDiff(diff) {
        reviewDiff.innerHTML = '';
        const files = diff.files || [];
        if (files.length === 0) {
            reviewDiff.innerHTML = '<div class="review-empty">No changes in this diff.</div>';
            return;
        }
        files.forEach((file) => reviewDiff.appendChild(renderFileDiff(file)));
        renderAllComments();
    }

    function renderFileDiff(file) {
        const path = file.new_path || file.old_path || '(unknown)';
        const wrap = document.createElement('div');
        wrap.className = 'diff-file';
        const header = document.createElement('div');
        header.className = 'diff-file-header';
        header.innerHTML = `<span class="diff-file-status diff-${escapeHtml(file.status || 'modified')}">${escapeHtml(file.status || 'modified')}</span><span class="diff-file-path">${escapeHtml(path)}</span>`;
        wrap.appendChild(header);
        (file.hunks || []).forEach((hunk) => {
            const hh = document.createElement('div');
            hh.className = 'diff-hunk-header';
            hh.textContent = hunk.header;
            wrap.appendChild(hh);
            (hunk.lines || []).forEach((line) => wrap.appendChild(renderDiffLine(path, file, line)));
        });
        return wrap;
    }

    function renderDiffLine(path, file, line) {
        const row = document.createElement('div');
        row.className = `diff-line diff-${line.kind}`;
        const side = line.new_line > 0 ? 'new' : 'old';
        const lineNo = side === 'new' ? line.new_line : line.old_line;
        const anchorFile = side === 'old' ? (file.old_path || path) : path;
        row.dataset.file = anchorFile;
        row.dataset.side = side;
        row.dataset.line = String(lineNo);
        const sign = line.kind === 'add' ? '+' : line.kind === 'del' ? '−' : ' ';
        row.innerHTML = `
            <span class="diff-gutter old">${line.old_line > 0 ? line.old_line : ''}</span>
            <span class="diff-gutter new">${line.new_line > 0 ? line.new_line : ''}</span>
            <span class="diff-comment-add" title="Add comment">+</span>
            <span class="diff-sign">${sign}</span>
            <span class="diff-code">${escapeHtml(line.text)}</span>
        `;
        row.querySelector('.diff-comment-add').addEventListener('click', (e) => {
            e.stopPropagation();
            openComposer(row, anchorFile, side, lineNo);
        });
        return row;
    }

    function renderAllComments() {
        // Remove existing comment blocks then re-attach from state.
        reviewDiff.querySelectorAll('.diff-comment-block').forEach((n) => n.remove());
        reviewComments.forEach((c) => {
            const row = findLineRow(c.file, c.side, c.line);
            if (row) insertCommentBlock(row, c);
        });
    }

    function findLineRow(file, side, line) {
        return reviewDiff.querySelector(`.diff-line[data-file="${cssEscape(file)}"][data-side="${side}"][data-line="${line}"]`);
    }

    function insertCommentBlock(row, comment) {
        const block = document.createElement('div');
        block.className = 'diff-comment-block';
        block.innerHTML = `
            <div class="diff-comment-body">${escapeHtml(comment.body)}</div>
            <button type="button" class="diff-comment-del" aria-label="Delete comment">&times;</button>
        `;
        block.querySelector('.diff-comment-del').addEventListener('click', () => deleteComment(comment));
        insertAfterTrailing(row, block);
    }

    function openComposer(row, file, side, line) {
        // Avoid duplicate composers on the same row.
        const existing = nextComposer(row);
        if (existing) { existing.querySelector('textarea').focus(); return; }
        const box = document.createElement('div');
        box.className = 'diff-comment-block diff-composer';
        box.innerHTML = `
            <textarea class="diff-comment-input" rows="3" placeholder="Leave a comment…"></textarea>
            <div class="diff-composer-actions">
                <button type="button" class="primary-btn diff-comment-save">Comment</button>
                <button type="button" class="ghost-btn diff-comment-cancel">Cancel</button>
            </div>
        `;
        box.querySelector('.diff-comment-cancel').addEventListener('click', () => box.remove());
        box.querySelector('.diff-comment-save').addEventListener('click', async () => {
            const body = box.querySelector('textarea').value.trim();
            if (!body) return;
            box.remove();
            await addComment({ file, side, line, body, created_at: new Date().toISOString() });
        });
        insertAfterTrailing(row, box);
        box.querySelector('textarea').focus();
    }

    // insertAfterTrailing inserts node after row and any comment blocks already
    // attached to it, so comments and composer stack in order under the line.
    function insertAfterTrailing(row, node) {
        let ref = row;
        while (ref.nextSibling && ref.nextSibling.classList && ref.nextSibling.classList.contains('diff-comment-block')) {
            ref = ref.nextSibling;
        }
        ref.parentNode.insertBefore(node, ref.nextSibling);
    }

    function nextComposer(row) {
        let ref = row.nextSibling;
        while (ref && ref.classList && ref.classList.contains('diff-comment-block')) {
            if (ref.classList.contains('diff-composer')) return ref;
            ref = ref.nextSibling;
        }
        return null;
    }

    async function addComment(comment) {
        reviewComments.push(comment);
        await persistReview();
        renderAllComments();
    }

    async function deleteComment(comment) {
        reviewComments = reviewComments.filter((c) => c !== comment);
        await persistReview();
        renderAllComments();
    }

    async function persistReview() {
        try {
            await apiPut(`/api/spec/${encodeURIComponent(currentSpecCode)}/review`, { comments: reviewComments });
        } catch (err) {
            showToast(`Save failed: ${err.message || err}`, 'err');
        }
    }

    async function onRequestChanges() {
        if (!currentSpecCode) return;
        if (reviewComments.length === 0) {
            showToast('Add at least one comment first', 'err');
            return;
        }
        if (!window.confirm(`Convert ${reviewComments.length} comment(s) into Fix tasks and send ${currentSpecCode} back to IN PROGRESS?`)) return;
        reviewStatus.textContent = 'Requesting changes…';
        reviewStatus.className = 'status-msg';
        try {
            const res = await apiPost(`/api/spec/${encodeURIComponent(currentSpecCode)}/request-changes`, {});
            showToast(`${currentSpecCode}: ${res.tasks_added} fix task(s) added`, 'ok');
            closeModal();
            await loadBoard();
        } catch (err) {
            reviewStatus.textContent = `Failed: ${err.message || err}`;
            reviewStatus.className = 'status-msg err';
        }
    }

    async function onIntegrate() {
        if (!currentSpecCode) return;
        if (!window.confirm(`Merge ${currentSpecCode}'s branch into base, remove its worktree and mark it DONE?`)) return;
        reviewStatus.textContent = 'Integrating…';
        reviewStatus.className = 'status-msg';
        try {
            await apiPost(`/api/spec/${encodeURIComponent(currentSpecCode)}/integrate`, {});
            showToast(`${currentSpecCode} integrated`, 'ok');
            closeModal();
            await loadBoard();
        } catch (err) {
            reviewStatus.textContent = `Failed: ${err.message || err}`;
            reviewStatus.className = 'status-msg err';
        }
    }

    // cssEscape escapes a string for use in a CSS attribute selector. Falls back
    // to a manual escape when CSS.escape is unavailable.
    function cssEscape(s) {
        if (window.CSS && CSS.escape) return CSS.escape(s);
        return String(s).replace(/["\\]/g, '\\$&');
    }

    // ---- API helpers --------------------------------------------------------

    async function apiGet(url) {
        const r = await fetch(url, { headers: { Accept: 'application/json' } });
        return parseResponse(r);
    }
    async function apiPost(url, body) {
        const r = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return parseResponse(r);
    }
    async function apiPut(url, body) {
        const r = await fetch(url, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return parseResponse(r);
    }
    async function parseResponse(r) {
        const text = await r.text();
        let data = null;
        try { data = text ? JSON.parse(text) : null; } catch (_) { data = null; }
        if (!r.ok) {
            const msg = data && data.error ? data.error : `HTTP ${r.status}`;
            throw new Error(msg);
        }
        return data;
    }

    // ---- PRD & Mockups -----------------------------------------------------

    async function openPRD() {
        prdModal.classList.remove('hidden');
        showPrdView();
        prdStatus.textContent = 'Loading...';
        prdStatus.className = 'status-msg';
        try {
            const data = await apiGet('/api/prd');
            currentPrdSnapshot = (data && data.body) || '';
            fillPrdView(currentPrdSnapshot);
            prdEditor.value(currentPrdSnapshot);
            prdStatus.textContent = '';
        } catch (err) {
            prdStatus.textContent = `Load failed: ${err.message || err}`;
            prdStatus.className = 'status-msg err';
        }
    }

    function closePRD() {
        prdModal.classList.add('hidden');
        showPrdView();
    }

    function fillPrdView(body) {
        prdBodyView.innerHTML = marked.parse(body || '*(no PRD yet)*');
    }

    function showPrdView() {
        prdView.classList.remove('hidden');
        prdForm.classList.add('hidden');
    }

    function enterPrdEditMode() {
        prdView.classList.add('hidden');
        prdForm.classList.remove('hidden');
        prdStatus.textContent = '';
        prdStatus.className = 'status-msg';
        setTimeout(() => prdEditor.codemirror.refresh(), 0);
    }

    function exitPrdEditMode() {
        prdEditor.value(currentPrdSnapshot || '');
        showPrdView();
    }

    async function onSavePRD(e) {
        e.preventDefault();
        const body = prdEditor.value();
        prdStatus.textContent = 'Saving...';
        prdStatus.className = 'status-msg';
        try {
            await apiPut('/api/prd', { body });
            currentPrdSnapshot = body;
            fillPrdView(body);
            showPrdView();
            prdStatus.textContent = 'Saved';
            prdStatus.className = 'status-msg ok';
            showToast('PRD updated', 'ok');
        } catch (err) {
            prdStatus.textContent = `Save failed: ${err.message || err}`;
            prdStatus.className = 'status-msg err';
        }
    }

    async function loadMockups() {
        try {
            const data = await apiGet('/api/mockups');
            mockupsCache = (data && data.mockups) || [];
            renderMockupsMenu();
        } catch (_) {
            mockupsCache = [];
            renderMockupsMenu();
        }
    }

    function renderMockupsMenu() {
        const appMockups = mockupsCache.filter((m) => !m.spec_code);
        const specMockups = mockupsCache.filter((m) => !!m.spec_code);
        mockupsMenu.innerHTML = '';

        const appSection = document.createElement('div');
        appSection.className = 'mockups-section';
        if (appMockups.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'dropdown-empty';
            empty.textContent = 'No app mockups';
            appSection.appendChild(empty);
        } else {
            appMockups.forEach((m) => appSection.appendChild(createMockupItem(m)));
        }
        mockupsMenu.appendChild(appSection);

        if (specMockups.length > 0) {
            mockupsMenu.appendChild(createSpecsSection(specMockups));
        }
    }

    function createMockupItem(m) {
        const a = document.createElement('a');
        a.href = m.url;
        a.target = '_blank';
        a.rel = 'noopener';
        a.className = 'dropdown-item';
        a.textContent = m.name;
        return a;
    }

    function createSpecsSection(items) {
        const section = document.createElement('div');
        section.className = 'mockups-section mockups-section-stories collapsed';

        const header = document.createElement('div');
        header.className = 'mockups-section-header clickable';
        header.setAttribute('role', 'button');
        header.setAttribute('tabindex', '0');
        header.innerHTML = `<span>Specs (${items.length})</span>`
            + '<svg class="mockups-section-caret" width="9" height="9" viewBox="0 0 9 9" aria-hidden="true">'
            + '<path d="M1.5 3l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>';

        const body = document.createElement('div');
        body.className = 'mockups-section-body hidden';
        items.forEach((m) => body.appendChild(createMockupItem(m)));

        const toggle = (e) => {
            e.stopPropagation();
            const collapsed = section.classList.toggle('collapsed');
            body.classList.toggle('hidden', collapsed);
        };
        header.addEventListener('click', toggle);
        header.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(e); }
        });

        section.appendChild(header);
        section.appendChild(body);
        return section;
    }

    function collapseSpecsSection() {
        const section = mockupsMenu.querySelector('.mockups-section-stories');
        if (!section) return;
        section.classList.add('collapsed');
        const body = section.querySelector('.mockups-section-body');
        if (body) body.classList.add('hidden');
    }

    function toggleMockupsMenu(e) {
        e.stopPropagation();
        const wasHidden = mockupsMenu.classList.contains('hidden');
        mockupsMenu.classList.toggle('hidden');
        if (wasHidden) collapseSpecsSection();
    }

    function escapeHtml(s) {
        if (s === null || s === undefined) return '';
        return String(s)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }
})();
