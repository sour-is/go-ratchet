package mem

import (
	"context"

	"go.salty.im/ratchet/client"
	"go.sour.is/pkg/locker"
	"go.sour.is/pkg/math"
)

type logs map[string][]any

type MemSession struct {
	logs *locker.Locked[logs]
}

type SessionLogger interface {
	ReadLog(ctx context.Context, streamID string, after, count int64) ([]any, error)
}

func NewMemSession(c *client.Client) *MemSession {
	m := &MemSession{logs: locker.New(make(logs))}

	client.On(c, func(ctx context.Context, args client.OnOfferSent) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnOfferReceived) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnSessionStarted) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnSessionClosed) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnMessageReceived) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnMessageSent) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnSaltySent) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnSaltyTextReceived) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnSaltyEventReceived) { m.Update(ctx, args) })
	client.On(c, func(ctx context.Context, args client.OnReceived) { m.Update(ctx, args) })

	return m
}

func (m *MemSession) ReadLog(ctx context.Context, streamID string, after, count int64) ([]any, error) {
	var lis []any
	err := m.logs.Use(ctx, func(ctx context.Context, l logs) error {
		stream, ok := l[streamID]
		if !ok || len(stream) == 0 {
			return nil
		}

		var first uint64 = 1
		var last uint64 = uint64(len(stream))
		// ---
		if last == 0 {
			return nil
		}

		start, count := math.PagerBox(first, last, after, count)
		if count == 0 {
			return nil
		}

		lis = make([]any, math.Abs(count))
		for i := range lis {

			// --- clone event
			var err error
			lis[i] = stream[start-1]
			if err != nil {
				return err
			}
			// ---

			if count > 0 {
				start += 1
			} else {
				start -= 1
			}
			if start < first || start > last {
				lis = lis[:i+1]
				break
			}
		}
		return nil
	})
	return lis, err
}
func (m *MemSession) Update(ctx context.Context, args any) {
	_ = m.logs.Use(ctx, func(ctx context.Context, l logs) error {
		switch msg := args.(type) {
		// case client.OnOfferSent:
		// case client.OnOfferReceived:
		case client.OnSessionStarted:
			log := l["user:"+msg.Them]
			log = append(log, msg)
			l["user:"+msg.Them] = log
		case client.OnSessionClosed:
			log := l["user:"+msg.Them]
			log = append(log, msg)
			l["user:"+msg.Them] = log
		case client.OnMessageReceived:
			log := l["user:"+msg.Them]
			log = append(log, msg)
			l["user:"+msg.Them] = log
		case client.OnMessageSent:
			log := l["user:"+msg.Them]
			log = append(log, msg)
			l["user:"+msg.Them] = log
		case client.OnSaltySent:
			log := l["user:"+msg.Them]
			log = append(log, msg)
			l["user:"+msg.Them] = log
		case client.OnSaltyTextReceived:
			log := l["user:"+msg.Msg.User.Nick]
			log = append(log, msg)
			l["user:"+msg.Msg.User.Nick] = log
		// case client.OnSaltyEventReceived:
		case client.OnReceived:
		default:
			log := l["system"]
			log = append(log, msg)
			l["system"] = log
		}
		return nil
	})
}
