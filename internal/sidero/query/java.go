package query

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func QueryJava(ctx context.Context, target Target, maximumSample int) (Result, error) {
	if target.Port < 1 || target.Port > 65535 || target.Host == "" {
		return Result{}, ErrUnsupported
	}
	started := time.Now()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))
	if err != nil {
		return Result{}, normalizeNetworkError(ctx, err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	host := []byte(target.Host)
	handshake := append(writeVarInt(0), writeVarInt(-1)...)
	handshake = append(handshake, writeVarInt(len(host))...)
	handshake = append(handshake, host...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, uint16(target.Port))
	handshake = append(handshake, port...)
	handshake = append(handshake, writeVarInt(1)...)
	packet := append(writeVarInt(len(handshake)), handshake...)
	packet = append(packet, 1, 0)
	if _, err := connection.Write(packet); err != nil {
		return Result{}, normalizeNetworkError(ctx, err)
	}
	reader := bufio.NewReader(io.LimitReader(connection, 1<<20))
	packetLength, err := readVarInt(reader)
	if err != nil || packetLength < 2 || packetLength > 1<<20 {
		return Result{}, ErrMalformed
	}
	packetID, err := readVarInt(reader)
	if err != nil || packetID != 0 {
		return Result{}, ErrMalformed
	}
	jsonLength, err := readVarInt(reader)
	if err != nil || jsonLength < 2 || jsonLength > packetLength || jsonLength > 1<<20 {
		return Result{}, ErrMalformed
	}
	payload := make([]byte, jsonLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Result{}, normalizeNetworkError(ctx, err)
	}
	var response struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct {
			Online int `json:"online"`
			Max    int `json:"max"`
			Sample []struct {
				Name string `json:"name"`
			} `json:"sample"`
		} `json:"players"`
		Description any `json:"description"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return Result{}, ErrMalformed
	}
	sample := make([]string, 0, min(maximumSample, len(response.Players.Sample)))
	for i, player := range response.Players.Sample {
		if i >= maximumSample {
			break
		}
		sample = append(sample, sanitizeText(player.Name, 64))
	}
	return Result{Supported: true, Online: true, Provider: "minecraft-java", Players: Players{Online: max(response.Players.Online, 0), Maximum: max(response.Players.Max, 0), Sample: sample}, Version: sanitizeText(response.Version.Name, 128), Protocol: response.Version.Protocol, MOTD: sanitizeText(flattenDescription(response.Description), 512), LatencyMS: time.Since(started).Milliseconds(), QueriedAt: time.Now().UTC()}, nil
}

func writeVarInt(value int) []byte {
	u := uint32(value)
	out := make([]byte, 0, 5)
	for {
		b := byte(u & 0x7f)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if u == 0 {
			return out
		}
	}
}

func readVarInt(reader io.ByteReader) (int, error) {
	var result uint32
	for position := 0; position < 5; position++ {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint32(value&0x7f) << (7 * position)
		if value&0x80 == 0 {
			return int(int32(result)), nil
		}
	}
	return 0, ErrMalformed
}

func flattenDescription(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		var out strings.Builder
		if text, ok := typed["text"].(string); ok {
			out.WriteString(text)
		}
		if extras, ok := typed["extra"].([]any); ok {
			for _, extra := range extras {
				out.WriteString(flattenDescription(extra))
			}
		}
		return out.String()
	case []any:
		var out strings.Builder
		for _, item := range typed {
			out.WriteString(flattenDescription(item))
		}
		return out.String()
	default:
		return ""
	}
}

func sanitizeText(value string, maximum int) string {
	var out []rune
	skipFormat := false
	for _, r := range value {
		if skipFormat {
			skipFormat = false
			continue
		}
		if r == '§' {
			skipFormat = true
			continue
		}
		if unicode.IsControl(r) && r != '\n' {
			continue
		}
		out = append(out, r)
		if len(out) >= maximum {
			break
		}
	}
	return strings.TrimSpace(string(out))
}

func normalizeNetworkError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if networkError, ok := err.(net.Error); ok && networkError.Timeout() {
		return ErrTimeout
	}
	return ErrOffline
}
