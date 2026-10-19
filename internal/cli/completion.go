package cli

import (
	"fmt"
	"strings"
)

// Shells lists supported completion shells.
var Shells = []string{"bash", "zsh", "fish"}

func (a *App) allFlags(c Command) []Flag { return append(append([]Flag{}, GlobalFlags...), c.Flags...) }

func flagWords(fl []Flag) string {
	var w []string
	for _, f := range fl {
		w = append(w, "--"+f.Name)
		if f.Short != "" {
			w = append(w, "-"+f.Short)
		}
	}
	return strings.Join(w, " ")
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Completion returns the completion script for shell ("bash", "zsh" or "fish").
func (a *App) Completion(shell string) (string, error) {
	switch shell {
	case "bash":
		return a.bash(), nil
	case "zsh":
		return a.zsh(), nil
	case "fish":
		return a.fish(), nil
	}
	return "", fmt.Errorf("unknown shell %q (use bash, zsh or fish)", shell)
}

func (a *App) bash() string {
	var b strings.Builder
	b.WriteString("# bash completion for nod; load with: source <(nod completion bash)\n_nod() {\n")
	b.WriteString("  local cur prev cmd i\n  COMPREPLY=()\n  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n  prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n  cmd=\"\"\n")
	b.WriteString("  for ((i=1; i<COMP_CWORD; i++)); do\n    case \"${COMP_WORDS[i]}\" in\n      --config) ((i++)) ;;\n      -*) ;;\n      *) cmd=\"${COMP_WORDS[i]}\"; break ;;\n    esac\n  done\n")
	fmt.Fprintf(&b, "  if [[ -z \"$cmd\" ]]; then\n    COMPREPLY=($(compgen -W %s -- \"$cur\"))\n    return\n  fi\n", shq(strings.Join(append(a.Names(), "help"), " ")+" "+flagWords(GlobalFlags)))
	b.WriteString("  case \"$cmd:$prev\" in\n")
	for _, n := range a.order {
		for _, f := range a.allFlags(a.cmds[n]) {
			if f.Kind == String && len(f.Choices) > 0 {
				fmt.Fprintf(&b, "    %s:--%s) COMPREPLY=($(compgen -W %s -- \"$cur\")); return ;;\n", n, f.Name, shq(strings.Join(f.Choices, " ")))
			}
		}
	}
	b.WriteString("  esac\n  case \"$cmd\" in\n")
	for _, n := range a.order {
		c := a.cmds[n]
		words := strings.Join(c.Subs, " ")
		fmt.Fprintf(&b, "    %s)\n      if [[ \"$cur\" == -* ]]; then COMPREPLY=($(compgen -W %s -- \"$cur\")); else COMPREPLY=($(compgen -W %s -- \"$cur\")); fi ;;\n",
			n, shq(flagWords(a.allFlags(c))), shq(words))
	}
	b.WriteString("  esac\n}\ncomplete -o default -F _nod nod\n")
	return b.String()
}

func (a *App) zsh() string {
	var b strings.Builder
	b.WriteString("#compdef nod\n# zsh completion for nod; load with: source <(nod completion zsh)\n_nod() {\n  local -a cmds\n  cmds=(\n")
	for _, n := range a.order {
		fmt.Fprintf(&b, "    %s\n", shq(n+":"+strings.ReplaceAll(a.cmds[n].Summary, ":", " -")))
	}
	b.WriteString("  )\n  if (( CURRENT == 2 )); then\n    _describe 'command' cmds\n    return\n  fi\n  case ${words[2]} in\n")
	for _, n := range a.order {
		c := a.cmds[n]
		fmt.Fprintf(&b, "    %s)\n", n)
		for _, f := range a.allFlags(c) {
			if f.Kind == String && len(f.Choices) > 0 {
				fmt.Fprintf(&b, "      if [[ ${words[CURRENT-1]} == --%s ]]; then compadd %s; return; fi\n", f.Name, strings.Join(f.Choices, " "))
			}
		}
		if len(c.Subs) > 0 {
			fmt.Fprintf(&b, "      if [[ ${words[CURRENT]} != -* ]]; then compadd %s; return; fi\n", strings.Join(c.Subs, " "))
		}
		fmt.Fprintf(&b, "      compadd -- %s ;;\n", flagWords(a.allFlags(c)))
	}
	b.WriteString("  esac\n}\ncompdef _nod nod\n")
	return b.String()
}

func (a *App) fish() string {
	var b strings.Builder
	b.WriteString("# fish completion for nod; load with: nod completion fish | source\ncomplete -c nod -f\n")
	for _, n := range a.order {
		fmt.Fprintf(&b, "complete -c nod -n '__fish_use_subcommand' -a %s -d %s\n", n, shq(a.cmds[n].Summary))
	}
	for _, f := range GlobalFlags {
		b.WriteString(fishFlag("", f))
	}
	for _, n := range a.order {
		c := a.cmds[n]
		cond := "__fish_seen_subcommand_from " + n
		for _, s := range c.Subs {
			fmt.Fprintf(&b, "complete -c nod -n '%s' -a %s\n", cond, s)
		}
		for _, f := range c.Flags {
			b.WriteString(fishFlag(cond, f))
		}
	}
	return b.String()
}

func fishFlag(cond string, f Flag) string {
	s := "complete -c nod"
	if cond != "" {
		s += " -n '" + cond + "'"
	}
	s += " -l " + f.Name
	if f.Short != "" {
		s += " -s " + f.Short
	}
	if f.Kind != Bool {
		s += " -x"
		if len(f.Choices) > 0 {
			s += " -a " + shq(strings.Join(f.Choices, " "))
		}
	}
	return s + " -d " + shq(f.Usage) + "\n"
}
