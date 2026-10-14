package kitty

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"time"
)

const chunkSize = 4096

func Render(writer io.Writer, pngData []byte, columns, rows int) error {
	return RenderReader(writer, bytes.NewReader(pngData), columns, rows)
}

func RenderReader(writer io.Writer, pngReader io.Reader, columns, rows int) error {
	if columns <= 0 || rows <= 0 {
		return errors.New("Kitty placement dimensions must be positive")
	}
	if pngReader == nil {
		return errors.New("PNG payload is empty")
	}
	return transmit(writer, pngReader, fmt.Sprintf("a=T,f=100,c=%d,r=%d,q=2", columns, rows), "")
}

func RenderAnimation(writer io.Writer, frames []image.Image, delays []time.Duration, columns, rows int, imageNumber uint32, loopCount int, wait func(time.Duration)) error {
	if len(frames) < 2 || len(frames) != len(delays) {
		return errors.New("Kitty animation needs matching frame and delay lists")
	}
	if columns <= 0 || rows <= 0 || imageNumber == 0 {
		return errors.New("Kitty animation dimensions and image number must be positive")
	}
	if loopCount < -1 {
		return errors.New("Kitty animation loop count is invalid")
	}
	normalizedDelays := append([]time.Duration(nil), delays...)
	for index, frame := range frames {
		if frame == nil || frame.Bounds().Empty() {
			return fmt.Errorf("Kitty animation frame %d is empty", index+1)
		}
		if normalizedDelays[index] <= 0 {
			normalizedDelays[index] = 100 * time.Millisecond
		}
	}
	for index, frame := range frames {
		buffer := &limitedBuffer{limit: 64 << 20}
		if err := png.Encode(buffer, frame); err != nil {
			return fmt.Errorf("encode Kitty animation frame %d: %w", index+1, err)
		}
		control := fmt.Sprintf("a=t,f=100,I=%d,q=2", imageNumber)
		continuation := ""
		if index > 0 {
			control = fmt.Sprintf("a=f,f=100,I=%d,z=%d,q=2", imageNumber, delayMilliseconds(normalizedDelays[index]))
			continuation = "a=f,q=2"
		}
		if err := transmit(writer, bytes.NewReader(buffer.Bytes()), control, continuation); err != nil {
			return err
		}
	}
	rootDelay := fmt.Sprintf("\x1b_Ga=a,I=%d,r=1,z=%d,q=2\x1b\\", imageNumber, delayMilliseconds(normalizedDelays[0]))
	if err := writeAll(writer, []byte(rootDelay)); err != nil {
		return err
	}
	placement := fmt.Sprintf("\x1b_Ga=p,I=%d,c=%d,r=%d,q=2\x1b\\", imageNumber, columns, rows)
	if err := writeAll(writer, []byte(placement)); err != nil {
		return err
	}
	if loopCount == 0 {
		start := fmt.Sprintf("\x1b_Ga=a,I=%d,s=3,v=1,q=2\x1b\\", imageNumber)
		return writeAll(writer, []byte(start))
	}
	cycles := 1
	if loopCount > 0 {
		cycles += loopCount
	}
	if wait == nil {
		wait = time.Sleep
	}
	for cycle := 0; cycle < cycles; cycle++ {
		for index := 1; index < len(frames); index++ {
			wait(normalizedDelays[index-1])
			if err := selectAnimationFrame(writer, imageNumber, index+1); err != nil {
				return err
			}
		}
		if cycle+1 < cycles {
			wait(normalizedDelays[len(normalizedDelays)-1])
			if err := selectAnimationFrame(writer, imageNumber, 1); err != nil {
				return err
			}
		}
	}
	wait(normalizedDelays[len(normalizedDelays)-1])
	return nil
}

func selectAnimationFrame(writer io.Writer, imageNumber uint32, frame int) error {
	control := fmt.Sprintf("\x1b_Ga=a,I=%d,c=%d,q=2\x1b\\", imageNumber, frame)
	return writeAll(writer, []byte(control))
}

func delayMilliseconds(delay time.Duration) int64 {
	milliseconds := delay.Milliseconds()
	if milliseconds < 1 {
		return 1
	}
	return milliseconds
}

func transmit(writer io.Writer, pngReader io.Reader, first, continuation string) error {
	chunks := &chunkWriter{
		writer:       writer,
		first:        first,
		continuation: continuation,
		buffer:       make([]byte, 0, chunkSize),
	}
	encoder := base64.NewEncoder(base64.StdEncoding, chunks)
	count, err := io.Copy(encoder, pngReader)
	closeErr := encoder.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if count == 0 {
		return errors.New("PNG payload is empty")
	}
	return chunks.close()
}

type chunkWriter struct {
	writer       io.Writer
	first        string
	continuation string
	buffer       []byte
	count        int
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

func (chunks *chunkWriter) Write(data []byte) (int, error) {
	originalLength := len(data)
	for len(data) > 0 {
		if len(chunks.buffer) == chunkSize {
			if err := chunks.flush(true); err != nil {
				return originalLength - len(data), err
			}
		}
		available := chunkSize - len(chunks.buffer)
		count := len(data)
		if count > available {
			count = available
		}
		chunks.buffer = append(chunks.buffer, data[:count]...)
		data = data[count:]
	}
	return originalLength, nil
}

func (chunks *chunkWriter) close() error {
	if len(chunks.buffer) == 0 {
		return nil
	}
	return chunks.flush(false)
}

func (chunks *chunkWriter) flush(more bool) error {
	control := chunks.first
	if chunks.count > 0 {
		control = chunks.continuation
	}
	continuation := "0"
	if more {
		continuation = "1"
	}
	if control != "" {
		control += ","
	}
	control += "m=" + continuation
	sequence := []byte("\x1b_G" + control + ";")
	sequence = append(sequence, chunks.buffer...)
	sequence = append(sequence, '\x1b', '\\')
	if err := writeAll(chunks.writer, sequence); err != nil {
		return err
	}
	chunks.count++
	chunks.buffer = chunks.buffer[:0]
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := writer.Write(data)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		data = data[count:]
	}
	return nil
}
