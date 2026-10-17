package model

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersionReportsWhatTheBinaryCarries(t *testing.T) {
	settings := func(pairs ...string) []debug.BuildSetting {
		out := make([]debug.BuildSetting, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, debug.BuildSetting{Key: pairs[i], Value: pairs[i+1]})
		}
		return out
	}
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"installed module", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true, "1.2.3"},
		{"pseudo-version from a checkout", &debug.BuildInfo{Main: debug.Module{Version: "v1.1.2-0.20261017134043-1e63cb91a898"}, Settings: settings("vcs.revision", "1e63cb91a89851b614e92b1da6bcc48d48937b13")}, true, "dev-1e63cb9"},
		{"pseudo-version without VCS", &debug.BuildInfo{Main: debug.Module{Version: "v1.1.2-0.20261017134043-1e63cb91a898"}}, true, "dev-1e63cb9"},
		{"dirty pseudo-version without VCS", &debug.BuildInfo{Main: debug.Module{Version: "v1.1.2-0.20261017134043-1e63cb91a898+dirty"}}, true, "dev-1e63cb9-dirty"},
		{"clean tag with a modified tree", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: settings("vcs.modified", "true")}, true, "1.2.3-dirty"},
		{"dirty tagged build", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3+dirty"}, Settings: settings("vcs.revision", "abc1234567", "vcs.modified", "true")}, true, "dev-abc1234-dirty"},
		{"local build", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: settings("vcs.revision", "1e63cb91a89851b614e92b1da6bcc48d48937b13", "vcs.modified", "false")}, true, "dev-1e63cb9"},
		{"dirty local build", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: settings("vcs.revision", "1e63cb91a89851b614e92b1da6bcc48d48937b13", "vcs.modified", "true")}, true, "dev-1e63cb9-dirty"},
		{"short revision", &debug.BuildInfo{Main: debug.Module{Version: ""}, Settings: settings("vcs.revision", "abc1234")}, true, "dev-abc1234"},
		{"no build info", nil, false, "devel"},
		{"nothing stamped", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, "devel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildVersion(tc.info, tc.ok); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}
