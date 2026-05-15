# Philosophy

Why atlas is shaped the way it is. These four principles set
atlas apart from most repo dashboards; every feature decision
and code review is weighed against them.

## Observability-only

atlas surfaces what `git` and the filesystem already say
(language, dirty state, upstream divergence, stash count, branch
count, recent cadence, activity tier) and never asks you to
curate or tag anything.

## Config is minimal and optional

atlas needs to know where your projects live, but you can supply
that as a positional `[PATH]`, `--root`, or `root` in the config
— whichever fits your workflow. Every other knob has a sensible
default. atlas runs fine with no config file at all; the
onboarding prompt only fires when no path source is supplied
(and you'd like to save one for next time).

## No marker files in your repos

atlas never writes a marker file or config inside your repos. No
`.atlas.toml`, no hidden directory, no per-repo metadata. atlas
reads, never edits — your repos are exactly as you left them.
The only files atlas writes live under `~/.config/atlas` and
`~/.cache/atlas`.

## No network

atlas never makes network calls — no `git fetch`, no GitHub API,
no remote default-branch detection. Behind/ahead reflects your
last manual fetch; refresh it yourself when you want fresher
numbers.
