// Kanban frontend, vanilla JS, no deps. Fetches state from /api/cards,
// renders 5 columns, supports drag-and-drop between columns, and a modal
// for create / edit / delete.

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

  // Drop target on the column body.
  body.addEventListener('dragover', e => {
    e.preventDefault();
    body.classList.add('drag-over');
  });
  body.addEventListener('dragleave', () => body.classList.remove('drag-over'));
  body.addEventListener('drop', async e => {
    e.preventDefault();
    body.classList.remove('drag-over');
    const cardId = e.dataTransfer.getData('text/card-id');
    if (!cardId) return;
    try {
      await apiUpdate(cardId, { column: col.id });
      reload();
    } catch (err) {
      console.error(err);
      alert('Move failed: ' + err.message);
    }
  });

  for (const card of cards) {
    body.appendChild(renderCard(card));
  }
  return colEl;
}

function renderCard(card) {
  const el = document.createElement('article');
  el.className = 'card';
  el.draggable = true;
  el.dataset.id = card.id;

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

  el.addEventListener('dragstart', e => {
    e.dataTransfer.setData('text/card-id', card.id);
    e.dataTransfer.effectAllowed = 'move';
    el.classList.add('dragging');
  });
  el.addEventListener('dragend', () => el.classList.remove('dragging'));

  el.addEventListener('click', () => openModal(card));

  return el;
}

// ===== Modal =====

function openModal(card) {
  if (card) {
    editingId = card.id;
    titleEl.value = card.title;
    descEl.value = card.description || '';
    colEl.value = card.column || 'to-do';
    delBtn.hidden = false;
  } else {
    editingId = null;
    titleEl.value = '';
    descEl.value = '';
    colEl.value = 'to-do';
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
