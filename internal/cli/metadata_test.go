package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetadataValuesAndExplicitRevision(t *testing.T) {
	for _, value := range []string{`"waiting_external"`, `true`, `null`, `9007199254740993`, `[1,null]`, `{"nested":false}`} {
		t.Run(value, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				assertEqual(t, "POST", r.Method)
				assertEqual(t, "/api/cards/card1/metadata/", r.URL.Path)
				assertEqual(t, "Bearer test-token", r.Header.Get("Authorization"))
				var body struct {
					Set      map[string]json.RawMessage `json:"set"`
					Revision int                        `json:"expected_revision"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				assertEqual(t, 7, body.Revision)
				assertEqual(t, value, string(body.Set["ops.status"]))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"id":"card1","metadata":{"ops.status":` + value + `},"metadata_revision":8}}`))
			}))
			defer server.Close()
			t.Setenv("KARDBRD_TOKEN", "test-token")
			stdout, _, err := executeRoot("--api-url", server.URL, "card", "metadata", "set", "card1", "ops.status", value, "--if-revision", "7")
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, 1, requests)
			if !json.Valid([]byte(stdout)) {
				t.Fatal(stdout)
			}
			if value == `9007199254740993` {
				assertCLIContains(t, stdout, value)
			}
		})
	}
}

func TestMetadataAcceptsLastWritableRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "9223372036854775806", string(body["expected_revision"]))
		_, _ = w.Write([]byte(`{"data":{"metadata_revision":9223372036854775807}}`))
	}))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")

	_, _, err := executeRoot("--api-url", server.URL, "card", "metadata", "set", "card1", "owner", `"A"`, "--if-revision", "9223372036854775806")
	if err != nil {
		t.Fatal(err)
	}
}

func TestMetadataExhaustedRevisionDoesNotPost(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertEqual(t, "GET", r.Method)
		gets++
		_, _ = w.Write([]byte(`{"data":{"id":"card1","metadata":{},"metadata_revision":9223372036854775807}}`))
	}))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")
	_, _, err := executeRoot("--api-url", server.URL, "card", "metadata", "set", "card1", "owner", `"A"`)
	if err == nil || !strings.Contains(err.Error(), "cannot advance") {
		t.Fatalf("error = %v", err)
	}
	assertEqual(t, 1, gets)
}

func TestMetadataFetchesRevisionOnceAndDoesNotRetryConflict(t *testing.T) {
	gets, posts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			gets++
			_, _ = w.Write([]byte(`{"data":{"id":"card1","metadata":{},"metadata_revision":12}}`))
			return
		}
		posts++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "12", string(body["expected_revision"]))
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"Metadata changed; fetch again","code":"METADATA_CONFLICT"}`))
	}))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")
	_, _, err := executeRoot("--api-url", server.URL, "card", "metadata", "set", "card1", "owner", `"A"`)
	if err == nil || !strings.Contains(err.Error(), "METADATA_CONFLICT") {
		t.Fatalf("error = %v", err)
	}
	assertEqual(t, 1, gets)
	assertEqual(t, 1, posts)
}

func TestMetadataGetDistinguishesMissingFromNullAndPreservesNumbers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"card1","metadata":{"nil":null,"n":9007199254740993},"metadata_revision":3}}`))
	}))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")
	for _, tc := range []struct {
		key, want string
		fail      bool
	}{{"nil", "null", false}, {"n", "9007199254740993", false}, {"missing", "", true}} {
		out, _, err := executeRoot("--api-url", server.URL, "card", "metadata", "get", "card1", tc.key)
		if tc.fail {
			if err == nil {
				t.Fatal("missing key must fail")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, tc.want, strings.TrimSpace(out))
		}
	}
}

func TestMetadataBulkUpdateFromStdinAndRemoveLiteralKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Set      map[string]json.RawMessage `json:"set"`
			Remove   []string                   `json:"remove"`
			Revision int                        `json:"expected_revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		assertEqual(t, `{"array":[true,null]}`, string(body.Set["a.b/c"]))
		assertEqual(t, "old,key", body.Remove[0])
		assertEqual(t, 0, body.Revision)
		_, _ = w.Write([]byte(`{"data":{"metadata_revision":1}}`))
	}))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"--api-url", server.URL, "card", "metadata", "update", "card1", "--set-file", "-", "--remove", "old,key", "--if-revision", "0"})
	cmd.SetIn(strings.NewReader(`{"a.b/c":{"array":[true,null]}}`))
	cmd.SetOut(new(bytes.Buffer))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataBulkUpdateFromFilePreservesExactJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Set      map[string]json.RawMessage `json:"set"`
			Revision int64                      `json:"expected_revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		assertEqual(t, `9007199254740993`, string(body.Set["large.number"]))
		assertEqual(t, int64(0), body.Revision)
		_, _ = w.Write([]byte(`{"data":{"metadata":{"large.number":9007199254740993},"metadata_revision":1}}`))
	}))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")

	setFile := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(setFile, []byte(`{"large.number":9007199254740993}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := executeRoot("--api-url", server.URL, "card", "metadata", "update", "card1", "--set-file", setFile, "--if-revision", "0")
	if err != nil {
		t.Fatal(err)
	}
	assertCLIContains(t, stdout, "9007199254740993")
}

func TestMetadataRemoveSendsNoNullValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["set"]; ok {
			t.Fatal("remove must not assign null")
		}
		assertEqual(t, `["x","a.b"]`, string(body["remove"]))
		_, _ = w.Write([]byte(`{"data":{"metadata_revision":2}}`))
	}))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")
	_, _, err := executeRoot("--api-url", server.URL, "card", "metadata", "remove", "card1", "x", "a.b", "--if-revision", "1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestInvalidMetadataArgumentsDoNotMakeRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") }))
	defer server.Close()
	t.Setenv("KARDBRD_TOKEN", "test-token")
	for _, args := range [][]string{
		{"set", "card1", "x", "unquoted"}, {"set", "card1", "x", "NaN"},
		{"set", "card1", "x", "true", "--if-revision", "-1"},
		{"set", "card1", "x", "true", "--if-revision", "9223372036854775807"},
		{"update", "card1", "--set", "[]"}, {"update", "card1", "--set", "null"},
		{"update", "card1", "--set", "{}"}, {"update", "card1", "--set", "{}", "--set-file", "-"},
		{"update", "card1", "--set", `{"x":1}`, "--remove", "x"},
		{"set", "card1", "x", "true", "--format", "tsv"},
	} {
		_, _, err := executeRoot(append([]string{"--api-url", server.URL, "card", "metadata"}, args...)...)
		if err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}
