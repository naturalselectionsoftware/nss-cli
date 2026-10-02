package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionDoesNotCallAPI(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("version attempted an API request")
		return nil, nil
	})}
	for _, args := range [][]string{{"--version"}, {"--version", "--json"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut, func(string) string { return "" }, client); code != 0 {
			t.Fatalf("version exited %d: %s", code, errOut.String())
		}
		if !strings.Contains(out.String(), version) {
			t.Fatalf("missing version: %s", out.String())
		}
		if len(args) == 2 && !json.Valid(out.Bytes()) {
			t.Fatalf("invalid version JSON: %s", out.String())
		}
	}
}

func TestTokenRedactedFromEchoedResponsesAndTransportErrors(t *testing.T) {
	const secret = "nss_at_synthetic-secret"
	for _, jsonOut := range []bool{false, true} {
		for _, mode := range []string{"success", "api-error", "transport-error", "usage-error"} {
			t.Run(mode+map[bool]string{true: "-json", false: "-human"}[jsonOut], func(t *testing.T) {
				client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					if mode == "transport-error" {
						return nil, errors.New("transport echoed " + secret)
					}
					status := 200
					body := `{"organizationId":"org","scopes":["` + secret + `"]}`
					if mode == "api-error" {
						status = 401
						body = `{"code":"INVALID_CREDENTIAL","detail":"echoed ` + secret + `"}`
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				args := []string{"whoami"}
				want := 0
				switch mode {
				case "api-error":
					want = 4
				case "transport-error":
					want = 3
				case "usage-error":
					args = []string{"--url"}
					want = 3
				}
				if jsonOut {
					args = append(args, "--json")
				}
				var out, errOut bytes.Buffer
				if code := run(args, &out, &errOut, func(k string) string {
					if k == "NSS_TOKEN" {
						return secret
					}
					return ""
				}, client); code != want {
					t.Fatalf("code %d, want %d", code, want)
				}
				if strings.Contains(out.String()+errOut.String(), secret) {
					t.Fatal("token leaked")
				}
				output := out.Bytes()
				if want != 0 {
					output = errOut.Bytes()
				}
				if jsonOut && !json.Valid(output) {
					t.Fatalf("invalid JSON: %s", output)
				}
				if mode != "usage-error" && !strings.Contains(string(output), "[REDACTED]") {
					t.Fatalf("expected redaction: %s", output)
				}
			})
		}
	}
}

func TestCountContractAndEnvironment(t *testing.T) {
	var got map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/automation/whoami" {
			json.NewEncoder(w).Encode(identity{CredentialType: "organization_automation_token", TokenID: "tok", OrganizationID: "org", Scopes: []string{"events:read"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization not supplied")
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("X-Request-ID", "request-1")
		json.NewEncoder(w).Encode(map[string]any{"count": 2})
	}))
	defer s.Close()
	env := func(k string) string {
		if k == "NSS_TOKEN" {
			return "secret"
		}
		if k == "NSS_URL" {
			return s.URL
		}
		return ""
	}
	var out, err bytes.Buffer
	if code := run([]string{"events", "count", "--query", `type:"entry-completed" AND severity:ERROR`, "--json"}, &out, &err, env, s.Client()); code != 0 {
		t.Fatalf("code %d: %s", code, err.String())
	}
	where := got["query"].(map[string]any)["where"].(map[string]any)
	if where["kind"] != "all" {
		t.Fatalf("unexpected query: %#v", got)
	}
	if !strings.Contains(out.String(), `"requestId": "request-1"`) {
		t.Fatalf("missing request ID: %s", out.String())
	}
}

func TestMachineErrorAndTokenRedaction(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "r-2")
		w.WriteHeader(401)
		w.Write([]byte(`{"code":"INVALID_CREDENTIAL","detail":"expired"}`))
	}))
	defer s.Close()
	env := func(k string) string {
		if k == "NSS_TOKEN" {
			return "top-secret"
		}
		return s.URL
	}
	var out, err bytes.Buffer
	code := run([]string{"whoami", "--json"}, &out, &err, env, s.Client())
	if code != 4 {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(err.String(), "top-secret") {
		t.Fatal("token leaked")
	}
	if !strings.Contains(err.String(), `"code":"INVALID_CREDENTIAL"`) || !strings.Contains(err.String(), `"requestId":"r-2"`) {
		t.Fatalf("bad error %s", err.String())
	}
}

func TestQueryPrecedence(t *testing.T) {
	q, e := parseQuery(common{query: "source:a OR severity:error AND NOT type:b"})
	if e != nil {
		t.Fatal(e)
	}
	w := q["where"].(map[string]any)
	if w["kind"] != "any" {
		t.Fatalf("%#v", w)
	}
}

func TestQueryPreservesCaseInsensitiveSourceAndTypeSemantics(t *testing.T) {
	q, e := parseQuery(common{query: "source:CHECKOUT AND type:Entry-Completed"})
	if e != nil {
		t.Fatal(e)
	}
	expressions := q["where"].(map[string]any)["expressions"].([]any)
	for _, expression := range expressions {
		predicate := expression.(map[string]any)
		if predicate["operator"] != "CASE_INSENSITIVE_EQ" {
			t.Fatalf("predicate lost EQL case-insensitive semantics: %#v", predicate)
		}
	}
}

func TestJSONFlagParseFailureUsesUsageErrorContract(t *testing.T) {
	env := func(k string) string {
		if k == "NSS_TOKEN" {
			return "token"
		}
		return "http://unused.invalid"
	}
	var out, err bytes.Buffer
	code := run([]string{"events", "count", "--bogus", "--json"}, &out, &err, env,
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			body := io.NopCloser(strings.NewReader(`{"credentialType":"organization_automation_token","organizationId":"org"}`))
			return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
		})})
	if code != 3 {
		t.Fatalf("code %d", code)
	}
	var response map[string]any
	if json.Unmarshal(err.Bytes(), &response) != nil {
		t.Fatalf("not JSON: %s", err.String())
	}
	if strings.Contains(err.String(), "flag provided but not defined:") && strings.Count(strings.TrimSpace(err.String()), "\n") > 0 {
		t.Fatalf("plaintext flag diagnostic leaked: %s", err.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchSendsPaginationExplicitly(t *testing.T) {
	var got map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/automation/whoami" {
			json.NewEncoder(w).Encode(identity{OrganizationID: "org"})
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"continuationCursor": "next", "events": []any{}})
	}))
	defer s.Close()
	env := func(k string) string {
		if k == "NSS_TOKEN" {
			return "token"
		}
		return s.URL
	}
	var out, err bytes.Buffer
	if code := run([]string{"events", "search", "--query", "severity:ERROR", "--limit", "7", "--cursor", "opaque", "--json"}, &out, &err, env, s.Client()); code != 0 {
		t.Fatalf("code %d: %s", code, err.String())
	}
	if got["pageSize"] != float64(7) || got["cursor"] != "opaque" {
		t.Fatalf("pagination not preserved: %#v", got)
	}
}

func TestURLFlagPrecedesEnvironment(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(identity{CredentialType: "organization_automation_token", OrganizationID: "org"})
	}))
	defer s.Close()
	env := func(k string) string {
		if k == "NSS_TOKEN" {
			return "token"
		}
		if k == "NSS_URL" {
			return "http://environment.invalid"
		}
		return ""
	}
	var out, err bytes.Buffer
	if code := run([]string{"--url", s.URL, "whoami", "--json"}, &out, &err, env, s.Client()); code != 0 {
		t.Fatalf("code %d: %s", code, err.String())
	}
}

func TestHierarchicalHelpDoesNotRequireTokenOrCallAPI(t *testing.T) {
	topics := [][]string{
		{"--help"},
		{"events", "--help"},
		{"events", "search", "--help"},
		{"events", "get", "--help"},
		{"events", "count", "--help"},
		{"events", "aggregate", "--help"},
		{"events", "series", "--help"},
		{"catalog", "--help"},
		{"semantics", "--help"},
		{"help", "events", "count"},
		{"events", "search", "--query", "severity:ERROR", "--limit", "25", "--help"},
		{"events", "get", "123e4567-e89b-12d3-a456-426614174000", "--help"},
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("help attempted an API request")
		return nil, nil
	})}
	for _, args := range topics {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var out, err bytes.Buffer
			if code := run(args, &out, &err, func(string) string { return "" }, client); code != 0 {
				t.Fatalf("code %d: %s", code, err.String())
			}
			if out.Len() == 0 {
				t.Fatal("empty help output")
			}
			if len(args) == 2 && args[0] == "events" && strings.Contains(out.String(), "--query is required") {
				t.Fatalf("help fell through to command validation: %s", out.String())
			}
		})
	}
}

func TestTopLevelHelpIsAgentReadyAndRedactsEnvironmentToken(t *testing.T) {
	const secret = "must-not-appear-in-help"
	env := func(key string) string {
		if key == "NSS_TOKEN" {
			return secret
		}
		return ""
	}
	var out, err bytes.Buffer
	if code := run([]string{"--help"}, &out, &err, env, http.DefaultClient); code != 0 {
		t.Fatalf("code %d: %s", code, err.String())
	}
	help := out.String()
	for _, want := range []string{
		"Recommended investigation flow", "whoami", "catalog", "semantics",
		"events search", "EQL v1", "--from/--to", "--snapshot", "--json",
		"NSS_TOKEN", "NSS_URL", `type:"entry-completed"`, "severity:ERROR",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("top-level help missing %q", want)
		}
	}
	if strings.Contains(help, secret) {
		t.Fatal("help exposed NSS_TOKEN contents")
	}
}

func TestCommandHelpDocumentsRequiredFlagsAndExamples(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"events", "search", "--help"}, []string{"--query EQL", "--limit", "--cursor", "continuationCursor", "Example:"}},
		{[]string{"events", "aggregate", "--help"}, []string{"--group-by", "source", "eventType", "severity", "Example:"}},
		{[]string{"events", "series", "--help"}, []string{"--bucket", "HOUR", "DAY", "WEEK", "Example:"}},
		{[]string{"semantics", "--help"}, []string{"--terms", "comma-separated", "Example:"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args[:len(tt.args)-1], "_"), func(t *testing.T) {
			var out, err bytes.Buffer
			if code := run(tt.args, &out, &err, func(string) string { return "" }, http.DefaultClient); code != 0 {
				t.Fatalf("code %d: %s", code, err.String())
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help missing %q: %s", want, out.String())
				}
			}
		})
	}
}

func TestHelpDocumentsOperationSemanticsAccurately(t *testing.T) {
	var top, series, get bytes.Buffer
	env := func(string) string { return "" }
	for _, tt := range []struct {
		args []string
		out  *bytes.Buffer
	}{
		{[]string{"--help"}, &top},
		{[]string{"events", "series", "--help"}, &series},
		{[]string{"events", "get", "--help"}, &get},
	} {
		if code := run(tt.args, tt.out, io.Discard, env, http.DefaultClient); code != 0 {
			t.Fatalf("%v exited %d", tt.args, code)
		}
	}
	if strings.Contains(top.String(), "receivedAt") || !strings.Contains(top.String(), "bound occurredAt") {
		t.Fatalf("top-level help misstates time-bound semantics: %s", top.String())
	}
	for _, want := range []string{"--from RFC3339 --to RFC3339", "absolute", "--from 2026-10-01T00:00:00Z", "--to 2026-10-02T00:00:00Z"} {
		if !strings.Contains(series.String(), want) {
			t.Errorf("series help missing %q: %s", want, series.String())
		}
	}
	if !strings.Contains(get.String(), "123e4567-e89b-12d3-a456-426614174000") {
		t.Fatalf("events get help lacks a valid UUID example: %s", get.String())
	}
}
