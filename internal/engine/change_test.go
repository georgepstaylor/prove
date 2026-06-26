package engine

import "testing"

func TestAnalyzeChangeSemver(t *testing.T) {
	cases := []struct {
		name  string
		patch string
		want  change
	}{
		{"go.mod patch bump", "@@ -3 +3 @@\n-\tgithub.com/foo/bar v1.2.3\n+\tgithub.com/foo/bar v1.2.4", changePatch},
		{"go.mod minor bump", "-\tgithub.com/foo/bar v1.2.3\n+\tgithub.com/foo/bar v1.3.0", changeMinor},
		{"go.mod major bump", "-\tgithub.com/foo/bar v1.2.3\n+\tgithub.com/foo/bar v2.0.0", changeMajor},
		{"package.json patch", "-    \"lodash\": \"4.17.20\",\n+    \"lodash\": \"4.17.21\",", changePatch},
		{"multiple bump levels are all recorded", "-\ta v1.0.0\n+\ta v1.0.1\n-\tb v1.0.0\n+\tb v2.0.0", changePatch | changeMajor},
		{
			"go.sum hash churn alongside bump is explained",
			"-github.com/foo/bar v1.0.0 h1:" + b64("A") + "\n" +
				"-github.com/foo/bar v1.0.0/go.mod h1:" + b64("B") + "\n" +
				"+github.com/foo/bar v1.0.1 h1:" + b64("C") + "\n" +
				"+github.com/foo/bar v1.0.1/go.mod h1:" + b64("D"),
			changePatch,
		},
		{"indirect marker dropped alongside bump is explained", "-\tgithub.com/foo/bar v1.0.0 // indirect\n+\tgithub.com/foo/bar v1.0.1", changePatch},
		// Security: a non-version edit anywhere in the file makes it unexplained.
		{"non-version change is unexplained", "-// old\n+// new", changeUnknown},
		{"added-only new dependency is unexplained", "+\tgithub.com/evil/backdoor v1.0.0", changeUnknown},
		{"different deps do not pair", "-\tfoo/aaa v1.0.0\n+\tfoo/bbb v1.0.0", changeUnknown},
		// #2 decoy: a real major hidden by a remainder change + a decoy patch must
		// surface the hidden line as changeUnknown so the file fails closed.
		{
			"hidden major plus decoy patch is unexplained",
			"-\tgithub.com/x v1.0.0 // indirect was-direct\n+\tgithub.com/x v2.0.0\n-\tgithub.com/y v3.0.0\n+\tgithub.com/y v3.0.1",
			changePatch | changeUnknown,
		},
		{"downgrade is not a forward bump", "-\tfoo v2.0.0\n+\tfoo v1.0.0", changeUnknown},
		{"empty patch has no change", "", 0},
		// #7: prerelease promotion and pseudo-version advance are forward patches.
		{"prerelease to release is a patch", "-\tfoo v1.0.0-rc.1\n+\tfoo v1.0.0", changePatch},
		{"pseudo-version advance is a patch", "-\tfoo v0.0.0-20231006140011-7918f672742d\n+\tfoo v0.0.0-20231120120000-abcdef012345", changePatch},
		// #8: a second version token on the line is paired, not ignored.
		{"trailing comment version unchanged, dep bumps", "-\tfoo v1.2.3 // see v0.9.0\n+\tfoo v1.2.4 // see v0.9.0", changePatch},
		{"a second, unrelated change is unexplained", "-\tfoo v1.2.3 extra\n+\tfoo v1.2.4 other", changeUnknown},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := analyzeChange(tt.patch); got != tt.want {
				t.Errorf("analyzeChange = %05b, want %05b", got, tt.want)
			}
		})
	}
}

func TestAnalyzeChangeSHA(t *testing.T) {
	cases := []struct {
		name  string
		patch string
		want  change
	}{
		{
			"pinned action SHA bumped",
			"-      uses: actions/checkout@a81bbbf8298c0fa03ea29cdc473d45769f953675\n+      uses: actions/checkout@b4ffde65f46336ab88eb53be808477a3936bae11",
			changeSHA,
		},
		{
			"pinned action SHA with trailing version comment",
			"-      uses: actions/checkout@a81bbbf8298c0fa03ea29cdc473d45769f953675 # v3.5.0\n+      uses: actions/checkout@b4ffde65f46336ab88eb53be808477a3936bae11 # v4.0.0",
			changeSHA,
		},
		{
			"docker image digest bumped",
			"-FROM alpine@sha256:" + hex64("a") + "\n+FROM alpine@sha256:" + hex64("b"),
			changeSHA,
		},
		{
			"bare 64-hex sha256 digest (no prefix) bumped",
			"-  digest: " + hex64("a") + "\n+  digest: " + hex64("b"),
			changeSHA,
		},
		{
			"version bump is not a SHA change",
			"-\tfoo v1.0.0\n+\tfoo v1.0.1",
			changePatch,
		},
		{
			"different refs do not pair",
			"-      uses: actions/checkout@a81bbbf8298c0fa03ea29cdc473d45769f953675\n+      uses: actions/setup-go@b4ffde65f46336ab88eb53be808477a3936bae11",
			changeUnknown,
		},
		// The text after a SHA must match too (apart from a version comment) —
		// arbitrary trailing content is not a clean pin.
		{
			"sha swap with changed non-comment suffix is unexplained",
			"-tool@" + hex64("a") + " ; old\n+tool@" + hex64("b") + " ; new",
			changeUnknown,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := analyzeChange(tt.patch); got != tt.want {
				t.Errorf("analyzeChange = %05b, want %05b", got, tt.want)
			}
		})
	}
}

// TestAnalyzeChangeContentDashes guards against #5: a removed content line whose
// text begins with "--" must be analysed as content, not mistaken for a header.
func TestAnalyzeChangeContentDashes(t *testing.T) {
	if got := analyzeChange("--- a heading line removed\n+something else"); got&changeUnknown == 0 {
		t.Errorf("a removed '--' content line should be analysed (unexplained), got %05b", got)
	}
}

// TestAnalyzeChangeBound guards against #4: an oversized patch is treated as
// unexplained rather than running the quadratic pairing.
func TestAnalyzeChangeBound(t *testing.T) {
	var b []byte
	for i := 0; i <= maxPatchLines; i++ {
		b = append(b, "-\tfoo v1.0.0\n"...)
	}
	if got := analyzeChange(string(b)); got != changeUnknown {
		t.Errorf("oversized patch should be changeUnknown, got %05b", got)
	}
}

// hex64 returns a 64-char hex string of a repeated digit/letter for digest tests.
func hex64(c string) string {
	s := ""
	for i := 0; i < 64; i++ {
		s += c
	}
	return s
}

// b64 returns a short base64-ish go.sum hash value built from a repeated char.
func b64(c string) string {
	s := ""
	for i := 0; i < 43; i++ {
		s += c
	}
	return s + "="
}
