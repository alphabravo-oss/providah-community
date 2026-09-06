package tfstate

import (
	"strings"
	"testing"
)

func TestStateIdentity(t *testing.T) {
	valid := `{"version":4,"lineage":"00112233-4455-6677-8899-aabbccddeeff","serial":0,"outputs":{"secret":{"value":"preserved"}}}`
	m, e := Parse([]byte(valid))
	if e != nil || m.Serial != 0 || len(m.SHA256) != 64 {
		t.Fatal(m, e)
	}
	for _, v := range []string{`null`, `[]`, valid + `{}`, strings.Replace(valid, `"serial":0`, `"serial":-1`, 1), strings.Replace(valid, `"serial":0`, `"serial":null`, 1), strings.Replace(valid, `"serial":0`, `"serial":1.1`, 1), strings.Replace(valid, `"serial":0`, `"serial":9223372036854775808`, 1), strings.Replace(valid, `"version":4`, `"version":3`, 1), strings.Replace(valid, `"serial":0`, `"serial":0,"serial":1`, 1), strings.Replace(valid, `00112233-4455-6677-8899-aabbccddeeff`, `bad`, 1)} {
		if _, e := Parse([]byte(v)); e == nil {
			t.Fatal("invalid state accepted")
		}
	}
}
