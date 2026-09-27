package testgensolver

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"strconv"
	"strings"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
)

type FieldValue struct {
	Name  string `json:"name"`
	Value Value  `json:"value"`
}

// Value is a closed, typed framework-neutral input. It carries no source,
// command, path, pointer address, or environment-derived data.
type Value struct {
	Kind     analysis.TypeKind `json:"kind"`
	Boolean  bool              `json:"boolean,omitempty"`
	Integer  string            `json:"integer,omitempty"`
	Float    string            `json:"float,omitempty"`
	Enum     string            `json:"enum,omitempty"`
	String   string            `json:"string,omitempty"`
	Null     bool              `json:"null,omitempty"`
	Pointee  *Value            `json:"pointee,omitempty"`
	Elements []Value           `json:"elements,omitempty"`
	Fields   []FieldValue      `json:"fields,omitempty"`
}

var errUnsafeDomain = errors.New("unproven finite domain")
var errDomainBudget = errors.New("domain memory budget exceeded")

type domainMeter struct {
	ctx   context.Context
	limit int64
	used  int64
}

func (m *domainMeter) reserve(size int64) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if size < 0 || size > m.limit-m.used {
		return errDomainBudget
	}
	m.used += size
	return nil
}

func finiteDomain(m *domainMeter, typ analysis.Type, thresholds []string, depth int) ([]Value, error) {
	if err := m.reserve(0); err != nil {
		return nil, err
	}
	if depth > 4 {
		return nil, errUnsafeDomain
	}
	switch typ.Kind {
	case analysis.TypeBoolean:
		if err := m.reserve(2 * 96); err != nil {
			return nil, err
		}
		return []Value{{Kind: typ.Kind}, {Kind: typ.Kind, Boolean: true}}, nil
	case analysis.TypeInteger:
		if typ.BitWidth != 8 && typ.BitWidth != 16 && typ.BitWidth != 32 && typ.BitWidth != 64 {
			return nil, errUnsafeDomain
		}
		width := uint(typ.BitWidth)
		limit := new(big.Int).Lsh(big.NewInt(1), width)
		min := big.NewInt(0)
		max := new(big.Int).Sub(limit, big.NewInt(1))
		if typ.Signed {
			half := new(big.Int).Rsh(limit, 1)
			min.Neg(half)
			max.Sub(half, big.NewInt(1))
		}
		candidates := []*big.Int{new(big.Int).Set(min), new(big.Int).Add(min, big.NewInt(1)), big.NewInt(-1), big.NewInt(0), big.NewInt(1), new(big.Int).Sub(max, big.NewInt(1)), new(big.Int).Set(max)}
		for _, threshold := range thresholds {
			n, ok := new(big.Int).SetString(threshold, 10)
			if !ok {
				return nil, errUnsafeDomain
			}
			candidates = append(candidates, new(big.Int).Sub(n, big.NewInt(1)), n, new(big.Int).Add(n, big.NewInt(1)))
		}
		set := map[string]bool{}
		values := []*big.Int{}
		for _, n := range candidates {
			if n.Cmp(min) >= 0 && n.Cmp(max) <= 0 && !set[n.String()] {
				set[n.String()] = true
				values = append(values, n)
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i].Cmp(values[j]) < 0 })
		if err := m.reserve(int64(len(values)) * 128); err != nil {
			return nil, err
		}
		result := make([]Value, len(values))
		for i, n := range values {
			result[i] = Value{Kind: typ.Kind, Integer: n.String()}
		}
		return result, nil
	case analysis.TypeFloating:
		if typ.BitWidth != 32 && typ.BitWidth != 64 {
			return nil, errUnsafeDomain
		}
		if err := m.reserve(6 * 128); err != nil {
			return nil, err
		}
		values := []Value{{Kind: typ.Kind, Float: "-1"}, {Kind: typ.Kind, Float: "0"}, {Kind: typ.Kind, Float: "1"}}
		for _, raw := range thresholds {
			n, err := strconv.ParseFloat(raw, typ.BitWidth)
			if err != nil || n != n || n > 1e300 || n < -1e300 {
				return nil, errUnsafeDomain
			}
			value := strconv.FormatFloat(n, 'g', -1, typ.BitWidth)
			found := false
			for _, prior := range values {
				found = found || prior.Float == value
			}
			if !found {
				values = append(values, Value{Kind: typ.Kind, Float: value})
			}
		}
		if typ.AllowNonFinite {
			values = append(values, Value{Kind: typ.Kind, Float: "NaN"}, Value{Kind: typ.Kind, Float: "+Inf"}, Value{Kind: typ.Kind, Float: "-Inf"})
		}
		return values, nil
	case analysis.TypeEnum:
		if len(typ.EnumValues) == 0 || len(typ.EnumValues) > 32 {
			return nil, errUnsafeDomain
		}
		set := map[string]bool{}
		values := make([]string, 0, len(typ.EnumValues))
		for _, member := range typ.EnumValues {
			if !identifier(member) || set[member] {
				return nil, errUnsafeDomain
			}
			set[member] = true
			values = append(values, member)
		}
		sort.Strings(values)
		if err := m.reserve(int64(len(values)) * 128); err != nil {
			return nil, err
		}
		result := make([]Value, len(values))
		for i, member := range values {
			result[i] = Value{Kind: typ.Kind, Enum: member}
		}
		return result, nil
	case analysis.TypePointer:
		if !typ.Owned || typ.Element == nil || depth >= 4 {
			return nil, errUnsafeDomain
		}
		if err := m.reserve(2 * 128); err != nil {
			return nil, err
		}
		pointee, err := finiteDomain(m, *typ.Element, nil, depth+1)
		if err != nil || len(pointee) == 0 {
			return nil, errUnsafeDomain
		}
		v := pointee[0]
		return []Value{{Kind: typ.Kind, Null: true}, {Kind: typ.Kind, Pointee: &v}}, nil
	case analysis.TypeString:
		if typ.MaxLength < 1 || typ.MaxLength > 256 {
			return nil, errUnsafeDomain
		}
		if err := m.reserve(3*128 + int64(typ.MaxLength)); err != nil {
			return nil, err
		}
		return []Value{{Kind: typ.Kind}, {Kind: typ.Kind, String: "a"}, {Kind: typ.Kind, String: strings.Repeat("a", typ.MaxLength)}}, nil
	case analysis.TypeArray:
		if typ.Bound < 1 || typ.Bound > 16 || typ.Element == nil {
			return nil, errUnsafeDomain
		}
		if err := m.reserve(int64(typ.Bound) * 2 * 128); err != nil {
			return nil, err
		}
		element, err := finiteDomain(m, *typ.Element, nil, depth+1)
		if err != nil {
			return nil, err
		}
		if len(element) == 0 {
			return nil, errUnsafeDomain
		}
		values := make([]Value, typ.Bound)
		for i := range values {
			values[i] = element[0]
		}
		result := []Value{{Kind: typ.Kind, Elements: values}}
		if len(element) > 1 {
			other := append([]Value(nil), values...)
			other[0] = element[1]
			result = append(result, Value{Kind: typ.Kind, Elements: other})
		}
		return result, nil
	case analysis.TypeRecord:
		if !typ.Proven || len(typ.Fields) == 0 || len(typ.Fields) > 16 {
			return nil, errUnsafeDomain
		}
		if err := m.reserve(int64(len(typ.Fields)) * 160); err != nil {
			return nil, err
		}
		fields := append([]analysis.Field(nil), typ.Fields...)
		sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		values := make([]FieldValue, len(fields))
		for i, field := range fields {
			if !identifier(field.Name) || i > 0 && field.Name == fields[i-1].Name {
				return nil, errUnsafeDomain
			}
			domain, err := finiteDomain(m, field.Type, nil, depth+1)
			if err != nil {
				return nil, err
			}
			if len(domain) == 0 {
				return nil, errUnsafeDomain
			}
			values[i] = FieldValue{Name: field.Name, Value: domain[0]}
		}
		return []Value{{Kind: typ.Kind, Fields: values}}, nil
	default:
		return nil, errUnsafeDomain
	}
}
