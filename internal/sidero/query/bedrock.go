package query

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"time"
)

var bedrockMagic = []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

func QueryBedrock(ctx context.Context, target Target) (Result, error) {
	if target.Port < 1 || target.Port > 65535 || target.Host == "" {
		return Result{}, ErrUnsupported
	}
	started := time.Now()
	connection, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))
	if err != nil {
		return Result{}, normalizeNetworkError(ctx, err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	packet := make([]byte, 33)
	packet[0] = 0x01
	binary.BigEndian.PutUint64(packet[1:9], uint64(time.Now().UnixMilli()))
	copy(packet[9:25], bedrockMagic)
	binary.BigEndian.PutUint64(packet[25:33], 1)
	if _, err := connection.Write(packet); err != nil {
		return Result{}, normalizeNetworkError(ctx, err)
	}
	response := make([]byte, 4096)
	n, err := connection.Read(response)
	if err != nil {
		return Result{}, normalizeNetworkError(ctx, err)
	}
	if n < 35 || response[0] != 0x1c || !equalBytes(response[17:33], bedrockMagic) {
		return Result{}, ErrMalformed
	}
	length := int(binary.BigEndian.Uint16(response[33:35]))
	if length < 1 || length > n-35 {
		return Result{}, ErrMalformed
	}
	fields := strings.Split(string(response[35:35+length]), ";")
	if len(fields) < 6 || fields[0] != "MCPE" {
		return Result{}, ErrMalformed
	}
	protocol, err1 := strconv.Atoi(fields[2])
	online, err2 := strconv.Atoi(fields[4])
	maximum, err3 := strconv.Atoi(fields[5])
	if err1 != nil || err2 != nil || err3 != nil {
		return Result{}, ErrMalformed
	}
	motd := fields[1]
	if len(fields) > 7 && fields[7] != "" {
		motd += "\n" + fields[7]
	}
	return Result{Supported: true, Online: true, Provider: "minecraft-bedrock", Players: Players{Online: max(online, 0), Maximum: max(maximum, 0), Sample: []string{}}, Version: sanitizeText(fields[3], 128), Protocol: protocol, MOTD: sanitizeText(motd, 512), LatencyMS: time.Since(started).Milliseconds(), QueriedAt: time.Now().UTC()}, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}
