// Package modelscli implements `nod models`: what to fetch, how to check it, and the one small
// template file a model needs. Nod does not download or convert models; an agent or the person
// installing does that (see `nod skill print`).
package modelscli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/MQ37/nod/internal/cli"
	"github.com/MQ37/nod/internal/install"
	"github.com/MQ37/nod/internal/models"
)

func init() {
	cli.Register("models", cli.Command{
		Summary: "list the Decider models, verify a model directory, write the lite chat template",
		Usage:   "models list [--json]\nmodels verify DIR [--tier fast|lite|full]\nmodels sidecar lite DIR",
		Examples: []string{
			"nod models list --json",
			"nod models verify ~/models/nod/fast --tier fast",
			"nod models sidecar lite ~/models/nod/lite",
		},
		Subs:  []string{"list", "verify", "sidecar"},
		Flags: []cli.Flag{{Name: "tier", Kind: cli.String, Choices: []string{"fast", "lite", "full", "full-q4"}, Usage: "tier to check the directory against"}},
		Run:   runModels,
	})
}

func runModels(c *cli.Context) int {
	if len(c.Args) == 0 {
		c.Errorf("expected list, verify or sidecar")
		return cli.ExitUsage
	}
	sub, rest := c.Args[0], c.Args[1:]
	switch sub {
	case "list":
		return list(c, rest)
	case "verify":
		return verify(c, rest)
	case "sidecar":
		return sidecar(c, rest)
	}
	c.Errorf("unknown models command %q (list, verify, sidecar)", sub)
	return cli.ExitUsage
}

func list(c *cli.Context, rest []string) int {
	if len(rest) != 0 {
		c.Errorf("list takes no arguments")
		return cli.ExitUsage
	}
	all, err := models.Manifest()
	if err != nil {
		c.Errorf("%v", err)
		return cli.ExitUnavailable
	}
	rec := models.Recommend(install.DetectRAM(runtime.GOOS, nil), install.DetectGPU(runtime.GOOS, nil))
	var infos []models.Info
	for _, t := range all {
		in, err := models.Describe(t, "<DOWNLOADED-SAFETENSORS-DIR>", "<OUTPUT-DIR>/"+t.GGUF)
		if err != nil {
			c.Errorf("%v", err)
			return cli.ExitUnavailable
		}
		in.Recommended = t.ID == rec
		infos = append(infos, in)
	}
	if c.G.JSON {
		c.JSON(infos)
		return 0
	}
	for _, in := range infos {
		mark := ""
		if in.Recommended {
			mark = "  (recommended for this machine)"
		}
		c.Printf("%-8s %s%s\n", in.ID, in.Repo, mark)
		if in.GGUFGB > 0 {
			gpu := ""
			if in.NeedsGPU {
				gpu = " and a GPU"
			}
			c.Printf("         %.1f GB GGUF, plan for %.0f GB RAM%s\n", in.GGUFGB, in.MinRAMGB, gpu)
		}
		if in.Note != "" {
			c.Printf("         %s\n", in.Note)
		}
		c.Printf("         source: %s, %d files; add --json for URLs, checksums and conversion steps\n", in.Source, len(in.Files))
	}
	return 0
}

func verify(c *cli.Context, rest []string) int {
	if len(rest) != 1 {
		c.Errorf("usage: nod models verify DIR --tier fast|lite|full")
		return cli.ExitUsage
	}
	id := c.Flags.String("tier")
	if id == "" {
		c.Errorf("--tier is required")
		return cli.ExitUsage
	}
	t, err := models.Lookup(id)
	if err != nil {
		c.Errorf("%v", err)
		return cli.ExitUsage
	}
	dir := rest[0]
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		c.Errorf("%s is not a directory", dir)
		return cli.ExitUsage
	}
	rs := models.VerifyDir(t, dir)
	if c.G.JSON {
		c.JSON(rs)
	} else {
		for _, r := range rs {
			line := fmt.Sprintf("%-10s %s", r.Status, r.Path)
			if r.Detail != "" {
				line += "  " + r.Detail
			}
			c.Printf("%s\n", line)
		}
	}
	if models.Failed(rs) {
		return cli.ExitVerify
	}
	return 0
}

func sidecar(c *cli.Context, rest []string) int {
	if len(rest) != 2 {
		c.Errorf("usage: nod models sidecar lite DIR")
		return cli.ExitUsage
	}
	t, err := models.Lookup(rest[0])
	if err != nil {
		c.Errorf("%v", err)
		return cli.ExitUsage
	}
	p, err := models.WriteChatSidecar(t, filepath.Clean(rest[1]))
	if err != nil {
		c.Errorf("%v", err)
		return cli.ExitUsage
	}
	if c.G.JSON {
		c.JSON(map[string]string{"wrote": p})
	} else {
		c.Printf("wrote %s\n", p)
	}
	return 0
}
