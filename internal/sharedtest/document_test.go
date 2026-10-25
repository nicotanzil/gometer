package sharedtest

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const refID = "11111111111111111111111111111111"

func document(public string, bindings ...Binding) []byte {
	raw, _ := json.Marshal(Document{SchemaVersion: 1, PublicConfig: json.RawMessage(public), SecretBindings: bindings})
	return raw
}
func TestPublicDocumentAndTypedBindings(t *testing.T) {
	cases := []struct{ public, path string }{
		{`{"name":"Public","url":"https://internal.example"}`, ""},
		{`{"url":"http://127.0.0.1:1","headers":{"Authorization":""}}`, "/headers/Authorization"},
		{`{"url":"https://target.example","method":"POST","body":""}`, "/body"},
		{`{"url":"https://target.example","variables":{"password":""},"scenarios":[{"name":"Login","steps":[{"name":"Login","url":"/login","method":"POST","body":"{{password|json}}"}]}]}`, "/variables/password"},
		{`{"url":"https://target.example","scenarios":[{"name":"Login","variables":{"headers":""},"steps":[{"name":"Request","url":"/"}]}]}`, "/scenarios/0/variables/headers"},
		{`{"url":"https://target.example","scenarios":[{"name":"Login","data":[{"password":""}],"steps":[{"name":"Login","url":"/login","method":"POST","body":"{{password|json}}"}]}]}`, "/scenarios/0/data/0/password"},
		{`{"url":"https://target.example","scenarios":[{"name":"Login","steps":[{"name":"Request","url":"/","headers":{"X-Key~":""}}]}]}`, "/scenarios/0/steps/0/headers/X-Key~0"},
		{`{"url":"https://target.example","scenarios":[{"name":"Login","steps":[{"name":"Repeat","loop":{"count":1,"steps":[{"name":"Request","url":"/","headers":{"Authorization":""}}]}}]}]}`, "/scenarios/0/steps/0/loop/steps/0/headers/Authorization"},
		{`{"url":"https://target.example","scenarios":[{"name":"Login","steps":[{"name":"Branch","if":{"condition":{"variable":"iteration","value":"1"},"then":[{"name":"Request","url":"/","body":"","method":"POST"}]}}]}]}`, "/scenarios/0/steps/0/if/then/0/body"},
	}
	for _, tc := range cases {
		var bindings []Binding
		if tc.path != "" {
			bindings = []Binding{{Path: tc.path, ReferenceID: refID, Revision: 1}}
		}
		d, _, err := Parse(document(tc.public, bindings...))
		if err != nil {
			t.Fatalf("path %q: %v", tc.path, err)
		}
		encoded, err := json.Marshal(d)
		if err != nil || !strings.Contains(string(encoded), `"public_config"`) {
			t.Fatal("public document did not round trip")
		}
	}
}
func TestRejectsAmbiguousDocumentsAndUnsafeBindings(t *testing.T) {
	valid := string(document(`{"url":"https://target.example","headers":{"Authorization":""}}`, Binding{"/headers/Authorization", refID, 1}))
	cases := []string{
		`null`, `{}`, valid + ` {}`,
		strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"SCHEMA_VERSION":1`, 1),
		strings.Replace(valid, `"public_config":`, `"value":"PRIVATE_VALUE","public_config":`, 1),
		strings.Replace(valid, `"Authorization":""`, `"Authorization":"PRIVATE_VALUE"`, 1),
		strings.Replace(valid, `"Authorization":""`, `"Authorization":"","authorization":""`, 1),
		strings.Replace(valid, `"revision":1`, `"revision":0`, 1),
		strings.Replace(valid, refID, strings.Repeat("A", 32), 1),
		strings.Replace(valid, `"path":"/headers/Authorization"`, `"value":"PRIVATE_VALUE","path":"/headers/Authorization"`, 1),
		strings.Replace(valid, `"/headers/Authorization"`, `"/headers/Missing"`, 1),
		strings.Replace(valid, `"/headers/Authorization"`, `"/headers/Authorization~2"`, 1),
		string(document(`{"url":"https://target.example","headers":{"Cookie":""}}`)),
		string(document(`{"url":"https://target.example","headers":{"X-Api-Key":"PRIVATE_VALUE"}}`)),
		string(document(`{"url":"https://target.example","headers":{"Host":""}}`, Binding{"/headers/Host", refID, 1})),
		string(document(`{"url":"https://target.example"}`, Binding{"/url", refID, 1})),
		string(document(`{"url":"https://target.example","body":""}`, Binding{"/body", refID, 1}, Binding{"/body", refID, 1})),
		string(document(`{"url":"https://target.example","scenarios":[{"name":"S","steps":[{"name":"R","body":""}]}]}`, Binding{"/scenarios/00/steps/0/body", refID, 1})),
		strings.Repeat(" ", 1<<20) + valid,
	}
	for i, raw := range cases {
		_, _, err := Parse([]byte(raw))
		if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "PRIVATE_VALUE") {
			t.Fatalf("case %d accepted or disclosed input: %v", i, err)
		}
	}
}
