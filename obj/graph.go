package obj

import (
	"encoding/binary"
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"reflect"
	"unsafe"

	"golang.org/x/tools/go/buildutil"
	"honnef.co/go/tools/obj/cgo"
	"honnef.co/go/tools/ssa"

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

type unsafeTypeAndValue struct {
	mode  byte
	Type  types.Type
	Value constant.Value
}

type unsafeSelection struct {
	kind     types.SelectionKind
	recv     types.Type   // type of x
	obj      types.Object // object denoted by x.f
	index    []int        // path from x to x.f
	indirect bool         // set if there was any pointer indirection on the path
}

type Package struct {
	*types.Package
	*types.Info
	SSA   *ssa.Package
	Build *build.Package
	Files []*ast.File
}

var Unsafe = &Package{Package: types.Unsafe}

type Graph struct {
	SSA *ssa.Program

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

	fset := token.NewFileSet()
	g := &Graph{
		SSA:       ssa.NewProgram(fset, 0),
		Fset:      fset,
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
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes:     map[ast.Node]*types.Scope{},
		InitOrder:  []*types.Initializer{},
	}
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

type mapping struct {
	astID   uint64
	indexID []byte
}

type mappings []mapping

func (m *mappings) add(astID uint64, indexID []byte) {
	*m = append(*m, mapping{astID, indexID})
}

func (m *mappings) encode(b []byte) []byte {
	// OPT(dh): come up with a more compact encoding.
	b = appendInt(b, uint64(len(*m)))
	for _, mapping := range *m {
		b = appendInt(b, mapping.astID)
		b = appendInt(b, uint64(len(mapping.indexID)))
		b = append(b, mapping.indexID...)
	}
	return b
}

func (m *mappings) decode(b []byte) []byte {
	n := binary.LittleEndian.Uint64(b)
	b = b[8:]
	for i := uint64(0); i < n; i++ {
		astID := binary.LittleEndian.Uint64(b)
		l := binary.LittleEndian.Uint64(b[8:])
		// copy the bytes because the argument to decode likely comes
		// from a KV store, and we don't own the slice.
		indexID := make([]byte, l)
		copy(indexID, b[16:16+l])
		b = b[16+l:]
		*m = append(*m, mapping{astID, indexID})
	}
	return b
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

	fenc := NewFileEncoder()
	ast := fenc.Encode(pkg.Files)
	cast := snappy.Encode(nil, ast)
	key = []byte(fmt.Sprintf("pkgs/%s\x00ast", pkg.Path()))
	g.set = badger.EntriesSet(g.set, key, cast)

	defs := new(mappings)
	uses := new(mappings)
	implicits := new(mappings)
	for ident, obj := range pkg.Info.Defs {
		if obj == nil {
			continue
		}
		if _, ok := obj.(*types.Label); ok {
			// XXX implement
			continue
		}
		g.encodeObject(obj)
		defs.add(fenc.ID(ident), g.objToID[obj])
	}
	for ident, obj := range pkg.Info.Uses {
		if _, ok := obj.(*types.Label); ok {
			// XXX implement
			continue
		}
		if _, ok := obj.(*types.Builtin); ok {
			continue
		}
		g.encodeObject(obj)
		uses.add(fenc.ID(ident), g.objToID[obj])
	}
	for node, obj := range pkg.Info.Implicits {
		g.encodeObject(obj)
		implicits.add(fenc.ID(node), g.objToID[obj])
	}
	// TODO InitOrder
	// TODO Scopes
	var b []byte
	b = defs.encode(b)
	b = uses.encode(b)
	b = implicits.encode(b)
	n := len(pkg.Info.Types)
	for expr := range pkg.Info.Types {
		// when go/types encounters an IncDecStmt, it creates a new
		// BasicLit with value 1. This node isn't part of the actual
		// AST, yet it ends up in Info.Types. As far as we can tell,
		// there is no way, other than iterating directly over
		// Info.Types, to encounter this node. It shouldn't be
		// necessary to serialize it.
		astID := fenc.ID(expr)
		if astID == 0 {
			n--
		}
	}
	b = appendInt(b, uint64(n))
	for expr, tv := range pkg.Info.Types {
		tv := *(*unsafeTypeAndValue)(unsafe.Pointer(&tv))
		astID := fenc.ID(expr)
		if astID == 0 {
			continue
		}
		g.encodeType(tv.Type)
		typID := g.typToID[tv.Type]
		b = appendInt(b, astID)
		b = appendInt(b, uint64(len(typID)))
		b = append(b, typID...)
		b = append(b, tv.mode)
		if tv.Value == nil {
			b = append(b, 0)
		} else {
			kind, data := encodeConstant(reflect.ValueOf(tv.Value))
			b = append(b, kind)
			b = appendInt(b, uint64(len(data)))
			b = append(b, data...)
		}
	}

	b = appendInt(b, uint64(len(pkg.Info.Selections)))
	for expr, sel := range pkg.Info.Selections {
		sel := (*unsafeSelection)(unsafe.Pointer(sel))

		astID := fenc.ID(expr)
		g.encodeType(sel.recv)
		recvID := g.typToID[sel.recv]
		g.encodeObject(sel.obj)
		objID := g.objToID[sel.obj]
		var t byte
		if sel.indirect {
			t = 1
		}
		kind := byte(sel.kind)

		b = appendInt(b, astID)
		b = appendInt(b, uint64(len(recvID)))
		b = append(b, recvID...)
		b = appendInt(b, uint64(len(objID)))
		b = append(b, objID...)
		b = appendInt(b, uint64(len(sel.index)))
		for _, idx := range sel.index {
			b = appendInt(b, uint64(idx))
		}
		b = append(b, t)
		b = append(b, kind)
	}

	// OPT(dh): consider compressing the mapping
	key = []byte(fmt.Sprintf("pkgs/%s\x00mapping", pkg.Path()))
	g.set = badger.EntriesSet(g.set, key, b)

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
