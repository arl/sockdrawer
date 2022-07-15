/*
The sockdrawer command is an analysis and visualization tool to help
you reorganize a complex Go package into several simpler ones.

Overview

sockdrawer operates on three kinds of graphs at different levels of
abstraction.  The lowest level is the NODE GRAPH.  A node is a
package-level declaration of a named entity (func, var, const or type).

An entire constant declaration is treated as a single node, even if it
contains multiple "specs" each defining multiple names, since constants
so grouped are typically closely related; an important special case is
an enumerated set data type.  Also, we treat each "spec" of a var or
type declaration as a single node.

	func f()				// a func node
	const ( a, b = 0, 1; c = 0 )		// a single const node
	var (
		a, b = 0, 1			// a single var node
		c = 0				// another var node
	)
	type ( x int; y int )			// a single type node

Each reference to a package-level entity E forms an edge in the node
graph, from the node in which it appears to the node E.  For example:

	var x int
	var y = x 			// edge y -> x
	func f() int { return y } 	// edge f -> y

Each method declaration depends on its receiver named type; in addition
we add an edge from each receiver type to its methods:

	type T int			// edge T -> T.f
	func (T) f() 			// edge T.f -> T

to ensure that a type and its methods stay together.

The node graph is highly cyclic, and obviously all nodes in a cycle must
belong to the same package for the package import graph to remain
acyclic.

So, we compute the second graph, the SCNODE GRAPH.  In essence, the
scnode graph is the graph of strongly connected components (SCCs) of the
(ordinary) node graph.  By construction, the scnode graph is acyclic.

We optionally perform an optimization at this point, which is to fuse
single-predecessor scnodes with their sole predecessor, as this tends to
reduce clutter in big graphs.  This means that the scnodes are no longer
true SCCs; however, the scnode graph remains acyclic.

We define a valid PARTITION P of the scnode graph as a mapping from
scnodes to CLUSTERS such that the projection of the scnode graph using
mapping P is an acyclic graph.  This third graph is the CLUSTER GRAPH.

Every partition represents a valid refactoring of the original package
into hypothetical subpackages, each cluster being a subpackage.  Two
partitions define the extreme ends of a spectrum: the MINIMAL partition
maps every scnode to a single cluster; it represents the status quo, a
monolithic package.  The MAXIMAL partition maps each scnode to a unique
cluster; this breaks the package up into an impractically large number
of small fragments.  The ideal partition lies somewhere in between.


Clusters file

The --clusters=<file> argument specifies a CLUSTERS FILE that constrains
the partition algorithm.  The file consists of a number of stanzas, each
assigning an import path to a cluster ("mypkg/internal/util") and
assigning a set of initial nodes ({x, y, z}) to it:

	= mypkg/internal/util
	x
	y  # this is a comment
	z

Order of stanzas is important: clusters must be be declared bottom to
top.  After each stanza, all nodes transitively reachable (via the node
graph) from that cluster are assigned to that cluster, if they have not
yet been assigned to some other cluster.  Thus we need only mention the
root nodes of the cluster, not all its internal nodes.  A warning is
reported if a node mentioned in a stanza already belongs to a previously
defined cluster.

There is an implicit cluster, "residue", that holds all remaining nodes
after the clusters defined by the file have been processed.  Initially,
when the clusters file is empty, the residue cluster contains the entire
package.  (It is logically at the top.)  The task for the user is to
iteratively define new clusters until the residue becomes empty.


Visualization

When sockdrawer is run, it analyzes the source package, builds the node
graph and the scgraph, loads the clusters file, computes the clusters for
every node, and then emits SVG renderings of the three levels of graphs,
with nodes colors coded as follows:

	green = cluster  (candidate subpackage)
	pink  = scnode   (strong component of size > 1)
	blue  = node     (func/type/var/const decl)

The graphs of all clusters, a DAG, has green nodes; clicking one takes
you to the graph over scnodes for that cluster, also a DAG.  Each pink
node in this graph represents a cyclical bunch of the node graph,
collapsed together for ease of viewing.  Each blue node here represents a
singleton SCC, a single declaration; singular SCCs are replaced by
their sole element for simplicity.

Clicking a pink (plural) scnode shows the cyclical portion of the node
graph that it represents.  (If the fusion optimization was enabled, it
may not be fully cyclic.)  All of its nodes are blue.

Clicking a blue node shows the definition of that node in godoc.
(The godoc server's base URL is specified by the --godoc flag.)


Workflow

Initially, all nodes belong to the "residue" cluster.  (GraphViz graph
rendering can be slow for the first several iterations.  A large monitor
is essential.)

The sockdrawer user's task when decomposing a package into clusters is
to identify the lowest-hanging fruit (so to speak) in the residue
cluster---a coherent group of related scnodes at the bottom of the
graph---and to "snip off" a bunch at the "stem" by appending a new
stanza to the clusters file and listing the roots of that bunch in the
stanza, and then to re-run the tool.


Nodes may be added to an existing stanza if appropriate, but if they are
added to a cluster that is "too low", this may create conflicts; keep an
eye out for warnings.

This process continues iteratively until the residue has become empty
and the sets of clusters are satisfactory.

The tool prints the assignments of nodes to clusters: the "shopping
list" for the refactoring work.  Clusters should be split off into
subpackages in dependency order, lowest first.


Caveats

The analysis chooses a single configuration, such as linux/amd64.
Declarations for other configurations (e.g. windows/arm) will be absent
from the node graph.

There may be some excessively large SCCs in the node graph that reflect
a circularity in the design.  For the purposes of analysis, you can
break them arbitrarily by commenting out some code, though more thought
will be required for a principled fix (e.g. dependency injection).


TODO

- Document the refactoring.
- Make pretty and stable names for anonymous nodes such as:
 	func init() {...}
	var _ int = ...
  Currently their names are very sensitive to lexical perturbations.
- Infer more constraints from co-located declarations.  Most of the stuff
  in the runtime's residue could be disposed of this way.
- Analyze the package's *_test.go files too.  If they define an external
  test package, we'll have to deal with two packages at once.
- Write tests.

*/
package main
