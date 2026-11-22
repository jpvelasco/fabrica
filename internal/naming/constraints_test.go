package naming

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	constraintAccount = "123456789012"
	// longestRegion is the longest commercial AWS region code (14 characters).
	longestRegion = "ap-southeast-3"
	// headroom is the number of prefix characters reserved below the computed
	// ceiling so later suffixes (for example "-scale-out-policy") still fit.
	headroom    = 4
	maxS3Bucket = 63
)

var (
	bucketPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)
	tablePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	ssmPathPattern = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
)

type nameKind int

const (
	kindOther nameKind = iota
	kindBucket
	kindTable
	kindIAM
	kindPolicy
	kindSG
	kindAlarmOrASG
	kindLaunchTemplate
	kindSSMPath
)

// kindOf classifies a catalog key by the lowercased final segment, so the
// AWS limit that applies follows from the name's role.
func kindOf(key string) nameKind {
	last := strings.ToLower(key[strings.LastIndex(key, ".")+1:])
	switch {
	case strings.HasPrefix(last, "storetable"), strings.HasPrefix(last, "coststoretable"):
		return kindTable
	case strings.HasSuffix(last, "bucket"):
		return kindBucket
	case strings.HasSuffix(last, "role"), strings.HasSuffix(last, "profile"):
		return kindIAM
	case strings.HasSuffix(last, "policy"):
		return kindPolicy
	case strings.HasSuffix(last, "alarm"), strings.HasSuffix(last, "asg"):
		return kindAlarmOrASG
	case strings.HasSuffix(last, "launchtemplate"):
		return kindLaunchTemplate
	case strings.HasSuffix(last, "sg"):
		return kindSG
	case strings.HasSuffix(last, "ssmpath"):
		return kindSSMPath
	}
	return kindOther
}

// violation returns a description when name breaks the AWS limit for its kind,
// or the empty string when it is acceptable.
func violation(key, name string) string {
	switch kindOf(key) {
	case kindBucket:
		return bucketViolation(name)
	case kindTable:
		return patternViolation(name, tablePattern, 255, "DynamoDB table")
	case kindIAM:
		return lengthViolation(name, 64, "IAM role or profile")
	case kindPolicy:
		return lengthViolation(name, 128, "IAM policy")
	case kindSG:
		return sgViolation(name)
	case kindAlarmOrASG:
		return lengthViolation(name, 255, "alarm or Auto Scaling group")
	case kindLaunchTemplate:
		return lengthViolation(name, 128, "launch template")
	case kindSSMPath:
		return patternViolation(name, ssmPathPattern, 2048, "SSM parameter path")
	}
	return lengthViolation(name, 255, "resource")
}

func lengthViolation(name string, max int, what string) string {
	if len(name) > max {
		return what + " name " + name + " is longer than " + strconv.Itoa(max)
	}
	return ""
}

func bucketViolation(name string) string {
	if len(name) < 3 || len(name) > maxS3Bucket {
		return "S3 bucket name " + name + " must be 3-63 characters"
	}
	if !bucketPattern.MatchString(name) {
		return "S3 bucket name " + name + " must be lowercase letters, digits, and hyphens"
	}
	return ""
}

func sgViolation(name string) string {
	if strings.HasPrefix(name, "sg-") {
		return "security group name " + name + " must not start with sg-"
	}
	return lengthViolation(name, 255, "security group")
}

func patternViolation(name string, pattern *regexp.Regexp, max int, what string) string {
	if msg := lengthViolation(name, max, what); msg != "" {
		return msg
	}
	if !pattern.MatchString(name) {
		return what + " name " + name + " contains characters AWS rejects"
	}
	return ""
}

// catalogViolations lists every catalog entry that breaks its AWS limit for a
// prefix, using the longest region so the S3 bucket bound is tested.
func catalogViolations(p Prefix) []string {
	var out []string
	for key, name := range Catalog(p, constraintAccount, longestRegion) {
		if msg := violation(key, name); msg != "" {
			out = append(out, key+": "+msg)
		}
	}
	return out
}

// computeCeiling returns the longest all-letter prefix that satisfies every
// catalog limit. Violations are monotonic in length, so the first failure ends the scan.
func computeCeiling() int {
	ceiling := 0
	for n := 1; n <= maxS3Bucket; n++ {
		if len(catalogViolations(Prefix{value: strings.Repeat("a", n)})) > 0 {
			break
		}
		ceiling = n
	}
	return ceiling
}

func TestMaxLenIsComputedCeilingMinusHeadroom(t *testing.T) {
	ceiling := computeCeiling()
	if ceiling == 0 {
		t.Fatal("no prefix length satisfies the catalog limits")
	}
	if ceiling != 24 {
		t.Fatalf("computed ceiling = %d, want 24 (lore store bucket: 39 fixed characters under the 63-character S3 cap)", ceiling)
	}
	if MaxLen != ceiling-headroom {
		t.Fatalf("MaxLen = %d, want ceiling %d minus headroom %d = %d", MaxLen, ceiling, headroom, ceiling-headroom)
	}
}

func TestCatalogSatisfiesAWSLimitsAtMaxLen(t *testing.T) {
	for _, p := range []Prefix{Default, MustResolve("acme"), MustResolve(strings.Repeat("a", MaxLen))} {
		if got := catalogViolations(p); len(got) > 0 {
			t.Fatalf("prefix %q violates AWS limits: %v", p.String(), got)
		}
	}
}

func TestCatalogRejectsPrefixAboveCeiling(t *testing.T) {
	if got := catalogViolations(Prefix{value: strings.Repeat("a", 25)}); len(got) == 0 {
		t.Fatal("25-character prefix passed; the S3 bucket limit is not enforced")
	}
}

func TestViolationReportsEachLimit(t *testing.T) {
	for _, tc := range []struct {
		key  string
		name string
		want string
	}{
		{"ci.role", strings.Repeat("r", 65), "longer than 64"},
		{"iam.ssmOutputPolicy", strings.Repeat("p", 129), "longer than 128"},
		{"ci.sg", "sg-lab", "must not start with sg-"},
		{"x.sg", strings.Repeat("s", 256), "longer than 255"},
		{"x.scaleOutAlarm", strings.Repeat("a", 256), "longer than 255"},
		{"x.agentsLaunchTemplate", strings.Repeat("l", 129), "longer than 128"},
		{"lore.storeTableLocks", "bad name", "characters AWS rejects"},
		{"horde.ssmPath", "/bad path", "characters AWS rejects"},
		{"state.bucket", "Upper", "lowercase letters"},
		{"state.bucket", "ab", "3-63 characters"},
		{"lore.storeBucket", strings.Repeat("b", 64), "3-63 characters"},
		{"x.other", strings.Repeat("o", 256), "longer than 255"},
	} {
		if got := violation(tc.key, tc.name); !strings.Contains(got, tc.want) {
			t.Errorf("violation(%q, %q) = %q, want containing %q", tc.key, tc.name, got, tc.want)
		}
	}
}

func TestViolationAcceptsValidNames(t *testing.T) {
	for _, tc := range []struct{ key, name string }{
		{"state.bucket", "fabrica-state-123456789012"},
		{"ci.role", "fabrica-ci-codebuild"},
		{"ci.sg", "fabrica-ci-sg"},
		{"horde.ssmPath", "/fabrica/horde"},
		{"lore.storeTableFragments", "fabrica-lore-store-123456789012-us-west-2-fragments"},
		{"ops.ci.alarm", "fabrica-ci-unhealthy"},
	} {
		if got := violation(tc.key, tc.name); got != "" {
			t.Errorf("violation(%q, %q) = %q, want empty", tc.key, tc.name, got)
		}
	}
}
