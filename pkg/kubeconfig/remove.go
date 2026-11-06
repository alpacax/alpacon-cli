package kubeconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
}

// Target is what a context points at once kubectl has merged the files.
type Target struct {
	// Server is the server of the cluster the context names, empty when no
	// file defines that cluster or the context names none.
	Server string
	// PluginCluster is the --cluster the context's user passes to
	// 'alpacon kube credentials', empty for a user that does not run it.
	PluginCluster string
	// StaticToken reports that the context's user carries a bearer token.
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
// file this package refuses to edit wraps ErrMalformed.
func Inspect(path, name string) (Holding, error) {
	root, err := readRoot(path)
	if err != nil || root == nil {
		return Holding{}, err
	}
	return Holding{
		Entry:   len(named(root, "contexts", name)) > 0 || len(named(root, "clusters", name)) > 0 || len(named(root, "users", name)) > 0,
		Current: stringValue(root, "current-context") == name,
	}, nil
}

// ContextTargets returns, for each file in paths that defines the context name,
// in order, what that definition points at as kubectl merges paths: the cluster
// and user it names are taken from the first file that defines each, which need
// not be the file holding the context. The first Target is the context kubectl
// uses. With no context of that name anywhere, the cluster and user called name
// stand in for it. A file that is missing or cannot be parsed is skipped.
func ContextTargets(paths []string, name string) []Target {
	roots := readRoots(paths)
	first := func(list, name string) *yaml.Node {
		for _, root := range roots {
			if items := named(root, list, name); len(items) > 0 {
				return items[0]
			}
		}
		return nil
	}
	target := func(cluster, user *yaml.Node) Target {
		var t Target
		if cluster != nil {
			if _, inner := lookup(cluster, "cluster"); inner != nil && inner.Kind == yaml.MappingNode {
				t.Server = stringValue(inner, "server")
			}
		}
		if user != nil {
			t.PluginCluster = pluginCluster(user)
			t.StaticToken = holdsStaticToken(user)
		}
		return t
	}
	// resolve follows the context's key field to the item of list with that
	// name. An empty field names nothing, whatever shares the context's name.
	resolve := func(context *yaml.Node, key, list string) *yaml.Node {
		if ref := contextField(context, key); ref != "" {
			return first(list, ref)
		}
		return nil
	}

	var targets []Target
	for _, root := range roots {
		contexts := named(root, "contexts", name)
		if len(contexts) == 0 {
			continue
		}
		targets = append(targets, target(resolve(contexts[0], "cluster", "clusters"), resolve(contexts[0], "user", "users")))
	}
	if len(targets) > 0 {
		return targets
	}
	if cluster, user := first("clusters", name), first("users", name); cluster != nil || user != nil {
		return []Target{target(cluster, user)}
	}
	return nil
}

// ClusterServers returns the server of every cluster named name in paths, in
// order and without repeats: the clusters Remove deletes, each of which may have
// been written for another workspace. A file that is missing or cannot be
// parsed is skipped.
func ClusterServers(paths []string, name string) []string {
	var servers []string
	for _, root := range readRoots(paths) {
		for _, cluster := range named(root, "clusters", name) {
			_, inner := lookup(cluster, "cluster")
			if inner == nil || inner.Kind != yaml.MappingNode {
				continue
			}
			if server := stringValue(inner, "server"); server != "" && !slices.Contains(servers, server) {
				servers = append(servers, server)
			}
		}
	}
	return servers
}

// readRoots parses each of paths that exists and parses, in order.
func readRoots(paths []string) []*yaml.Node {
	var roots []*yaml.Node
	for _, path := range paths {
		if root, err := readRoot(path); err == nil && root != nil {
			roots = append(roots, root)
		}
	}
	return roots
}

// pluginCommands are the names the credential plugin runs under: kube login
// writes alpacon, and ac is the CLI's alias.
var pluginCommands = []string{"alpacon", "ac"}

// pluginCluster reads the value after --cluster in a user's exec args when the
// user runs 'alpacon kube credentials'. The shape is the one kube login writes;
// a test there reads what it writes back through ContextTargets.
func pluginCluster(item *yaml.Node) string {
	_, user := lookup(item, "user")
	if user == nil || user.Kind != yaml.MappingNode {
		return ""
	}
	_, exec := lookup(user, "exec")
	if exec == nil || exec.Kind != yaml.MappingNode {
		return ""
	}
	command := strings.TrimSuffix(filepath.Base(stringValue(exec, "command")), ".exe")
	if !slices.Contains(pluginCommands, command) {
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
