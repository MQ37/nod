package install

import (
	"strings"
	"testing"
)

func TestRecommendTier(t *testing.T) {
	const gib = uint64(1 << 30)
	for _, tc := range []struct {
		name string
		ram  uint64
		gpu  bool
		want string
	}{
		{"small cpu", 16 * gib, false, "fast"}, {"24 ram", 24 * gib, false, "lite"}, {"32 with gpu", 32 * gib, true, "lite"}, {"64 gpu", 64 * gib, true, "full"}, {"64 cpu", 64 * gib, false, "lite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RecommendTier(tc.ram, tc.gpu); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestDetectRAMSources(t *testing.T) {
	for _, tc := range []struct {
		name, goos string
		content    []byte
		want       uint64
	}{
		{"linux meminfo", "linux", []byte("MemTotal:       16384 kB\n"), 16 << 20},
		{"darwin sysctl", "darwin", []byte("17179869184\n"), 16 << 30},
		{"unknown", "freebsd", []byte(""), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(path string) ([]byte, error) {
				if tc.goos == "darwin" && path != "sysctl:hw.memsize" {
					t.Fatalf("unexpected path %s", path)
				}
				return tc.content, nil
			}
			if got := DetectRAM(tc.goos, read); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestBuildPlanAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     Options
		wantTier string
		wantErr  bool
	}{
		{"server only", Options{Home: "/tmp/u", Addr: "127.0.0.1:11096", ServerOnly: true}, "fast", false},
		{"host wiring", Options{Home: "/tmp/u", Addr: "127.0.0.1:11096", Hosts: []string{"claude"}}, "fast", false},
		{"bad mode", Options{Mode: "maybe"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := BuildPlan(tc.opts, "linux", "amd64", 8<<30, false)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if err == nil {
				if p.Tier != tc.wantTier {
					t.Fatalf("tier=%s", p.Tier)
				}
				if tc.opts.ServerOnly && len(p.Steps) != 3 {
					t.Fatalf("steps=%v", p.Steps)
				}
				b, e := p.RenderJSON()
				if e != nil || !strings.Contains(string(b), "rollback") {
					t.Fatalf("json=%s err=%v", b, e)
				}
			}
		})
	}
}
