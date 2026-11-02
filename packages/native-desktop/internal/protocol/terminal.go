package protocol

import "errors"

type TerminalOpcode byte

const (
	Output   TerminalOpcode = 1
	Input    TerminalOpcode = 2
	Resize   TerminalOpcode = 3
	Snapshot TerminalOpcode = 4
	Restore  TerminalOpcode = 5
)

type TerminalFrame struct {
	Opcode  TerminalOpcode
	Slot    byte
	Payload []byte
}

func EncodeTerminalFrame(frame TerminalFrame) ([]byte, error) {
	if frame.Opcode < Output || frame.Opcode > Restore {
		return nil, errors.New("invalid terminal opcode")
	}
	result := make([]byte, 2+len(frame.Payload))
	result[0] = byte(frame.Opcode)
	result[1] = frame.Slot
	copy(result[2:], frame.Payload)
	return result, nil
}
func DecodeTerminalFrame(data []byte) (TerminalFrame, error) {
	if len(data) < 2 || data[0] < byte(Output) || data[0] > byte(Restore) {
		return TerminalFrame{}, errors.New("invalid terminal frame")
	}
	return TerminalFrame{Opcode: TerminalOpcode(data[0]), Slot: data[1], Payload: data[2:]}, nil
}
