package ctest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxUnknownJSONDepth = 64

type showOnlyDecoder struct {
	decoder *json.Decoder
	limits  Limits
}

func ParseShowOnlyJSON(data []byte, limits Limits) (Snapshot, error) {
	if !limits.valid() {
		return Snapshot{}, ErrInvalidLimits
	}
	if len(data) > limits.MaxDocumentBytes {
		return Snapshot{}, limitError("document")
	}
	if !utf8.Valid(data) {
		return Snapshot{}, invalidError("document", "must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	parser := showOnlyDecoder{decoder: decoder, limits: limits}
	snapshot, err := parser.snapshot()
	if err != nil {
		return Snapshot{}, err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return Snapshot{}, invalidError("document", fmt.Sprintf("unexpected trailing token %v", token))
		}
		return Snapshot{}, invalidError("document", err.Error())
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (parser showOnlyDecoder) snapshot() (Snapshot, error) {
	var result Snapshot
	var kindSet, versionSet, graphSet, testsSet bool
	err := parser.object("document", func(name string) error {
		switch name {
		case "kind":
			if kindSet {
				return duplicateField("document.kind")
			}
			kindSet = true
			value, err := parser.text("document.kind", false)
			if err != nil {
				return err
			}
			if value != "ctestInfo" {
				return invalidError("document.kind", "must be ctestInfo")
			}
			result.Kind = value
		case "version":
			if versionSet {
				return duplicateField("document.version")
			}
			versionSet = true
			value, err := parser.version()
			if err != nil {
				return err
			}
			result.Version = value
		case "backtraceGraph":
			if graphSet {
				return duplicateField("document.backtraceGraph")
			}
			graphSet = true
			value, err := parser.backtraceGraph()
			if err != nil {
				return err
			}
			result.BacktraceGraph = value
		case "tests":
			if testsSet {
				return duplicateField("document.tests")
			}
			testsSet = true
			value, err := parser.tests()
			if err != nil {
				return err
			}
			result.Tests = value
		default:
			return parser.skipValue(0)
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	if !kindSet || !versionSet || !graphSet || !testsSet {
		return Snapshot{}, invalidError("document", "kind, version, backtraceGraph, and tests are required")
	}
	return result, nil
}

func (parser showOnlyDecoder) version() (Version, error) {
	var result Version
	var majorSet, minorSet bool
	err := parser.object("version", func(name string) error {
		switch name {
		case "major":
			if majorSet {
				return duplicateField("version.major")
			}
			majorSet = true
			value, err := parser.integer("version.major", 1)
			if err != nil {
				return err
			}
			result.Major = value
		case "minor":
			if minorSet {
				return duplicateField("version.minor")
			}
			minorSet = true
			value, err := parser.integer("version.minor", 0)
			if err != nil {
				return err
			}
			result.Minor = value
		default:
			return parser.skipValue(0)
		}
		return nil
	})
	if err != nil {
		return Version{}, err
	}
	if !majorSet || !minorSet || result.Major != 1 {
		return Version{}, invalidError("version", "major 1 and a non-negative minor are required")
	}
	return result, nil
}

func (parser showOnlyDecoder) backtraceGraph() (BacktraceGraph, error) {
	result := BacktraceGraph{
		Commands: []string{},
		Files:    []string{},
		Nodes:    []BacktraceNode{},
	}
	var commandsSet, filesSet, nodesSet bool
	err := parser.object("backtraceGraph", func(name string) error {
		switch name {
		case "commands":
			if commandsSet {
				return duplicateField("backtraceGraph.commands")
			}
			commandsSet = true
			values, err := parser.stringArray(
				"backtraceGraph.commands",
				parser.limits.MaxBacktraceCommands,
				false,
			)
			if err != nil {
				return err
			}
			result.Commands = values
		case "files":
			if filesSet {
				return duplicateField("backtraceGraph.files")
			}
			filesSet = true
			values, err := parser.stringArray(
				"backtraceGraph.files",
				parser.limits.MaxBacktraceFiles,
				false,
			)
			if err != nil {
				return err
			}
			result.Files = values
		case "nodes":
			if nodesSet {
				return duplicateField("backtraceGraph.nodes")
			}
			nodesSet = true
			values, err := parser.backtraceNodes()
			if err != nil {
				return err
			}
			result.Nodes = values
		default:
			return parser.skipValue(0)
		}
		return nil
	})
	if err != nil {
		return BacktraceGraph{}, err
	}
	if !commandsSet || !filesSet || !nodesSet {
		return BacktraceGraph{}, invalidError(
			"backtraceGraph",
			"commands, files, and nodes are required",
		)
	}
	return result, nil
}

func (parser showOnlyDecoder) backtraceNodes() ([]BacktraceNode, error) {
	if err := parser.beginArray("backtraceGraph.nodes"); err != nil {
		return nil, err
	}
	result := []BacktraceNode{}
	for parser.decoder.More() {
		if len(result) >= parser.limits.MaxBacktraceNodes {
			return nil, limitError("backtraceGraph.nodes")
		}
		node, err := parser.backtraceNode(len(result))
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	if err := parser.endArray("backtraceGraph.nodes"); err != nil {
		return nil, err
	}
	return result, nil
}

func (parser showOnlyDecoder) backtraceNode(index int) (BacktraceNode, error) {
	var result BacktraceNode
	var fileSet, commandSet, lineSet, parentSet bool
	field := fmt.Sprintf("backtraceGraph.nodes[%d]", index)
	err := parser.object(field, func(name string) error {
		switch name {
		case "file":
			if fileSet {
				return duplicateField(field + ".file")
			}
			fileSet = true
			value, err := parser.integer(field+".file", 0)
			if err != nil {
				return err
			}
			result.File = value
		case "command":
			if commandSet {
				return duplicateField(field + ".command")
			}
			commandSet = true
			value, err := parser.integer(field+".command", 0)
			if err != nil {
				return err
			}
			result.Command = intPointer(value)
		case "line":
			if lineSet {
				return duplicateField(field + ".line")
			}
			lineSet = true
			value, err := parser.integer(field+".line", 1)
			if err != nil {
				return err
			}
			result.Line = intPointer(value)
		case "parent":
			if parentSet {
				return duplicateField(field + ".parent")
			}
			parentSet = true
			value, err := parser.integer(field+".parent", 0)
			if err != nil {
				return err
			}
			result.Parent = intPointer(value)
		default:
			return parser.skipValue(0)
		}
		return nil
	})
	if err != nil {
		return BacktraceNode{}, err
	}
	if !fileSet {
		return BacktraceNode{}, invalidError(field+".file", "is required")
	}
	return result, nil
}

func (parser showOnlyDecoder) tests() ([]RawTest, error) {
	if err := parser.beginArray("tests"); err != nil {
		return nil, err
	}
	result := []RawTest{}
	for parser.decoder.More() {
		if len(result) >= parser.limits.MaxTests {
			return nil, limitError("tests")
		}
		value, err := parser.test(len(result))
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := parser.endArray("tests"); err != nil {
		return nil, err
	}
	return result, nil
}

func (parser showOnlyDecoder) test(index int) (RawTest, error) {
	result := RawTest{Command: []string{}, Properties: []Property{}}
	var nameSet, configSet, commandSet, backtraceSet, propertiesSet bool
	field := fmt.Sprintf("tests[%d]", index)
	err := parser.object(field, func(name string) error {
		switch name {
		case "name":
			if nameSet {
				return duplicateField(field + ".name")
			}
			nameSet = true
			value, err := parser.text(field+".name", false)
			if err != nil {
				return err
			}
			result.Name = value
		case "config":
			if configSet {
				return duplicateField(field + ".config")
			}
			configSet = true
			value, err := parser.text(field+".config", false)
			if err != nil {
				return err
			}
			result.Config = value
		case "command":
			if commandSet {
				return duplicateField(field + ".command")
			}
			commandSet = true
			value, err := parser.command(field + ".command")
			if err != nil {
				return err
			}
			result.Command = value
		case "backtrace":
			if backtraceSet {
				return duplicateField(field + ".backtrace")
			}
			backtraceSet = true
			value, err := parser.integer(field+".backtrace", 0)
			if err != nil {
				return err
			}
			result.Backtrace = value
		case "properties":
			if propertiesSet {
				return duplicateField(field + ".properties")
			}
			propertiesSet = true
			value, err := parser.properties(field + ".properties")
			if err != nil {
				return err
			}
			result.Properties = value
		default:
			return parser.skipValue(0)
		}
		return nil
	})
	if err != nil {
		return RawTest{}, err
	}
	if !nameSet || !commandSet || !backtraceSet {
		return RawTest{}, invalidError(field, "name, non-empty command, and backtrace are required")
	}
	return result, nil
}

func (parser showOnlyDecoder) command(field string) ([]string, error) {
	if err := parser.beginArray(field); err != nil {
		return nil, err
	}
	result := []string{}
	for parser.decoder.More() {
		if len(result) >= parser.limits.MaxCommandArguments {
			return nil, limitError(field)
		}
		value, err := parser.text(
			fmt.Sprintf("%s[%d]", field, len(result)),
			len(result) > 0,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := parser.endArray(field); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, invalidError(field, "must contain an executable")
	}
	return result, nil
}

func (parser showOnlyDecoder) properties(field string) ([]Property, error) {
	if err := parser.beginArray(field); err != nil {
		return nil, err
	}
	result := []Property{}
	for parser.decoder.More() {
		if len(result) >= parser.limits.MaxPropertiesPerTest {
			return nil, limitError(field)
		}
		value, err := parser.property(fmt.Sprintf("%s[%d]", field, len(result)))
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := parser.endArray(field); err != nil {
		return nil, err
	}
	return result, nil
}

func (parser showOnlyDecoder) property(field string) (Property, error) {
	var result Property
	var nameSet, valueSet bool
	err := parser.object(field, func(name string) error {
		switch name {
		case "name":
			if nameSet {
				return duplicateField(field + ".name")
			}
			nameSet = true
			value, err := parser.text(field+".name", false)
			if err != nil {
				return err
			}
			result.Name = value
		case "value":
			if valueSet {
				return duplicateField(field + ".value")
			}
			valueSet = true
			value, err := parser.propertyValue(field + ".value")
			if err != nil {
				return err
			}
			result.Value = value
		default:
			return parser.skipValue(0)
		}
		return nil
	})
	if err != nil {
		return Property{}, err
	}
	if !nameSet || !valueSet {
		return Property{}, invalidError(field, "name and value are required")
	}
	return result, nil
}

func (parser showOnlyDecoder) propertyValue(field string) (PropertyValue, error) {
	token, err := parser.decoder.Token()
	if err != nil {
		return PropertyValue{}, invalidError(field, err.Error())
	}
	switch value := token.(type) {
	case string:
		if err := parser.validateText(field, value, true); err != nil {
			return PropertyValue{}, err
		}
		return PropertyValue{Kind: PropertyString, String: value}, nil
	case bool:
		return PropertyValue{Kind: PropertyBoolean, Boolean: value}, nil
	case json.Number:
		if err := validateJSONNumber(value.String()); err != nil {
			return PropertyValue{}, invalidError(field, err.Error())
		}
		return PropertyValue{Kind: PropertyNumber, Number: value.String()}, nil
	case json.Delim:
		if value != '[' {
			return PropertyValue{}, invalidError(field, "must be a scalar or an array of strings")
		}
		result := []string{}
		for parser.decoder.More() {
			if len(result) >= parser.limits.MaxPropertyStrings {
				return PropertyValue{}, limitError(field)
			}
			item, err := parser.text(fmt.Sprintf("%s[%d]", field, len(result)), true)
			if err != nil {
				return PropertyValue{}, err
			}
			result = append(result, item)
		}
		if err := parser.endArray(field); err != nil {
			return PropertyValue{}, err
		}
		return PropertyValue{Kind: PropertyStrings, Strings: result}, nil
	default:
		return PropertyValue{}, invalidError(field, "must be a string, number, boolean, or array of strings")
	}
}

func (parser showOnlyDecoder) stringArray(field string, limit int, allowEmpty bool) ([]string, error) {
	if err := parser.beginArray(field); err != nil {
		return nil, err
	}
	result := []string{}
	for parser.decoder.More() {
		if len(result) >= limit {
			return nil, limitError(field)
		}
		value, err := parser.text(fmt.Sprintf("%s[%d]", field, len(result)), allowEmpty)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := parser.endArray(field); err != nil {
		return nil, err
	}
	return result, nil
}

func (parser showOnlyDecoder) object(field string, member func(string) error) error {
	token, err := parser.decoder.Token()
	if err != nil {
		return invalidError(field, err.Error())
	}
	if token != json.Delim('{') {
		return invalidError(field, "must be an object")
	}
	for parser.decoder.More() {
		token, err := parser.decoder.Token()
		if err != nil {
			return invalidError(field, err.Error())
		}
		name, ok := token.(string)
		if !ok {
			return invalidError(field, "object member name must be a string")
		}
		if err := parser.validateText(field+".member", name, false); err != nil {
			return err
		}
		if err := member(name); err != nil {
			return err
		}
	}
	token, err = parser.decoder.Token()
	if err != nil || token != json.Delim('}') {
		if err != nil {
			return invalidError(field, err.Error())
		}
		return invalidError(field, "unterminated object")
	}
	return nil
}

func (parser showOnlyDecoder) beginArray(field string) error {
	token, err := parser.decoder.Token()
	if err != nil {
		return invalidError(field, err.Error())
	}
	if token != json.Delim('[') {
		return invalidError(field, "must be an array")
	}
	return nil
}

func (parser showOnlyDecoder) endArray(field string) error {
	token, err := parser.decoder.Token()
	if err != nil {
		return invalidError(field, err.Error())
	}
	if token != json.Delim(']') {
		return invalidError(field, "unterminated array")
	}
	return nil
}

func (parser showOnlyDecoder) text(field string, allowEmpty bool) (string, error) {
	token, err := parser.decoder.Token()
	if err != nil {
		return "", invalidError(field, err.Error())
	}
	value, ok := token.(string)
	if !ok {
		return "", invalidError(field, "must be a string")
	}
	if err := parser.validateText(field, value, allowEmpty); err != nil {
		return "", err
	}
	return value, nil
}

func (parser showOnlyDecoder) validateText(field, value string, allowEmpty bool) error {
	if !utf8.ValidString(value) {
		return invalidError(field, "must be valid UTF-8")
	}
	if !allowEmpty && value == "" {
		return invalidError(field, "must not be empty")
	}
	if strings.IndexByte(value, 0) >= 0 {
		return invalidError(field, "must not contain NUL")
	}
	if len([]byte(value)) > parser.limits.MaxStringBytes {
		return limitError(field)
	}
	return nil
}

func (parser showOnlyDecoder) integer(field string, minimum int64) (int, error) {
	token, err := parser.decoder.Token()
	if err != nil {
		return 0, invalidError(field, err.Error())
	}
	number, ok := token.(json.Number)
	if !ok {
		return 0, invalidError(field, "must be an integer")
	}
	if strings.ContainsAny(number.String(), ".eE") {
		return 0, invalidError(field, "must be an integer")
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value < minimum || value > maxSafeInteger || uint64(value) > uint64(^uint(0)>>1) {
		return 0, invalidError(field, "integer is outside the supported range")
	}
	return int(value), nil
}

func (parser showOnlyDecoder) skipValue(depth int) error {
	if depth > maxUnknownJSONDepth {
		return invalidError("unknown field", "nesting is too deep")
	}
	token, err := parser.decoder.Token()
	if err != nil {
		return invalidError("unknown field", err.Error())
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		for parser.decoder.More() {
			if _, err := parser.decoder.Token(); err != nil {
				return invalidError("unknown field", err.Error())
			}
			if err := parser.skipValue(depth + 1); err != nil {
				return err
			}
		}
		token, err = parser.decoder.Token()
		if err != nil || token != json.Delim('}') {
			return invalidError("unknown field", "unterminated object")
		}
	case '[':
		for parser.decoder.More() {
			if err := parser.skipValue(depth + 1); err != nil {
				return err
			}
		}
		token, err = parser.decoder.Token()
		if err != nil || token != json.Delim(']') {
			return invalidError("unknown field", "unterminated array")
		}
	default:
		return invalidError("unknown field", "unexpected delimiter")
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	names := make(map[string]struct{}, len(snapshot.Tests))
	for index, test := range snapshot.Tests {
		if _, exists := names[test.Name]; exists {
			return invalidError(fmt.Sprintf("tests[%d].name", index), "duplicate logical name")
		}
		names[test.Name] = struct{}{}
		if test.Backtrace < 0 || test.Backtrace >= len(snapshot.BacktraceGraph.Nodes) {
			return invalidError(fmt.Sprintf("tests[%d].backtrace", index), "index is outside backtraceGraph.nodes")
		}
	}
	graph := snapshot.BacktraceGraph
	for index, node := range graph.Nodes {
		if node.File < 0 || node.File >= len(graph.Files) {
			return invalidError(fmt.Sprintf("backtraceGraph.nodes[%d].file", index), "index is outside files")
		}
		if node.Command != nil && (*node.Command < 0 || *node.Command >= len(graph.Commands)) {
			return invalidError(fmt.Sprintf("backtraceGraph.nodes[%d].command", index), "index is outside commands")
		}
		if node.Parent != nil && (*node.Parent < 0 || *node.Parent >= len(graph.Nodes)) {
			return invalidError(fmt.Sprintf("backtraceGraph.nodes[%d].parent", index), "index is outside nodes")
		}
	}
	if err := validateBacktraceAcyclic(graph.Nodes); err != nil {
		return err
	}
	return nil
}

func validateBacktraceAcyclic(nodes []BacktraceNode) error {
	states := make([]uint8, len(nodes))
	for start := range nodes {
		current := start
		for states[current] == 0 {
			states[current] = 1
			if nodes[current].Parent == nil {
				break
			}
			current = *nodes[current].Parent
		}
		if states[current] == 1 && nodes[current].Parent != nil {
			return invalidError(
				fmt.Sprintf("backtraceGraph.nodes[%d].parent", current),
				"backtrace graph contains a cycle",
			)
		}
		current = start
		for states[current] == 1 {
			states[current] = 2
			if nodes[current].Parent == nil {
				break
			}
			current = *nodes[current].Parent
		}
	}
	return nil
}

func validateJSONNumber(value string) error {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return errors.New("number is outside the supported range")
	}
	if math.Trunc(parsed) == parsed && math.Abs(parsed) > float64(maxSafeInteger) {
		return errors.New("integer is outside the supported safe range")
	}
	return nil
}

func intPointer(value int) *int {
	return &value
}

func invalidError(field, message string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidSnapshot, field, message)
}

func duplicateField(field string) error {
	return invalidError(field, "must not be repeated")
}

func limitError(field string) error {
	return fmt.Errorf("%w: %s", ErrLimitExceeded, field)
}
