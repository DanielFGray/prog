import * as THREE from './vendor/three.module.js';
import { OrbitControls } from './vendor/OrbitControls.js';
import { LineSegments2 } from './vendor/LineSegments2.js';
import { LineSegmentsGeometry } from './vendor/LineSegmentsGeometry.js';
import { LineMaterial } from './vendor/LineMaterial.js';
import { projectGraph, layoutGraph } from './projection.js';

const COLORS = { draft: '#8290a4', open: '#85b9f7', in_progress: '#edbd74', reviewing: '#b9a1e8', blocked: '#ed8b83', done: '#739e92', canceled: '#596579' };
// Filter buckets for the Status control. "blocked" and "ready" aren't stored
// statuses on a task — the CLI derives them from unresolved deps (node.blocked,
// populated by the server from the same resolution rules as `prog ready`).
// "blocked" only ever appears as a stored status on epics (derived from children).
const STATUS_FILTERS = [
  { key: 'draft', match: node => node.status === 'draft' },
  { key: 'open', match: node => node.status === 'open' },
  { key: 'ready', match: node => node.type === 'task' && node.status === 'open' && !node.blocked },
  { key: 'in_progress', match: node => node.status === 'in_progress' },
  { key: 'blocked', match: node => node.blocked || node.status === 'blocked' },
  { key: 'reviewing', match: node => node.status === 'reviewing' },
  { key: 'done', match: node => node.status === 'done' },
  { key: 'canceled', match: node => node.status === 'canceled' },
];
const STATUS_MATCHERS = Object.fromEntries(STATUS_FILTERS.map(f => [f.key, f.match]));
const $ = id => document.getElementById(id);
const state = { data: { nodes: [], edges: [] }, expanded: new Set(), selected: null, hovered: null, filters: { status: new Set(), label: new Set() } };
let projection, layout, meshes = [], lineMaterials = [], graph = new THREE.Group(), pickTargets = [];
let frame = null, pointerStart = null, drag = false;
const viewport = $('viewport');
const scene = new THREE.Scene();
scene.background = new THREE.Color('#0d121a');
const camera = new THREE.PerspectiveCamera(48, 1, 0.1, 100000);
const renderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: 'low-power' });
renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
$('scene').append(renderer.domElement);
scene.add(graph, new THREE.HemisphereLight(0xd4e5ff, 0x344258, 2.4));
const controls = new OrbitControls(camera, renderer.domElement);
controls.enableDamping = true;
controls.dampingFactor = 0.16;
controls.addEventListener('change', requestRender);
const ray = new THREE.Raycaster();
const pointer = new THREE.Vector2();

function requestRender() {
  if (frame === null) frame = requestAnimationFrame(() => {
    frame = null;
    controls.update();
    renderer.render(scene, camera);
  });
}

function matches() {
  const query = $('search').value.trim().toLowerCase();
  return new Set(state.data.nodes.filter(node =>
    (!$('project').value || node.project === $('project').value) &&
    (!state.filters.status.size || [...state.filters.status].some(key => STATUS_MATCHERS[key](node))) &&
    (!state.filters.label.size || node.labels.some(label => state.filters.label.has(label))) &&
    (!$('hide-done').checked || !['done', 'canceled'].includes(node.status) || state.filters.status.has(node.status)) &&
    (!query || `${node.id} ${node.title} ${node.description ?? ''}`.toLowerCase().includes(query))
  ).map(node => node.id));
}

function rebuild(refit = false) {
  setHover(null);
  scene.remove(graph);
  graph.traverse(object => { object.geometry?.dispose(); object.material?.dispose(); });
  focusLines = null;
  graph = new THREE.Group();
  scene.add(graph);
  meshes = []; pickTargets = []; lineMaterials = [];
  const matched = matches();
  projection = projectGraph(state.data.nodes, state.data.edges, state.expanded, matched);
  const visible = new Set(projection.nodes.map(node => node.id));
  if (state.selected && !visible.has(state.selected)) state.selected = null;

  for (const type of ['task', 'epic']) {
    const nodes = projection.nodes.filter(node => node.type === type);
    if (!nodes.length) continue;
    const geometry = new THREE.SphereGeometry(type === 'epic' ? 6 : 2.7, 14, 10);
    const mesh = new THREE.InstancedMesh(geometry, new THREE.MeshLambertMaterial(), nodes.length);
    const matrix = new THREE.Matrix4();
    nodes.forEach((node, index) => {
      const p = layout.positions.get(node.id);
      mesh.setMatrixAt(index, matrix.makeTranslation(p.x, p.y, p.z));
      mesh.setColorAt(index, new THREE.Color(COLORS[node.status]));
    });
    mesh.userData.nodes = nodes;
    mesh.computeBoundingSphere();
    graph.add(mesh); meshes.push(mesh); pickTargets.push(mesh);
  }
  // A ring distinguishes a family entrance from a task. Expanded family
  // corners show reserved space without drawing a wall over the children.
  const rings = [], corners = [];
  for (const node of projection.nodes.filter(node => node.type === 'epic')) {
    const p = layout.positions.get(node.id);
    for (let i = 0; i < 32; i++) {
      const a = i / 32 * Math.PI * 2, b = (i + 1) / 32 * Math.PI * 2;
      rings.push(p.x + Math.cos(a) * 9, p.y, p.z + Math.sin(a) * 9, p.x + Math.cos(b) * 9, p.y, p.z + Math.sin(b) * 9);
    }
    if (!node.expanded || !node.child_count) continue;
    const { center, size } = layout.bounds.get(node.id);
    for (const x of [-1, 1]) for (const y of [-1, 1]) for (const z of [-1, 1]) {
      const point = center.map((value, i) => value + [x, y, z][i] * size[i] / 2);
      for (let axis = 0; axis < 3; axis++) {
        const end = [...point]; end[axis] -= [x, y, z][axis] * Math.min(8, size[axis] / 4);
        corners.push(...point, ...end);
      }
    }
  }
  addLines(rings, '#a9c6ed', 0.55, 1);
  addLines(corners, '#6585ab', 0.32, 1);
  const values = [];
  for (const edge of projection.edges) {
    const a = layout.positions.get(edge.from), b = layout.positions.get(edge.to);
    values.push(a.x, a.y, a.z, b.x, b.y, b.z);
  }
  addLines(values, '#6e91b5', projection.edges.length > 150 ? 0.16 : 0.38, 1);
  $('counts').textContent = `${projection.nodes.length} visible / ${matched.size} matching / ${projection.edges.length} connections`;
  $('empty').hidden = projection.nodes.length !== 0;
  renderList(matched);
  showDetail();
  if (refit) fit();
  updateFocus();
  requestRender();
}

function addLines(values, color, opacity, width) {
  if (!values.length) return null;
  const geometry = new LineSegmentsGeometry();
  geometry.setPositions(values);
  const material = new LineMaterial({ color, linewidth: width, transparent: true, opacity, depthWrite: false });
  material.resolution.set(viewport.clientWidth, viewport.clientHeight);
  const line = new LineSegments2(geometry, material);
  graph.add(line); lineMaterials.push(material);
  return line;
}

let focusLines = null;
function updateFocus() {
  if (focusLines) {
    graph.remove(focusLines);
    focusLines.geometry.dispose(); focusLines.material.dispose();
    lineMaterials = lineMaterials.filter(material => material !== focusLines.material);
    focusLines = null;
  }
  const id = state.hovered ?? state.selected;
  const neighbors = new Set([id]);
  const values = [];
  for (const edge of projection?.edges ?? []) {
    if (edge.from !== id && edge.to !== id) continue;
    neighbors.add(edge.from); neighbors.add(edge.to);
    const source = layout.positions.get(edge.to), target = layout.positions.get(edge.from);
    const a = new THREE.Vector3(source.x, source.y, source.z);
    const b = new THREE.Vector3(target.x, target.y, target.z);
    values.push(...a.toArray(), ...b.toArray());
    // Arrow at the midpoint points from prerequisite to dependent.
    const direction = b.clone().sub(a).normalize();
    let side = new THREE.Vector3(0, 1, 0).cross(direction);
    if (side.lengthSq() < 0.01) side = new THREE.Vector3(1, 0, 0).cross(direction);
    side.normalize().multiplyScalar(2);
    const tip = a.clone().lerp(b, 0.55);
    const base = tip.clone().addScaledVector(direction, -5);
    values.push(...base.clone().add(side).toArray(), ...tip.toArray(), ...base.clone().sub(side).toArray(), ...tip.toArray());
  }
  focusLines = addLines(values, '#e6bb7a', 0.95, 2);
  for (const mesh of meshes) {
    mesh.userData.nodes.forEach((node, index) => {
      const color = new THREE.Color(COLORS[node.status]);
      if (id && !neighbors.has(node.id)) color.multiplyScalar(0.33);
      else if (id === node.id) color.lerp(new THREE.Color('#ffffff'), 0.3);
      mesh.setColorAt(index, color);
    });
    mesh.instanceColor.needsUpdate = true;
  }
  requestRender();
}

function fit(id = null) {
  const box = new THREE.Box3();
  if (id && layout.bounds.has(id)) {
    const { center, size } = layout.bounds.get(id);
    box.setFromCenterAndSize(new THREE.Vector3(...center), new THREE.Vector3(...size));
  } else {
    for (const node of projection?.nodes ?? []) {
      const p = layout.positions.get(node.id);
      box.expandByPoint(new THREE.Vector3(p.x, p.y, p.z));
      if (node.expanded) {
        const { center, size } = layout.bounds.get(node.id);
        box.union(new THREE.Box3().setFromCenterAndSize(new THREE.Vector3(...center), new THREE.Vector3(...size)));
      }
    }
  }
  if (box.isEmpty()) return;
  const center = box.getCenter(new THREE.Vector3());
  const radius = Math.max(box.getSize(new THREE.Vector3()).length() / 2, 24);
  const fov = 2 * Math.atan(Math.tan(THREE.MathUtils.degToRad(camera.fov / 2)) * Math.min(1, camera.aspect));
  const distance = radius / Math.sin(fov / 2) * 1.15;
  controls.target.copy(center);
  camera.position.copy(center).add(new THREE.Vector3(0.7, 0.45, 1).normalize().multiplyScalar(distance));
  camera.near = 0.1; camera.far = Math.max(100000, distance * 5);
  camera.updateProjectionMatrix(); controls.update(); requestRender();
}

function pick(event) {
  const rect = renderer.domElement.getBoundingClientRect();
  pointer.set((event.clientX - rect.left) / rect.width * 2 - 1, 1 - (event.clientY - rect.top) / rect.height * 2);
  camera.updateMatrixWorld();
  ray.setFromCamera(pointer, camera);
  const hit = ray.intersectObjects(pickTargets, false)[0];
  return hit ? hit.object.userData.nodes[hit.instanceId].id : null;
}

function setHover(id, event) {
  const changed = state.hovered !== id;
  state.hovered = id;
  const tooltip = $('tooltip');
  tooltip.hidden = !id;
  if (id && event) {
    const node = projection.byId.get(id);
    tooltip.replaceChildren(document.createTextNode(node.title));
    const info = document.createElement('small');
    info.textContent = `${node.type} / ${node.status.replaceAll('_', ' ')}${node.type === 'epic' ? ` / ${projection.descendants.get(id)} subtasks` : ''}`;
    tooltip.append(info);
    const rect = viewport.getBoundingClientRect();
    tooltip.style.left = `${Math.max(8, Math.min(event.clientX - rect.left + 14, rect.width - tooltip.offsetWidth - 8))}px`;
    tooltip.style.top = `${Math.max(8, Math.min(event.clientY - rect.top + 16, rect.height - tooltip.offsetHeight - 8))}px`;
  }
  renderer.domElement.style.cursor = id ? 'pointer' : 'grab';
  if (changed) updateFocus();
}

renderer.domElement.addEventListener('pointerdown', event => { pointerStart = [event.clientX, event.clientY]; drag = false; });
renderer.domElement.addEventListener('pointermove', event => {
  if (pointerStart && Math.hypot(event.clientX - pointerStart[0], event.clientY - pointerStart[1]) > 4) drag = true;
  setHover(drag || event.pointerType === 'touch' ? null : pick(event), event);
});
renderer.domElement.addEventListener('pointerleave', () => setHover(null));
renderer.domElement.addEventListener('pointercancel', () => { pointerStart = null; drag = false; setHover(null); });
renderer.domElement.addEventListener('pointerup', event => {
  pointerStart = null;
  if (!drag && event.button === 0) select(pick(event));
  drag = false;
});

function select(id, focus = false) {
  state.selected = id;
  if (id && !projection.nodes.some(node => node.id === id)) {
    // Navigation to a hidden subtask opens its ancestors, not unrelated epics.
    for (let parent = projection.parent.get(id); parent; parent = projection.parent.get(parent)) state.expanded.add(parent);
    if (!projection.representative.has(id)) {
      $('project').value = ''; $('search').value = ''; $('hide-done').checked = false;
      clearMultiselect('status'); clearMultiselect('label');
      syncURL();
    }
    rebuild();
  }
  showDetail(); updateFocus();
  for (const item of $('work-list').children) item.setAttribute('aria-current', String(item.dataset.id === id));
  if (focus && id) fit(id);
}

function showDetail() {
  const panel = $('detail');
  panel.hidden = !state.selected;
  if (!state.selected) return;
  const node = projection.byId.get(state.selected);
  if (!node) { panel.hidden = true; return; }
  panel.replaceChildren();
  const close = button('Close', () => select(null), 'close');
  const meta = element('div', `${node.type.toUpperCase()} / ${node.project || 'No project'}`, 'section-heading');
  const title = element('h2', node.title);
  const status = element('div', `${node.status.replaceAll('_', ' ')}${node.blocked ? ' / has unresolved blockers' : ''}`, 'meta');
  const actions = element('div', '', 'detail-actions');
  const children = projection.children.get(node.id);
  if (node.type === 'epic' && children.length) actions.append(button(`${state.expanded.has(node.id) ? 'Collapse' : 'Expand'} ${children.length} children`, () => {
    if (state.expanded.has(node.id)) {
      const collapse = id => { state.expanded.delete(id); for (const child of projection.children.get(id)) collapse(child); };
      collapse(node.id);
    } else state.expanded.add(node.id);
    rebuild(); fit(node.id);
  }, 'primary'));
  actions.append(button('Focus', () => fit(node.id)));
  panel.append(close, meta, title, status, actions);
  if (node.parent_id) relations('PART OF', [node.parent_id]);
  if (children.length) relations(`CHILDREN / ${children.length}`, children);
  const blockers = state.data.edges.filter(edge => edge.from === node.id).map(edge => edge.to);
  const dependents = state.data.edges.filter(edge => edge.to === node.id).map(edge => edge.from);
  relations(`BLOCKED BY / ${blockers.length}`, blockers);
  relations(`BLOCKS / ${dependents.length}`, dependents);
  const internal = projection.internal.get(node.id)?.length ?? 0;
  if (internal) panel.append(element('p', `${internal} dependencies are inside this collapsed family. Expand to see them.`, 'meta'));
  if (node.description) panel.append(element('h3', 'DESCRIPTION'), element('div', node.description, 'description'));
  panel.append(element('p', node.id, 'meta'));
  function relations(heading, ids) {
    if (!ids.length) return;
    panel.append(element('h3', heading));
    for (const id of ids) panel.append(button(projection.byId.get(id)?.title ?? id, () => select(id, true), 'relation'));
  }
}

function renderList(matched) {
  const searching = $('search').value.trim() !== '';
  const nodes = state.data.nodes.filter(node => matched.has(node.id) && (searching || node.type === 'epic'));
  nodes.sort((a, b) => a.title.localeCompare(b.title));
  $('list-title').textContent = searching ? 'SEARCH RESULTS' : 'EPICS';
  $('list-count').textContent = nodes.length;
  $('work-list').replaceChildren(...nodes.map(node => {
    const item = button('', () => select(node.id, true), 'work-item');
    item.dataset.id = node.id; item.setAttribute('role', 'listitem');
    item.setAttribute('aria-current', String(state.selected === node.id));
    item.append(element('span', node.title, 'title'), element('small', `${node.project} / ${node.type === 'epic' ? `${projection.descendants.get(node.id)} subtasks` : node.status}`));
    return item;
  }));
}

function element(tag, text, className = '') {
  const node = document.createElement(tag); node.textContent = text; node.className = className; return node;
}
function button(text, action, className = '') {
  const node = element('button', text, className); node.type = 'button'; node.addEventListener('click', action); return node;
}
function options(id, values, caption) {
  const value = $(id).value;
  $(id).replaceChildren(new Option(caption, ''), ...values.map(item => new Option(item.replaceAll('_', ' '), item)));
  $(id).value = values.includes(value) ? value : '';
}

const MULTISELECT_CAPTIONS = { status: 'All statuses', label: 'All labels' };
function renderMultiselect(key, values, { searchable = false } = {}) {
  state.filters[key] = new Set([...state.filters[key]].filter(value => values.includes(value)));
  const optionRows = values.map(value => {
    const option = element('label', '', 'multiselect-option');
    option.dataset.value = value.toLowerCase();
    const input = document.createElement('input');
    input.type = 'checkbox'; input.value = value; input.checked = state.filters[key].has(value);
    input.addEventListener('change', () => {
      if (input.checked) state.filters[key].add(value); else state.filters[key].delete(value);
      updateMultiselectToggle(key); syncURL(); rebuild(true);
    });
    option.append(input, document.createTextNode(value.replaceAll('_', ' ')));
    return option;
  });
  $(`${key}-menu`).replaceChildren(...(searchable ? [multiselectFilter(key)] : []), ...optionRows);
  updateMultiselectToggle(key);
}
function multiselectFilter(key) {
  const input = document.createElement('input');
  input.type = 'search'; input.className = 'multiselect-filter';
  input.placeholder = `Filter ${MULTISELECT_CAPTIONS[key].replace('All ', '')}`;
  input.addEventListener('click', event => event.stopPropagation());
  input.addEventListener('input', () => {
    const query = input.value.trim().toLowerCase();
    for (const option of $(`${key}-menu`).querySelectorAll('.multiselect-option')) {
      option.hidden = query !== '' && !option.dataset.value.includes(query);
    }
  });
  return input;
}
function updateMultiselectToggle(key) {
  const set = state.filters[key];
  $(`${key}-toggle`).textContent = set.size === 0 ? MULTISELECT_CAPTIONS[key]
    : set.size === 1 ? [...set][0].replaceAll('_', ' ') : `${set.size} selected`;
}
function clearMultiselect(key) {
  state.filters[key].clear();
  for (const input of $(`${key}-menu`).querySelectorAll('input')) input.checked = false;
  updateMultiselectToggle(key);
}
function closeMultiselects() {
  for (const key of Object.keys(MULTISELECT_CAPTIONS)) {
    $(`${key}-menu`).hidden = true;
    $(`${key}-toggle`).setAttribute('aria-expanded', 'false');
  }
}
for (const key of Object.keys(MULTISELECT_CAPTIONS)) {
  $(`${key}-toggle`).addEventListener('click', event => {
    event.stopPropagation();
    const menu = $(`${key}-menu`), opening = menu.hidden;
    closeMultiselects();
    menu.hidden = !opening;
    $(`${key}-toggle`).setAttribute('aria-expanded', String(!menu.hidden));
    const filter = menu.querySelector('.multiselect-filter');
    if (!menu.hidden && filter) { filter.value = ''; filter.dispatchEvent(new Event('input')); filter.focus(); }
  });
}
document.addEventListener('click', event => { if (!event.target.closest('.multiselect')) closeMultiselects(); });

function syncURL() {
  const params = new URLSearchParams();
  for (const id of ['project', 'search']) if ($(id).value) params.set(id, $(id).value);
  for (const key of Object.keys(MULTISELECT_CAPTIONS)) for (const value of state.filters[key]) params.append(key, value);
  if ($('hide-done').checked) params.set('hide_done', '1');
  history.replaceState(null, '', `${location.pathname}${params.size ? '?' + params : ''}`);
}

async function load() {
  $('refresh').disabled = true; $('error').hidden = true;
  try {
    const response = await fetch('/api/graph', { cache: 'no-store' });
    if (!response.ok) throw new Error(`Server returned ${response.status}`);
    const data = await response.json();
    const nextLayout = layoutGraph(data.nodes);
    state.data = data; layout = nextLayout;
    options('project', [...new Set(data.nodes.map(node => node.project))].sort(), 'All projects');
    const params = new URLSearchParams(location.search);
    for (const id of ['project', 'search']) if (params.has(id)) $(id).value = params.get(id);
    state.filters.status = new Set(params.getAll('status'));
    state.filters.label = new Set(params.getAll('label'));
    renderMultiselect('status', STATUS_FILTERS.map(f => f.key));
    renderMultiselect('label', [...new Set(data.nodes.flatMap(node => node.labels))].sort(), { searchable: true });
    $('hide-done').checked = params.get('hide_done') === '1';
    rebuild(true);
  } catch (error) {
    $('error').textContent = `Could not load work. ${error.message}. Use Refresh to retry.`;
    $('error').hidden = false;
  } finally { $('refresh').disabled = false; }
}

renderMultiselect('status', STATUS_FILTERS.map(f => f.key));
for (const id of ['project', 'hide-done']) $(id).addEventListener('change', () => { syncURL(); rebuild(true); });
let searchTimer;
$('search').addEventListener('input', () => { clearTimeout(searchTimer); searchTimer = setTimeout(() => { syncURL(); rebuild(true); }, 160); });
$('refresh').addEventListener('click', load);
$('fit').addEventListener('click', () => { select(null); fit(); });
$('collapse').addEventListener('click', () => { state.expanded.clear(); state.selected = null; rebuild(true); });
document.addEventListener('keydown', event => {
  if (event.key === 'Escape') { select(null); setHover(null); closeMultiselects(); }
  if (event.key === 'f' && !event.target.matches('input,select,textarea')) fit();
});
new ResizeObserver(() => {
  const width = viewport.clientWidth, height = viewport.clientHeight;
  camera.aspect = width / height; camera.updateProjectionMatrix();
  renderer.setSize(width, height);
  for (const material of lineMaterials) material.resolution.set(width, height);
  requestRender();
}).observe(viewport);
load();
