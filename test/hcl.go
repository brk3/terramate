// Copyright 2021 Mineiros GmbH
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

package test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	hhcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/hcl/ast"
	"github.com/mineiros-io/terramate/hcl/info"
	"github.com/mineiros-io/terramate/project"
)

// ParseTerramateConfig parses the Terramate configuration found
// on the given dir, returning the parsed configuration.
func ParseTerramateConfig(t *testing.T, dir string) hcl.Config {
	t.Helper()

	parser, err := hcl.NewTerramateParser(dir, dir)
	assert.NoError(t, err)

	err = parser.AddDir(dir)
	assert.NoError(t, err)

	cfg, err := parser.ParseConfig()
	assert.NoError(t, err)

	return cfg
}

// AssertGenCodeEquals checks if got gen code equals want. Since got
// is generated by Terramate it will be stripped of its Terramate
// header (if present) before comparing with want.
func AssertGenCodeEquals(t *testing.T, got string, want string) {
	t.Helper()

	const trimmedChars = "\n "

	// Terramate header validation is done separately, here we check only code.
	// So headers are removed.
	got = removeTerramateHCLHeader(got)
	got = strings.Trim(got, trimmedChars)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("generated code doesn't match expectation")
		t.Errorf("want:\n%q", want)
		t.Errorf("got:\n%q", got)
		t.Fatalf("diff:\n%s", diff)
	}
}

// AssertTerramateConfig checks if two given Terramate configs are equal.
func AssertTerramateConfig(t *testing.T, got, want hcl.Config) {
	t.Helper()

	assertTerramateBlock(t, got.Terramate, want.Terramate)
	assertStackBlock(t, got.Stack, want.Stack)
	assertAssertsBlock(t, got.Asserts, want.Asserts, "terramate asserts")
	AssertDiff(t, got.Vendor, want.Vendor, "terramate vendor")
	assertGenHCLBlocks(t, got.Generate.HCLs, want.Generate.HCLs)
	assertGenFileBlocks(t, got.Generate.Files, want.Generate.Files)
}

// AssertDiff will compare the two values and fail if they are not the same
// providing a comprehensive textual diff of the differences between them.
// If provided msg must be a string + any formatting parameters. The msg will be
// added if the assertion fails.
func AssertDiff(t *testing.T, got, want interface{}, msg ...interface{}) {
	t.Helper()

	if diff := cmp.Diff(got, want, cmp.AllowUnexported(project.Path{})); diff != "" {
		errmsg := fmt.Sprintf("-(got) +(want):\n%s", diff)
		if len(msg) > 0 {
			errmsg = fmt.Sprintf(msg[0].(string), msg[1:]...) + ": " + errmsg
		}
		t.Fatalf(errmsg)
	}
}

// NewExpr parses the given string and returns a hcl.Expression.
func NewExpr(t *testing.T, expr string) hhcl.Expression {
	t.Helper()

	res, err := ast.ParseExpression(expr, "test")
	assert.NoError(t, err)
	return res
}

// AssertConfigEquals asserts that two [config.Assert] are equal.
func AssertConfigEquals(t *testing.T, got, want []config.Assert) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d assert blocks, want %d", len(got), len(want))
	}

	for i, g := range got {
		w := want[i]
		if g.Assertion != w.Assertion {
			t.Errorf("got.Assertion[%d]=%t, want=%t", i, g.Assertion, w.Assertion)
		}
		if g.Warning != w.Warning {
			t.Errorf("got.Warning[%d]=%t, want=%t", i, g.Warning, w.Warning)
		}
		AssertDiff(t, g.Range, w.Range, "range mismatch")
		assert.EqualStrings(t, w.Message, g.Message, "message mismatch")
	}
}

// AssertEqualPos checks if two ast.Pos are equal.
func AssertEqualPos(t *testing.T, got, want info.Pos, fmtargs ...any) {
	t.Helper()

	msg := prefixer(fmtargs...)

	assert.EqualInts(t, want.Line(), got.Line(), msg("line mismatch"))
	assert.EqualInts(t, want.Column(), got.Column(), msg("column mismatch"))
	assert.EqualInts(t, want.Byte(), got.Byte(), msg("byte mismatch"))
}

// AssertEqualRanges checks if two ranges are equal.
// If the wanted range is zero value of the type no check will be performed since
// this communicates that the caller is not interested on validating the range.
func AssertEqualRanges(t *testing.T, got, want info.Range, fmtargs ...any) {
	t.Helper()

	if isZeroRange(want) {
		return
	}

	msg := prefixer(fmtargs...)

	assert.EqualStrings(t, want.HostPath(), got.HostPath(), msg("host path mismatch"))
	AssertEqualPaths(t, got.Path(), want.Path(), msg("path mismatch"))
	AssertEqualPos(t, got.Start(), want.Start(), msg("start pos mismatch"))
	AssertEqualPos(t, got.End(), want.End(), msg("end pos mismatch"))
}

// AssertEqualPaths checks if two paths are equal.
func AssertEqualPaths(t *testing.T, got, want project.Path, fmtargs ...any) {
	t.Helper()

	if len(fmtargs) > 0 {
		assert.EqualStrings(t, want.String(), got.String(),
			fmt.Sprintf(fmtargs[0].(string), fmtargs[1:]...))
	} else {
		assert.EqualStrings(t, want.String(), got.String())
	}
}

func assertAssertsBlock(t *testing.T, got, want []hcl.AssertConfig, ctx string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s: got %d assert blocks, want %d", ctx, len(got), len(want))
	}

	for i, g := range got {
		w := want[i]
		newctx := fmt.Sprintf("%s: assert %d", ctx, i)
		AssertEqualRanges(t, g.Range, w.Range, "%s: range mismatch", newctx)
		assert.EqualStrings(t,
			exprAsStr(t, w.Assertion), exprAsStr(t, g.Assertion),
			"%s: assertion expr mismatch", newctx)
		assert.EqualStrings(t,
			exprAsStr(t, w.Message), exprAsStr(t, g.Message),
			"%s: message expr mismatch", newctx)
		assert.EqualStrings(t,
			exprAsStr(t, w.Warning), exprAsStr(t, g.Warning),
			"%s: warning expr mismatch", newctx)
	}
}

func exprAsStr(t *testing.T, expr hhcl.Expression) string {
	t.Helper()

	if expr == nil {
		return ""
	}

	tokens := ast.TokensForExpression(expr)
	return string(tokens.Bytes())
}

func assertTerramateBlock(t *testing.T, got, want *hcl.Terramate) {
	t.Helper()

	if want == got {
		// same pointer, or both nil
		return
	}

	if (want == nil) != (got == nil) {
		t.Fatalf("want[%v] != got[%v]", want, got)
	}

	if want == nil {
		t.Fatalf("want[nil] but got[%+v]", got)
	}

	assert.EqualStrings(t, want.RequiredVersion, got.RequiredVersion,
		"required_version mismatch")

	if (want.Config == nil) != (got.Config == nil) {
		t.Fatalf("want.Config[%+v] != got.Config[%+v]",
			want.Config, got.Config)
	}

	assertTerramateConfigBlock(t, got.Config, want.Config)
}

func assertTerramateConfigBlock(t *testing.T, got, want *hcl.RootConfig) {
	t.Helper()

	if want == nil {
		return
	}

	if (want.Git == nil) != (got.Git == nil) {
		t.Fatalf(
			"want.Git[%+v] != got.Git[%+v]",
			want.Git,
			got.Git,
		)
	}

	if want.Git != nil {
		if *want.Git != *got.Git {
			t.Fatalf("want.Git[%+v] != got.Git[%+v]", want.Git, got.Git)
		}
	}

	assertTerramateRunBlock(t, got.Run, want.Run)
}

func assertGenHCLBlocks(t *testing.T, got, want []hcl.GenHCLBlock) {
	t.Helper()

	// We don't have a good way to compare all contents for now
	assert.EqualInts(t, len(want), len(got), "genhcl blocks differ in len")

	for i, gotBlock := range got {
		wantBlock := want[i]
		AssertEqualRanges(t, gotBlock.Range, wantBlock.Range, "genhcl range differs")
		assert.EqualStrings(t, wantBlock.Label, gotBlock.Label, "genhcl label differs")
		assertAssertsBlock(t, gotBlock.Asserts, wantBlock.Asserts, "genhcl asserts")
	}
}

func assertGenFileBlocks(t *testing.T, got, want []hcl.GenFileBlock) {
	t.Helper()

	// We don't have a good way to compare all contents for now
	assert.EqualInts(t, len(want), len(got), "genfile blocks differ in len")

	for i, gotBlock := range got {
		wantBlock := want[i]
		AssertEqualRanges(t, gotBlock.Range, wantBlock.Range, "genfile range differs")
		assert.EqualStrings(t, wantBlock.Label, gotBlock.Label, "genfile label differs")
		assertAssertsBlock(t, gotBlock.Asserts, wantBlock.Asserts, "genfile asserts")
	}
}

func assertTerramateRunBlock(t *testing.T, got, want *hcl.RunConfig) {
	t.Helper()

	if (want == nil) != (got == nil) {
		t.Fatalf("want.Run[%+v] != got.Run[%+v]", want, got)
	}

	if want == nil {
		return
	}

	assert.IsTrue(t, want.CheckGenCode == got.CheckGenCode,
		"want.Run.CheckGenCode %v != got.Run.CheckGenCode %v",
		want.CheckGenCode, got.CheckGenCode)

	if (want.Env == nil) != (got.Env == nil) {
		t.Fatalf(
			"want.Run.Env[%+v] != got.Run.Env[%+v]",
			want.Env,
			got.Env,
		)
	}

	if want.Env == nil {
		return
	}

	// There is no easy way to compare hclsyntax.Attribute
	// (or hcl.Attribute, or hclsyntax.Expression, etc).
	// So we do this hack in an attempt of comparing the attributes
	// original expressions (no eval involved).

	gotHCL := hclFromAttributes(t, got.Env.Attributes)
	wantHCL := hclFromAttributes(t, want.Env.Attributes)

	AssertDiff(t, gotHCL, wantHCL)
}

// hclFromAttributes ensures that we always build the same HCL document
// given an hcl.Attributes.
func hclFromAttributes(t *testing.T, attrs ast.Attributes) string {
	t.Helper()

	file := hclwrite.NewEmptyFile()
	body := file.Body()
	for _, attr := range attrs.SortedList() {
		body.SetAttributeRaw(attr.Name, ast.TokensForExpression(attr.Expr))
	}

	return string(file.Bytes())
}

func assertStackBlock(t *testing.T, got, want *hcl.Stack) {
	if (got == nil) != (want == nil) {
		t.Fatalf("want[%+v] != got[%+v]", want, got)
	}

	if want == nil {
		return
	}

	assert.EqualInts(t, len(got.After), len(want.After), "After length mismatch")

	for i, w := range want.After {
		assert.EqualStrings(t, w, got.After[i], "stack after mismatch")
	}
}

// WriteRootConfig writes a basic terramate root config.
func WriteRootConfig(t *testing.T, rootdir string) {
	WriteFile(t, rootdir, "root.config.tm", `
terramate {
	config {

	}
}
			`)
}

func removeTerramateHCLHeader(code string) string {
	lines := []string{}

	for _, line := range strings.Split(code, "\n") {
		if strings.HasPrefix(line, "// TERRAMATE") {
			continue
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// prefixer ass the given fmtargs as a prefix of any string passed
// to the returned function, if any. If fmtargs is empty then no prefix is added.
func prefixer(fmtargs ...any) func(string) string {
	prefix := ""

	if len(fmtargs) > 0 {
		prefix = fmt.Sprintf(fmtargs[0].(string), fmtargs[1:]...)
	}

	return func(s string) string {
		if prefix != "" {
			return fmt.Sprintf("%s: %s", prefix, s)
		}
		return s
	}
}

func isZeroRange(r info.Range) bool {
	var zero info.Range
	return zero == r
}
