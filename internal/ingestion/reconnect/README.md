# Reconnect Package

The `reconnect` package provides backoff strategies and a reconnection manager for handling transient connection failures.

## Strategy Interface

```go
type Strategy interface {
    NextDelay() (time.Duration, bool)  // Returns delay and whether to continue
    Reset()
}
```

## Backoff Strategies

### ExponentialBackoff

Exponential delay with +/-20% jitter. Supports unlimited retries when `maxRetries` is 0.

```go
backoff := reconnect.NewExponentialBackoff(
    1*time.Second,   // initialDelay
    30*time.Second,  // maxDelay
    2.0,             // multiplier
    5,               // maxRetries (0 = unlimited)
)

delay, shouldContinue := backoff.NextDelay()
backoff.Reset()  // Reset to initial state
```

### LinearBackoff

Fixed delay between retries. Supports unlimited retries when `maxRetries` is 0.

```go
backoff := reconnect.NewLinearBackoff(
    5*time.Second,  // delay (fixed)
    10,             // maxRetries (0 = unlimited)
)

delay, shouldContinue := backoff.NextDelay()
backoff.Reset()
```

## Manager

Orchestrates reconnection using a `Strategy` and callback functions.

```go
mgr := reconnect.NewManager(strategy, logrusLogger)

mgr.SetCallbacks(
    func(ctx context.Context) error { /* attempt connection */ },
    func() { /* on success */ },
    func(err error) { /* on permanent failure (retries exhausted) */ },
)

mgr.Start(ctx)  // Starts reconnection loop in a goroutine
mgr.Stop()      // Stops the reconnection loop
```

The manager loops through retry attempts using the strategy's `NextDelay()`. It stops when:
- The connection callback succeeds (calls `onSuccess`)
- The strategy signals no more retries (`shouldContinue = false`, calls `onFailure`)
- The context is cancelled
- `Stop()` is called

## Files

- `reconnect.go`: Strategy interface, ExponentialBackoff, LinearBackoff, Manager
- `reconnect_test.go`: Tests for backoff strategies and manager
