package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/cmd/iso8583/describe"
)

func main() {
	var rawMessage []byte

	rawMessage = append(rawMessage, []byte(`0100`)...)

	// Let's recover bitmap. We know that:
	// * Bit-map=F23E440108E18062 (primary)
	// * Xbit-map=0000000000000020 (secondary)
	s := "F23E440108E18062" + "0000000000000020"
	// by converting this hex into bytes we will get the original bitmap.
	binaryBitmap, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	// Having binary bitmap, we can add it to the message:
	rawMessage = append(rawMessage, binaryBitmap...)

	// reconstruct the rest parts of the message from the documentation
	rawMessage = append(rawMessage, []byte(`16531278xxxxxx545800000000000015900010052054184197`)...)
	rawMessage = append(rawMessage, []byte(`392054181005XXXX100550470121159840511034127800419739rbsYFXKS2700820000`)...)
	rawMessage = append(rawMessage, []byte(`000147643 Jolly Lane        Brooklyn ParkMNUS025IAU Dental            `)...)
	rawMessage = append(rawMessage, []byte(`   840011101110072560172700055428    84000540MCI012TDCV0511XXX        `)...)

	message := iso8583.NewMessage(brandSpec)

	message.Unpack(rawMessage)

	err = describe.Message(os.Stdout, message)
	if err != nil {
		panic(fmt.Sprintf("describing message: %w", err))
	}
}
