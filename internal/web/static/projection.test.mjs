import assert from 'node:assert/strict';
import test from 'node:test';
import { projectGraph, layoutGraph } from './projection.js';

const nodes = [
  { id: 'root', type: 'epic', project: 'p' },
  { id: 'nested', type: 'epic', parent_id: 'root', project: 'p' },
  { id: 'a', type: 'task', parent_id: 'nested', project: 'p' },
  { id: 'b', type: 'task', parent_id: 'nested', project: 'p' },
  ...Array.from({ length: 8 }, (_, i) => ({ id: `loose-${i}`, type: 'task', project: 'p' })),
];
const edges = [
  { from: 'a', to: 'b' },
  { from: 'loose-0', to: 'a' },
  { from: 'loose-0', to: 'b' },
  { from: 'a', to: 'loose-1' },
  { from: 'b', to: 'a' }, // Real dependency cycles must not break membership.
];

test('overview hides all descendants and accounts for every real dependency', () => {
  const graph = projectGraph(nodes, edges);
  assert.deepEqual(graph.nodes.map(node => node.id).sort(), ['root', ...nodes.filter(node => node.id.startsWith('loose')).map(node => node.id)].sort());
  assert.equal(graph.nodes.find(node => node.id === 'root').descendant_count, 3);
  assert.equal(graph.edges.find(edge => edge.from === 'loose-0').count, 2);
  assert.equal(graph.internal.get('root').length, 2);
  const recovered = [...graph.edges.flatMap(edge => edge.underlying), ...[...graph.internal.values()].flat()];
  assert.deepEqual(new Set(recovered), new Set(edges));
});

test('nested expansion reveals only that level, including sibling dependencies', () => {
  const first = projectGraph(nodes, edges, new Set(['root']));
  assert(first.nodes.some(node => node.id === 'nested'));
  assert(!first.nodes.some(node => node.id === 'a'));
  assert.equal(first.representative.get('a'), 'nested');
  const second = projectGraph(nodes, edges, new Set(['root', 'nested']));
  assert.equal(second.nodes.length, nodes.length);
  assert.equal(second.edges.length, edges.length);
  assert(second.edges.some(edge => edge.from === 'a' && edge.to === 'b'));
  const collapsed = projectGraph(nodes, edges, new Set(['nested']));
  assert.equal(collapsed.representative.get('a'), 'root');
  assert(!collapsed.nodes.some(node => node.id === 'nested'));
});

test('filters retain ancestry without falsely matching or revealing siblings', () => {
  const graph = projectGraph(nodes, edges, new Set(['root', 'nested']), new Set(['a']));
  assert.deepEqual(new Set(graph.nodes.map(node => node.id)), new Set(['root', 'nested', 'a']));
  assert.equal(graph.nodes.find(node => node.id === 'root').context, true);
  assert.equal(graph.nodes.find(node => node.id === 'a').context, false);
  assert.equal(graph.edges.length, 0);
});

test('layout is deterministic, volumetric, and assigns every item once', () => {
  const layout = layoutGraph(nodes);
  assert.equal(layout.positions.size, nodes.length);
  assert.equal(new Set([...layout.positions.values()].map(p => JSON.stringify(p))).size, nodes.length);
  assert.deepEqual(layoutGraph([...nodes].reverse()).positions, layout.positions);
  const points = [...layout.positions.values()];
  const spans = ['x', 'y', 'z'].map(axis => Math.max(...points.map(p => p[axis])) - Math.min(...points.map(p => p[axis])));
  assert(Math.min(...spans) / Math.max(...spans) > 0.2, `thin layout: ${spans}`);
  // Non-zero tetrahedron volume proves positions are not merely a tilted plane.
  const origin = points[0];
  const v = points.slice(1).map(p => [p.x - origin.x, p.y - origin.y, p.z - origin.z]);
  const determinant = (a, b, c) => a[0] * (b[1] * c[2] - b[2] * c[1]) - a[1] * (b[0] * c[2] - b[2] * c[0]) + a[2] * (b[0] * c[1] - b[1] * c[0]);
  assert(v.some(a => v.some(b => v.some(c => Math.abs(determinant(a, b, c)) > 1))));
});

test('every descendant remains inside each ancestor family reservation', () => {
  const layout = layoutGraph(nodes);
  const graph = projectGraph(nodes, edges);
  for (const node of nodes) {
    let id = node.parent_id;
    const p = layout.positions.get(node.id);
    while (id) {
      const { center, size } = layout.bounds.get(id);
      for (const [axis, value] of [p.x, p.y, p.z].entries()) assert(Math.abs(value - center[axis]) <= size[axis] / 2, `${node.id} outside ${id}`);
      id = graph.parent.get(id);
    }
  }
});

test('empty graphs and missing parents work; malformed membership fails explicitly', () => {
  assert.equal(layoutGraph([]).positions.size, 0);
  assert.equal(projectGraph([{ id: 'orphan', type: 'task', parent_id: 'absent' }], []).nodes.length, 1);
  assert.throws(() => projectGraph([{ id: 'x', type: 'epic', parent_id: 'y' }, { id: 'y', type: 'epic', parent_id: 'x' }], []), /cycle/);
});
