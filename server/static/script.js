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

const boardEl = document.querySelector('.board');
const modal     = document.getElementById('card-modal');
const form      = document.getElementById('card-form');
const titleEl   = document.getElementById('card-title');
const descEl    = document.getElementById('card-description');
const colEl     = document.getElementById('card-column');
const colorEl   = document.getElementById('card-color');
const tagsEl    = document.getElementById('card-tags');
const delBtn    = document.getElementById('card-delete');
const cancelBtn = document.getElementById('card-cancel');
const addBtn    = document.getElementById('add-card');

function parseTags(raw) {
  return raw.split(',').map(t => t.trim()).filter(Boolean);
}

let editingId = null; // null while creating, card.id while editing

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

  if (card.tags && card.tags.length > 0) {
    const tagsRow = document.createElement('div');
    tagsRow.className = 'tags';
    for (const tag of card.tags) {
      const pill = document.createElement('span');
      pill.className = 'tag';
      pill.textContent = tag;
      tagsRow.appendChild(pill);
    }
    el.appendChild(tagsRow);
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
  if (card) {
    editingId = card.id;
    titleEl.value = card.title;
    descEl.value = card.description || '';
    colEl.value = card.column || 'to-do';
    colorEl.value = card.color || '';
    tagsEl.value = (card.tags || []).join(', ');
    delBtn.hidden = false;
  } else {
    editingId = null;
    titleEl.value = '';
    descEl.value = '';
    colEl.value = 'to-do';
    colorEl.value = '';
    tagsEl.value = '';
    delBtn.hidden = true;
  }
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
    tags: parseTags(tagsEl.value),
  };
  if (!payload.title) return;
  try {
    if (editingId) {
      await apiUpdate(editingId, payload);
    } else {
      await apiCreate(payload);
    }
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
// When the user types @ in the description field, we fetch /api/agents and
// show a filtered dropdown. Arrow keys navigate; Enter selects; Escape closes.

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

async function loadTags() {
  try {
    const res = await fetch('api/tags');
    return res.ok ? await res.json() : [];
  } catch { return []; }
}

// Returns { trigger, query, atStart } if cursor follows @ or #, else null.
function mentionQueryAt(el) {
  const before = el.value.slice(0, el.selectionStart);
  const m = before.match(/[@#](\w*)$/);
  return m ? { trigger: m[0][0], query: m[1], atStart: el.selectionStart - m[0].length } : null;
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
    btn.addEventListener('mousedown', e => {
      e.preventDefault(); // keep textarea focused
      applyMention(textarea, atStart, trigger, name);
      hideMentions();
    });
    suggestEl.appendChild(btn);
  }
  suggestEl.firstChild.classList.add('active');

  const rect = textarea.getBoundingClientRect();
  suggestEl.style.top  = (rect.bottom + 4) + 'px';
  suggestEl.style.left = rect.left + 'px';
  suggestEl.style.width = rect.width + 'px';
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
  const hit = mentionQueryAt(descEl);
  if (!hit) { hideMentions(); return; }
  const names = hit.trigger === '@' ? await loadAgents() : await loadTags();
  if (!names.length) { hideMentions(); return; }
  showMentions(descEl, names, hit.query, hit.atStart, hit.trigger);
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
// Small delay on blur lets the mousedown-on-suggestion fire first.
descEl.addEventListener('blur', () => setTimeout(hideMentions, 150));
// Hide if the modal closes.
modal.addEventListener('close', hideMentions);

// ===== Boot =====

async function reload() {
  try {
    render(await apiList());
  } catch (err) {
    console.error(err);
    boardEl.innerHTML = '<p style="padding:1rem;color:#b54848">Failed to load: ' + err.message + '</p>';
  }
}

reload();
