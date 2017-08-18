package obj

import (
	"encoding/binary"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"log"

	"golang.org/x/tools/go/buildutil"
	"honnef.co/go/tools/obj/cgo"

	"github.com/dgraph-io/badger"
	"github.com/golang/snappy"
	uuid "github.com/satori/go.uuid"
)

// OPT(dh): in types with elems like slices, consider storing the
// concrete underlying type together with the type ID, so that we can
// defer the actual lookup

// OPT(dh): also consider not using UUIDs for types. if the IDs were
// sequential, we could use a range query to load all referred types
// in one go. UUIDs do help with multiple tools writing to the same
// database, though.

// OPT(dh): optimize calculation of IDs (use byte slices and in-place
// modifications instead of all the Sprintf calls)

// OPT(dh): use batch sets when inserting data

// TODO(dh): add index mapping package names to import pathscd
// TODO(dh): store AST, types.Info and checksums

type Package struct {
	*types.Package
	*types.Info
	Build *build.Package
	Files []*ast.File
}

var Unsafe = &Package{Package: types.Unsafe}

type Graph struct {
	curpkg string

	Fset *token.FileSet

	kv *badger.KV

	objToID map[types.Object][]byte
	typToID map[types.Type][]byte

	idToObj map[string]types.Object
	idToTyp map[string]types.Type
	idToPkg map[string]*Package

	// OPT(dh): merge idToPkg and pkgs
	pkgs      map[string]*Package
	augmented map[*types.Package]*Package

	scopes map[*Package]map[string][]byte
	set    []*badger.Entry

	build build.Context

	checker *types.Config
}

func OpenGraph(dir string) (*Graph, error) {
	opt := badger.DefaultOptions
	opt.Dir = dir
	opt.ValueDir = dir
	kv, err := badger.NewKV(&opt)
	if err != nil {
		return nil, err
	}

	g := &Graph{
		Fset:      token.NewFileSet(),
		kv:        kv,
		objToID:   map[types.Object][]byte{},
		typToID:   map[types.Type][]byte{},
		idToObj:   map[string]types.Object{},
		idToTyp:   map[string]types.Type{},
		idToPkg:   map[string]*Package{},
		pkgs:      map[string]*Package{},
		scopes:    map[*Package]map[string][]byte{},
		build:     build.Default,
		checker:   &types.Config{},
		augmented: map[*types.Package]*Package{},
	}
	g.checker.Importer = g

	return g, nil
}

func (g *Graph) Augment(pkg *types.Package) *Package {
	return g.augmented[pkg]
}

func (g *Graph) Import(path string) (*types.Package, error) {
	panic("not implemented, use ImportFrom")
}

func (g *Graph) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	bpkg, err := g.build.Import(path, srcDir, 0)
	if err != nil {
		return nil, err
	}

	if bpkg.ImportPath == "unsafe" {
		return types.Unsafe, nil
	}

	// TODO(dh): use checksum to verify that package is up to date
	if pkg, ok := g.pkgs[bpkg.ImportPath]; ok {
		return pkg.Package, nil
	}
	if g.HasPackage(bpkg.ImportPath) {
		log.Println("importing from graph:", bpkg.ImportPath)
		pkg := g.Package(bpkg.ImportPath)
		return pkg.Package, nil
	}

	log.Println("compiling:", bpkg.ImportPath)

	// TODO(dh): support returning partially built packages. For
	// example, an invalid AST still is usable for some operations.
	var files []*ast.File
	for _, f := range bpkg.GoFiles {
		af, err := buildutil.ParseFile(g.Fset, &g.build, nil, bpkg.Dir, f, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, af)
	}

	if len(bpkg.CgoFiles) > 0 {
		cgoFiles, err := cgo.ProcessCgoFiles(bpkg, g.Fset, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, cgoFiles...)
	}

	// TODO(dh): collect info
	info := &types.Info{}
	pkg, err := g.checker.Check(path, g.Fset, files, info)
	if err != nil {
		return nil, err
	}

	// TODO(dh): build SSA

	apkg := &Package{
		Package: pkg,
		Info:    info,
		Files:   files,
		Build:   bpkg,
	}
	g.InsertPackage(apkg)
	return pkg, nil
}

func (g *Graph) HasPackage(path string) bool {
	if path == "unsafe" {
		return true
	}
	if _, ok := g.pkgs[path]; ok {
		return true
	}
	ok, _ := g.kv.Exists([]byte(fmt.Sprintf("pkgs/%s\x00name", path)))
	return ok
}

func (g *Graph) InsertPackage(pkg *Package) {
	if pkg.Package == types.Unsafe {
		return
	}
	if _, ok := g.pkgs[pkg.Build.ImportPath]; ok {
		return
	}
	log.Println("inserting", pkg)
	g.pkgs[pkg.Build.ImportPath] = pkg
	g.augmented[pkg.Package] = pkg

	g.set = []*badger.Entry{}
	for _, imp := range pkg.Imports() {
		key := []byte(fmt.Sprintf("pkgs/%s\x00imports/%s", pkg.Path(), imp.Path()))
		g.set = badger.EntriesSet(g.set, key, nil)
	}

	key := []byte(fmt.Sprintf("pkgs/%s\x00name", pkg.Path()))
	g.set = badger.EntriesSet(g.set, key, []byte(pkg.Name()))

	id := []byte(fmt.Sprintf("pkgs/%s\x00scopes/%s", pkg.Path(), g.encodeScope(pkg, pkg.Scope())))
	key = []byte(fmt.Sprintf("pkgs/%s\x00scope", pkg.Path()))
	g.set = badger.EntriesSet(g.set, key, id)

	ast := NewFileEncoder().Encode(pkg.Files)
	cast := snappy.Encode(nil, ast)
	key = []byte(fmt.Sprintf("pkgs/%s\x00ast", pkg.Path()))
	g.set = badger.EntriesSet(g.set, key, cast)

	g.kv.BatchSet(g.set)
	g.set = nil
}

func (g *Graph) encodeScope(pkg *Package, scope *types.Scope) [16]byte {
	id := [16]byte(uuid.NewV1())

	var args [][]byte

	names := scope.Names()
	n := make([]byte, binary.MaxVarintLen64)
	l := binary.PutUvarint(n, uint64(len(names)))
	n = n[:l]
	args = append(args, n)

	for _, name := range names {
		obj := scope.Lookup(name)
		g.encodeObject(obj)
		args = append(args, g.objToID[obj])
	}

	n = make([]byte, binary.MaxVarintLen64)
	l = binary.PutUvarint(n, uint64(scope.NumChildren()))
	n = n[:l]
	args = append(args, n)

	for i := 0; i < scope.NumChildren(); i++ {
		sid := g.encodeScope(pkg, scope.Child(i))
		args = append(args, []byte(fmt.Sprintf("pkgs/%s\x00scopes/%s", pkg.Path(), sid)))
	}

	v := encodeBytes(args...)
	key := []byte(fmt.Sprintf("pkgs/%s\x00scopes/%s", pkg.Path(), id))
	g.set = badger.EntriesSet(g.set, key, v)

	return id
}

const (
	kindFunc = iota
	kindVar
	kindTypename
	kindConst
	kindPkgname

	kindSignature
	kindNamed
	kindSlice
	kindPointer
	kindInterface
	kindArray
	kindStruct
	kindTuple
	kindMap
	kindChan
)

func (g *Graph) Close() error {
	return g.kv.Close()
}
