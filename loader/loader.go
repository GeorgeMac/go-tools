package loader

import (
	"go/ast"
	"go/build"
	"go/token"
	"go/types"

	"honnef.co/go/tools/obj"
	"honnef.co/go/tools/ssa"
)

// FIXME(dh): when we reparse a package, new files get added to the
// FileSet. There is, however, no way of removing files from the
// FileSet, so it grows forever, leaking memory.

// FIXME(dh): go/ssa uses typeutil.Hasher, which grows monotonically –
// i.e. leaks memory over time.

type Package struct {
	*types.Package
	*types.Info
	Build *build.Package
	SSA   *ssa.Package

	dependencies        map[*Package]struct{}
	reverseDependencies map[*Package]struct{}

	Program  *Program
	Files    map[*token.File]*ast.File
	Explicit bool
	State    int
	Errors   []error
}

const (
	InvalidAST = 1 << iota
	TypeError
	NoSSA
)

func (a *Program) Remove(pkg *Package) {
	if pkg.SSA != nil {
		a.SSA.RemovePackage(pkg.SSA)
	}
	for rdep := range pkg.reverseDependencies {
		a.Remove(rdep)
	}
	for dep := range pkg.dependencies {
		delete(dep.reverseDependencies, pkg)
	}
	delete(a.Packages, pkg.Path())
	delete(a.TypePackages, pkg.Package)
	// XXX remove old packages from graph
}

func (a *Program) newPackage() *Package {
	return &Package{
		Info: &types.Info{
			Types:      map[ast.Expr]types.TypeAndValue{},
			Defs:       map[*ast.Ident]types.Object{},
			Uses:       map[*ast.Ident]types.Object{},
			Implicits:  map[ast.Node]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
			Scopes:     map[ast.Node]*types.Scope{},
			InitOrder:  []*types.Initializer{},
		},
		dependencies:        map[*Package]struct{}{},
		reverseDependencies: map[*Package]struct{}{},
		Program:             a,
	}
}

type Program struct {
	Fset *token.FileSet
	// Packages maps import paths to type-checked packages.
	Packages     map[string]*Package
	TypePackages map[*types.Package]*Package
	SSA          *ssa.Program
	Build        build.Context

	checker *types.Config
	Errors  []error

	logDepth int

	g *obj.Graph
}

func NewProgram(g *obj.Graph) *Program {
	fset := token.NewFileSet()
	a := &Program{
		Fset:         fset,
		Packages:     map[string]*Package{},
		TypePackages: map[*types.Package]*Package{},
		SSA:          ssa.NewProgram(fset, ssa.GlobalDebug),
		checker:      &types.Config{},
		Build:        build.Default,
		g:            g,
	}
	a.checker.Importer = a
	a.checker.Error = func(err error) {
		a.Errors = append(a.Errors, err)
	}
	return a
}

func (a *Program) InitialPackages() []*Package {
	// TODO(dh): rename to ExplicitPackages
	var pkgs []*Package
	for _, pkg := range a.Packages {
		if pkg.Explicit {
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs
}

func (a *Program) Import(path string) (*types.Package, error) {
	return nil, nil
}

func (a *Program) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	bpkg, err := a.Build.Import(path, srcDir, 0)
	if err != nil {
		return nil, err
	}

	if pkg, ok := a.Packages[bpkg.ImportPath]; ok {
		return pkg.Package, nil
	}
	// FIXME(dh): don't recurse forever on circular dependencies
	pkg, err := a.compile(path, srcDir)
	return pkg.Package, err
}

// Package returns a cached package. The result may be nil.
func (a *Program) Package(path string) *Package {
	return a.Packages[path]
}

// CompilePackage returns a compiled package. It will either use the
// cache if appropriate, or compile the package.
func (a *Program) CompiledPackage(path string) (*Package, error) {
	pkg, ok := a.Packages[path]
	if ok {
		return pkg, nil
	}
	return a.Compile(path)
}

func (a *Program) Compile(path string) (*Package, error) {
	// TODO(dh): support cgo preprocessing a la go/loader
	//
	// TODO(dh): support scoping packages to their build tags
	//
	// TODO(dh): build packages in parallel
	//
	// TODO(dh): don't recompile up to date packages
	//
	// TODO(dh): remove stale reverse dependencies

	pkg, err := a.compile(path, ".")
	pkg.Explicit = true
	return pkg, err
}

// XXX update reverse dependencies when package gets removed

func (a *Program) register(pkg *Package) {
	if a.Packages[pkg.Path()] == pkg {
		// Already registered the package
		return
	}
	a.Packages[pkg.Path()] = pkg
	a.TypePackages[pkg.Package] = pkg

	for _, imp := range pkg.Imports() {
		// Importing packages from the index can load multiple new
		// packages into memory, not just the one explicitly compiled.
		// We have to make sure that these dependencies are tracked
		// correctly.
		//
		// It is not possible that we're holding a reference to an old
		// version of a dependency. New versions can only appear after
		// the old version has been explicitly removed, which untracks
		// it.
		aimp, ok := a.TypePackages[imp]
		if !ok {
			// We don't yet know about the package
			aimp = a.newPackage()
			aimp.Package = imp
			// TODO(dh): set files and build SSA form
			a.register(aimp)
		}
		pkg.dependencies[aimp] = struct{}{}
		aimp.reverseDependencies[pkg] = struct{}{}
	}
}

func (a *Program) compile(path string, srcdir string) (*Package, error) {
	a.logDepth++
	defer func() { a.logDepth-- }()
	pkg := a.newPackage()
	old, ok := a.Packages[path]
	if ok {
		pkg.Explicit = old.Explicit
		a.Remove(old)
	}

	// if path == "unsafe" {
	// 	pkg.Package = types.Unsafe
	// 	return pkg, nil
	// }

	// log.Printf("%scompiling %s", strings.Repeat("\t", a.logDepth), path)
	// build, err := a.Build.Import(path, srcdir, 0)
	// if err != nil {
	// 	return pkg, err
	// }

	pkg.Build = build
	var files []*ast.File
	// if a.g.HasPackage(build.ImportPath) {
	// 	log.Println("using graph for", build.ImportPath)
	// 	// XXX validate checksum
	// 	// XXX load AST from somewhere
	// 	// XXX load packageinfo from somewhere
	// 	pkg.Package = a.g.Package(build.ImportPath)
	// 	a.register(pkg)
	// 	return pkg, nil
	// }

	// pkg.Files = map[*token.File]*ast.File{}
	// for _, f := range build.GoFiles {
	// 	// TODO(dh): cache parsed files and only reparse them if
	// 	// necessary
	// 	af, err := buildutil.ParseFile(a.Fset, &a.Build, nil, build.Dir, f, parser.ParseComments)
	// 	if err != nil {
	// 		pkg.Errors = append(pkg.Errors, err)
	// 		pkg.State |= InvalidAST
	// 	}
	// 	tf := a.Fset.File(af.Pos())
	// 	pkg.Files[tf] = af
	// 	files = append(files, af)
	// }

	// if len(build.CgoFiles) > 0 {
	// 	cgoFiles, err := cgo.ProcessCgoFiles(build, a.Fset, nil, parser.ParseComments)
	// 	if err != nil {
	// 		pkg.Errors = append(pkg.Errors, err)
	// 		pkg.State |= InvalidAST
	// 	}
	// 	files = append(files, cgoFiles...)
	// 	for _, af := range cgoFiles {
	// 		tf := a.Fset.File(af.Pos())
	// 		pkg.Files[tf] = af
	// 	}
	// }

	pkg.Package, err = a.checker.Check(path, a.Fset, files, pkg.Info)
	if err != nil {
		pkg.State |= TypeError
	}

	if pkg.State == 0 && files != nil {
		incomplete := false
		for _, dep := range pkg.Imports() {
			if a.TypePackages[dep].State != 0 {
				incomplete = true
				break
			}
		}
		if !incomplete {
			a.g.InsertPackage(pkg.Build, pkg.Package)
			pkg.SSA = a.SSA.CreatePackage(pkg.Package, files, pkg.Info, true)
			pkg.SSA.Build()
		}
	} else {
		pkg.State |= NoSSA
	}

	pkg.Errors = append(pkg.Errors, a.Errors...)
	a.Errors = nil
	a.g.InsertPackage(pkg.Package)
	a.register(pkg)
	return pkg, nil
}
