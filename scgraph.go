package main

// This file defines the strong-component graph.
// (It is used only to simplify the renderings.)

import (
	"bytes"
	"fmt"
	"os"
	"sort"
)

// An scnode is a node in the scnode graph.
// It is (approximately; see -fuse) an SCC of the node graph.
type scnode struct {
	id           int              // unique id
	nodes        map[*node]bool   // elements of this SCC
	succs, preds map[*scnode]bool // scnode graph adjacency sets
	cluster      *cluster         // the cluster to which this SCC belongs
}

const maxLines = 8 // maximum number of lines in a label

func (s *scnode) String() string {
	var buf bytes.Buffer
	// Order nodes by exportedness and in-degree.
	order := make([]*node, 0, len(s.nodes))
	for n := range s.nodes {
		order = append(order, n)
	}
	sort.Sort(byExportednessAndInDegree(order))
	for i, n := range order {
		if i > 0 {
			buf.WriteByte('\n')
		}
		if i == maxLines-1 && len(order) > maxLines {
			fmt.Fprintf(&buf, "+ %d more", len(order)-i)
			break
		}
		buf.WriteString(n.String())
	}
	return buf.String()
}

type byExportednessAndInDegree []*node

func (b byExportednessAndInDegree) Len() int { return len(b) }
func (b byExportednessAndInDegree) Less(i, j int) bool {
	if r := b[i].exportedness() - b[j].exportedness(); r != 0 {
		return r > 0
	}
	if r := len(b[i].preds) - len(b[j].preds); r != 0 {
		return r > 0
	}
	return false
}
func (b byExportednessAndInDegree) Swap(i, j int) { b[i], b[j] = b[j], b[i] }

func (o *organizer) makeSCGraph(fuse bool) map[*scnode]bool {
	// Kosaraju's algorithm---Tarjan is overkill here.

	// Forward pass.
	S := make([]*node, 0, len(o.nodes)) // postorder stack
	seen := make(map[*node]bool)
	var visit func(n *node)
	visit = func(n *node) {
		if !seen[n] {
			seen[n] = true
			for s := range n.succs {
				visit(s)
			}
			S = append(S, n)
		}
	}

	for _, n := range o.nodes {
		visit(n)
	}

	// Reverse pass.
	var current *scnode
	seen = make(map[*node]bool)
	var rvisit func(d *node)
	rvisit = func(d *node) {
		if !seen[d] {
			seen[d] = true
			current.nodes[d] = true
			d.scc = current
			for p := range d.preds {
				rvisit(p)
			}
		}
	}
	scnodes := make(map[*scnode]bool)
	for len(S) > 0 {
		top := S[len(S)-1]
		S = S[:len(S)-1] // pop
		if !seen[top] {
			current = &scnode{
				id:      len(scnodes),
				cluster: top.cluster,
				nodes:   make(map[*node]bool),
				succs:   make(map[*scnode]bool),
				preds:   make(map[*scnode]bool),
			}
			rvisit(top)
			scnodes[current] = true
		}
	}

	// Build the strong-component DAG by
	// projecting the edges of the node graph,
	// discarding self-edges.
	for s := range scnodes {
		for n := range s.nodes {
			for pred := range n.preds {
				if s != pred.scc {
					s.preds[pred.scc] = true
				}
			}
			for succ := range n.succs {
				if s != succ.scc {
					s.succs[succ.scc] = true
				}
			}
		}
	}

	if debug {
		fmt.Fprintf(os.Stderr, "\t%d SCCs\n", len(scnodes))
	}

	// TODO(adonovan): do we still need this?
	if fuse {
		// Now fold each single-predecessor scnode into that predecessor.
		// Iterate until a fixed point is reached.
		//
		// Example:  a -> b -> c
		//                b -> d
		// Becomes:  ab -> c
		//           ab -> d
		// Then:     abcd
		//
		// Since the loop conserves predecessor count for all
		// non-deleted scnodes, the algorithm is order-invariant.
		for {
			var changed bool
			for b := range scnodes {
				if b == nil || len(b.preds) != 1 {
					continue
				}
				var a *scnode
				for a = range b.preds {
				}
				// a is sole predecessor of b
				if a.cluster != b.cluster {
					// don't fuse SCCs belonging to different clusters!
					continue
				}

				changed = true

				b.preds = nil
				delete(a.succs, b)

				// a gets all b's nodes
				for n := range b.nodes {
					a.nodes[n] = true
					n.scc = a
				}
				b.nodes = nil

				// a gets all b's succs
				for c := range b.succs {
					a.succs[c] = true
					c.preds[a] = true
					delete(c.preds, b)
				}
				b.succs = nil

				delete(scnodes, b)
			}
			if !changed {
				break
			}
		}

		if debug {
			fmt.Fprintf(os.Stderr, "\t%d SCCs (excluding single-predecessor ones)\n",
				len(scnodes))
		}
	}

	return scnodes
}
