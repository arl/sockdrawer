package main

// This file defines the main control flow.

/*
 Usage examples:

 Display:
 % ./sockdrawer -clusters golang.org/x/tools/cmd/sockdrawer/runtime.clusters \
                -godoc http://adonovan.nyc.corp:4999 -fuse -graphdir=out runtime

 Refactor:
 % ./sockdrawer -clusters golang.org/x/tools/cmd/sockdrawer/runtime.clusters \
                -outdir=/tmp/src runtime
 % find /tmp/src/ -name \*.go -exec sed -i -e 's?//go:.*?//go-redacted?' {} \;
 % GOPATH=/tmp command go build -gcflags "-e" residue

*/

// TODO(adonovan): lots on the refactoring side; see refactor.go.

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/tools/go/loader"
)

const debug = false

var (
	clusterFile = flag.String("clusters", "", "File containing cluster annotations")
	print       = flag.Bool("print", false, "Print the partition to stdout")
	outdir      = flag.String("outdir", "", "enable package splitting, using this output directory")
	graphdir    = flag.String("graphdir", "", "enable graph rendering, using this output directory")
	fuse        = flag.Bool("fuse", false, "fuse each single-predecessor SCC with its sole predecessor; this reduces the complexity of the output graphs")
	godoc       = flag.String("godoc", "http://localhost:4999", "base URL for godoc server")
)

const Usage = `Usage: sockdrawer -clusters=file [flags...] <args>

sockdrawer is a tool for splitting a package into two or more subpackages.

Partition flags:
 -clusters=file		Load the cluster definitions from the specified file.

Display flags:
 -print                 Print the partition in text form to the standard output.
 -graphdir=dir		Render graphs of the proposed split to this directory.
 -godoc=url		In rendered graphs, emit links to godoc at this address.
 -fuse			Display each single-predecessor SCC fused to its sole predecessor.

Refactoring flags:
 -outdir=dir		Split the package into subpackages, writing them here.
` + loader.FromArgsUsage

func main() {
	flag.Parse()
	args := flag.Args()
	if err := doMain(args); err != nil {
		fmt.Fprintf(os.Stderr, "sockdrawer: %s\n", err)
		os.Exit(1)
	}
}

func doMain(args []string) error {
	conf := loader.Config{
		// SourceImports: true, // TODO(arl) not found in loader.Config
		ParserMode: parser.ParseComments,
	}

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, Usage)
		return nil
	}

	// Use the initial packages from the command line.
	// TODO(adonovan): support *_test.go files too.
	_, err := conf.FromArgs(args, false /*FIXME*/)
	if err != nil {
		return err
	}

	// Typecheck only the necessary function bodies.
	// TODO(adonovan): opt: type-check only the bodies of functions
	// with the initial packages.
	conf.TypeCheckFuncBodies = func(p string) bool { return true }

	// Load, parse and type-check the whole program.
	iprog, err := conf.Load()
	if err != nil {
		return err
	}

	// TODO(adonovan): fix: generalize to multiple packages, or at least,
	// one package plus its external test package.
	info := iprog.InitialPackages()[0]
	return sockdrawer(conf.Fset, info)
}

type organizer struct {
	fset       *token.FileSet
	info       *loader.PackageInfo
	nodes      []*node // nodes for top-level decls/specs, in lexical order
	nodesByObj map[types.Object]*node
}

func sockdrawer(fset *token.FileSet, info *loader.PackageInfo) error {
	o := organizer{
		fset:       fset,
		info:       info,
		nodesByObj: make(map[types.Object]*node),
	}

	// Using the AST and Ident-to-Object mapping,
	// build the dependency graph over package-level nodes.
	o.buildNodeGraph()

	// Load the clusters file, if any,
	// and compute the implied partition.
	var clusters []*cluster // topological order
	if f := *clusterFile; f != "" {
		var err error
		if clusters, err = loadClusterFile(f, o.nodes); err != nil {
			return err
		}
	}
	clusters = addResidualCluster(o.nodes, clusters)

	// Print the partition?
	if *print {
		// Use the same format as the clusters file.
		fmt.Printf("# Package: %q\n", info.Pkg.Path())
		fmt.Printf("# Initial cluster file: %q\n", *clusterFile)
		fmt.Printf("# %d nodes in %d clusters\n", len(o.nodes), len(clusters))
		fmt.Println()

		for _, c := range clusters {
			var ss []string
			for n := range c.nodes {
				posn := n.o.fset.Position(n.syntax.Pos())
				base := filepath.Base(posn.Filename)
				// Comment out concrete method nodes since they can't be
				// specified in cluster file syntax.
				// (They're tied to their receiver type's cluster anyway.)
				var comment string
				if n.recv != nil {
					comment = "# "
				}
				ss = append(ss, fmt.Sprintf("%s%-40s# %s:%d", comment, n.name, base, posn.Line))
			}
			sort.Strings(ss)
			fmt.Printf("= %s\n", c.importPath)
			for _, s := range ss {
				fmt.Println(s)
			}
			fmt.Println()
		}
	}

	// Display partition graphically?
	if *graphdir != "" {
		// Compute the strong component graph to
		// simplify the displayed output.
		scgraph := o.makeSCGraph(*fuse)

		if err := renderGraphs(clusters, scgraph); err != nil {
			return err
		}
	}

	// Do the refactoring?
	if *outdir != "" {
		if err := o.refactor(clusters); err != nil {
			return err
		}
	}

	return nil
}
