package main

// This file defines the refactoring.

// TODO(adonovan): fix:
// - exported API functions may be moved into internal subpackages,
//   making them invisible.  We'll need shims/delegates for func and const.
//   Types and vars are trickier.
// - use nice import names (e.g. core not _core) when it would be unambiguous to do so.
// - preserve comments before/in import decls.
// - look at files for non-linux/amd64 platforms
// - deal with assembly, compiler entrypoints
// - check for all conflicts: struct fields, concrete methods, interface methods.
// - check for definition conflicts at file scope
// - check for field definition conflicts
// - check for (abstract and concrete) method definition conflicts
// - check for renamed package-level types used as embedded fields, etc.
// - check for reference conflicts (hard)
// - emit 'git mv' commands so that new files are treated as moves, not adds.
// - struct literals T{1,2} may need field names T{X:1, Y:2}.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (o *organizer) refactor(clusters []*cluster) error {
	// new names for objects that must become exported
	exportNames := make(map[types.Object]string)
	export := func(obj types.Object) {
		if !ast.IsExported(obj.Name()) {
			if _, ok := exportNames[obj]; !ok {
				exportNames[obj] = exportedName(obj.Name())
			}
		}
	}

	// Find objects requiring a name change for export:
	// the heads of node-graph edges that span clusters.
	for _, n := range o.nodes {
		for succ := range n.succs {
			if n.cluster != succ.cluster {
				if !succ.mustExport {
					succ.mustExport = true
					for _, obj := range succ.objects {
						export(obj)
					}
				}
			}
		}
	}

	// Fix up package-level definition conflicts in each cluster.
	for _, c := range clusters {
		// For now, all import names will be "_" + the last segment.
		// TODO(adonovan): avoid _ when not needed and make sure
		// the last segment is a valid identifier.
		// Alternatively, apply gorename on a file-by-file basis
		// to eliminate the underscores.

		c.name = "_" + path.Base(c.importPath) // (default)
		c.scope = make(map[string]*node)
		for n := range c.nodes {
			for _, obj := range n.objects {
				if !isPackageLevel(obj) {
					continue
				}
				// NB: only exported symbols may conflict.
				// That may change when we deal with imports.
				name := obj.Name()
				if new, ok := exportNames[obj]; ok {
					name = new
				}
				if prev := c.scope[name]; prev != nil {
					fmt.Fprintf(os.Stderr, "%s: warning: exporting %s\n",
						o.fset.Position(n.syntax.Pos()),
						obj.Name())
					fmt.Fprintf(os.Stderr, "%s: \twould conflict with %s; adding 'X' prefix.\n",
						o.fset.Position(prev.syntax.Pos()), name)

					// TODO(adonovan): fix: use a unique prefix
					// that never appears in the package!
					name = "X" + name
					exportNames[obj] = name
				}
				c.scope[name] = n
			}
		}
	}

	// Mark selectables (fields and methods) for export if they
	// are ever referenced from outside their defining package.
	// TODO(adonovan): fix: must compute consequences (a la gorename).
	for _, n := range o.nodes {
		for _, obj := range n.uses {
			if v, ok := obj.(*types.Var); ok && v.IsField() {
				// field
			} else if f, ok := obj.(*types.Func); ok && methodRecv(f) != nil {
				// method
			} else {
				continue
			}
			// obj is a field or method

			// inter-cluster reference?
			if o.nodesByObj[obj].cluster != n.cluster {
				export(obj)
			}
		}
	}

	// Inspect referring identifiers within each node.
	// Compute import dependencies (existing and new packages).
	// Qualify inter-cluster references with the new package name.
	for _, n := range o.nodes {
		for id, obj := range n.uses {
			// existing import dependency?
			if pkgName, ok := obj.(*types.PkgName); ok {
				n.addImport(pkgName)
				continue
			}

			name := id.Name
			if new, ok := exportNames[obj]; ok {
				name = new
			}

			// Cross-package reference to package-level entity?
			//
			// TODO(adonovan): fix: check the lexical
			// structure to see if the name is free.  If
			// not, uniquify n2.cluster.name.  For now,
			// globally qualify; later, uniquify it only as
			// needed on a per-cluster basis.
			if isPackageLevel(obj) {
				n2 := o.nodesByObj[obj]
				if n2.cluster != n.cluster {
					// qualify the identifier
					name = n2.cluster.name + "." + name
					n.addImport(n2.cluster)

				}
			}

			id.Name = name
		}
	}

	// Modify defining identifiers for exported objects.
	for id, obj := range o.info.Defs {
		if new, ok := exportNames[obj]; ok {
			id.Name = new
		}
	}

	// Split the source files into files in subpackages.
	if err := o.split(); err != nil {
		return err
	}

	// Now write the clusters out:
	var failed bool
	fmt.Fprintf(os.Stderr, "Writing refactored output...\n")
	for _, c := range clusters {
		dir := filepath.Join(*outdir, c.importPath)
		fmt.Fprintf(os.Stderr, "\t%s", dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, ": %v", err)
			failed = true
		} else {
			// Create an empty .s file in each new package;
			// this causes gc to suppress "missing function
			// body" errors until link time.
			ioutil.WriteFile(filepath.Join(dir, "dummy.s"), nil, 0666)

			for base, out := range c.outputFiles {
				filename := filepath.Join(dir, base)
				if err := out.writeFile(filename); err != nil {
					fmt.Fprintf(os.Stderr, ": %v", err)
					failed = true
				}
			}
		}
		fmt.Fprintln(os.Stderr)
	}
	if failed {
		return fmt.Errorf("there were I/O errors")
	}
	return nil
}

// split writes the (modified) AST for each node to the output file to
// which it belongs, in lexical order.
//
func (o *organizer) split() error {
	// TODO(adonovan): fix: look at other uses too: references to
	// interface methods and struct fields.

	// Now we pretty-print the modified syntax trees, split the text
	// into node-sized chunks (along with preceding
	// whitespace/comments), and append each chunk to the relevant
	// (split) files belonging to each cluster.
	//
	// To back, split the text into node-sized chunks and attach the
	// text of each one to the appropriate node's text.
	//
	// We do this one file at a time, splitting the pretty text into
	// declarations, with order determined by forEachDecl again, for
	// consistency.  This way each decl corresponds to o.nodes[i].
	//
	var i int // node index
	for _, f := range o.info.Files {
		filename := o.fset.Position(f.Pos()).Filename
		filebase := filepath.Base(filename)

		// Print each file and parse it back.
		var buf bytes.Buffer
		if err := format.Node(&buf, o.fset, f); err != nil {
			return fmt.Errorf("pretty-printing %s failed: %v", filename, err)
		}

		fset2 := token.NewFileSet()
		f2, err := parser.ParseFile(fset2, filename, &buf, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parsing of pretty-printed %s failed: %v", filename, err)
		}
		text := buf.Bytes()

		// All text operations are newline-terminated.

		// Record the initial comment that runs from the start
		// of the file up (but not including) the package decl.
		// Each output file will get a copy of it, plus a
		// package decl appropriate to its cluster.
		initialComment := text[:int(f2.Package)-fset2.File(f2.Pos()).Base()]

		// Skip to beyond the import block.
		//
		// TODO(adonovan): fix: don't discard comments between
		// the package decl and the import decl.  (Fortunately
		// "runtime" uses few imports.)
		pos := f2.Name.End() // after package decl
		for _, decl := range f2.Decls {
			if decl, ok := decl.(*ast.GenDecl); ok && decl.Tok == token.IMPORT {
				pos = decl.End()
			}
		}
		offset := fset2.Position(pos).Offset // offset of end of previous decl
		offset = withNewline(text, offset)

		var enterGroupText []byte // current group's opening whitespace and "var ("

		// Map parsed pretty decls back to their corresponding nodes.
		forEachDecl(f2, func(syntax ast.Node, parent *ast.GenDecl) {
			// Find node and cluster corresponding to syntax.
			// (Careful: methods have no node of their own,
			// so we can't use o.nodes[i].)
			n := o.nodes[i]
			i++
			out := n.cluster.file(filebase)
			out.addImportsFor(n)

			// first time writing to this file?
			if out.head.Len() == 0 {
				out.head.Write(initialComment)
				// TODO(adonovan): fix: think about the
				// leading \n.  Is it sound w.r.t. both
				// package documentation (which doesn't
				// want it) and +build comments (which
				// need it)?
				fmt.Fprintf(&out.head, "package %s\n\n",
					path.Base(n.cluster.importPath))
			}

			// Handle transitions into/out of group decls:
			// var(...), type(...).
			if parent == nil {
				// syntax is a complete decl

				// leaving previous group
				if out.groupDecl != nil {
					out.body.WriteString(")\n")
					out.groupDecl = nil
				}
			} else {
				// syntax is one var or type spec in a group decl

				// first spec of group?
				if syntax == parent.Specs[0] {
					// save preceding whitespace and "var ("
					lparen := fset2.Position(parent.Lparen).Offset
					lparen = withNewline(text, lparen)
					enterGroupText = text[offset:lparen]
					offset = lparen
				}

				// has group changed?
				if parent != out.groupDecl {
					// leave previous group
					if out.groupDecl != nil {
						out.body.WriteString(")\n")
					}

					// enter new group
					out.body.Write(enterGroupText)
					out.groupDecl = parent
				}
			}
			// The final implicit "leaving group" transition for
			// each file is handled by (*cluster).writeFile.

			// TODO(adonovan): fix: don't discard comments
			// at the end of each file; copy them to all
			// output files.

			// Emit node syntax.
			// Emit in all text since the end of the last decl.
			end := fset2.Position(syntax.End()).Offset
			end = withNewline(text, end)
			out.body.Write(text[offset:end])
			offset = end

			// last spec of group?
			if parent != nil && syntax == parent.Specs[len(parent.Specs)-1] {
				// consume input up to ')'
				rparen := fset2.Position(parent.Rparen).Offset
				rparen = withNewline(text, rparen)
				offset = rparen
			}
		})
	}
	if i != len(o.nodes) {
		panic("internal error")
	}
	return nil
}

func withNewline(data []byte, i int) int {
	for ; i < len(data); i++ {
		if data[i] == '\n' {
			return i + 1
		}
	}
	return i
}

func (n *node) addImport(imp interface{}) {
	if n.imports == nil {
		n.imports = make(map[interface{}]bool)
	}
	n.imports[imp] = true
}

// outputFile holds state for each output file.
type outputFile struct {
	head, body bytes.Buffer         // head is package decl + cluster imports
	imports    map[interface{}]bool // union of node.imports
	groupDecl  ast.Decl             // previous group decl, if any
}

func (out *outputFile) addImportsFor(n *node) {
	if out.imports == nil {
		out.imports = make(map[interface{}]bool)
	}
	for imp := range n.imports {
		out.imports[imp] = true
	}
}

func (c *cluster) file(base string) *outputFile {
	f := c.outputFiles[base]
	if f == nil {
		f = new(outputFile)
		c.outputFiles[base] = f
	}
	return f
}

// writeFile writes the outputFile data to the specified file.
func (out *outputFile) writeFile(filename string) error {
	// Add necessary imports to head.
	if len(out.imports) > 0 {
		var importLines []string
		for imp := range out.imports {
			var name, importPath string
			switch imp := imp.(type) {
			case *types.PkgName:
				name = imp.Name()
				importPath = imp.Imported().Path()
			case *cluster:
				name = imp.name
				importPath = imp.importPath
			}
			var spec string
			if name == path.Base(importPath) {
				spec = fmt.Sprintf("\t%q\n", importPath)
			} else {
				spec = fmt.Sprintf("\t%s %q\n", name, importPath)
			}
			importLines = append(importLines, spec)
		}
		sort.Strings(importLines)
		fmt.Fprintf(&out.head, "import (\n")
		for _, imp := range importLines {
			out.head.WriteString(imp)
		}
		fmt.Fprintf(&out.head, ")\n")
	}

	// Implement final state transition.
	if out.groupDecl != nil {
		// leaving var or type(...) decl
		out.body.WriteString(")\n")
	}

	// Write formatted head and data to filename.
	out.head.Write(out.body.Bytes())
	data := out.head.Bytes()

	// Run it through gofmt.
	data, err := format.Source(data)
	if err != nil {
		return fmt.Errorf("failed to gofmt %s: %v", filename, err)
	}

	return ioutil.WriteFile(filename, data, 0666)
}

// exportName returns the corresponding exported name for a non-exported identifier.
func exportedName(name string) string {
	// Underscores are used to avoid conflicts with keywords
	// (e.g. _func) or built-in identifiers (e.g. _string),
	// or to suppress export of uppercase names (e.g. _ESRCH).
	// Strip them off.
	name = strings.TrimLeft(name, "_")

	r, size := utf8.DecodeRuneInString(name)
	name = string(unicode.ToUpper(r)) + name[size:] // "foo" -> "Foo"

	if !unicode.IsLetter(r) {
		name = "X" + name // e.g. "_64bit" -> "X64bit"
		// TODO(adonovan): fix: result may yet conflict.
	}
	return name
}

// -- from refactor/rename --

func isPackageLevel(obj types.Object) bool {
	return obj.Pkg().Scope().Lookup(obj.Name()) == obj
}
