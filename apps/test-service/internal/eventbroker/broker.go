package eventbroker

import (
	"context"
	"errors"
	"sync"

	"unit-test-ide.local/test-service/internal/task"
)

type Source interface {
	Watermark(context.Context) (int64, error)
	EventsAfter(context.Context, int64, int64, int) ([]task.Event, error)
}

var (
	ErrSubscriberTooSlow = errors.New("subscriber too slow")
	ErrInvalidCursor     = errors.New("invalid event cursor")

	errInvalidConfiguration = errors.New("invalid event broker configuration")
	errReadWatermark        = errors.New("read event watermark")
	errReplayFailed         = errors.New("event replay failed")
	errNonIncreasingPublish = errors.New("eventbroker: non-increasing published sequence")
)

type Subscription struct {
	Events <-chan task.Event
	Errors <-chan error
	close  func()
}

func (s *Subscription) Close() {
	if s != nil && s.close != nil {
		s.close()
	}
}

type subscriber struct {
	id       uint64
	after    int64
	buffer   []task.Event
	live     bool
	out      chan task.Event
	errs     chan error
	done     <-chan struct{}
	cancel   context.CancelFunc
	terminal error
}

type Broker struct {
	mu sync.Mutex

	source        Source
	queueSize     int
	pageSize      int
	nextID        uint64
	lastPublished int64
	subscribers   map[uint64]*subscriber
}

func New(source Source, queueSize, pageSize int) (*Broker, error) {
	if source == nil || queueSize < 1 || pageSize < 1 {
		return nil, errInvalidConfiguration
	}
	return &Broker{
		source:      source,
		queueSize:   queueSize,
		pageSize:    pageSize,
		subscribers: make(map[uint64]*subscriber),
	}, nil
}

func (b *Broker) Publish(event task.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if event.Sequence <= b.lastPublished {
		b.reportLocked(errNonIncreasingPublish)
		return
	}
	b.lastPublished = event.Sequence
	for _, subscriber := range b.subscribers {
		if event.Sequence <= subscriber.after {
			continue
		}
		if subscriber.live {
			b.sendLocked(subscriber, event)
			continue
		}
		if len(subscriber.buffer) == b.queueSize {
			b.dropLocked(subscriber, ErrSubscriberTooSlow)
			continue
		}
		subscriber.buffer = append(subscriber.buffer, copyEvent(event))
	}
}

func (b *Broker) Subscribe(ctx context.Context, afterSequence int64) (*Subscription, error) {
	if afterSequence < 0 {
		return nil, ErrInvalidCursor
	}

	subscriptionContext, cancel := context.WithCancel(ctx)
	subscriber := &subscriber{
		after:  afterSequence,
		buffer: make([]task.Event, 0, b.queueSize),
		out:    make(chan task.Event, b.queueSize),
		errs:   make(chan error, 1),
		done:   subscriptionContext.Done(),
		cancel: cancel,
	}
	b.mu.Lock()
	b.nextID++
	subscriber.id = b.nextID
	b.subscribers[subscriber.id] = subscriber
	b.mu.Unlock()

	watermark, err := b.source.Watermark(subscriptionContext)
	if err != nil {
		err = b.setupError(ctx, subscriber, errReadWatermark)
		b.remove(subscriber, nil)
		return nil, err
	}
	if err := subscriptionContext.Err(); err != nil {
		err = b.setupError(ctx, subscriber, err)
		b.remove(subscriber, nil)
		return nil, err
	}
	if afterSequence > watermark {
		b.remove(subscriber, nil)
		return nil, ErrInvalidCursor
	}

	subscription := &Subscription{
		Events: subscriber.out,
		Errors: subscriber.errs,
		close: func() {
			b.remove(subscriber, nil)
		},
	}
	go b.replay(subscriptionContext, subscriber, watermark)
	go func() {
		<-subscriptionContext.Done()
		b.remove(subscriber, nil)
	}()
	return subscription, nil
}

func (b *Broker) replay(ctx context.Context, subscriber *subscriber, watermark int64) {
	cursor := subscriber.after
	for cursor < watermark {
		page, err := b.source.EventsAfter(ctx, cursor, watermark, b.pageSize)
		if err != nil {
			if ctx.Err() != nil {
				b.remove(subscriber, nil)
			} else {
				b.remove(subscriber, errReplayFailed)
			}
			return
		}
		if len(page) == 0 {
			b.remove(subscriber, errReplayFailed)
			return
		}

		previousCursor := cursor
		for _, event := range page {
			if event.Sequence <= cursor {
				continue
			}
			if event.Sequence > watermark {
				b.remove(subscriber, errReplayFailed)
				return
			}
			cursor = event.Sequence
			if !b.send(subscriber, event) {
				return
			}
		}
		if cursor == previousCursor {
			b.remove(subscriber, errReplayFailed)
			return
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.registeredLocked(subscriber) {
		return
	}
	for _, event := range subscriber.buffer {
		if event.Sequence <= subscriber.after {
			continue
		}
		if !b.sendLocked(subscriber, event) {
			return
		}
	}
	subscriber.buffer = nil
	subscriber.live = true
}

func (b *Broker) send(subscriber *subscriber, event task.Event) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.registeredLocked(subscriber) {
		return false
	}
	return b.sendLocked(subscriber, event)
}

func (b *Broker) sendLocked(subscriber *subscriber, event task.Event) bool {
	if event.Sequence <= subscriber.after {
		return true
	}
	select {
	case <-subscriber.done:
		b.dropLocked(subscriber, nil)
		return false
	default:
	}
	value := copyEvent(event)
	select {
	case <-subscriber.done:
		b.dropLocked(subscriber, nil)
		return false
	case subscriber.out <- value:
		subscriber.after = event.Sequence
		return true
	default:
		b.dropLocked(subscriber, ErrSubscriberTooSlow)
		return false
	}
}

func (b *Broker) reportLocked(err error) {
	for _, subscriber := range b.subscribers {
		select {
		case subscriber.errs <- err:
		default:
		}
	}
}

func (b *Broker) setupError(ctx context.Context, subscriber *subscriber, sourceError error) error {
	b.mu.Lock()
	terminal := subscriber.terminal
	b.mu.Unlock()
	if errors.Is(terminal, ErrSubscriberTooSlow) {
		return terminal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if terminal != nil {
		return terminal
	}
	return sourceError
}

func (b *Broker) remove(subscriber *subscriber, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.registeredLocked(subscriber) {
		b.dropLocked(subscriber, err)
	}
}

func (b *Broker) registeredLocked(subscriber *subscriber) bool {
	current, ok := b.subscribers[subscriber.id]
	return ok && current == subscriber
}

func (b *Broker) dropLocked(subscriber *subscriber, err error) {
	subscriber.terminal = err
	delete(b.subscribers, subscriber.id)
	subscriber.cancel()
	if err != nil {
		select {
		case <-subscriber.errs:
		default:
		}
		subscriber.errs <- err
	}
	close(subscriber.out)
	close(subscriber.errs)
}

func copyEvent(event task.Event) task.Event {
	event.Payload = append([]byte(nil), event.Payload...)
	return event
}
