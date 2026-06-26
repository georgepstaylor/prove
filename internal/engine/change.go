package engine

import (
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// change is the set of change kinds found in one file's patch (a bitmask). A rule
// with a semver/sha constraint approves the file only if this set is non-empty
// and a subset of what the rule permits; changeUnknown is never permitted, so any
// unrecognised line fails the file closed (human review). Detection is per file,
// keeping the constraint scoped to the file the rule matches.
type change uint8

const (
	changeUnknown change = 1 << iota // a line that is neither a clean bump nor a pin
	changePatch
	changeMinor
	changeMajor
	changeSHA
)

// slot replaces a version/SHA token when comparing two lines structurally. A
// placeholder rather than removal keeps surrounding text aligned, so "foo 1.0.0"
// and "foo" don't reduce to the same skeleton.
const slot = "\x00"

// maxPatchLines caps the per-file diff size analyzeChange pairs line by line; a
// larger diff is treated as changeUnknown, bounding the O(removed×added) cost.
const maxPatchLines = 2000

// versionToken matches a semver-like version, stopping at "/" so go.sum lines
// like "v1.2.3/go.mod" yield just the version. e.g. v1.2.3, 4.17.20, 1.0.0-rc.1.
var versionToken = regexp.MustCompile(`v?\d+\.\d+\.\d+[^\s",)/]*`)

// shaToken matches a bare 64-hex sha256, a bare 40-hex git SHA, or a sha256/512
// digest. 64-hex comes first so a full sha256 isn't truncated to 40.
var shaToken = regexp.MustCompile(`\b[0-9a-f]{64}\b|\b[0-9a-f]{40}\b|sha(?:256|512):[0-9a-f]+`)

// churnToken matches text that changes alongside a bump without being part of the
// dependency identity (go.sum "h1:" hashes, the "// indirect" marker). Only these
// fixed, payload-free strings are tolerated.
var churnToken = regexp.MustCompile(`h1:[A-Za-z0-9+/=]+|// indirect`)

// analyzeChange returns the set of change kinds in one file's patch, pairing
// removed with added lines and marking changeUnknown for anything it can't
// account for so a semver/sha rule fails closed.
func analyzeChange(patch string) change {
	if patch == "" {
		return 0
	}
	removed, added := splitPatch(patch)
	if len(removed) > maxPatchLines || len(added) > maxPatchLines {
		return changeUnknown
	}

	var c change
	consumed := make([]bool, len(added))
	for _, r := range removed {
		c |= classify(r, added, consumed)
	}
	for _, used := range consumed {
		if !used {
			c |= changeUnknown // an added line with no counterpart (e.g. a new dependency)
		}
	}
	return c
}

// classify returns the kinds for removed paired with the first unconsumed added
// line it forms a clean change with (marking it consumed), else changeUnknown.
func classify(removed string, added []string, consumed []bool) change {
	for i, a := range added {
		if consumed[i] {
			continue
		}
		if kinds, ok := classifyPair(removed, a); ok {
			consumed[i] = true
			return kinds
		}
	}
	return changeUnknown
}

// classifyPair returns the change kinds for a line pair, with ok=false if the
// lines differ in more than their version/SHA tokens (skeletons must match). A
// changed SHA makes it a pin, and version tokens on a pin line are treated as a
// tracking comment; otherwise each changed version must be a forward bump.
func classifyPair(oldLine, newLine string) (kinds change, ok bool) {
	oVer := versionToken.FindAllString(oldLine, -1)
	nVer := versionToken.FindAllString(newLine, -1)
	oSha := shaToken.FindAllString(oldLine, -1)
	nSha := shaToken.FindAllString(newLine, -1)
	if len(oVer) != len(nVer) || len(oSha) != len(nSha) {
		return 0, false
	}
	if skeleton(oldLine) != skeleton(newLine) {
		return 0, false
	}

	for i := range oSha {
		if oSha[i] != nSha[i] {
			return changeSHA, true // a pin swap; version tokens are a tracking comment
		}
	}
	for i := range oVer {
		if oVer[i] == nVer[i] {
			continue
		}
		if k := bumpLevel(oVer[i], nVer[i]); k != 0 {
			kinds |= k
		} else {
			kinds |= changeUnknown // changed, but not a forward bump
		}
	}
	return kinds, kinds != 0
}

// skeleton reduces a line to its identity form — churn removed, version/SHA
// tokens slotted, whitespace normalized — so equal skeletons differ only in those
// token values.
func skeleton(line string) string {
	s := stripChurn(line)
	s = versionToken.ReplaceAllString(s, slot)
	s = shaToken.ReplaceAllString(s, slot)
	return normalizeSpace(s)
}

func stripChurn(s string) string {
	return churnToken.ReplaceAllString(s, "")
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// splitPatch collects removed ("-") and added ("+") line content from a unified
// diff. GitHub's per-file patch has no "---"/"+++" headers, so a line like
// "--- option" is real removed content, not a header.
func splitPatch(patch string) (removed, added []string) {
	for _, line := range strings.Split(patch, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case '+':
			added = append(added, line[1:])
		case '-':
			removed = append(removed, line[1:])
		}
	}
	return removed, added
}

// bumpLevel returns the forward-bump level between two versions, or 0 if either
// is unparseable or the change isn't forward. Full semver precedence, so a
// prerelease→release promotion (1.0.0-rc.1 → 1.0.0) or pseudo-version advance is
// a patch.
func bumpLevel(oldV, newV string) change {
	from, err := semver.NewVersion(oldV)
	if err != nil {
		return 0
	}
	to, err := semver.NewVersion(newV)
	if err != nil {
		return 0
	}
	if to.Compare(from) <= 0 {
		return 0 // equal or downgrade
	}
	switch {
	case to.Major() != from.Major():
		return changeMajor
	case to.Minor() != from.Minor():
		return changeMinor
	default:
		return changePatch // patch, or a prerelease/pseudo-version advance
	}
}
