// Kanban frontend, vanilla JS, no deps. Fetches state from /api/cards,
// renders 5 columns, supports drag-and-drop between AND within columns
// (with an insertion indicator) on both desktop and touch via the Pointer
// Events API, and a modal for create / edit / delete.

const COLUMNS = [
  { id: 'to-do',       label: 'To Do' },
  { id: 'blocked',     label: 'Blocked' },
  { id: 'in-progress', label: 'In Progress' },
  { id: 'in-review',   label: 'In Review' },
  { id: 'done',        label: 'Done' },
];

const boardEl      = document.querySelector('.board');
const modal        = document.getElementById('card-modal');
const form         = document.getElementById('card-form');
const titleEl      = document.getElementById('card-title');
const descEl       = document.getElementById('card-description');
const colEl        = document.getElementById('card-column');
const colorEl      = document.getElementById('card-color');
const projectEl    = document.getElementById('card-project');
const delBtn       = document.getElementById('card-delete');
const cancelBtn    = document.getElementById('card-cancel');
const addBtn       = document.getElementById('add-card');
const attachDropEl = document.getElementById('attach-drop');
const attachInputEl= document.getElementById('attach-input');
const attachListEl = document.getElementById('attach-list');
const attachPickBtn= document.getElementById('attach-pick');

let editingId      = null; // null while creating, card.id while editing
let projects       = [];   // cached project list
let attachExisting = [];   // existing attachments loaded from the server for the current card
let attachPending  = [];   // files queued for upload when Save is clicked

// ===== API =====

async function apiList() {
  const res = await fetch('api/cards');
  if (!res.ok) throw new Error('list failed');
  return res.json();
}
async function apiCreate(card) {
  const res = await fetch('api/cards', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(card),
  });
  if (!res.ok) throw new Error('create failed: ' + res.status);
  return res.json();
}
async function apiUpdate(id, patch) {
  const res = await fetch('api/cards/' + encodeURIComponent(id), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  if (!res.ok) throw new Error('update failed: ' + res.status);
  return res.json();
}
async function apiDelete(id) {
  const res = await fetch('api/cards/' + encodeURIComponent(id), { method: 'DELETE' });
  if (!res.ok) throw new Error('delete failed: ' + res.status);
}
async function apiListProjects() {
  const res = await fetch('api/projects');
  if (!res.ok) throw new Error('projects list failed');
  return res.json();
}
async function apiCreateProject(name) {
  const res = await fetch('api/projects', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) throw new Error('create project failed: ' + res.status);
  return res.json();
}

// ===== Project dropdown =====

function projectName(id) {
  if (!id) return null;
  const p = projects.find(p => p.id === id);
  return p ? p.name : null;
}

function populateProjectDropdown(selectedId) {
  projectEl.innerHTML = '<option value="">— none —</option>';
  for (const p of projects) {
    const opt = document.createElement('option');
    opt.value = p.id;
    opt.textContent = p.name;
    projectEl.appendChild(opt);
  }
  const addOpt = document.createElement('option');
  addOpt.value = '__new__';
  addOpt.textContent = '＋ New project…';
  projectEl.appendChild(addOpt);
  projectEl.value = selectedId || '';
}

projectEl.addEventListener('change', async () => {
  if (projectEl.value !== '__new__') return;
  const name = prompt('Project name:');
  if (!name || !name.trim()) { projectEl.value = ''; return; }
  try {
    const p = await apiCreateProject(name.trim());
    projects.push(p);
    populateProjectDropdown(p.id);
  } catch (err) {
    console.error(err);
    alert('Could not create project: ' + err.message);
    projectEl.value = '';
  }
});

// ===== Drag-and-drop helpers =====
//
// We use the Pointer Events API rather than the HTML5 drag-and-drop API
// because the latter does not fire on touch devices. Pointer events unify
// mouse, touch, and stylus, so the same code path drives desktop and mobile.
//
// Flow: pointerdown on a card primes a drag; once the pointer moves past
// DRAG_THRESHOLD px we attach a ghost element following the pointer, find
// the column body under it via elementFromPoint, and show an insertion
// indicator. pointerup either commits the move (PATCH column+position) or,
// if the threshold was never crossed, lets the click handler open the modal.

const DRAG_THRESHOLD_PX = 6;

let dragState = null;

// Single shared indicator line that marks the drop slot.
let dropIndicator = null;
function getDropIndicator() {
  if (!dropIndicator) {
    dropIndicator = document.createElement('div');
    dropIndicator.className = 'drop-indicator';
  }
  return dropIndicator;
}
function clearDropIndicator() {
  if (dropIndicator && dropIndicator.parentNode) {
    dropIndicator.parentNode.removeChild(dropIndicator);
  }
}

// Compute the index, within a column body, where a drop at clientY would land.
// Excludes the card being dragged so reordering within a column behaves as expected.
function dropIndexAt(body, clientY) {
  const cards = [...body.querySelectorAll('.card:not(.dragging)')];
  for (let i = 0; i < cards.length; i++) {
    const r = cards[i].getBoundingClientRect();
    if (clientY < r.top + r.height / 2) return i;
  }
  return cards.length;
}

function placeDropIndicator(body, index) {
  const cards = [...body.querySelectorAll('.card:not(.dragging)')];
  const ind = getDropIndicator();
  if (index >= cards.length) body.appendChild(ind);
  else body.insertBefore(ind, cards[index]);
}

function clearDragOverHighlight() {
  document.querySelectorAll('.column-body.drag-over').forEach(b =>
    b.classList.remove('drag-over'));
}

// Locate the column body under (clientX, clientY). The drag ghost has
// pointer-events: none so it doesn't shadow elementFromPoint, but we hide
// it anyway as a belt-and-braces for stacking-context edge cases.
function columnBodyAt(clientX, clientY) {
  const ghost = dragState && dragState.ghost;
  if (ghost) ghost.style.visibility = 'hidden';
  const el = document.elementFromPoint(clientX, clientY);
  if (ghost) ghost.style.visibility = '';
  if (!el) return null;
  return el.closest('.column-body');
}

function makeGhost(card) {
  const ghost = card.cloneNode(true);
  ghost.classList.add('drag-ghost');
  const r = card.getBoundingClientRect();
  ghost.style.width = r.width + 'px';
  return ghost;
}

function positionGhost(ghost, clientX, clientY, offsetX, offsetY) {
  ghost.style.left = (clientX - offsetX) + 'px';
  ghost.style.top  = (clientY - offsetY) + 'px';
}

function beginDrag(e) {
  const card = dragState.card;
  card.classList.add('dragging');
  const r = card.getBoundingClientRect();
  // Use the pointer's offset from the card's top-left so the ghost
  // tracks under the finger/cursor where the drag began.
  dragState.offsetX = dragState.startX - r.left;
  dragState.offsetY = dragState.startY - r.top;
  dragState.ghost = makeGhost(card);
  document.body.appendChild(dragState.ghost);
  positionGhost(dragState.ghost, e.clientX, e.clientY,
                dragState.offsetX, dragState.offsetY);
}

function updateDrag(e) {
  positionGhost(dragState.ghost, e.clientX, e.clientY,
                dragState.offsetX, dragState.offsetY);
  clearDragOverHighlight();
  const body = columnBodyAt(e.clientX, e.clientY);
  if (body) {
    body.classList.add('drag-over');
    placeDropIndicator(body, dropIndexAt(body, e.clientY));
  } else {
    clearDropIndicator();
  }
}

async function commitDrag(e) {
  const ds = dragState;
  dragState = null;
  const body = columnBodyAt(e.clientX, e.clientY);
  ds.ghost.remove();
  ds.card.classList.remove('dragging');
  clearDragOverHighlight();
  clearDropIndicator();
  if (!body) return;
  const columnId = body.closest('.column').dataset.id;
  const index = dropIndexAt(body, e.clientY);
  try {
    await apiUpdate(ds.cardId, { column: columnId, position: index });
    reload();
  } catch (err) {
    console.error(err);
    alert('Move failed: ' + err.message);
  }
}

function abortDrag() {
  if (!dragState) return;
  const ds = dragState;
  dragState = null;
  if (ds.ghost) ds.ghost.remove();
  if (ds.card) ds.card.classList.remove('dragging');
  clearDragOverHighlight();
  clearDropIndicator();
}

// Global handlers so the drag tracks even when the pointer slides off
// the originating card. setPointerCapture would also work, but routing
// through the document keeps the move/up logic in one place.
document.addEventListener('pointermove', e => {
  if (!dragState || e.pointerId !== dragState.pointerId) return;
  if (!dragState.started) {
    const dx = e.clientX - dragState.startX;
    const dy = e.clientY - dragState.startY;
    if (dx * dx + dy * dy < DRAG_THRESHOLD_PX * DRAG_THRESHOLD_PX) return;
    dragState.started = true;
    beginDrag(e);
  }
  // Stop the page scrolling on touch once a drag is live.
  if (e.cancelable) e.preventDefault();
  updateDrag(e);
}, { passive: false });

document.addEventListener('pointerup', e => {
  if (!dragState || e.pointerId !== dragState.pointerId) return;
  if (!dragState.started) {
    // Below threshold — treat as a tap; the card's click handler will fire.
    dragState = null;
    return;
  }
  // Tag the card so the synthetic click that follows pointerup is swallowed
  // by the card's click handler instead of opening the modal.
  dragState.card.dataset.justDragged = '1';
  commitDrag(e);
});

document.addEventListener('pointercancel', abortDrag);

// ===== Rendering =====

function render(cards) {
  boardEl.innerHTML = '';
  const byCol = Object.fromEntries(COLUMNS.map(c => [c.id, []]));
  for (const card of cards) {
    if (!byCol[card.column]) byCol[card.column] = [];
    byCol[card.column].push(card);
  }
  for (const col of COLUMNS) {
    const colCards = (byCol[col.id] || []).sort((a, b) => a.position - b.position);
    boardEl.appendChild(renderColumn(col, colCards));
  }
}

function renderColumn(col, cards) {
  const colEl = document.createElement('section');
  colEl.className = 'column';
  colEl.dataset.id = col.id;
  colEl.innerHTML = `
    <header class="column-header">
      <span>${col.label}</span>
      <span class="count">${cards.length}</span>
    </header>
    <div class="column-body"></div>
  `;
  const body = colEl.querySelector('.column-body');
  // Drop targets are detected dynamically via elementFromPoint during
  // pointermove (see the global drag handlers above), so the body itself
  // needs no per-column listeners.

  for (const card of cards) {
    body.appendChild(renderCard(card));
  }
  return colEl;
}

function renderCard(card) {
  const el = document.createElement('article');
  el.className = 'card';
  el.dataset.id = card.id;
  if (card.color) {
    el.dataset.color = card.color;
  }

  const title = document.createElement('div');
  title.className = 'title';
  title.textContent = card.title;
  el.appendChild(title);

  if (card.description) {
    const desc = document.createElement('div');
    desc.className = 'desc-preview';
    desc.textContent = card.description;
    el.appendChild(desc);
  }

  const pName = projectName(card.projectId);
  if (pName) {
    const tag = document.createElement('div');
    tag.className = 'card-project-tag';
    tag.textContent = pName;
    el.appendChild(tag);
  }

  const idChip = document.createElement('span');
  idChip.className = 'card-id';
  idChip.textContent = card.id.slice(0, 8);
  el.appendChild(idChip);

  if (card.attachments && card.attachments.length > 0) {
    const clip = document.createElement('span');
    clip.className = 'card-attach-count';
    const n = card.attachments.length;
    clip.textContent = '📎 ' + n + (n === 1 ? ' file' : ' files');
    el.appendChild(clip);
  }

  el.addEventListener('pointerdown', e => {
    // Primary button only for mouse; touch and pen have no button concept
    // so e.button is 0 for them anyway.
    if (e.button !== 0) return;
    dragState = {
      cardId: card.id,
      card: el,
      pointerId: e.pointerId,
      startX: e.clientX,
      startY: e.clientY,
      started: false,
    };
  });

  el.addEventListener('click', e => {
    if (el.dataset.justDragged) {
      delete el.dataset.justDragged;
      e.stopPropagation();
      return;
    }
    openModal(card);
  });

  return el;
}

// ===== Modal =====

function openModal(card) {
  attachPending  = [];
  attachExisting = card ? (card.attachments || []) : [];
  populateProjectDropdown(card ? card.projectId : '');
  if (card) {
    editingId = card.id;
    titleEl.value = card.title;
    descEl.value = card.description || '';
    colEl.value = card.column || 'to-do';
    colorEl.value = card.color || '';
    delBtn.hidden = false;
  } else {
    editingId = null;
    titleEl.value = '';
    descEl.value = '';
    colEl.value = 'to-do';
    colorEl.value = '';
    delBtn.hidden = true;
  }
  renderAttachList();
  modal.showModal();
  setTimeout(() => titleEl.focus(), 0);
}

cancelBtn.addEventListener('click', () => modal.close());

form.addEventListener('submit', async e => {
  e.preventDefault();
  const payload = {
    title: titleEl.value.trim(),
    description: descEl.value,
    column: colEl.value,
    color: colorEl.value,
    projectId: projectEl.value === '__new__' ? '' : projectEl.value,
  };
  if (!payload.title) return;
  try {
    let cardId = editingId;
    if (editingId) {
      await apiUpdate(editingId, payload);
    } else {
      const created = await apiCreate(payload);
      cardId = created.id;
    }
    // Upload any pending files
    for (const p of attachPending) {
      const fd = new FormData();
      fd.append('file', p.file);
      const res = await fetch('api/cards/' + encodeURIComponent(cardId) + '/attachments', {
        method: 'POST', body: fd,
      });
      if (!res.ok) console.error('attachment upload failed for', p.file.name, res.status);
    }
    attachPending = [];
    modal.close();
    reload();
  } catch (err) {
    console.error(err);
    alert('Save failed: ' + err.message);
  }
});

delBtn.addEventListener('click', async () => {
  if (!editingId) return;
  if (!confirm('Delete this card?')) return;
  try {
    await apiDelete(editingId);
    modal.close();
    reload();
  } catch (err) {
    console.error(err);
    alert('Delete failed: ' + err.message);
  }
});

addBtn.addEventListener('click', () => openModal(null));

// ===== Theme toggle =====
//
// The initial theme is applied in index.html before first paint, reading
// localStorage and falling back to prefers-color-scheme. Here we just wire
// the button to flip it and update the aria-pressed state.

const themeBtn = document.getElementById('theme-toggle');
function syncThemeButton() {
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
  themeBtn.setAttribute('aria-pressed', isDark ? 'true' : 'false');
  themeBtn.setAttribute(
    'aria-label',
    isDark ? 'Switch to light mode' : 'Switch to dark mode'
  );
  themeBtn.title = isDark ? 'Switch to light mode' : 'Switch to dark mode';
}
themeBtn.addEventListener('click', () => {
  const next = document.documentElement.getAttribute('data-theme') === 'dark'
    ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', next);
  try { localStorage.setItem('kanban-theme', next); } catch (e) { /* ignore */ }
  syncThemeButton();
});
syncThemeButton();

// ===== @-mention agent suggestions =====
//
// Typing @ in the description field fetches /api/agents and shows a filtered
// dropdown. Arrow keys navigate; Enter selects; Escape closes.

const suggestEl = document.getElementById('mention-suggestions');
let agentsCache = null;
let mentionState = null; // { textarea, atStart, trigger } while the dropdown is open

async function loadAgents() {
  if (agentsCache !== null) return agentsCache;
  try {
    const res = await fetch('api/agents');
    agentsCache = res.ok ? await res.json() : [];
  } catch { agentsCache = []; }
  return agentsCache;
}

// Returns { trigger, query, atStart } if cursor follows @, else null.
function mentionQueryAt(el) {
  const before = el.value.slice(0, el.selectionStart);
  const m = before.match(/@(\w*)$/);
  return m ? { trigger: '@', query: m[1], atStart: el.selectionStart - m[0].length } : null;
}

function applyMention(textarea, atStart, trigger, name) {
  const before = textarea.value.slice(0, atStart);
  const after = textarea.value.slice(textarea.selectionStart);
  textarea.value = before + trigger + name + after;
  const pos = atStart + 1 + name.length;
  textarea.selectionStart = textarea.selectionEnd = pos;
}

function hideMentions() {
  suggestEl.hidden = true;
  suggestEl.innerHTML = '';
  mentionState = null;
}

function showMentions(textarea, names, query, atStart, trigger) {
  const filtered = names.filter(n => n.toLowerCase().startsWith(query.toLowerCase()));
  if (!filtered.length) { hideMentions(); return; }

  mentionState = { textarea, atStart, trigger };
  suggestEl.innerHTML = '';
  for (const name of filtered) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'mention-item';
    btn.dataset.name = name;
    const sigil = document.createElement('span');
    sigil.className = 'mention-at';
    sigil.textContent = trigger;
    btn.appendChild(sigil);
    btn.appendChild(document.createTextNode(name));
    // Desktop: mousedown fires before blur; preventDefault keeps textarea focused.
    btn.addEventListener('mousedown', e => {
      e.preventDefault();
      applyMention(textarea, atStart, trigger, name);
      hideMentions();
    });
    // Mobile: touchend fires after blur (blur has a 300 ms grace period).
    // preventDefault stops the subsequent synthetic mousedown/click.
    btn.addEventListener('touchend', e => {
      e.preventDefault();
      applyMention(textarea, atStart, trigger, name);
      hideMentions();
    });
    suggestEl.appendChild(btn);
  }
  suggestEl.firstChild.classList.add('active');

  // Flip above the textarea when the virtual keyboard is covering the space below.
  // visualViewport.height shrinks when the keyboard opens (unlike window.innerHeight
  // on iOS), so this reliably detects the keyboard on mobile.
  const rect = textarea.getBoundingClientRect();
  const vpH = window.visualViewport ? window.visualViewport.height : window.innerHeight;
  suggestEl.classList.toggle('above', vpH - rect.bottom < 120);
  suggestEl.hidden = false;
}

function moveMentionActive(delta) {
  const items = [...suggestEl.querySelectorAll('.mention-item')];
  if (!items.length) return;
  const cur = items.findIndex(el => el.classList.contains('active'));
  items[cur]?.classList.remove('active');
  items[Math.max(0, Math.min(items.length - 1, cur + delta))].classList.add('active');
}

async function onDescInput() {
  // Snapshot cursor position synchronously — selectionStart can reset to 0
  // during async gaps (mobile keyboards, automation contexts).
  const snapValue = descEl.value;
  const snapSel   = descEl.selectionStart;
  // Defer one tick so mobile keyboards can settle before we check.
  await new Promise(r => setTimeout(r, 0));
  const before = snapValue.slice(0, snapSel);
  const m = before.match(/@(\w*)$/);
  if (!m) { hideMentions(); return; }
  const query = m[1], atStart = snapSel - m[0].length;
  const names = await loadAgents();
  if (!names.length) { hideMentions(); return; }
  showMentions(descEl, names, query, atStart, '@');
}

function onDescKeydown(e) {
  if (suggestEl.hidden) return;
  if (e.key === 'Escape') { hideMentions(); e.stopPropagation(); return; }
  if (e.key === 'ArrowDown') { e.preventDefault(); moveMentionActive(1); return; }
  if (e.key === 'ArrowUp')   { e.preventDefault(); moveMentionActive(-1); return; }
  if (e.key === 'Enter' && mentionState) {
    const active = suggestEl.querySelector('.mention-item.active');
    if (active) {
      e.preventDefault();
      applyMention(mentionState.textarea, mentionState.atStart, mentionState.trigger, active.dataset.name);
      hideMentions();
    }
  }
}

descEl.addEventListener('input',   onDescInput);
descEl.addEventListener('keydown', onDescKeydown);
// Grace period on blur: 300 ms lets touchend fire before we close the list.
descEl.addEventListener('blur', () => setTimeout(hideMentions, 300));
// Hide if the modal closes.
modal.addEventListener('close', hideMentions);

// ===== Attachments =====

function fmtSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

function renderAttachList() {
  attachListEl.innerHTML = '';
  for (const a of attachExisting) {
    const li = document.createElement('li');
    li.className = 'attach-item';
    const link = document.createElement('a');
    link.href = 'api/cards/' + encodeURIComponent(editingId) + '/attachments/' + encodeURIComponent(a.id);
    link.download = a.filename;
    link.textContent = a.filename;
    link.className = 'attach-name';
    const size = document.createElement('span');
    size.className = 'attach-size';
    size.textContent = fmtSize(a.size);
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'attach-del-btn';
    btn.title = 'Remove attachment';
    btn.textContent = '×';
    btn.addEventListener('click', async () => {
      try {
        const res = await fetch('api/cards/' + encodeURIComponent(editingId) + '/attachments/' + encodeURIComponent(a.id), { method: 'DELETE' });
        if (!res.ok) throw new Error(res.status);
        attachExisting = attachExisting.filter(x => x.id !== a.id);
        li.remove();
      } catch (err) {
        alert('Could not delete attachment: ' + err.message);
      }
    });
    li.append(link, size, btn);
    attachListEl.appendChild(li);
  }
  for (const p of attachPending) {
    const li = document.createElement('li');
    li.className = 'attach-item attach-pending';
    const name = document.createElement('span');
    name.className = 'attach-name';
    name.textContent = p.file.name;
    const size = document.createElement('span');
    size.className = 'attach-size';
    size.textContent = fmtSize(p.file.size);
    const badge = document.createElement('span');
    badge.className = 'attach-pending-badge';
    badge.textContent = 'pending';
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'attach-del-btn';
    btn.title = 'Remove';
    btn.textContent = '×';
    btn.addEventListener('click', () => {
      attachPending = attachPending.filter(x => x !== p);
      li.remove();
    });
    li.append(name, size, badge, btn);
    attachListEl.appendChild(li);
  }
}

const MAX_ATTACH_BYTES = 10 * 1024 * 1024;

function addAttachFiles(fileList) {
  for (const f of fileList) {
    if (f.size > MAX_ATTACH_BYTES) {
      alert(f.name + ' is too large (max 10 MB).');
      continue;
    }
    attachPending.push({ file: f });
  }
  renderAttachList();
}

attachPickBtn.addEventListener('click', () => attachInputEl.click());
attachInputEl.addEventListener('change', () => {
  addAttachFiles(attachInputEl.files);
  attachInputEl.value = '';
});
attachDropEl.addEventListener('dragover', e => {
  e.preventDefault();
  attachDropEl.classList.add('drag-active');
});
attachDropEl.addEventListener('dragleave', () => attachDropEl.classList.remove('drag-active'));
attachDropEl.addEventListener('drop', e => {
  e.preventDefault();
  attachDropEl.classList.remove('drag-active');
  addAttachFiles(e.dataTransfer.files);
});

// ===== Boot =====

async function reload() {
  try {
    const [cards, projs] = await Promise.all([apiList(), apiListProjects()]);
    projects = projs || [];
    render(cards);
  } catch (err) {
    console.error(err);
    boardEl.innerHTML = '<p style="padding:1rem;color:#b54848">Failed to load: ' + err.message + '</p>';
  }
}

reload();
