package tests

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kristyancarvalho/bors/internal/cli"
	"github.com/kristyancarvalho/bors/internal/input"
	"github.com/kristyancarvalho/bors/internal/render/ansi"
	"github.com/kristyancarvalho/bors/internal/render/kitty"
	size "github.com/kristyancarvalho/bors/internal/render/size"
	"github.com/kristyancarvalho/bors/internal/terminal"
)

func TestSizeParsingAndFit(t *testing.T) {
	valid := map[string]size.Limits{
		"80x24": {Width: 80, Height: 24},
		"80":    {Width: 80},
		"x24":   {Height: 24},
	}
	for value, expected := range valid {
		actual, err := size.Parse(value)
		if err != nil || actual != expected {
			t.Fatalf("Parse(%q) = %#v, %v; want %#v", value, actual, err, expected)
		}
	}
	for _, value := range []string{"", "x", "80x", "0x24", "80x0", "80xx24", "-2", "abc", "9223372036854775808"} {
		if _, err := size.Parse(value); err == nil {
			t.Errorf("Parse(%q) succeeded", value)
		}
	}
	cases := []struct {
		name  string
		w     int
		h     int
		ratio float64
	}{
		{name: "landscape", w: 1600, h: 900, ratio: 16.0 / 9.0},
		{name: "portrait", w: 900, h: 1600, ratio: 9.0 / 16.0},
		{name: "square", w: 1200, h: 1200, ratio: 1},
		{name: "extreme wide", w: 32768, h: 1, ratio: 32768},
		{name: "extreme tall", w: 1, h: 32768, ratio: 1.0 / 32768.0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			width, height, err := size.Fit(test.w, test.h, 80, 24, 0.5)
			if err != nil {
				t.Fatal(err)
			}
			if width < 1 || height < 1 || width > 80 || height > 24 {
				t.Fatalf("Fit returned out-of-range dimensions %dx%d", width, height)
			}
			actualRatio := float64(width) / float64(height) * 0.5
			if test.ratio > 0.01 && test.ratio < 100 && abs(actualRatio-test.ratio) > test.ratio*0.12 {
				t.Errorf("visual aspect ratio %f, want near %f", actualRatio, test.ratio)
			}
		})
	}
	if _, _, err := size.Fit(100, 100, 2000, 2000, 0.5); err == nil {
		t.Fatal("Fit accepted a target area beyond the safety limit")
	}
}

func TestInputFormatsAndErrors(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			encoded := encodeImage(t, format, color.NRGBA{R: 200, G: 80, B: 40, A: 255})
			decoded, err := input.Load(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Format != format || decoded.Pixels.Bounds().Dx() != 2 || decoded.Pixels.Bounds().Dy() != 2 {
				t.Fatalf("decoded format/bounds = %s/%v", decoded.Format, decoded.Pixels.Bounds())
			}
			if format == "png" && !bytes.Equal(decoded.PNG, encoded) {
				t.Fatal("PNG payload was not preserved")
			}
		})
	}
	pngPath := filepath.Join(t.TempDir(), "streamed.png")
	pngBytes := encodeImage(t, "png", color.NRGBA{G: 180, A: 255})
	if err := os.WriteFile(pngPath, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	pngFile, err := os.Open(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	streamed, err := input.LoadFile(pngFile)
	if closeErr := pngFile.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || streamed.Format != "png" || streamed.PNG != nil {
		t.Fatalf("LoadFile did not keep PNG data streamable: %#v, %v", streamed, err)
	}
	for name, data := range map[string][]byte{
		"empty":       nil,
		"corrupt":     []byte("not an image"),
		"unsupported": []byte("RIFFxxxxWEBP"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := input.Load(bytes.NewReader(data)); err == nil {
				t.Fatal("invalid image was accepted")
			}
		})
	}
	if _, err := input.Load(errorReader{}); err == nil || !strings.Contains(err.Error(), "read image") {
		t.Fatalf("read failure error = %v", err)
	}
	largeImage := image.NewNRGBA(image.Rect(0, 0, 32_769, 1))
	var largePNG bytes.Buffer
	if err := png.Encode(&largePNG, largeImage); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Load(bytes.NewReader(largePNG.Bytes())); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("oversized image dimensions error = %v", err)
	}
	var animation bytes.Buffer
	frames := []*image.Paletted{
		image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.RGBA{R: 255, A: 255}}),
		image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.RGBA{B: 255, A: 255}}),
	}
	if err := gif.EncodeAll(&animation, &gif.GIF{Image: frames, Delay: []int{1, 1}, Config: image.Config{ColorModel: frames[0].Palette, Width: 1, Height: 1}}); err != nil {
		t.Fatal(err)
	}
	decoded, err := input.Load(bytes.NewReader(animation.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Frames) != 2 || decoded.LoopCount != 0 || decoded.Frames[0].Delay != 10*time.Millisecond || decoded.Frames[1].Delay != 10*time.Millisecond {
		t.Fatalf("GIF animation metadata = %d frames, loop %d, delays %#v", len(decoded.Frames), decoded.LoopCount, decoded.Frames)
	}
	red, _, blue, _ := decoded.Pixels.At(0, 0).RGBA()
	if red <= blue {
		t.Fatal("GIF first frame was not rendered")
	}
	red, _, blue, _ = decoded.Frames[1].Pixels.At(0, 0).RGBA()
	if blue <= red {
		t.Fatal("GIF second frame was not composited")
	}
}

func TestGIFDisposalAndFrameOffsets(t *testing.T) {
	palette := color.Palette{
		color.RGBA{},
		color.RGBA{R: 255, A: 255},
		color.RGBA{B: 255, A: 255},
		color.RGBA{G: 255, A: 255},
	}
	first := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
	first.SetColorIndex(0, 0, 1)
	first.SetColorIndex(1, 0, 1)
	second := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	second.SetColorIndex(0, 0, 2)
	third := image.NewPaletted(image.Rect(1, 0, 2, 1), palette)
	third.SetColorIndex(1, 0, 3)
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{
		Image:     []*image.Paletted{first, second, third},
		Delay:     []int{1, 1, 1},
		Disposal:  []byte{gif.DisposalBackground, gif.DisposalNone, gif.DisposalNone},
		LoopCount: -1,
		Config:    image.Config{ColorModel: palette, Width: 2, Height: 1},
	}); err != nil {
		t.Fatal(err)
	}
	decoded, err := input.Load(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Frames) != 3 {
		t.Fatalf("decoded %d GIF frames, want 3", len(decoded.Frames))
	}
	red, _, _, _ := decoded.Frames[0].Pixels.At(1, 0).RGBA()
	backgroundRed, _, backgroundBlue, _ := decoded.Frames[1].Pixels.At(1, 0).RGBA()
	_, _, secondBlue, _ := decoded.Frames[1].Pixels.At(0, 0).RGBA()
	_, _, blueAtLeft, _ := decoded.Frames[2].Pixels.At(0, 0).RGBA()
	_, greenAtRight, _, _ := decoded.Frames[2].Pixels.At(1, 0).RGBA()
	if red == 0 || backgroundRed != 0 || backgroundBlue != 0 || blueAtLeft == 0 || greenAtRight == 0 {
		t.Fatalf("GIF disposal or offset composition is incorrect: frame colors %x, %x/%x, %x, %x/%x", red, backgroundRed, backgroundBlue, secondBlue, blueAtLeft, greenAtRight)
	}
}

func TestANSIOutputModesAndCleanup(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	source.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	source.SetNRGBA(1, 1, color.NRGBA{R: 255, B: 255, A: 255})
	var output bytes.Buffer
	if err := ansi.Render(&output, source, 2, 1, 2, ansi.TrueColor, true); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Count(text, "▀") != 2 || !strings.Contains(text, "\x1b[38;2;255;0;0m") || !strings.Contains(text, "\x1b[48;2;0;0;255m") {
		t.Fatalf("unexpected half-block output %q", text)
	}
	if !strings.HasSuffix(text, "\x1b[0m\n") || strings.Count(text, "\x1b[0m\n") != 1 {
		t.Fatalf("line reset missing or repeated: %q", text)
	}
	output.Reset()
	if err := ansi.Render(&output, source, 2, 2, 1, ansi.Palette256, true); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "█") != 4 || strings.Count(output.String(), "\x1b[0m\n") != 2 || !strings.Contains(output.String(), "\x1b[38;5;") {
		t.Fatalf("unexpected single-cell output %q", output.String())
	}
	output.Reset()
	if err := ansi.Render(&output, source, 2, 2, 1, ansi.Palette16, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[91m") {
		t.Fatalf("16-color output missing expected ANSI code: %q", output.String())
	}
	output.Reset()
	if err := ansi.Render(&output, source, 1, 1, 2, ansi.Palette16, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[44m") {
		t.Fatalf("16-color background mapping missing: %q", output.String())
	}
	transparent := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	output.Reset()
	if err := ansi.Render(&output, transparent, 1, 1, 1, ansi.TrueColor, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[38;2;0;0;0m") {
		t.Fatalf("transparent pixel was not composited against black: %q", output.String())
	}
	oddHeight := image.NewNRGBA(image.Rect(0, 0, 1, 3))
	output.Reset()
	if err := ansi.Render(&output, oddHeight, 1, 2, 2, ansi.TrueColor, true); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "▀") != 2 || strings.Count(output.String(), "\x1b[0m\n") != 2 {
		t.Fatalf("odd-height image left a trailing colored row: %q", output.String())
	}
	output.Reset()
	if err := ansi.Render(&output, transparent, 1, 1, 1, ansi.TrueColor, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "#") || strings.Contains(output.String(), "█") {
		t.Fatalf("non-UTF-8 single-cell output used an unsuitable glyph: %q", output.String())
	}
}

func TestColorModeDetection(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want ansi.ColorMode
	}{
		{name: "unknown", want: ansi.Palette16},
		{name: "256 colors", env: []string{"TERM=screen-256color"}, want: ansi.Palette256},
		{name: "truecolor", env: []string{"COLORTERM=truecolor"}, want: ansi.TrueColor},
	}
	for _, test := range cases {
		mode, err := ansi.ResolveColorMode("auto", test.env)
		if err != nil || mode != test.want {
			t.Errorf("%s: ResolveColorMode = %q, %v; want %q", test.name, mode, err, test.want)
		}
	}
	if _, err := ansi.ResolveColorMode("invalid", nil); err == nil {
		t.Fatal("invalid color mode succeeded")
	}
}

func TestKittyProtocolChunking(t *testing.T) {
	payload := make([]byte, 12_000)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	var output bytes.Buffer
	if err := kitty.Render(&output, payload, 42, 21); err != nil {
		t.Fatal(err)
	}
	chunks := parseKittyChunks(t, output.Bytes())
	if len(chunks) < 3 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].control, "a=T,f=100,c=42,r=21,q=2") {
		t.Fatalf("first chunk control = %q", chunks[0].control)
	}
	var reconstructed bytes.Buffer
	for index, chunk := range chunks {
		if len(chunk.data) > 4096 || (index < len(chunks)-1 && len(chunk.data)%4 != 0) {
			t.Fatalf("chunk %d has invalid encoded length %d", index, len(chunk.data))
		}
		if index < len(chunks)-1 && !strings.HasSuffix(chunk.control, "m=1") {
			t.Errorf("non-final chunk %d has control %q", index, chunk.control)
		}
		if index == len(chunks)-1 && !strings.HasSuffix(chunk.control, "m=0") {
			t.Errorf("final chunk has control %q", chunk.control)
		}
		reconstructed.Write(chunk.data)
	}
	decoded := make([]byte, reconstructed.Len())
	count, err := base64.StdEncoding.Decode(decoded, reconstructed.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded[:count], payload) {
		t.Fatal("protocol chunks did not reconstruct the original payload")
	}
	output.Reset()
	if err := kitty.Render(&output, []byte{1, 2, 3}, 1, 1); err != nil {
		t.Fatal(err)
	}
	if len(parseKittyChunks(t, output.Bytes())) != 1 {
		t.Fatal("small payload did not use a single chunk")
	}
}

func TestKittyChunkBoundaries(t *testing.T) {
	for _, sourceSize := range []int{3071, 3072, 3073, 6144} {
		t.Run(fmt.Sprint(sourceSize), func(t *testing.T) {
			payload := bytes.Repeat([]byte{0x5a}, sourceSize)
			var output bytes.Buffer
			if err := kitty.Render(&output, payload, 4, 2); err != nil {
				t.Fatal(err)
			}
			chunks := parseKittyChunks(t, output.Bytes())
			var reconstructed bytes.Buffer
			for index, chunk := range chunks {
				if len(chunk.data) > 4096 || (index < len(chunks)-1 && len(chunk.data)%4 != 0) {
					t.Fatalf("chunk %d has invalid length %d", index, len(chunk.data))
				}
				reconstructed.Write(chunk.data)
			}
			decoded, err := base64.StdEncoding.DecodeString(reconstructed.String())
			if err != nil || !bytes.Equal(decoded, payload) {
				t.Fatalf("payload did not survive chunk boundary: %v", err)
			}
		})
	}
}

func TestKittyAnimationProtocolFinite(t *testing.T) {
	frames := []image.Image{
		image.NewNRGBA(image.Rect(0, 0, 2, 2)),
		image.NewNRGBA(image.Rect(0, 0, 2, 2)),
	}
	frames[0].(*image.NRGBA).SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	frames[1].(*image.NRGBA).SetNRGBA(0, 0, color.NRGBA{B: 255, A: 255})
	delays := []time.Duration{20 * time.Millisecond, 30 * time.Millisecond}
	var output bytes.Buffer
	var waited []time.Duration
	wait := func(delay time.Duration) { waited = append(waited, delay) }
	options := kitty.AnimationOptions{Columns: 8, Rows: 4, ImageNumber: 77, LoopCount: -1, Wait: wait}
	if err := kitty.RenderAnimation(&output, frames, delays, options); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	requireKittyControls(t, text, []string{
		"a=t,f=100,I=77,q=2",
		"a=f,f=100,I=77,z=30,q=2",
		"a=a,I=77,r=1,z=20,q=2",
		"a=p,I=77,c=8,r=4,q=2",
		"a=a,I=77,c=2,q=2",
	})
	if strings.Contains(text, "s=3,v=1") || strings.Count(text, "a=f,f=100") != 1 || len(waited) != 2 || waited[0] != delays[0] || waited[1] != delays[1] {
		t.Fatalf("finite Kitty animation control or timing is wrong: %q, waits %#v", text, waited)
	}
	if err := kitty.RenderAnimation(io.Discard, frames[:1], delays[:1], options); err == nil {
		t.Fatal("single-frame Kitty animation succeeded")
	}
}

func TestKittyAnimationProtocolInfinite(t *testing.T) {
	frames := kittyAnimationFrames()
	delays := []time.Duration{20 * time.Millisecond, 30 * time.Millisecond}
	var output bytes.Buffer
	var waited []time.Duration
	wait := func(delay time.Duration) { waited = append(waited, delay) }
	options := kitty.AnimationOptions{Columns: 8, Rows: 4, ImageNumber: 78, LoopCount: 0, Wait: wait}
	if err := kitty.RenderAnimation(&output, frames, delays, options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "a=a,I=78,s=3,v=1,q=2") || len(waited) != 0 {
		t.Fatalf("infinite Kitty animation was not delegated to the terminal: %q", output.String())
	}
	if err := kitty.RenderAnimation(io.Discard, frames, delays, kitty.AnimationOptions{Columns: 8, Rows: 4, ImageNumber: 79, LoopCount: 0}); err != nil {
		t.Fatal(err)
	}
}

func kittyAnimationFrames() []image.Image {
	frames := []image.Image{
		image.NewNRGBA(image.Rect(0, 0, 2, 2)),
		image.NewNRGBA(image.Rect(0, 0, 2, 2)),
	}
	frames[0].(*image.NRGBA).SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	frames[1].(*image.NRGBA).SetNRGBA(0, 0, color.NRGBA{B: 255, A: 255})
	return frames
}

func requireKittyControls(t *testing.T, output string, expectedControls []string) {
	t.Helper()
	for _, expected := range expectedControls {
		if !strings.Contains(output, expected) {
			t.Errorf("Kitty animation output omitted %q", expected)
		}
	}
}

func TestKittyAnimationChunkContinuationMetadata(t *testing.T) {
	frames := largeKittyAnimationFrames()
	var output bytes.Buffer
	options := kitty.AnimationOptions{Columns: 20, Rows: 10, ImageNumber: 91, LoopCount: 0}
	if err := kitty.RenderAnimation(&output, frames, []time.Duration{40 * time.Millisecond, 60 * time.Millisecond}, options); err != nil {
		t.Fatal(err)
	}
	chunks := parseKittyChunks(t, output.Bytes())
	assertKittyChunkSizes(t, chunks)
	assertAnimationChunkMetadata(t, chunks)
}

func largeKittyAnimationFrames() []image.Image {
	frames := make([]image.Image, 2)
	state := uint32(1)
	for frameIndex := range frames {
		frame := image.NewNRGBA(image.Rect(0, 0, 128, 128))
		for index := 0; index < len(frame.Pix); index += 4 {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			frame.Pix[index] = byte(state)
			frame.Pix[index+1] = byte(state >> 8)
			frame.Pix[index+2] = byte(state >> 16)
			frame.Pix[index+3] = 255
		}
		frames[frameIndex] = frame
	}
	return frames
}

func assertKittyChunkSizes(t *testing.T, chunks []kittyChunk) {
	for index, chunk := range chunks {
		if len(chunk.data) > 4096 || (index < len(chunks)-1 && len(chunk.data) > 0 && len(chunk.data)%4 != 0) {
			t.Fatalf("Kitty animation chunk %d has invalid data length %d", index, len(chunk.data))
		}
	}
}

func assertAnimationChunkMetadata(t *testing.T, chunks []kittyChunk) {
	frameChunkCount := 0
	for index, chunk := range chunks {
		if !strings.Contains(chunk.control, "a=f") {
			continue
		}
		frameChunkCount++
		assertAnimationChunkControl(t, index, chunk)
	}
	if frameChunkCount < 2 {
		t.Fatalf("large animation frame did not exercise chunk continuation: %d chunks", frameChunkCount)
	}
}

func assertAnimationChunkControl(t *testing.T, index int, chunk kittyChunk) {
	if strings.Contains(chunk.control, "f=100") && !strings.Contains(chunk.control, "I=91") {
		t.Errorf("animation chunk omitted its image number: %q", chunk.control)
	}
	if index > 0 && !strings.Contains(chunk.control, "m=0") && !strings.Contains(chunk.control, "m=1") {
		t.Errorf("animation continuation omitted its chunk marker: %q", chunk.control)
	}
}

func TestKittyRejectsInvalidArgumentsAndWriterErrors(t *testing.T) {
	if err := kitty.Render(io.Discard, []byte{1}, 0, 1); err == nil {
		t.Fatal("zero-width placement succeeded")
	}
	if err := kitty.Render(io.Discard, nil, 1, 1); err == nil {
		t.Fatal("empty payload succeeded")
	}
	if err := kitty.Render(errorWriter{}, []byte{1}, 1, 1); err == nil {
		t.Fatal("writer error was ignored")
	}
}

func TestKittyCapabilityQueryAndRestoration(t *testing.T) {
	reply := []byte("\x1b_Gi=31;EINVAL:dummy\x1b\\\x1b[?1;2c")
	transport := &fakeTransport{response: reply}
	supported, err := terminal.DetectKitty(transport, time.Second)
	if err != nil || !supported || transport.restored != 1 {
		t.Fatalf("DetectKitty = %t, %v, restored %d times", supported, err, transport.restored)
	}
	if !bytes.Contains(transport.written, []byte("a=q,t=d,f=24;AAAA")) || !bytes.HasSuffix(transport.written, []byte("\x1b[c")) {
		t.Fatalf("unexpected capability query %q", transport.written)
	}
	unsupported := &fakeTransport{response: []byte("\x1b[?1;2c")}
	supported, err = terminal.DetectKitty(unsupported, time.Second)
	if err != nil || supported || unsupported.restored != 1 {
		t.Fatalf("unsupported query = %t, %v, restored %d times", supported, err, unsupported.restored)
	}
	unrelatedInput := &fakeTransport{response: []byte("qnext")}
	supported, err = terminal.DetectKitty(unrelatedInput, time.Second)
	if err != nil || supported || len(unrelatedInput.response) != 4 || unrelatedInput.restored != 1 {
		t.Fatalf("unrelated input handling = %t, %v, remaining %q", supported, err, unrelatedInput.response)
	}
	malformed := &fakeTransport{response: []byte("\x1b_Gi=32;OK\x1b\\")}
	supported, err = terminal.DetectKitty(malformed, time.Second)
	if err != nil || supported || malformed.restored != 1 {
		t.Fatalf("malformed response = %t, %v", supported, err)
	}
	timedOut := &fakeTransport{}
	supported, err = terminal.DetectKitty(timedOut, time.Second)
	if err != nil || supported || timedOut.restored != 1 {
		t.Fatalf("timeout response = %t, %v, restored %d times", supported, err, timedOut.restored)
	}
	writeFailure := &fakeTransport{writeErr: errors.New("write failed")}
	if _, err := terminal.DetectKitty(writeFailure, time.Second); err == nil || writeFailure.restored != 1 {
		t.Fatal("query write failure did not restore terminal")
	}
	readFailure := &fakeTransport{readErr: errors.New("read failed")}
	if _, err := terminal.DetectKitty(readFailure, time.Second); err == nil || readFailure.restored != 1 {
		t.Fatal("query read failure did not restore terminal")
	}
	restoreFailure := &fakeTransport{response: reply, restoreErr: errors.New("restore failed")}
	if _, err := terminal.DetectKitty(restoreFailure, time.Second); !errors.Is(err, terminal.ErrRestore) {
		t.Fatalf("restore error = %v", err)
	}
	unsafe := &fakeTransport{beginErr: terminal.ErrUnsafeQuery}
	if supported, err := terminal.DetectKitty(unsafe, time.Second); supported || err != nil {
		t.Fatalf("unsafe query = %t, %v", supported, err)
	}
}

func TestCLIFlagsAndStreams(t *testing.T) {
	imageData := encodeImage(t, "png", color.NRGBA{R: 200, A: 255})
	path := filepath.Join(t.TempDir(), "photo with spaces.png")
	if err := os.WriteFile(path, imageData, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"--fallback", "--kitty", path},
		{"--fallback", "--size", "80xx24", path},
		{"--fallback", "--resolution", "3", path},
		{"--fallback", "--color", "invalid", path},
		{"--fallback"},
		{"--fallback", "one", "two"},
	} {
		runtime, _, stderr := testRuntime(nil)
		if code := cli.Run(arguments, runtime, "test"); code == 0 || stderr.Len() == 0 {
			t.Errorf("Run(%q) = %d, stderr %q", arguments, code, stderr.String())
		}
	}
	runtime, stdout, stderr := testRuntime(nil)
	if code := cli.Run([]string{"--help"}, runtime, "test"); code != 0 || !strings.Contains(stdout.String(), "Usage: bors") {
		t.Fatalf("help output = %d, %q, stderr %q", code, stdout.String(), stderr.String())
	}
	runtime, stdout, stderr = testRuntime(nil)
	if code := cli.Run([]string{"--version"}, runtime, "1.2.3"); code != 0 || stdout.String() != "bors 1.2.3\n" {
		t.Fatalf("version output = %d, %q, stderr %q", code, stdout.String(), stderr.String())
	}
	runtime, _, stderr = testRuntime(nil)
	runtime.OutputTTY = false
	if code := cli.Run([]string{"--fallback", path}, runtime, "test"); code == 0 || !strings.Contains(stderr.String(), "interactive terminal") {
		t.Fatalf("redirected stdout result = %d, %q", code, stderr.String())
	}
	runtime, stdout, stderr = testRuntime(nil)
	if code := cli.Run([]string{"--fallback", path}, runtime, "test"); code != 0 || !strings.Contains(stdout.String(), "▀") {
		t.Fatalf("space-containing path result = %d, output %q, stderr %q", code, stdout.String(), stderr.String())
	}
	for _, dimension := range []struct {
		value string
		rows  int
		cells int
	}{
		{value: "8", rows: 4, cells: 32},
		{value: "x8", rows: 8, cells: 128},
	} {
		sizeRuntime, sizeOutput, sizeError := testRuntime(nil)
		if code := cli.Run([]string{"--fallback", "--size", dimension.value, path}, sizeRuntime, "test"); code != 0 {
			t.Fatalf("single-axis size %q result = %d, stderr %q", dimension.value, code, sizeError.String())
		}
		if strings.Count(sizeOutput.String(), "▀") != dimension.cells || strings.Count(sizeOutput.String(), "\x1b[0m\n") != dimension.rows {
			t.Errorf("single-axis size %q produced %d cells and %d rows", dimension.value, strings.Count(sizeOutput.String(), "▀"), strings.Count(sizeOutput.String(), "\x1b[0m\n"))
		}
	}
	shortRuntime, shortOutput, shortError := testRuntime(nil)
	shortRuntime.Probe = func() (bool, error) { t.Fatal("forced fallback should skip probing"); return true, nil }
	if code := cli.Run([]string{"-f", "-s", "8x4", "-r", "1", "--color", "256", path}, shortRuntime, "test"); code != 0 || strings.Count(shortOutput.String(), "█") == 0 {
		t.Fatalf("short flag fallback result = %d, stderr %q", code, shortError.String())
	}
	asciiRuntime, asciiOutput, asciiError := testRuntime(nil)
	asciiRuntime.Env = []string{"LANG=C", "TERM=dumb"}
	if code := cli.Run([]string{"--fallback", "--size", "8x4", path}, asciiRuntime, "test"); code != 0 || !strings.Contains(asciiOutput.String(), "#") || strings.Contains(asciiOutput.String(), "▀") {
		t.Fatalf("non-UTF-8 default resolution = %d, output %q, stderr %q", code, asciiOutput.String(), asciiError.String())
	}
	failingRuntime, _, failingError := testRuntime(nil)
	failingRuntime.Stdout = errorWriter{}
	if code := cli.Run([]string{"--fallback", path}, failingRuntime, "test"); code == 0 || !strings.Contains(failingError.String(), "output failed") {
		t.Fatalf("output failure result = %d, stderr %q", code, failingError.String())
	}
	stdinRuntime, stdinOutput, stdinError := testRuntime(bytes.NewReader(imageData))
	stdinRuntime.InputTTY = false
	probeCalled := false
	stdinRuntime.Probe = func() (bool, error) { probeCalled = true; return true, nil }
	if code := cli.Run([]string{"-s", "8x4", "-"}, stdinRuntime, "test"); code != 0 || probeCalled || !strings.Contains(stdinOutput.String(), "▀") {
		t.Fatalf("stdin result = %d, probe %t, stderr %q", code, probeCalled, stdinError.String())
	}
	missingRuntime, _, missingError := testRuntime(nil)
	if code := cli.Run([]string{"--fallback", "missing.png"}, missingRuntime, "test"); code == 0 || !strings.Contains(missingError.String(), "open image") {
		t.Fatalf("missing path result = %d, stderr %q", code, missingError.String())
	}
	corruptPath := filepath.Join(t.TempDir(), "corrupt.png")
	if err := os.WriteFile(corruptPath, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptRuntime, _, corruptError := testRuntime(nil)
	if code := cli.Run([]string{"--fallback", corruptPath}, corruptRuntime, "test"); code == 0 || !strings.Contains(corruptError.String(), "decode image") {
		t.Fatalf("corrupt input result = %d, stderr %q", code, corruptError.String())
	}
	directoryRuntime, _, directoryError := testRuntime(nil)
	if code := cli.Run([]string{"--fallback", t.TempDir()}, directoryRuntime, "test"); code == 0 || !strings.Contains(directoryError.String(), "not a regular file") {
		t.Fatalf("directory input result = %d, stderr %q", code, directoryError.String())
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pathDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDirectory, "-image.png"), imageData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(pathDirectory); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)
	dashRuntime, dashOutput, dashError := testRuntime(nil)
	if code := cli.Run([]string{"--fallback", "--", "-image.png"}, dashRuntime, "test"); code != 0 || !strings.Contains(dashOutput.String(), "▀") {
		t.Fatalf("dash-prefixed path result = %d, output %q, stderr %q", code, dashOutput.String(), dashError.String())
	}
}

func TestCLIAnimatedGIFFallback(t *testing.T) {
	palette := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	frames := []*image.Paletted{
		image.NewPaletted(image.Rect(0, 0, 2, 2), palette),
		image.NewPaletted(image.Rect(0, 0, 2, 2), palette),
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			frames[0].SetColorIndex(x, y, 0)
			frames[1].SetColorIndex(x, y, 1)
		}
	}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{
		Image:     frames,
		Delay:     []int{2, 3},
		LoopCount: -1,
		Config:    image.Config{ColorModel: palette, Width: 2, Height: 2},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "animated.gif")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, output, stderr := testRuntime(nil)
	var waited []time.Duration
	runtime.Wait = func(delay time.Duration) { waited = append(waited, delay) }
	runtime.GetSize = func() (terminal.Size, error) {
		return terminal.Size{Columns: 80, Rows: 24, CellRatio: 1}, nil
	}
	if code := cli.Run([]string{"--fallback", "--size", "2x2", "--color", "truecolor", path}, runtime, "test"); code != 0 {
		t.Fatalf("animated GIF CLI result = %d, stderr %q", code, stderr.String())
	}
	text := output.String()
	if !strings.Contains(text, "\x1b[38;2;255;0;0m") || !strings.Contains(text, "\x1b[38;2;0;0;255m") || strings.Count(text, "▀") != 8 || !strings.Contains(text, "\x1b[2A") {
		t.Fatalf("ANSI animation did not redraw both frames in place; output bytes %d", len(text))
	}
	if len(waited) != 2 || waited[0] != 20*time.Millisecond || waited[1] != 30*time.Millisecond {
		t.Fatalf("ANSI animation delays = %#v", waited)
	}
	runtime, output, stderr = testRuntime(nil)
	waited = nil
	runtime.Wait = func(delay time.Duration) { waited = append(waited, delay) }
	if code := cli.Run([]string{"--kitty", path}, runtime, "test"); code != 0 {
		t.Fatalf("Kitty animated GIF CLI result = %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(output.String(), "a=t,f=100,I=") || !strings.Contains(output.String(), "a=f,f=100,I=") || len(waited) != 2 {
		t.Fatalf("Kitty GIF animation was not transmitted: waits %#v, output %q", waited, output.String())
	}
}

func TestCLIAutomaticAndForcedKittySelection(t *testing.T) {
	imageData := encodeImage(t, "png", color.NRGBA{B: 200, A: 255})
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, imageData, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, output, stderr := testRuntime(nil)
	probeCalls := 0
	runtime.Probe = func() (bool, error) { probeCalls++; return true, nil }
	if code := cli.Run([]string{path}, runtime, "test"); code != 0 || probeCalls != 1 || !strings.Contains(output.String(), "a=T,f=100") {
		t.Fatalf("automatic Kitty selection = %d, probes %d, stderr %q", code, probeCalls, stderr.String())
	}
	chunks := parseKittyChunks(t, output.Bytes())
	var directPayload bytes.Buffer
	for _, chunk := range chunks {
		directPayload.Write(chunk.data)
	}
	transmitted, err := base64.StdEncoding.DecodeString(directPayload.String())
	if err != nil || !bytes.Equal(transmitted, imageData) {
		t.Fatalf("file PNG was not transmitted directly: %v", err)
	}
	runtime, output, stderr = testRuntime(nil)
	runtime.Probe = func() (bool, error) { return false, nil }
	if code := cli.Run([]string{path}, runtime, "test"); code != 0 || !strings.Contains(output.String(), "▀") {
		t.Fatalf("automatic fallback = %d, stderr %q", code, stderr.String())
	}
	runtime, output, stderr = testRuntime(nil)
	runtime.Probe = func() (bool, error) { t.Fatal("forced Kitty should skip probing"); return false, nil }
	if code := cli.Run([]string{"--kitty", path}, runtime, "test"); code != 0 || !strings.Contains(output.String(), "a=T,f=100") {
		t.Fatalf("forced Kitty = %d, stderr %q", code, stderr.String())
	}
	jpegPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(jpegPath, encodeImage(t, "jpeg", color.NRGBA{R: 50, G: 70, B: 180, A: 255}), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, output, stderr = testRuntime(nil)
	runtime.Probe = func() (bool, error) { t.Fatal("forced Kitty should skip probing"); return false, nil }
	if code := cli.Run([]string{"--kitty", jpegPath}, runtime, "test"); code != 0 {
		t.Fatalf("JPEG Kitty conversion = %d, stderr %q", code, stderr.String())
	}
	chunks = parseKittyChunks(t, output.Bytes())
	var encoded bytes.Buffer
	for _, chunk := range chunks {
		encoded.Write(chunk.data)
	}
	converted, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil || !bytes.HasPrefix(converted, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("JPEG conversion did not produce a PNG payload: %v", err)
	}
	runtime, output, stderr = testRuntime(nil)
	runtime.Probe = func() (bool, error) { return false, errors.New("query failed") }
	if code := cli.Run([]string{path}, runtime, "test"); code != 0 || !strings.Contains(output.String(), "▀") {
		t.Fatalf("query failure did not fall back: %d, stderr %q", code, stderr.String())
	}
}

type kittyChunk struct {
	control string
	data    []byte
}

func parseKittyChunks(t *testing.T, output []byte) []kittyChunk {
	t.Helper()
	var chunks []kittyChunk
	for len(output) > 0 {
		if !bytes.HasPrefix(output, []byte("\x1b_G")) {
			t.Fatalf("invalid Kitty prefix %q", output[:min(len(output), 20)])
		}
		end := bytes.Index(output, []byte("\x1b\\"))
		if end < 0 {
			t.Fatal("unterminated Kitty command")
		}
		command := output[len("\x1b_G"):end]
		separator := bytes.IndexByte(command, ';')
		if separator < 0 {
			chunks = append(chunks, kittyChunk{control: string(command)})
			output = output[end+len("\x1b\\"):]
			continue
		}
		chunks = append(chunks, kittyChunk{control: string(command[:separator]), data: append([]byte(nil), command[separator+1:]...)})
		output = output[end+len("\x1b\\"):]
	}
	return chunks
}

func encodeImage(t *testing.T, format string, value color.NRGBA) []byte {
	t.Helper()
	imageValue := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			imageValue.SetNRGBA(x, y, value)
		}
	}
	var output bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&output, imageValue)
	case "jpeg":
		err = jpeg.Encode(&output, imageValue, &jpeg.Options{Quality: 90})
	case "gif":
		err = gif.Encode(&output, imageValue, nil)
	default:
		err = fmt.Errorf("unknown test format %q", format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testRuntime(stdin io.Reader) (cli.Runtime, *bytes.Buffer, *bytes.Buffer) {
	if stdin == nil {
		stdin = bytes.NewReader(nil)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return cli.Runtime{
		Stdin:     stdin,
		Stdout:    stdout,
		Stderr:    stderr,
		InputTTY:  true,
		OutputTTY: true,
		GetSize: func() (terminal.Size, error) {
			return terminal.Size{Columns: 80, Rows: 24, CellRatio: 0.5}, nil
		},
		Probe: func() (bool, error) { return false, nil },
		Env:   []string{"LANG=en_US.UTF-8", "TERM=xterm-256color"},
	}, stdout, stderr
}

type fakeTransport struct {
	response   []byte
	written    []byte
	beginErr   error
	writeErr   error
	readErr    error
	restoreErr error
	restored   int
}

func (transport *fakeTransport) Begin() (func() error, error) {
	if transport.beginErr != nil {
		return nil, transport.beginErr
	}
	return func() error {
		transport.restored++
		return transport.restoreErr
	}, nil
}

func (transport *fakeTransport) Write(data []byte) error {
	transport.written = append(transport.written, data...)
	return transport.writeErr
}

func (transport *fakeTransport) ReadAvailableByte(time.Duration) (byte, bool, error) {
	if transport.readErr != nil {
		return 0, false, transport.readErr
	}
	if len(transport.response) == 0 {
		return 0, false, nil
	}
	value := transport.response[0]
	transport.response = transport.response[1:]
	return value, true, nil
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("output failed")
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
