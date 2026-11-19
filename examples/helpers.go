package examples

import (
	"fmt"
	"io"

	"github.com/moov-io/iso8583/network"
)

// ReadMessageLength reads a 2-byte binary message length header.
func ReadMessageLength(r io.Reader) (int, error) {
	header := network.NewBinary2BytesHeader()
	n, err := header.ReadFrom(r)
	if err != nil {
		return n, err
	}
	return header.Length(), nil
}

// WriteMessageLength writes a 2-byte binary message length header.
func WriteMessageLength(w io.Writer, length int) (int, error) {
	header := network.NewBinary2BytesHeader()
	header.SetLength(length)
	n, err := header.WriteTo(w)
	if err != nil {
		return n, fmt.Errorf("writing message header: %w", err)
	}
	return n, nil
}
