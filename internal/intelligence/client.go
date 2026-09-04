package intelligence

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type Client struct {
	api     anthropic.Client
	model   anthropic.Model
	timeout time.Duration
	sem     chan struct{}
	waiting int64
	maxWait int64
	breaker *breaker
}

func New(apiKey, model string) *Client {
	var opts []option.RequestOption
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if model == "" {
		model = "claude-opus-5"
	}
	return &Client{
		api:     anthropic.NewClient(opts...),
		model:   anthropic.Model(model),
		timeout: 10 * time.Second,
		sem:     make(chan struct{}, 2),
		maxWait: 10,
		breaker: newBreaker(3, 30*time.Second),
	}
}

type breaker struct {
	mu              sync.Mutex
	consecutiveFail int
	threshold       int
	cooldown        time.Duration
	openUntil       time.Time
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{threshold: threshold, cooldown: cooldown}
}

func (b *breaker) open() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().Before(b.openUntil)
}

func (b *breaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFail = 0
}

func (b *breaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFail++
	if b.consecutiveFail >= b.threshold {
		b.openUntil = time.Now().Add(b.cooldown)
	}
}

func (c *Client) acquire() (func(), bool) {
	waiting := atomic.AddInt64(&c.waiting, 1)
	if waiting > c.maxWait {
		atomic.AddInt64(&c.waiting, -1)
		return nil, false
	}
	c.sem <- struct{}{}
	atomic.AddInt64(&c.waiting, -1)
	return func() { <-c.sem }, true
}
