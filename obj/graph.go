package obj

import (
	"encoding/hex"
	"fmt"
	"go/types"
	"log"
	"reflect"
	"strconv"

	"honnef.co/go/tools/typeutil"

	"github.com/dgraph-io/badger"
	uuid "github.com/satori/go.uuid"
)

// OPT(dh): use enums instead of strings for object/type kinds

// OPT(dh): precompute aggregates. e.g., instead of having to query
// 4-8 keys for a single key, query one key that contains a binary
// blob.

// OPT(dh): deduplicate types

// OPT(dh): in types with elems like slices, consider storing the
// concrete underlying type together with the type ID, so that we can
// defer the actual lookup

// OPT(dh): also consider not using UUIDs for types. if the IDs were
// sequential, we could use a range query to load all referred types
// in one go. UUIDs do help with multiple tools writing to the same
// database, though.

// OPT(dh): optimize calculation of IDs (use byte slices and in-place
// modifications instead of all the Sprintf calls)

// OPT(dh): store UUIDs as raw bytes, not as their string representation

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

	for _, imp := range pkg.Imports() {
		key := fmt.Sprintf("pkgs/%s\x00imports/%s", pkg.Path(), imp.Path())
		g.kv.Set([]byte(key), nil, 0)
		g.InsertPackage(imp)
	}

	key := []byte(fmt.Sprintf("pkgs/%s\x00name", pkg.Path()))
	g.kv.Set(key, []byte(pkg.Name()), 0)

	id := []byte(fmt.Sprintf("scopes/%s", g.encodeScope(pkg.Scope())))
	key = []byte(fmt.Sprintf("pkgs/%s\x00scope", pkg.Path()))
	g.kv.Set(key, id, 0)
}

func (g *Graph) encodeScope(scope *types.Scope) uuid.UUID {
	id := uuid.NewV1()

	for i, name := range scope.Names() {
		// OPT(dh): store index as raw bytes
		key := []byte(fmt.Sprintf("scopes/%s/objects/%d", id, i))
		obj := scope.Lookup(name)
		g.encodeObject(obj)
		g.kv.Set(key, g.objToID[obj], 0)
	}

	for i := 0; i < scope.NumChildren(); i++ {
		// OPT(dh): children scopes are only ever referenced by their
		// parent scopes. it would make sense to store them
		// sequentially, in a way that only requires one iterator seek
		// to load an entire scope tree in one go, instead of chasing
		// references. additionally, scopes belong to packages, so we
		// can sort them with packages and read and delete them all in
		// one go, without following references.

		// OPT(dh): index as bytes
		key := []byte(fmt.Sprintf("scopes/%s/children/%d", id, i))
		sid := g.encodeScope(scope.Child(i))
		g.kv.Set(key, []byte(fmt.Sprintf("scopes/%s", sid)), 0)
	}

	return id
}

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
	key := []byte(fmt.Sprintf("pkgs/%s\x00objects/%s", path, id))
	g.objToID[obj] = key

	g.encodeType(obj.Type())
	typID := g.typToID.At(obj.Type())
	typ := ""
	switch obj.(type) {
	case *types.Func:
		typ = "func"
	case *types.Var:
		typ = "var"
	case *types.TypeName:
		typ = "typename"
	case *types.Const:
		typ = "const"
	case *types.PkgName:
		typ = "pkgname"
	default:
		log.Println(obj, obj.Pkg())
		panic(fmt.Sprintf("%T", obj))
	}

	// OPT(dh): don't use Sprintf
	var v string
	switch obj := obj.(type) {
	case *types.PkgName:
		v = fmt.Sprintf("%s\x00%s\x00%s\x00%s", obj.Name(), typ, typID, obj.Imported().Path())
	case *types.Const:
		kind, data := encodeConstant(reflect.ValueOf(obj.Val()))
		// OPT(dh): encode data raw, not in base 16. We're currently
		// doing it because null bytes act as field separators.
		v = fmt.Sprintf("%s\x00%s\x00%s\x00%c\x00%s", obj.Name(), typ, typID, kind, hex.EncodeToString(data))
	default:
		v = fmt.Sprintf("%s\x00%s\x00%s", obj.Name(), typ, typID)
	}

	g.kv.Set(key, []byte(v), 0)
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
	key := []byte(fmt.Sprintf("types/%s", id))
	g.typToID.Set(T, key)

	typ := ""
	switch T := T.(type) {
	case *types.Signature:
		typ = "signature"

		g.encodeType(T.Params())
		g.encodeType(T.Results())
		if T.Recv() != nil {
			g.encodeObject(T.Recv())
		}

		key = []byte(fmt.Sprintf("types/%s/params", id))
		g.kv.Set(key, g.typToID.At(T.Params()).([]byte), 0)
		key = []byte(fmt.Sprintf("types/%s/results", id))
		g.kv.Set(key, g.typToID.At(T.Results()).([]byte), 0)
		if T.Recv() != nil {
			key = []byte(fmt.Sprintf("types/%s/recv", id))
			g.kv.Set(key, g.objToID[T.Recv()], 0)
		}
		key = []byte(fmt.Sprintf("types/%s/variadic", id))
		if T.Variadic() {
			g.kv.Set(key, []byte{1}, 0)
		} else {
			// OPT(dh): don't store variadic if it's false
			g.kv.Set(key, []byte{0}, 0)
		}
	case *types.Named:
		typ = "named"

		underlying := T.Underlying()
		g.encodeType(underlying)
		key = []byte(fmt.Sprintf("types/%s/underlying", id))
		g.kv.Set(key, g.typToID.At(underlying).([]byte), 0)

		typename := T.Obj()
		g.encodeObject(typename)
		key = []byte(fmt.Sprintf("types/%s/obj", id))
		g.kv.Set(key, g.objToID[typename], 0)

		for i := 0; i < T.NumMethods(); i++ {
			fn := T.Method(i)
			g.encodeObject(fn)
			// OPT(dh): store index as raw bytes
			key = []byte(fmt.Sprintf("types/%s/method/%d", id, i))
			g.kv.Set(key, g.objToID[fn], 0)
		}
	case *types.Slice:
		typ = "slice"
		elem := T.Elem()
		g.encodeType(elem)
		key = []byte(fmt.Sprintf("types/%s/elem", id))
		g.kv.Set(key, g.typToID.At(elem).([]byte), 0)
	case *types.Pointer:
		typ = "pointer"
		elem := T.Elem()
		g.encodeType(elem)
		key = []byte(fmt.Sprintf("types/%s/elem", id))
		g.kv.Set(key, g.typToID.At(elem).([]byte), 0)
	case *types.Interface:
		typ = "interface"

		for i := 0; i < T.NumExplicitMethods(); i++ {
			fn := T.ExplicitMethod(i)
			g.encodeObject(fn)
			// OPT(dh): store index as bytes
			key = []byte(fmt.Sprintf("types/%s/method/%d", id, i))
			g.kv.Set(key, g.objToID[fn], 0)
		}

		for i := 0; i < T.NumEmbeddeds(); i++ {
			embedded := T.Embedded(i)
			g.encodeType(embedded)
			// OPT(dh): store index as bytes
			key = []byte(fmt.Sprintf("types/%s/embedded/%d", id, i))
			g.kv.Set(key, g.typToID.At(embedded).([]byte), 0)
		}
	case *types.Array:
		typ = "array"

		elem := T.Elem()
		g.encodeType(elem)
		key = []byte(fmt.Sprintf("types/%s/elem", id))
		g.kv.Set(key, g.typToID.At(elem).([]byte), 0)

		n := T.Len()
		key = []byte(fmt.Sprintf("types/%s/len", id))
		// OPT(dh): store the number as raw bytes, not ASCII. We're
		// using ASCII here to make debugging easier.
		g.kv.Set(key, []byte(strconv.Itoa(int(n))), 0)
	case *types.Struct:
		typ = "struct"

		for i := 0; i < T.NumFields(); i++ {
			field := T.Field(i)
			tag := T.Tag(i)
			g.encodeObject(field)

			// OPT(dh): store index as bytes
			key = []byte(fmt.Sprintf("types/%s/field/%d", id, i))
			g.kv.Set(key, g.objToID[field], 0)

			// OPT(dh): store index as bytes
			// OPT(dh): don't store empty tags
			key = []byte(fmt.Sprintf("types/%s/tag/%d", id, i))
			g.kv.Set(key, []byte(tag), 0)
		}
	case *types.Tuple:
		typ = "tuple"

		for i := 0; i < T.Len(); i++ {
			v := T.At(i)
			g.encodeObject(v)
			// OPT(dh): store index as bytes
			key = []byte(fmt.Sprintf("types/%s/item/%d", id, i))
			g.kv.Set(key, g.objToID[v], 0)
		}
	case *types.Map:
		typ = "map"

		g.encodeType(T.Key())
		g.encodeType(T.Elem())
		key := []byte(fmt.Sprintf("types/%s/key", id))
		g.kv.Set(key, g.typToID.At(T.Key()).([]byte), 0)
		key = []byte(fmt.Sprintf("types/%s/elem", id))
		g.kv.Set(key, g.typToID.At(T.Elem()).([]byte), 0)

	case *types.Chan:
		typ = "chan"

		g.encodeType(T.Elem())
		key = []byte(fmt.Sprintf("types/%s/elem", id))
		g.kv.Set(key, g.typToID.At(T.Elem()).([]byte), 0)

		key = []byte(fmt.Sprintf("types/%s/dir", id))
		g.kv.Set(key, []byte{byte(T.Dir())}, 0)
	default:
		panic(fmt.Sprintf("%T", T))
	}
	key = []byte(fmt.Sprintf("types/%s/type", id))
	g.kv.Set(key, []byte(typ), 0)
}

func (g *Graph) Close() error {
	return g.kv.Close()
}
