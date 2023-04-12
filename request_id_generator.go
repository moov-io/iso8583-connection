package connection

import (
	"errors"
	"fmt"

	"github.com/moov-io/iso8583"
)

// RequestIDGenerator is an interface that generates a unique identifier for a
// request so that responses from the server can be matched to the original
// request.
type RequestIDGenerator interface {
	GenerateRequestID(msg *iso8583.Message) (string, error)
}

// defaultRequestIDGenerator is the default implementation of RequestIDGenerator
// that uses the STAN (field 11) of the message as the request ID.
type defaultRequestIDGenerator struct{}

func (d *defaultRequestIDGenerator) GenerateRequestID(message *iso8583.Message) (string, error) {
	if message == nil {
		return "", fmt.Errorf("message required")
	}

	stan, err := message.GetString(11)
	if err != nil {
		return "", fmt.Errorf("getting STAN (field 11) of the message: %w", err)
	}

	if stan == "" {
		return "", errors.New("STAN is missing")
	}

	return stan, nil
}
