package connection

import "github.com/moov-io/iso8583"

func defaultMessageUnpacker(spec *iso8583.MessageSpec) func([]byte) (Message, error) {
	return func(rawMessage []byte) (Message, error) {
		message := iso8583.NewMessage(spec)
		err := message.Unpack(rawMessage)

		var m Message = message

		return m, err
	}
}
