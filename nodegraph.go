package main

// This file defines node and constructs the node graph.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// A node represents a top-level declaration (including methods).
// An entire const declaration is a single node.
// An entire var or type "spec" is a single node.
//
// Examples:
// 	func f()			// FuncDecl node
//	func (T) f() {...}		// FuncDecl node (method)
//	func init() {...} 		// FuncDecl node (no types.Object)
//	type (
//		T int			// TypeSpec node
//		U int   		// TypeSpec node
//	)
//	type T int			// TypeDecl node
//	const ( a, b = 0, 1; c = 0 )	// GenDecl(CONST) node (multiple objects)
//	var x = f()			// GenDecl(VAR) node
// 	var x, y = f()   		// GenDecl(VAR) node (multiple objects)
// 	var _ T = C(0)			// GenDecl(VAR) node (no object)
//
type node struct {
	o            *organizer
	id           int                         // zero-based ordinal, lexical order
	name         string                      // unique name, as used in clusters file
	syntax       ast.Node                    // ast.Decl, or ast.Spec if var/type in group
	uses         map[*ast.Ident]types.Object // uses of pkg- and file-scope objects
	objects      []types.Object              // declared objects in lexical order; blanks omitted
	recv         types.Type                  // receiver  type, iff concrete method decl
	succs, preds map[*node]bool              // node graph adjacency sets
	scc          *scnode                     // SCC to which this node belongs
	cluster      *cluster                    // cluster to which this node belongs

	// renaming state:
	mustExport bool                 // node must be exported to other clusters
	imports    map[interface{}]bool // existing (*PkgName) and new (*cluster) dependencies
	text       []byte               // text, after renaming
}

func (n *node) String() string {
	var buf bytes.Buffer
	buf.WriteString(n.name)
	if nobj := len(n.objects); nobj > 1 {
		fmt.Fprintf(&buf, " + %d", nobj-1)
	}
	return buf.String()
}

func (n *node) godocURL() string {
	posn := n.o.fset.Position(n.syntax.Pos())
	i := strings.Index(posn.Filename, "/src/") // TODO(adonovan): fix hack

	selLen := 1
	switch syntax := n.syntax.(type) {
	case *ast.FuncDecl:
		selLen = len("func")
	case *ast.GenDecl:
		switch syntax.Tok {
		case token.CONST:
			selLen = len("const")
		case token.VAR:
			selLen = len("var")
		case token.TYPE:
			selLen = len("type")
		}
	case *ast.TypeSpec:
		// For "type (...; x T; ...)", select "x".
		selLen = len(syntax.Name.Name)
	case *ast.ValueSpec:
		// For "var (...; x, y = ...)", select "x, y".
		selLen = int(syntax.Names[len(syntax.Names)-1].End() - syntax.Names[0].Pos())
	}
	return fmt.Sprintf("%s/%s?s=%d:%d#L%d", *godoc,
		posn.Filename[i+1:], posn.Offset, posn.Offset+selLen, posn.Line)
}

func (n *node) exportedness() int {
	for _, obj := range n.objects {
		if obj.Exported() {
			return 1
		}
	}
	return 0
}

func addEdge(from, to *node) {
	if from == to {
		return // skip self-edges
	}
	from.succs[to] = true
	to.preds[from] = true
}

func (o *organizer) buildNodeGraph() {
	if debug {
		fmt.Fprintf(os.Stderr, "\n\n\n==== %s ====\n\n\n", o.info.Pkg.Path())
	}

	// -- Pass 1: Defs ----------------------------------------------------

	for _, f := range o.info.Files {
		// These two vars are used for generation symbol names:
		// e.g. "func$alg.3", for the third init function in runtime/alg.go
		base := strings.TrimSuffix(filepath.Base(o.fset.Position(f.Pos()).Filename), ".go")
		var seq int

		forEachDecl(f, func(syntax ast.Node, parent *ast.GenDecl) {
			n := &node{
				o:      o,
				id:     len(o.nodes),
				syntax: syntax,
				uses:   make(map[*ast.Ident]types.Object),
				succs:  make(map[*node]bool),
				preds:  make(map[*node]bool),
			}

			// Visit the top-level AST, associating with n
			// every object declared within it that could
			// possibly be references outside it, including:
			// - package-level objects (const/func/var/type)
			// - concrete methods
			// - struct fields (consider y in "var x struct{y int}")
			// - abstract methods (consider y in "var x interface{y()}")
			ast.Inspect(syntax, func(syntax ast.Node) bool {
				if id, ok := syntax.(*ast.Ident); ok {
					// Definition of package-level object,
					// or struct field or interface method?
					if obj := o.info.Info.Defs[id]; obj != nil {
						if isPackageLevel(obj) {
							// package-level object
							n.objects = append(n.objects, obj)
						} else if v, ok := obj.(*types.Var); ok && v.IsField() {
							// struct field
						} else if _, ok := obj.(*types.Func); ok {
							// method or init function
							recv := methodRecv(obj)
							if recv != nil && !isInterface(methodRecv(obj)) {
								// concrete method
								n.recv = recv
								n.objects = append(n.objects, obj)
							}
						} else {
							return true // ignore
						}
						o.nodesByObj[obj] = n
					}
				}
				return true
			})

			// Name the node.
			if n.objects != nil {
				// Only the first object (in lexical order) of a group
				// (e.g. a const decl) is used for the node label.
				n.name = n.objects[0].Name()

				// concrete method decl?
				if n.recv != nil {
					// TODO(arl) old code, doesn't compile
					//  n.name = fmt.Sprintf("(%s).%s",
					// 	 types.TypeString(o.info.Pkg, n.recv), n.name)
					n.name = fmt.Sprintf("(%s).%s", n.recv, n.name)
				}
			} else {
				// e.g. blank identifier, or func init.
				seq++
				n.name = defaultName(syntax, base, seq)
			}

			o.nodes = append(o.nodes, n)
		})
	}

	// -- Pass 2: Refs ----------------------------------------------------

	// Gather references from this syntax tree to other
	// top-level trees, and create graph edges for them.
	// (Also gather refs to existing import names in 'uses'.)
	for _, n := range o.nodes {
		ast.Inspect(n.syntax, func(syntax ast.Node) bool {
			if id, ok := syntax.(*ast.Ident); ok {
				if obj, ok := o.info.Info.Uses[id]; ok {
					if n2, ok := o.nodesByObj[obj]; ok {
						addEdge(n, n2)
						n.uses[id] = obj
					} else if _, ok := obj.(*types.PkgName); ok {
						n.uses[id] = obj
					}
				}
			}
			return true
		})

		// To ensure methods and receiver types stay together,
		// we add edges to each method from its receiver type.
		if n.recv != nil {
			addEdge(o.nodesByObj[recvTypeName(n.recv)], n)
		}
	}

	if debug {
		fmt.Fprintf(os.Stderr, "\t%d nodes\n", len(o.nodes))
	}
}

// -- util -------------------------------------------------------------

// defaultName invents a reasonably stable temporary name for syntax
// based on its kind and sequence number within its file.
func defaultName(syntax ast.Node, base string, seq int) string {
	// No object: func init, or blank identifier.
	var kind string
	switch syntax := syntax.(type) {
	case *ast.FuncDecl:
		// e.g. func init()
		kind = "func"
	case *ast.ValueSpec:
		// e.g. var ( _ int )
		kind = "var"
	case *ast.GenDecl:
		switch syntax.Tok {
		case token.CONST:
			kind = "const" // e.g. const _ int
		case token.VAR:
			kind = "var" // e.g. var _ int
		case token.TYPE:
			kind = "type" // e.g. type _ int
		}
	default:
		// can't happen?
		kind = reflect.TypeOf(syntax).String()
	}
	return fmt.Sprintf("%s$%s.%d", kind, base, seq)
}

// forEachDecl calls fn for each syntax tree (decl or spec) in the file
// that should have its own node.  If syntax is a VarSpec or TypeSpec in
// a group, parent is the enclosing decl.
func forEachDecl(file *ast.File, fn func(syntax ast.Node, parent *ast.GenDecl)) {
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			switch decl.Tok {
			case token.CONST:
				// treat decl as one node
				fn(decl, nil)

			case token.VAR, token.TYPE:
				if decl.Lparen != 0 {
					// group decl: each spec gets its own node
					for _, spec := range decl.Specs {
						fn(spec, decl)
					}
				} else {
					// singleton: one node for entire decl
					fn(decl, nil)
				}
			}

		case *ast.FuncDecl:
			// funcs (but not methods) get their own node
			fn(decl, nil)
		}
	}
}

func recvTypeName(T types.Type) *types.TypeName {
	if ptr, ok := T.(*types.Pointer); ok {
		T = ptr.Elem()
	}
	return T.(*types.Named).Obj()
}

// methodRecv returns the receiver type of obj,
// if it's a method, or nil otherwise.
// TODO(adonovan): move this to go/types.  It gets re-invented a lot.
func methodRecv(obj types.Object) types.Type {
	if obj, ok := obj.(*types.Func); ok {
		recv := obj.Type().(*types.Signature).Recv()
		if recv != nil {
			return recv.Type()
		}
	}
	return nil
}

// isInterface reports whether T's underlying type is an interface.
func isInterface(T types.Type) bool {
	_, ok := T.Underlying().(*types.Interface)
	return ok
}
