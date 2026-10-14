package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kristyancarvalho/bors/internal/input"
	"github.com/kristyancarvalho/bors/internal/render/ansi"
	"github.com/kristyancarvalho/bors/internal/render/kitty"
	size "github.com/kristyancarvalho/bors/internal/render/size"
	"github.com/kristyancarvalho/bors/internal/terminal"
)

type Runtime struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	InputTTY  bool
	OutputTTY bool
	GetSize   func() (terminal.Size, error)
	Probe     func() (bool, error)
	Env       []string
	Wait      func(time.Duration)
}

func Run(arguments []string, runtime Runtime, version string) int {
	parsed, err := parseOptions(arguments)
	if errors.Is(err, ErrHelp) {
		if writeErr := help(runtime.Stdout); writeErr != nil {
			return report(runtime.Stderr, writeErr)
		}
		return 0
	}
	if err != nil {
		return report(runtime.Stderr, err)
	}
	if parsed.version {
		if _, err := fmt.Fprintf(runtime.Stdout, "bors %s\n", version); err != nil {
			return report(runtime.Stderr, err)
		}
		return 0
	}
	if !runtime.OutputTTY {
		return report(runtime.Stderr, errors.New("stdout is not an interactive terminal"))
	}
	if runtime.Stdin == nil || runtime.Stdout == nil || runtime.Stderr == nil {
		return report(runtime.Stderr, errors.New("standard input, output, and error streams are required"))
	}
	imageData, imageFile, err := loadImage(parsed.path, runtime.Stdin)
	if err != nil {
		return report(runtime.Stderr, err)
	}
	if imageFile != nil {
		defer imageFile.Close()
	}
	terminalSize := terminal.Size{}
	var sizeErr error
	if runtime.GetSize != nil {
		terminalSize, sizeErr = runtime.GetSize()
	} else {
		sizeErr = errors.New("terminal size is unavailable")
	}
	if sizeErr != nil {
		if parsed.size.Width == 0 || parsed.size.Height == 0 {
			return report(runtime.Stderr, sizeErr)
		}
		terminalSize = terminal.Size{CellRatio: 0.5}
	}
	if terminalSize.CellRatio <= 0 {
		terminalSize.CellRatio = 0.5
	}
	maxWidth, maxHeight := parsed.size.Width, parsed.size.Height
	if maxWidth == 0 {
		maxWidth = terminalSize.Columns
	}
	if maxHeight == 0 {
		maxHeight = terminalSize.Rows
	}
	if maxWidth <= 0 || maxHeight <= 0 {
		return report(runtime.Stderr, errors.New("terminal dimensions are unavailable; specify both dimensions with --size"))
	}
	columns, rows, err := size.Fit(imageData.Pixels.Bounds().Dx(), imageData.Pixels.Bounds().Dy(), maxWidth, maxHeight, terminalSize.CellRatio)
	if err != nil {
		return report(runtime.Stderr, err)
	}
	useKitty, err := selectKitty(parsed, runtime)
	if err != nil {
		return report(runtime.Stderr, err)
	}
	if useKitty {
		if len(imageData.Frames) > 1 {
			imageNumber, err := kittyImageNumber()
			if err != nil {
				return report(runtime.Stderr, fmt.Errorf("create Kitty animation id: %w", err))
			}
			frames := make([]image.Image, len(imageData.Frames))
			delays := make([]time.Duration, len(imageData.Frames))
			for index, frame := range imageData.Frames {
				frames[index] = frame.Pixels
				delays[index] = frame.Delay
			}
			if err := kitty.RenderAnimation(runtime.Stdout, frames, delays, columns, rows, imageNumber, imageData.LoopCount, runtime.Wait); err != nil {
				return report(runtime.Stderr, err)
			}
			return 0
		}
		if imageData.Format == "png" && imageFile != nil {
			if err := kitty.RenderReader(runtime.Stdout, imageFile, columns, rows); err != nil {
				return report(runtime.Stderr, err)
			}
			return 0
		}
		payload, err := kittyPNG(imageData)
		if err != nil {
			return report(runtime.Stderr, err)
		}
		if err := kitty.Render(runtime.Stdout, payload, columns, rows); err != nil {
			return report(runtime.Stderr, err)
		}
		return 0
	}
	resolution := parsed.resolution
	unicodeOutput := utf8Usable(runtime.Env)
	if resolution == 0 {
		resolution = 1
		if unicodeOutput {
			resolution = 2
		}
	}
	colorMode, err := ansi.ResolveColorMode(parsed.color, runtime.Env)
	if err != nil {
		return report(runtime.Stderr, err)
	}
	if len(imageData.Frames) > 1 {
		if err := renderANSIAnimation(runtime.Stdout, imageData.Frames, columns, rows, resolution, colorMode, unicodeOutput, imageData.LoopCount, runtime.Wait); err != nil {
			return report(runtime.Stderr, err)
		}
		return 0
	}
	if err := ansi.Render(runtime.Stdout, imageData.Pixels, columns, rows, resolution, colorMode, unicodeOutput); err != nil {
		return report(runtime.Stderr, err)
	}
	return 0
}

func renderANSIAnimation(writer io.Writer, frames []input.Frame, columns, rows, resolution int, mode ansi.ColorMode, unicodeOutput bool, loopCount int, wait func(time.Duration)) error {
	if len(frames) < 2 || loopCount < -1 {
		return errors.New("ANSI animation metadata is invalid")
	}
	if wait == nil {
		wait = time.Sleep
	}
	cycles := 1
	if loopCount == 0 {
		cycles = -1
	} else if loopCount > 0 {
		cycles += loopCount
	}
	drawn := false
	previousDelay := time.Duration(0)
	for cycle := 0; cycles < 0 || cycle < cycles; cycle++ {
		for _, frame := range frames {
			if frame.Pixels == nil || frame.Delay <= 0 {
				return errors.New("ANSI animation frame is invalid")
			}
			if drawn {
				wait(previousDelay)
				if _, err := fmt.Fprintf(writer, "\x1b[%dA", rows); err != nil {
					return err
				}
			}
			if err := ansi.Render(writer, frame.Pixels, columns, rows, resolution, mode, unicodeOutput); err != nil {
				return err
			}
			drawn = true
			previousDelay = frame.Delay
		}
	}
	wait(previousDelay)
	return nil
}

func kittyImageNumber() (uint32, error) {
	var data [4]byte
	if _, err := rand.Read(data[:]); err != nil {
		return 0, err
	}
	imageNumber := binary.LittleEndian.Uint32(data[:])
	if imageNumber == 0 {
		imageNumber = 1
	}
	return imageNumber, nil
}

func selectKitty(parsed options, runtime Runtime) (bool, error) {
	if parsed.kitty {
		return true, nil
	}
	if parsed.fallback || !runtime.InputTTY || !runtime.OutputTTY {
		return false, nil
	}
	if runtime.Probe == nil {
		return false, nil
	}
	supported, err := runtime.Probe()
	if err != nil {
		if errors.Is(err, terminal.ErrRestore) {
			return false, fmt.Errorf("restore terminal after capability query: %w", err)
		}
		return false, nil
	}
	return supported, nil
}

func loadImage(path string, stdin io.Reader) (input.Image, *os.File, error) {
	if path == "-" {
		decoded, err := input.Load(stdin)
		return decoded, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return input.Image{}, nil, fmt.Errorf("open image %q: %w", path, err)
	}
	decoded, err := input.LoadFile(file)
	if err != nil {
		file.Close()
		return input.Image{}, nil, fmt.Errorf("image %q: %w", path, err)
	}
	return decoded, file, nil
}

func kittyPNG(decoded input.Image) ([]byte, error) {
	if decoded.Format == "png" {
		return decoded.PNG, nil
	}
	const maxPayload = 64 << 20
	buffer := &limitedBuffer{limit: maxPayload}
	if err := png.Encode(buffer, decoded.Pixels); err != nil {
		return nil, fmt.Errorf("encode image as PNG for Kitty: %w", err)
	}
	return buffer.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > buffer.limit-buffer.Len() {
		return 0, errors.New("converted PNG exceeds the 64 MiB payload limit")
	}
	return buffer.Buffer.Write(data)
}

func utf8Usable(environment []string) bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := environmentValue(environment, key)
		if value != "" {
			return containsUTF8(value)
		}
	}
	return false
}

func containsUTF8(value string) bool {
	normalized := strings.ToLower(value)
	return strings.Contains(normalized, "utf-8") || strings.Contains(normalized, "utf8")
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, item := range environment {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			return item[len(prefix):]
		}
	}
	return ""
}

func report(writer io.Writer, err error) int {
	if writer != nil {
		fmt.Fprintf(writer, "bors: %v\n", err)
	}
	return 2
}
