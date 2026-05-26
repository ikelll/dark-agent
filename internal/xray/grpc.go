package xray

import (
	"context"
	"fmt"
	"time"

	proxycmd "github.com/xtls/xray-core/app/proxyman/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	vless "github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GRPCClient wraps the Xray HandlerService and StatsService gRPC clients.
type GRPCClient struct {
	conn    *grpc.ClientConn
	handler proxycmd.HandlerServiceClient
	stats   statscmd.StatsServiceClient
}

// NewGRPCClient dials the local Xray gRPC API (addr = "127.0.0.1:10085").
// Returns an error if the connection cannot be established within 3 seconds.
func NewGRPCClient(addr string) (*GRPCClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, //nolint:staticcheck // DialContext is still the standard way before grpc v2
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to xray gRPC at %s: %w", addr, err)
	}
	return &GRPCClient{
		conn:    conn,
		handler: proxycmd.NewHandlerServiceClient(conn),
		stats:   statscmd.NewStatsServiceClient(conn),
	}, nil
}

// Close releases the gRPC connection.
func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

// AddUser adds a VLESS user to a running Xray inbound via HandlerService.AlterInbound.
// Does NOT restart or reload Xray — purely runtime operation.
func (c *GRPCClient) AddUser(inboundTag, uuid, email, flow string) error {
	account := &vless.Account{
		Id:   uuid,
		Flow: flow,
	}
	user := &protocol.User{
		Email:   email,
		Account: serial.ToTypedMessage(account),
	}
	op := serial.ToTypedMessage(&proxycmd.AddUserOperation{User: user})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.handler.AlterInbound(ctx, &proxycmd.AlterInboundRequest{
		Tag:       inboundTag,
		Operation: op,
	})
	if err != nil {
		return fmt.Errorf("HandlerService.AddUser inbound=%s email=%s: %w", inboundTag, email, err)
	}
	return nil
}

// RemoveUser removes a VLESS user from a running Xray inbound by email identifier.
// Does NOT restart or reload Xray — purely runtime operation.
func (c *GRPCClient) RemoveUser(inboundTag, email string) error {
	op := serial.ToTypedMessage(&proxycmd.RemoveUserOperation{Email: email})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.handler.AlterInbound(ctx, &proxycmd.AlterInboundRequest{
		Tag:       inboundTag,
		Operation: op,
	})
	if err != nil {
		return fmt.Errorf("HandlerService.RemoveUser inbound=%s email=%s: %w", inboundTag, email, err)
	}
	return nil
}

// QueryStats queries Xray StatsService for stats matching the given pattern.
// Returns a map of stat name → value (bytes). Returns nil map if unavailable.
func (c *GRPCClient) QueryStats(pattern string, reset bool) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.stats.QueryStats(ctx, &statscmd.QueryStatsRequest{
		Pattern: pattern,
		Reset_:  reset,
	})
	if err != nil {
		return nil, fmt.Errorf("StatsService.QueryStats: %w", err)
	}

	result := make(map[string]int64, len(resp.Stat))
	for _, s := range resp.Stat {
		result[s.Name] = s.Value
	}
	return result, nil
}
