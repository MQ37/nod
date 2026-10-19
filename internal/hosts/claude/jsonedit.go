package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// A tiny surgical JSON editor: it inserts and removes members/elements as text so everything else in
// settings.json stays byte-for-byte (key order, indentation, spacing). The input is validated with
// encoding/json first, so the scanner below can assume well-formed JSON.

type jv interface{} // jstr | jnum | jobj | jarr
type jstr string    // string value
type jnum int       // number value
type jpair struct {
	k string
	v jv
}
type jobj []jpair
type jarr []jv

type style struct {
	multi bool
	unit  string // one indent level
}

func quote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

// render writes v; ind is the indentation of the line the value starts on.
func (st style) render(v jv, ind string) string {
	switch x := v.(type) {
	case jstr:
		return quote(string(x))
	case jnum:
		return fmt.Sprint(int(x))
	case jobj:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = quote(p.k) + ":" + st.sp() + st.render(p.v, ind+st.unit)
		}
		return st.wrap("{", "}", parts, ind)
	case jarr:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = st.render(e, ind+st.unit)
		}
		return st.wrap("[", "]", parts, ind)
	}
	return "null"
}

func (st style) sp() string { return " " }

func (st style) wrap(open, close string, parts []string, ind string) string {
	if len(parts) == 0 {
		return open + close
	}
	if !st.multi {
		return open + strings.Join(parts, ", ") + close
	}
	in := ind + st.unit
	return open + "\n" + in + strings.Join(parts, ",\n"+in) + "\n" + ind + close
}

// ---- scanning ----

func skipWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// valueEnd returns the index just after the value starting at i.
func valueEnd(b []byte, i int) int {
	switch b[i] {
	case '"':
		i++
		for b[i] != '"' {
			if b[i] == '\\' {
				i++
			}
			i++
		}
		return i + 1
	case '{', '[':
		open := b[i]
		cl := byte('}')
		if open == '[' {
			cl = ']'
		}
		i = skipWS(b, i+1)
		if b[i] == cl {
			return i + 1
		}
		for {
			if open == '{' {
				i = valueEnd(b, i) // key
				i = skipWS(b, i)
				i = skipWS(b, i+1) // colon
			}
			i = skipWS(b, valueEnd(b, i))
			if b[i] == cl {
				return i + 1
			}
			i = skipWS(b, i+1) // comma
		}
	}
	for i < len(b) && !strings.ContainsRune(" \t\r\n,]}", rune(b[i])) {
		i++
	}
	return i
}

type child struct {
	key        string // members only
	start, end int    // whole member (key..value) or element span
	vstart     int    // value start
}

// children lists the members (object) or elements (array) of the container opening at pos, and the index of its closing bracket.
func children(b []byte, pos int) ([]child, int) {
	obj := b[pos] == '{'
	var cs []child
	i := skipWS(b, pos+1)
	for b[i] != '}' && b[i] != ']' {
		c := child{start: i}
		if obj {
			ke := valueEnd(b, i)
			_ = json.Unmarshal(b[i:ke], &c.key)
			i = skipWS(b, ke)
			i = skipWS(b, i+1)
		}
		c.vstart = i
		c.end = valueEnd(b, i)
		cs = append(cs, c)
		i = skipWS(b, c.end)
		if b[i] == ',' {
			i = skipWS(b, i+1)
		}
	}
	return cs, i
}

func find(b []byte, pos int, key string) (child, bool) {
	cs, _ := children(b, pos)
	for i := len(cs) - 1; i >= 0; i-- { // last duplicate wins, like the JSON parsers
		if cs[i].key == key {
			return cs[i], true
		}
	}
	return child{}, false
}

func lineIndent(b []byte, pos int) string {
	s := bytes.LastIndexByte(b[:pos], '\n') + 1
	e := s
	for e < len(b) && (b[e] == ' ' || b[e] == '\t') {
		e++
	}
	return string(b[s:e])
}

// onOwnLine: only whitespace between the previous newline and pos.
func onOwnLine(b []byte, pos int) bool {
	s := bytes.LastIndexByte(b[:pos], '\n')
	return s >= 0 && len(bytes.TrimSpace(b[s+1:pos])) == 0
}

func detectStyle(b []byte, root int) style {
	cs, _ := children(b, root)
	if len(cs) == 0 {
		return style{multi: true, unit: "  "}
	}
	if onOwnLine(b, cs[0].start) {
		return style{multi: true, unit: lineIndent(b, cs[0].start)}
	}
	return style{}
}

// ---- insertion ----

func insertChild(b []byte, pos int, st style, text func(ind string) string) []byte {
	cs, closeIdx := children(b, pos)
	var ins string
	var at int
	if len(cs) == 0 {
		ind := lineIndent(b, pos)
		if st.multi {
			return concat(b[:pos+1], []byte("\n"+ind+st.unit+text(ind+st.unit)+"\n"+ind), b[closeIdx:])
		}
		return concat(b[:pos+1], []byte(text(ind)), b[closeIdx:])
	}
	last := cs[len(cs)-1]
	if st.multi && onOwnLine(b, cs[0].start) {
		ind := lineIndent(b, cs[0].start)
		ins = ",\n" + ind + text(ind)
	} else {
		ins = ", " + text(lineIndent(b, pos))
	}
	at = last.end
	return concat(b[:at], []byte(ins), b[at:])
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func addMember(b []byte, pos int, st style, key string, v jv) []byte {
	return insertChild(b, pos, st, func(ind string) string { return quote(key) + ": " + st.render(v, ind) })
}

func appendElem(b []byte, pos int, st style, v jv) []byte {
	return insertChild(b, pos, st, func(ind string) string { return st.render(v, ind) })
}

// ---- removal ----

func removeChild(b []byte, pos, i int) []byte {
	cs, closeIdx := children(b, pos)
	switch {
	case len(cs) == 1:
		return concat(b[:pos+1], b[closeIdx:])
	case i > 0:
		return concat(b[:cs[i-1].end], b[cs[i].end:])
	default:
		return concat(b[:cs[0].start], b[cs[1].start:])
	}
}

// ---- nod entries ----

func isOurs(cmd string) bool {
	for _, suf := range []string{" hook claude", " hook claude --observe", " hook claude --warm"} {
		if strings.HasSuffix(cmd, suf) {
			bin := strings.Trim(strings.TrimSuffix(cmd, suf), `"'`)
			base := bin[strings.LastIndexAny(bin, `/\`)+1:]
			return strings.HasPrefix(base, "nod")
		}
	}
	return false
}

func isObserve(cmd string) bool { return strings.HasSuffix(cmd, " --observe") }
func isWarm(cmd string) bool    { return strings.HasSuffix(cmd, " --warm") }

// entryCommands parses one element of an event array: its hook commands (nil when not a hook group).
func entryCommands(raw []byte) []string {
	var e struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return nil
	}
	var out []string
	for _, h := range e.Hooks {
		out = append(out, h.Command)
	}
	return out
}

type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

func prepare(b []byte) ([]byte, bool, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return []byte("{}"), true, nil
	}
	if !json.Valid(b) {
		return nil, false, &userError{"is not valid JSON (comments, trailing commas or a BOM are not accepted); fix it by hand, nothing was changed"}
	}
	i := skipWS(b, 0)
	if b[i] != '{' {
		return nil, false, &userError{"top level is not a JSON object; nothing was changed"}
	}
	return b, false, nil
}

func rootPos(b []byte) int { return skipWS(b, 0) }

// ensure adds entry to hooks.<event> unless an element with a command satisfying present already exists.
func ensure(b []byte, event string, entry jv, present func(cmd string) bool) ([]byte, error) {
	b, fresh, err := prepare(b)
	if err != nil {
		return nil, err
	}
	root := rootPos(b)
	st := detectStyle(b, root)
	out, err := ensureIn(b, root, st, event, entry, present)
	if err != nil {
		return nil, err
	}
	if fresh {
		out = append(out, '\n')
	}
	return out, nil
}

func ensureIn(b []byte, root int, st style, event string, entry jv, present func(string) bool) ([]byte, error) {
	h, ok := find(b, root, "hooks")
	if !ok {
		return addMember(b, root, st, "hooks", jobj{{event, jarr{entry}}}), nil
	}
	if b[h.vstart] != '{' {
		return nil, &userError{`"hooks" is not an object; nothing was changed`}
	}
	ev, ok := find(b, h.vstart, event)
	if !ok {
		return addMember(b, h.vstart, st, event, jarr{entry}), nil
	}
	if b[ev.vstart] != '[' {
		return nil, &userError{fmt.Sprintf(`"hooks.%s" is not an array; nothing was changed`, event)}
	}
	els, _ := children(b, ev.vstart)
	for _, e := range els {
		for _, c := range entryCommands(b[e.start:e.end]) {
			if present(c) {
				return b, nil
			}
		}
	}
	return appendElem(b, ev.vstart, st, entry), nil
}

// removeOurs removes every nod hook element (and containers that become empty). Returns the number
// removed and the number left alone because the same element also holds someone else's hook.
func removeOurs(b []byte) ([]byte, int, int, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return b, 0, 0, nil
	}
	b, _, err := prepare(b)
	if err != nil {
		return nil, 0, 0, err
	}
	removed, skipped := 0, 0
	for {
		root := rootPos(b)
		h, ok := find(b, root, "hooks")
		if !ok || b[h.vstart] != '{' {
			return b, removed, skipped, nil
		}
		evs, _ := children(b, h.vstart)
		done := true
		skipped = 0
	scan:
		for ei, ev := range evs {
			if b[ev.vstart] != '[' {
				continue
			}
			els, _ := children(b, ev.vstart)
			for i, e := range els {
				cmds := entryCommands(b[e.start:e.end])
				ours := 0
				for _, c := range cmds {
					if isOurs(c) {
						ours++
					}
				}
				if ours == 0 {
					continue
				}
				if ours != len(cmds) {
					skipped++
					continue
				}
				b = removeChild(b, ev.vstart, i)
				removed++
				if els2, _ := children(b, ev.vstart); len(els2) == 0 {
					b = removeChild(b, h.vstart, ei)
					if hs, _ := children(b, h.vstart); len(hs) == 0 {
						b = removeChild(b, root, indexOf(b, root, "hooks"))
					}
				}
				done = false
				break scan
			}
		}
		if done {
			return b, removed, skipped, nil
		}
	}
}

func indexOf(b []byte, pos int, key string) int {
	cs, _ := children(b, pos)
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].key == key {
			return i
		}
	}
	return -1
}

// state reports which of our entries are present.
func state(b []byte) (pre, observers bool, err error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return false, false, nil
	}
	if _, _, err = prepare(b); err != nil {
		return false, false, err
	}
	var doc struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return false, false, nil // hooks of an unexpected shape: nothing of ours is wired
	}
	for ev, els := range doc.Hooks {
		for _, e := range els {
			for _, c := range entryCommands(e) {
				if !isOurs(c) {
					continue
				}
				if ev == "PreToolUse" && !isObserve(c) {
					pre = true
				}
				if ev == "SessionStart" && isWarm(c) {
					observers = true
				}
				if isObserve(c) {
					observers = true
				}
			}
		}
	}
	return
}
