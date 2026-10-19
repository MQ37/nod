package skill

import "github.com/MQ37/nod/internal/cli"

func init() {
	cli.Register("skill", cli.Command{
		Summary: "print or install the agent skill (SKILL.md)",
		Usage:   "skill print | skill install [--dir DIR] [--json]",
		Examples: []string{
			"nod skill print",
			"nod skill install",
			"nod skill install --dir ~/.config/my-agent/skills/nod",
		},
		Subs: []string{"print", "install"},
		Raw:  true,
		Run: func(c *cli.Context) int {
			return Command{}.Run(c.Args, IO{Stdin: c.Stdin, Stdout: c.Stdout, Stderr: c.Stderr})
		},
	})
}
