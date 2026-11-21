'use strict';
// Forms remain functional without JavaScript. Enhancement only adds drag ordering
// and hides fields which do not apply to the selected media type.
const kind = document.querySelector('#kind');
if (kind) {
  const refresh = () => {
    document.querySelector('#episode-fields').hidden = kind.value === 'movie';
    document.querySelector('#first-episode-field').hidden = kind.value === 'season';
    document.querySelector('#last-episode-field').hidden = !['season', 'episode_range'].includes(kind.value);
    for (const input of document.querySelectorAll('#episode-fields input')) {
      input.disabled = !!input.closest('[hidden]');
    }
  };
  kind.addEventListener('change', refresh);
  refresh();
}
const list = document.querySelector('#entry-list');
if (list) {
  let dragged = null;
  let original = '';
  const order = () => [...list.querySelectorAll('[data-entry-id]')].map(row => row.dataset.entryId).join(' ');
  const clearMarkers = () => {
    for (const row of list.children) row.classList.remove('drop-before', 'drop-after');
  };
  for (const handle of list.querySelectorAll('.drag-handle')) {
    handle.hidden = false;
    handle.draggable = true;
    handle.addEventListener('dragstart', event => {
      dragged = handle.closest('[data-entry-id]');
      original = order();
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/plain', dragged.dataset.entryId);
      document.querySelector('#order-status').textContent = 'Moving entry. Drop it at its new position.';
    });
    handle.addEventListener('dragend', () => {
      // Moving the source in dragover can cancel native Chromium drags. Only
      // mark a destination until drop, so a cancelled drag never changes order.
      clearMarkers();
      dragged = null;
      document.querySelector('#order-status').textContent = '';
    });
  }
  list.addEventListener('dragover', event => {
    if (!dragged) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    clearMarkers();
    const target = event.target.closest('[data-entry-id]');
    if (!target || target === dragged) return;
    const bounds = target.getBoundingClientRect();
    target.classList.add(event.clientY < bounds.y + bounds.height / 2 ? 'drop-before' : 'drop-after');
  });
  list.addEventListener('drop', event => {
    if (!dragged) return;
    event.preventDefault();
    const target = event.target.closest('[data-entry-id]');
    clearMarkers();
    if (!target || target === dragged) return;
    const bounds = target.getBoundingClientRect();
    list.insertBefore(dragged, event.clientY < bounds.y + bounds.height / 2 ? target : target.nextSibling);
    const current = order();
    dragged = null;
    if (current === original) return;
    document.querySelector('#entry-order').value = current;
    document.querySelector('#order-status').textContent = 'Saving order…';
    document.querySelector('#reorder-form').requestSubmit();
  });
}

if (location.hash.startsWith('#entry-')) {
  document.getElementById(location.hash.slice(1))?.focus();
}

// Bulk bar: show how many entries are selected, and confirm removals.
const bulk = document.querySelector('#bulk-actions');
if (bulk) {
  const count = document.querySelector('#selected-count');
  const boxes = () => [...document.querySelectorAll('input[form="bulk-actions"][name="entry_id"]')];
  const idle = count.textContent;
  const update = () => {
    const n = boxes().filter(box => box.checked).length;
    count.textContent = n ? `${n} selected` : idle;
  };
  document.addEventListener('change', event => {
    if (event.target.matches('input[form="bulk-actions"]')) update();
  });
  bulk.addEventListener('submit', event => {
    const action = event.submitter && event.submitter.value;
    if (action !== 'remove') return;
    const n = boxes().filter(box => box.checked).length;
    const confirmBox = bulk.querySelector('input[name="confirm_remove"]');
    if (n && !confirmBox.checked) {
      if (confirm(`Remove ${n} ${n === 1 ? 'entry' : 'entries'} from this collection?`)) confirmBox.checked = true;
      else event.preventDefault();
    }
  });
  update();
}

// The add-collection modal closes with Escape, like any dialog.
const modal = document.querySelector('#add-modal');
if (modal) {
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape') modal.querySelector('[data-close]')?.click();
  });
  const first = modal.querySelector('.modal-bar input:not([type=hidden])') || modal.querySelector('.modal-body input:not([type=hidden]),.modal-body textarea');
  if (first && !location.hash) first.focus();
}

// Closing the add dialog goes back to where it was opened from, not always to Collections.
document.querySelectorAll('[data-close]').forEach(link => link.addEventListener('click', event => {
  if (!document.referrer) return;
  const from = new URL(document.referrer);
  if (from.origin !== location.origin || from.pathname.endsWith('/add') || from.pathname.includes('/login')) return;
  event.preventDefault();
  location.assign(from.href);
}));

// The collections filter applies as soon as it is changed.
document.querySelectorAll('.listcol-search select').forEach(select => select.addEventListener('change', () => select.form.submit()));
