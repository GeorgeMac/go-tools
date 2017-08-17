package obj

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"go/types"

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

	g.scopes[pkg] = map[string][]byte{}

	prefix := []byte(fmt.Sprintf("pkgs/%s\x00scopes/", pkg.Path()))
	it.Rewind()
	for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
		cp := make([]byte, len(it.Item().Value()))
		copy(cp, it.Item().Value())

		g.scopes[pkg][string(it.Item().Key())] = cp
	}
	g.decodeScope(g.scopes[pkg], pkg.Scope(), item.Value())

	// TODO(dh): clear g.scopes[pkg]

	pkg.SetImports(imps)
	pkg.MarkComplete()

	return pkg
}

func (g *Graph) decodeScope(scopes map[string][]byte, scope *types.Scope, id []byte) {
	vals := decodeBytes(scopes[string(id)])
	n, _ := binary.Uvarint(vals[0])
	for i := uint64(0); i < n; i++ {
		obj := g.decodeObject(vals[i+1])
		scope.Insert(obj)
	}

	vals = vals[n+1:]
	n, _ = binary.Uvarint(vals[0])
	for i := uint64(0); i < n; i++ {
		child := types.NewScope(scope, 0, 0, "")
		g.decodeScope(scopes, child, vals[i+1])
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

	vals := decodeBytes(item.Value())
	name, typ, typID := string(vals[0]), vals[1], vals[2]

	T := g.decodeType(typID)
	switch typ[0] {
	case kindFunc:
		// XXX do scope
		return types.NewFunc(0, pkg, name, T.(*types.Signature))
	case kindVar:
		return types.NewVar(0, pkg, name, T)
	case kindTypename:
		return types.NewTypeName(0, pkg, name, T)
	case kindConst:
		kind := vals[3][0]
		data := vals[4]
		val := decodeConstant2(kind, data)
		return types.NewConst(0, pkg, name, T, val)
	case kindPkgname:
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

func decodeBytes(b []byte) [][]byte {
	var out [][]byte
	for {
		if len(b) == 0 {
			break
		}
		n, l := binary.Uvarint(b)
		b = b[l:]
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

func (g *Graph) decodeType(id []byte) types.Type {
	if T, ok := g.idToTyp[string(id)]; ok {
		return T
	}

	if bytes.HasPrefix(id, []byte("builtin/")) {
		return builtins[string(id[len("builtin/"):])]
	}

	var item badger.KVItem
	// XXX verify that key exists
	g.kv.Get(id, &item)

	vals := decodeBytes(item.Value())
	switch vals[0][0] {
	case kindSignature:
		T := new(types.Signature)
		g.idToTyp[string(id)] = T

		vparams := vals[1]
		vresults := vals[2]
		vrecv := vals[3]
		vvariadic := vals[4]

		var recv *types.Var
		if len(vrecv) != 0 {
			recv = g.decodeObject(vrecv).(*types.Var)
		}
		params := g.decodeType(vparams).(*types.Tuple)
		results := g.decodeType(vresults).(*types.Tuple)
		variadic := vvariadic[0] != 0
		*T = *types.NewSignature(recv, params, results, variadic)

		return T
	case kindTuple:
		T := new(types.Tuple)
		g.idToTyp[string(id)] = T

		var vars []*types.Var
		for _, val := range vals[1:] {
			vars = append(vars, g.decodeObject(val).(*types.Var))
		}

		if len(vars) > 0 {
			*T = *types.NewTuple(vars...)
		}
		return T
	case kindNamed:
		T := new(types.Named)
		g.idToTyp[string(id)] = T

		vunderlying := vals[1]
		vobj := vals[2]

		obj := g.decodeObject(vobj).(*types.TypeName)
		underlying := g.decodeType(vunderlying)

		var fns []*types.Func
		for _, val := range vals[3:] {
			fns = append(fns, g.decodeObject(val).(*types.Func))
		}

		*T = *types.NewNamed(obj, underlying, fns)
		return T
	case kindStruct:
		T := new(types.Struct)
		g.idToTyp[string(id)] = T

		var fields []*types.Var
		var tags []string

		if len(vals) > 1 {
			for i := 1; i < len(vals); i += 2 {
				field := vals[i]
				tag := vals[i+1]

				fields = append(fields, g.decodeObject(field).(*types.Var))
				tags = append(tags, string(tag))
			}
		}

		*T = *types.NewStruct(fields, tags)
		return T
	case kindInterface:
		T := new(types.Interface)
		g.idToTyp[string(id)] = T

		var fns []*types.Func
		var embeddeds []*types.Named

		n, _ := binary.Uvarint(vals[1])
		vals = vals[2:]
		for i := uint64(0); i < n; i++ {
			fns = append(fns, g.decodeObject(vals[i]).(*types.Func))
		}

		vals = vals[n:]
		n, _ = binary.Uvarint(vals[0])
		vals = vals[1:]
		for i := uint64(0); i < n; i++ {
			embeddeds = append(embeddeds, g.decodeType(vals[i]).(*types.Named))
		}

		*T = *types.NewInterface(fns, embeddeds)
		T.Complete()
		return T
	case kindPointer:
		T := new(types.Pointer)
		g.idToTyp[string(id)] = T

		elem := g.decodeType([]byte(vals[1]))
		*T = *types.NewPointer(elem)

		return T
	case kindSlice:
		T := new(types.Slice)
		g.idToTyp[string(id)] = T

		elem := g.decodeType([]byte(vals[1]))
		*T = *types.NewSlice(elem)

		return T
	case kindMap:
		T := new(types.Map)
		g.idToTyp[string(id)] = T

		key := g.decodeType([]byte(vals[1]))
		elem := g.decodeType([]byte(vals[2]))
		*T = *types.NewMap(key, elem)

		return T
	case kindChan:
		T := new(types.Chan)
		g.idToTyp[string(id)] = T

		elem := g.decodeType(vals[1])
		dir := vals[2][0]
		*T = *types.NewChan(types.ChanDir(dir), elem)

		return T
	case kindArray:
		T := new(types.Array)
		g.idToTyp[string(id)] = T

		elem := g.decodeType(vals[1])
		n, _ := binary.Uvarint(vals[2])
		*T = *types.NewArray(elem, int64(n))

		return T
	default:
		panic(string(vals[0]))
	}
}
