package apicmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFields_NestedAndTypedValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		fields []fieldInput
		want   string
	}{
		{"raw string", []fieldInput{{"name=42", false}}, `{"name":"42"}`},
		{"typed literals", []fieldInput{{"enabled=true", true}, {"disabled=false", true}, {"count=42", true}, {"empty=null", true}, {"other=1.5", true}}, `{"enabled":true,"disabled":false,"count":42,"empty":null,"other":"1.5"}`},
		{"typed int beyond 32-bit width", []fieldInput{{"big=2147483648", true}, {"small=-2147483649", true}}, `{"big":2147483648,"small":-2147483649}`},
		{"typed int beyond 64-bit width stays string", []fieldInput{{"huge=99999999999999999999", true}}, `{"huge":"99999999999999999999"}`},
		{"raw gh literals", []fieldInput{{"robot=Hubot", false}, {"destroyer=false", false}, {"helper=true", false}, {"location=@work", false}}, `{"robot":"Hubot","destroyer":"false","helper":"true","location":"@work"}`},
		{"nested map", []fieldInput{{"config[name]=one", false}, {"config[active]=true", true}}, `{"config":{"name":"one","active":true}}`},
		{"array", []fieldInput{{"names[]=one", false}, {"names[]=two", false}}, `{"names":["one","two"]}`},
		{"array objects", []fieldInput{{"items[][name]=one", false}, {"items[][count]=1", true}, {"items[][name]=two", false}}, `{"items":[{"name":"one","count":1},{"name":"two"}]}`},
		{"gh nested arrays", []fieldInput{{"branch[name]=patch-1", false}, {"robots[]=Hubot", false}, {"robots[]=Dependabot", false}, {"labels[][name]=bug", false}, {"labels[][color]=red", false}, {"labels[][colorOptions][]=red", false}, {"labels[][colorOptions][]=blue", false}, {"labels[][name]=feature", false}, {"labels[][color]=green", false}, {"labels[][colorOptions][]=red", false}, {"labels[][colorOptions][]=green", false}, {"labels[][colorOptions][]=yellow", false}, {"nested[][key1][key2][key3]=value", false}, {"empty[]", false}, {"branch[protections]=true", true}, {"ids[]=123", true}, {"ids[]=456", true}}, `{"branch":{"name":"patch-1","protections":true},"robots":["Hubot","Dependabot"],"labels":[{"name":"bug","color":"red","colorOptions":["red","blue"]},{"name":"feature","color":"green","colorOptions":["red","green","yellow"]}],"nested":[{"key1":{"key2":{"key3":"value"}}}],"empty":[],"ids":[123,456]}`},
		{"empty array", []fieldInput{{"names[]", false}}, `{"names":[]}`},
		{"stdin", []fieldInput{{"body=@-", true}}, `{"body":"hello"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseFields(tc.fields, strings.NewReader("hello"))

			require.NoError(t, err)
			encoded, err := json.Marshal(got)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(encoded))
		})
	}
}

func TestMagicFieldValue_ReadsFileAndReportsMissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "field.txt")
	require.NoError(t, os.WriteFile(path, []byte("file contents"), 0600))

	value, err := magicFieldValue("@"+path, strings.NewReader(""))

	require.NoError(t, err)
	assert.Equal(t, "file contents", value)
	_, err = magicFieldValue("@", strings.NewReader(""))
	require.Error(t, err)
}

func TestParseFields_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		fields []fieldInput
	}{
		{"missing equals", []fieldInput{{"name", false}}},
		{"invalid bracket", []fieldInput{{"a[=1", false}}},
		{"blank root", []fieldInput{{"[]=x", false}}},
		{"string to array", []fieldInput{{"object[field]=A", false}, {"object[field][]=x", false}}},
		{"string to object", []fieldInput{{"object[field]=B", false}, {"object[field][field2]=x", false}}},
		{"object to string", []fieldInput{{"object[field][field2]=C", false}, {"object[field]=x", false}}},
		{"object to array", []fieldInput{{"object[field][field2]=D", false}, {"object[field][]=x", false}}},
		{"array to string", []fieldInput{{"object[field][]=E", false}, {"object[field]=x", false}}},
		{"array to object", []fieldInput{{"object[field][]=F", false}, {"object[field][field2]=x", false}}},
		{"root scalar to map", []fieldInput{{"a=1", false}, {"a[b]=2", false}}},
		{"duplicate scalar", []fieldInput{{"a=1", false}, {"a=2", false}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFields(tc.fields, strings.NewReader(""))

			assert.Error(t, err)
		})
	}
}
