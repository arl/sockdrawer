package main

// This file defines the cluster graph.

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type cluster struct {
	id          int
	importPath  string // declared name, e.g. "runtime/internal/core"
	name        string // short import name, e.g. "_core"
	nodes       map[*node]bool
	scope       map[string]*node       // maps package-level names to decls
	outputFiles map[string]*outputFile // output file data, keyed by file base name
}

func (c *cluster) finish() {
	// mark applies n's cluster to all nodes reachable from it that
	// don't have a cluster assignment yet.
	var mark func(n *node)
	mark = func(n *node) {
		for s := range n.succs {
			if s.cluster == nil {
				s.cluster = n.cluster
				n.cluster.nodes[s] = true
				if debug {
					fmt.Printf("\t%-50s (indirect)\n", s)
				}
				mark(s)
			}
		}
	}

	var first, prev *node
	for n := range c.nodes {
		if first == nil {
			first = n
		}
		if prev != nil {
			mark(n)
		}
		prev = n
	}
	if prev != nil {
		mark(first)
	}

	c.outputFiles = make(map[string]*outputFile)
}

func loadClusterFile(filename string, nodes []*node) ([]*cluster, error) {
	clusterNames := map[string]bool{"residue": true}

	byName := make(map[string]*node)
	for _, n := range nodes {
		byName[n.name] = n
	}

	f, err := os.Open(*clusterFile)
	if err != nil {
		return nil, err
	}
	in := bufio.NewScanner(f)
	var linenum int
	var c *cluster
	var clusters []*cluster
	for in.Scan() {
		linenum++
		line := strings.TrimSpace(in.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i]) // strip comments
		}
		if line == "" {
			continue // skip blanks
		}
		if strings.HasPrefix(line, "= ") {
			if c != nil {
				c.finish()
			}

			c = &cluster{
				id:         len(clusters),
				importPath: line[2:],
				nodes:      make(map[*node]bool),
			}
			if clusterNames[c.importPath] {
				fmt.Fprintf(os.Stderr,
					"%s:%d: warning: duplicate cluster name: %s; ignoring\n",
					*clusterFile, linenum, c.importPath)
				continue
			}
			clusters = append(clusters, c)
			if debug {
				fmt.Printf("\n# cluster %s\n", c.importPath)
			}
			continue
		}
		if c == nil {
			fmt.Fprintf(os.Stderr,
				"%s:%d: warning: node before '= cluster' marker; ignoring\n",
				*clusterFile, linenum)
			continue
		}

		n := byName[line]
		if n == nil {
			fmt.Fprintf(os.Stderr,
				"%s:%d: warning: can't find node %q; ignoring\n",
				*clusterFile, linenum, line)
		} else if n.cluster != nil {
			fmt.Fprintf(os.Stderr,
				"%s:%d: warning: node %q appears in clusters %q and %q; ignoring\n",
				*clusterFile, linenum, line, n.cluster.importPath, c.importPath)
		} else {
			n.cluster = c
			if debug {
				fmt.Printf("\t%s\n", n)
			}
			c.nodes[n] = true
		}
	}
	if c != nil {
		c.finish()
	}

	f.Close()
	if err := in.Err(); err != nil {
		return nil, err
	}

	return clusters, nil
}

func addResidualCluster(nodes []*node, clusters []*cluster) []*cluster {
	// The final cluster, residue, includes all other nodes.
	c := &cluster{
		id:         len(clusters),
		importPath: "residue",
		nodes:      make(map[*node]bool),
	}
	if debug {
		fmt.Printf("\n# cluster %s\n", c.importPath)
	}
	for _, n := range nodes {
		if n.cluster == nil {
			n.cluster = c
			if debug {
				fmt.Printf("\t%-50s\n", n)
			}
			c.nodes[n] = true
		}
	}
	c.finish()
	if len(c.nodes) > 0 {
		clusters = append(clusters, c)
	}
	return clusters
}
