package engine

// ChangedFile is a file touched by a pull request. Previous is set when the file
// was renamed, in which case both the new and old paths must be allowed. Patch
// is the unified diff for the file (may be empty for large or binary changes),
// used to detect version bumps from the actual change rather than the PR title.
type ChangedFile struct {
	Path     string
	Previous string
	Patch    string
}

// TouchedPaths returns every path the change affects: the current path, plus the
// previous path on a rename.
func (f ChangedFile) TouchedPaths() []string {
	if f.Previous != "" && f.Previous != f.Path {
		return []string{f.Path, f.Previous}
	}
	return []string{f.Path}
}
