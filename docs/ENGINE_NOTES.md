# Engine port notes

The deterministic engine packages port `tiers.py`, `rules.py`, `pipeline.py`, and the prompt/conversation helpers in `guard.py`. Python regular expressions that require lookarounds are handled by boundary checks or shell-specific code; the tier lexer remains conservative and returns `?` for unmodeled constructs.

## Golden coverage

`testdata/golden/rules_tiers.jsonl` contains synthetic command/cwd cases with both `rules_default` and `rules_all_ask` expectations; the Go rules tests check both. Pipeline, prompt, conversation, and short-reply behavior are compared against `pipeline.json`, `prompts.json`, `prompt_consts.json`, and `short_reply.json`. The golden inputs are generated from synthetic commands and conversations only.

Probabilities are not classified by this deterministic pipeline; when supplied as answer fields, the pipeline applies the same numeric thresholds without rounding.
