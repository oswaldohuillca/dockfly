package docker

import (
	"encoding/binary"
	"errors"
	"io"
)

// Demux separa el flujo multiplexado de logs de Docker (sin TTY): cada trama
// lleva una cabecera de 8 bytes con el tipo (stdout/stderr) y el tamaño.
// Ambos se escriben en w, en el orden en que llegan.
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
