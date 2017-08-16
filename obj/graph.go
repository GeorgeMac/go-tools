package obj

import (
	"encoding/binary"
	"fmt"
	"go/types"
	"reflect"

	"honnef.co/go/tools/typeutil"

	"github.com/dgraph-io/badger"
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

type Graph struct {
	kv *badger.KV

	objToID map[types.Object][]byte
	typToID typeutil.Map

	idToObj map[string]types.Object
	idToTyp map[string]types.Type
	idToPkg map[string]*types.Package

	pkgs map[string]*types.Package

	scopes map[*types.Package]map[string][]byte
	set    []*badger.Entry
}

func OpenGraph(dir string) (*Graph, error) {
	opt := badger.DefaultOptions
	opt.Dir = dir
	opt.ValueDir = dir
	kv, err := badger.NewKV(&opt)
	if err != nil {
		return nil, err
	}

	return &Graph{
		kv:      kv,
		objToID: map[types.Object][]byte{},
		idToObj: map[string]types.Object{},
		idToTyp: map[string]types.Type{},
		idToPkg: map[string]*types.Package{},
		pkgs:    map[string]*types.Package{},
		scopes:  map[*types.Package]map[string][]byte{},
	}, nil
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

func (g *Graph) InsertPackage(pkg *types.Package) {
	if pkg == types.Unsafe {
		return
	}
	if _, ok := g.pkgs[pkg.Path()]; ok {
		return
	}
	g.pkgs[pkg.Path()] = pkg

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

	g.kv.BatchSet(g.set)
	g.set = nil
}

func (g *Graph) encodeScope(pkg *types.Package, scope *types.Scope) [16]byte {
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

func (g *Graph) encodeObject(obj types.Object) {
	if _, ok := g.objToID[obj]; ok {
		return
	}
	if obj.Pkg() == nil {
		g.objToID[obj] = []byte(fmt.Sprintf("builtin/%s", obj.Name()))
		return
	}
	id := uuid.NewV1()
	path := obj.Pkg().Path()
	key := []byte(fmt.Sprintf("pkgs/%s\x00objects/%s", path, [16]byte(id)))
	g.objToID[obj] = key

	g.encodeType(obj.Type())
	typID := g.typToID.At(obj.Type()).([]byte)
	var typ byte
	switch obj.(type) {
	case *types.Func:
		typ = kindFunc
	case *types.Var:
		typ = kindVar
	case *types.TypeName:
		typ = kindTypename
	case *types.Const:
		typ = kindConst
	case *types.PkgName:
		typ = kindPkgname
	default:
		panic(fmt.Sprintf("%T", obj))
	}

	var v []byte
	switch obj := obj.(type) {
	case *types.PkgName:
		v = encodeBytes(
			[]byte(obj.Name()),
			[]byte{typ},
			typID,
			[]byte(obj.Imported().Path()),
		)
	case *types.Const:
		kind, data := encodeConstant(reflect.ValueOf(obj.Val()))
		v = encodeBytes(
			[]byte(obj.Name()),
			[]byte{typ},
			typID,
			[]byte{kind},
			data,
		)
	default:
		v = encodeBytes(
			[]byte(obj.Name()),
			[]byte{typ},
			typID,
		)
	}

	g.set = badger.EntriesSet(g.set, key, v)
}

func encodeBytes(vs ...[]byte) []byte {
	var out []byte
	num := make([]byte, binary.MaxVarintLen64)
	for _, v := range vs {
		n := binary.PutUvarint(num, uint64(len(v)))
		out = append(out, num[:n]...)
		out = append(out, v...)
	}
	return out
}

func (g *Graph) encodeType(T types.Type) {
	if id := g.typToID.At(T); id != nil {
		return
	}
	if T, ok := T.(*types.Basic); ok {
		// OPT(dh): use an enum instead of strings for the built in
		// types
		g.typToID.Set(T, []byte(fmt.Sprintf("builtin/%s", T.Name())))
		return
	}
	id := uuid.NewV1()
	key := []byte(fmt.Sprintf("types/%s", [16]byte(id)))
	g.typToID.Set(T, key)

	switch T := T.(type) {
	case *types.Signature:
		g.encodeType(T.Params())
		g.encodeType(T.Results())
		if T.Recv() != nil {
			g.encodeObject(T.Recv())
		}

		variadic := byte(0)
		if T.Variadic() {
			variadic = 1
		}
		params := g.typToID.At(T.Params()).([]byte)
		results := g.typToID.At(T.Results()).([]byte)
		recv := g.objToID[T.Recv()]

		v := encodeBytes(
			[]byte{kindSignature},
			params,
			results,
			recv,
			[]byte{variadic},
		)

		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Named:
		var args [][]byte
		args = append(args, []byte{kindNamed})

		underlying := T.Underlying()
		g.encodeType(underlying)
		args = append(args, g.typToID.At(underlying).([]byte))

		typename := T.Obj()
		g.encodeObject(typename)
		args = append(args, g.objToID[typename])

		for i := 0; i < T.NumMethods(); i++ {
			fn := T.Method(i)
			g.encodeObject(fn)
			args = append(args, g.objToID[fn])
		}
		v := encodeBytes(args...)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Slice:
		elem := T.Elem()
		g.encodeType(elem)
		v := encodeBytes(
			[]byte{kindSlice},
			g.typToID.At(elem).([]byte),
		)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Pointer:
		elem := T.Elem()
		g.encodeType(elem)
		v := encodeBytes(
			[]byte{kindPointer},
			g.typToID.At(elem).([]byte),
		)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Interface:
		var args [][]byte
		args = append(args, []byte{kindInterface})

		n := make([]byte, binary.MaxVarintLen64)
		l := binary.PutUvarint(n, uint64(T.NumExplicitMethods()))
		args = append(args, n[:l])

		for i := 0; i < T.NumExplicitMethods(); i++ {
			fn := T.ExplicitMethod(i)
			g.encodeObject(fn)
			args = append(args, g.objToID[fn])
		}

		n = make([]byte, binary.MaxVarintLen64)
		l = binary.PutUvarint(n, uint64(T.NumEmbeddeds()))
		args = append(args, n[:l])

		for i := 0; i < T.NumEmbeddeds(); i++ {
			embedded := T.Embedded(i)
			g.encodeType(embedded)
			args = append(args, g.typToID.At(embedded).([]byte))
		}
		v := encodeBytes(args...)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Array:
		elem := T.Elem()
		g.encodeType(elem)

		n := make([]byte, binary.MaxVarintLen64)
		l := binary.PutUvarint(n, uint64(T.Len()))
		n = n[:l]
		v := encodeBytes(
			[]byte{kindArray},
			g.typToID.At(elem).([]byte),
			n,
		)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Struct:
		var args [][]byte
		args = append(args, []byte{kindStruct})
		for i := 0; i < T.NumFields(); i++ {
			field := T.Field(i)
			tag := T.Tag(i)
			g.encodeObject(field)

			args = append(args, g.objToID[field])
			args = append(args, []byte(tag))
		}
		v := encodeBytes(args...)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Tuple:
		var args [][]byte
		args = append(args, []byte{kindTuple})
		for i := 0; i < T.Len(); i++ {
			v := T.At(i)
			g.encodeObject(v)
			args = append(args, g.objToID[v])
		}
		v := encodeBytes(args...)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Map:
		g.encodeType(T.Key())
		g.encodeType(T.Elem())
		v := encodeBytes(
			[]byte{kindMap},
			g.typToID.At(T.Key()).([]byte),
			g.typToID.At(T.Elem()).([]byte),
		)
		g.set = badger.EntriesSet(g.set, key, v)
	case *types.Chan:
		g.encodeType(T.Elem())

		v := encodeBytes(
			[]byte{kindChan},
			g.typToID.At(T.Elem()).([]byte),
			[]byte{byte(T.Dir())},
		)
		g.set = badger.EntriesSet(g.set, key, v)
	default:
		panic(fmt.Sprintf("%T", T))
	}
}

func (g *Graph) Close() error {
	return g.kv.Close()
}
