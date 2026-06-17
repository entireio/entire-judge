package cli

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
	"github.com/suhaanthayyil/entire-judge/internal/gitutil"
)

const (
	envCLIVersion    = "ENTIRE_CLI_VERSION"
	envRepoRoot      = "ENTIRE_REPO_ROOT"
	envPluginDataDir = "ENTIRE_PLUGIN_DATA_DIR"
)

// EntireEnv captures environment variables supplied by Entire when it dispatches
// an external plugin command.
type EntireEnv struct {
	CLIVersion    string
	RepoRoot      string
	PluginDataDir string
}

// repoStorage locates a repo's brain on disk.
type repoStorage struct {
	Key      string
	BrainDir string
}

// resolveBrainStorage resolves the brain directory for a repository. Brains are
// authored by the `brain` plugin under its own data dir
// (<plugins-data>/brain/repos/<repoKey>), which is a sibling of judge's data dir
// (<plugins-data>/judge), so judge must look there rather than under its own
// data dir. It also falls back to judge's own data dir for brains built by
// `entire judge add`. The repo key is read from the brain manifest's repo_key
// field when a brain already exists; otherwise it is derived from the git origin
// remote (gh/<owner>/<repo>), falling back to a local key.
func resolveBrainStorage(ctx context.Context, runner gitutil.CommandRunner, env EntireEnv, repoDir string) repoStorage {
	key := deriveRepoKey(ctx, runner, repoDir)
	candidates := brainDataDirs(env.PluginDataDir)
	// The primary (brain plugin) location is what an unresolved error should
	// point the user at — that's where `entire brain refresh` writes.
	primary, _ := storageIn(candidates[0], key)
	for _, dataDir := range candidates {
		if st, ok := storageIn(dataDir, key); ok {
			return st
		}
	}
	return primary
}

// storageIn resolves the repo's brain dir under a single plugin data dir,
// preferring the manifest's own repo_key when a brain exists there (the manifest
// is authoritative). The bool reports whether a brain manifest was found.
func storageIn(dataDir, key string) (repoStorage, bool) {
	brainDir := brainstore.BrainDir(dataDir, key)
	// LoadManifest tolerates a missing manifest (returns an empty one), so use
	// Exists to decide whether a brain actually lives here.
	if !brainstore.Exists(brainDir) {
		return repoStorage{Key: key, BrainDir: brainDir}, false
	}
	if manifest, err := brainstore.LoadManifest(brainDir); err == nil && manifest.RepoKey != "" && manifest.RepoKey != key {
		key = manifest.RepoKey
		brainDir = brainstore.BrainDir(dataDir, key)
	}
	return repoStorage{Key: key, BrainDir: brainDir}, true
}

// brainDataDirs returns the plugin data dirs to search for a brain, most
// authoritative first: the sibling `brain` plugin data dir (where
// `entire brain refresh` writes), then judge's own data dir (where
// `entire judge add` writes). The host sets ENTIRE_PLUGIN_DATA_DIR to judge's
// dir (.../plugins/data/judge); the brain's is its sibling (.../plugins/data/brain).
func brainDataDirs(pluginDataDir string) []string {
	if pluginDataDir == "" {
		return []string{""}
	}
	brainDir := pluginDataDir
	if filepath.Base(pluginDataDir) != brainPluginDirName {
		brainDir = filepath.Join(filepath.Dir(pluginDataDir), brainPluginDirName)
	}
	if brainDir == pluginDataDir {
		return []string{pluginDataDir}
	}
	return []string{brainDir, pluginDataDir}
}

const brainPluginDirName = "brain"

// deriveRepoKey produces a slash-joined storage key for a repository, preferring
// gh/<owner>/<repo> derived from the origin remote and falling back to a local
// path-based key.
func deriveRepoKey(ctx context.Context, runner gitutil.CommandRunner, repoDir string) string {
	if remote := gitutil.RemoteOriginURL(ctx, runner, repoDir); remote != "" {
		if key, ok := repoKeyFromRemote(remote); ok {
			return key
		}
	}
	abs := repoDir
	if a, err := filepath.Abs(repoDir); err == nil {
		abs = a
	}
	return filepath.ToSlash(filepath.Join("local", localSlug(abs)))
}

var (
	scpRemoteRegex = regexp.MustCompile(`^([^@/:]+@)?([^/:]+):(.+)$`)
	hostSlugRegex  = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// repoKeyFromRemote parses a git remote URL into a gh/<owner>/<repo> style key.
// GitHub hosts use the "gh" host slug; other hosts keep a sanitized host segment.
func repoKeyFromRemote(remote string) (string, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", false
	}
	host, path, ok := splitRemote(remote)
	if !ok {
		return "", false
	}
	components := normalizeRepoPath(path)
	if len(components) == 0 {
		return "", false
	}
	slug := hostSlug(host)
	return strings.Join(append([]string{slug}, components...), "/"), true
}

// splitRemote extracts (host, path) from an https://, ssh://, or scp-style git
// remote URL.
func splitRemote(remote string) (host, path string, ok bool) {
	if idx := strings.Index(remote, "://"); idx >= 0 {
		rest := remote[idx+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", "", false
		}
		host = rest[:slash]
		path = rest[slash+1:]
		if h, _, found := strings.Cut(host, ":"); found {
			host = h
		}
		return host, path, host != "" && path != ""
	}
	if match := scpRemoteRegex.FindStringSubmatch(remote); len(match) == 4 {
		return match[2], match[3], match[2] != "" && match[3] != ""
	}
	return "", "", false
}

// normalizeRepoPath splits an owner/repo path, dropping a trailing ".git" and
// empty segments.
func normalizeRepoPath(path string) []string {
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	var out []string
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// hostSlug maps a git host to its storage slug: GitHub becomes "gh"; other hosts
// are sanitized into a filesystem-safe segment.
func hostSlug(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "github.com", "www.github.com", "ssh.github.com":
		return "gh"
	}
	slug := hostSlugRegex.ReplaceAllString(host, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "host"
	}
	return slug
}

// localSlug derives a filesystem-safe key segment from a repo path for repos with
// no usable remote.
func localSlug(repoDir string) string {
	base := filepath.Base(repoDir)
	slug := hostSlugRegex.ReplaceAllString(strings.ToLower(base), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "repo"
	}
	return slug
}

func valueOrUnset(value string) string {
	if value == "" {
		return "<unset>"
	}
	return value
}
