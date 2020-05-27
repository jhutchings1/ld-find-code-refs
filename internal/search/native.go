package search

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/launchdarkly/ld-find-code-refs/internal/helpers"
	"github.com/launchdarkly/ld-find-code-refs/internal/ld"
	"github.com/launchdarkly/ld-find-code-refs/internal/validation"
	"github.com/monochromegane/go-gitignore"
	"golang.org/x/tools/godoc/util"
)

type ignore struct {
	path    string
	ignores []gitignore.IgnoreMatcher
}

func newIgnore(path string, ignoreFiles []string) ignore {
	ignores := make([]gitignore.IgnoreMatcher, 0, len(ignoreFiles))
	for _, ignoreFile := range ignoreFiles {
		i, err := gitignore.NewGitIgnore(filepath.Join(path, ignoreFile))
		if err != nil {
			continue
		}
		ignores = append(ignores, i)
	}
	return ignore{path: path, ignores: ignores}
}

func (m ignore) Match(path string, isDir bool) bool {
	for _, i := range m.ignores {
		if i.Match(path, isDir) {
			return true
		}
	}

	return false
}

type file struct {
	path  string
	lines []string
}

func (f file) linesIfMatch(projKey string, aliases []string, matchLineNum, ctxLines int, line, flagKey, delimiters string) *ld.HunkRep {
	matchedFlag := false
	aliasMatches := []string{}
	if matchHasDelimiters(line, flagKey, delimiters) {
		matchedFlag = true
	}

	for _, alias := range aliases {
		if strings.Contains(line, alias) {
			aliasMatches = append(aliasMatches, alias)
		}
	}

	if !matchedFlag && len(aliasMatches) == 0 {
		return nil
	}

	startingLineNum := matchLineNum - ctxLines
	if startingLineNum < 0 {
		startingLineNum = 0
	}

	endingLineNum := matchLineNum + ctxLines + 1
	if endingLineNum >= len(f.lines) {
		endingLineNum = len(f.lines) - 1
	}

	context := f.lines[startingLineNum:endingLineNum]

	ret := ld.HunkRep{ProjKey: projKey, FlagKey: flagKey, StartingLineNumber: startingLineNum + 1, Lines: strings.Join(context, "\n")}
	for _, alias := range aliasMatches {
		ret.Aliases = []string{alias}
	}

	return &ret
}

func matchHasDelimiters(match string, flagKey string, delimiters string) bool {
	for _, left := range delimiters {
		for _, right := range delimiters {
			if strings.Contains(match, string(left)+flagKey+string(right)) {
				return true
			}
		}
	}
	return false
}

func consolidateHunks(hunks []ld.HunkRep, ctxLines int) []ld.HunkRep {
	if ctxLines < 0 {
		return hunks
	}

	combinedHunks := make([]ld.HunkRep, 0, len(hunks))
	// Continually iterate over the slice of hunks, combining overlapping hunks
	// until a pass is made with no overlaps
	for combinedInLast := true; combinedInLast; {
		combinedInLast = false
		if len(hunks) <= 1 {
			return hunks
		}
		for i, hunk := range hunks[1:] {
			prevHunk := hunks[i]
			if prevHunk.StartingLineNumber+2*ctxLines >= hunk.StartingLineNumber {
				combinedHunks = append(combinedHunks, combineHunks(prevHunk, hunk, ctxLines))
				combinedInLast = true
			} else {
				combinedHunks = append(combinedHunks, prevHunk)
				// on the last iteration and the last hunk was not combined
				if i == len(hunks)-1 {
					combinedHunks = append(combinedHunks, hunk)
				}
			}
		}

		// Reset hunk slices for the next pass
		hunks = combinedHunks
		combinedHunks = make([]ld.HunkRep, 0, len(hunks))
	}
	return hunks
}

// combineHunks assumes the startingLineNumber of a is less than b
func combineHunks(a, b ld.HunkRep, ctxLines int) ld.HunkRep {
	aLines := strings.Split(a.Lines, "\n")
	bLines := strings.Split(b.Lines, "\n")
	combinedLines := append(aLines, bLines...)
	return ld.HunkRep{
		StartingLineNumber: a.StartingLineNumber,
		Lines:              strings.Join(helpers.Dedupe(combinedLines), "\n"),
		ProjKey:            a.ProjKey,
		FlagKey:            a.FlagKey,
		Aliases:            helpers.Dedupe(append(a.Aliases, b.Aliases...)),
	}
}

func (f file) toHunks(projKey string, aliases map[string][]string, ctxLines int, delimiters string) *ld.ReferenceHunksRep {
	hunks := []ld.HunkRep{}

	ctxLinesString := ""
	for i := 0; i <= ctxLines; i++ {
		ctxLinesString = ctxLinesString + ".*\\n?"
	}
	for flagKey := range aliases {
		hunksForFlag := []ld.HunkRep{}
		for i, line := range f.lines {
			match := f.linesIfMatch(projKey, aliases[flagKey], i, ctxLines, line, flagKey, delimiters)
			if match != nil {
				hunksForFlag = append(hunksForFlag, *match)
			}
		}

		hunks = append(hunks, hunksForFlag...)
	}

	if len(hunks) == 0 {
		return nil
	}
	return &ld.ReferenceHunksRep{Path: f.path, Hunks: hunks}
}

func SearchForRefs(projKey, workspace string, searchTerms []string, aliases map[string][]string, ctxLines int, delimiters []byte) ([]ld.ReferenceHunksRep, error) {
	ignoreFiles := []string{".gitignore", ".ignore", ".ldignore"}
	allIgnores := newIgnore(workspace, ignoreFiles)

	files := make(chan file)
	references := make(chan ld.ReferenceHunksRep)

	// Start workers to process files asynchronously
	go func() {
		w := new(sync.WaitGroup)
		for file := range files {
			file := file
			w.Add(1)
			go func() {
				reference := file.toHunks(projKey, aliases, ctxLines, string(delimiters))
				if reference != nil {
					references <- *reference
				}
				w.Done()
			}()
		}
		w.Wait()
		close(references)
	}()

	fileWg := sync.WaitGroup{}
	readFile := func(path string, info os.FileInfo, err error) error {
		isDir := info.IsDir()
		if strings.HasPrefix(info.Name(), ".") || allIgnores.Match(path, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		} else if isDir {
			return nil
		}

		fileWg.Add(1)
		go func() error {
			defer fileWg.Done()
			lines, err := readFileLines(path)
			if err != nil {
				return err
			}

			if !util.IsText([]byte(strings.Join(lines, "\n"))) {
				return nil
			}

			files <- file{path: strings.TrimPrefix(path, workspace+"/"), lines: lines}
			return nil
		}()
		return nil
	}

	err := filepath.Walk(workspace, readFile)
	if err != nil {
		return nil, err
	}
	fileWg.Wait()
	close(files)

	ret := []ld.ReferenceHunksRep{}
	for reference := range references {
		ret = append(ret, reference)
	}
	return ret, nil
}

func readFileLines(path string) ([]string, error) {
	if !validation.FileExists(path) {
		return nil, errors.New("file does not exist")
	}

	file, err := os.Open(path)
	defer file.Close()

	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	var txtlines []string

	for scanner.Scan() {
		txtlines = append(txtlines, scanner.Text())
	}

	return txtlines, nil
}