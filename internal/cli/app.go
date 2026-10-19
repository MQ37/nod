package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Exit codes (SPEC section 1).
const (
	ExitOK          = 0
	ExitUsage       = 64 // usage error or other command failure (1 is reserved for check=ask)
	ExitBlock       = 2  // check=block
	ExitUnavailable = 3  // not installed / unreachable / check error
	ExitVerify      = 4  // checksum or hook verification failed
	ExitNeeds       = 5  // non-interactive run needs a decision from the human
)

// Globals are the flags every command accepts.
type Globals struct {
	JSON           bool
	Yes            bool
	NonInteractive bool
	Quiet          bool
	Verbose        bool
	Config         string
}

// GlobalFlags is the global flag set.
var GlobalFlags = []Flag{
	{Name: "json", Kind: Bool, Usage: "machine-readable output on stdout, human text on stderr"},
	{Name: "yes", Short: "y", Kind: Bool, Usage: "accept the plan and defaults without asking"},
	{Name: "non-interactive", Kind: Bool, Usage: "never prompt (default when stdin/stdout is not a TTY or CI is set)"},
	{Name: "quiet", Short: "q", Kind: Bool, Usage: "only print errors and results"},
	{Name: "verbose", Short: "v", Kind: Bool, Usage: "more detail (decisions: include command text if it was logged)"},
	{Name: "config", Kind: String, Arg: "PATH", Usage: "config file (default: $XDG_CONFIG_HOME/nod/config.json)"},
	{Name: "help", Short: "h", Kind: Bool, Usage: "show help"},
}

// Env is the process environment, injectable for tests.
type Env struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Getenv    func(string) string
	StdinTTY  bool
	StdoutTTY bool
}

// Context is what a command's Run receives.
type Context struct {
	Env
	Ctx   context.Context
	App   *App
	Name  string
	Flags *Parsed // nil for Raw commands
	G     Globals
	Args  []string // positionals (all remaining args for Raw commands)
	Rest  []string // args after "--"
}

// Need is one unanswered question of a non-interactive run.
type Need struct {
	Flag     string   `json:"flag"`
	Choices  []string `json:"choices,omitempty"`
	Question string   `json:"question,omitempty"`
}

// Interactive reports whether the command may prompt.
func (c *Context) Interactive() bool { return !c.G.NonInteractive }

// Out is where human text goes: stdout normally, stderr with --json.
func (c *Context) Out() io.Writer {
	if c.G.JSON {
		return c.Stderr
	}
	return c.Stdout
}

// Printf writes human text (always shown).
func (c *Context) Printf(format string, a ...any) { fmt.Fprintf(c.Out(), format, a...) }

// Infof writes human progress text unless --quiet.
func (c *Context) Infof(format string, a ...any) {
	if !c.G.Quiet {
		fmt.Fprintf(c.Out(), format, a...)
	}
}

// Errorf writes an error line to stderr, prefixed with the command name.
func (c *Context) Errorf(format string, a ...any) {
	fmt.Fprintf(c.Stderr, "nod %s: %s\n", c.Name, fmt.Sprintf(format, a...))
}

// JSON writes v as indented JSON on stdout.
func (c *Context) JSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintf(c.Stdout, "%s\n", b)
}

// Needs reports unanswered questions and returns ExitNeeds.
func (c *Context) Needs(needs []Need) int {
	if c.G.JSON {
		c.JSON(map[string]any{"needs": needs})
	}
	fmt.Fprintf(c.Stderr, "nod %s: needs a decision from you (non-interactive); pass:\n", c.Name)
	for _, n := range needs {
		line := "  " + n.Flag
		if len(n.Choices) > 0 {
			line += " " + strings.Join(n.Choices, "|")
		}
		if n.Question != "" {
			line += "   # " + n.Question
		}
		fmt.Fprintln(c.Stderr, line)
	}
	return ExitNeeds
}

// Command is one nod subcommand.
type Command struct {
	Name      string
	Summary   string
	Usage     string   // synopsis lines after "nod "
	Examples  []string // full command lines
	Flags     []Flag
	Subs      []string // positional sub-words, for help and completion
	Raw       bool     // pass all args after the name to Run unparsed (self-parsing packages)
	UsageExit int      // exit code for usage errors (default 1; check uses 3)
	Run       func(*Context) int
}

// App is a command registry plus dispatcher.
type App struct {
	cmds  map[string]Command
	order []string
}

// NewApp returns an empty app.
func NewApp() *App { return &App{cmds: map[string]Command{}} }

// Default is the process-wide registry other packages plug into via Register.
var Default = NewApp()

// Register adds a command to the Default app.
func Register(name string, c Command) { Default.Register(name, c) }

// Register adds a command; it panics on a duplicate (a programming error).
func (a *App) Register(name string, c Command) {
	if _, dup := a.cmds[name]; dup {
		panic("cli: duplicate command " + name)
	}
	c.Name = name
	a.cmds[name] = c
	a.order = append(a.order, name)
}

// Has reports whether name is registered.
func (a *App) Has(name string) bool { _, ok := a.cmds[name]; return ok }

// Names returns command names in registration order.
func (a *App) Names() []string { return append([]string{}, a.order...) }

// Command returns a registered command.
func (a *App) Command(name string) (Command, bool) { c, ok := a.cmds[name]; return c, ok }

func envNonInteractive(e Env) bool {
	return e.Getenv("CI") != "" || e.Getenv("NOD_NONINTERACTIVE") != "" || !e.StdinTTY || !e.StdoutTTY
}

func globalsFrom(p *Parsed, e Env) Globals {
	g := Globals{
		JSON: p.Bool("json"), Yes: p.Bool("yes"), NonInteractive: p.Bool("non-interactive"),
		Quiet: p.Bool("quiet"), Verbose: p.Bool("verbose"), Config: p.String("config"),
	}
	if envNonInteractive(e) {
		g.NonInteractive = true
	}
	return g
}

// Run dispatches args (without the program name) and returns the exit code.
func (a *App) Run(ctx context.Context, env Env, args []string) int {
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	// Find the command token: the first non-flag word before "--" (--config takes a value).
	idx := -1
	helpSeen := false
	for i := 0; i < len(args); i++ {
		t := args[i]
		if t == "--" {
			break
		}
		if t == "-h" || t == "--help" {
			helpSeen = true
		}
		if t == "--config" {
			i++
			continue
		}
		if strings.HasPrefix(t, "-") {
			continue
		}
		idx = i
		break
	}
	if idx < 0 {
		if helpSeen {
			a.writeUsage(env.Stdout)
			return ExitOK
		}
		a.writeUsage(env.Stderr)
		return ExitUsage
	}
	name := args[idx]
	leading, rest := args[:idx], args[idx+1:]
	if name == "help" {
		if len(rest) > 0 {
			if c, ok := a.cmds[rest[0]]; ok {
				a.writeCommandHelp(env.Stdout, c)
				return ExitOK
			}
			fmt.Fprintf(env.Stderr, "nod: unknown command %q\n", rest[0])
			return ExitUsage
		}
		a.writeUsage(env.Stdout)
		return ExitOK
	}
	cmd, ok := a.cmds[name]
	if !ok {
		fmt.Fprintf(env.Stderr, "nod: unknown command %q\n", name)
		if s := a.suggest(name); s != "" {
			fmt.Fprintf(env.Stderr, "did you mean %q?\n", s)
		}
		fmt.Fprintln(env.Stderr, "run 'nod help' for the list of commands")
		return ExitUsage
	}
	usageExit := cmd.UsageExit
	if usageExit == 0 {
		usageExit = ExitUsage
	}
	c := &Context{Env: env, Ctx: ctx, App: a, Name: name}
	if cmd.Raw {
		p, err := ParseFlags(GlobalFlags, leading)
		if err != nil {
			fmt.Fprintf(env.Stderr, "nod %s: %v\n", name, err)
			return usageExit
		}
		c.G, c.Args, c.Flags = globalsFrom(p, env), rest, p
		return cmd.Run(c)
	}
	specs := append(append([]Flag{}, GlobalFlags...), cmd.Flags...)
	all := append(append([]string{}, leading...), rest...)
	p, err := ParseFlags(specs, all)
	if err != nil {
		fmt.Fprintf(env.Stderr, "nod %s: %v\nrun 'nod %s --help' for usage\n", name, err, name)
		return usageExit
	}
	if p.Bool("help") {
		a.writeCommandHelp(env.Stdout, cmd)
		return ExitOK
	}
	c.G, c.Flags, c.Args, c.Rest = globalsFrom(p, env), p, p.Args, p.Rest
	return cmd.Run(c)
}

func (a *App) suggest(name string) string {
	best, bd := "", 3
	for _, n := range a.order {
		if d := editDistance(name, n); d < bd {
			best, bd = n, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

const exitCodesHelp = `Exit codes:
  0 ok   1 usage/other error   2 check=block   3 not installed/unreachable
  4 verification failed (checksum, hook not wired)   5 needs a decision from the human
`

func (a *App) writeUsage(w io.Writer) {
	fmt.Fprint(w, "nod gates an AI agent's tool calls (shell commands, edits, fetches) with the Decider model.\n\nUsage:\n  nod <command> [flags]\n\nCommands:\n")
	for _, n := range a.order {
		fmt.Fprintf(w, "  %-11s %s\n", n, a.cmds[n].Summary)
	}
	fmt.Fprint(w, "\nGlobal flags:\n")
	writeFlags(w, GlobalFlags)
	fmt.Fprint(w, "\nExamples:\n  nod install --dry-run --json\n  nod install --model fast --host claude --yes\n  nod check -- rm -rf /tmp/x\n\n"+exitCodesHelp+"\nRun 'nod <command> --help' for details.\n")
}

func (a *App) writeCommandHelp(w io.Writer, c Command) {
	fmt.Fprintf(w, "nod %s: %s\n\nUsage:\n", c.Name, c.Summary)
	usage := c.Usage
	if usage == "" {
		usage = c.Name + " [flags]"
	}
	for _, l := range strings.Split(usage, "\n") {
		fmt.Fprintf(w, "  nod %s\n", l)
	}
	if len(c.Flags) > 0 {
		fmt.Fprint(w, "\nFlags:\n")
		writeFlags(w, c.Flags)
	}
	fmt.Fprint(w, "\nGlobal flags:\n")
	writeFlags(w, GlobalFlags)
	if len(c.Examples) > 0 {
		fmt.Fprint(w, "\nExamples:\n")
		for _, e := range c.Examples {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	fmt.Fprint(w, "\n"+exitCodesHelp)
}

func flagLabel(f Flag) string {
	s := "--" + f.Name
	if f.Short != "" {
		s = "-" + f.Short + ", " + s
	}
	if f.Kind != Bool {
		arg := f.Arg
		if arg == "" && len(f.Choices) > 0 {
			arg = strings.Join(f.Choices, "|")
		}
		if arg == "" {
			arg = "VALUE"
		}
		s += " " + arg
	}
	return s
}

func writeFlags(w io.Writer, fl []Flag) {
	for _, f := range fl {
		u := f.Usage
		if f.Default != "" && f.Kind != Bool {
			u += " (default " + f.Default + ")"
		}
		fmt.Fprintf(w, "  %-28s %s\n", flagLabel(f), u)
	}
}
