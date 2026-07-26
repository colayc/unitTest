package eventbroker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

const testTimeout = 5 * time.Second

type fakeSource struct {
	mu sync.Mutex

	events         []task.Event
	watermarkErr   error
	replayErr      error
	emptyReplay    bool
	blockWatermark bool
	blockReplay    bool
	watermarkStart chan struct{}
	watermarkGate  chan struct{}
	watermarkOnce  sync.Once
	replayStart    chan struct{}
	replayGate     chan struct{}
	replayOnce     sync.Once
	calls          [][3]int64
}

func newFakeSource(initial []task.Event) *fakeSource {
	return &fakeSource{
		events:         append([]task.Event(nil), initial...),
		watermarkStart: make(chan struct{}),
		watermarkGate:  make(chan struct{}),
		replayStart:    make(chan struct{}),
		replayGate:     make(chan struct{}),
	}
}

func newBlockingFakeSource(initial []task.Event) *fakeSource {
	source := newFakeSource(initial)
	source.blockReplay = true
	return source
}

func (s *fakeSource) Watermark(ctx context.Context) (int64, error) {
	s.mu.Lock()
	block := s.blockWatermark
	watermarkErr := s.watermarkErr
	watermark := int64(0)
	if len(s.events) != 0 {
		watermark = s.events[len(s.events)-1].Sequence
	}
	s.mu.Unlock()

	s.watermarkOnce.Do(func() { close(s.watermarkStart) })
	if block {
		select {
		case <-s.watermarkGate:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if watermarkErr != nil {
		return 0, watermarkErr
	}
	return watermark, nil
}

func (s *fakeSource) EventsAfter(ctx context.Context, after, through int64, limit int) ([]task.Event, error) {
	s.mu.Lock()
	s.calls = append(s.calls, [3]int64{after, through, int64(limit)})
	block := s.blockReplay
	replayErr := s.replayErr
	emptyReplay := s.emptyReplay
	s.mu.Unlock()

	s.replayOnce.Do(func() { close(s.replayStart) })
	if block {
		select {
		case <-s.replayGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if replayErr != nil {
		return nil, replayErr
	}
	if emptyReplay {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	page := make([]task.Event, 0, limit)
	for _, event := range s.events {
		if event.Sequence > after && event.Sequence <= through {
			page = append(page, cloneEvent(event))
			if len(page) == limit {
				break
			}
		}
	}
	return page, nil
}

func (s *fakeSource) append(event task.Event) {
	s.mu.Lock()
	s.events = append(s.events, cloneEvent(event))
	s.mu.Unlock()
}

func (s *fakeSource) waitForReplayStart(t *testing.T) {
	t.Helper()
	select {
	case <-s.replayStart:
	case <-time.After(testTimeout):
		t.Fatal("replay did not start")
	}
}

func (s *fakeSource) waitForWatermarkStart(t *testing.T) {
	t.Helper()
	select {
	case <-s.watermarkStart:
	case <-time.After(testTimeout):
		t.Fatal("watermark read did not start")
	}
}

func (s *fakeSource) releaseReplay() {
	close(s.replayGate)
}

func (s *fakeSource) replayCalls() [][3]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][3]int64(nil), s.calls...)
}

func event(sequence int64) task.Event {
	return task.Event{
		Sequence: sequence,
		ID:       fmt.Sprintf("event-%d", sequence),
		EventDraft: task.EventDraft{
			TaskID:  "task-1",
			Type:    task.EventTaskOutput,
			Payload: []byte(fmt.Sprintf(`{"sequence":%d}`, sequence)),
		},
	}
}

func events(sequences ...int64) []task.Event {
	result := make([]task.Event, 0, len(sequences))
	for _, sequence := range sequences {
		result = append(result, event(sequence))
	}
	return result
}

func cloneEvent(value task.Event) task.Event {
	value.Payload = append([]byte(nil), value.Payload...)
	return value
}

func mustBroker(t *testing.T, source Source, queueSize, pageSize int) *Broker {
	t.Helper()
	broker, err := New(source, queueSize, pageSize)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return broker
}

func mustSubscribe(t *testing.T, broker *Broker, ctx context.Context, after int64) *Subscription {
	t.Helper()
	subscription, err := broker.Subscribe(ctx, after)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	subscription.Activate()
	return subscription
}

type subscriptionActivator interface{ Activate() }

func activateSubscription(t *testing.T, subscription *Subscription) {
	t.Helper()
	activator, ok := any(subscription).(subscriptionActivator)
	if !ok {
		t.Fatal("Subscription does not expose Activate")
	}
	activator.Activate()
}

func TestSubscribeReturnsPausedUntilActivate(t *testing.T) {
	source := newFakeSource(events(1, 2, 3, 4, 5, 6, 7, 8, 9, 10))
	broker := mustBroker(t, source, 2, 2)
	subscription, err := broker.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	time.Sleep(25 * time.Millisecond)
	if calls := source.replayCalls(); len(calls) != 0 {
		t.Fatalf("replay started before Activate: %v", calls)
	}
	select {
	case value := <-subscription.Events:
		t.Fatalf("event delivered before Activate: %#v", value)
	case err := <-subscription.Errors:
		t.Fatalf("error delivered before Activate: %v", err)
	default:
	}

	received := make(chan []int64, 1)
	go func() {
		sequences := make([]int64, 0, 10)
		for range 10 {
			value, ok := <-subscription.Events
			if !ok {
				break
			}
			sequences = append(sequences, value.Sequence)
		}
		received <- sequences
	}()
	activateSubscription(t, subscription)
	select {
	case sequences := <-received:
		if !reflect.DeepEqual(sequences, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
			t.Fatalf("sequences=%v", sequences)
		}
	case <-time.After(testTimeout):
		t.Fatal("activated replay did not complete")
	}
}

func TestSubscriptionActivateIsIdempotentAndCloseBeforeActivateStopsReplay(t *testing.T) {
	t.Run("idempotent", func(t *testing.T) {
		source := newFakeSource(events(1))
		broker := mustBroker(t, source, 8, 8)
		subscription, err := broker.Subscribe(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer subscription.Close()
		var callers sync.WaitGroup
		for range 20 {
			callers.Add(1)
			go func() { defer callers.Done(); activateSubscription(t, subscription) }()
		}
		callers.Wait()
		if got := readEvent(t, subscription.Events).Sequence; got != 1 {
			t.Fatalf("sequence=%d", got)
		}
		time.Sleep(25 * time.Millisecond)
		if calls := source.replayCalls(); len(calls) != 1 {
			t.Fatalf("replay calls=%v", calls)
		}
	})

	t.Run("close before activate", func(t *testing.T) {
		source := newFakeSource(events(1))
		broker := mustBroker(t, source, 8, 8)
		subscription, err := broker.Subscribe(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		subscription.Close()
		activateSubscription(t, subscription)
		time.Sleep(25 * time.Millisecond)
		if calls := source.replayCalls(); len(calls) != 0 {
			t.Fatalf("replay started after Close: %v", calls)
		}
		broker.mu.Lock()
		registered := len(broker.subscribers)
		broker.mu.Unlock()
		if registered != 0 {
			t.Fatalf("subscribers after Close-before-Activate=%d", registered)
		}
		requireClosed(t, subscription.Events)
		requireClosed(t, subscription.Errors)
	})
}

func readEvent(t *testing.T, events <-chan task.Event) task.Event {
	t.Helper()
	select {
	case value, ok := <-events:
		if !ok {
			t.Fatal("events channel closed early")
		}
		return value
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for event")
		return task.Event{}
	}
}

func readSequences(t *testing.T, events <-chan task.Event, count int) []int64 {
	t.Helper()
	sequences := make([]int64, 0, count)
	for range count {
		sequences = append(sequences, readEvent(t, events).Sequence)
	}
	return sequences
}

func readError(t *testing.T, errs <-chan error) error {
	t.Helper()
	select {
	case err, ok := <-errs:
		if !ok {
			t.Fatal("errors channel closed before reporting an error")
		}
		return err
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for error")
		return nil
	}
}

func requireClosed[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	select {
	case _, ok := <-channel:
		if ok {
			t.Fatal("channel remained open")
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for channel close")
	}
}

func waitForLiveSubscribers(t *testing.T, broker *Broker, count int) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		broker.mu.Lock()
		live := 0
		for _, subscriber := range broker.subscribers {
			if subscriber.live {
				live++
			}
		}
		broker.mu.Unlock()
		if live == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("live subscribers did not reach %d", count)
}

func TestSubscribeBridgesReplayAndLiveWithoutGap(t *testing.T) {
	source := newBlockingFakeSource(events(1, 2))
	broker := mustBroker(t, source, 8, 2)
	subscription := mustSubscribe(t, broker, context.Background(), 0)
	defer subscription.Close()

	source.waitForReplayStart(t)
	source.append(event(3))
	broker.Publish(event(3))
	source.releaseReplay()

	if got := readSequences(t, subscription.Events, 3); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("sequences = %v", got)
	}
}

func TestSubscribeAtWatermarkReceivesOnlyLiveEvents(t *testing.T) {
	broker := mustBroker(t, newFakeSource(events(1, 2)), 4, 2)
	subscription := mustSubscribe(t, broker, context.Background(), 2)
	defer subscription.Close()

	broker.Publish(event(3))
	if got := readSequences(t, subscription.Events, 1); !reflect.DeepEqual(got, []int64{3}) {
		t.Fatalf("sequences = %v", got)
	}
}

func TestSubscribeRejectsInvalidCursor(t *testing.T) {
	broker := mustBroker(t, newFakeSource(events(1, 2)), 4, 2)
	for _, after := range []int64{-1, 3} {
		if _, err := broker.Subscribe(context.Background(), after); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("Subscribe(after=%d) error = %v, want ErrInvalidCursor", after, err)
		}
	}
}

func TestSubscribeDeduplicatesReplayAndBufferedPublish(t *testing.T) {
	source := newBlockingFakeSource(events(1, 2))
	broker := mustBroker(t, source, 8, 2)
	subscription := mustSubscribe(t, broker, context.Background(), 0)
	defer subscription.Close()

	source.waitForReplayStart(t)
	broker.Publish(event(2))
	source.releaseReplay()
	broker.Publish(event(3))

	if got := readSequences(t, subscription.Events, 3); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("sequences = %v", got)
	}
}

func TestReplayUsesFixedWatermarkAndPagesInGlobalOrder(t *testing.T) {
	source := newFakeSource(events(1, 2, 3, 4, 5))
	broker := mustBroker(t, source, 8, 2)
	subscription := mustSubscribe(t, broker, context.Background(), 0)
	defer subscription.Close()

	if got := readSequences(t, subscription.Events, 5); !reflect.DeepEqual(got, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("sequences = %v", got)
	}
	wantCalls := [][3]int64{{0, 5, 2}, {2, 5, 2}, {4, 5, 2}}
	if got := source.replayCalls(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("EventsAfter calls = %v, want %v", got, wantCalls)
	}
}

func TestPublishIgnoresNonIncreasingSequenceAndReportsInternalError(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 8, 2)
	subscription := mustSubscribe(t, broker, context.Background(), 0)
	defer subscription.Close()

	broker.Publish(event(2))
	broker.Publish(event(1))
	broker.Publish(event(2))
	broker.Publish(event(3))

	if got := readSequences(t, subscription.Events, 2); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("sequences = %v", got)
	}
	if err := readError(t, subscription.Errors); err == nil || !strings.Contains(err.Error(), "non-increasing") {
		t.Fatalf("reported error = %v", err)
	}
}

func TestSlowSubscriberDoesNotBlockPublisherOrFastSubscriber(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 1, 1)
	slow := mustSubscribe(t, broker, context.Background(), 0)
	defer slow.Close()
	fast := mustSubscribe(t, broker, context.Background(), 0)
	defer fast.Close()
	waitForLiveSubscribers(t, broker, 2)

	broker.Publish(event(1))
	if got := readEvent(t, fast.Events).Sequence; got != 1 {
		t.Fatalf("fast sequence = %d, want 1", got)
	}

	published := make(chan struct{})
	go func() {
		broker.Publish(event(2))
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(testTimeout):
		t.Fatal("Publish blocked behind slow subscriber")
	}

	if got := readEvent(t, fast.Events).Sequence; got != 2 {
		t.Fatalf("fast sequence = %d, want 2", got)
	}
	if err := readError(t, slow.Errors); !errors.Is(err, ErrSubscriberTooSlow) {
		t.Fatalf("slow error = %v, want ErrSubscriberTooSlow", err)
	}
	for {
		select {
		case buffered, ok := <-slow.Events:
			if !ok {
				goto slowEventsClosed
			}
			if buffered.Sequence != 1 {
				t.Fatalf("slow buffered sequence = %d, want 1", buffered.Sequence)
			}
		case <-time.After(testTimeout):
			t.Fatal("timed out waiting for slow events channel close")
		}
	}

slowEventsClosed:
	requireClosed(t, slow.Errors)

	broker.Publish(event(3))
	if got := readEvent(t, fast.Events).Sequence; got != 3 {
		t.Fatalf("fast sequence after slow drop = %d, want 3", got)
	}
}

func TestSlowSubscriberReceivesTerminalOverflowAfterEarlierInternalError(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 1, 1)
	subscription := mustSubscribe(t, broker, context.Background(), 0)
	defer subscription.Close()
	waitForLiveSubscribers(t, broker, 1)

	broker.Publish(event(1))
	broker.Publish(event(1))
	broker.Publish(event(2))

	if err := readError(t, subscription.Errors); !errors.Is(err, ErrSubscriberTooSlow) {
		t.Fatalf("terminal error = %v, want ErrSubscriberTooSlow", err)
	}
}

func TestContextCancellationDuringReplayClosesSubscription(t *testing.T) {
	source := newBlockingFakeSource(events(1))
	broker := mustBroker(t, source, 2, 1)
	ctx, cancel := context.WithCancel(context.Background())
	subscription := mustSubscribe(t, broker, ctx, 0)

	source.waitForReplayStart(t)
	cancel()
	requireClosed(t, subscription.Events)
	requireClosed(t, subscription.Errors)
}

func TestSendDoesNotDeliverAfterContextIsAlreadyCanceled(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 2, 1)
	ctx, cancel := context.WithCancel(context.Background())
	subscription := mustSubscribe(t, broker, ctx, 0)

	broker.mu.Lock()
	var registered *subscriber
	for _, candidate := range broker.subscribers {
		registered = candidate
	}
	if registered == nil {
		broker.mu.Unlock()
		t.Fatal("subscriber was not registered")
	}
	cancel()
	delivered := broker.sendLocked(registered, event(1))
	broker.mu.Unlock()

	if delivered {
		t.Fatal("send succeeded after context cancellation")
	}
	requireClosed(t, subscription.Events)
	requireClosed(t, subscription.Errors)
}

func TestSubscriptionCloseIsIdempotentAndStopsDelivery(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 2, 1)
	subscription := mustSubscribe(t, broker, context.Background(), 0)

	subscription.Close()
	subscription.Close()
	broker.Publish(event(1))
	requireClosed(t, subscription.Events)
	requireClosed(t, subscription.Errors)
}

func TestReplayFailureIsReportedAndClosesSubscription(t *testing.T) {
	source := newFakeSource(events(1))
	source.replayErr = errors.New("replay unavailable")
	broker := mustBroker(t, source, 2, 1)
	subscription := mustSubscribe(t, broker, context.Background(), 0)

	if err := readError(t, subscription.Errors); err == nil || err.Error() != "event replay failed" {
		t.Fatalf("replay error = %v", err)
	}
	requireClosed(t, subscription.Events)
	requireClosed(t, subscription.Errors)
}

func TestEmptyReplayPageBeforeWatermarkFailsClosed(t *testing.T) {
	source := newFakeSource(events(1))
	source.emptyReplay = true
	broker := mustBroker(t, source, 2, 1)
	subscription := mustSubscribe(t, broker, context.Background(), 0)

	if err := readError(t, subscription.Errors); err == nil || err.Error() != "event replay failed" {
		t.Fatalf("empty replay error = %v, want sanitized replay failure", err)
	}
	requireClosed(t, subscription.Events)
	requireClosed(t, subscription.Errors)
}

func TestWatermarkFailureDoesNotReturnSubscription(t *testing.T) {
	source := newFakeSource(nil)
	source.watermarkErr = errors.New("database details must not escape")
	broker := mustBroker(t, source, 2, 1)

	if subscription, err := broker.Subscribe(context.Background(), 0); subscription != nil || err == nil || err.Error() != "read event watermark" {
		t.Fatalf("Subscribe() = (%v, %v), want nil sanitized error", subscription, err)
	}
}

func TestSetupOverflowWhileWatermarkIsBlockedReturnsSubscriberTooSlow(t *testing.T) {
	source := newFakeSource(nil)
	source.blockWatermark = true
	broker := mustBroker(t, source, 1, 1)
	type subscribeResult struct {
		subscription *Subscription
		err          error
	}
	result := make(chan subscribeResult, 1)
	go func() {
		subscription, err := broker.Subscribe(context.Background(), 0)
		result <- subscribeResult{subscription: subscription, err: err}
	}()

	source.waitForWatermarkStart(t)
	broker.Publish(event(1))
	broker.Publish(event(2))

	select {
	case got := <-result:
		if got.subscription != nil || !errors.Is(got.err, ErrSubscriberTooSlow) {
			t.Fatalf("Subscribe() = (%v, %v), want nil, ErrSubscriberTooSlow", got.subscription, got.err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Subscribe did not return after setup overflow canceled Watermark")
	}
}

func TestCallerCancellationWhileWatermarkIsBlockedReturnsContextError(t *testing.T) {
	source := newFakeSource(nil)
	source.blockWatermark = true
	broker := mustBroker(t, source, 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	type subscribeResult struct {
		subscription *Subscription
		err          error
	}
	result := make(chan subscribeResult, 1)
	go func() {
		subscription, err := broker.Subscribe(ctx, 0)
		result <- subscribeResult{subscription: subscription, err: err}
	}()

	source.waitForWatermarkStart(t)
	cancel()

	select {
	case got := <-result:
		if got.subscription != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("Subscribe() = (%v, %v), want nil, context.Canceled", got.subscription, got.err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Subscribe did not return after caller canceled Watermark")
	}
}

func TestPublishCopiesPayloadForEverySubscriber(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 2, 1)
	first := mustSubscribe(t, broker, context.Background(), 0)
	defer first.Close()
	second := mustSubscribe(t, broker, context.Background(), 0)
	defer second.Close()

	published := event(1)
	want := cloneEvent(published).Payload
	broker.Publish(published)
	for index := range published.Payload {
		published.Payload[index] = 'x'
	}
	firstEvent := readEvent(t, first.Events)
	firstEvent.Payload[0] = 'y'
	secondEvent := readEvent(t, second.Events)
	if !reflect.DeepEqual(secondEvent.Payload, want) {
		t.Fatalf("second payload = %q, want independent copy %q", secondEvent.Payload, want)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	source := newFakeSource(nil)
	for _, values := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
		if broker, err := New(source, values[0], values[1]); broker != nil || err == nil {
			t.Errorf("New(queue=%d,page=%d) = (%v,%v), want nil,error", values[0], values[1], broker, err)
		}
	}
}

func TestConcurrentPublishSubscribeCancelAndClose(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 4, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup

	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for sequence := int64(1); sequence <= 500; sequence++ {
			broker.Publish(event(sequence))
		}
	}()
	for worker := range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 25; iteration++ {
				ctx, cancel := context.WithCancel(context.Background())
				subscription, err := broker.Subscribe(ctx, 0)
				if err != nil {
					if !errors.Is(err, ErrSubscriberTooSlow) {
						t.Errorf("worker %d Subscribe() error = %v", worker, err)
					}
					cancel()
					continue
				}
				if iteration%2 == 0 {
					cancel()
				} else {
					subscription.Close()
				}
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("concurrent operations did not finish")
	}
}

func TestBrokerCloseClosesAllSubscriptionsAndRejectsNewOnes(t *testing.T) {
	broker := mustBroker(t, newFakeSource(nil), 4, 2)
	first := mustSubscribe(t, broker, context.Background(), 0)
	second := mustSubscribe(t, broker, context.Background(), 0)
	first.Activate()
	second.Activate()

	if err := broker.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	requireClosed(t, first.Events)
	requireClosed(t, first.Errors)
	requireClosed(t, second.Events)
	requireClosed(t, second.Errors)
	if subscription, err := broker.Subscribe(context.Background(), 0); !errors.Is(err, ErrBrokerClosed) || subscription != nil {
		t.Fatalf("Subscribe after Close = %#v, %v", subscription, err)
	}
}
