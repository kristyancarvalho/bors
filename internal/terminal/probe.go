package terminal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const DefaultProbeTimeout = 120 * time.Millisecond

var ErrUnsafeQuery = errors.New("terminal query is unsafe")
var ErrRestore = errors.New("terminal state restoration failed")

type QueryTransport interface {
	Begin() (func() error, error)
	Write([]byte) error
	ReadAvailableByte(time.Duration) (byte, bool, error)
}

func DetectKitty(transport QueryTransport, timeout time.Duration) (supported bool, err error) {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	restore, beginErr := transport.Begin()
	if beginErr != nil {
		if errors.Is(beginErr, ErrUnsafeQuery) {
			return false, nil
		}
		return false, beginErr
	}
	defer func() {
		if restoreErr := restore(); restoreErr != nil {
			err = errors.Join(ErrRestore, err, restoreErr)
			supported = false
		}
	}()
	query := []byte("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c")
	if err = transport.Write(query); err != nil {
		return false, err
	}
	deadline := time.Now().Add(timeout)
	response := make([]byte, 0, 128)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		value, available, readErr := transport.ReadAvailableByte(remaining)
		if readErr != nil {
			return false, readErr
		}
		if !available {
			break
		}
		response = append(response, value)
		if len(response) == 1 && value != 0x1b {
			return false, nil
		}
		if len(response) == 2 && value != '_' && value != '[' {
			return false, nil
		}
		if len(response) == 3 && ((response[1] == '_' && value != 'G') || (response[1] == '[' && value != '?')) {
			return false, nil
		}
		if len(response) > 4096 {
			return false, nil
		}
		if hasKittyReply(response) {
			return true, nil
		}
		if hasDA1Reply(response) {
			return false, nil
		}
	}
	return false, nil
}

func hasKittyReply(response []byte) bool {
	start := bytes.Index(response, []byte("\x1b_Gi=31;"))
	if start < 0 {
		return false
	}
	return bytes.Index(response[start+len("\x1b_Gi=31;"):], []byte("\x1b\\")) >= 0
}

func hasDA1Reply(response []byte) bool {
	start := bytes.Index(response, []byte("\x1b[?"))
	if start < 0 {
		return false
	}
	return bytes.IndexByte(response[start+len("\x1b[?"):], 'c') >= 0
}

func ProbeKitty(input *os.File, output io.Writer, timeout time.Duration) (bool, error) {
	if input == nil || output == nil || !IsTerminal(input) {
		return false, nil
	}
	return DetectKitty(&ttyQueryTransport{input: input, output: output}, timeout)
}

type ttyQueryTransport struct {
	input  *os.File
	output io.Writer
}

func (transport *ttyQueryTransport) Begin() (func() error, error) {
	unread, err := unix.IoctlGetInt(int(transport.input.Fd()), unix.TIOCINQ)
	if err != nil || unread > 0 {
		return nil, ErrUnsafeQuery
	}
	state, err := term.MakeRaw(int(transport.input.Fd()))
	if err != nil {
		return nil, ErrUnsafeQuery
	}
	return func() error {
		return term.Restore(int(transport.input.Fd()), state)
	}, nil
}

func (transport *ttyQueryTransport) Write(data []byte) error {
	for len(data) > 0 {
		written, err := transport.output.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func (transport *ttyQueryTransport) ReadAvailableByte(timeout time.Duration) (byte, bool, error) {
	if timeout <= 0 {
		return 0, false, nil
	}
	milliseconds := int((timeout + time.Millisecond - 1) / time.Millisecond)
	fd := int32(transport.input.Fd())
	descriptor := []unix.PollFd{{Fd: fd, Events: unix.POLLIN}}
	count, err := unix.Poll(descriptor, milliseconds)
	if err != nil {
		return 0, false, err
	}
	if count == 0 {
		return 0, false, nil
	}
	if descriptor[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
		return 0, false, io.ErrUnexpectedEOF
	}
	var value [1]byte
	count, err = transport.input.Read(value[:])
	if err != nil {
		return 0, false, err
	}
	if count == 0 {
		return 0, false, io.ErrUnexpectedEOF
	}
	return value[0], true, nil
}
