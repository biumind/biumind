# biumind (Go SDK)

Go client for [BiuMind Agentics](https://biumind.com).

Stdlib-only — no third-party runtime dependencies.

## Install

```bash
go get gitrelay.com/biumind/biumind/sdks/go
```

## Usage

```go
package main

import (
    "context"
    "fmt"

    biumind "gitrelay.com/biumind/biumind/sdks/go"
)

func main() {
    cfg, err := biumind.LoadConfig() // BIUMIND_MODEL_RELAY_URL + BIUMIND_TOKEN
    if err != nil { panic(err) }

    relay := biumind.NewRelayClient(cfg)
    chunks, errs := relay.MessagesStream(context.Background(),
        biumind.MessagesRequest{
            Model:    "claude-3-5-sonnet-latest",
            Messages: []biumind.Message{{Role: "user", Content: "hi"}},
        })
    for c := range chunks { fmt.Print(c) }
    for err := range errs { panic(err) }

    mem := biumind.NewMemoryClient(cfg)
    _, _ = mem.Store(context.Background(), "proj_x",
        "user prefers dark mode", biumind.StoreOptions{})
    r, _ := mem.Recall(context.Background(), "proj_x",
        "ui preference", biumind.RecallOptions{})
    for _, m := range r.Memories {
        fmt.Println(m.Score, m.Content)
    }
}
```

## Errors

```go
var be *biumind.Error
if errors.As(err, &be) {
    if be.IsRateLimit() { time.Sleep(be.RetryAfter) }
    if be.IsAuth()      { /* refresh token */ }
}
```
