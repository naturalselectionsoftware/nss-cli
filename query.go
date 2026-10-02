package main

import (
	"fmt"
	"strings"
	"unicode"
)

type parser struct {
	s string
	p int
}

func parseQuery(c common) (map[string]any, error) {
	if strings.TrimSpace(c.query) == "" {
		return nil, fmt.Errorf("--query is required")
	}
	q := map[string]any{"version": 1, "where": map[string]any{"kind": "all", "expressions": []any{}}}
	putTimes(q, c)
	p := &parser{s: c.query}
	e, err := p.expr()
	if err == nil {
		p.space()
		if p.p != len(p.s) {
			err = p.bad("unexpected input")
		}
	}
	if err != nil {
		return nil, err
	}
	q["where"] = e
	return q, nil
}
func (p *parser) expr() (any, error) {
	left, e := p.and()
	if e != nil {
		return nil, e
	}
	xs := []any{left}
	for p.word("OR") {
		v, e := p.and()
		if e != nil {
			return nil, e
		}
		xs = append(xs, v)
	}
	if len(xs) == 1 {
		return left, nil
	}
	return map[string]any{"kind": "any", "expressions": xs}, nil
}
func (p *parser) and() (any, error) {
	left, e := p.unary()
	if e != nil {
		return nil, e
	}
	xs := []any{left}
	for p.word("AND") {
		v, e := p.unary()
		if e != nil {
			return nil, e
		}
		xs = append(xs, v)
	}
	if len(xs) == 1 {
		return left, nil
	}
	return map[string]any{"kind": "all", "expressions": xs}, nil
}
func (p *parser) unary() (any, error) {
	if p.word("NOT") {
		v, e := p.unary()
		return map[string]any{"kind": "not", "expression": v}, e
	}
	p.space()
	if p.take('(') {
		v, e := p.expr()
		if e != nil {
			return nil, e
		}
		p.space()
		if !p.take(')') {
			return nil, p.bad("expected )")
		}
		return v, nil
	}
	return p.predicate()
}
func (p *parser) predicate() (any, error) {
	p.space()
	start := p.p
	for p.p < len(p.s) && (unicode.IsLetter(rune(p.s[p.p]))) {
		p.p++
	}
	field := strings.ToLower(p.s[start:p.p])
	fields := map[string]string{"source": "source", "severity": "severity", "type": "eventType", "tag": "tags", "correlation": "correlationId", "occurred": "timestamp", "received": "receivedAt", "message": "message"}
	f, ok := fields[field]
	if !ok {
		return nil, p.bad("unsupported query field")
	}
	p.space()
	op := ""
	for _, x := range []string{">=", "<=", ">", "<", ":", "="} {
		if strings.HasPrefix(p.s[p.p:], x) {
			op = x
			p.p += len(x)
			break
		}
	}
	if op == "" {
		return nil, p.bad("expected operator")
	}
	if op == ":" || op == "=" {
		op = "EQ"
	} else {
		op = map[string]string{">": "GT", ">=": "GTE", "<": "LT", "<=": "LTE"}[op]
	}
	p.space()
	v, e := p.value()
	if e != nil {
		return nil, e
	}
	typ := "STRING"
	if field == "occurred" || field == "received" {
		typ = "TIMESTAMP"
	}
	if field == "severity" {
		v = strings.ToUpper(v)
	}
	if (field == "source" || field == "type") && op == "EQ" {
		op = "CASE_INSENSITIVE_EQ"
	}
	return map[string]any{"kind": "predicate", "field": f, "operator": op, "type": typ, "value": v}, nil
}
func (p *parser) value() (string, error) {
	if p.p >= len(p.s) {
		return "", p.bad("expected value")
	}
	if p.take('"') {
		var b strings.Builder
		for p.p < len(p.s) {
			c := p.s[p.p]
			p.p++
			if c == '"' {
				if b.Len() == 0 {
					return "", p.bad("empty value")
				}
				return b.String(), nil
			}
			if c == '\\' {
				if p.p >= len(p.s) || (p.s[p.p] != '\\' && p.s[p.p] != '"') {
					return "", p.bad("invalid escape")
				}
				c = p.s[p.p]
				p.p++
			}
			b.WriteByte(c)
		}
		return "", p.bad("unterminated quote")
	}
	start := p.p
	for p.p < len(p.s) && !unicode.IsSpace(rune(p.s[p.p])) && p.s[p.p] != ')' && p.s[p.p] != '(' {
		p.p++
	}
	if start == p.p {
		return "", p.bad("expected value")
	}
	return p.s[start:p.p], nil
}
func (p *parser) space() {
	for p.p < len(p.s) && unicode.IsSpace(rune(p.s[p.p])) {
		p.p++
	}
}
func (p *parser) word(w string) bool {
	save := p.p
	p.space()
	if len(p.s)-p.p >= len(w) && strings.EqualFold(p.s[p.p:p.p+len(w)], w) && (p.p+len(w) == len(p.s) || !unicode.IsLetter(rune(p.s[p.p+len(w)]))) {
		p.p += len(w)
		return true
	}
	p.p = save
	return false
}
func (p *parser) take(c byte) bool {
	if p.p < len(p.s) && p.s[p.p] == c {
		p.p++
		return true
	}
	return false
}
func (p *parser) bad(s string) error {
	return fmt.Errorf("invalid --query at character %d: %s", p.p+1, s)
}
