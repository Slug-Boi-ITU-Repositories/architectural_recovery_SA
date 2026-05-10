package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Package struct {
	ImportPath string   `json:"ImportPath"`
	Name       string   `json:"Name"`
	Module     *Module  `json:"Module"`
	Imports    []string `json:"Imports"`
	Dir        string   `json:"Dir"`
	GoFiles    []string `json:"GoFiles"`
}

type Module struct {
	Path string `json:"Path"`
}

type Node struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Module   string `json:"module"`
	FullPath string `json:"fullPath"`
	Methods  int    `json:"methods"`
	Churn    int    `json:"churn"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

func main() {
	dir := flag.String("dir", ".", "path to Go module root")
	out := flag.String("out", "depgraph.html", "output HTML file")
	maxDepth := flag.Int("depth", 0, "max import depth (0 = unlimited)")
	churnFile := flag.String("churn", "", "path to churn CSV file (filename,commit_count)")
	flag.Parse()

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fatalf("resolving path: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Scanning %s ...\n", absDir)

	modulePath, err := getModulePath(absDir)
	if err != nil {
		fatalf("reading module: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Module: %s\n", modulePath)

	pkgs, err := listPackages(absDir)
	if err != nil {
		fatalf("listing packages: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Found %d packages\n", len(pkgs))

	churn := map[string]int{}
	if *churnFile != "" {
		var err error
		churn, err = loadChurn(*churnFile)
		if err != nil {
			fatalf("loading churn: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Loaded churn for %d files\n", len(churn))
	}

	// --- Modify pkgs here before the graph is built ---
	// Remove a specific package:   delete(pkgs, "github.com/foo/bar/internal/secret")
	// Remove all test packages:    for k, p := range pkgs { if strings.Contains(p.Name, "test") { delete(pkgs, k) } }
	// Remove a whole subtree:      for k := range pkgs { if strings.HasPrefix(k, "github.com/foo/bar/cmd") { delete(pkgs, k) } }
	// Rename a label:              pkgs["github.com/foo/bar"].Name = "myapp"

	for k, p := range pkgs {
		if strings.Contains(p.Name, "test") {
			delete(pkgs, k)
		} else if strings.Contains(p.ImportPath, "test") {
			delete(pkgs, k)
		}
	}

	graph := buildGraph(pkgs, modulePath, *maxDepth, churn)
	fmt.Fprintf(os.Stderr, "Graph: %d nodes, %d edges\n", len(graph.Nodes), len(graph.Edges))

	if err := renderHTML(graph, modulePath, *out); err != nil {
		fatalf("rendering HTML: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Written to %s\n", *out)
}

func getModulePath(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-m")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// listPackages returns a map of ImportPath -> *Package for module packages only.
// Only direct imports within the module are used as edges.
func listPackages(dir string) (map[string]*Package, error) {
	return runGoList(dir, "./...")
}

func runGoList(dir string, args ...string) (map[string]*Package, error) {
	cmdArgs := append([]string{"list", "-json"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %v: %w", args, err)
	}
	pkgs := make(map[string]*Package)
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p Package
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decoding package: %w", err)
		}
		pkgs[p.ImportPath] = &p
	}
	return pkgs, nil
}

func buildGraph(pkgs map[string]*Package, modulePath string, maxDepth int, churn map[string]int) Graph {
	// Collect reachable packages up to maxDepth using BFS
	included := make(map[string]bool)
	if maxDepth > 0 {
		type entry struct {
			path  string
			depth int
		}
		var queue []entry
		for path := range pkgs {
			if strings.HasPrefix(path, modulePath) {
				queue = append(queue, entry{path, 0})
				included[path] = true
			}
		}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			p, ok := pkgs[cur.path]
			if !ok || cur.depth >= maxDepth {
				continue
			}
			for _, imp := range p.Imports {
				if !included[imp] {
					included[imp] = true
					queue = append(queue, entry{imp, cur.depth + 1})
				}
			}
		}
	} else {
		for path := range pkgs {
			included[path] = true
		}
	}

	nodeSet := make(map[string]bool)
	var nodes []Node
	var edges []Edge

	addNode := func(path string, p *Package) {
		if nodeSet[path] {
			return
		}
		nodeSet[path] = true

		label := strings.TrimPrefix(path, modulePath+"/")
		label = strings.TrimPrefix(label, "pkg/")
		if label == modulePath {
			label = "(root)"
		}

		// Sum churn across all .go files in this package
		pkgChurn := 0
		if p != nil {
			for _, f := range p.GoFiles {
				pkgChurn += churn[filepath.Base(f)]
			}
		}

		nodes = append(nodes, Node{
			ID:       path,
			Label:    label,
			Kind:     "internal",
			Module:   modulePath,
			FullPath: path,
			Methods:  countExported(p),
			Churn:    pkgChurn,
		})
	}

	for path, p := range pkgs {
		if !included[path] {
			continue
		}
		addNode(path, p)

		for _, imp := range p.Imports {
			if !included[imp] {
				continue
			}
			dep, ok := pkgs[imp]
			if !ok {
				continue
			}
			addNode(imp, dep)
			edges = append(edges, Edge{Source: path, Target: imp})
		}
	}

	return Graph{Nodes: nodes, Edges: edges}
}

func renderHTML(g Graph, modulePath, outPath string) error {
	nodesJSON, _ := json.Marshal(g.Nodes)
	edgesJSON, _ := json.Marshal(g.Edges)

	data := map[string]interface{}{
		"Module": modulePath,
		"Nodes":  template.JS(nodesJSON),
		"Edges":  template.JS(edgesJSON),
	}

	tmpl, err := template.New("depgraph").Parse(htmlTemplate)
	if err != nil {
		return err
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return tmpl.Execute(f, data)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// loadChurn reads a CSV of (filename, commit_count) and returns a map of
// base filename -> total commits. Filenames may include paths; only the
// base name is used as the key so it can match against GoFiles entries.
func loadChurn(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comment = '#'
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}

	churn := make(map[string]int)
	for _, rec := range records {
		if len(rec) < 2 {
			continue
		}
		filename := filepath.Base(strings.TrimSpace(rec[0]))
		if filename == "filename" {
			continue // header row
		}
		count, err := strconv.Atoi(strings.TrimSpace(rec[1]))
		if err != nil {
			continue
		}
		churn[filename] += count
	}
	return churn, nil
}

// countExported counts exported functions and methods in a package's Go source files.
func countExported(p *Package) int {
	if p == nil || p.Dir == "" {
		return 0
	}
	fset := token.NewFileSet()
	count := 0
	for _, name := range p.GoFiles {
		path := filepath.Join(p.Dir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.IsExported() {
				count++
			}
		}
	}
	return count
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Dependency Graph — {{.Module}}</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: system-ui, sans-serif; background: #0f1117; color: #e2e4ea; height: 100vh; display: flex; flex-direction: column; }
  #header { padding: 12px 20px; background: #181c26; border-bottom: 1px solid #2a2f3d; display: flex; align-items: center; gap: 16px; flex-shrink: 0; }
  #header h1 { font-size: 15px; font-weight: 500; color: #c8cad4; }
  #header code { font-size: 13px; color: #7c8fc9; background: #1e2436; padding: 2px 8px; border-radius: 4px; }
  #controls { display: flex; gap: 10px; align-items: center; margin-left: auto; flex-wrap: wrap; }
  #controls label { font-size: 12px; color: #8890a8; display: flex; align-items: center; gap: 6px; cursor: pointer; }
  #controls input[type=range] { width: 80px; }
  #legend { display: flex; gap: 12px; align-items: center; }
  .dot-internal { background: #7c6fe0; }
  #info { position: absolute; top: 60px; right: 16px; width: 280px; background: #181c26; border: 1px solid #2a2f3d; border-radius: 8px; padding: 14px; font-size: 12px; display: none; z-index: 10; }
  #info h2 { font-size: 13px; font-weight: 500; margin-bottom: 8px; color: #c8cad4; word-break: break-all; }
  #info p  { color: #8890a8; margin-bottom: 4px; line-height: 1.5; }
  #info strong { color: #c8cad4; }
  #stats { font-size: 12px; color: #8890a8; }
  svg { flex: 1; }
  .node circle { cursor: pointer; stroke-width: 1.5; transition: r 0.15s; }
  .node circle:hover { stroke-width: 2.5; }
  .node text { pointer-events: none; font-size: 11px; fill: #c8cad4; }
  .link { stroke-opacity: 0.35; stroke-width: 1; }
  .link.highlighted { stroke-opacity: 0.9; stroke-width: 2; }
  .node.dimmed circle { opacity: 0.15; }
  .node.dimmed text   { opacity: 0.1; }
  .node.text-hidden text { opacity: 0 !important; }
  .link.dimmed { stroke-opacity: 0.04; }
  #search { background: #1e2436; border: 1px solid #2a2f3d; color: #e2e4ea; padding: 4px 10px; border-radius: 4px; font-size: 12px; width: 160px; }
  #search::placeholder { color: #4a5068; }
  #filter-btn { background: #1e2436; border: 1px solid #2a2f3d; color: #8890a8; padding: 4px 10px; border-radius: 4px; font-size: 12px; cursor: pointer; white-space: nowrap; }
  #filter-btn:hover { border-color: #4a5068; color: #c8cad4; }
  #filter-panel { position: absolute; top: 52px; left: 16px; background: #181c26; border: 1px solid #2a2f3d; border-radius: 8px; padding: 12px 14px; z-index: 20; display: none; min-width: 220px; max-width: 320px; }
  #filter-panel h3 { font-size: 12px; font-weight: 500; color: #8890a8; margin-bottom: 10px; text-transform: uppercase; letter-spacing: 0.06em; }
  .prefix-group { margin-bottom: 10px; }
  .prefix-group-label { font-size: 11px; color: #4a5068; margin-bottom: 4px; }
  .prefix-pills { display: flex; flex-wrap: wrap; gap: 5px; }
  .pill { font-size: 11px; padding: 3px 9px; border-radius: 99px; border: 1px solid #2a2f3d; background: #1e2436; color: #8890a8; cursor: pointer; user-select: none; transition: background 0.1s, color 0.1s; }
  .pill.active { background: #2e2a4a; border-color: #7c6fe0; color: #c8cad4; }
  .pill:hover { border-color: #4a5068; color: #c8cad4; }
  #filter-clear { margin-top: 8px; font-size: 11px; color: #4a5068; cursor: pointer; }
  #filter-clear:hover { color: #8890a8; }
</style>
</head>
<body>
<div id="header">
  <h1>Dependency Graph</h1>
  <code>{{.Module}}</code>
  <div id="controls">
    <button id="filter-btn">&#9776; Filter packages</button>
    <input id="search" type="text" placeholder="Search packages…">
    <label>Depth <select id="depth-filter" style="background:#1e2436;border:1px solid #2a2f3d;color:#e2e4ea;padding:3px 6px;border-radius:4px;font-size:12px"><option value="0">all</option></select></label>
    <label>Edge weight <select id="size-by" style="background:#1e2436;border:1px solid #2a2f3d;color:#e2e4ea;padding:3px 6px;border-radius:4px;font-size:12px"><option value="out">imports</option><option value="in">imported by</option></select></label>
    <label>Link dist <input type="range" id="link-dist" min="40" max="300" value="120"></label>
    <span id="stats"></span>
  </div>
</div>
<div id="filter-panel">
  <h3>Filter by prefix</h3>
  <div id="prefix-list"></div>
  <div id="filter-clear">Clear all filters</div>
</div>
<div id="info">
  <h2 id="info-path"></h2>
  <p><strong>Kind:</strong> <span id="info-kind"></span></p>
  <p><strong>Module:</strong> <span id="info-module"></span></p>
  <p><strong>Imports:</strong> <span id="info-imports"></span> packages</p>
  <p><strong>Imported by:</strong> <span id="info-importedby"></span> packages</p>
  <p><strong>Exported funcs:</strong> <span id="info-methods"></span></p>
  <p><strong>Churn (commits):</strong> <span id="info-churn"></span></p>
</div>
<svg id="graph"></svg>
<script src="https://cdnjs.cloudflare.com/ajax/libs/d3/7.9.0/d3.min.js"></script>
<script>
const RAW_NODES = {{.Nodes}};
const RAW_EDGES = {{.Edges}};

const color = { internal: '#7c6fe0', external: '#e07c5f', stdlib: '#5f8ea0' };

let searchTerm = '';
let activeFilters = new Set();
let maxDepthFilter = 0;

// Populate depth dropdown from actual label depths in the data
(function() {
  const depths = new Set();
  RAW_NODES.forEach(n => {
    if (n.label === '(root)') { depths.add(0); return; }
    depths.add(n.label.split('/').length);
  });
  const sel = document.getElementById('depth-filter');
  [...depths].sort((a, b) => a - b).forEach(d => {
    if (d === 0) return; // already have "all"
    const opt = document.createElement('option');
    opt.value = d;
    opt.textContent = '<= ' + d;
    sel.appendChild(opt);
  });
})();

function filteredData() {
  const nodeSet = new Set();
  RAW_NODES.forEach(n => {
    if (searchTerm && !n.fullPath.toLowerCase().includes(searchTerm) && !n.label.toLowerCase().includes(searchTerm)) return;
    if (activeFilters.size > 0) {
      const match = [...activeFilters].some(p => n.fullPath.startsWith(p));
      if (!match) return;
    }
    if (maxDepthFilter > 0) {
      const depth = n.label === '(root)' ? 0 : n.label.split('/').length;
      if (depth > maxDepthFilter) return;
    }
    nodeSet.add(n.id);
  });
  const nodes = RAW_NODES.filter(n => nodeSet.has(n.id));
  const edges = RAW_EDGES
    .filter(e => nodeSet.has(e.source) && nodeSet.has(e.target))
    .map(e => ({ source: e.source, target: e.target }));
  return { nodes, edges };
}

const svgEl = document.getElementById('graph');
let sim, linkSel, nodeSel;

function buildAdj(nodes, edges) {
  const out = {}, inc = {};
  nodes.forEach(n => { out[n.id] = []; inc[n.id] = []; });
  edges.forEach(e => {
    const sid = typeof e.source === 'object' ? e.source.id : e.source;
    const tid = typeof e.target === 'object' ? e.target.id : e.target;
    if (out[sid]) out[sid].push(tid);
    if (inc[tid]) inc[tid].push(sid);
  });
  return { out, inc };
}

function nodeRadius(d) {
  const base = { internal: 5, external: 4, stdlib: 3 }[d.kind] || 4;
  return base + Math.sqrt(d.methods || 0) * 1.8;
}

function render() {
  const { nodes, edges } = filteredData();
  document.getElementById('stats').textContent = nodes.length + ' nodes · ' + edges.length + ' edges';

  const W = svgEl.clientWidth || 1200;
  const H = svgEl.clientHeight || 700;
  d3.select('#graph').selectAll('*').remove();

  const g = d3.select('#graph')
    .call(d3.zoom().on('zoom', e => container.attr('transform', e.transform)))
    .append('g');
  const container = g;

  d3.select('#graph').append('defs').append('marker')
    .attr('id', 'arrow').attr('viewBox', '0 0 10 10')
    .attr('refX', 14).attr('refY', 5)
    .attr('markerWidth', 5).attr('markerHeight', 5)
    .attr('orient', 'auto-start-reverse')
    .append('path').attr('d', 'M2 1L8 5L2 9').attr('fill', 'none')
    .attr('stroke', '#4a5068').attr('stroke-width', 1.5);

  const adj = buildAdj(nodes, edges);

  const sizeBy = document.getElementById('size-by').value;
  const maxDegree = Math.max(1, ...nodes.map(n =>
    sizeBy === 'in' ? (adj.inc[n.id] || []).length : (adj.out[n.id] || []).length
  ));
  edges.forEach(e => {
    const id = sizeBy === 'in' ? e.target : e.source;
    const degree = sizeBy === 'in' ? (adj.inc[id] || []).length : (adj.out[id] || []).length;
    e.heat = degree / maxDegree;
  });

  function edgeColor(heat) {
    const stops = [
      [0x4a, 0x50, 0x68],
      [0xc8, 0xb0, 0x30],
      [0xe0, 0x72, 0x20],
      [0xe0, 0x28, 0x28],
    ];
    const t = heat * (stops.length - 1);
    const i = Math.min(Math.floor(t), stops.length - 2);
    const f = t - i;
    const a = stops[i], b = stops[i + 1];
    const r = Math.round(a[0] + (b[0] - a[0]) * f);
    const g = Math.round(a[1] + (b[1] - a[1]) * f);
    const bl = Math.round(a[2] + (b[2] - a[2]) * f);
    return 'rgb(' + r + ',' + g + ',' + bl + ')';
  }

  const linkDist = +document.getElementById('link-dist').value;

  sim = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(edges).id(d => d.id).distance(linkDist).strength(0.4))
    .force('charge', d3.forceManyBody().strength(-180))
    .force('center', d3.forceCenter(W / 2, H / 2))
    .force('collision', d3.forceCollide(d => nodeRadius(d) + 2));

  linkSel = container.append('g').selectAll('line')
    .data(edges).join('line')
    .attr('class', 'link')
    .attr('stroke-width', 1.5)
    .attr('stroke', d => edgeColor(d.heat))
    .attr('marker-end', 'url(#arrow)');

  const nodeG = container.append('g').selectAll('g')
    .data(nodes).join('g')
    .attr('class', 'node')
    .call(d3.drag()
      .on('start', (e, d) => { if (!e.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
      .on('drag',  (e, d) => { d.fx = e.x; d.fy = e.y; })
      .on('end',   (e, d) => { if (!e.active) sim.alphaTarget(0); d.fx = null; d.fy = null; }))
    .on('click', (e, d) => showInfo(d, nodes, edges))
    .on('mouseover', (e, d) => highlight(d, nodes, edges, adj))
    .on('mouseout',  () => clearHighlight());

  const maxChurn = Math.max(1, ...nodes.map(n => n.churn || 0));

  nodeG.append('circle')
    .attr('r', d => d.churn ? nodeRadius(d) + (d.churn / maxChurn) * 10 : 0)
    .attr('fill', 'none')
    .attr('stroke', '#e0a020')
    .attr('stroke-width', 1.5)
    .attr('stroke-opacity', d => 0.2 + (d.churn / maxChurn) * 0.7);

  nodeG.append('circle')
    .attr('r', d => nodeRadius(d))
    .attr('fill', d => color[d.kind])
    .attr('stroke', d => d3.color(color[d.kind]).brighter(0.5));

  nodeG.append('text')
    .text(d => d.label)
    .attr('x', d => nodeRadius(d) + 4)
    .attr('y', 4)
    .style('font-size', d => d.kind === 'internal' ? '12px' : '10px')
    .style('opacity', d => d.kind === 'internal' ? 1 : 0.6);

  nodeSel = nodeG;
  sim.on('tick', () => {
    linkSel
      .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
    nodeG.attr('transform', d => 'translate(' + d.x + ',' + d.y + ')');
  });
}

function highlight(d, nodes, edges, adj) {
  const connected = new Set([d.id]);
  edges.forEach(e => {
    const sid = typeof e.source === 'object' ? e.source.id : e.source;
    const tid = typeof e.target === 'object' ? e.target.id : e.target;
    if (sid === d.id) connected.add(tid);
    if (tid === d.id) connected.add(sid);
  });
  nodeSel.classed('dimmed', n => !connected.has(n.id));
  nodeSel.classed('text-hidden', n => n.id !== d.id && !connected.has(n.id));
  linkSel.classed('dimmed', e => {
    const sid = typeof e.source === 'object' ? e.source.id : e.source;
    const tid = typeof e.target === 'object' ? e.target.id : e.target;
    return sid !== d.id && tid !== d.id;
  });
  linkSel.classed('highlighted', e => {
    const sid = typeof e.source === 'object' ? e.source.id : e.source;
    const tid = typeof e.target === 'object' ? e.target.id : e.target;
    return sid === d.id || tid === d.id;
  });
}

function clearHighlight() {
  if (!nodeSel) return;
  nodeSel.classed('dimmed', false).classed('text-hidden', false);
  linkSel.classed('dimmed', false).classed('highlighted', false);
}

function showInfo(d, nodes, edges) {
  const { out, inc } = buildAdj(nodes, edges);
  document.getElementById('info-path').textContent = d.fullPath;
  document.getElementById('info-kind').textContent = d.kind;
  document.getElementById('info-module').textContent = d.module || '(stdlib)';
  document.getElementById('info-imports').textContent = (out[d.id] || []).length;
  document.getElementById('info-importedby').textContent = (inc[d.id] || []).length;
  document.getElementById('info-methods').textContent = d.methods || 0;
  document.getElementById('info-churn').textContent = d.churn || 0;
  document.getElementById('info').style.display = 'block';
}

// --- Filter panel ---
function buildPrefixTree() {
  const groups = {};
  RAW_NODES.forEach(n => {
    if (n.kind !== 'internal') return;
    const parts = n.label === '(root)' ? [] : n.label.split('/');
    if (parts.length === 0) return;
    const top = parts[0];
    if (!groups[top]) groups[top] = new Set();
    if (parts.length > 1) groups[top].add(parts[0] + '/' + parts[1]);
  });
  return groups;
}

function getFullPrefix(relPrefix) {
  for (const n of RAW_NODES) {
    if (n.kind === 'internal' && (n.label === relPrefix || n.label.startsWith(relPrefix + '/'))) {
      const idx = n.fullPath.indexOf(relPrefix);
      if (idx !== -1) return n.fullPath.slice(0, idx) + relPrefix;
    }
  }
  return relPrefix;
}

function renderFilterPanel() {
  const groups = buildPrefixTree();
  const list = document.getElementById('prefix-list');
  list.innerHTML = '';
  Object.keys(groups).sort().forEach(top => {
    const fullTop = getFullPrefix(top);
    const group = document.createElement('div');
    group.className = 'prefix-group';
    const label = document.createElement('div');
    label.className = 'prefix-group-label';
    label.textContent = top;
    group.appendChild(label);
    const pills = document.createElement('div');
    pills.className = 'prefix-pills';

    const topPill = document.createElement('span');
    topPill.className = 'pill' + (activeFilters.has(fullTop) ? ' active' : '');
    topPill.textContent = top + '/…';
    topPill.dataset.prefix = fullTop;
    topPill.addEventListener('click', toggleFilter);
    pills.appendChild(topPill);

    [...groups[top]].sort().forEach(sub => {
      const fullSub = getFullPrefix(sub);
      const pill = document.createElement('span');
      pill.className = 'pill' + (activeFilters.has(fullSub) ? ' active' : '');
      pill.textContent = sub.split('/')[1] + '/…';
      pill.dataset.prefix = fullSub;
      pill.addEventListener('click', toggleFilter);
      pills.appendChild(pill);
    });

    group.appendChild(pills);
    list.appendChild(group);
  });
}

function toggleFilter(e) {
  const prefix = e.target.dataset.prefix;
  if (activeFilters.has(prefix)) {
    activeFilters.delete(prefix);
    e.target.classList.remove('active');
  } else {
    activeFilters.add(prefix);
    e.target.classList.add('active');
  }
  render();
}

const filterBtn = document.getElementById('filter-btn');
const filterPanel = document.getElementById('filter-panel');
filterBtn.addEventListener('click', e => {
  e.stopPropagation();
  const open = filterPanel.style.display === 'block';
  filterPanel.style.display = open ? 'none' : 'block';
  if (!open) renderFilterPanel();
});
document.addEventListener('click', e => {
  if (!filterPanel.contains(e.target) && e.target !== filterBtn) {
    filterPanel.style.display = 'none';
  }
});
document.getElementById('filter-clear').addEventListener('click', () => {
  activeFilters.clear();
  renderFilterPanel();
  render();
});
// ---

document.getElementById('depth-filter').addEventListener('change', e => { maxDepthFilter = +e.target.value; render(); });
document.getElementById('size-by').addEventListener('change', () => render());
document.getElementById('link-dist').addEventListener('input', () => {
  if (!sim) return;
  sim.force('link').distance(+document.getElementById('link-dist').value);
  sim.alpha(0.5).restart();
});
document.getElementById('search').addEventListener('input', e => { searchTerm = e.target.value.toLowerCase(); render(); });
document.getElementById('graph').addEventListener('click', e => {
  if (e.target === svgEl) document.getElementById('info').style.display = 'none';
});
window.addEventListener('resize', render);
render();
</script>
</body>
</html>`