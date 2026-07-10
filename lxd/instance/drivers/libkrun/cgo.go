package libkrun

/*
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"
)

func cStr(s string) *C.char {
	return C.CString(s)
}

func optCStr(s string) *C.char {
	if s == "" {
		return nil
	}

	return C.CString(s)
}

func freeCStr(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}
