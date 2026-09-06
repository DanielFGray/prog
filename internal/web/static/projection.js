// Membership and dependency direction are independent: from depends on to.
export function projectGraph(nodes, edges, expanded = new Set(), matches = null) {
  const byId = new Map(nodes.map(node => [node.id, node]));
  const children = new Map(nodes.map(node => [node.id, []]));
  const parent = new Map();
  for (const node of nodes) {
    if (byId.get(node.parent_id)?.type === 'epic') {
      parent.set(node.id, node.parent_id);
      children.get(node.parent_id).push(node.id);
    }
  }
  const depths = new Map();
  const visiting = new Set();
  function depth(id) {
    if (depths.has(id)) return depths.get(id);
    if (visiting.has(id)) throw new Error(`Epic membership contains a cycle at ${id}`);
    visiting.add(id);
    const value = parent.has(id) ? depth(parent.get(id)) + 1 : 0;
    visiting.delete(id);
    depths.set(id, value);
    return value;
  }
  for (const id of byId.keys()) depth(id);
  const retained = new Set(matches ?? byId.keys());
  for (const id of [...retained]) {
    if (!byId.has(id)) { retained.delete(id); continue; }
    let ancestor = parent.get(id);
    while (ancestor) { retained.add(ancestor); ancestor = parent.get(ancestor); }
  }
  const descendants = new Map();
  function count(id) {
    const result = children.get(id).reduce((total, child) => total + 1 + count(child), 0);
    descendants.set(id, result);
    return result;
  }
  const roots = nodes.filter(node => !parent.has(node.id)).map(node => node.id).sort();
  for (const id of roots) count(id);
  const representative = new Map();
  const visible = [];
  function walk(id, hiddenBy = null) {
    if (!retained.has(id)) return;
    representative.set(id, hiddenBy ?? id);
    if (!hiddenBy) visible.push({
      ...byId.get(id),
      parent_id: parent.get(id) ?? null,
      child_count: children.get(id).length,
      descendant_count: descendants.get(id),
      expanded: expanded.has(id),
      context: matches !== null && !matches.has(id),
    });
    const hidden = hiddenBy ?? (expanded.has(id) ? null : id);
    for (const child of [...children.get(id)].sort()) walk(child, hidden);
  }
  for (const id of roots) walk(id);
  const aggregate = new Map();
  const internal = new Map();
  for (const edge of edges) {
    const from = representative.get(edge.from), to = representative.get(edge.to);
    if (!from || !to) continue;
    if (from === to) {
      if (!internal.has(from)) internal.set(from, []);
      internal.get(from).push(edge);
      continue;
    }
    const key = JSON.stringify([from, to]);
    if (!aggregate.has(key)) aggregate.set(key, { from, to, underlying: [] });
    aggregate.get(key).underlying.push(edge);
  }
  return {
    nodes: visible,
    edges: [...aggregate.values()].map(edge => ({ ...edge, count: edge.underlying.length })),
    internal, representative, children, parent, byId, roots, descendants,
  };
}

// Reserve each complete family's bounds, even when collapsed. Expanding a
// family must not move its neighbors. Only geometry radii use world units;
// camera distance never changes node sizes or edge endpoints.
export function layoutGraph(nodes) {
  const tree = projectGraph(nodes, []);
  const positions = new Map();
  const bounds = new Map();
  const sizes = new Map();
  const offsets = new Map();
  const gap = 12;
  function measure(id) {
    const childIds = [...tree.children.get(id)].sort();
    if (!childIds.length) {
      const size = tree.byId.get(id).type === 'epic' ? 12 : 6;
      sizes.set(id, [size, size, size]);
      offsets.set(id, []);
      return sizes.get(id);
    }
    const packed = pack(childIds.map(child => ({ id: child, size: measure(child) })), gap);
    // The epic is the entrance to its family, above the children's volume.
    const size = [Math.max(12, packed.size[0]), packed.size[1] + 20, Math.max(12, packed.size[2])];
    offsets.set(id, packed.entries.map(entry => ({ ...entry, offset: [entry.offset[0], entry.offset[1] - 10, entry.offset[2]] })));
    sizes.set(id, size);
    return size;
  }
  const projects = new Map();
  for (const id of tree.roots) {
    const project = tree.byId.get(id).project;
    if (!projects.has(project)) projects.set(project, []);
    projects.get(project).push({ id, size: measure(id) });
  }
  const islands = [...projects].sort(([a], [b]) => a.localeCompare(b)).map(([name, roots]) => ({ name, ...pack(roots, 24) }));
  const world = pack(islands.map((island, i) => ({ id: i, size: island.size })), 64);
  const projectBounds = [];
  function place(id, center) {
    const size = sizes.get(id);
    bounds.set(id, { center, size });
    const children = offsets.get(id);
    positions.set(id, { x: center[0], y: center[1] + (children.length ? size[1] / 2 - 6 : 0), z: center[2] });
    for (const child of children) place(child.id, child.offset.map((value, axis) => value + center[axis]));
  }
  for (const entry of world.entries) {
    const island = islands[entry.id];
    projectBounds.push({ name: island.name, center: entry.offset, size: island.size });
    for (const root of island.entries) place(root.id, root.offset.map((value, axis) => value + entry.offset[axis]));
  }
  return { positions, bounds, projects: projectBounds };
}

// Balanced boxes avoid allocating an entire giant grid cell to every small
// task. At each split, join along the axis with the shortest resulting box.
function pack(entries, gap) {
  if (!entries.length) return { size: [0, 0, 0], entries: [] };
  if (entries.length === 1) return { size: entries[0].size, entries: [{ id: entries[0].id, offset: [0, 0, 0] }] };
  const middle = Math.ceil(entries.length / 2);
  const a = pack(entries.slice(0, middle), gap), b = pack(entries.slice(middle), gap);
  let axis = 0;
  for (let i = 1; i < 3; i++) if (a.size[i] + b.size[i] < a.size[axis] + b.size[axis]) axis = i;
  const size = a.size.map((value, i) => i === axis ? value + b.size[i] + gap : Math.max(value, b.size[i]));
  return {
    size,
    entries: [
      ...a.entries.map(entry => ({ id: entry.id, offset: entry.offset.map((value, i) => value - (i === axis ? (b.size[i] + gap) / 2 : 0)) })),
      ...b.entries.map(entry => ({ id: entry.id, offset: entry.offset.map((value, i) => value + (i === axis ? (a.size[i] + gap) / 2 : 0)) })),
    ],
  };
}
