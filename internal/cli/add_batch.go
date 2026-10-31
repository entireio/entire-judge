package cli

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// runJudgeAddBatch adds every submission named on the command line and/or in
// --from. A single positional URL keeps the original fail-fast behavior; with
// more than one, failures are collected so one bad repo cannot stall a jury
// that is importing a whole event.
func runJudgeAddBatch(cmd *cobra.Command, opts Options, flags addFlags, args []string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	urls := append([]string(nil), args...)
	if flags.from != "" {
		src, err := readSubmissionSource(flags.from, flags.column)
		if err != nil {
			return err
		}
		for _, missing := range src.missing {
			fmt.Fprintf(errOut, "warning: no repo link found for %s\n", missing)
		}
		urls = append(urls, src.urls...)
	} else if flags.column != "" {
		return fmt.Errorf("invalid_flags: --column requires --from")
	}
	urls = dedupeURLs(urls)

	if len(urls) == 0 {
		if flags.from != "" {
			return fmt.Errorf("no_submissions: found no repo URLs in %s", flags.from)
		}
		return fmt.Errorf("no_submissions: pass a repo URL or --from FILE")
	}
	if len(urls) == 1 {
		return runJudgeAdd(cmd, opts, flags, urls[0])
	}

	fmt.Fprintf(out, "adding %d submissions into %s\n", len(urls), flags.dir)
	var failed []string
	dirOwner := make(map[string]string, len(urls)) // checkout dir -> URL that claimed it
	for i, u := range urls {
		fmt.Fprintf(out, "[%d/%d] %s\n", i+1, len(urls), u)
		// Checkout dirs are owner_repo with no host, so github.com/a/b and
		// gitlab.com/a/b would share one dir and the second would silently
		// "skip" onto the first repo's clone.
		if name := submissionDirName(u); name != "" {
			if prev, ok := dirOwner[name]; ok {
				fmt.Fprintf(errOut, "error: %s: dir_collision: checkout dir %q is already used by %s\n", u, name, prev)
				failed = append(failed, u)
				continue
			}
			dirOwner[name] = u
		}
		if err := runJudgeAdd(cmd, opts, flags, u); err != nil {
			fmt.Fprintf(errOut, "error: %s: %v\n", u, err)
			failed = append(failed, u)
		}
	}
	fmt.Fprintf(out, "added %d of %d submissions\n", len(urls)-len(failed), len(urls))
	if len(failed) > 0 {
		return fmt.Errorf("add_failed: %d submission(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

type submissionSource struct {
	urls    []string
	missing []string // human-readable labels for rows with no repo link
}

// readSubmissionSource loads repo URLs from a plain list or a CSV export.
func readSubmissionSource(path, column string) (submissionSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return submissionSource{}, fmt.Errorf("read_failed: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // Excel/Devpost BOM
	if strings.EqualFold(filepath.Ext(path), ".csv") || column != "" {
		return parseSubmissionCSV(bytes.NewReader(data), column)
	}
	return parseSubmissionList(bytes.NewReader(data)), nil
}

// parseSubmissionList reads one entry per line, skipping blanks and # comments.
// A line that is already a clone URL with its own transport or credentials
// (ssh://, git@host:path, https://user@host/...) is kept verbatim. Otherwise a
// recognizable repo link is normalized to the repo root, and any other URL or
// path is passed through so self-hosted git still works. Lines that are none
// of these (prose, a CSV header saved as .txt) are reported, not cloned.
func parseSubmissionList(r io.Reader) submissionSource {
	var src submissionSource
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		single := !strings.ContainsAny(line, " \t,;")
		if single && looksLikeCloneURL(line) && keepVerbatim(line) {
			src.urls = append(src.urls, line)
			continue
		}
		if found := extractRepoURLs(line); len(found) > 0 {
			src.urls = append(src.urls, found...)
			continue
		}
		if single && looksLikeCloneURL(line) {
			src.urls = append(src.urls, line)
			continue
		}
		src.missing = append(src.missing, fmt.Sprintf("line %d (%q)", n, truncate(line, 60)))
	}
	return src
}

var scpLikeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+@[A-Za-z0-9_.-]+:[^/]`)

// looksLikeCloneURL reports whether s is shaped like something git clone
// accepts: a URL with a scheme, an scp-like address, or a local path.
func looksLikeCloneURL(s string) bool {
	if strings.HasPrefix(s, "-") {
		return false
	}
	return strings.Contains(s, "://") || scpLikeRe.MatchString(s) ||
		strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~")
}

// keepVerbatim reports whether rewriting s to a bare https URL would lose its
// transport or credentials.
func keepVerbatim(s string) bool {
	if scpLikeRe.MatchString(s) {
		return true
	}
	i := strings.Index(s, "://")
	if i < 0 {
		return false
	}
	scheme := strings.ToLower(s[:i])
	if scheme != "http" && scheme != "https" {
		return true
	}
	rest := s[i+3:]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	return strings.Contains(rest, "@")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// parseSubmissionCSV scans a CSV with a header row for repo links. Devpost
// puts them in free-form columns ("Try it out" links, custom questions), so by
// default every cell is scanned; --column restricts it to one header.
func parseSubmissionCSV(r io.Reader, column string) (submissionSource, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	header, err := cr.Read()
	if err != nil {
		return submissionSource{}, fmt.Errorf("csv_invalid: %w", err)
	}
	col := -1
	if column != "" {
		for i, h := range header {
			if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(column)) {
				col = i
				break
			}
		}
		if col < 0 {
			return submissionSource{}, fmt.Errorf("csv_column_not_found: no column %q (have: %s)", column, strings.Join(header, ", "))
		}
	}
	titleCol := -1
	for i, h := range header {
		if h := strings.ToLower(strings.TrimSpace(h)); h == "project title" || h == "title" || h == "project name" {
			titleCol = i
			break
		}
	}

	var src submissionSource
	for row := 2; ; row++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return submissionSource{}, fmt.Errorf("csv_invalid: row %d: %w", row, err)
		}
		if isBlankRecord(rec) {
			continue
		}
		var found []string
		if col >= 0 {
			if col < len(rec) {
				found = extractRepoURLs(rec[col])
			}
		} else {
			for _, cell := range rec {
				found = append(found, extractRepoURLs(cell)...)
			}
		}
		if len(found) == 0 {
			label := fmt.Sprintf("row %d", row)
			if titleCol >= 0 && titleCol < len(rec) && strings.TrimSpace(rec[titleCol]) != "" {
				label = fmt.Sprintf("%q (row %d)", strings.TrimSpace(rec[titleCol]), row)
			}
			src.missing = append(src.missing, label)
			continue
		}
		src.urls = append(src.urls, found...)
	}
	return src, nil
}

func isBlankRecord(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

var repoLinkRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9.-])(?:https?://)?(?:www\.)?(github\.com|gitlab\.com|codeberg\.org|bitbucket\.org)/([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+)`)

// Owner segments that are GitHub/GitLab site pages, not repositories.
var nonRepoOwners = map[string]bool{
	"orgs": true, "features": true, "settings": true, "sponsors": true,
	"marketplace": true, "topics": true, "collections": true, "apps": true,
	"login": true, "explore": true, "users": true, "about": true,
}

// extractRepoURLs pulls canonical https clone URLs out of free text, dropping
// deep links like /tree/main or /blob/... and .git. GitHub, Codeberg, and
// Bitbucket repos are always owner/repo. GitLab allows nested subgroups
// (group/sub/repo), so there every segment up to the /-/ deep-link marker is
// kept.
func extractRepoURLs(text string) []string {
	var urls []string
	for _, m := range repoLinkRe.FindAllStringSubmatch(text, -1) {
		host := strings.ToLower(m[1])
		segs := strings.Split(m[2], "/")
		if host == "gitlab.com" {
			for i, seg := range segs {
				// "-" is GitLab's deep-link marker (/-/tree/main); old-style
				// links use /tree/ or /blob/ directly.
				if seg == "-" || seg == "tree" || seg == "blob" {
					segs = segs[:i]
					break
				}
			}
		} else {
			segs = segs[:2]
		}
		if len(segs) < 2 || nonRepoOwners[strings.ToLower(segs[0])] {
			continue
		}
		last := strings.TrimSuffix(strings.TrimRight(segs[len(segs)-1], "."), ".git")
		if last == "" {
			continue
		}
		segs[len(segs)-1] = last
		urls = append(urls, "https://"+host+"/"+strings.Join(segs, "/"))
	}
	return urls
}

// dedupeURLs keeps the first occurrence of each repo, case-insensitively,
// since GitHub owner/repo paths are case-insensitive.
func dedupeURLs(urls []string) []string {
	seen := make(map[string]bool, len(urls))
	var out []string
	for _, u := range urls {
		k := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(u), "/"), ".git"))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(u))
	}
	return out
}
