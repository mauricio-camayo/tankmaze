package db

import "testing"

func TestLatestMajorVersion(t *testing.T) {
	cases := []struct {
		name     string
		versions []TankVersion
		want     string
		wantOK   bool
	}{
		{
			name:     "no versions",
			versions: nil,
			want:     "",
			wantOK:   false,
		},
		{
			name: "only minor versions — no major to report",
			versions: []TankVersion{
				{Version: "v1.1", VersionType: "minor"},
				{Version: "v1.2", VersionType: "minor"},
			},
			want:   "",
			wantOK: false,
		},
		{
			name: "single major version",
			versions: []TankVersion{
				{Version: "v1", VersionType: "major"},
			},
			want:   "v1",
			wantOK: true,
		},
		{
			name: "picks highest of several majors, ignoring minors and order",
			versions: []TankVersion{
				{Version: "v0.1", VersionType: "minor"},
				{Version: "v2", VersionType: "major"},
				{Version: "v1", VersionType: "major"},
				{Version: "v3.1", VersionType: "minor"},
				{Version: "v4", VersionType: "major"},
				{Version: "v3", VersionType: "major"},
			},
			want:   "v4",
			wantOK: true,
		},
		{
			name: "double-digit major sorts numerically, not lexically",
			versions: []TankVersion{
				{Version: "v9", VersionType: "major"},
				{Version: "v10", VersionType: "major"},
			},
			want:   "v10",
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LatestMajorVersion(tc.versions)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("LatestMajorVersion(%+v) = (%q, %v), want (%q, %v)", tc.versions, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
