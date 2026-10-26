package prediction

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	predictionv1 "aagasa/internal/gen/prediction/v1"
)

type Client struct {
	conn   *grpc.ClientConn
	client predictionv1.PredictionServiceClient
}

func Dial(address string) (*Client, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial prediction service: %w", err)
	}
	return &Client{conn: conn, client: predictionv1.NewPredictionServiceClient(conn)}, nil
}

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

func (c *Client) Version(ctx context.Context) (string, error) {
	response, err := c.client.Health(ctx, &predictionv1.HealthRequest{})
	if err != nil {
		return "", fmt.Errorf("prediction health: %w", err)
	}
	return response.GetVersion(), nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}
