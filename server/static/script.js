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
const delBtn    = document.getElementById('card-delete');
const cancelBtn = document.getElementById('card-cancel');
const addBtn    = document.getElementById('add-card');

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
// Flow (mouse/pen): pointerdown on a card primes a drag; once the pointer
// moves past DRAG_THRESHOLD px we attach a ghost element following the pointer,
// find the column body under it via elementFromPoint, and show an insertion
// indicator. pointerup either commits the move (PATCH column+position) or,
// if the threshold was never crossed, lets the click handler open the modal.
//
// Flow (touch): cards have touch-action:none, so the browser hands us every
// touch gesture. A touch is ambiguous — it could be a scroll or a drag — so we
// disambiguate by intent: a drag only begins after the finger is held still for
// LONG_PRESS_MS. Any significant move before that timer fires is treated as a
// scroll and we pan the board/column under the finger ourselves. This keeps
// scrolling fluid even when the finger lands on a card (the common case on a
// dense board), while still allowing deliberate drags.

const DRAG_THRESHOLD_PX = 6;
// Touch: hold this long without moving to turn a touch into a drag. Moving
// before this elapses scrolls instead. Tunable; 2s is deliberately cautious.
const LONG_PRESS_MS = 2000;
// Touch: movement beyond this (px) during the long-press window commits to a
// scroll. A little slop so a resting finger's jitter doesn't cancel the press.
const TOUCH_SLOP_PX = 8;

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

// Starts the visible drag at the pointer's last-known position. Called from the
// pointermove handler (mouse/pen, on threshold) and from the long-press timer
// (touch), so it reads coordinates from dragState rather than an event.
function beginDrag() {
  const card = dragState.card;
  card.classList.add('dragging');
  card.classList.remove('drag-armed');
  const r = card.getBoundingClientRect();
  // Use the pointer's offset from the card's top-left so the ghost
  // tracks under the finger/cursor where the drag began.
  dragState.offsetX = dragState.startX - r.left;
  dragState.offsetY = dragState.startY - r.top;
  dragState.ghost = makeGhost(card);
  document.body.appendChild(dragState.ghost);
  positionGhost(dragState.ghost, dragState.lastX, dragState.lastY,
                dragState.offsetX, dragState.offsetY);
}

function clearLongPress() {
  if (dragState && dragState.timer) {
    clearTimeout(dragState.timer);
    dragState.timer = null;
  }
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
  clearLongPress();
  dragState = null;
  if (ds.ghost) ds.ghost.remove();
  if (ds.card) {
    ds.card.classList.remove('dragging');
    ds.card.classList.remove('drag-armed');
  }
  clearDragOverHighlight();
  clearDropIndicator();
}

// Pan the column (vertical) and board (horizontal) under the finger by the
// frame's delta, so a touch-scroll started on a card tracks 1:1. Used only on
// touch, where cards have touch-action:none and the browser won't scroll for us.
function touchScrollBy(ds, e) {
  const dx = e.clientX - ds.lastX;
  const dy = e.clientY - ds.lastY;
  ds.lastX = e.clientX;
  ds.lastY = e.clientY;
  if (ds.scroller) ds.scroller.scrollTop -= dy;
  if (ds.board) ds.board.scrollLeft -= dx;
}

// Global handlers so the drag tracks even when the pointer slides off
// the originating card. setPointerCapture would also work, but routing
// through the document keeps the move/up logic in one place.
document.addEventListener('pointermove', e => {
  if (!dragState || e.pointerId !== dragState.pointerId) return;
  const ds = dragState;

  if (ds.isTouch) {
    if (ds.phase === 'pending') {
      const dx = e.clientX - ds.startX;
      const dy = e.clientY - ds.startY;
      if (dx * dx + dy * dy < TOUCH_SLOP_PX * TOUCH_SLOP_PX) return;
      // Moved before the long-press fired: this is a scroll, not a drag.
      clearLongPress();
      ds.phase = 'scroll';
    }
    if (ds.phase === 'scroll') {
      touchScrollBy(ds, e);
      if (e.cancelable) e.preventDefault();
      return;
    }
    if (ds.phase === 'drag') {
      ds.lastX = e.clientX;
      ds.lastY = e.clientY;
      ds.moved = true;
      if (e.cancelable) e.preventDefault();
      updateDrag(e);
    }
    return;
  }

  // Mouse / pen: immediate threshold drag.
  if (ds.phase !== 'drag') {
    const dx = e.clientX - ds.startX;
    const dy = e.clientY - ds.startY;
    if (dx * dx + dy * dy < DRAG_THRESHOLD_PX * DRAG_THRESHOLD_PX) return;
    ds.lastX = e.clientX;
    ds.lastY = e.clientY;
    ds.phase = 'drag';
    ds.moved = true;
    beginDrag();
  }
  if (e.cancelable) e.preventDefault();
  updateDrag(e);
}, { passive: false });

document.addEventListener('pointerup', e => {
  if (!dragState || e.pointerId !== dragState.pointerId) return;
  const ds = dragState;
  clearLongPress();

  if (ds.phase === 'drag' && ds.moved) {
    // Tag the card so the synthetic click that follows pointerup is swallowed
    // by the card's click handler instead of opening the modal.
    ds.card.dataset.justDragged = '1';
    commitDrag(e);
    return;
  }
  // pending (a tap), scroll, or a long-press that never moved: no move to
  // commit. Tear down any ghost; the click handler decides about the modal.
  if (ds.phase === 'drag') {
    if (ds.ghost) ds.ghost.remove();
    ds.card.classList.remove('dragging');
    clearDragOverHighlight();
    clearDropIndicator();
  }
  ds.card.classList.remove('drag-armed');
  dragState = null;
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

  el.addEventListener('pointerdown', e => {
    // Primary button only for mouse; touch and pen have no button concept
    // so e.button is 0 for them anyway.
    if (e.button !== 0) return;
    const isTouch = e.pointerType === 'touch';
    dragState = {
      cardId: card.id,
      card: el,
      pointerId: e.pointerId,
      isTouch,
      startX: e.clientX,
      startY: e.clientY,
      lastX: e.clientX,
      lastY: e.clientY,
      phase: 'pending',
      moved: false,
      timer: null,
      board: el.closest('.board'),
      scroller: el.closest('.column-body'),
    };
    if (isTouch) {
      // Hold still for LONG_PRESS_MS to turn this touch into a drag. If the
      // finger moves first, the pointermove handler cancels this and scrolls.
      dragState.timer = setTimeout(() => {
        if (!dragState || dragState.phase !== 'pending') return;
        dragState.phase = 'drag';
        dragState.moved = false;
        el.classList.add('drag-armed');
        if (navigator.vibrate) navigator.vibrate(15);
        beginDrag();
      }, LONG_PRESS_MS);
    }
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
    delBtn.hidden = false;
  } else {
    editingId = null;
    titleEl.value = '';
    descEl.value = '';
    colEl.value = 'to-do';
    colorEl.value = '';
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
