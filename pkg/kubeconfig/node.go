package kubeconfig

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

const (
	strTag  = "!!str"
	boolTag = "!!bool"
	nullTag = "!!null"
	seqTag  = "!!seq"
	mapTag  = "!!map"
)

func scalar(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

func mapping(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: mapTag, Content: pairs}
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == nullTag
}

func lookup(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			return k, m.Content[i+1]
		}
	}
	return nil, nil
}

func stringValue(m *yaml.Node, key string) string {
	_, v := lookup(m, key)
	if v == nil || v.Kind != yaml.ScalarNode || isNull(v) {
		return ""
	}
	return v.Value
}

// insertSorted adds key at its alphabetical place among m's keys. kubectl
// writes every mapping with sorted keys, so this keeps a file kubectl wrote
// looking like one, and places a key sensibly in a file it did not.
func insertSorted(m *yaml.Node, key string, value *yaml.Node) {
	at := len(m.Content)
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value > key {
			at = i
			break
		}
	}
	pair := []*yaml.Node{scalar(strTag, key), value}
	m.Content = append(m.Content[:at], append(pair, m.Content[at:]...)...)
}

// setScalar sets key in m to a scalar, editing an existing value node in place
// so the comments attached to it stay.
func setScalar(m *yaml.Node, key, tag, value string) {
	_, v := lookup(m, key)
	if v == nil {
		insertSorted(m, key, scalar(tag, value))
		return
	}
	v.Kind, v.Tag, v.Value, v.Style, v.Content = yaml.ScalarNode, tag, value, 0, nil
}

func deleteKey(m *yaml.Node, key string) {
	kept := m.Content[:0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			continue
		}
		kept = append(kept, m.Content[i], m.Content[i+1])
	}
	m.Content = kept
}

// childMapping returns key's mapping in m, creating it when absent and turning
// an empty value (key with nothing after it) into a mapping in place. Open has
// already refused any other kind.
func childMapping(m *yaml.Node, key string) *yaml.Node {
	_, v := lookup(m, key)
	if v == nil {
		v = mapping()
		insertSorted(m, key, v)
		return v
	}
	if isNull(v) {
		v.Kind, v.Tag, v.Value, v.Style = yaml.MappingNode, mapTag, "", 0
	}
	return v
}

// childSequence is childMapping for the top-level lists.
func childSequence(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	k, v := lookup(m, key)
	if v == nil {
		k, v = scalar(strTag, key), &yaml.Node{Kind: yaml.SequenceNode, Tag: seqTag}
		insertSorted(m, key, v)
		return k, v
	}
	if isNull(v) {
		v.Kind, v.Tag, v.Value, v.Style = yaml.SequenceNode, seqTag, "", 0
	}
	return k, v
}

// appendBlock appends item to the sequence under key and leaves the sequence in
// block style, as kubectl writes it: `clusters: []` gets its first item.
func appendBlock(key, seq, item *yaml.Node) {
	if seq.Style&yaml.FlowStyle != 0 {
		seq.Style &^= yaml.FlowStyle
		// A flow sequence holds its trailing comment itself; once the sequence
		// is block, that comment would be emitted after the next key instead.
		if seq.LineComment != "" && key.LineComment == "" {
			key.LineComment, seq.LineComment = seq.LineComment, ""
		}
	}
	seq.Content = append(seq.Content, item)
}

func itemsNamed(seq *yaml.Node, name string) []*yaml.Node {
	var found []*yaml.Node
	for _, item := range seq.Content {
		if item.Kind == yaml.MappingNode && stringValue(item, "name") == name {
			found = append(found, item)
		}
	}
	return found
}

// keepFirstNamed removes every item named name but the first, which it returns
// (nil when there is none). kubectl refuses a file that repeats a name, so
// collapsing them repairs the file rather than losing anything kubectl used.
func keepFirstNamed(seq *yaml.Node, name string) *yaml.Node {
	var first *yaml.Node
	kept := seq.Content[:0]
	for _, item := range seq.Content {
		if item.Kind == yaml.MappingNode && stringValue(item, "name") == name {
			if first != nil {
				continue
			}
			first = item
		}
		kept = append(kept, item)
	}
	seq.Content = kept
	return first
}

// checkNoAnchors refuses an anchor or alias anywhere under n. Editing an
// anchored node in place would also change every alias of it, and dropping a
// duplicate that holds an anchor would leave its aliases pointing at nothing.
func checkNoAnchors(n *yaml.Node, where string) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fmt.Errorf("%s uses a YAML anchor or alias, which this edit cannot preserve", where)
	}
	for _, c := range n.Content {
		if err := checkNoAnchors(c, where); err != nil {
			return err
		}
	}
	return nil
}

// checkUniqueKeys refuses a mapping that names one key twice, which kubectl
// refuses to load as well.
func checkUniqueKeys(m *yaml.Node, where string) error {
	seen := map[string]bool{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if k.Value == "<<" {
			return fmt.Errorf("%s uses a YAML merge key, which this edit cannot preserve", where)
		}
		if seen[k.Value] {
			return fmt.Errorf("%s has the key %q more than once", where, k.Value)
		}
		seen[k.Value] = true
	}
	return nil
}
