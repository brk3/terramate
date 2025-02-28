// Copyright 2022 Mineiros GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stack

import (
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/hcl/eval"
	"github.com/mineiros-io/terramate/stdlib"
)

// EvalCtx represents the evaluation context of a stack.
type EvalCtx struct {
	*eval.Context

	root *config.Root
}

// NewEvalCtx creates a new stack evaluation context.
func NewEvalCtx(root *config.Root, stack *config.Stack, globals *eval.Object) *EvalCtx {
	evalctx := eval.NewContext(stdlib.Functions(stack.HostDir(root)))
	evalwrapper := &EvalCtx{
		Context: evalctx,
		root:    root,
	}
	evalwrapper.SetMetadata(stack)
	evalwrapper.SetGlobals(globals)
	return evalwrapper
}

// SetGlobals sets the given globals on the stack evaluation context.
func (e *EvalCtx) SetGlobals(g *eval.Object) {
	e.SetNamespace("global", g.AsValueMap())
}

// SetMetadata sets the given metadata on the stack evaluation context.
func (e *EvalCtx) SetMetadata(st *config.Stack) {
	runtime := e.root.Runtime()
	runtime.Merge(st.RuntimeValues(e.root))
	e.SetNamespace("terramate", runtime)
}
