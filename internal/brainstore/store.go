package brainstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	manifestFileName  = "manifest.json"
	factsDirName      = "facts"
	factsFileName     = "facts.ndjson"
	factsMaxLineBytes = 64 * 1024

	// DefaultBranch is the branch the judge falls back to when the manifest does
	// not declare one.
	DefaultBranch = "main"

	// repoStoreDirName is the per-repo subtree under ENTIRE_PLUGIN_DATA_DIR.
	repoStoreDirName = "repos"
)

// Exists reports whether a directory holds an exported brain (a readable
// manifest.json that is not a directory).
func Exists(brainDir string) bool {
	info, err := os.Stat(filepath.Join(brainDir, manifestFileName))
	return err == nil && !info.IsDir()
}

// LoadManifest reads and parses the brain manifest. A missing manifest yields an
// empty manifest (so first-run callers do not special-case it); a malformed
// manifest is a hard error.
func LoadManifest(brainDir string) (*Manifest, error) {
	path := filepath.Join(brainDir, manifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{SchemaVersion: SchemaVersion}, nil
		}
		return nil, fmt.Errorf("read brain manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse brain manifest: %w", err)
	}
	return &manifest, nil
}

// Branch resolves the brain's default branch, falling back to DefaultBranch.
func (m *Manifest) Branch() string {
	if m != nil && m.DefaultBranch != "" {
		return m.DefaultBranch
	}
	if m != nil && m.Sources != nil && m.Sources.Sessions != nil && m.Sources.Sessions.DefaultBranch != "" {
		return m.Sources.Sessions.DefaultBranch
	}
	return DefaultBranch
}

// LoadFacts reads a branch's durable facts. Durable facts are an optional signal:
// a brain may have none, and a missing facts directory or file yields an empty
// slice rather than an error. Candidate layouts are probed in order so both the
// simple <facts>/<branch>/facts.ndjson layout and a slugged sibling layout
// resolve.
func LoadFacts(brainDir, branch string) ([]FactRecord, error) {
	for _, candidate := range factsCandidatePaths(brainDir, branch) {
		records, err := parseFactsFile(candidate)
		if err != nil {
			return nil, err
		}
		if len(records) > 0 {
			return records, nil
		}
	}
	return nil, nil
}

// factsCandidatePaths lists the on-disk locations a branch's facts file may live
// at. The simple <facts>/<branch>/facts.ndjson layout is tried first; a slugged
// branch directory (facts/<branch-slug>-<hash>/facts.ndjson) is matched by glob
// as a fallback so a brain built by the slugging layout still resolves.
func factsCandidatePaths(brainDir, branch string) []string {
	var paths []string
	factsRoot := filepath.Join(brainDir, factsDirName)
	paths = append(paths, filepath.Join(factsRoot, filepath.FromSlash(branch), factsFileName))
	// Slugged sibling layout: facts/<branch-slug>-<hash>/facts.ndjson.
	if matches, err := filepath.Glob(filepath.Join(factsRoot, "*", factsFileName)); err == nil {
		paths = append(paths, matches...)
	}
	return paths
}

func parseFactsFile(path string) ([]FactRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 8*1024), factsMaxLineBytes)
	var records []FactRecord
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var record FactRecord
		if err := json.Unmarshal([]byte(text), &record); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", filepath.Base(path), line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// ReadRelativeFile reads a brain-relative file (e.g. a transcript path from the
// manifest), rejecting paths that escape the brain directory.
func ReadRelativeFile(brainDir, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe brain-relative path: %s", rel)
	}
	data, err := os.ReadFile(filepath.Join(brainDir, clean))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// BrainDir resolves the on-disk brain directory for a repository. The brain dir
// is <pluginDataDir>/repos/<repoKey>. repoKey is read from the brain manifest if
// one already exists under any candidate key; otherwise it is derived from the
// repo's git origin remote (gh/<owner>/<repo>), falling back to a local key.
func BrainDir(pluginDataDir, repoKey string) string {
	return filepath.Join(pluginDataDir, repoStoreDirName, filepath.FromSlash(repoKey))
}
