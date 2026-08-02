package query

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMinecraftJavaQuery(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go serveJavaFixture(listener)
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	result, err := QueryJava(context.Background(), Target{Host: host, Port: port}, 5)
	require.NoError(t, err)
	require.True(t, result.Online)
	require.Equal(t, "minecraft-java", result.Provider)
	require.Equal(t, 2, result.Players.Online)
	require.Equal(t, "Fixture Server", result.MOTD)
}

func TestMinecraftBedrockQuery(t *testing.T) {
	connection, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer connection.Close()
	go serveBedrockFixture(connection)
	host, portText, err := net.SplitHostPort(connection.LocalAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	result, err := QueryBedrock(context.Background(), Target{Host: host, Port: port})
	require.NoError(t, err)
	require.True(t, result.Online)
	require.Equal(t, "minecraft-bedrock", result.Provider)
	require.Equal(t, 3, result.Players.Online)
	require.Equal(t, "Bedrock Fixture\nSub MOTD", result.MOTD)
}

func TestManagerCachesAndCoalesces(t *testing.T) {
	var calls atomic.Int64
	m := NewManager(Config{Timeout: time.Second, Cache: time.Minute, Stale: time.Minute, OfflineBackoff: time.Second, MaximumRetries: 0}, func(ctx context.Context, provider string, target Target) (Result, error) {
		calls.Add(1)
		return Result{Supported: true, Online: true, Provider: provider, QueriedAt: time.Now().UTC()}, nil
	})
	target := Target{Host: "127.0.0.1", Port: 25565}
	_, err := m.Query(context.Background(), "server", 1, "minecraft-java", target)
	require.NoError(t, err)
	_, err = m.Query(context.Background(), "server", 1, "minecraft-java", target)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
}

func TestManagerReturnsStaleResultAndAppliesOfflineBackoff(t *testing.T) {
	var calls atomic.Int64
	m := NewManager(Config{Timeout: time.Second, Cache: time.Millisecond, Stale: time.Minute, OfflineBackoff: time.Minute}, func(context.Context, string, Target) (Result, error) {
		if calls.Add(1) == 1 {
			return Result{Supported: true, Online: true, Provider: "minecraft-java", QueriedAt: time.Now().UTC()}, nil
		}
		return Result{}, ErrOffline
	})
	target := Target{Host: "127.0.0.1", Port: 25565}
	_, err := m.Query(context.Background(), "server", 1, "minecraft-java", target)
	require.NoError(t, err)
	time.Sleep(3 * time.Millisecond)
	stale, err := m.Query(context.Background(), "server", 1, "minecraft-java", target)
	require.NoError(t, err)
	require.True(t, stale.Stale)
	_, err = m.Query(context.Background(), "server", 1, "minecraft-java", target)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
}

func TestManagerNormalizesProviderTimeout(t *testing.T) {
	m := NewManager(Config{Timeout: time.Millisecond, Cache: time.Second, Stale: time.Second, OfflineBackoff: time.Second}, func(ctx context.Context, _ string, _ Target) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	_, err := m.Query(context.Background(), "server", 1, "minecraft-java", Target{Host: "127.0.0.1", Port: 25565})
	require.ErrorIs(t, err, ErrTimeout)
}

func serveJavaFixture(listener net.Listener) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	_, _ = readVarInt(reader)
	_, _ = readVarInt(reader)
	_, _ = readVarInt(reader)
	hostLength, _ := readVarInt(reader)
	_, _ = io.CopyN(io.Discard, reader, int64(hostLength)+3)
	_, _ = readVarInt(reader)
	_, _ = readVarInt(reader)
	payload, _ := json.Marshal(map[string]any{"version": map[string]any{"name": "1.21.4", "protocol": 769}, "players": map[string]any{"online": 2, "max": 20, "sample": []map[string]string{{"name": "Player"}}}, "description": map[string]any{"text": "Fixture Server"}})
	packet := append(writeVarInt(0), writeVarInt(len(payload))...)
	packet = append(packet, payload...)
	response := append(writeVarInt(len(packet)), packet...)
	_, _ = connection.Write(response)
}

func serveBedrockFixture(connection net.PacketConn) {
	buffer := make([]byte, 1024)
	n, address, err := connection.ReadFrom(buffer)
	if err != nil || n < 9 {
		return
	}
	message := []byte("MCPE;Bedrock Fixture;671;1.21.0;3;10;id;Sub MOTD;Survival;1;19132;19133;")
	response := make([]byte, 0, 35+len(message))
	response = append(response, 0x1c)
	response = append(response, buffer[1:9]...)
	response = append(response, make([]byte, 8)...)
	response = append(response, bedrockMagic...)
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(message)))
	response = append(response, length...)
	response = append(response, message...)
	_, _ = connection.WriteTo(response, address)
}
