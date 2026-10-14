package kitty

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
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
	chunks := &chunkWriter{
		writer: writer,
		first:  fmt.Sprintf("a=T,f=100,c=%d,r=%d,q=2", columns, rows),
		buffer: make([]byte, 0, chunkSize),
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
	writer io.Writer
	first  string
	buffer []byte
	count  int
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
		control = ""
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
