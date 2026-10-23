package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

// checkResultsFile is the editable cache written by `ttsctl check` and read by
// `ttsctl check --redo`. It lives in the current working directory.
const checkResultsFile = "check_results.txt"

const checkResultsHeader = "# file_id\tseq\ttext_hash\tsilences\n"

type checkCacheEntry struct {
	FileID   int64
	Seq      int
	TextHash string
	Details  string
}

// readCheckCache parses the cache file. A missing file is not an error. Invalid
// lines are counted and skipped so users can edit the file by hand.
func readCheckCache(path string) (entries []checkCacheEntry, exists bool, invalid int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, 0, nil
		}
		return nil, false, 0, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	exists = true
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, ok := parseCheckCacheLine(line)
		if !ok {
			invalid++
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, true, invalid, fmt.Errorf("read %s: %w", path, err)
	}
	return entries, true, invalid, nil
}

// parseCheckCacheLine accepts the canonical tab-separated format and a more
// forgiving space-separated format. The first three fields are required; any
// remaining text is kept as the human-readable silence summary.
func parseCheckCacheLine(line string) (checkCacheEntry, bool) {
	tabbed := strings.Contains(line, "\t")
	fields := strings.Split(line, "\t")
	if !tabbed || len(fields) < 3 {
		fields = strings.Fields(line)
		tabbed = false
	}
	if len(fields) < 3 {
		return checkCacheEntry{}, false
	}

	fileID, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
	if err != nil || fileID <= 0 {
		return checkCacheEntry{}, false
	}
	seq, err := strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil || seq <= 0 {
		return checkCacheEntry{}, false
	}
	hash := strings.ToLower(strings.TrimSpace(fields[2]))
	if !isHexHash(hash) {
		return checkCacheEntry{}, false
	}

	details := ""
	if len(fields) > 3 {
		if tabbed {
			details = strings.Join(fields[3:], "\t")
		} else {
			details = strings.Join(fields[3:], " ")
		}
	}
	return checkCacheEntry{
		FileID:   fileID,
		Seq:      seq,
		TextHash: hash,
		Details:  strings.TrimSpace(details),
	}, true
}

// writeCheckCache atomically replaces the cache file with the given problems.
// It returns the number of problem lines written (one per file/seq pair).
func writeCheckCache(path string, candidates []*checkCandidate) (int, error) {
	var b strings.Builder
	b.WriteString(checkResultsHeader)
	lines := 0
	for _, candidate := range candidates {
		details := checkCandidateDetail(candidate)
		details = strings.ReplaceAll(details, "\t", " ")
		details = strings.ReplaceAll(details, "\n", " ")
		for _, ref := range candidate.refs {
			fmt.Fprintf(&b, "%d\t%d\t%s\t%s\n", ref.FileID, ref.Seq, candidate.textHash, details)
			lines++
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	return lines, nil
}

func checkCandidateDetail(candidate *checkCandidate) string {
	if candidate.cachedDetail != "" {
		return candidate.cachedDetail
	}
	return strings.Join(checkDetails(candidate), "; ")
}

// cachedCheckCandidates resolves cached (file_id, seq, hash) entries against
// the current database. Stale or edited-out entries are reported and skipped.
func cachedCheckCandidates(a *app, entries []checkCacheEntry) ([]*checkCandidate, error) {
	refs, err := a.store.SpeechRefs()
	if err != nil {
		return nil, err
	}

	type fileSeq struct {
		fileID int64
		seq    int
	}
	byFileSeq := make(map[fileSeq]store.SpeechRef, len(refs))
	refsByKey := make(map[audioKey][]store.SpeechRef, len(refs))
	for _, ref := range refs {
		byFileSeq[fileSeq{fileID: ref.FileID, seq: ref.Seq}] = ref
		key := audioKey{voiceKey: ref.VoiceKey, textHash: ref.TextHash}
		refsByKey[key] = append(refsByKey[key], ref)
	}

	detailsByKey := make(map[audioKey]string)
	stale := 0
	for _, entry := range entries {
		ref, ok := byFileSeq[fileSeq{fileID: entry.FileID, seq: entry.Seq}]
		if !ok || ref.TextHash != entry.TextHash {
			stale++
			continue
		}
		key := audioKey{voiceKey: ref.VoiceKey, textHash: ref.TextHash}
		if _, exists := detailsByKey[key]; !exists {
			detailsByKey[key] = entry.Details
		}
	}
	if stale > 0 {
		a.errorf("warning: skipped %d stale cache entry/entries\n", stale)
	}

	candidates := make([]*checkCandidate, 0, len(detailsByKey))
	for key, details := range detailsByKey {
		keyRefs := refsByKey[key]
		if len(keyRefs) == 0 {
			continue
		}
		candidate := &checkCandidate{
			path:         audioPath(a.cfg.AudioDir, key.voiceKey, key.textHash),
			voiceKey:     key.voiceKey,
			textHash:     key.textHash,
			refs:         keyRefs,
			cachedDetail: details,
		}
		if info, err := wavutil.InspectFile(candidate.path); err == nil {
			candidate.duration = info.Duration
		}
		candidates = append(candidates, candidate)
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].refs[0], candidates[j].refs[0]
		if a.FileID != b.FileID {
			return a.FileID < b.FileID
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return candidates[i].textHash < candidates[j].textHash
	})
	return candidates, nil
}
