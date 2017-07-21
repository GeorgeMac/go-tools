package augur

import (
	"errors"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"path/filepath"

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

	Files []*ast.File
	SSA   *ssa.Package
	Build *build.Package

	Dependencies        map[string]struct{}
	ReverseDependencies map[string]struct{}

	Explicit bool

	Program *Augur

	dirty bool
}

func (a *Augur) newPackage() *Package {
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
		Dependencies:        map[string]struct{}{},
		ReverseDependencies: map[string]struct{}{},
		Program:             a,
	}
}

type Augur struct {
	Fset *token.FileSet
	// Packages maps import paths to type-checked packages.
	Packages map[string]*Package
	SSA      *ssa.Program
	Build    build.Context

	checker *types.Config
}

func NewAugur() *Augur {
	fset := token.NewFileSet()
	a := &Augur{
		Fset:     fset,
		Packages: map[string]*Package{},
		SSA:      ssa.NewProgram(fset, ssa.GlobalDebug),
		checker:  &types.Config{},
		Build:    build.Default,
	}
	a.checker.Importer = a
	return a
}

func (a *Augur) InitialPackages() []*Package {
	// TODO(dh): rename to ExplicitPackages
	var pkgs []*Package
	for _, pkg := range a.Packages {
		if pkg.Explicit {
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs
}

func (a *Augur) Import(path string) (*types.Package, error) {
	return nil, nil
}

func (a *Augur) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	// FIXME(dh): support vendoring
	pkg, ok := a.Packages[path]
	if ok && !pkg.dirty {
		return pkg.Package, nil
	}
	// FIXME(dh): don't recurse forever on circular dependencies
	pkg, err := a.compile(path)
	return pkg.Package, err
}

func (a *Augur) Package(path string) *Package {
	return a.Packages[path]
}

func (a *Augur) Compile(path string) (*Package, error) {
	// TODO(dh): support cgo preprocessing a la go/loader
	//
	// TODO(dh): support scoping packages to their build tags
	//
	// TODO(dh): build packages in parallel
	//
	// TODO(dh): don't recompile up to date packages
	//
	// TODO(dh): remove stale reverse dependencies

	pkg, err := a.compile(path)
	if err != nil {
		return nil, err
	}
	pkg.Explicit = true
	return pkg, nil
}

func (a *Augur) markDirty(pkg *Package) {
	pkg.dirty = true
	for rdep := range pkg.ReverseDependencies {
		a.markDirty(a.Package(rdep))
	}
}

func (a *Augur) RecompileDirtyPackages() error {
	for path, pkg := range a.Packages {
		if !pkg.dirty {
			continue
		}
		_, err := a.compile(path)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *Augur) compile(path string) (*Package, error) {
	pkg := a.newPackage()
	old, ok := a.Packages[path]
	if ok {
		pkg.ReverseDependencies = old.ReverseDependencies
		pkg.Explicit = old.Explicit
	}

	log.Println("compiling", path)
	// OPT(dh): when compile gets called while rebuilding dirty
	// packages, it is unnecessary to call markDirty. in fact, this
	// causes exponential complexity.
	a.markDirty(pkg)
	if path == "unsafe" {
		pkg.Package = types.Unsafe
		a.Packages[path] = pkg
		pkg.dirty = false
		return pkg, nil
	}

	var err error
	pkg.Build, err = a.Build.Import(path, ".", 0)
	if err != nil {
		return nil, err
	}
	if len(pkg.Build.CgoFiles) != 0 {
		return nil, errors.New("cgo is not currently supported")
	}

	pkg.Files = nil
	for _, f := range pkg.Build.GoFiles {
		// TODO(dh): cache parsed files and only reparse them if
		// necessary
		af, err := parser.ParseFile(a.Fset, filepath.Join(pkg.Build.Dir, f), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		pkg.Files = append(pkg.Files, af)
	}

	pkg.Package, err = a.checker.Check(path, a.Fset, pkg.Files, pkg.Info)
	if err != nil {
		return nil, err
	}
	prev := a.Packages[path]
	a.Packages[path] = pkg
	if prev != nil {
		a.SSA.RemovePackage(prev.SSA)
	}
	pkg.SSA = a.SSA.CreatePackage(pkg.Package, pkg.Files, pkg.Info, true)
	pkg.SSA.Build()

	for _, imp := range pkg.Build.Imports {
		// FIXME(dh): support vendoring
		dep := a.Package(imp)
		pkg.Dependencies[dep.Path()] = struct{}{}
		dep.ReverseDependencies[pkg.Path()] = struct{}{}
	}

	pkg.dirty = false
	log.Println("\tcompiled", path)
	return pkg, nil
}
