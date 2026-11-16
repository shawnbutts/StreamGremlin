package updates

import (
	"regexp"
	"strconv"
	"strings"
)

// Version is a release version: MAJOR.MINOR.PATCH with an optional
// pre-release, like 0.1.0-beta.5 (Semantic Versioning 2.0.0, without
// build metadata, which releases don't use).
type Version struct {
	Major, Minor, Patch int
	Pre                 []string // "beta", "5"; empty for a full release
	text                string
}

var versionRE = regexp.MustCompile(`^(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// ParseVersion reads a version, without a leading "v".
func ParseVersion(s string) (Version, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	v := Version{text: s}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
		for _, id := range v.Pre {
			// A numeric identifier has no leading zeros (SemVer item 9).
			if len(id) > 1 && id[0] == '0' && isDigits(id) {
				return Version{}, false
			}
		}
	}
	return v, true
}

func (v Version) String() string { return v.text }

// Prerelease reports whether v is a pre-release, like a beta.
func (v Version) Prerelease() bool { return len(v.Pre) > 0 }

// Compare is -1, 0 or +1 as v is older than, the same as or newer than w,
// by SemVer's precedence: 0.1.0-beta.10 is newer than 0.1.0-beta.9, and
// 0.1.0 is newer than any 0.1.0 pre-release.
func (v Version) Compare(w Version) int {
	for _, d := range [][2]int{{v.Major, w.Major}, {v.Minor, w.Minor}, {v.Patch, w.Patch}} {
		if c := cmpInt(d[0], d[1]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.Pre) == 0 && len(w.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(w.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(w.Pre); i++ {
		a, b := v.Pre[i], w.Pre[i]
		an, bn := isDigits(a), isDigits(b)
		switch {
		case an && bn:
			x, _ := strconv.Atoi(a)
			y, _ := strconv.Atoi(b)
			if c := cmpInt(x, y); c != 0 {
				return c
			}
		case an:
			return -1
		case bn:
			return 1
		default:
			if c := strings.Compare(a, b); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(v.Pre), len(w.Pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func isDigits(s string) bool {
	if s == "" || len(s) > 9 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
