// Command nss provides deterministic, organization-scoped access to NSS events.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultURL = "http://localhost:8080"

// version is set by GoReleaser for tagged releases.
var version = "dev"

type apiClient struct {
	base, token string
	http        *http.Client
}
type apiError struct {
	Status                  int
	Code, Detail, RequestID string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s: %s (request %s)", e.Code, e.Detail, e.RequestID)
}

func (c *apiClient) do(method, path string, body any) (json.RawMessage, string, error) {
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.base, "/")+path, input)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, "", err
	}
	rid := resp.Header.Get("X-Request-ID")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var p struct{ Code, Detail, RequestID string }
		_ = json.Unmarshal(b, &p)
		if p.Code == "" {
			p.Code = "HTTP_" + strconv.Itoa(resp.StatusCode)
		}
		if p.Detail == "" {
			p.Detail = strings.TrimSpace(string(b))
		}
		if p.RequestID != "" {
			rid = p.RequestID
		}
		return nil, rid, &apiError{resp.StatusCode, p.Code, p.Detail, rid}
	}
	return b, rid, nil
}

type identity struct {
	CredentialType string   `json:"credentialType"`
	TokenID        string   `json:"tokenId"`
	OrganizationID string   `json:"organizationId"`
	Scopes         []string `json:"scopes"`
}

func (c *apiClient) identity() (identity, error) {
	var v identity
	b, _, e := c.do("GET", "/api/v1/automation/whoami", nil)
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return v, e
}

type common struct{ query, from, to, snapshot string }

func addCommon(f *flag.FlagSet, c *common) {
	f.StringVar(&c.query, "query", "", "EQL v1 expression")
	f.StringVar(&c.from, "from", "", "RFC 3339 lower time bound")
	f.StringVar(&c.to, "to", "", "RFC 3339 upper time bound")
	f.StringVar(&c.snapshot, "snapshot", "", "database snapshot returned by a prior operation")
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, &http.Client{Timeout: 30 * time.Second}))
}
func run(args []string, out, errOut io.Writer, getenv func(string) string, hc *http.Client) int {
	// Buffer complete output so a credential split across writes is also redacted.
	token := getenv("NSS_TOKEN")
	var stdout, stderr bytes.Buffer
	code := runCommand(args, &stdout, &stderr, getenv, hc)
	for _, stream := range []struct {
		buffer *bytes.Buffer
		writer io.Writer
	}{{&stdout, out}, {&stderr, errOut}} {
		text := stream.buffer.String()
		if token != "" {
			encoded, _ := json.Marshal(token)
			for _, secret := range []string{string(encoded[1 : len(encoded)-1]), url.QueryEscape(token), url.PathEscape(token), token} {
				text = strings.ReplaceAll(text, secret, "[REDACTED]")
			}
		}
		fmt.Fprint(stream.writer, text)
	}
	return code
}

func runCommand(args []string, out, errOut io.Writer, getenv func(string) string, hc *http.Client) int {
	jsonOut := removeBool(&args, "--json")
	urlOverride, urlSet, urlErr := removeValue(&args, "--url")
	if urlErr != nil {
		return usageError(errOut, jsonOut, urlErr.Error())
	}
	base := getenv("NSS_URL")
	if base == "" {
		base = defaultURL
	}
	if urlSet {
		base = urlOverride
	}
	if len(args) == 1 && args[0] == "--version" {
		return emit(out, jsonOut, map[string]string{"version": version}, "nss "+version+"\n")
	}
	if topic, ok := requestedHelp(args); ok {
		if !writeHelp(out, topic) {
			return usageError(errOut, jsonOut, "unknown help topic: "+strings.Join(topic, " "))
		}
		return 0
	}
	token := getenv("NSS_TOKEN")
	if token == "" {
		return fail(errOut, jsonOut, &apiError{Code: "MISSING_TOKEN", Detail: "NSS_TOKEN is required"})
	}
	c := &apiClient{base: base, token: token, http: hc}
	cmd := args[0]
	args = args[1:]
	if cmd == "whoami" {
		id, e := c.identity()
		if e != nil {
			return fail(errOut, jsonOut, e)
		}
		return emit(out, jsonOut, id, fmt.Sprintf("Organization: %s\nCredential: %s\nToken ID: %s\nScopes: %s\n", id.OrganizationID, id.CredentialType, id.TokenID, strings.Join(id.Scopes, ", ")))
	}
	id, e := c.identity()
	if e != nil {
		return fail(errOut, jsonOut, e)
	}
	root := "/api/v1/organizations/" + url.PathEscape(id.OrganizationID) + "/event-operations"
	if cmd == "catalog" {
		f := flag.NewFlagSet("catalog", flag.ContinueOnError)
		snapshot := f.String("snapshot", "", "database snapshot")
		if e = parseFlags(f, args); e != nil {
			return usageError(errOut, jsonOut, e.Error())
		}
		p := root + "/catalog"
		if *snapshot != "" {
			p += "?snapshot=" + url.QueryEscape(*snapshot)
		}
		return request(c, "GET", p, nil, out, errOut, jsonOut)
	}
	if cmd == "semantics" {
		f := flag.NewFlagSet("semantics", flag.ContinueOnError)
		var x common
		addCommon(f, &x)
		terms := f.String("terms", "", "comma-separated discovery terms")
		limit := f.Int("limit", 20, "sample limit")
		if e = parseFlags(f, args); e != nil {
			return usageError(errOut, jsonOut, e.Error())
		}
		if *terms == "" {
			return usageError(errOut, jsonOut, "--terms is required")
		}
		body := map[string]any{"terms": splitCSV(*terms), "limit": *limit}
		putTimes(body, x)
		return request(c, "POST", root+"/semantic-discovery", body, out, errOut, jsonOut)
	}
	if cmd != "events" || len(args) == 0 {
		return usageError(errOut, jsonOut, "unknown command")
	}
	sub := args[0]
	args = args[1:]
	if sub == "get" {
		f := flag.NewFlagSet("events get", flag.ContinueOnError)
		if e = parseFlags(f, args); e != nil {
			return usageError(errOut, jsonOut, e.Error())
		}
		if f.NArg() != 1 {
			return usageError(errOut, jsonOut, "events get requires one event ID")
		}
		p := "/api/v1/events/" + url.PathEscape(f.Arg(0)) + "?organizationId=" + url.QueryEscape(id.OrganizationID)
		return request(c, "GET", p, nil, out, errOut, jsonOut)
	}
	f := flag.NewFlagSet("events "+sub, flag.ContinueOnError)
	var x common
	addCommon(f, &x)
	limit := f.Int("limit", 50, "page/result safety limit")
	cursor := f.String("cursor", "", "continuation cursor")
	group := f.String("group-by", "", "source, eventType, or severity")
	bucket := f.String("bucket", "", "HOUR, DAY, or WEEK")
	fields := f.String("fields", "", "comma-separated resource,attributes,raw_payload")
	if e = parseFlags(f, args); e != nil {
		return usageError(errOut, jsonOut, e.Error())
	}
	query, e := parseQuery(x)
	if e != nil {
		return usageError(errOut, jsonOut, e.Error())
	}
	switch sub {
	case "search":
		body := map[string]any{"query": query, "pageSize": *limit}
		if *cursor != "" {
			body["cursor"] = *cursor
		}
		if *fields != "" {
			body["fields"] = upperCSV(*fields)
		}
		return request(c, "POST", root+"/search", body, out, errOut, jsonOut)
	case "count":
		return request(c, "POST", root+"/count", operationBody(query, x.snapshot), out, errOut, jsonOut)
	case "aggregate":
		if *group == "" {
			return usageError(errOut, jsonOut, "--group-by is required")
		}
		body := operationBody(query, x.snapshot)
		body["groupBy"] = *group
		return request(c, "POST", root+"/aggregate", body, out, errOut, jsonOut)
	case "series":
		if *bucket == "" {
			return usageError(errOut, jsonOut, "--bucket is required")
		}
		body := operationBody(query, x.snapshot)
		body["bucket"] = strings.ToUpper(*bucket)
		return request(c, "POST", root+"/time-series", body, out, errOut, jsonOut)
	default:
		return usageError(errOut, jsonOut, "unknown events command")
	}
}

func request(c *apiClient, method, path string, body any, out, errOut io.Writer, j bool) int {
	b, r, e := c.do(method, path, body)
	if e != nil {
		return fail(errOut, j, e)
	}
	if j {
		var v any
		if json.Unmarshal(b, &v) == nil {
			if m, ok := v.(map[string]any); ok && r != "" {
				m["requestId"] = r
			}
			b, _ = json.Marshal(v)
		}
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, b, "", "  ") == nil {
		b = pretty.Bytes()
	}
	fmt.Fprintln(out, string(b))
	return 0
}
func emit(out io.Writer, j bool, v any, h string) int {
	if !j {
		fmt.Fprint(out, h)
		return 0
	}
	b, _ := json.Marshal(v)
	fmt.Fprintln(out, string(b))
	return 0
}
func fail(w io.Writer, j bool, e error) int {
	var a *apiError
	if !errors.As(e, &a) {
		a = &apiError{Code: "CLIENT_ERROR", Detail: e.Error()}
	}
	if j {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": a.Code, "detail": a.Detail, "requestId": a.RequestID, "status": a.Status}})
		fmt.Fprintln(w, string(b))
	} else {
		fmt.Fprintf(w, "Error: %s\n", a.Error())
	}
	if a.Status == 401 || a.Status == 403 {
		return 4
	}
	if a.Status >= 500 {
		return 5
	}
	return 3
}
func usageError(w io.Writer, j bool, s string) int {
	return fail(w, j, &apiError{Code: "USAGE_ERROR", Detail: s})
}
func parseFlags(f *flag.FlagSet, args []string) error {
	// The flag package normally writes a plaintext diagnostic before returning.
	// Suppress that side effect so every usage failure follows our JSON contract.
	f.SetOutput(io.Discard)
	return f.Parse(args)
}
func requestedHelp(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, true
	}
	if args[0] == "help" {
		return commandHelpTopic(args[1:]), true
	}
	if args[0] == "--help" || args[0] == "-h" {
		return nil, true
	}
	if args[len(args)-1] == "--help" || args[len(args)-1] == "-h" {
		return commandHelpTopic(args[:len(args)-1]), true
	}
	return nil, false
}

func commandHelpTopic(args []string) []string {
	if len(args) >= 2 && args[0] == "events" {
		if _, ok := helpText[strings.Join(args[:2], " ")]; ok {
			return args[:2]
		}
	}
	if len(args) >= 1 {
		if _, ok := helpText[args[0]]; ok {
			return args[:1]
		}
	}
	return args
}

func writeHelp(w io.Writer, topic []string) bool {
	key := strings.Join(topic, " ")
	text, ok := helpText[key]
	if !ok {
		return false
	}
	fmt.Fprint(w, text)
	return true
}

var helpText = map[string]string{
	"": `NSS provides deterministic, organization-scoped access to operational events.

Usage: nss [--url URL] [--json] <command> [options]
       nss --version [--json]

Recommended investigation flow:
  1. whoami verifies the organization, credential type, and scopes.
  2. catalog lists available sources, event types, severities, and a snapshot.
  3. semantics explains unfamiliar terms using representative events.
  4. count, search, aggregate, and series answer deterministic questions.

Commands:
  whoami             Show the authenticated organization and token metadata.
  catalog            List the organization's event vocabulary.
  semantics          Discover the meaning and usage of event terms.
  events search      Page through matching events and receive a continuation cursor.
  events get         Retrieve one event by ID.
  events count       Count matching events exactly.
  events aggregate   Group matching events by source, eventType, or severity.
  events series      Bucket matching events by HOUR, DAY, or WEEK.

EQL v1 queries use field:value predicates with AND, OR, NOT, and parentheses:
  severity:ERROR
  type:"entry-completed" AND source:checkout
  (severity:ERROR OR severity:WARN) AND NOT source:test
Fields are source, type, severity, message, tag, correlation, occurred, and received.
Use --from/--to with RFC 3339 timestamps to bound occurredAt. Reuse a returned
--snapshot for deterministic follow-up operations. Use --json for script/agent output.

Environment:
  NSS_TOKEN   Organization automation token (required for operations; never displayed).
  NSS_URL     API base URL (default http://localhost:8080; overridden by --url).

Example investigation:
  nss whoami --json
  nss catalog --json
  nss semantics --terms ttsCast,processed --json
  nss events count --query 'type:"entry-completed"' --json
  nss events search --query 'severity:ERROR' --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --json
  nss events aggregate --query 'severity:ERROR' --group-by source --json
  nss events series --query 'type:"entry-completed"' --bucket HOUR --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --json

Run nss <command> --help for exact flags and more examples.
`,
	"events": `Usage: nss events <search|get|count|aggregate|series> [options]

Deterministic event operations:
  search      Page through events; supports --limit, --cursor, and --fields.
  get         Retrieve a single event by ID.
  count       Count matching events.
  aggregate   Group matches; requires --group-by source|eventType|severity.
  series      Time-bucket matches; requires --bucket HOUR|DAY|WEEK and both time bounds.

Search, count, aggregate, and series require --query EQL and accept RFC 3339
--from/--to bounds. Count, aggregate, and series also accept --snapshot from a
prior response. Add --json for stable machine-readable output.

Examples:
  nss events count --query 'type:"entry-completed"' --json
  nss events search --query 'severity:ERROR' --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --json
`,
	"events search": `Usage: nss events search --query EQL [--from RFC3339] [--to RFC3339] [--limit N] [--cursor CURSOR] [--fields CSV] [--json]

Page through events matching an EQL v1 expression. --query is required. --limit
sets the page size (default 50); pass response continuationCursor to --cursor for
the next page. The opaque cursor preserves the stable search snapshot. --fields
accepts resource,attributes,raw_payload. RFC 3339 bounds filter occurredAt.

Example:
  nss events search --query 'severity:ERROR' --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --limit 50 --json
`,
	"events get": `Usage: nss events get EVENT_ID [--json]

Retrieve one organization-scoped event by ID. EVENT_ID is required.

Example:
  nss events get 123e4567-e89b-12d3-a456-426614174000 --json
`,
	"events count": `Usage: nss events count --query EQL [--from RFC3339] [--to RFC3339] [--snapshot SNAPSHOT] [--json]

Count events matching a required EQL v1 query. RFC 3339 bounds filter occurredAt;
reuse a returned snapshot for deterministic follow-up operations.

Example:
  nss events count --query 'type:"entry-completed"' --json
`,
	"events aggregate": `Usage: nss events aggregate --query EQL --group-by VALUE [--from RFC3339] [--to RFC3339] [--snapshot SNAPSHOT] [--json]

Group matching events. --query and --group-by are required. Accepted group values
are source, eventType, and severity. Bounds are RFC 3339 timestamps.

Example:
  nss events aggregate --query 'severity:ERROR' --group-by source --json
`,
	"events series": `Usage: nss events series --query EQL --bucket VALUE --from RFC3339 --to RFC3339 [--snapshot SNAPSHOT] [--json]

Produce a time series for matching events. --query, --bucket, and an absolute
--from/--to range are required. Accepted buckets are HOUR, DAY, and WEEK. The
RFC 3339 bounds filter occurredAt.

Example:
  nss events series --query 'type:"entry-completed"' --bucket HOUR --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --json
`,
	"catalog": `Usage: nss catalog [--snapshot SNAPSHOT] [--json]

List the organization's sources, event types, and severities. Optionally reuse a
database snapshot returned by a prior operation for a deterministic view.

Example:
  nss catalog --json
`,
	"semantics": `Usage: nss semantics --terms TERM[,TERM...] [--from RFC3339] [--to RFC3339] [--limit N] [--json]

Explain unfamiliar event terms from organization data. --terms is required and is
a comma-separated list; --limit controls the sample limit (default 20). Optional
RFC 3339 bounds constrain discovery.

Example:
  nss semantics --terms ttsCast,processed --json
`,
	"whoami": `Usage: nss whoami [--json]

Show the authenticated organization, credential type, token ID, and scopes. The
NSS_TOKEN credential value itself is never displayed.

Example:
  nss whoami --json
`,
}

func removeBool(a *[]string, key string) bool {
	r := (*a)[:0]
	found := false
	for _, v := range *a {
		if v == key {
			found = true
		} else {
			r = append(r, v)
		}
	}
	*a = r
	return found
}
func removeValue(a *[]string, key string) (string, bool, error) {
	r := (*a)[:0]
	value, found := "", false
	for i := 0; i < len(*a); i++ {
		if (*a)[i] != key {
			r = append(r, (*a)[i])
			continue
		}
		if found || i+1 == len(*a) {
			return "", false, fmt.Errorf("%s requires exactly one value", key)
		}
		found, value = true, (*a)[i+1]
		i++
	}
	*a = r
	return value, found, nil
}
func splitCSV(s string) []string {
	var r []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			r = append(r, v)
		}
	}
	return r
}
func upperCSV(s string) []string {
	r := splitCSV(s)
	for i := range r {
		r[i] = strings.ToUpper(r[i])
	}
	return r
}
func putTimes(m map[string]any, c common) {
	if c.from != "" {
		m["from"] = c.from
	}
	if c.to != "" {
		m["to"] = c.to
	}
}
func operationBody(q any, s string) map[string]any {
	m := map[string]any{"query": q}
	if s != "" {
		m["snapshot"] = s
	}
	return m
}
