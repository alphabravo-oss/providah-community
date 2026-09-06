package inputspec

import (
	"strings"
	"testing"
)

func TestInputContract(t *testing.T) {
	schema := `{"version":1,"inputs":[{"name":"size","label":"Size","type":"string","choices":["small","large"]},{"name":"count","label":"Count","type":"integer","min":1,"max":3},{"name":"enabled","label":"Enabled","type":"boolean"}]}`
	fields, e := Parse([]byte(schema))
	if e != nil {
		t.Fatal(e)
	}
	got, e := Values(fields, []byte(`{"size":"small","enabled":true,"count":2}`))
	if e != nil || string(got) != `{"count":2,"enabled":true,"size":"small"}` {
		t.Fatal(string(got), e)
	}
	for _, raw := range []string{`{}`, `null`, `{"size":"small","count":4,"enabled":true}`, `{"size":"small","count":2,"enabled":"true"}`, `{"size":"other","count":2,"enabled":true}`, `{"size":"small","count":2,"enabled":null}`, `{"size":"small","count":2,"enabled":true,"extra":1}`, `{"size":"small","count":2,"count":3,"enabled":true}`} {
		if _, e := Values(fields, []byte(raw)); e == nil {
			t.Fatal("invalid inputs accepted", raw)
		}
	}
	for _, raw := range []string{strings.Replace(schema, `"size"`, `"ansible_host"`, 1), strings.Replace(schema, `"min":1,`, ``, 1), strings.Replace(schema, `["small","large"]`, `[]`, 1), strings.Replace(schema, `"boolean"`, `"secret"`, 1), schema + `{}`} {
		if _, e := Parse([]byte(raw)); e == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}
