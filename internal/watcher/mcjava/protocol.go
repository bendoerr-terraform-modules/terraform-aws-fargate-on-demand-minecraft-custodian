// Package mcjava implements the Minecraft Java Edition Server List Ping (status) protocol.
package mcjava

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	segmentBits    = 0x7f
	continueBit    = 0x80
	bitsPerSegment = 7
	maxVarIntBytes = 5

	packetIDHandshake      = 0x00
	packetIDStatusRequest  = 0x00
	packetIDStatusResponse = 0x00

	protocolVersionAny = -1
	nextStateStatus    = 1
)

// ErrVarIntTooLong reports a VarInt longer than the protocol's 5-byte maximum.
var ErrVarIntTooLong = errors.New("mcjava: varint is longer than 5 bytes")

// AppendVarInt appends v in the protocol's VarInt encoding.
func AppendVarInt(b []byte, v int32) []byte {
	u := uint32(v) //nolint:gosec // G115: two's-complement reinterpretation is the wire format.
	for u >= continueBit {
		b = append(b, byte(u&segmentBits)|continueBit)
		u >>= bitsPerSegment
	}
	return append(b, byte(u))
}

// ReadVarInt reads one VarInt.
func ReadVarInt(r io.ByteReader) (int32, error) {
	var u uint32
	for i := range maxVarIntBytes {
		b, err := r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("mcjava: read varint: %w", err)
		}
		u |= uint32(b&segmentBits) << (bitsPerSegment * i)
		if b&continueBit == 0 {
			return int32(u), nil //nolint:gosec // G115: two's-complement reinterpretation is the wire format.
		}
	}
	return 0, ErrVarIntTooLong
}

// HandshakePacket builds the framed handshake that asks for the status state.
func HandshakePacket(host string, port uint16) []byte {
	payload := AppendVarInt(nil, protocolVersionAny)
	payload = appendString(payload, host)
	payload = binary.BigEndian.AppendUint16(payload, port)
	payload = AppendVarInt(payload, nextStateStatus)
	return frame(packetIDHandshake, payload)
}

// StatusRequestPacket builds the framed, empty status request.
func StatusRequestPacket() []byte {
	return frame(packetIDStatusRequest, nil)
}

// ReadStatusResponse reads one framed status response and returns its JSON payload.
func ReadStatusResponse(r *bufio.Reader, maxLen int) ([]byte, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	if length <= 0 || int(length) > maxLen {
		return nil, fmt.Errorf("mcjava: status response length %d outside 1..%d", length, maxLen)
	}
	body := make([]byte, length)
	if _, err = io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("mcjava: read status response: %w", err)
	}
	br := bytes.NewReader(body)
	id, err := ReadVarInt(br)
	if err != nil {
		return nil, err
	}
	if id != packetIDStatusResponse {
		return nil, fmt.Errorf("mcjava: unexpected packet id 0x%02x in status response", id)
	}
	strLen, err := ReadVarInt(br)
	if err != nil {
		return nil, err
	}
	if strLen < 0 || int(strLen) > br.Len() {
		return nil, fmt.Errorf("mcjava: status JSON length %d does not fit the packet body", strLen)
	}
	data := make([]byte, strLen)
	if _, err = io.ReadFull(br, data); err != nil {
		return nil, fmt.Errorf("mcjava: read status JSON: %w", err)
	}
	return data, nil
}

type statusResponse struct {
	Players *statusPlayers `json:"players"`
}

type statusPlayers struct {
	Online *int `json:"online"`
}

// ParseOnline extracts players.online from a status JSON document.
func ParseOnline(data []byte) (int, error) {
	var resp statusResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, fmt.Errorf("mcjava: decode status JSON: %w", err)
	}
	if resp.Players == nil || resp.Players.Online == nil {
		return 0, errors.New("mcjava: status JSON has no players.online")
	}
	if *resp.Players.Online < 0 {
		return 0, fmt.Errorf("mcjava: negative players.online %d", *resp.Players.Online)
	}
	return *resp.Players.Online, nil
}

func frame(id int32, payload []byte) []byte {
	body := AppendVarInt(nil, id)
	body = append(body, payload...)
	out := AppendVarInt(nil, int32(len(body))) //nolint:gosec // G115: packets we build are a few bytes.
	return append(out, body...)
}

func appendString(b []byte, s string) []byte {
	b = AppendVarInt(b, int32(len(s))) //nolint:gosec // G115: host names are far below 2^31 bytes.
	return append(b, s...)
}
