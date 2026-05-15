# Third-party notices

atlas is distributed as a single static Go binary. It links the
following third-party libraries; each ships under a permissive
license compatible with redistribution.

This file records the **direct** dependencies declared in `go.mod`.
Transitive dependencies are pinned in `go.sum`; their licenses are
listed in their respective source repositories.

## Direct dependencies

| Module                                       | Version  | License        |
| -------------------------------------------- | -------- | -------------- |
| github.com/BurntSushi/toml                   | v1.6.0   | MIT            |
| github.com/atotto/clipboard                  | v0.1.4   | BSD-3-Clause   |
| github.com/charmbracelet/bubbles             | v1.0.0   | MIT            |
| github.com/charmbracelet/bubbletea           | v1.3.10  | MIT            |
| github.com/charmbracelet/lipgloss            | v1.1.0   | MIT            |
| github.com/charmbracelet/x/term              | v0.2.2   | MIT            |
| github.com/sahilm/fuzzy                      | v0.1.1   | MIT            |
| github.com/spf13/cobra                       | v1.10.2  | Apache-2.0     |

## Notable transitive dependencies

These ship in the binary via the direct deps above:

| Module                                       | License      |
| -------------------------------------------- | ------------ |
| github.com/charmbracelet/x/* (ansi, cellbuf, colorprofile, harmonica) | MIT |
| github.com/aymanbagabas/go-osc52/v2          | MIT          |
| github.com/erikgeiser/coninput               | MIT          |
| github.com/inconshreveable/mousetrap         | Apache-2.0   |
| github.com/lucasb-eyer/go-colorful           | MIT          |
| github.com/mattn/go-isatty                   | MIT          |
| github.com/mattn/go-localereader             | MIT          |
| github.com/mattn/go-runewidth                | MIT          |
| github.com/muesli/ansi                       | MIT          |
| github.com/muesli/cancelreader               | MIT          |
| github.com/muesli/termenv                    | MIT          |
| github.com/rivo/uniseg                       | MIT          |
| github.com/spf13/pflag                       | BSD-3-Clause |
| github.com/xo/terminfo                       | MIT          |
| golang.org/x/sys                             | BSD-3-Clause |
| golang.org/x/text                            | BSD-3-Clause |

## Regenerating this file

The list above is maintained by hand. To audit the full transitive
set:

```
go list -m -json all
```

Each module's source repository contains its `LICENSE` file with the
authoritative copyright notice.

## License compatibility

atlas is released under the [MIT License](LICENSE). All direct and
listed transitive dependencies use MIT, BSD, or Apache-2.0 licenses,
which are mutually compatible for redistribution under MIT terms.
