package ansi

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
)

type ColorMode string

const (
	TrueColor  ColorMode = "truecolor"
	Palette256 ColorMode = "256"
	Palette16  ColorMode = "16"
)

type RGB struct {
	R uint8
	G uint8
	B uint8
}

func ResolveColorMode(requested string, environment []string) (ColorMode, error) {
	switch requested {
	case "truecolor":
		return TrueColor, nil
	case "256":
		return Palette256, nil
	case "auto":
		colorterm := strings.ToLower(environmentValue(environment, "COLORTERM"))
		if colorterm == "truecolor" || colorterm == "24bit" {
			return TrueColor, nil
		}
		if strings.HasSuffix(strings.ToLower(environmentValue(environment, "TERM")), "256color") {
			return Palette256, nil
		}
		return Palette16, nil
	default:
		return "", fmt.Errorf("unsupported color mode %q", requested)
	}
}

func Render(writer io.Writer, source image.Image, columns, rows, resolution int, mode ColorMode, unicodeOutput bool) error {
	if source == nil {
		return errors.New("image is nil")
	}
	if columns <= 0 || rows <= 0 || int64(columns)*int64(rows) > 1_000_000 {
		return errors.New("ANSI dimensions must be positive and within the safety limit")
	}
	if source.Bounds().Dx() <= 0 || source.Bounds().Dy() <= 0 {
		return errors.New("image dimensions must be positive")
	}
	if resolution != 1 && resolution != 2 {
		return errors.New("ANSI resolution must be 1 or 2")
	}
	if mode != TrueColor && mode != Palette256 && mode != Palette16 {
		return errors.New("ANSI color mode is invalid")
	}
	if resolution == 2 && rows > 500_000/columns {
		return errors.New("ANSI pixel dimensions exceed the safety limit")
	}
	pixels := resize(source, columns, rows*resolution)
	buffer := bufio.NewWriter(writer)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			if resolution == 1 {
				pixel := pixels[row*columns+column]
				if err := writeForeground(buffer, pixel, mode); err != nil {
					return err
				}
				glyph := '#'
				if unicodeOutput {
					glyph = '█'
				}
				if _, err := buffer.WriteRune(glyph); err != nil {
					return err
				}
				continue
			}
			upper := pixels[(row*2)*columns+column]
			lower := pixels[(row*2+1)*columns+column]
			if err := writeForeground(buffer, upper, mode); err != nil {
				return err
			}
			if err := writeBackground(buffer, lower, mode); err != nil {
				return err
			}
			if _, err := buffer.WriteRune('▀'); err != nil {
				return err
			}
		}
		if _, err := buffer.WriteString("\x1b[0m\n"); err != nil {
			return err
		}
	}
	return buffer.Flush()
}

func resize(source image.Image, width, height int) []RGB {
	bounds := source.Bounds()
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	pixels := make([]RGB, width*height)
	for y := 0; y < height; y++ {
		sourceY := bounds.Min.Y + int(int64(y)*int64(sourceHeight)/int64(height))
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + int(int64(x)*int64(sourceWidth)/int64(width))
			red, green, blue, _ := source.At(sourceX, sourceY).RGBA()
			pixels[y*width+x] = RGB{R: uint8(red >> 8), G: uint8(green >> 8), B: uint8(blue >> 8)}
		}
	}
	return pixels
}

func writeForeground(writer io.Writer, pixel RGB, mode ColorMode) error {
	switch mode {
	case TrueColor:
		_, err := fmt.Fprintf(writer, "\x1b[38;2;%d;%d;%dm", pixel.R, pixel.G, pixel.B)
		return err
	case Palette256:
		_, err := fmt.Fprintf(writer, "\x1b[38;5;%dm", nearest256(pixel))
		return err
	default:
		_, err := fmt.Fprintf(writer, "\x1b[%dm", foregroundCode(nearest16(pixel)))
		return err
	}
}

func writeBackground(writer io.Writer, pixel RGB, mode ColorMode) error {
	switch mode {
	case TrueColor:
		_, err := fmt.Fprintf(writer, "\x1b[48;2;%d;%d;%dm", pixel.R, pixel.G, pixel.B)
		return err
	case Palette256:
		_, err := fmt.Fprintf(writer, "\x1b[48;5;%dm", nearest256(pixel))
		return err
	default:
		_, err := fmt.Fprintf(writer, "\x1b[%dm", backgroundCode(nearest16(pixel)))
		return err
	}
}

func nearest256(pixel RGB) uint8 {
	bestIndex := 0
	bestDistance := int(^uint(0) >> 1)
	for index := 0; index < 256; index++ {
		candidate := color256(index)
		distance := colorDistance(pixel, candidate)
		if distance < bestDistance {
			bestIndex = index
			bestDistance = distance
		}
	}
	return uint8(bestIndex)
}

func nearest16(pixel RGB) uint8 {
	bestIndex := 0
	bestDistance := int(^uint(0) >> 1)
	for index, candidate := range palette16 {
		distance := colorDistance(pixel, candidate)
		if distance < bestDistance {
			bestIndex = index
			bestDistance = distance
		}
	}
	return uint8(bestIndex)
}

func color256(index int) RGB {
	if index < len(palette16) {
		return palette16[index]
	}
	if index < 232 {
		levels := [...]uint8{0, 95, 135, 175, 215, 255}
		index -= 16
		return RGB{R: levels[index/36], G: levels[index/6%6], B: levels[index%6]}
	}
	gray := uint8(8 + (index-232)*10)
	return RGB{R: gray, G: gray, B: gray}
}

func colorDistance(first, second RGB) int {
	deltaRed := int(first.R) - int(second.R)
	deltaGreen := int(first.G) - int(second.G)
	deltaBlue := int(first.B) - int(second.B)
	return 3*deltaRed*deltaRed + 4*deltaGreen*deltaGreen + 2*deltaBlue*deltaBlue
}

func foregroundCode(index uint8) int {
	if index < 8 {
		return 30 + int(index)
	}
	return 90 + int(index-8)
}

func backgroundCode(index uint8) int {
	if index < 8 {
		return 40 + int(index)
	}
	return 100 + int(index-8)
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, value := range environment {
		if strings.HasPrefix(value, prefix) {
			return value[len(prefix):]
		}
	}
	return ""
}

var palette16 = [...]RGB{
	{R: 0, G: 0, B: 0},
	{R: 205, G: 0, B: 0},
	{R: 0, G: 205, B: 0},
	{R: 205, G: 205, B: 0},
	{R: 0, G: 0, B: 238},
	{R: 205, G: 0, B: 205},
	{R: 0, G: 205, B: 205},
	{R: 229, G: 229, B: 229},
	{R: 127, G: 127, B: 127},
	{R: 255, G: 0, B: 0},
	{R: 0, G: 255, B: 0},
	{R: 255, G: 255, B: 0},
	{R: 92, G: 92, B: 255},
	{R: 255, G: 0, B: 255},
	{R: 0, G: 255, B: 255},
	{R: 255, G: 255, B: 255},
}
