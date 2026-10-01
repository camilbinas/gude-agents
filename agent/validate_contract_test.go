package agent

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

// schemaKeywordProbes pairs common JSON Schema keywords with a schema and a
// payload that violates only that keyword. The probe decides whether
// ValidateToolInput actually enforces the keyword.
var schemaKeywordProbes = []struct {
	keyword string
	schema  string
	payload string
}{
	{"type", `{"type":"string"}`, `1`},
	{"enum", `{"enum":["a"]}`, `"b"`},
	{"required", `{"type":"object","required":["a"]}`, `{}`},
	{"properties", `{"type":"object","properties":{"a":{"type":"string"}}}`, `{"a":1}`},
	{"items", `{"type":"array","items":{"type":"string"}}`, `[1]`},
	{"minimum", `{"type":"number","minimum":5}`, `1`},
	{"maximum", `{"type":"number","maximum":5}`, `10`},
	{"exclusiveMinimum", `{"type":"number","exclusiveMinimum":5}`, `5`},
	{"multipleOf", `{"type":"number","multipleOf":2}`, `3`},
	{"minLength", `{"type":"string","minLength":3}`, `"a"`},
	{"maxLength", `{"type":"string","maxLength":1}`, `"abc"`},
	{"pattern", `{"type":"string","pattern":"^a$"}`, `"b"`},
	{"additionalProperties", `{"type":"object","properties":{"a":{}},"additionalProperties":false}`, `{"a":1,"b":2}`},
	{"minItems", `{"type":"array","minItems":2}`, `[1]`},
	{"maxItems", `{"type":"array","maxItems":1}`, `[1,2]`},
	{"uniqueItems", `{"type":"array","uniqueItems":true}`, `[1,1]`},
	{"const", `{"const":"x"}`, `"y"`},
	{"oneOf", `{"oneOf":[{"type":"string"}]}`, `1`},
	{"anyOf", `{"anyOf":[{"type":"string"}]}`, `1`},
	{"allOf", `{"allOf":[{"type":"string"}]}`, `1`},
	{"not", `{"not":{"type":"string"}}`, `"a"`},
}

// TestSchemaContract_DocumentedKeywordsMatchEnforcement keeps the advertised
// structured-output / tool-input contract honest in both directions: every
// keyword listed in supportedSchemaKeywords must be enforced, and any keyword
// that is enforced must be listed (and therefore documented). Adding support
// for a new keyword is expected to update the list and the docs; this test
// does not assert that unsupported keywords should stay unsupported.
func TestSchemaContract_DocumentedKeywordsMatchEnforcement(t *testing.T) {
	for _, probe := range schemaKeywordProbes {
		t.Run(probe.keyword, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal([]byte(probe.schema), &schema); err != nil {
				t.Fatal(err)
			}
			enforced := ValidateToolInput(schema, json.RawMessage(probe.payload)) != nil
			documented := slices.Contains(supportedSchemaKeywords, probe.keyword)
			switch {
			case documented && !enforced:
				t.Fatalf("%q is advertised as enforced but a violating payload passed", probe.keyword)
			case enforced && !documented:
				t.Fatalf("%q is enforced but missing from supportedSchemaKeywords and the docs", probe.keyword)
			}
		})
	}
	for _, kw := range supportedSchemaKeywords {
		found := false
		for _, probe := range schemaKeywordProbes {
			found = found || probe.keyword == kw
		}
		if !found {
			t.Errorf("supported keyword %q has no enforcement probe", kw)
		}
	}
}

// TestSchemaContract_DocsDescribeSupportedSubset checks that the public docs
// state the subset explicitly instead of implying full JSON Schema support.
func TestSchemaContract_DocsDescribeSupportedSubset(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "validate.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var godoc string
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "ValidateToolInput" {
			godoc = fn.Doc.Text()
		}
	}
	markdown, err := os.ReadFile("../docs/structured-output.md")
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]string{
		"ValidateToolInput godoc":   godoc,
		"docs/structured-output.md": string(markdown),
	}
	for name, text := range docs {
		if !strings.Contains(text, "not enforced") {
			t.Errorf("%s must state that keywords outside the subset are not enforced", name)
		}
		for _, kw := range supportedSchemaKeywords {
			if !strings.Contains(text, `"`+kw+`"`) && !strings.Contains(text, "`"+kw+"`") {
				t.Errorf("%s does not list supported keyword %q", name, kw)
			}
		}
	}
}
