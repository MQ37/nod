package power

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func supply(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range files {
		if err := os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSysfs(t *testing.T) {
	cases := []struct {
		name    string
		build   func(root string)
		battery bool
		plugged bool // Allowed("plugged") == nil
	}{
		{"laptop on AC with a charge limit", func(r string) {
			supply(t, r, "AC", map[string]string{"type": "Mains", "online": "1"})
			supply(t, r, "BAT0", map[string]string{"type": "Battery", "status": "Not charging"})
		}, true, true},
		{"laptop on battery", func(r string) {
			supply(t, r, "AC", map[string]string{"type": "Mains", "online": "0"})
			supply(t, r, "BAT0", map[string]string{"type": "Battery", "status": "Discharging"})
		}, true, false},
		{"usb-c charger", func(r string) {
			supply(t, r, "ucsi-source-psy-1", map[string]string{"type": "USB", "online": "1"})
			supply(t, r, "BAT0", map[string]string{"type": "Battery"})
		}, true, true},
		{"desktop, no supplies", func(r string) {}, false, true},
		{"mouse battery only is not a laptop", func(r string) {
			supply(t, r, "hid-mouse", map[string]string{"type": "Battery", "scope": "Device"})
		}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			c.build(root)
			s := ReadSysfs(root)
			if s.HasBattery != c.battery {
				t.Fatalf("HasBattery=%v want %v (%+v)", s.HasBattery, c.battery, s)
			}
			if got := Allowed("plugged", s) == nil; got != c.plugged {
				t.Fatalf("plugged policy allowed=%v want %v (%+v)", got, c.plugged, s)
			}
		})
	}
	if s := ReadSysfs(filepath.Join(t.TempDir(), "missing")); s.Known || s.OnBattery() {
		t.Fatalf("missing dir must read as unknown, not battery: %+v", s)
	}
}

func TestPmset(t *testing.T) {
	ac := "Now drawing from 'AC Power'\n -InternalBattery-0 (id=1)\t100%; charged; 0:00 remaining present: true\n"
	bat := "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t80%; discharging; 3:10 remaining present: true\n"
	if s := ReadPmset(func() (string, error) { return ac, nil }); !s.OnAC || s.OnBattery() {
		t.Fatalf("ac: %+v", s)
	}
	if s := ReadPmset(func() (string, error) { return bat, nil }); !s.OnBattery() {
		t.Fatalf("battery: %+v", s)
	}
	if s := ReadPmset(func() (string, error) { return "", errors.New("no pmset") }); s.Known {
		t.Fatalf("error must read as unknown: %+v", s)
	}
}

func TestPolicies(t *testing.T) {
	onBat := State{Known: true, HasBattery: true}
	if Allowed("always", onBat) != nil {
		t.Fatal("always must allow on battery")
	}
	if !errors.Is(Allowed("plugged", onBat), ErrOnBattery) {
		t.Fatal("plugged must refuse on battery")
	}
	if !errors.Is(Allowed("never", State{Known: true, OnAC: true}), ErrDisabled) {
		t.Fatal("never must always refuse")
	}
	if !errors.Is(Allowed("bogus", onBat), ErrOnBattery) {
		t.Fatal("unknown policy must fail towards saving power")
	}
}

func TestCachedReadsOncePerTTL(t *testing.T) {
	n, now := 0, time.Unix(0, 0)
	r := Cached(func() State { n++; return State{} }, 5*time.Second, func() time.Time { return now })
	r()
	r()
	now = now.Add(6 * time.Second)
	r()
	if n != 2 {
		t.Fatalf("reads=%d want 2", n)
	}
}
