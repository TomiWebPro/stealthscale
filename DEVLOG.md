# DEVLOG

Append-only log of automated agent work on this repo. One entry per agent
run, **newest last**. Never edit or reorder older entries — append only.

Format:

```
## <YYYY-MM-DD HH:MM UTC> — <job name>
- what was done / found / committed (with short hashes) / issues filed or closed
```

## 2026-10-10 00:50 UTC — ss-worker

- picked up P0 issue `202610082015-go127-json-format-tag-sshaction` (Go 1.27
  encoding/json rejects `format:nano` tag in `tailcfg.SSHAction`)
- applied Route A: pinned `GOTOOLCHAIN=go1.26.7` in Makefile build/test targets
- refactored `SSHActionHandler` (noise.go) to marshal JSON into a buffer before
  writing HTTP headers — now returns 500 on encode failure instead of 200 empty body
- moved issue to closed/
- commit: `0f4a8fe`
- remaining open issues: `202610092238-network-emulation-gaps.md` (P2),
  `202610092238-webui-coordinator-election.md` (P2)
