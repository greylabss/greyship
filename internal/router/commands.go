package router

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/sched"
	"greyship.tech/internal/store"
)

// QueueCommands sends the router's requests through the job queue: pause and resume to the instance's node
// agent, scale-ups to the scheduler.
type QueueCommands struct {
	Pool *pgxpool.Pool
}

func (c QueueCommands) Pause(ctx context.Context, instanceID, nodeID string) error {
	return sched.SendCommand(ctx, store.New(c.Pool), nodeID, sched.Command{Action: "instance.pause", InstanceID: instanceID})
}

func (c QueueCommands) Resume(ctx context.Context, instanceID, nodeID string) error {
	return sched.SendCommand(ctx, store.New(c.Pool), nodeID, sched.Command{Action: "instance.resume", InstanceID: instanceID})
}

func (c QueueCommands) ScaleUp(ctx context.Context, serviceID, reason string) error {
	return c.send(ctx, bus.ScaleUp, sched.ScaleRequest{ServiceID: serviceID, Reason: reason})
}

func (c QueueCommands) send(ctx context.Context, kind string, payload any) error {
	_, err := bus.Enqueue(ctx, store.New(c.Pool), kind, payload)
	return err
}
