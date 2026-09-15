package css

import (
	"reflect"
	"testing"
)

func TestParseRulesDeclarationsAndPositions(t *testing.T) {
	src := "/* a { color: red } */\n" +
		".a, .b:where(:hover) {\n" +
		"  color: #fff;\n" +
		"  background: url(\"x;y{z}.png\") no-repeat;\n" +
		"  margin: 0 !important\n" +
		"}\n" +
		"@media (max-width: 900px) {\n" +
		"  .c { --helm-x: rgb(0 0 0 / .5); }\n" +
		"}\n" +
		"@import url(\"other.css\");\n"
	rules := Parse(src)
	if len(rules) != 3 {
		t.Fatalf("rules = %d, want 3: %+v", len(rules), rules)
	}
	a := rules[0]
	if a.Prelude != ".a, .b:where(:hover)" || a.Line != 2 {
		t.Errorf("first rule = %q at line %d", a.Prelude, a.Line)
	}
	if got := Selectors(a.Prelude); !reflect.DeepEqual(got, []string{".a", ".b:where(:hover)"}) {
		t.Errorf("selectors = %q", got)
	}
	want := []Decl{
		{Property: "color", Value: "#fff", Line: 3, Col: 10},
		{Property: "background", Value: `url("x;y{z}.png") no-repeat`, Line: 4, Col: 15},
		{Property: "margin", Value: "0 !important", Line: 5, Col: 11, Important: true},
	}
	if !reflect.DeepEqual(a.Decls, want) {
		t.Errorf("decls = %+v\nwant %+v", a.Decls, want)
	}
	media := rules[1]
	if media.Prelude != "@media (max-width: 900px)" || len(media.Rules) != 1 || media.Rules[0].Decls[0].Property != "--helm-x" || media.Rules[0].Decls[0].Line != 8 {
		t.Errorf("media = %+v", media)
	}
	if rules[2].Block || rules[2].Prelude != `@import url("other.css")` {
		t.Errorf("import = %+v", rules[2])
	}
}

func TestParseDeclarationsOffsetsIntoTheFile(t *testing.T) {
	got := ParseDeclarations("color: red; font-family: Arial", 7, 20)
	want := []Decl{
		{Property: "color", Value: "red", Line: 7, Col: 27},
		{Property: "font-family", Value: "Arial", Line: 7, Col: 45},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decls = %+v\nwant %+v", got, want)
	}
}
