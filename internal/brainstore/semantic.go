package brainstore

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SemanticSummary is a bounded, judge-facing digest of the entire-sem snapshot:
// what a team actually built, by symbol kind and language, with the busiest
// files. The execution lens uses it to check built-vs-planned.
type SemanticSummary struct {
	Provider     string
	Symbols      int
	Relations    int
	Files        int
	Capabilities []string
	ByKind       []LabelCount // symbol kinds (function, class, route, …), most first
	ByLanguage   []LabelCount
	TopFiles     []LabelCount // file path -> symbol count
}

// LabelCount is a label with its tally, used for the digest histograms.
type LabelCount struct {
	Label string
	Count int
}

// semRecord is the subset of entire-sem's NDJSON record schema the judge reads.
type semRecord struct {
	RecordType string `json:"record_type"`
	Kind       string `json:"kind"`
	FilePath   string `json:"file_path"`
	Language   string `json:"language"`
}

// semanticSnapshotMaxBytes caps how much of the snapshot NDJSON the judge scans,
// so a huge repo's snapshot cannot blow up memory.
const semanticSnapshotMaxBytes = 32 << 20

// LoadSemanticSummary reads the sem snapshot referenced by the semantic source
// and returns a bounded digest. The sem layer is OPTIONAL: a nil source, a
// missing/unreadable snapshot, or a path that tries to escape the brain dir all
// yield (nil, nil) — absence is not an error, the lenses degrade.
func LoadSemanticSummary(brainDir string, source *SemanticSource) (*SemanticSummary, error) {
	if source == nil || source.SnapshotPath == "" {
		return nil, nil
	}
	rel := filepath.FromSlash(source.SnapshotPath)
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return nil, nil
	}
	f, err := os.Open(filepath.Join(brainDir, rel))
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	sum := &SemanticSummary{Provider: source.Provider, Capabilities: source.Capabilities}
	kind := map[string]int{}
	lang := map[string]int{}
	fileSym := map[string]int{}
	files := map[string]struct{}{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	read := 0
	for sc.Scan() {
		line := sc.Bytes()
		read += len(line)
		if read > semanticSnapshotMaxBytes {
			break
		}
		var rec semRecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		switch rec.RecordType {
		case "symbol":
			sum.Symbols++
			if rec.Kind != "" {
				kind[rec.Kind]++
			}
			if rec.Language != "" {
				lang[rec.Language]++
			}
			if rec.FilePath != "" {
				fileSym[rec.FilePath]++
				files[rec.FilePath] = struct{}{}
			}
		case "relation":
			sum.Relations++
		case "file":
			if rec.FilePath != "" {
				files[rec.FilePath] = struct{}{}
			}
		}
	}

	sum.Files = len(files)
	// Fall back to the source manifest's counts if the snapshot didn't enumerate.
	if sum.Files == 0 {
		sum.Files = source.Files
	}
	if sum.Symbols == 0 {
		sum.Symbols = source.Symbols
	}
	if sum.Relations == 0 {
		sum.Relations = source.Relations
	}
	sum.ByKind = topLabelCounts(kind, 8)
	sum.ByLanguage = topLabelCounts(lang, 6)
	sum.TopFiles = topLabelCounts(fileSym, 6)
	return sum, nil
}

// topLabelCounts returns the highest-count labels (count desc, then label asc),
// capped at limit.
func topLabelCounts(m map[string]int, limit int) []LabelCount {
	out := make([]LabelCount, 0, len(m))
	for k, v := range m {
		out = append(out, LabelCount{Label: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
