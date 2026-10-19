package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTripAndAddIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{InstalledAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Binary: "/usr/bin/nod", ServiceName: "nod-sandbox"}
	s.Add(Record{Kind: KindFile, Path: "/tmp/config", Created: true, Backup: "/tmp/config.original.bak"})
	s.Add(Record{Kind: KindFile, Path: "/tmp/config", Created: false, Backup: "/tmp/config.reinstall.bak"})
	if len(s.Records) != 1 || !s.Records[0].Created || s.Records[0].Backup != "/tmp/config.original.bak" {
		t.Fatalf("records=%+v", s.Records)
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Binary != s.Binary || got.ServiceName != "nod-sandbox" || len(got.Records) != 1 || !got.Records[0].Created || got.Records[0].Backup != "/tmp/config.original.bak" {
		t.Fatalf("state=%+v", got)
	}
}
