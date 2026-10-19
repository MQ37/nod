package decider

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Answer types in decider_config.json. A /decide "bool" is noul and a "scale" is score.
const (
	TypeChoice = "choice"
	TypeNoul   = "noul"
	TypeScore  = "score"
)

// Temps is the state-first temperature of one model.
// ByOptions, when set, replaces the scalar and the by-type map: T(n) = max(min, a + b ln n).
type Temps struct {
	T         float64
	ByType    map[string]float64
	ByOptions *optSpec
}

type optSpec struct {
	A, B, Min float64
}

func (o optSpec) of(n int) float64 {
	if n < 2 {
		n = 2
	}
	v := o.A + o.B*math.Log(float64(n))
	if v < o.Min {
		return o.Min
	}
	return v
}

// ForSlots is one temperature per answer slot.
func (t Temps) ForSlots(types []string, nopts []int) []float64 {
	if t.ByOptions != nil {
		out := make([]float64, len(nopts))
		for i, n := range nopts {
			out[i] = t.ByOptions.of(n)
		}
		return out
	}
	out := make([]float64, len(types))
	for i, ty := range types {
		if v, ok := t.ByType[ty]; ok {
			out[i] = v
		} else {
			out[i] = t.T
		}
	}
	return out
}

type rawConfig struct {
	Temperature     json.RawMessage            `json:"temperature"`
	ByType          map[string]json.RawMessage `json:"temperature_by_type"`
	ByOptions       map[string]json.RawMessage `json:"temperature_by_options"`
	SchemaFirst     json.RawMessage            `json:"temperature_schema_first"`
	SchemaByType    map[string]json.RawMessage `json:"temperature_schema_first_by_type"`
	Layout          *string                    `json:"layout"`
	ChatTemplate    *bool                      `json:"chat_template"`
	NeutralizeNone  *bool                      `json:"neutralize_none"`
	Version         string                     `json:"version"`
	AbstainBelow    *float64                   `json:"abstain_below"`
	hasSchemaFirst  bool
	hasByType       bool
	hasByOptions    bool
	hasSchemaByType bool
}

// ParseConfig reads decider_config.json. A missing neutralize_none is true, matching decider-ai 1.8.0.
func ParseConfig(b []byte) (Temps, fileConfig, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json: %w", err)
	}
	cfg := rawConfig{}
	if v, ok := raw["temperature"]; ok {
		cfg.Temperature = v
	}
	if v, ok := raw["temperature_by_type"]; ok {
		cfg.hasByType = true
		if string(v) != "null" {
			if err := json.Unmarshal(v, &cfg.ByType); err != nil {
				return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json \"temperature_by_type\": %w", err)
			}
		}
	}
	if v, ok := raw["temperature_by_options"]; ok {
		cfg.hasByOptions = true
		if string(v) != "null" {
			if err := json.Unmarshal(v, &cfg.ByOptions); err != nil {
				return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json \"temperature_by_options\": %w", err)
			}
		}
	}
	if v, ok := raw["temperature_schema_first"]; ok {
		cfg.hasSchemaFirst = true
		cfg.SchemaFirst = v
	}
	if v, ok := raw["temperature_schema_first_by_type"]; ok {
		cfg.hasSchemaByType = true
		if string(v) != "null" {
			if err := json.Unmarshal(v, &cfg.SchemaByType); err != nil {
				return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json \"temperature_schema_first_by_type\": %w", err)
			}
		}
	}
	if v, ok := raw["layout"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json \"layout\": %w", err)
		}
		cfg.Layout = &s
	}
	if v, ok := raw["chat_template"]; ok {
		var on bool
		if err := json.Unmarshal(v, &on); err != nil {
			return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json \"chat_template\": %w", err)
		}
		cfg.ChatTemplate = &on
	}
	if v, ok := raw["neutralize_none"]; ok {
		var on bool
		if err := json.Unmarshal(v, &on); err != nil {
			return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json \"neutralize_none\": %w", err)
		}
		cfg.NeutralizeNone = &on
	}
	if v, ok := raw["version"]; ok {
		if err := json.Unmarshal(v, &cfg.Version); err != nil {
			return Temps{}, fileConfig{}, fmt.Errorf("decider_config.json \"version\": %w", err)
		}
	}
	temps, err := tempsFrom(cfg)
	if err != nil {
		return Temps{}, fileConfig{}, err
	}
	fc, err := fileFrom(cfg)
	if err != nil {
		return Temps{}, fileConfig{}, err
	}
	return temps, fc, nil
}

// fileConfig is the non-temperature part of decider_config.json.
type fileConfig struct {
	Layout         string
	NeutralizeNone bool
	Version        string
	AbstainBelow   float64
}

func fileFrom(cfg rawConfig) (fileConfig, error) {
	layout := "plain"
	if cfg.Layout != nil {
		layout = *cfg.Layout
	} else if cfg.ChatTemplate != nil && *cfg.ChatTemplate {
		layout = "chat"
	}
	if layout != "plain" && layout != "chat" {
		return fileConfig{}, fmt.Errorf("decider_config.json names the prompt layout %q; this build knows \"plain\" and \"chat\"", layout)
	}
	if layout == "plain" && cfg.ChatTemplate != nil && *cfg.ChatTemplate {
		return fileConfig{}, fmt.Errorf("decider_config.json says layout plain and chat_template true")
	}
	neutral := true
	if cfg.NeutralizeNone != nil {
		neutral = *cfg.NeutralizeNone
	}
	abstain := 0.0
	if cfg.AbstainBelow != nil {
		abstain = *cfg.AbstainBelow
	}
	return fileConfig{Layout: layout, NeutralizeNone: neutral, Version: cfg.Version, AbstainBelow: abstain}, nil
}

func tempsFrom(cfg rawConfig) (Temps, error) {
	where := "decider_config.json"
	T := 1.0
	if len(cfg.Temperature) != 0 && string(cfg.Temperature) != "null" {
		v, err := positiveRaw(cfg.Temperature, where+` "temperature"`, true)
		if err != nil {
			return Temps{}, err
		}
		T = v
	} else if len(cfg.Temperature) == 0 {
		T = 1
	}
	m, err := byType(cfg.ByType, cfg.hasByType, where+` "temperature_by_type"`)
	if err != nil {
		return Temps{}, err
	}
	if _, err := byType(cfg.SchemaByType, cfg.hasSchemaByType, where+` "temperature_schema_first_by_type"`); err != nil {
		return Temps{}, err
	}
	opt, err := byOptions(cfg.ByOptions, cfg.hasByOptions, where+` "temperature_by_options"`)
	if err != nil {
		return Temps{}, err
	}
	if opt != nil {
		if len(m) > 0 {
			return Temps{}, fmt.Errorf(`%s: "temperature_by_options" and "temperature_by_type" cannot both be set`, where)
		}
		return Temps{T: T, ByOptions: opt}, nil
	}
	if cfg.hasSchemaFirst && string(cfg.SchemaFirst) != "null" && len(cfg.SchemaFirst) != 0 {
		if _, err := positiveRaw(cfg.SchemaFirst, where+` "temperature_schema_first"`, true); err != nil {
			return Temps{}, err
		}
	}
	return Temps{T: T, ByType: m}, nil
}

func byType(m map[string]json.RawMessage, present bool, where string) (map[string]float64, error) {
	if !present || m == nil {
		return nil, nil
	}
	out := map[string]float64{}
	for k, raw := range m {
		if k != TypeChoice && k != TypeNoul && k != TypeScore {
			return nil, fmt.Errorf("%s has the unknown key %q; the keys are \"choice\", \"noul\" and \"score\"", where, k)
		}
		if isJSONBool(raw) {
			return nil, fmt.Errorf("%s[%q] must be a finite number > 0", where, k)
		}
		v, err := positiveRaw(raw, fmt.Sprintf("%s[%q]", where, k), false)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func byOptions(m map[string]json.RawMessage, present bool, where string) (*optSpec, error) {
	if !present || m == nil {
		return nil, nil
	}
	for k := range m {
		if k != "a" && k != "b" && k != "min" {
			return nil, fmt.Errorf(`%s must be {"a": number, "b": number, "min": number > 0 (optional)}`, where)
		}
	}
	if _, ok := m["a"]; !ok {
		return nil, fmt.Errorf(`%s must be {"a": number, "b": number, "min": number > 0 (optional)}`, where)
	}
	if _, ok := m["b"]; !ok {
		return nil, fmt.Errorf(`%s must be {"a": number, "b": number, "min": number > 0 (optional)}`, where)
	}
	a, err := finiteRaw(m["a"], where+"[\"a\"]")
	if err != nil {
		return nil, err
	}
	b, err := finiteRaw(m["b"], where+"[\"b\"]")
	if err != nil {
		return nil, err
	}
	min := 0.05
	if raw, ok := m["min"]; ok {
		min, err = positiveRaw(raw, where+"['min']", false)
		if err != nil {
			return nil, err
		}
	}
	return &optSpec{A: a, B: b, Min: min}, nil
}

func isJSONBool(raw json.RawMessage) bool {
	s := string(raw)
	return s == "true" || s == "false"
}

func finiteRaw(raw json.RawMessage, where string) (float64, error) {
	if isJSONBool(raw) {
		return 0, fmt.Errorf("%s must be a finite number", where)
	}
	v, err := asFloat(raw, true)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s must be a finite number", where)
	}
	return v, nil
}

func positiveRaw(raw json.RawMessage, where string, allowString bool) (float64, error) {
	if isJSONBool(raw) {
		return 0, fmt.Errorf("%s must be a finite number > 0", where)
	}
	v, err := asFloat(raw, allowString)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, fmt.Errorf("%s must be a finite number > 0", where)
	}
	return v, nil
}

func asFloat(raw json.RawMessage, allowString bool) (float64, error) {
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	if !allowString {
		return 0, fmt.Errorf("not a number")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}
