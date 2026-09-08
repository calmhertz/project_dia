// Package prediction is the Go client for the internal Python satellite
// computation service (spec.md section 5.2).
package prediction

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	predictionv1 "aagasa/internal/gen/prediction/v1"
)

// Client talks to the Python prediction service over gRPC.
type Client struct {
	conn   *grpc.ClientConn
	client predictionv1.PredictionServiceClient
}

// Dial creates a lazy connection to the prediction service. The service is
// internal to the deployment, so transport credentials are not used.
func Dial(address string) (*Client, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial prediction service: %w", err)
	}
	return &Client{conn: conn, client: predictionv1.NewPredictionServiceClient(conn)}, nil
}

// Health reports whether the prediction service is serving.
func (c *Client) Health(ctx context.Context) error {
	response, err := c.client.Health(ctx, &predictionv1.HealthRequest{})
	if err != nil {
		return fmt.Errorf("prediction health: %w", err)
	}
	if response.GetStatus() != predictionv1.HealthResponse_STATUS_SERVING {
		return fmt.Errorf("prediction service not serving: %s", response.GetStatus())
	}
	return nil
}

// Version returns the prediction service build version.
func (c *Client) Version(ctx context.Context) (string, error) {
	response, err := c.client.Health(ctx, &predictionv1.HealthRequest{})
	if err != nil {
		return "", fmt.Errorf("prediction health: %w", err)
	}
	return response.GetVersion(), nil
}

// Close releases the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
