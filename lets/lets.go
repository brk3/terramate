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

// Package lets provides parsing and evaluation of lets blocks.
package lets

import (
	hhcl "github.com/hashicorp/hcl/v2"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/hcl/ast"
	"github.com/mineiros-io/terramate/hcl/eval"
	"github.com/mineiros-io/terramate/hcl/fmt"
	"github.com/mineiros-io/terramate/hcl/info"
	"github.com/mineiros-io/terramate/mapexpr"
	"github.com/rs/zerolog/log"
	"github.com/zclconf/go-cty/cty"
)

// Errors returned when parsing and evaluating lets.
const (
	ErrEval      errors.Kind = "lets eval"
	ErrRedefined errors.Kind = "lets redefined"
)

type (
	// Expr is an unevaluated let expression.
	Expr struct {
		// Origin contains the information where the expr is defined.
		Origin info.Range

		hhcl.Expression
	}

	// Exprs is the map of unevaluated let expressions visible in a
	// directory.
	Exprs map[string]Expr

	// Value is an evaluated let.
	Value struct {
		Origin info.Range

		cty.Value
	}

	// Map is an evaluated lets map.
	Map map[string]Value
)

// Load loads all the lets from the hcl blocks.
func Load(letblock *ast.MergedBlock, ctx *eval.Context) error {
	exprs, err := loadExprs(letblock)
	if err != nil {
		return err
	}

	return exprs.Eval(ctx)
}

// Eval evaluates all lets expressions and returns an EvalReport..
func (letExprs Exprs) Eval(ctx *eval.Context) error {
	logger := log.With().
		Str("action", "Exprs.Eval()").
		Logger()

	lets := Map{}
	pendingExprsErrs := map[string]*errors.List{}
	pendingExprs := make(Exprs)

	copyexprs(pendingExprs, letExprs)
	removeUnset(pendingExprs)

	if !ctx.HasNamespace("let") {
		ctx.SetNamespace("let", map[string]cty.Value{})
	}

	for len(pendingExprs) > 0 {
		amountEvaluated := 0

		logger.Trace().Msg("evaluating pending expressions")

	pendingExpression:
		for name, expr := range pendingExprs {
			logger := logger.With().
				Stringer("origin", expr.Origin.Path()).
				Str("let", name).
				Logger()

			vars := expr.Variables()
			pendingExprsErrs[name] = errors.L()

			logger.Trace().Msg("checking var access inside expression")

			for _, namespace := range vars {
				if !ctx.HasNamespace(namespace.RootName()) {
					pendingExprsErrs[name].Append(errors.E(
						ErrEval,
						namespace.SourceRange(),
						"unknown variable namespace: %s", namespace.RootName(),
					))

					continue
				}

				if namespace.RootName() != "let" {
					continue
				}

				switch attr := namespace[1].(type) {
				case hhcl.TraverseAttr:
					if _, isPending := pendingExprs[attr.Name]; isPending {
						continue pendingExpression
					}
				default:
					panic("unexpected type of traversal - this is a BUG")
				}
			}

			if err := pendingExprsErrs[name].AsError(); err != nil {
				continue
			}

			logger.Trace().Msg("evaluating expression")

			val, err := ctx.Eval(expr)
			if err != nil {
				pendingExprsErrs[name].Append(errors.E(ErrEval, err, "let.%s", name))
				continue
			}

			lets[name] = Value{
				Origin: expr.Origin,
				Value:  val,
			}

			amountEvaluated++

			delete(pendingExprs, name)
			delete(pendingExprsErrs, name)

			logger.Trace().Msg("updating lets eval context with evaluated attribute")

			ctx.SetNamespace("let", lets.Attributes())
		}

		if amountEvaluated == 0 {
			break
		}
	}

	errs := errors.L()
	for name, expr := range pendingExprs {
		err := pendingExprsErrs[name].AsError()
		if err == nil {
			err = errors.E(expr.Range(), "undefined let %s", name)
		}
		errs.AppendWrap(ErrEval, err)
	}

	return errs.AsError()
}

// String provides a string representation of the evaluated lets.
func (lets Map) String() string {
	return fmt.FormatAttributes(lets.Attributes())
}

// Attributes returns all the lets attributes, the key in the map
// is the attribute name with its corresponding value mapped
func (lets Map) Attributes() map[string]cty.Value {
	attrcopy := map[string]cty.Value{}
	for k, v := range lets {
		attrcopy[k] = v.Value
	}
	return attrcopy
}

func removeUnset(exprs Exprs) {
	for name, expr := range exprs {
		traversal, diags := hhcl.AbsTraversalForExpr(expr.Expression)
		if diags.HasErrors() {
			continue
		}
		if len(traversal) != 1 {
			continue
		}
		if traversal.RootName() == "unset" {
			delete(exprs, name)
		}
	}
}

func copyexprs(dst, src Exprs) {
	for k, v := range src {
		dst[k] = v
	}
}

func loadExprs(letblock *ast.MergedBlock) (Exprs, error) {
	letExprs := Exprs{}

	for _, attr := range letblock.Attributes.SortedList() {
		letExprs[attr.Name] = Expr{
			Origin:     attr.Range,
			Expression: attr.Expr,
		}
	}

	for _, mapBlock := range letblock.Blocks {
		varName := mapBlock.Labels[0]
		if _, ok := letblock.Attributes[varName]; ok {
			return nil, errors.E(
				ErrRedefined,
				"map label %s conflicts with let.%s attribute", varName, varName)
		}
		mapExpr, err := mapexpr.NewMapExpr(mapBlock)
		if err != nil {
			return nil, errors.E(ErrEval, err)
		}
		letExprs[mapBlock.Labels[0]] = Expr{
			Origin:     mapBlock.RawOrigins[0].Range,
			Expression: mapExpr,
		}
	}

	return letExprs, nil
}
