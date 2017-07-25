// +build ignore

package obj

import (
	"encoding/binary"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"reflect"
)

type MappingEncoder struct {
	w    io.Writer
	fenc *FileEncoder
	tenc *TypesEncoder
}

func NewMappingEncoder(w io.Writer, fenc *FileEncoder, tenc *TypesEncoder) *MappingEncoder {
	return &MappingEncoder{
		w:    w,
		fenc: fenc,
		tenc: tenc,
	}
}

func (enc *MappingEncoder) object(node ast.Node, obj interface{}) {
	fid, ok := enc.fenc.ID(node)
	if !ok {
		panic("couldn't find ID for AST node")
	}
	tid, ok := enc.tenc.ID(obj)
	if !ok {
		if _, ok := obj.(*types.Scope); ok {
			panic("internal inconsistency")
		}
		enc.tenc.EncodeObject(obj.(types.Object))
		tid, ok = enc.tenc.ID(obj)
		if !ok {
			panic("internal inconsistency")
		}
	}

	b := [2]uint64{fid, uint64(tid)}
	binary.Write(enc.w, binary.LittleEndian, b)
}

func (enc *MappingEncoder) int(v int) {
	binary.Write(enc.w, binary.LittleEndian, int64(v))
}

func (enc *MappingEncoder) Encode(fset *token.FileSet, info *types.Info) {
	// XXX store InitOrder
	total := len(info.Types)
	for expr := range info.Types {
		_, ok := enc.fenc.ID(expr)
		if !ok {
			total--
		}
	}
	enc.int(total)
	for expr, tv := range info.Types {
		fid, ok := enc.fenc.ID(expr)
		if !ok {
			// XXX go/types creates constants for BasicLits that don't
			// actually exist in the AST. This seems to be related to
			// postfix increment/decrement. For example, `x++` creates
			// a basic literal of type int with value 1.
			continue
		}
		tid, ok := enc.tenc.ID(tv.Type)
		if !ok {
			enc.tenc.EncodeType(tv.Type)
			tid, ok = enc.tenc.ID(tv.Type)
			if !ok {
				panic("internal inconsistency")
			}
		}
		var kind byte
		var data []byte
		if tv.Value != nil {
			kind, data = encodeConstant(reflect.ValueOf(tv.Value))
		}
		binary.Write(enc.w, binary.LittleEndian, fid)
		binary.Write(enc.w, binary.LittleEndian, tid)
		enc.w.Write([]byte{kind})
		binary.Write(enc.w, binary.LittleEndian, uint64(len(data)))
		enc.w.Write(data)
	}
	enc.tenc.FinishTypes()

	enc.int(len(info.Defs))
	for ident, obj := range info.Defs {
		enc.object(ident, obj)
	}

	enc.int(len(info.Uses))
	for ident, obj := range info.Uses {
		enc.object(ident, obj)
	}

	enc.int(len(info.Implicits))
	for node, obj := range info.Implicits {
		enc.object(node, obj)
	}

	enc.int(len(info.Scopes))
	for node, scope := range info.Scopes {
		enc.object(node, scope)
	}
}

type MappingDecoder struct {
	r    io.Reader
	fdec *FileDecoder
	tdec *TypesDecoder
}

func NewMappingDecoder(fdec *FileDecoder, tdec *TypesDecoder) *MappingDecoder {
	return &MappingDecoder{
		fdec: fdec,
		tdec: tdec,
	}
}

func (dec *MappingDecoder) int() int64 {
	var id int64
	binary.Read(dec.r, binary.LittleEndian, &id)
	return id
}

func (dec *MappingDecoder) Decode(r io.Reader) *types.Info {
	dec.r = r
	var ids [2]uint64
	info := &types.Info{
		Types:     map[ast.Expr]types.TypeAndValue{},
		Defs:      map[*ast.Ident]types.Object{},
		Implicits: map[ast.Node]types.Object{},
		Uses:      map[*ast.Ident]types.Object{},
		Scopes:    map[ast.Node]*types.Scope{},
	}

	n := dec.int()
	for i := int64(0); i < n; i++ {
		binary.Read(dec.r, binary.LittleEndian, ids[:])
		c := decodeConstant(dec.r)
		if c != nil {
			_ = c
		}
	}
	n = dec.int()
	for i := int64(0); i < n; i++ {
		binary.Read(dec.r, binary.LittleEndian, ids[:])
		ident := dec.fdec.Object(ids[0]).(*ast.Ident)
		obj := dec.tdec.Object(ids[1])
		if obj == nil {
			info.Defs[ident] = nil
			continue
		}
		info.Defs[ident] = obj.(types.Object)
	}

	n = dec.int()
	for i := int64(0); i < n; i++ {
		binary.Read(dec.r, binary.LittleEndian, ids[:])
		ident := dec.fdec.Object(ids[0]).(*ast.Ident)
		obj := dec.tdec.Object(ids[1])
		if obj == nil {
			info.Uses[ident] = nil
			continue
		}
		info.Uses[ident] = obj.(types.Object)
	}
	n = dec.int()
	for i := int64(0); i < n; i++ {
		binary.Read(dec.r, binary.LittleEndian, ids[:])
		node := dec.fdec.Object(ids[0]).(ast.Node)
		obj := dec.tdec.Object(ids[1])
		if obj == nil {
			info.Implicits[node] = nil
			continue
		}
		info.Implicits[node] = obj.(types.Object)
	}

	n = dec.int()
	for i := int64(0); i < n; i++ {
		binary.Read(dec.r, binary.LittleEndian, ids[:])
		node := dec.fdec.Object(ids[0]).(ast.Node)
		obj := dec.tdec.Object(ids[1])
		if obj == nil {
			info.Scopes[node] = nil
			continue
		}
		info.Scopes[node] = obj.(*types.Scope)
	}

	return info
}
