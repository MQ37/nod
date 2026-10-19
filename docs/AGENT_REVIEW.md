# Auto review with the agent's own model

No GPU, no download, no local model? Let the model your agent already runs do the review:

```sh
nod install --model agent --host hermes        # or: --host opencode
```

For Hermes and OpenCode the plugin runs inside the agent, so it can send one extra, separate request to **the same model the agent is using** (say, Opus in Hermes) instead of the Decider. Nod keeps the part that has to be consistent: its server builds the review prompt, the plugin asks the model, and Nod's pipeline turns the answers into allow, ask or block, with the same questions and the same consent rules as the Decider. There is no local model and no llama.cpp; `nod serve --model agent` is a tiny server that only builds prompts and applies the pipeline.

## What to know before you turn it on

- **It costs one extra request per reviewed call**, on the agent's own model and account, and adds its latency. Obviously read-only calls are not reviewed. With a large model that is real money; the Decider on a small local model is free after setup.
- **The reviewer sees only what the Decider sees:** the tool call (secrets redacted and clipped), the working directory, your own messages, and the assistant message each of them replies to. Never tool output or file contents. The review prompt tells the model that all of it is data to judge, not instructions.
- **A chat model answers with one choice, not probabilities.** A call you explicitly approved is allowed only if it is not in a block-class category; for those, Nod still asks, where the Decider can allow a clearly approved call. A model reviewing its own session is also a weaker check than an independent one: if you want a second opinion that the agent cannot influence, use a Decider.
- **Failure means ask**, never allow: an unreachable Nod server, a model error or timeout, or an unparsable answer.
- **Claude Code and Pi are not supported in this mode.** Their hooks run outside the agent and cannot reach its model; use a Decider model for them.
- OpenCode has no way for a plugin to raise a prompt, so "ask" tells the agent to get your explicit approval in plain words, and the next review sees it.

Per-host details (Hermes timeouts, the OpenCode child session, cost and failure handling per host): [HOSTS.md](HOSTS.md#agent-review-nod-install---model-agent) and [HOSTS.md](HOSTS.md#opencode).
