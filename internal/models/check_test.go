package models

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func statuses(rs []Result) map[string]string {
	m := map[string]string{}
	for _, r := range rs {
		m[r.Path] = r.Status
	}
	return m
}

func TestVerifyDirFastTier(t *testing.T) {
	fast, err := Lookup("fast")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rs := VerifyDir(fast, dir)
	if !Failed(rs) {
		t.Fatal("an empty directory must fail")
	}
	for _, r := range rs {
		if r.Status != "missing" {
			t.Fatalf("%s: %s", r.Path, r.Status)
		}
	}
	// A file with the wrong content is a checksum or size failure, never ok.
	write(t, dir, "tokenizer_config.json", "not the real file")
	if got := statuses(VerifyDir(fast, dir))["tokenizer_config.json"]; got != "size" && got != "checksum" {
		t.Fatalf("tampered file reported %q", got)
	}
}

func TestVerifyDirAcceptsMatchingPinnedFile(t *testing.T) {
	body := "hello"
	sum := sha256.Sum256([]byte(body))
	tier := Tier{ID: "x", Source: "hf-gguf", GGUF: "m.gguf", Files: []File{{Path: "m.gguf", Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}}
	dir := t.TempDir()
	write(t, dir, "m.gguf", body)
	if rs := VerifyDir(tier, dir); Failed(rs) || rs[0].Status != "ok" {
		t.Fatalf("%+v", rs)
	}
}

func TestVerifyDirConvertedGGUFIsUnpinnedNotFailed(t *testing.T) {
	lite, _ := Lookup("lite")
	dir := t.TempDir()
	write(t, dir, lite.GGUF, "pretend gguf")
	got := statuses(VerifyDir(lite, dir))
	if got[lite.GGUF] != "unpinned" {
		t.Fatalf("converted gguf: %q (no published hash exists, so it is reported, not failed)", got[lite.GGUF])
	}
	if got[ChatSidecarName] != "missing" {
		t.Fatalf("lite without decider_chat.json must be flagged: %v", got)
	}
}

func TestChatSidecarOnlyForLite(t *testing.T) {
	lite, _ := Lookup("lite")
	fast, _ := Lookup("fast")
	dir := t.TempDir()
	if _, err := WriteChatSidecar(fast, dir); err == nil {
		t.Fatal("fast does not use the chat layout")
	}
	p, err := WriteChatSidecar(lite, dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"layout":"chat"`) || !strings.Contains(string(b), "head_ids") {
		t.Fatalf("sidecar content: %s", b)
	}
}

func TestRecommend(t *testing.T) {
	const gib = uint64(1 << 30)
	for _, c := range []struct {
		ram  uint64
		gpu  bool
		want string
	}{{128 * gib, true, "full"}, {128 * gib, false, "lite"}, {32 * gib, true, "lite"}, {16 * gib, true, "fast"}, {8 * gib, false, "fast"}} {
		if got := Recommend(c.ram, c.gpu); got != c.want {
			t.Fatalf("Recommend(%d GiB, gpu=%v)=%s want %s", c.ram/gib, c.gpu, got, c.want)
		}
	}
}

func TestDescribeListsDownloadsAndConversion(t *testing.T) {
	full, _ := Lookup("full")
	in, err := Describe(full, "SRC", "OUT.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Files) == 0 || !strings.HasPrefix(in.Files[0].URL, "https://huggingface.co/Mapika/decider-35b-a3b/resolve/main/") {
		t.Fatalf("files: %+v", in.Files)
	}
	var patched bool
	for _, s := range in.Convert {
		if s.Name == "patch-block-count" {
			patched = true
		}
	}
	if !patched {
		t.Fatal("the 35B needs its metadata patch steps")
	}
}
