package main

// This file emits renderings of all three levels of graphs as SVG files.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func renderGraphs(clusters []*cluster, scgraph map[*scnode]bool) error {
	fmt.Fprintln(os.Stderr, "Rendering graphs")
	if err := os.MkdirAll(*graphdir, 0755); err != nil {
		return err
	}

	// Write the graph of clusters.
	base := "clusters"
	if err := writeClusters(base+".dot", clusters); err != nil {
		return err
	}
	if err := runDot(base+".dot", base+".svg"); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nRun:\n\t%% browser %s\n",
		filepath.Join(*graphdir, base+".svg"))

	return nil
}

// writeClusters writes to dotfile the graph (DAG) of clusters.
// It also generates all subgraphs.
func writeClusters(dotfile string, clusters []*cluster) (err error) {
	f, err := os.Create(filepath.Join(*graphdir, dotfile))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()

	fmt.Fprintln(f, "digraph clusters {")
	fmt.Fprintln(f, `  node [shape="box",style="rounded,filled",fillcolor="#e0ffe0"];`)
	fmt.Fprintln(f, `  edge [arrowhead="open"];`)
	fmt.Fprintln(f, `  labelloc="t"; label="All clusters\n\n";`)
	for _, c := range clusters {
		base := fmt.Sprintf("cluster%d", c.id)

		// nodes
		// NB: %q is not quite the graphviz quoting function.
		fmt.Fprintf(f, "  n%d [URL=%q,label=%q];\n", c.id, base+".svg",
			strings.Replace(c.importPath, "/", "/\n", -1))

		// Find scnodes of nodes of this cluster.
		scnodes := make(map[*scnode]bool)
		for n := range c.nodes {
			scnodes[n.scc] = true
		}

		// Project edges from SCC graph onto clusters.
		succs := make(map[*cluster]bool)
		for s := range scnodes {
			for succ := range s.succs {
				if succ.cluster != c {
					succs[succ.cluster] = true
				}
			}
		}

		// edges
		for succ := range succs {
			fmt.Fprintf(f, "  n%d -> n%d;\n", c.id, succ.id)
		}

		if err := writeSCCs(c.importPath, base+".dot", scnodes); err != nil {
			return err
		}
		if err := runDot(base+".dot", base+".svg"); err != nil {
			return err
		}
	}
	fmt.Fprintln(f, "}")
	return nil
}

// writeSCCs writes to dotfile the graph (DAG) of SCCs for a single cluster.
// It also generates all subgraphs.
func writeSCCs(name, dotfile string, scgraph map[*scnode]bool) (err error) {
	f, err := os.Create(filepath.Join(*graphdir, dotfile))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()

	fmt.Fprintln(f, "digraph scgraph {")
	fmt.Fprintln(f, `  graph [rankdir=LR];`)
	fmt.Fprintln(f, `  edge [arrowhead="open"];`)
	fmt.Fprintf(f, `  labelloc="t"; label="Cluster: %s\n\n";`, name)
	fmt.Fprintln(f, `  node [shape="box",style=filled];`)
	for s := range scgraph {
		// nodes
		var url, color string
		if len(s.nodes) == 1 {
			for n := range s.nodes {
				url = n.godocURL()
			}
			color = "#f0e0ff"
		} else {
			base := fmt.Sprintf("scc%d", s.id)
			if err := writeNodes(base+".dot", s.String(), s.nodes); err != nil {
				return err
			}
			if err := runDot(base+".dot", base+".svg"); err != nil {
				return err
			}

			url = base + ".svg"
			color = "#e0f0ff"
		}
		// NB: %q is not quite the graphviz quoting function.
		fmt.Fprintf(f, "  n%d [fillcolor=%q,URL=%q,label=%q];\n", s.id, color, url, s.String())

		// intra-cluster edges
		for succ := range s.succs {
			if succ.cluster == s.cluster {
				fmt.Fprintf(f, "  n%d -> n%d;\n", s.id, succ.id)
			} else {
				// TODO(adonovan): show inter-cluster edges?
				// Probably too much.
			}
		}
	}
	fmt.Fprintln(f, "}")
	return nil
}

// writeNodes writes to dotfile the graph (strongly connected) of nodes
// (package-level named entities) for a single non-trivial SCC.
func writeNodes(dotfile, name string, graph map[*node]bool) (err error) {
	f, err := os.Create(filepath.Join(*graphdir, dotfile))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()

	// TODO(adonovan): use hash-value numbering to merge nodes of
	// equivalent topology (same set of succs/preds).

	fmt.Fprintln(f, "digraph scgraph {")
	fmt.Fprintln(f, `  edge [arrowhead="open"];`)
	fmt.Fprintf(f, `  labelloc="t"; label="Strongly connected component: %s\n\n";`, name)
	fmt.Fprintln(f, `  node [shape="box",style=filled,fillcolor="#f0e0ff"];`)

	for n := range graph {
		// nodes
		// NB: %q is not quite the graphviz quoting function.
		fmt.Fprintf(f, "  n%d [URL=%q,label=%q];\n", n.id, n.godocURL(), n.String())

		// TODO(adonovan): display two edges a-->b and b-->a as
		// a single double-headed one.

		// SCC-internal edges (ignoring synthetic edges from annotations)
		for succ, real := range n.succs {
			if real && succ.scc.id == n.scc.id {
				fmt.Fprintf(f, "  n%d -> n%d;\n", n.id, succ.id)
			}
		}
	}
	fmt.Fprintln(f, "}")
	return nil
}

func runDot(dotfile, svgfile string) error {
	cmd := exec.Command("/bin/sh", "-c", "/usr/bin/dot -Tsvg "+filepath.Join(*graphdir, dotfile)+" >"+filepath.Join(*graphdir, svgfile))
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
