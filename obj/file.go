package obj

import (
	"encoding/binary"
	"fmt"
	"go/ast"
	"reflect"
	"unsafe"
)

// OPT(dh): optimize for size. for example, use varints.

var (
	tASTFile   = reflect.TypeOf(&ast.File{})
	tASTObject = reflect.TypeOf(&ast.Object{})
	tASTScope  = reflect.TypeOf(&ast.Scope{})
)

type FileDecoder struct {
	ptrs []reflect.Value
}

func NewFileDecoder() *FileDecoder {
	return &FileDecoder{}
}

func (dec *FileDecoder) Object(id uint64) interface{} {
	return dec.ptrs[id].Interface()
}

func (dec *FileDecoder) Decode(b []byte) []*ast.File {
	n := binary.LittleEndian.Uint64(b)
	b = b[8:]
	dec.ptrs = make([]reflect.Value, n)
	var f *ast.File
	var fs []*ast.File
	for len(b) > 0 {
		ptr := binary.LittleEndian.Uint64(b[0:8])
		tag := b[8]
		T := Tags[tag]
		b = b[9:]

		v := dec.ptrs[ptr]
		if !v.IsValid() {
			v = reflect.New(T.Elem())
			dec.ptrs[ptr] = v
		}

		if T == tASTFile {
			f = v.Interface().(*ast.File)
			fs = append(fs, f)
		}
		vElem := v.Elem()
		n := vElem.NumField()
		fieldTypes := FieldTypes[tag]
		for i := 0; i < n; i++ {
			field := vElem.Field(i)
			fieldType := fieldTypes[i]
			switch fieldType {
			case tASTObject, tASTScope:
				continue
			}
			switch field.Kind() {
			case reflect.String:
				l := binary.LittleEndian.Uint64(b[0:8])
				b = b[8:]
				s := string(b[:l])
				field.SetString(s)
				b = b[l:]
			case reflect.Slice:
				l := binary.LittleEndian.Uint64(b[0:8])
				b = b[8:]
				if int64(l) >= 0 {
					field.Set(reflect.MakeSlice(fieldType, int(l), int(l)))
					isInterface := fieldType.Elem().Kind() == reflect.Interface

					type iface struct {
						a uintptr
						b uintptr
					}

					var ptr unsafe.Pointer
					switch reflect.PtrTo(fieldType.Elem()) {
					case reflect.TypeOf((*ast.Expr)(nil)):
						ptr = unsafe.Pointer(&exprMap)
					case reflect.TypeOf((*ast.Decl)(nil)):
						ptr = unsafe.Pointer(&declMap)
					case reflect.TypeOf((*ast.Node)(nil)):
						ptr = unsafe.Pointer(&nodeMap)
					case reflect.TypeOf((*ast.Stmt)(nil)):
						ptr = unsafe.Pointer(&stmtMap)
					case reflect.TypeOf((*ast.Spec)(nil)):
						ptr = unsafe.Pointer(&specMap)
					}
					table := (*[57]func() iface)(ptr)

					for j := 0; j < int(l); j++ {
						e := field.Index(j)
						ptr := binary.LittleEndian.Uint64(b[0:8])
						var v reflect.Value
						if isInterface {
							tag := b[8]
							b = b[9:]
							if ptr != 0 {
								v = dec.ptrs[ptr]
								if !v.IsValid() {
									*(*iface)(unsafe.Pointer(e.UnsafeAddr())) = table[tag]()
									dec.ptrs[ptr] = e.Elem()
								}
							}
						} else {
							b = b[8:]
							if ptr != 0 {
								v = dec.ptrs[ptr]
								if !v.IsValid() {
									v := reflect.New(e.Type().Elem())
									e.Set(v)
									dec.ptrs[ptr] = v
								}
							}
						}
						if ptr != 0 && v != (reflect.Value{}) {
							e.Set(v)
						}
					}
				}
			case reflect.Int:
				l := binary.LittleEndian.Uint64(b[0:8])
				b = b[8:]
				field.SetInt(int64(l))
			case reflect.Bool:
				switch b[0] {
				case 0:
					field.SetBool(false)
				case 1:
					field.SetBool(true)
				default:
					panic("invalid bool")
				}
				b = b[1:]
			case reflect.Interface:
				ptr := binary.LittleEndian.Uint64(b[0:8])
				tag := b[8]
				if ptr != 0 {
					v := dec.ptrs[ptr]
					if !v.IsValid() {
						switch fff := field.Addr().Interface().(type) {
						case *ast.Expr:
							*fff = exprMap[tag]()
						case *ast.Decl:
							*fff = declMap[tag]()
						case *ast.Node:
							*fff = nodeMap[tag]()
						case *ast.Stmt:
							*fff = stmtMap[tag]()
						default:
							panic(fmt.Sprintf("unexpected type %T", field.Addr().Interface()))
						}
						dec.ptrs[ptr] = field.Elem()

					} else {
						field.Set(v)
					}
				}
				b = b[8:]
				b = b[1:]
			case reflect.Ptr:
				ptr := binary.LittleEndian.Uint64(b[0:8])
				if ptr != 0 {
					v := dec.ptrs[ptr]
					if !v.IsValid() {
						v = reflect.New(field.Type().Elem())
						dec.ptrs[ptr] = v
					}
					field.Set(v)
				}
				b = b[8:]
			default:
				panic(fmt.Sprintf("unexpected kind: %s", field.Kind()))
			}
		}
	}

	return fs
}

type FileEncoder struct {
	ptrs      map[interface{}]uint64
	allocated map[uint64]interface{}
	maxPtr    uint64
}

func NewFileEncoder() *FileEncoder {
	return &FileEncoder{
		ptrs:      map[interface{}]uint64{},
		allocated: map[uint64]interface{}{},
	}
}

func (enc *FileEncoder) ID(v interface{}) (uint64, bool) {
	id, ok := enc.ptrs[v]
	return id, ok
}

func (enc *FileEncoder) ptr(v interface{}) uint64 {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.IsNil() {
		return 0
	}
	ptr, ok := enc.ptrs[v]
	if ok {
		return ptr
	}
	enc.maxPtr++
	enc.ptrs[v] = enc.maxPtr
	enc.allocated[enc.maxPtr] = v
	return enc.maxPtr
}

func (enc *FileEncoder) Encode(fs []*ast.File) []byte {
	b := make([]byte, 8)

	// TODO(dh): pull marshalStruct out into a method
	marshalStruct := func(node interface{}) {
		if node == nil {
			return
		}

		ptr := enc.ptr(node)
		tag := Types[reflect.TypeOf(node)]
		b = appendInt(b, ptr)
		b = append(b, tag)

		v := reflect.ValueOf(node)
		n := v.Elem().NumField()
		for i := 0; i < n; i++ {
			field := v.Elem().Field(i)

			switch field.Type() {
			case tASTObject:
				continue
			case tASTScope:
				continue
			}
			switch field.Kind() {
			case reflect.String:
				b = appendInt(b, uint64(field.Len()))
				b = append(b, field.String()...)
			case reflect.Slice:
				if field.IsNil() {
					b = appendInt(b, (1<<64)-1)
				} else {
					n := field.Len()
					b = appendInt(b, uint64(n))
					isInterface := field.Type().Elem().Kind() == reflect.Interface
					for j := 0; j < n; j++ {
						e := field.Index(j)
						ptr := enc.ptr(e.Interface())
						b = appendInt(b, ptr)
						if isInterface {
							tag := Types[e.Elem().Type()]
							b = append(b, tag)
						}

					}
				}
			case reflect.Int:
				n := uint64(field.Int())
				b = appendInt(b, n)
			case reflect.Bool:
				if field.Bool() {
					b = append(b, 1)
				} else {
					b = append(b, 0)
				}
			case reflect.Interface:
				if field.IsNil() {
					b = appendInt(b, 0)
					b = append(b, 0)
				} else {
					ptr := enc.ptr(field.Interface())
					b = appendInt(b, ptr)
					tag := Types[field.Elem().Type()]
					b = append(b, tag)
				}
			case reflect.Ptr:
				ptr := enc.ptr(field.Interface())
				b = appendInt(b, ptr)
			default:
				panic(fmt.Sprintf("unexpected kind: %s", field.Kind()))
			}
		}
	}

	for _, f := range fs {
		ast.Inspect(f, func(node ast.Node) bool {
			marshalStruct(node)
			return true
		})

		for _, cg := range f.Comments {
			marshalStruct(cg)
			// even though (*ast.File).Comments is a collection of all
			// comments of all nodes in the file, none of the
			// *ast.CommentGroup or *ast.Comment match existing pointers.
			// Maybe they're the comments that aren't attached to any
			// nodes?
			for _, c := range cg.List {
				marshalStruct(c)
			}
		}
	}

	binary.LittleEndian.PutUint64(b, uint64(len(enc.allocated)+1))
	return b
}

func appendInt(b []byte, x uint64) []byte {
	return append(b,
		byte(x),
		byte(x>>8),
		byte(x>>16),
		byte(x>>24),
		byte(x>>32),
		byte(x>>40),
		byte(x>>48),
		byte(x>>56))
}

var FieldTypes = [255][10]reflect.Type{}

var Types = map[reflect.Type]byte{}
var Tags = [255]reflect.Type{}

func init() {
	for i, v := range []interface{}{
		&ast.ArrayType{},
		&ast.AssignStmt{},
		&ast.BadDecl{},
		&ast.BadExpr{},
		&ast.BadStmt{},
		&ast.BasicLit{},
		&ast.BinaryExpr{},
		&ast.BlockStmt{},
		&ast.BranchStmt{},
		&ast.CallExpr{},
		&ast.CaseClause{},
		&ast.ChanType{},
		&ast.CommClause{},
		&ast.Comment{},
		&ast.CommentGroup{},
		&ast.CompositeLit{},
		&ast.DeclStmt{},
		&ast.DeferStmt{},
		&ast.Ellipsis{},
		&ast.EmptyStmt{},
		&ast.ExprStmt{},
		&ast.Field{},
		&ast.FieldList{},
		&ast.File{},
		&ast.ForStmt{},
		&ast.FuncDecl{},
		&ast.FuncLit{},
		&ast.FuncType{},
		&ast.GenDecl{},
		&ast.GoStmt{},
		&ast.Ident{},
		&ast.IfStmt{},
		&ast.ImportSpec{},
		&ast.IncDecStmt{},
		&ast.IndexExpr{},
		&ast.InterfaceType{},
		&ast.KeyValueExpr{},
		&ast.LabeledStmt{},
		&ast.MapType{},
		&ast.Object{},
		&ast.Package{},
		&ast.ParenExpr{},
		&ast.RangeStmt{},
		&ast.ReturnStmt{},
		&ast.Scope{},
		&ast.SelectStmt{},
		&ast.SelectorExpr{},
		&ast.SendStmt{},
		&ast.SliceExpr{},
		&ast.StarExpr{},
		&ast.StructType{},
		&ast.SwitchStmt{},
		&ast.TypeAssertExpr{},
		&ast.TypeSpec{},
		&ast.TypeSwitchStmt{},
		&ast.UnaryExpr{},
		&ast.ValueSpec{},
	} {
		T := reflect.TypeOf(v)
		Types[T] = byte(i)
		Tags[byte(i)] = T

		n := T.Elem().NumField()
		for j := 0; j < n; j++ {
			FieldTypes[byte(i)][j] = T.Elem().Field(j).Type
		}
	}
}

var (
	exprMap = [57]func() ast.Expr{
		0:  func() ast.Expr { return &ast.ArrayType{} },
		3:  func() ast.Expr { return &ast.BadExpr{} },
		5:  func() ast.Expr { return &ast.BasicLit{} },
		6:  func() ast.Expr { return &ast.BinaryExpr{} },
		9:  func() ast.Expr { return &ast.CallExpr{} },
		11: func() ast.Expr { return &ast.ChanType{} },
		15: func() ast.Expr { return &ast.CompositeLit{} },
		18: func() ast.Expr { return &ast.Ellipsis{} },
		26: func() ast.Expr { return &ast.FuncLit{} },
		27: func() ast.Expr { return &ast.FuncType{} },
		30: func() ast.Expr { return &ast.Ident{} },
		34: func() ast.Expr { return &ast.IndexExpr{} },
		35: func() ast.Expr { return &ast.InterfaceType{} },
		36: func() ast.Expr { return &ast.KeyValueExpr{} },
		38: func() ast.Expr { return &ast.MapType{} },
		41: func() ast.Expr { return &ast.ParenExpr{} },
		46: func() ast.Expr { return &ast.SelectorExpr{} },
		48: func() ast.Expr { return &ast.SliceExpr{} },
		49: func() ast.Expr { return &ast.StarExpr{} },
		50: func() ast.Expr { return &ast.StructType{} },
		52: func() ast.Expr { return &ast.TypeAssertExpr{} },
		55: func() ast.Expr { return &ast.UnaryExpr{} },
	}

	stmtMap = [57]func() ast.Stmt{
		1:  func() ast.Stmt { return &ast.AssignStmt{} },
		4:  func() ast.Stmt { return &ast.BadStmt{} },
		7:  func() ast.Stmt { return &ast.BlockStmt{} },
		8:  func() ast.Stmt { return &ast.BranchStmt{} },
		10: func() ast.Stmt { return &ast.CaseClause{} },
		12: func() ast.Stmt { return &ast.CommClause{} },
		16: func() ast.Stmt { return &ast.DeclStmt{} },
		17: func() ast.Stmt { return &ast.DeferStmt{} },
		19: func() ast.Stmt { return &ast.EmptyStmt{} },
		20: func() ast.Stmt { return &ast.ExprStmt{} },
		24: func() ast.Stmt { return &ast.ForStmt{} },
		29: func() ast.Stmt { return &ast.GoStmt{} },
		31: func() ast.Stmt { return &ast.IfStmt{} },
		33: func() ast.Stmt { return &ast.IncDecStmt{} },
		37: func() ast.Stmt { return &ast.LabeledStmt{} },
		42: func() ast.Stmt { return &ast.RangeStmt{} },
		43: func() ast.Stmt { return &ast.ReturnStmt{} },
		45: func() ast.Stmt { return &ast.SelectStmt{} },
		47: func() ast.Stmt { return &ast.SendStmt{} },
		51: func() ast.Stmt { return &ast.SwitchStmt{} },
		54: func() ast.Stmt { return &ast.TypeSwitchStmt{} },
	}

	declMap = [57]func() ast.Decl{
		2:  func() ast.Decl { return &ast.BadDecl{} },
		25: func() ast.Decl { return &ast.FuncDecl{} },
		28: func() ast.Decl { return &ast.GenDecl{} },
	}

	nodeMap = [57]func() ast.Node{
		0:  func() ast.Node { return &ast.ArrayType{} },
		1:  func() ast.Node { return &ast.AssignStmt{} },
		2:  func() ast.Node { return &ast.BadDecl{} },
		3:  func() ast.Node { return &ast.BadExpr{} },
		4:  func() ast.Node { return &ast.BadStmt{} },
		5:  func() ast.Node { return &ast.BasicLit{} },
		6:  func() ast.Node { return &ast.BinaryExpr{} },
		7:  func() ast.Node { return &ast.BlockStmt{} },
		8:  func() ast.Node { return &ast.BranchStmt{} },
		9:  func() ast.Node { return &ast.CallExpr{} },
		10: func() ast.Node { return &ast.CaseClause{} },
		11: func() ast.Node { return &ast.ChanType{} },
		12: func() ast.Node { return &ast.CommClause{} },
		13: func() ast.Node { return &ast.Comment{} },
		14: func() ast.Node { return &ast.CommentGroup{} },
		15: func() ast.Node { return &ast.CompositeLit{} },
		16: func() ast.Node { return &ast.DeclStmt{} },
		17: func() ast.Node { return &ast.DeferStmt{} },
		18: func() ast.Node { return &ast.Ellipsis{} },
		19: func() ast.Node { return &ast.EmptyStmt{} },
		20: func() ast.Node { return &ast.ExprStmt{} },
		21: func() ast.Node { return &ast.Field{} },
		22: func() ast.Node { return &ast.FieldList{} },
		23: func() ast.Node { return &ast.File{} },
		24: func() ast.Node { return &ast.ForStmt{} },
		25: func() ast.Node { return &ast.FuncDecl{} },
		26: func() ast.Node { return &ast.FuncLit{} },
		27: func() ast.Node { return &ast.FuncType{} },
		28: func() ast.Node { return &ast.GenDecl{} },
		29: func() ast.Node { return &ast.GoStmt{} },
		30: func() ast.Node { return &ast.Ident{} },
		31: func() ast.Node { return &ast.IfStmt{} },
		32: func() ast.Node { return &ast.ImportSpec{} },
		33: func() ast.Node { return &ast.IncDecStmt{} },
		34: func() ast.Node { return &ast.IndexExpr{} },
		35: func() ast.Node { return &ast.InterfaceType{} },
		36: func() ast.Node { return &ast.KeyValueExpr{} },
		37: func() ast.Node { return &ast.LabeledStmt{} },
		38: func() ast.Node { return &ast.MapType{} },
		40: func() ast.Node { return &ast.Package{} },
		41: func() ast.Node { return &ast.ParenExpr{} },
		42: func() ast.Node { return &ast.RangeStmt{} },
		43: func() ast.Node { return &ast.ReturnStmt{} },
		45: func() ast.Node { return &ast.SelectStmt{} },
		46: func() ast.Node { return &ast.SelectorExpr{} },
		47: func() ast.Node { return &ast.SendStmt{} },
		48: func() ast.Node { return &ast.SliceExpr{} },
		49: func() ast.Node { return &ast.StarExpr{} },
		50: func() ast.Node { return &ast.StructType{} },
		51: func() ast.Node { return &ast.SwitchStmt{} },
		52: func() ast.Node { return &ast.TypeAssertExpr{} },
		53: func() ast.Node { return &ast.TypeSpec{} },
		54: func() ast.Node { return &ast.TypeSwitchStmt{} },
		55: func() ast.Node { return &ast.UnaryExpr{} },
		56: func() ast.Node { return &ast.ValueSpec{} },
	}

	specMap = [57]func() ast.Spec{
		32: func() ast.Spec { return &ast.ImportSpec{} },
		56: func() ast.Spec { return &ast.ValueSpec{} },
		53: func() ast.Spec { return &ast.TypeSpec{} },
	}
)
