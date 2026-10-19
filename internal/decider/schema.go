package decider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Schema is one /decide schema. Order is the JSON object order; Go maps do not keep it.
type Schema struct {
	Order  []string
	Fields map[string]Field
}

// Field is one schema entry.
type Field struct {
	Type    string
	Options []string
	Legend  legend
}

type legend struct {
	raw   json.RawMessage
	list  []string
	dict  bool
	keys  []string // original key text, sorted by numeric value when dict
	descs []string
}

// UnmarshalJSON accepts the Python /decide schema object.
func (s *Schema) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("schema must be an object")
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return fmt.Errorf("schema must be an object")
	}
	s.Order = nil
	s.Fields = map[string]Field{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := kt.(string)
		if !ok {
			return fmt.Errorf("schema key is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		f, err := parseField(key, raw)
		if err != nil {
			return err
		}
		if _, exists := s.Fields[key]; exists {
			return fmt.Errorf("schema[%q] is repeated", key)
		}
		s.Order = append(s.Order, key)
		s.Fields[key] = f
	}
	tok, err = dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '}' {
		return fmt.Errorf("schema must be an object")
	}
	return nil
}

func parseField(key string, raw json.RawMessage) (Field, error) {
	where := fmt.Sprintf("schema[%q]", key)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return Field{}, fmt.Errorf("%s is not a field object", where)
	}
	t := "choice"
	if v, ok := obj["type"]; ok {
		if err := json.Unmarshal(v, &t); err != nil {
			return Field{}, fmt.Errorf("%s: type is not a string", where)
		}
	}
	switch t {
	case "choice", "":
		opts, err := stringList(obj["options"])
		if err != nil || len(opts) == 0 {
			return Field{}, fmt.Errorf("%s: \"options\" must be a non-empty list of strings", where)
		}
		return Field{Type: "choice", Options: opts}, nil
	case "bool":
		return Field{Type: "bool", Options: []string{"no", "yes"}}, nil
	case "scale":
		leg, err := parseLegend(obj["legend"])
		if err != nil {
			return Field{}, fmt.Errorf("%s: %w", where, err)
		}
		labels := make([]string, len(leg.keys))
		for i := range leg.keys {
			labels[i] = leg.keys[i] + ": " + leg.descs[i]
		}
		return Field{Type: "scale", Options: labels, Legend: leg}, nil
	default:
		return Field{}, fmt.Errorf("%s: unknown field type %q", where, t)
	}
}

func stringList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing")
	}
	var ss []string
	if err := json.Unmarshal(raw, &ss); err == nil {
		for _, s := range ss {
			if s == "" && len(ss) > 0 {
				// empty string is still a string; allowed
			}
		}
		return ss, nil
	}
	// A JSON object is accepted the way Python iterates a dict: keys, in order.
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a list")
	}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("not a string")
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func parseLegend(raw json.RawMessage) (legend, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return legend{}, fmt.Errorf("\"legend\" must be a non-empty list or {level number: description} map")
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		if len(list) == 0 {
			return legend{}, fmt.Errorf("\"legend\" must be a non-empty list or {level number: description} map")
		}
		keys := make([]string, len(list))
		for i := range list {
			keys[i] = strconv.Itoa(i)
		}
		return legend{raw: append(json.RawMessage(nil), raw...), list: list, keys: keys, descs: list}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return legend{}, fmt.Errorf("\"legend\" must be a non-empty list or {level number: description} map")
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return legend{}, fmt.Errorf("\"legend\" must be a non-empty list or {level number: description} map")
	}
	type pair struct {
		key  string
		desc string
		num  float64
	}
	var pairs []pair
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return legend{}, err
		}
		k, ok := kt.(string)
		if !ok {
			return legend{}, fmt.Errorf("the keys of a \"legend\" map must be finite numbers")
		}
		n, err := strconv.ParseFloat(k, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return legend{}, fmt.Errorf("the keys of a \"legend\" map must be finite numbers")
		}
		var desc string
		if err := dec.Decode(&desc); err != nil {
			return legend{}, fmt.Errorf("the \"legend\" contains a value that is not finite JSON")
		}
		pairs = append(pairs, pair{key: k, desc: desc, num: n})
	}
	if len(pairs) == 0 {
		return legend{}, fmt.Errorf("\"legend\" must be a non-empty list or {level number: description} map")
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].num < pairs[j].num })
	leg := legend{raw: append(json.RawMessage(nil), raw...), dict: true}
	for _, p := range pairs {
		leg.keys = append(leg.keys, p.key)
		leg.descs = append(leg.descs, p.desc)
	}
	return leg, nil
}

// Answer is one /decide field. MarshalJSON writes Python's field order.
type Answer struct {
	Type          string
	Choice        string
	Confidence    float64
	Noul          float64
	Score         float64
	Probabilities []prob
	Legend        json.RawMessage
	// Abstain is set when the winning probability is below abstain_below. Choice is then omitted as null.
	Abstain bool
}

// OneHot is a choice answer with all probability on one option. It lets a judge that returns a
// verdict instead of logits (a chat model reviewing a call) feed the same pipeline as the Decider.
func OneHot(options []string, choice string) Answer {
	a := Answer{Type: "choice", Choice: choice, Confidence: 1}
	for _, o := range options {
		p := 0.0
		if o == choice {
			p = 1
		}
		a.Probabilities = append(a.Probabilities, prob{Key: o, P: p})
	}
	return a
}

type prob struct {
	Key string
	P   float64
}

// MarshalJSON writes the answer object in decider-ai field order.
func (a Answer) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	switch a.Type {
	case "noul":
		fmt.Fprintf(&b, `"noul":%s,"type":"noul"`, num(a.Noul))
	case "choice":
		if a.Abstain {
			b.WriteString(`"choice":null`)
		} else {
			cb, err := json.Marshal(a.Choice)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, `"choice":%s`, cb)
		}
		fmt.Fprintf(&b, `,"confidence":%s,"type":"choice","probabilities":`, num(a.Confidence))
		writeProbs(&b, a.Probabilities)
	case "scale":
		fmt.Fprintf(&b, `"score":%s,"confidence":%s,"type":"scale","legend":`, num(a.Score), num(a.Confidence))
		if len(a.Legend) == 0 {
			b.WriteString("null")
		} else {
			b.Write(a.Legend)
		}
		b.WriteString(`,"probabilities":`)
		writeProbs(&b, a.Probabilities)
	default:
		return nil, fmt.Errorf("unknown answer type %q", a.Type)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func writeProbs(b *bytes.Buffer, ps []prob) {
	b.WriteByte('{')
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(p.Key)
		fmt.Fprintf(b, `%s:%s`, kb, num(p.P))
	}
	b.WriteByte('}')
}

func num(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// MarshalAnswers writes the /decide object in schema order.
func MarshalAnswers(schema Schema, answers map[string]Answer) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range schema.Order {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		ab, err := answers[k].MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(ab)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Prob returns the probability stored under key, or 0.
func (a Answer) Prob(key string) float64 {
	for _, p := range a.Probabilities {
		if p.Key == key {
			return p.P
		}
	}
	return 0
}
