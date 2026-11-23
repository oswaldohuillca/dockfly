package docker

import (
	"encoding/binary"
	"errors"
	"io"
)

// Demux splits Docker's multiplexed log stream (no TTY): every frame carries
// an 8-byte header with the kind (stdout/stderr) and the size. Both are
// written to w, in the order they arrive.
func Demux(r io.Reader, w io.Writer) error {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		size := int64(binary.BigEndian.Uint32(hdr[4:]))
		if _, err := io.CopyN(w, r, size); err != nil {
			return err
		}
	}
}
