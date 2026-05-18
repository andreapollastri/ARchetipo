(function () {
    'use strict';

    const boardEl = document.getElementById('board');
    const refreshBtn = document.getElementById('refresh-btn');
    const modal = document.getElementById('modal-root');
    const modalClose = document.getElementById('modal-close');
    const modalTitle = document.getElementById('story-editor-title');
    const tabs = modal.querySelectorAll('.tab');
    const panels = modal.querySelectorAll('.tab-panel');
    const storyForm = document.getElementById('story-form');
    const planForm = document.getElementById('plan-form');
    const storyStatus = document.getElementById('story-status');
    const planStatus = document.getElementById('plan-status');
    const storyPreview = document.getElementById('story-preview');
    const planPreview = document.getElementById('plan-preview');
    const storyPreviewBtn = document.getElementById('story-preview-btn');
    const planPreviewBtn = document.getElementById('plan-preview-btn');
    const tasksTbody = document.getElementById('tasks-tbody');
    const addTaskBtn = document.getElementById('add-task-btn');
    const toast = document.getElementById('toast');

    let currentStoryCode = null;
    let boardSnapshot = null; // last loaded board (for undo on failed drag)

    refreshBtn.addEventListener('click', loadBoard);
    modalClose.addEventListener('click', closeModal);
    modal.addEventListener('click', (e) => {
        if (e.target === modal) closeModal();
    });
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && !modal.classList.contains('hidden')) closeModal();
    });
    tabs.forEach((t) => t.addEventListener('click', () => activateTab(t.dataset.tab)));
    storyForm.addEventListener('submit', onSaveStory);
    planForm.addEventListener('submit', onSavePlan);
    storyPreviewBtn.addEventListener('click', () => togglePreview(storyForm.body, storyPreview));
    planPreviewBtn.addEventListener('click', () => togglePreview(planForm.plan_body, planPreview));
    addTaskBtn.addEventListener('click', () => addTaskRow());

    loadBoard();

    async function loadBoard() {
        boardEl.innerHTML = '<div class="empty-board">Loading...</div>';
        try {
            const view = await apiGet('/api/board');
            renderBoard(view);
            boardSnapshot = view;
        } catch (err) {
            boardEl.innerHTML = `<div class="empty-board">Error: ${escapeHtml(err.message || err)}</div>`;
        }
    }

    function renderBoard(view) {
        boardEl.innerHTML = '';
        if (!view.columns || view.columns.length === 0) {
            boardEl.innerHTML = '<div class="empty-board">No backlog yet. Run <code>archetipo init</code> first.</div>';
            return;
        }
        view.columns.forEach((col) => {
            const columnEl = document.createElement('section');
            columnEl.className = 'column';
            columnEl.dataset.id = col.id;
            columnEl.dataset.status = col.status;

            const header = document.createElement('header');
            header.className = 'column-header';
            header.innerHTML = `
                <span class="column-title"><span class="column-dot"></span>${escapeHtml(col.title || col.id)}</span>
                <span class="column-count">${(col.stories || []).length}</span>
            `;
            columnEl.appendChild(header);

            const body = document.createElement('div');
            body.className = 'column-body';
            body.dataset.columnId = col.id;
            (col.stories || []).forEach((s) => body.appendChild(renderCard(s)));
            if (!col.stories || col.stories.length === 0) {
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

    function renderCard(story) {
        const el = document.createElement('article');
        el.className = 'card';
        el.dataset.code = story.code;
        el.innerHTML = `
            <div class="card-top">
                <span class="card-code">${escapeHtml(story.code)}</span>
                ${story.priority ? `<span class="priority-badge priority-${escapeHtml(story.priority)}">${escapeHtml(story.priority)}</span>` : ''}
            </div>
            <div class="card-title">${escapeHtml(story.title || '(untitled)')}</div>
            <div class="card-meta">
                <span>${story.epic && story.epic.code ? escapeHtml(story.epic.code) : ''}</span>
                <span class="card-points">${Number.isFinite(story.story_points) ? story.story_points + ' pt' : ''}</span>
            </div>
        `;
        el.addEventListener('click', () => openEditor(story.code));
        return el;
    }

    function emptyHint() {
        const e = document.createElement('div');
        e.className = 'empty-column';
        e.textContent = 'Drop a card here';
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
        currentStoryCode = code;
        modalTitle.textContent = `Story ${code}`;
        modal.classList.remove('hidden');
        activateTab('story');
        storyStatus.textContent = 'Loading...';
        planStatus.textContent = '';
        try {
            const detail = await apiGet(`/api/story/${encodeURIComponent(code)}`);
            fillStoryForm(detail.story);
            fillPlanForm(detail.plan_body || '', detail.tasks || []);
            storyStatus.textContent = '';
        } catch (err) {
            storyStatus.textContent = `Load failed: ${err.message || err}`;
            storyStatus.className = 'status-msg err';
        }
    }

    function fillStoryForm(s) {
        storyForm.title.value = s.title || '';
        storyForm.priority.value = s.priority || 'MEDIUM';
        storyForm.story_points.value = s.story_points || 0;
        storyForm.scope.value = s.scope || '';
        storyForm.blocked_by.value = (s.blocked_by || []).join(', ');
        storyForm.body.value = s.body || '';
        storyPreview.classList.add('hidden');
        storyPreview.innerHTML = '';
    }

    function fillPlanForm(body, tasks) {
        planForm.plan_body.value = body || '';
        tasksTbody.innerHTML = '';
        (tasks || []).forEach((t) => addTaskRow(t));
        planPreview.classList.add('hidden');
        planPreview.innerHTML = '';
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

    async function onSaveStory(e) {
        e.preventDefault();
        if (!currentStoryCode) return;
        const blocked = storyForm.blocked_by.value
            .split(',')
            .map((s) => s.trim())
            .filter(Boolean);
        const patch = {
            title: storyForm.title.value,
            priority: storyForm.priority.value,
            story_points: parseInt(storyForm.story_points.value, 10) || 0,
            scope: storyForm.scope.value,
            blocked_by: blocked,
            body: storyForm.body.value,
        };
        storyStatus.textContent = 'Saving...';
        storyStatus.className = 'status-msg';
        try {
            await apiPut(`/api/story/${encodeURIComponent(currentStoryCode)}`, patch);
            storyStatus.textContent = 'Saved';
            storyStatus.className = 'status-msg ok';
            showToast(`${currentStoryCode} updated`, 'ok');
            await loadBoard();
        } catch (err) {
            storyStatus.textContent = `Save failed: ${err.message || err}`;
            storyStatus.className = 'status-msg err';
        }
    }

    async function onSavePlan(e) {
        e.preventDefault();
        if (!currentStoryCode) return;
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
            plan_body: planForm.plan_body.value,
            tasks,
        };
        planStatus.textContent = 'Saving...';
        planStatus.className = 'status-msg';
        try {
            await apiPut(`/api/story/${encodeURIComponent(currentStoryCode)}/plan`, payload);
            planStatus.textContent = 'Saved';
            planStatus.className = 'status-msg ok';
            showToast(`${currentStoryCode} plan updated`, 'ok');
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
    }

    function togglePreview(textarea, previewEl) {
        if (previewEl.classList.contains('hidden')) {
            previewEl.innerHTML = marked.parse(textarea.value || '');
            previewEl.classList.remove('hidden');
        } else {
            previewEl.classList.add('hidden');
        }
    }

    function closeModal() {
        modal.classList.add('hidden');
        currentStoryCode = null;
    }

    function showToast(msg, kind) {
        toast.textContent = msg;
        toast.classList.remove('hidden', 'ok', 'err');
        if (kind) toast.classList.add(kind);
        clearTimeout(showToast._t);
        showToast._t = setTimeout(() => toast.classList.add('hidden'), 2200);
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
