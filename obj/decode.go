package obj

import (
	"bytes"
	"fmt"
	"go/types"
	"strconv"

	"honnef.co/go/spew"

	"github.com/dgraph-io/badger"
)

func (g *Graph) Package(path string) *types.Package {
	if path == "unsafe" {
		return types.Unsafe
	}
	if pkg, ok := g.pkgs[path]; ok {
		return pkg
	}

	opt := badger.DefaultIteratorOptions
	it := g.kv.NewIterator(opt)
	// XXX check that the package exists
	key := []byte(fmt.Sprintf("pkgs/%s\x00name", path))
	it.Seek(key)
	name := string(it.Item().Value())

	pkg := types.NewPackage(path, name)
	g.pkgs[path] = pkg
	g.idToPkg[fmt.Sprintf("pkgs/%s", path)] = pkg

	var imps []*types.Package
	key = []byte(fmt.Sprintf("pkgs/%s\x00imports/", path))
	for it.Seek(key); it.ValidForPrefix(key); it.Next() {
		imp := string(it.Item().Key()[len(key):])
		imps = append(imps, g.Package(imp))
	}

	key = []byte(fmt.Sprintf("pkgs/%s\x00objects/", path))
	for it.Seek(key); it.ValidForPrefix(key); it.Next() {
		g.decodeObjectItem(it.Item(), pkg)
	}
	it.Close()

	key = []byte(fmt.Sprintf("pkgs/%s\x00scope", path))
	var item badger.KVItem
	g.kv.Get(key, &item)

	g.decodeScope(pkg.Scope(), item.Value())

	pkg.SetImports(imps)
	pkg.MarkComplete()

	return pkg
}

func (g *Graph) getPackage(id []byte) *types.Package {
	return g.idToPkg[string(id)]
}

func (g *Graph) decodeScope(scope *types.Scope, id []byte) {
	prefix := []byte(fmt.Sprintf("%s/objects/", id))
	it := g.kv.NewIterator(badger.DefaultIteratorOptions)
	defer it.Close()
	for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
		obj := g.decodeObject(it.Item().Value())
		scope.Insert(obj)
	}

	prefix = []byte(fmt.Sprintf("%s/children/", id))
	for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
		child := types.NewScope(scope, 0, 0, "")
		g.decodeScope(child, it.Item().Value())
	}
}

func (g *Graph) decodeObject(id []byte) types.Object {
	if obj, ok := g.idToObj[string(id)]; ok {
		return obj
	}
	// OPT(dh): cache these builtin objects
	switch string(id) {
	case "builtin/error":
		return types.Universe.Lookup("error")
	case "builtin/Error":
		return types.Universe.Lookup("error").(*types.TypeName).Type().(*types.Named).Underlying().(*types.Interface).Method(0)
	}
	var item badger.KVItem
	g.kv.Get(id, &item)
	obj := g.decodeObjectItem(&item, nil)
	g.idToObj[string(id)] = obj
	return obj
}

func (g *Graph) decodeObjectItem(item *badger.KVItem, pkg *types.Package) (ret types.Object) {
	key := item.Key()
	if obj, ok := g.idToObj[string(key)]; ok {
		return obj
	}
	defer func() {
		g.idToObj[string(key)] = ret
	}()

	vals := bytes.Split(item.Value(), []byte{'\x00'})
	name, typ, typID := string(vals[0]), string(vals[1]), vals[2]

	T := g.decodeType(typID)
	switch typ {
	case "func":
		// XXX do scope
		return types.NewFunc(0, pkg, name, T.(*types.Signature))
	case "var":
		return types.NewVar(0, pkg, name, T)
	case "typename":
		return types.NewTypeName(0, pkg, name, T)
	case "const":
		kind := vals[3][0]
		data := vals[4]
		val := decodeConstant2(kind, data)
		return types.NewConst(0, pkg, name, T, val)
	case "pkgname":
		path := vals[3]
		return types.NewPkgName(0, pkg, name, g.Package(string(path)))
	default:
		panic(typ)
	}
}

var builtins = map[string]*types.Basic{
	"invalid type": types.Typ[types.Invalid],
	"bool":         types.Typ[types.Bool],
	"int":          types.Typ[types.Int],
	"int8":         types.Typ[types.Int8],
	"int16":        types.Typ[types.Int16],
	"int32":        types.Typ[types.Int32],
	"rune":         types.Typ[types.Int32],
	"int64":        types.Typ[types.Int64],
	"uint":         types.Typ[types.Uint],
	"uint8":        types.Typ[types.Uint8],
	"byte":         types.Typ[types.Uint8],
	"uint16":       types.Typ[types.Uint16],
	"uint32":       types.Typ[types.Uint32],
	"uint64":       types.Typ[types.Uint64],
	"uintptr":      types.Typ[types.Uintptr],
	"float32":      types.Typ[types.Float32],
	"float64":      types.Typ[types.Float64],
	"complex64":    types.Typ[types.Complex64],
	"complex128":   types.Typ[types.Complex128],
	"string":       types.Typ[types.String],
	"Pointer":      types.Typ[types.UnsafePointer],

	"untyped bool":    types.Typ[types.UntypedBool],
	"untyped int":     types.Typ[types.UntypedInt],
	"untyped rune":    types.Typ[types.UntypedRune],
	"untyped float":   types.Typ[types.UntypedFloat],
	"untyped complex": types.Typ[types.UntypedComplex],
	"untyped string":  types.Typ[types.UntypedString],
	"untyped nil":     types.Typ[types.UntypedNil],
}

func (g *Graph) decodeType(id []byte) types.Type {
	if T, ok := g.idToTyp[string(id)]; ok {
		return T
	}

	if bytes.HasPrefix(id, []byte("builtin/")) {
		return builtins[string(id[len("builtin/"):])]
	}

	// XXX verify that key exists
	it := g.kv.NewIterator(badger.DefaultIteratorOptions)
	defer it.Close()
	// OPT(dh): use a struct (union of all fields) instead of a map
	m := map[string]string{}
	for it.Seek(id); it.ValidForPrefix(id); it.Next() {
		item := it.Item()
		idx := bytes.LastIndex(item.Key(), []byte{'/'})
		key := item.Key()[idx+1:]

		m[string(key)] = string(item.Value())
	}

	switch m["type"] {
	case "signature":
		T := new(types.Signature)
		g.idToTyp[string(id)] = T

		var recv *types.Var
		if m["recv"] != "" {
			recv = g.decodeObject([]byte(m["recv"])).(*types.Var)
		}
		params := g.decodeType([]byte(m["params"])).(*types.Tuple)
		results := g.decodeType([]byte(m["results"])).(*types.Tuple)
		variadic := m["variadic"] != "\x00"
		*T = *types.NewSignature(recv, params, results, variadic)

		return T
	case "tuple":
		T := new(types.Tuple)
		g.idToTyp[string(id)] = T

		prefix := []byte(fmt.Sprintf("%s/item/", id))
		// OPT(dh): we don't need a second iterator, the first
		// iterator collects all the keys already
		it := g.kv.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		var vars []*types.Var
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			vars = append(vars, g.decodeObject(it.Item().Value()).(*types.Var))
		}

		if len(vars) > 0 {
			*T = *types.NewTuple(vars...)
		}
		return T
	case "named":
		T := new(types.Named)
		g.idToTyp[string(id)] = T

		obj := g.decodeObject([]byte(m["obj"])).(*types.TypeName)
		underlying := g.decodeType([]byte(m["underlying"]))
		prefix := []byte(fmt.Sprintf("%s/method/", id))
		// OPT(dh): we don't need a second iterator, the first
		// iterator collects all the keys already
		it := g.kv.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		var fns []*types.Func
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			fns = append(fns, g.decodeObject(it.Item().Value()).(*types.Func))
		}
		*T = *types.NewNamed(obj, underlying, fns)
		return T
	case "struct":
		T := new(types.Struct)
		g.idToTyp[string(id)] = T

		var fields []*types.Var
		var tags []string

		prefix := []byte(fmt.Sprintf("%s/field/", id))
		// OPT(dh): we don't need a second iterator, the first
		// iterator collects all the keys already
		it := g.kv.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			fields = append(fields, g.decodeObject(it.Item().Value()).(*types.Var))
		}

		prefix = []byte(fmt.Sprintf("%s/tag/", id))
		// OPT(dh): we don't need a second iterator, the first
		// iterator collects all the keys already
		it = g.kv.NewIterator(badger.DefaultIteratorOptions)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			tags = append(tags, string(it.Item().Value()))
		}

		*T = *types.NewStruct(fields, tags)
		return T
	case "interface":
		T := new(types.Interface)
		g.idToTyp[string(id)] = T

		var fns []*types.Func
		var embeddeds []*types.Named

		prefix := []byte(fmt.Sprintf("%s/method/", id))
		// OPT(dh): we don't need a second iterator, the first
		// iterator collects all the keys already
		it := g.kv.NewIterator(badger.DefaultIteratorOptions)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			fns = append(fns, g.decodeObject(it.Item().Value()).(*types.Func))
		}

		prefix = []byte(fmt.Sprintf("%s/embedded/", id))
		// OPT(dh): we don't need a second iterator, the first
		// iterator collects all the keys already
		it = g.kv.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			embeddeds = append(embeddeds, g.decodeType(it.Item().Value()).(*types.Named))
		}

		*T = *types.NewInterface(fns, embeddeds)
		T.Complete()
		return T
	case "pointer":
		T := new(types.Pointer)
		g.idToTyp[string(id)] = T

		elem := g.decodeType([]byte(m["elem"]))
		*T = *types.NewPointer(elem)

		return T
	case "slice":
		T := new(types.Slice)
		g.idToTyp[string(id)] = T

		elem := g.decodeType([]byte(m["elem"]))
		*T = *types.NewSlice(elem)

		return T
	case "map":
		T := new(types.Map)
		g.idToTyp[string(id)] = T

		key := g.decodeType([]byte(m["key"]))
		elem := g.decodeType([]byte(m["elem"]))
		*T = *types.NewMap(key, elem)

		return T
	case "chan":
		T := new(types.Chan)
		g.idToTyp[string(id)] = T

		elem := g.decodeType([]byte(m["elem"]))
		dir := m["dir"][0]
		*T = *types.NewChan(types.ChanDir(dir), elem)

		return T
	case "array":
		T := new(types.Array)
		g.idToTyp[string(id)] = T

		elem := g.decodeType([]byte(m["elem"]))
		n, _ := strconv.Atoi(m["len"])
		*T = *types.NewArray(elem, int64(n))

		return T
	default:
		spew.Dump(m)
		panic(m["type"])
	}
}
