# Bors

Bors displays PNG, JPEG, and GIF images directly in a terminal. It uses the Kitty Graphics Protocol when automatic capability detection confirms support and otherwise draws ANSI-colored Unicode blocks.

## Contents

- [Build and install](#build-and-install)
- [Usage](#usage)
- [Options](#options)
- [Rendering behavior](#rendering-behavior)
- [Supported formats and limitations](#supported-formats-and-limitations)
- [Architecture](#architecture)
- [Development](#development)

## Build and install

Requires Go 1.27.2 or later.

```sh
git clone https://github.com/kristyancarvalho/bors.git
cd bors
go build -o bors ./cmd/bors
```

To install the checkout into the Go binary directory:

```sh
go install ./cmd/bors
```

## Usage

```sh
bors ~/Pictures/photo.png
bors -s 80x24 ./screenshot.jpg
bors --fallback -s 80x24 ./photo.png
bors --kitty ./photo.png
cat photo.png | bors -
```

Bors accepts one path per invocation. Use `-` for stdin and `--` before a path that begins with a dash. Standard output must be an interactive terminal so image escape sequences are not written into a file or pipe.

## Options

| Option | Behavior |
| --- | --- |
| `-h`, `--help` | Print usage and available options. |
| `--version` | Print the build version. |
| `-f`, `--fallback` | Force ANSI blocks and skip Kitty detection. |
| `--kitty` | Force Kitty Graphics Protocol output. |
| `-s`, `--size SIZE` | Maximum terminal-cell dimensions: `80x24`, `80`, or `x24`. |
| `-r`, `--resolution 1\|2` | Fallback rows per cell; defaults to 2 when the locale indicates UTF-8, otherwise 1. |
| `--color auto\|truecolor\|256` | Select ANSI colors. `auto` uses truecolor for `COLORTERM=truecolor` or `24bit`, 256 colors for a `TERM` ending in `256color`, and 16 colors when capabilities are unknown. |

The size is a maximum in terminal cells. Bors preserves the displayed image proportions using the terminal cell aspect ratio when available. A missing width or height uses the corresponding terminal dimension.

## Rendering behavior

Automatic mode first requires an interactive stdout. When stdin and stdout are terminals, Bors uses the Kitty query action and DA1 response to identify Kitty Graphics Protocol support. A positive response selects Kitty; no response, unsupported output, or non-terminal stdin selects ANSI. Forced modes do not switch renderers.

The Kitty renderer transmits PNG data directly in base64 chunks and converts JPEG/GIF input to PNG. It places the image in a cell-sized rectangle and lets the terminal advance the cursor after placement. The ANSI renderer uses upper-half blocks at resolution 2 and solid blocks at resolution 1; when the locale does not indicate UTF-8, resolution 1 uses the ASCII `#` glyph. ANSI output is an approximation: its appearance varies with fonts, color support, terminal cell geometry, and color palette. Transparent pixels are composited against black because the default terminal background cannot be queried reliably.

## Supported formats and limitations

- PNG, JPEG, and GIF are accepted. Only the first GIF frame is rendered.
- Input is limited to 64 MiB; decoded images are limited to 32,768 pixels per side and 40 million pixels total.
- Kitty output requires a terminal that supports Kitty Graphics Protocol. Bors does not infer support from `TERM` alone.
- Automatic Kitty detection is skipped when stdin is not a terminal, including when image data is piped to `bors -`.
- The ANSI renderer uses ANSI color sequences with Unicode blocks by default and ASCII `#` for resolution 1 when UTF-8 is unavailable. Its output is not pixel-identical to the source image.
- A project license has not been selected yet; see [the license issue](https://github.com/kristyancarvalho/bors/issues/3) before redistributing the project.

## Architecture

```mermaid
flowchart LR
    CLI --> Input[Image input]
    CLI --> Terminal[Terminal detection and sizing]
    CLI --> Kitty[Kitty renderer]
    CLI --> ANSI[ANSI renderer]
    Input --> Kitty
    Input --> ANSI
    Terminal --> CLI
```

## Development

```sh
gofmt -w $(rg --files -g '*.go')
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Automated protocol tests use fake terminal transports and validate emitted sequences. They do not establish compatibility with a real terminal emulator. Manual visual checks in Kitty-compatible and non-Kitty terminals remain separate from the automated test suite.
