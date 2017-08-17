// Copyright 2016 Dominik Honnef. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

func NewJump(parent *BasicBlock) *Jump {
	return &Jump{anInstruction{parent}}
}
