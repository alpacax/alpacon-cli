package kubeconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Removal says what Remove changed.
type Removal struct {
	Changed        bool
	ClearedCurrent bool
	// StaticToken reports that the removed user carried a bearer token in the
	// file, which is what 'kube config' writes: no saved record names its
	// session, so the caller cannot close it.
	StaticToken bool
	// KeptUser is the user the removed context named under another name. Remove
	// leaves it, since another context or a hand edit may own it.
	KeptUser string
	// KeptUserToken reports that KeptUser carries a bearer token.
	KeptUserToken bool
	// Dangling lists the contexts left in the file that name the removed
	// cluster or user, such as one 'kubectl config rename-context' renamed.
	Dangling []string
}

// Holding is what one kubeconfig file holds of a name, read without its lock.
type Holding struct {
	Entry   bool // it has a cluster, user or context of that name
	Current bool // its current-context is that name
	// Server is the server of the cluster the name's context points at in this
	// file, empty when this file does not say.
	Server string
	// PluginCluster is the --cluster the name's user passes to
	// 'kube credentials', empty for a user that does not run the plugin.
	PluginCluster string
	// StaticToken reports that the name's user carries a bearer token.
	StaticToken bool
}

// Remove deletes every cluster, user and context named name. A current-context
// naming it is set to "", the value kubectl writes for a fresh config, so the
// file does not keep pointing at a context that is gone. Nothing reaches disk
// until Save.
func (ed *Editor) Remove(name string) Removal {
	var r Removal
	for _, item := range named(ed.root, "users", name) {
		r.StaticToken = r.StaticToken || holdsStaticToken(item)
	}
	if contexts := named(ed.root, "contexts", name); len(contexts) > 0 {
		if user := contextField(contexts[0], "user"); user != "" && user != name {
			r.KeptUser = user
			for _, item := range named(ed.root, "users", user) {
				r.KeptUserToken = r.KeptUserToken || holdsStaticToken(item)
			}
		}
	}
	cluster := removeNamed(ed.root, "clusters", name)
	user := removeNamed(ed.root, "users", name)
	context := removeNamed(ed.root, "contexts", name)
	r.Changed = cluster || user || context
	if cluster || user {
		r.Dangling = contextsNaming(ed.root, name)
	}
	if stringValue(ed.root, "current-context") == name {
		setScalar(ed.root, "current-context", strTag, "")
		r.ClearedCurrent = true
		r.Changed = true
	}
	return r
}

// named returns the items of root's list called name.
func named(root *yaml.Node, list, name string) []*yaml.Node {
	_, seq := lookup(root, list)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	return itemsNamed(seq, name)
}

// contextField reads key ("cluster" or "user") of a context item.
func contextField(item *yaml.Node, key string) string {
	_, inner := lookup(item, "context")
	if inner == nil || inner.Kind != yaml.MappingNode {
		return ""
	}
	return stringValue(inner, key)
}

// contextsNaming lists the contexts whose cluster or user is name.
func contextsNaming(root *yaml.Node, name string) []string {
	_, seq := lookup(root, "contexts")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	var found []string
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if contextField(item, "cluster") == name || contextField(item, "user") == name {
			if n := stringValue(item, "name"); !slices.Contains(found, n) {
				found = append(found, n)
			}
		}
	}
	return found
}

func holdsStaticToken(item *yaml.Node) bool {
	_, user := lookup(item, "user")
	if user == nil || user.Kind != yaml.MappingNode {
		return false
	}
	_, token := lookup(user, "token")
	return token != nil && !isNull(token)
}

func removeNamed(root *yaml.Node, list, name string) bool {
	_, seq := lookup(root, list)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return false
	}
	removed := false
	kept := seq.Content[:0]
	for _, item := range seq.Content {
		if item.Kind == yaml.MappingNode && stringValue(item, "name") == name {
			removed = true
			continue
		}
		kept = append(kept, item)
	}
	seq.Content = kept
	return removed
}

// Inspect reads what path holds of name. A missing file is an empty Holding; a
// file this package refuses to edit wraps ErrMalformed. A duplicated name is
// read from its first item, as kubectl does, where Lookup refuses it: the
// caller removes every item anyway.
func Inspect(path, name string) (Holding, error) {
	root, err := readRoot(path)
	if err != nil || root == nil {
		return Holding{}, err
	}

	first := func(list, name string) *yaml.Node {
		if items := named(root, list, name); len(items) > 0 {
			return items[0]
		}
		return nil
	}
	context, cluster, user := first("contexts", name), first("clusters", name), first("users", name)
	h := Holding{
		Entry:   context != nil || cluster != nil || user != nil,
		Current: stringValue(root, "current-context") == name,
	}
	if context != nil {
		if target := contextField(context, "cluster"); target != "" && target != name {
			cluster = first("clusters", target)
		}
	}
	if cluster != nil {
		if _, inner := lookup(cluster, "cluster"); inner != nil && inner.Kind == yaml.MappingNode {
			h.Server = stringValue(inner, "server")
		}
	}
	if user != nil {
		h.PluginCluster = pluginCluster(user)
		h.StaticToken = holdsStaticToken(user)
	}
	return h, nil
}

// pluginCluster reads the value after --cluster in a user's exec args, which
// only a user running 'kube credentials' has. The shape is the one kube login
// writes; a test there reads what it writes back through Inspect.
func pluginCluster(item *yaml.Node) string {
	_, user := lookup(item, "user")
	if user == nil || user.Kind != yaml.MappingNode {
		return ""
	}
	_, exec := lookup(user, "exec")
	if exec == nil || exec.Kind != yaml.MappingNode {
		return ""
	}
	_, args := lookup(exec, "args")
	if args == nil || args.Kind != yaml.SequenceNode {
		return ""
	}
	if len(args.Content) < 2 || args.Content[0].Value != "kube" || args.Content[1].Value != "credentials" {
		return ""
	}
	for i := 2; i+1 < len(args.Content); i++ {
		if args.Content[i].Value == "--cluster" {
			return args.Content[i+1].Value
		}
	}
	return ""
}

// CurrentContext returns the current-context kubectl would use across paths:
// the first file that sets a non-empty one. A missing file is skipped and a
// malformed one fails the call, as in Lookup.
func CurrentContext(paths []string) (string, error) {
	for _, path := range paths {
		root, err := readRoot(path)
		if err != nil {
			return "", err
		}
		if root == nil {
			continue
		}
		if current := stringValue(root, "current-context"); current != "" {
			return current, nil
		}
	}
	return "", nil
}

// readRoot parses path, returning a nil root for a file that does not exist.
func readRoot(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	_, root, err := parse(path, data)
	return root, err
}
