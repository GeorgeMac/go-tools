// +build ignore

package obj

import (
	"encoding/binary"
	"errors"
	"fmt"
	"go/types"
	"io"
	"log"
	"reflect"
	"unsafe"

	"honnef.co/go/tools/typeutil"
)

/*
objects/types in a scope cannot refer to objects in unknown scopes,
except for package scopes. the only unknown scopes are child and
sibling scopes, neither of which are accessible to the current scope.

furthermore, they cannot refer to unknown objects in known scopes
(except for package scopes), either. all objects in the current and in
parent scopes that can be referred to must have occured before the
object in question, lexically. ← well, at least that'd be true of
Scope.Names WASNT FUCKING SORTED
*/

/*
Func objects can only exist on the package level and in package, or
no, scopes. Only declared functions, methods and interface methods
show up as Func objects, but not closures.

methods can only exist on named types, so that's how we can
canonicalize them.

*/

/*

we need to canonicalize all objects when importing multiple packages,
as packages will contain definitions for external package-level
symbols they depend on.

we don't need to canonicalize scopes or objects in non-pkg scopes,
though. only one archive encodes nested scopes, everything else is on
the top level.

*/

// OPT(dh): use variable length integers
// OPT(dh): deduplicate strings

type TypesDecoder struct {
	r      io.Reader
	pkgs   map[string]*types.Package
	ids    map[int64]interface{}
	scopes map[int64]*types.Scope

	curPkg   *types.Package
	curScope *types.Scope

	typesDone bool

	ifaces []*types.Interface
}

// NewTypesDecoder creates a new type data decoder. The packages
// argument should contain all packages, keyed by import path, already
// known to the type system. This list is used to cannonicalize
// imported packages. Imported packages will get added to the map.
func NewTypesDecoder(packages map[string]*types.Package) *TypesDecoder {
	dec := &TypesDecoder{pkgs: packages}
	return dec
}

func (dec *TypesDecoder) init() {
	dec.curPkg = nil
	dec.curScope = nil
	dec.typesDone = false
	dec.ids = map[int64]interface{}{}
	dec.scopes = map[int64]*types.Scope{}
	dec.ifaces = nil
	dec.ids[0] = nil
	for i, T := range types.Typ {
		dec.ids[int64(i)+1] = T
	}

	n := len(dec.ids)
	for i, name := range types.Universe.Names() {
		obj := types.Universe.Lookup(name)
		dec.ids[int64(n)+int64(i)] = obj
	}
}

func (dec *TypesDecoder) Object(id uint64) interface{} {
	if id == 0 {
		return nil
	}
	return dec.ids[int64(id)]
}

func (dec *TypesDecoder) id() (int64, error) {
	return dec.int()
}

func (dec *TypesDecoder) int() (int64, error) {
	var v int64
	err := binary.Read(dec.r, binary.LittleEndian, &v)
	return v, err
}

func (dec *TypesDecoder) bool() (bool, error) {
	b := make([]byte, 1)
	_, err := io.ReadFull(dec.r, b)
	return b[0] != 0, err
}

func (dec *TypesDecoder) string() (string, error) {
	n, err := dec.int()
	if err != nil {
		return "", err
	}
	if n < 0 {
		return "", errors.New("negative string length")
	}
	if n > 100*1000*1000 { // 100 MB
		return "", errors.New("string value too large (>100MB)")
	}
	b := make([]byte, n)
	_, err = io.ReadFull(dec.r, b)
	return string(b), err
}

func (dec *TypesDecoder) qualifiedName() (string, *types.Package, *types.Scope, error) {
	name, err := dec.string()
	if err != nil {
		return "", nil, nil, err
	}
	pkg, err := dec.pkg()
	if err != nil {
		return "", nil, nil, err
	}
	scope, _, err := dec.decodeScope()
	if err != nil {
		return "", nil, nil, err
	}
	return name, pkg, scope, nil
}

type unsafePackage struct {
	_     string
	_     string
	scope *types.Scope
}

type unsafeSignature struct {
	scope    *types.Scope
	recv     *types.Var
	_        [2]uintptr
	variadic bool
}

// Decode decodes a single package archive and returns the contained
// package. It populates a mapping of IDs to types and objects, which
// can be queried with Object. It is only valid until the next call to
// Decode.
func (dec *TypesDecoder) Decode(r io.Reader) (*types.Package, error) {
	dec.init()
	dec.r = r
	name, err := dec.string()
	if err != nil {
		return nil, err
	}
	path, err := dec.string()
	if err != nil {
		return nil, err
	}
	pkg := dec.pkgs[path]
	if pkg == nil {
		pkg = types.NewPackage(path, name)
		dec.pkgs[path] = pkg
	}
	if pkg.Name() != name {
		log.Fatal("XXX inconsistent")
	}

	dec.curPkg = pkg
	dec.curScope = types.Universe
	scope, err := dec.decodeScopeRecursively()
	if err != nil {
		return nil, err
	}

	(*unsafePackage)(unsafe.Pointer(pkg)).scope = scope

	// Decode all imported top-level symbols
	n, err := dec.int()
	if err != nil {
		return nil, err
	}
	for i := int64(0); i < n; i++ {
		name, err := dec.string()
		if err != nil {
			return nil, err
		}
		path, err := dec.string()
		if err != nil {
			return nil, err
		}
		pkg := dec.pkgs[path]
		if pkg == nil {
			pkg = types.NewPackage(path, name)
			dec.pkgs[path] = pkg
		}
		if pkg.Name() != name {
			log.Fatal("XXX inconsistent")
		}
		dec.curPkg = pkg
		dec.curScope = types.Universe
		scope, _, err := dec.decodeScope()
		if err != nil {
			return nil, err
		}

		(*unsafePackage)(unsafe.Pointer(pkg)).scope = scope
	}

	// Decode additional types that were appended. Those are things
	// like generic builtins, comma-ok assignments and so on.
	for !dec.typesDone {
		dec.typ()
	}

	// Decode additional objects that were appended. Those are objects
	// that do not exist in any scope, such as blank identifiers and
	// init functions.
	for {
		_, err := dec.obj()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}

	var imps []*types.Package
	for _, imp := range dec.pkgs {
		if imp == pkg {
			continue
		}
		// FIXME(dh): go/types says the list is in source order
		imps = append(imps, imp)
	}

	for _, iface := range dec.ifaces {
		iface.Complete()
	}
	pkg.SetImports(imps)
	pkg.MarkComplete()
	return pkg, nil
}

func (dec *TypesDecoder) obj() (types.Object, error) {
	tagOrID, err := dec.id()
	if err != nil {
		return nil, err
	}
	if tagOrID == 0 {
		return nil, nil
	}
	if tagOrID > 0 {
		obj, ok := dec.ids[tagOrID].(types.Object)
		if !ok {
			return nil, fmt.Errorf("ID %d should point to Object, but points to %T",
				tagOrID, dec.ids[tagOrID])
		}
		return obj, nil
	}

	id, err := dec.id()
	if err != nil {
		return nil, err
	}
	var obj types.Object
	switch tagOrID {
	case varTag:
		V := &types.Var{}
		dec.ids[id] = V

		name, pkg, scope, err := dec.qualifiedName()
		if err != nil {
			return nil, err
		}
		anon, err := dec.bool()
		if err != nil {
			return nil, err
		}
		field, err := dec.bool()
		if err != nil {
			return nil, err
		}
		T, err := dec.typ()
		if err != nil {
			return nil, err
		}
		if field {
			*V = *types.NewField(0, pkg, name, T, anon)
		} else {
			*V = *types.NewVar(0, pkg, name, T)
		}
		if scope != nil {
			scope.Insert(V)
		}
		obj = V
	case typeNameTag:
		V := &types.TypeName{}
		dec.ids[id] = V

		name, pkg, scope, err := dec.qualifiedName()
		if err != nil {
			return nil, err
		}

		if pkg != dec.curPkg {
			v := pkg.Scope().Lookup(name)
			if v != nil {
				vv, ok := v.(*types.TypeName)
				if !ok {
					log.Println(pkg.Scope())
					return nil, fmt.Errorf("type inconsistency, %s is both TypeName and %T", name, v)
				}
				dec.ids[id] = vv

				// read ignored data
				dec.typ()
				return vv, nil
			}
		}

		var named *types.TypeName
		if pkg == nil {
			named, _ = types.Universe.Lookup(name).(*types.TypeName)
			if named == nil {
				log.Fatalln("couldn't find built-in type", name)
			}
		} else if scope != nil {
			named, _ = scope.Lookup(name).(*types.TypeName)
		}
		// if _, ok := named.(*types.TypeName); !ok && named != nil {
		// 	return nil, fmt.Errorf("expected TypeName, got %T", named)
		// }
		if named == nil {
			named = types.NewTypeName(0, pkg, name, nil)
		}
		*V = *named
		if scope != nil {
			scope.Insert(V)
		}
		if _, err := dec.typ(); err != nil {
			return nil, err
		}
		obj = V
	case builtinTag:
		name, err := dec.string()
		if err != nil {
			return nil, err
		}
		obj = types.Universe.Lookup(name)
		dec.ids[id] = obj
	case funcTag:
		V := &types.Func{}
		dec.ids[id] = V

		name, pkg, _, err := dec.qualifiedName()
		if err != nil {
			return nil, err
		}
		t, err := dec.typ()
		if err != nil {
			return nil, err
		}
		T, ok := t.(*types.Signature)
		if !ok {
			return nil, fmt.Errorf("expected Signature, got %T", t)
		}
		iface, err := dec.bool()
		if err != nil {
			return nil, err
		}
		fscope, err := dec.decodeScopeRecursively()
		if err != nil {
			return nil, err
		}
		(*unsafeSignature)(unsafe.Pointer(T)).scope = fscope
		*V = *types.NewFunc(0, pkg, name, T)
		// XXX do we need to insert into the scope sooner?
		if T.Recv() == nil && !iface {
			if pkg == nil {
				// XXX universe
			} else {
				pkg.Scope().Insert(V)
			}
		}
		obj = V
	case constTag:
		V := &types.Const{}
		dec.ids[id] = V

		name, pkg, scope, err := dec.qualifiedName()
		if err != nil {
			return nil, err
		}
		T, err := dec.typ()
		if err != nil {
			return nil, err
		}

		kind, err := dec.int()
		if err != nil {
			return nil, err
		}
		n, err := dec.int()
		if err != nil {
			return nil, err
		}
		if n < 0 {
			panic("XXX internal error")
		}
		if n > 100*1000*1000 {
			panic("XXX internal error")
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(dec.r, b); err != nil {
			return nil, err
		}
		v := decodeConstant2(byte(kind), b)
		*V = *types.NewConst(0, pkg, name, T, v)
		if scope != nil {
			// XXX do we need to insert into the scope sooner?
			scope.Insert(V)
		}
		obj = V
	case nilTag:
		obj = types.Universe.Lookup("nil")
		dec.ids[id] = obj
	case labelTag:
		V := &types.Label{}
		dec.ids[id] = V

		name, pkg, scope, err := dec.qualifiedName()
		if err != nil {
			return nil, err
		}
		*V = *types.NewLabel(0, pkg, name)
		if scope != nil {
			scope.Insert(V)
		}
	case pkgNameTag:
		V := &types.PkgName{}
		dec.ids[id] = V

		name, pkg, scope, err := dec.qualifiedName()
		if err != nil {
			return nil, err
		}
		imp, err := dec.pkg()
		if err != nil {
			return nil, err
		}
		*V = *types.NewPkgName(0, pkg, name, imp)
		if scope != nil {
			scope.Insert(V)
		}
		obj = V
	case scopeTag:
		return nil, errors.New("saw Scope incorrectly encoded as object")
	default:
		return nil, fmt.Errorf("unknown tag %d", tagOrID)
	}
	return obj, nil
}

func (dec *TypesDecoder) record(id int64, typ types.Type) {
	dec.ids[id] = typ
}

func (dec *TypesDecoder) typ() (types.Type, error) {
	tagOrID, err := dec.id()
	if err != nil {
		return nil, err
	}
	if tagOrID == endTypesTag {
		dec.typesDone = true
		return nil, nil
	}
	if tagOrID == 0 {
		return nil, nil
	}
	if tagOrID > 0 {
		T, ok := dec.ids[tagOrID].(types.Type)
		if !ok {
			return nil, fmt.Errorf("ID %d should point to Type, but points to %T",
				tagOrID, dec.ids[tagOrID])
		}
		return T, nil
	}
	id, err := dec.id()
	if err != nil {
		return nil, err
	}
	switch tagOrID {
	case tupleTag:
		t := new(types.Tuple)
		dec.record(id, t)

		n, err := dec.int()
		if err != nil {
			return nil, err
		}
		var fields []*types.Var
		for i := int64(0); i < n; i++ {
			obj, err := dec.obj()
			if err != nil {
				return nil, err
			}
			field, ok := obj.(*types.Var)
			if !ok {
				return nil, fmt.Errorf("expected Var, got %T", obj)
			}
			fields = append(fields, field)
		}
		if len(fields) == 0 {
			return t, nil
		}
		*t = *types.NewTuple(fields...)
		return t, nil
	case mapTag:
		t := new(types.Map)
		dec.record(id, t)

		key, err := dec.typ()
		if err != nil {
			return nil, err
		}
		val, err := dec.typ()
		if err != nil {
			return nil, err
		}
		*t = *types.NewMap(key, val)
		return t, nil
	case chanTag:
		t := new(types.Chan)
		dec.record(id, t)

		v, err := dec.int()
		if err != nil {
			return nil, err
		}
		dir := types.ChanDir(v)
		T, err := dec.typ()
		if err != nil {
			return nil, err
		}
		*t = *types.NewChan(dir, T)
		return t, nil
	case arrayTag:
		t := new(types.Array)
		dec.record(id, t)

		n, err := dec.int()
		if err != nil {
			return nil, err
		}
		elem, err := dec.typ()
		if err != nil {
			return nil, err
		}
		*t = *types.NewArray(elem, n)
		return t, nil
	case namedTag:
		t := new(types.Named)
		dec.record(id, t)

		obj, err := dec.obj()
		if err != nil {
			return nil, err
		}
		name, ok := obj.(*types.TypeName)
		if !ok {
			return nil, fmt.Errorf("expected TypeName, got %T", obj)
		}
		T, err := dec.typ()
		if err != nil {
			return nil, err
		}
		n, err := dec.int()
		if err != nil {
			return nil, err
		}
		var meths []*types.Func
		for i := int64(0); i < n; i++ {
			obj, err := dec.obj()
			if err != nil {
				return nil, err
			}
			fn, ok := obj.(*types.Func)
			if !ok {
				return nil, fmt.Errorf("expected Func, got %T", obj)
			}
			meths = append(meths, fn)
		}
		*t = *types.NewNamed(name, T, meths)
		return t, nil
	case structTag:
		t := new(types.Struct)
		dec.record(id, t)

		n, err := dec.int()
		if err != nil {
			return nil, err
		}
		var fields []*types.Var
		var tags []string
		for i := int64(0); i < n; i++ {
			obj, err := dec.obj()
			if err != nil {
				return nil, err
			}
			field, ok := obj.(*types.Var)
			if !ok {
				return nil, fmt.Errorf("expected Var, got %T", obj)
			}
			fields = append(fields, field)
			s, err := dec.string()
			if err != nil {
				return nil, err
			}
			tags = append(tags, s)
		}
		*t = *types.NewStruct(fields, tags)
		return t, nil
	case sliceTag:
		t := new(types.Slice)
		dec.record(id, t)

		elem, err := dec.typ()
		if err != nil {
			return nil, err
		}
		*t = *types.NewSlice(elem)
		return t, nil
	case ptrTag:
		t := new(types.Pointer)
		dec.record(id, t)

		T, err := dec.typ()
		if err != nil {
			return nil, err
		}
		*t = *types.NewPointer(T)
		return t, nil
	case signatureTag:
		t := new(types.Signature)
		dec.record(id, t)

		tt, err := dec.typ()
		if err != nil {
			return nil, err
		}
		params, ok := tt.(*types.Tuple)
		if !ok {
			return nil, fmt.Errorf("expected Tuple, got %T", tt)
		}
		tt, err = dec.typ()
		if err != nil {
			return nil, err
		}
		results, ok := tt.(*types.Tuple)
		if !ok {
			return nil, fmt.Errorf("expected Tuple, got %T", tt)
		}
		variadic, err := dec.bool()
		if err != nil {
			return nil, err
		}
		obj, err := dec.obj()
		if err != nil {
			return nil, err
		}
		recv, _ := obj.(*types.Var)
		*t = *types.NewSignature(recv, params, results, false)
		// append can have variadic strings, in the form of
		// `append([]byte, string...)`. NewSignature will not let us
		// do this, so we use unsafe.
		(*unsafeSignature)(unsafe.Pointer(t)).variadic = variadic
		return t, nil
	case interfaceTag:
		t := new(types.Interface)
		dec.record(id, t)

		n, err := dec.int()
		if err != nil {
			return nil, err
		}
		var embedded []*types.Named
		for i := int64(0); i < n; i++ {
			tt, err := dec.typ()
			if err != nil {
				return nil, err
			}
			named, ok := tt.(*types.Named)
			if !ok {
				return nil, fmt.Errorf("expected Named, got %T", tt)
			}
			embedded = append(embedded, named)
		}

		n, err = dec.int()
		if err != nil {
			return nil, err
		}
		var meths []*types.Func
		for i := int64(0); i < n; i++ {
			obj, err := dec.obj()
			if err != nil {
				return nil, err
			}
			fn, ok := obj.(*types.Func)
			if !ok {
				return nil, fmt.Errorf("expected Func, got %T", obj)
			}
			meths = append(meths, fn)
		}
		*t = *types.NewInterface(meths, embedded)
		dec.ifaces = append(dec.ifaces, t)
		return t, nil
	default:
		return nil, fmt.Errorf("unknown tag %d", tagOrID)
	}
}

func (dec *TypesDecoder) pkg() (*types.Package, error) {
	tagOrID, err := dec.id()
	if err != nil {
		return nil, err
	}
	if tagOrID == 0 {
		return nil, nil
	}
	if tagOrID > 0 {
		pkg, ok := dec.ids[tagOrID].(*types.Package)
		if !ok {
			return nil, fmt.Errorf("ID %d should point to package, but points to %T",
				tagOrID, dec.ids[tagOrID])
		}
		return pkg, nil
	}
	if tagOrID != pkgTag {
		return nil, fmt.Errorf("expected to read package, but got tag %d", tagOrID)
	}

	id, err := dec.id()
	if err != nil {
		return nil, err
	}
	name, err := dec.string()
	if err != nil {
		return nil, err
	}
	path, err := dec.string()
	if err != nil {
		return nil, err
	}

	// XXX verify name and path
	pkg := dec.pkgs[path]
	if pkg == nil {
		pkg = types.NewPackage(path, name)
		dec.pkgs[path] = pkg
	} else if pkg.Name() != name {
		return nil, fmt.Errorf("conflicting names %s and %s for package %q",
			pkg.Name(), name, path)
	}
	dec.ids[id] = pkg
	return pkg, nil
}

func (dec *TypesDecoder) decodeScope() (scope *types.Scope, existing bool, err error) {
	tagOrID, err := dec.id()
	if err != nil {
		return nil, false, err
	}
	if tagOrID == 0 {
		return nil, true, nil
	}
	if tagOrID > 0 {
		return dec.scopes[tagOrID], true, nil
	}
	scope = types.NewScope(dec.curScope, 0, 0, "")
	dec.curScope = scope

	id, err := dec.id()
	if err != nil {
		return nil, false, err
	}
	dec.scopes[id] = scope
	n, err := dec.int()
	if err != nil {
		return nil, false, err
	}
	for i := int64(0); i < n; i++ {
		obj, err := dec.obj()
		if err != nil {
			return nil, false, err
		}
		if obj == nil {
			return nil, false, errors.New("encountered nil Object in scope")
		}
		scope.Insert(obj)
	}

	return scope, false, nil
}

func (dec *TypesDecoder) decodeScopeRecursively() (*types.Scope, error) {
	prevScope := dec.curScope
	defer func() {
		dec.curScope = prevScope
	}()

	scope, existing, err := dec.decodeScope()
	if err != nil {
		return nil, err
	}
	if scope == nil || existing {
		return scope, nil
	}

	n, err := dec.int()
	if err != nil {
		return nil, err
	}
	for i := int64(0); i < n; i++ {
		dec.decodeScopeRecursively()
	}

	return scope, nil
}

type TypesEncoder struct {
	w        io.Writer
	typeIDs  *typeutil.Map
	objIDs   map[types.Object]int64
	pkgIDs   map[*types.Package]int64
	scopeIDs map[*types.Scope]int64

	curPkg *types.Package
}

func (enc *TypesEncoder) ID(v interface{}) (int64, bool) {
	if v == nil {
		return 0, true
	}
	var id int64
	var ok bool
	switch v := v.(type) {
	case types.Object:
		id, ok = enc.objIDs[v]
	case types.Type:
		id, ok = enc.typeIDs.At(v).(int64)
	case *types.Package:
		id, ok = enc.pkgIDs[v]
	case *types.Scope:
		id, ok = enc.scopeIDs[v]
	default:
		panic(fmt.Sprintf("invalid type %T", v))
	}
	return id, ok
}

func NewTypesEncoder(w io.Writer) *TypesEncoder {
	enc := &TypesEncoder{
		w:        w,
		typeIDs:  &typeutil.Map{},
		objIDs:   map[types.Object]int64{},
		pkgIDs:   map[*types.Package]int64{},
		scopeIDs: map[*types.Scope]int64{},
	}

	for i, T := range types.Typ {
		enc.typeIDs.Set(T, int64(i)+1)
	}

	n := enc.typeIDs.Len() + 1
	for i, name := range types.Universe.Names() {
		obj := types.Universe.Lookup(name)
		enc.objIDs[obj] = int64(n) + int64(i)
	}

	return enc
}

func (enc *TypesEncoder) Encode(pkg *types.Package) {
	enc.curPkg = pkg

	enc.string(pkg.Name())
	enc.string(pkg.Path())
	// recursively encode all named objects in all scopes
	enc.encodeScopeRecursively(pkg.Scope())

	// encode all exported symbols in directly imported packages
	enc.int(int64(len(pkg.Imports())))
	for _, imp := range pkg.Imports() {
		enc.encodeImport(imp)
	}

	// XXX figure out why some fields aren't being encoded in scopes
	// but show up in Defs. Some fields only show up in Uses.
}

func (enc *TypesEncoder) encodeImport(pkg *types.Package) {
	enc.string(pkg.Name())
	enc.string(pkg.Path())
	enc.encodeScope(pkg.Scope())
}

func (enc *TypesEncoder) encodeScope(scope *types.Scope) {
	if scope == nil {
		enc.id(0)
		return
	}

	idx, ok := enc.scopeIDs[scope]
	if ok {
		enc.id(idx)
		return
	}

	enc.tag(scopeTag)
	enc.newScopeID(scope)
	names := scope.Names()
	enc.int(int64(len(names)))
	for _, name := range names {
		obj := scope.Lookup(name)
		enc.encodeObj(obj)
	}
}

func (enc *TypesEncoder) encodeScopeRecursively(scope *types.Scope) {
	if scope == nil {
		enc.id(0)
		return
	}

	idx, ok := enc.scopeIDs[scope]
	if ok {
		enc.id(idx)
		return
	}

	enc.encodeScope(scope)

	n := scope.NumChildren()
	enc.int(int64(n))
	for i := 0; i < n; i++ {
		enc.encodeScopeRecursively(scope.Child(i))
	}
}

func (enc *TypesEncoder) EncodeObject(obj types.Object) {
	enc.encodeObj(obj)
}

func (enc *TypesEncoder) EncodeType(T types.Type) {
	enc.encodeType(T)
}

// XXX encode all positions

func (enc *TypesEncoder) encodeObj(obj types.Object) {
	if obj == nil {
		enc.id(0)
		return
	}
	idx, ok := enc.objIDs[obj]
	if ok {
		enc.id(idx)
		return
	}

	switch obj := obj.(type) {
	case *types.Var:
		enc.encodeVar(obj)
	case *types.TypeName:
		enc.encodeTypeName(obj)
	case *types.Builtin:
		enc.encodeBuiltin(obj)
	case *types.Func:
		enc.encodeFunc(obj)
	case *types.Const:
		enc.encodeConst(obj)
	case *types.PkgName:
		enc.encodePkgName(obj)
	case *types.Nil:
		enc.encodeNil(obj)
	case *types.Label:
		enc.encodeLabel(obj)
	default:
		panic(fmt.Sprintf("unexpected type %T", obj))
	}
}

func (enc *TypesEncoder) pkg(pkg *types.Package) {
	if pkg == nil {
		enc.id(0)
		return
	}
	if id, ok := enc.pkgIDs[pkg]; ok {
		enc.id(id)
		return
	}
	enc.tag(pkgTag)
	enc.newPkgID(pkg)
	enc.string(pkg.Name())
	enc.string(pkg.Path())
}

func (enc *TypesEncoder) qualifiedName(obj types.Object) {
	enc.string(obj.Name())
	enc.pkg(obj.Pkg())
	enc.encodeScope(obj.Parent())
}

func (enc *TypesEncoder) encodeVar(obj *types.Var) {
	enc.tag(varTag)
	enc.newObjID(obj)
	enc.qualifiedName(obj)
	enc.bool(obj.Anonymous())
	enc.bool(obj.IsField())
	enc.encodeType(obj.Type())
}

func (enc *TypesEncoder) encodeTypeName(obj *types.TypeName) {
	enc.tag(typeNameTag)
	enc.newObjID(obj)
	enc.qualifiedName(obj)
	enc.encodeType(obj.Type())
}

func (enc *TypesEncoder) encodeBuiltin(obj *types.Builtin) {
	enc.tag(builtinTag)
	enc.newObjID(obj)
	enc.string(obj.Name())
}

func (enc *TypesEncoder) encodeFunc(obj *types.Func) {
	enc.tag(funcTag)
	enc.newObjID(obj)
	enc.qualifiedName(obj)
	enc.encodeType(obj.Type())
	recv := obj.Type().(*types.Signature).Recv()
	enc.bool(recv != nil && types.IsInterface(recv.Type()))
	if obj.Pkg() == enc.curPkg {
		enc.encodeScopeRecursively(obj.Scope())
	} else {
		enc.encodeScopeRecursively(nil)
	}
}

func (enc *TypesEncoder) encodeConst(obj *types.Const) {
	enc.tag(constTag)
	enc.newObjID(obj)

	enc.qualifiedName(obj)
	enc.encodeType(obj.Type())
	kind, data := encodeConstant(reflect.ValueOf(obj.Val()))
	// OPT(dh): don't use 64 bits to store one byte
	enc.int(int64(kind))
	enc.int(int64(len(data)))
	enc.w.Write(data)
}

func (enc *TypesEncoder) encodeNil(obj *types.Nil) {
	enc.tag(nilTag)
	enc.newObjID(obj)
}

func (enc *TypesEncoder) encodeLabel(obj *types.Label) {
	enc.tag(labelTag)
	enc.newObjID(obj)
	enc.qualifiedName(obj)
}

func (enc *TypesEncoder) encodePkgName(obj *types.PkgName) {
	enc.tag(pkgNameTag)
	enc.newObjID(obj)
	enc.qualifiedName(obj)
	enc.pkg(obj.Imported())
}

func (enc *TypesEncoder) encodeType(T types.Type) {
	idx, ok := enc.typeIDs.At(T).(int64)
	if ok {
		enc.id(idx)
		return
	}

	switch T := T.(type) {
	case *types.Array:
		enc.encodeArray(T)
	case *types.Named:
		enc.encodeNamed(T)
	case *types.Struct:
		enc.encodeStruct(T)
	case *types.Slice:
		enc.encodeSlice(T)
	case *types.Signature:
		enc.encodeSignature(T)
	case *types.Basic:
		panic("internal error: all basic types should exist statically")
	case *types.Interface:
		enc.encodeInterface(T)
	case *types.Pointer:
		enc.encodePtr(T)
	case *types.Tuple:
		enc.encodeTuple(T)
	case *types.Map:
		enc.encodeMap(T)
	case *types.Chan:
		enc.encodeChan(T)
	default:
		panic(fmt.Sprintf("unexpected type %T", T))
	}
}

func (enc *TypesEncoder) bool(b bool) {
	if b {
		enc.w.Write([]byte{1})
	} else {
		enc.w.Write([]byte{0})
	}
}

func (enc *TypesEncoder) int(v int64) {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(v))
	enc.w.Write(b)
}

func (enc *TypesEncoder) newID() int64 {
	return int64(enc.typeIDs.Len()) +
		int64(len(enc.objIDs)) +
		int64(len(enc.pkgIDs)) +
		int64(len(enc.scopeIDs))
}

func (enc *TypesEncoder) newTypeID(T types.Type) {
	id := enc.newID()
	enc.typeIDs.Set(T, id)
	enc.id(id)
}

func (enc *TypesEncoder) newObjID(obj types.Object) {
	id := enc.newID()
	enc.objIDs[obj] = id
	enc.id(id)
}

func (enc *TypesEncoder) newScopeID(scope *types.Scope) {
	id := enc.newID()
	enc.scopeIDs[scope] = id
	enc.id(id)
}

func (enc *TypesEncoder) newPkgID(pkg *types.Package) {
	id := enc.newID()
	enc.pkgIDs[pkg] = id
	enc.id(id)
}

func (enc *TypesEncoder) id(id int64) {
	enc.int(id)
}

func (enc *TypesEncoder) tag(tag int64) {
	if tag >= 0 {
		panic("tag mustn't be positive")
	}
	enc.int(tag)
}

func (enc *TypesEncoder) string(s string) {
	enc.int(int64(len(s)))
	enc.w.Write([]byte(s))
}

func (enc *TypesEncoder) encodeArray(T *types.Array) {
	enc.tag(arrayTag)
	enc.newTypeID(T)
	enc.int(T.Len())
	enc.encodeType(T.Elem())
}

func (enc *TypesEncoder) encodeInterface(T *types.Interface) {
	enc.tag(interfaceTag)
	enc.newTypeID(T)

	n := T.NumEmbeddeds()
	enc.int(int64(n))
	for i := 0; i < n; i++ {
		enc.encodeType(T.Embedded(i))
	}

	n = T.NumExplicitMethods()
	enc.int(int64(n))
	for i := 0; i < n; i++ {
		enc.encodeObj(T.ExplicitMethod(i))
	}
}

func (enc *TypesEncoder) encodeNamed(T *types.Named) {
	enc.tag(namedTag)
	enc.newTypeID(T)
	enc.encodeObj(T.Obj())
	enc.encodeType(T.Underlying())
	n := T.NumMethods()
	enc.int(int64(n))
	for i := 0; i < n; i++ {
		enc.encodeObj(T.Method(i))
	}
}

func (enc *TypesEncoder) encodeStruct(T *types.Struct) {
	enc.tag(structTag)
	enc.newTypeID(T)
	enc.int(int64(T.NumFields()))
	for i := 0; i < T.NumFields(); i++ {
		f := T.Field(i)
		enc.encodeObj(f)
		enc.string(T.Tag(i))
	}
}

func (enc *TypesEncoder) encodeSlice(T *types.Slice) {
	enc.tag(sliceTag)
	enc.newTypeID(T)
	enc.encodeType(T.Elem())
}

func (enc *TypesEncoder) encodePtr(T *types.Pointer) {
	enc.tag(ptrTag)
	enc.newTypeID(T)
	enc.encodeType(T.Elem())
}

func (enc *TypesEncoder) encodeSignature(T *types.Signature) {
	enc.tag(signatureTag)
	enc.newTypeID(T)
	enc.encodeType(T.Params())
	enc.encodeType(T.Results())
	enc.bool(T.Variadic())
	recv := T.Recv()
	if recv == nil || types.IsInterface(recv.Type()) {
		enc.encodeObj(nil)
	} else {
		enc.encodeObj(T.Recv())
	}
}

func (enc *TypesEncoder) encodeTuple(T *types.Tuple) {
	enc.tag(tupleTag)
	enc.newTypeID(T)
	n := T.Len()
	enc.int(int64(n))
	for i := 0; i < n; i++ {
		enc.encodeObj(T.At(i))
	}
}

func (enc *TypesEncoder) encodeMap(T *types.Map) {
	enc.tag(mapTag)
	enc.newTypeID(T)
	enc.encodeType(T.Key())
	enc.encodeType(T.Elem())
}

func (enc *TypesEncoder) encodeChan(T *types.Chan) {
	enc.tag(chanTag)
	enc.newTypeID(T)
	// OPT(dh): don't use 64 bits for storing the direction
	enc.int(int64(T.Dir()))
	enc.encodeType(T.Elem())
}

func (enc *TypesEncoder) FinishTypes() {
	enc.tag(endTypesTag)
}

const (
	// objects
	varTag = -(iota + 1)
	typeNameTag
	builtinTag
	funcTag
	constTag
	nilTag
	labelTag
	pkgNameTag
	pkgTag

	scopeTag

	// types
	tupleTag
	mapTag
	chanTag
	arrayTag
	namedTag
	structTag
	sliceTag
	ptrTag
	signatureTag
	interfaceTag

	endTypesTag
)

var tagNames = map[int64]string{
	varTag:       "var",
	typeNameTag:  "type name",
	builtinTag:   "builtin",
	funcTag:      "func",
	constTag:     "const",
	nilTag:       "nil",
	labelTag:     "label",
	pkgNameTag:   "pkg name",
	pkgTag:       "pkg",
	scopeTag:     "scope",
	tupleTag:     "tuple",
	mapTag:       "map",
	chanTag:      "chan",
	arrayTag:     "array",
	namedTag:     "named",
	structTag:    "struct",
	sliceTag:     "slice",
	ptrTag:       "pointer",
	signatureTag: "signature",
	interfaceTag: "interface",
}
