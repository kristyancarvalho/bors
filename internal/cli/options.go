package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	size "github.com/kristyancarvalho/bors/internal/render/size"
)

var ErrHelp = errors.New("help requested")

type options struct {
	path       string
	fallback   bool
	kitty      bool
	version    bool
	help       bool
	size       size.Limits
	resolution int
	color      string
}

func parseOptions(arguments []string) (options, error) {
	var parsed options
	var sizeValue string
	flags := flag.NewFlagSet("bors", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&parsed.help, "h", false, "Print help and exit")
	flags.BoolVar(&parsed.help, "help", false, "Print help and exit")
	flags.BoolVar(&parsed.version, "version", false, "Print version and exit")
	flags.BoolVar(&parsed.fallback, "f", false, "Force ANSI fallback")
	flags.BoolVar(&parsed.fallback, "fallback", false, "Force ANSI fallback")
	flags.BoolVar(&parsed.kitty, "kitty", false, "Force Kitty Graphics Protocol")
	flags.StringVar(&sizeValue, "s", "", "Maximum dimensions in terminal cells")
	flags.StringVar(&sizeValue, "size", "", "Maximum dimensions in terminal cells")
	flags.IntVar(&parsed.resolution, "r", 0, "Fallback resolution: 1 or 2")
	flags.IntVar(&parsed.resolution, "resolution", 0, "Fallback resolution: 1 or 2")
	flags.StringVar(&parsed.color, "color", "auto", "Fallback color mode: auto, truecolor, or 256")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, ErrHelp
		}
		return options{}, err
	}
	if parsed.help {
		return options{}, ErrHelp
	}
	if parsed.fallback && parsed.kitty {
		return options{}, errors.New("--fallback and --kitty cannot be used together")
	}
	if parsed.resolution != 0 && parsed.resolution != 1 && parsed.resolution != 2 {
		return options{}, errors.New("--resolution must be 1 or 2")
	}
	if parsed.color != "auto" && parsed.color != "truecolor" && parsed.color != "256" {
		return options{}, errors.New("--color must be auto, truecolor, or 256")
	}
	sizeSpecified := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "size" || current.Name == "s" {
			sizeSpecified = true
		}
	})
	if sizeSpecified {
		limits, err := size.Parse(sizeValue)
		if err != nil {
			return options{}, fmt.Errorf("--size: %w", err)
		}
		parsed.size = limits
	}
	if parsed.version {
		return parsed, nil
	}
	remaining := flags.Args()
	if len(remaining) != 1 {
		return options{}, errors.New("provide exactly one image path or '-' for stdin")
	}
	parsed.path = remaining[0]
	return parsed, nil
}

func help(output io.Writer) error {
	_, err := fmt.Fprint(output, `Usage: bors [flags] IMAGE

Display an image in the terminal using Kitty Graphics Protocol or ANSI blocks.
Use IMAGE "-" to read an image from stdin and -- before a path beginning with '-'.

Flags:
  -h, --help             Print this help
      --version          Print the build version
  -f, --fallback         Force ANSI fallback
      --kitty            Force Kitty Graphics Protocol
  -s, --size SIZE        Maximum dimensions in cells: WIDTHxHEIGHT, WIDTH, or xHEIGHT
  -r, --resolution 1|2  Fallback resolution
      --color MODE       Fallback color mode: auto, truecolor, or 256

Examples:
  bors ~/Pictures/photo.png
  bors -s 80x24 ./screenshot.jpg
  bors --fallback -s 80x24 ./photo.png
  cat photo.png | bors -
`)
	return err
}
