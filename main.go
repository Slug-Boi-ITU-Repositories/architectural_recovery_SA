package main

import (
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
	"strings"
)

type Package struct {
	ImportPath string   `json:"ImportPath"`
	Name       string   `json:"Name"`
	Module     *Module  `json:"Module"`
	Imports    []string `json:"Imports"`
	Standard   bool     `json:"Standard"`
	Dir        string   `json:"Dir"`
	GoFiles    []string `json:"GoFiles"`
}

type Module struct {
	Path string `json:"Path"`
}

type Node struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // "internal", "external", "stdlib"
	Module   string `json:"module"`
	FullPath string `json:"fullPath"`
	Methods  int    `json:"methods"`
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
	noStdlib := flag.Bool("no-stdlib", false, "exclude standard library packages")
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

	graph := buildGraph(pkgs, modulePath, *noStdlib, *maxDepth)
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

// listPackages returns a map of ImportPath -> *Package for easy mutation.
func listPackages(dir string) (map[string]*Package, error) {
	cmd := exec.Command("go", "list", "-json", "-deps", "./...")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
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

func buildGraph(pkgs map[string]*Package, modulePath string, noStdlib bool, maxDepth int) Graph {
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

		kind := "external"
		if p != nil && p.Standard {
			kind = "stdlib"
		} else if strings.HasPrefix(path, modulePath) {
			kind = "internal"
		}

		mod := ""
		if p != nil && p.Module != nil {
			mod = p.Module.Path
		} else if kind == "external" {
			parts := strings.Split(path, "/")
			if len(parts) >= 3 && strings.Contains(parts[0], ".") {
				mod = strings.Join(parts[:3], "/")
			} else {
				mod = parts[0]
			}
		}

		label := path
		if kind == "internal" {
			label = strings.TrimPrefix(path, modulePath+"/")
			if label == modulePath {
				label = "(root)"
			}
		} else {
			parts := strings.Split(path, "/")
			label = parts[len(parts)-1]
		}

		nodes = append(nodes, Node{
			ID:       path,
			Label:    label,
			Kind:     kind,
			Module:   mod,
			FullPath: path,
			Methods:  countExported(p),
		})
	}

	for path, p := range pkgs {
		if !included[path] {
			continue
		}
		if noStdlib && p.Standard {
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
			if noStdlib && dep.Standard {
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
