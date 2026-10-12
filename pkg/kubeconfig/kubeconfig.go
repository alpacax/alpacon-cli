// Package kubeconfig writes one cluster, user and context into a kubeconfig
// file and reads them back. It edits the parsed YAML tree rather than a decoded
// struct, so entries, keys and comments it does not own survive the rewrite.
package kubeconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
	"go.yaml.in/yaml/v3"
)

var (
	// ErrLocked marks a kubeconfig another process holds the write lock on.
	ErrLocked = errors.New("kubeconfig is locked")

	// ErrMalformed marks a kubeconfig whose shape this package refuses to edit.
	ErrMalformed = errors.New("unsupported kubeconfig")

	// ErrContextNotFound and ErrClusterNotFound mark a Lookup miss.
	ErrContextNotFound = errors.New("context not found")
	ErrClusterNotFound = errors.New("cluster not found")
)

const (
	newFilePerm = 0600 // the file holds a bearer token
	dirPerm     = 0755 // kubectl's mode for a directory it creates
)

// freshConfig is the document a missing or empty file starts from, with the
// top-level keys in the order kubectl writes them.
const freshConfig = `apiVersion: v1
clusters: []
contexts: []
current-context: ""
kind: Config
preferences: {}
users: []
`

// Entry is what Apply writes. Name is shared by the cluster, the user and the
// context, as one session yields exactly one of each.
type Entry struct {
	Name                  string
	Server                string
	ProxyURL              string // empty removes a proxy-url left by an earlier write
	Token                 string
	InsecureSkipTLSVerify bool
}

// Editor holds a kubeconfig's write lock and its parsed contents between Open
// and Save, so a file that cannot be edited is found out before anything is
// created for it.
type Editor struct {
	path     string
	lockPath string
	locked   bool
	doc      *yaml.Node
	root     *yaml.Node // its top-level mapping
}

type lockedError struct{ path string }

func (e *lockedError) Error() string {
	return fmt.Sprintf("another process is editing %s; remove %s.lock if no kubectl is running", e.path, e.path)
}

func (e *lockedError) Is(target error) bool { return target == ErrLocked }

type malformedError struct{ path, reason string }

func (e *malformedError) Error() string {
	return fmt.Sprintf("cannot use %s: %s", e.path, e.reason)
}

func (e *malformedError) Is(target error) bool { return target == ErrMalformed }

// Open takes kubectl's write lock on path (<path>.lock, created exclusively, so
// a kubectl editing the file at the same time and this command exclude each
// other), then reads and checks the file. A missing, empty or comment-only file
// opens as a fresh config. Close must be called on the returned Editor.
func Open(path string) (*Editor, error) {
	if path == "" {
		return nil, errors.New("kubeconfig path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("failed to create the directory for %s: %w", path, err)
	}

	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, newFilePerm)
	if errors.Is(err, fs.ErrExist) {
		return nil, &lockedError{path: path}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock %s: %w", path, err)
	}
	ed := &Editor{path: path, lockPath: lockPath, locked: true}
	if err := f.Close(); err != nil {
		_ = ed.Close()
		return nil, fmt.Errorf("failed to lock %s: %w", path, err)
	}

	if err := ed.load(); err != nil {
		_ = ed.Close()
		return nil, err
	}
	return ed, nil
}

func (ed *Editor) load() error {
	target, err := resolveTarget(ed.path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read %s: %w", ed.path, err)
	}
	doc, root, err := parse(ed.path, data)
	if err != nil {
		return err
	}
	if err := checkWritable(filepath.Dir(target)); err != nil {
		return err
	}
	ed.doc, ed.root = doc, root
	return nil
}

// resolveTarget follows path's symlinks to the file Save will replace, as
// utils.SaveStreamAtomic does, so the directory checked is the one written to.
func resolveTarget(path string) (string, error) {
	target := path
	for range 255 {
		info, err := os.Lstat(target)
		if errors.Is(err, fs.ErrNotExist) {
			return target, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to access %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory", path)
			}
			return target, nil
		}
		next, err := os.Readlink(target)
		if err != nil {
			return "", fmt.Errorf("failed to resolve %s: %w", path, err)
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(target), next)
		}
		target = next
	}
	return "", fmt.Errorf("failed to resolve %s: too many symlinks", path)
}

// checkWritable proves dir accepts a new file, which is what the atomic
// replace needs; the existing file's own mode does not matter to a rename.
func checkWritable(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".alpacon-kubeconfig-check-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, removeErr)
	}
	return nil
}

// parse decodes data into a document whose top level is a mapping, and refuses
// any shape an in-place edit could not keep intact.
func parse(path string, data []byte) (*yaml.Node, *yaml.Node, error) {
	malformed := func(format string, args ...any) error {
		return &malformedError{path: path, reason: fmt.Sprintf(format, args...)}
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	err := dec.Decode(&doc)
	if errors.Is(err, io.EOF) {
		// An empty or comment-only file. The decoder hands back no node to
		// carry the comments, so they are moved onto the fresh document.
		fresh, root, err := freshDocument()
		if err != nil {
			return nil, nil, err
		}
		fresh.HeadComment = commentLines(data)
		return fresh, root, nil
	}
	if err != nil {
		return nil, nil, malformed("not valid YAML: %v", err)
	}
	for {
		var extra yaml.Node
		err := dec.Decode(&extra)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, malformed("not valid YAML: %v", err)
		}
		// kubectl reads the first document alone. A trailing empty one (a
		// stray "---") carries nothing to lose; anything else would be dropped.
		if !isEmptyDocument(&extra) {
			return nil, nil, malformed("it holds more than one YAML document")
		}
	}

	if len(doc.Content) != 1 {
		return nil, nil, malformed("not a YAML document")
	}
	root := doc.Content[0]
	if isNull(root) {
		// An empty document ("---" alone, or "null") is a fresh config. It is
		// filled in place so any comment attached to it stays.
		_, fresh, err := freshDocument()
		if err != nil {
			return nil, nil, err
		}
		root.Kind, root.Tag, root.Value, root.Style, root.Content = yaml.MappingNode, mapTag, "", 0, fresh.Content
		return &doc, root, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, nil, malformed("its top level is not a mapping")
	}
	if err := validate(root); err != nil {
		return nil, nil, malformed("%s", err.Error())
	}
	return &doc, root, nil
}

func isEmptyDocument(doc *yaml.Node) bool {
	if doc.HeadComment != "" || doc.LineComment != "" || doc.FootComment != "" {
		return false
	}
	for _, c := range doc.Content {
		if !isNull(c) || c.HeadComment != "" || c.LineComment != "" || c.FootComment != "" {
			return false
		}
	}
	return true
}

// commentLines returns the comment lines of a file that holds nothing else,
// in the form the encoder writes as a document's head comment.
func commentLines(data []byte) string {
	var lines []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func freshDocument() (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(freshConfig), &doc); err != nil {
		return nil, nil, err
	}
	return &doc, doc.Content[0], nil
}

// listKeys are the top-level lists Apply edits, each item a mapping holding a
// name and a nested mapping under the item key.
var listKeys = []struct{ list, item string }{
	{"clusters", "cluster"},
	{"contexts", "context"},
	{"users", "user"},
}

func validate(root *yaml.Node) error {
	if err := checkUniqueKeys(root, "the top level"); err != nil {
		return err
	}
	for _, lk := range listKeys {
		_, v := lookup(root, lk.list)
		if v == nil || isNull(v) {
			continue
		}
		if err := checkNoAnchors(v, lk.list); err != nil {
			return err
		}
		if v.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s is not a list", lk.list)
		}
		for i, item := range v.Content {
			where := fmt.Sprintf("%s item %d", lk.list, i+1)
			if item.Kind != yaml.MappingNode {
				return fmt.Errorf("%s is not a mapping", where)
			}
			if err := checkUniqueKeys(item, where); err != nil {
				return err
			}
			if _, name := lookup(item, "name"); name != nil && name.Kind != yaml.ScalarNode {
				return fmt.Errorf("%s has a name that is not a string", where)
			}
			_, inner := lookup(item, lk.item)
			if inner == nil || isNull(inner) {
				continue
			}
			if inner.Kind != yaml.MappingNode {
				return fmt.Errorf("%s has a %s that is not a mapping", where, lk.item)
			}
			if err := checkUniqueKeys(inner, where); err != nil {
				return err
			}
		}
	}
	if _, v := lookup(root, "current-context"); v != nil {
		if v.Kind != yaml.ScalarNode || v.Anchor != "" {
			return errors.New("current-context is not a string")
		}
	}
	return nil
}

// Apply writes e into the parsed file. It returns the current-context it
// replaced (which may equal e.Name), and the host of the cluster's previous
// server when that differs from e.Server's, so the caller can say another
// endpoint's entry was overwritten. Nothing reaches disk until Save.
func (ed *Editor) Apply(e Entry) (previousContext string, replacedServer string) {
	ed.root.Style &^= yaml.FlowStyle

	replacedServer = ed.applyCluster(e)
	ed.applyUser(e)
	ed.applyContext(e)

	if _, v := lookup(ed.root, "current-context"); v != nil && !isNull(v) {
		previousContext = v.Value
	}
	setScalar(ed.root, "current-context", strTag, e.Name)
	return previousContext, replacedServer
}

// applyCluster edits the cluster in place: certificate data and any other key
// it does not set stay as they were.
func (ed *Editor) applyCluster(e Entry) string {
	key, seq := childSequence(ed.root, "clusters")
	item := keepFirstNamed(seq, e.Name)
	if item == nil {
		item = mapping(scalar(strTag, "cluster"), mapping(), scalar(strTag, "name"), scalar(strTag, e.Name))
		appendBlock(key, seq, item)
	}
	cluster := childMapping(item, "cluster")

	oldServer := stringValue(cluster, "server")
	replaced := ""
	if oldServer != "" && !strings.EqualFold(serverHost(oldServer), serverHost(e.Server)) {
		replaced = serverHost(oldServer)
		// A CA or server name set for the old endpoint would verify the new
		// one against the wrong certificate, so they leave with the host.
		deleteKey(cluster, "certificate-authority")
		deleteKey(cluster, "certificate-authority-data")
		deleteKey(cluster, "tls-server-name")
	}
	setScalar(cluster, "server", strTag, e.Server)
	if e.ProxyURL != "" {
		setScalar(cluster, "proxy-url", strTag, e.ProxyURL)
	} else {
		deleteKey(cluster, "proxy-url")
	}
	if e.InsecureSkipTLSVerify {
		setScalar(cluster, "insecure-skip-tls-verify", boolTag, "true")
		// kubectl refuses a cluster that names a CA alongside the insecure flag
		// ("specifying a root certificates file with the insecure flag is not
		// allowed"), so a CA left by an earlier write would break the context.
		deleteKey(cluster, "certificate-authority")
		deleteKey(cluster, "certificate-authority-data")
	} else {
		deleteKey(cluster, "insecure-skip-tls-verify")
	}
	return replaced
}

// serverHost is the host[:port] of a server URL, or the raw value when it has
// none, so an unparsable old value is still reported rather than hidden.
func serverHost(server string) string {
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		return u.Host
	}
	return server
}

// applyUser replaces the user whole: a stale exec plugin or client
// certificate left beside the token would take precedence or conflict.
func (ed *Editor) applyUser(e Entry) {
	key, seq := childSequence(ed.root, "users")
	fresh := []*yaml.Node{
		scalar(strTag, "name"), scalar(strTag, e.Name),
		scalar(strTag, "user"), mapping(scalar(strTag, "token"), scalar(strTag, e.Token)),
	}
	if item := keepFirstNamed(seq, e.Name); item != nil {
		item.Content = fresh
		return
	}
	appendBlock(key, seq, mapping(fresh...))
}

// applyContext points the context at the cluster and user in place, keeping a
// namespace or extensions the user set on it.
func (ed *Editor) applyContext(e Entry) {
	key, seq := childSequence(ed.root, "contexts")
	item := keepFirstNamed(seq, e.Name)
	if item == nil {
		item = mapping(scalar(strTag, "context"), mapping(), scalar(strTag, "name"), scalar(strTag, e.Name))
		appendBlock(key, seq, item)
	}
	context := childMapping(item, "context")
	setScalar(context, "cluster", strTag, e.Name)
	setScalar(context, "user", strTag, e.Name)
}

// Save writes the edited file atomically in kubectl's layout: two-space
// indent, with list items flush under their key.
func (ed *Editor) Save() error {
	if !ed.locked {
		return errors.New("kubeconfig editor is closed")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	enc.CompactSeqIndent()
	if err := enc.Encode(ed.doc); err != nil {
		return fmt.Errorf("failed to encode %s: %w", ed.path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("failed to encode %s: %w", ed.path, err)
	}

	// An existing file keeps its mode, and it is passed as the mode for a new
	// file too: SaveStreamAtomic warns when the kept mode is wider than the one
	// requested and advises removing the file, which is wrong for a kubeconfig
	// that holds the user's other clusters. The warning below says what to do
	// instead. Re-permissioning the user's file is not this command's call.
	perm := os.FileMode(newFilePerm)
	existing := false
	if info, err := os.Stat(ed.path); err == nil && info.Mode().IsRegular() {
		perm, existing = info.Mode().Perm(), true
	}
	if _, err := utils.SaveStreamAtomic(ed.path, &buf, perm); err != nil {
		return fmt.Errorf("failed to write %s: %w", ed.path, err)
	}
	if existing && readableByOthers(perm) {
		utils.CliWarning("%s holds a bearer token and is readable by other accounts; run 'chmod 600 %s'", ed.path, ed.path)
	}
	return nil
}

// readableByOthers reports a group- or world-readable mode. Windows has no
// Unix modes (os.Stat reports 0666 for any writable file), so it never warns.
func readableByOthers(perm os.FileMode) bool {
	return runtime.GOOS != "windows" && perm&0044 != 0
}

// Close releases the write lock. It is safe to call more than once, and it
// never removes a lock this Editor did not create.
func (ed *Editor) Close() error {
	if !ed.locked {
		return nil
	}
	ed.locked = false
	if err := os.Remove(ed.lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove %s: %w", ed.lockPath, err)
	}
	return nil
}

// Lookup returns the server and proxy-url of the cluster that contextName
// points at, merging paths the way kubectl does: each name is taken from the
// first file that defines it, a missing file is skipped, and a malformed one
// fails the whole lookup. A context found nowhere wraps ErrContextNotFound; a
// cluster found nowhere wraps ErrClusterNotFound.
func Lookup(paths []string, contextName string) (server, proxyURL string, err error) {
	type file struct {
		path string
		root *yaml.Node
	}
	var files []file
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("failed to read %s: %w", path, err)
		}
		_, root, err := parse(path, data)
		if err != nil {
			return "", "", err
		}
		files = append(files, file{path: path, root: root})
	}
	searched := strings.Join(paths, ", ")

	find := func(list, name string) (*yaml.Node, string, error) {
		for _, f := range files {
			item, err := uniqueItem(f.path, f.root, list, name)
			if err != nil {
				return nil, "", err
			}
			if item != nil {
				return item, f.path, nil
			}
		}
		return nil, "", nil
	}

	context, contextPath, err := find("contexts", contextName)
	if err != nil {
		return "", "", err
	}
	if context == nil {
		return "", "", fmt.Errorf("%w: no context %q in %s", ErrContextNotFound, contextName, searched)
	}
	clusterName := ""
	if _, inner := lookup(context, "context"); inner != nil && inner.Kind == yaml.MappingNode {
		clusterName = stringValue(inner, "cluster")
	}
	if clusterName == "" {
		return "", "", fmt.Errorf("%w: context %q in %s names no cluster", ErrClusterNotFound, contextName, contextPath)
	}

	cluster, clusterPath, err := find("clusters", clusterName)
	if err != nil {
		return "", "", err
	}
	if cluster == nil {
		return "", "", fmt.Errorf("%w: no cluster %q (used by context %q) in %s", ErrClusterNotFound, clusterName, contextName, searched)
	}
	if _, inner := lookup(cluster, "cluster"); inner != nil && inner.Kind == yaml.MappingNode {
		server, proxyURL = stringValue(inner, "server"), stringValue(inner, "proxy-url")
	}
	if server == "" {
		return "", "", fmt.Errorf("cluster %q in %s has no server", clusterName, clusterPath)
	}
	return server, proxyURL, nil
}

// uniqueItem returns the item of root's list named name, nil when absent. A
// repeated name is refused, as kubectl refuses to load such a file.
func uniqueItem(path string, root *yaml.Node, list, name string) (*yaml.Node, error) {
	_, seq := lookup(root, list)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil, nil
	}
	items := itemsNamed(seq, name)
	if len(items) > 1 {
		return nil, &malformedError{path: path, reason: fmt.Sprintf("%s has more than one entry named %q", list, name)}
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}
