package obj

import (
	"bytes"
	"encoding/binary"
	"go/constant"
	"io"
	"math/big"
	"reflect"
	"unsafe"
)

const (
	constUnknownVal = iota + 1
	constBoolVal
	constStringVal
	constInt64Val
	constIntVal
	constRatVal
	constFloatVal
	constComplexVal
)

func encodeConstant(rv reflect.Value) (kind byte, data []byte) {
	T := rv.Type()
	switch T.Kind() {
	case reflect.Struct:
		// unknownVal, intVal, ratVal, floatVal, complexVal
		switch T.NumField() {
		case 0:
			// unknownVal
			kind = constUnknownVal
		case 1:
			// intVal, ratVal, floatVal, complexVal
			switch T.Field(0).Type {
			case reflect.TypeOf(&big.Int{}):
				kind = constIntVal
				data, _ = (*big.Int)(unsafe.Pointer(rv.Field(0).Pointer())).GobEncode()
			case reflect.TypeOf(&big.Rat{}):
				kind = constRatVal
				data, _ = (*big.Rat)(unsafe.Pointer(rv.Field(0).Pointer())).GobEncode()
			case reflect.TypeOf(&big.Float{}):
				kind = constFloatVal
				data, _ = (*big.Float)(unsafe.Pointer(rv.Field(0).Pointer())).GobEncode()
			default:
				panic("internal error")
			}
		case 2:
			// complexVal
			kind = constComplexVal
			re := rv.Field(0).Elem()
			im := rv.Field(1).Elem()
			kindRe, dataRe := encodeConstant(re)
			kindIm, dataIm := encodeConstant(im)
			data = make([]byte, 2+2*8+len(dataRe)+len(dataIm))
			data[0] = kindRe
			binary.LittleEndian.PutUint64(data[1:], uint64(len(dataRe)))
			copy(data[1+8:], dataRe)
			data[1+8+len(dataRe)] = kindIm
			binary.LittleEndian.PutUint64(data[1+8+len(dataRe)+1:], uint64(len(dataIm)))
			copy(data[1+8+len(dataRe)+1+8:], dataIm)
		}
	case reflect.Bool:
		// boolVal
		kind = constBoolVal
		data = make([]byte, 1)
		if rv.Bool() {
			data[0] = 1
		}
	case reflect.String:
		// string
		kind = constStringVal
		s := rv.String()
		data = make([]byte, 8+len(s))
		binary.LittleEndian.PutUint64(data, uint64(len(s)))
	case reflect.Int64:
		// int64Val
		kind = constInt64Val
		data = make([]byte, 8)
		binary.LittleEndian.PutUint64(data, uint64(rv.Int()))
	default:
		panic("internal error")
	}
	return kind, data
}

func decodeConstant(r io.Reader) constant.Value {
	b := make([]byte, 1)
	io.ReadFull(r, b)
	kind := b[0]
	var n uint64
	binary.Read(r, binary.LittleEndian, &n)
	if n > 0 {
		b = make([]byte, n)
		io.ReadFull(r, b)
	}
	return decodeConstant2(kind, b)
}

func decodeConstant2(kind byte, b []byte) constant.Value {
	// XXX rename this function
	if kind == 0 {
		return nil
	}
	switch kind {
	case constUnknownVal:
		return constant.MakeUnknown()
	case constBoolVal:
		return constant.MakeBool(b[0] != 0)
	case constStringVal:
		return constant.MakeString(string(b))
	case constInt64Val:
		return constant.MakeInt64(int64(binary.LittleEndian.Uint64(b)))
	case constIntVal:
		v := &big.Int{}
		v.GobDecode(b)
		// Force creation of an intVal
		c := constant.MakeUint64(1 << 63)
		*(*big.Int)(unsafe.Pointer(reflect.ValueOf(c).Field(0).Pointer())) = *v
		return c
	case constRatVal:
		v := &big.Rat{}
		v.GobDecode(b)
		// Force creation of a ratVal
		c := constant.MakeFloat64(1)
		*(*big.Rat)(unsafe.Pointer(reflect.ValueOf(c).Field(0).Pointer())) = *v
		return c
	case constFloatVal:
		v := &big.Float{}
		v.GobDecode(b)
		// Force creation of a floatVal
		c := constant.ToFloat(constant.MakeUint64(0))
		*(*big.Float)(unsafe.Pointer(reflect.ValueOf(c).Field(0).Pointer())) = *v
		return c
	case constComplexVal:
		br := bytes.NewReader(b)
		re := decodeConstant(br)
		im := decodeConstant(br)
		// Force creation of a complexVal
		c := constant.ToComplex(constant.MakeUint64(0))
		rv := reflect.New(reflect.ValueOf(c).Type())
		*(*constant.Value)(unsafe.Pointer(rv.Elem().Field(0).UnsafeAddr())) = re
		*(*constant.Value)(unsafe.Pointer(rv.Elem().Field(1).UnsafeAddr())) = im
		return rv.Elem().Interface().(constant.Value)
	default:
		panic("internal error")
	}
}
