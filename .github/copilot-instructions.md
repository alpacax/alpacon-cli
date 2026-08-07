# GitHub Copilot instructions

The repository's instructions live in `AGENTS.md` (a symlink to `CLAUDE.md`) and
apply to every agent, Copilot included. Read that file—CLI usage string
convention, Cobra command conventions, error handling, exit codes, and writing
conventions are all there.

This file used to carry its own copy of those rules. It drifted from the code it
described, so it is now a pointer rather than a duplicate: fix conventions in
`CLAUDE.md` and every tool sees the fix.
