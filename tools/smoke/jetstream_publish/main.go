// jetstream_publish — publish a fake inbound channel envelope to the
// JetStream stream so you can watch it land in the runtime consumer
// (or via `deploy/scripts/smoke-jetstream.sh`).
//
// Pairs with tools/smoke/jetstream_wiring/ (which ensures the stream
// + consumer). This one is purely the producer side.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

func main() {
	natsURL := flag.String("nats", envOr("NATS_URL", "nats://localhost:4222"), "NATS URL")
	env := flag.String("env", envOr("BIUMIND_ENV", "dev"), "deployment env tag")
	channel := flag.String("channel", "smoke", "channel name (telegram, slack, smoke, ...)")
	text := flag.String("text", "hello from jetstream_publish", "envelope text")
	count := flag.Int("count", 1, "how many messages to publish")
	flag.Parse()

	b, err := bus.Connect(*natsURL, "smoke-jetstream-publish", *env)
	if err != nil {
		fail("connect: %v", err)
	}
	defer b.Close()
	js, err := b.JetStream()
	if err != nil {
		fail("jetstream init: %v", err)
	}

	subj := bus.Subject(*env, "channels", "inbound", *channel)
	for i := 0; i < *count; i++ {
		body := map[string]any{
			"envelope": map[string]any{
				"message_id":      fmt.Sprintf("smoke-%d", i),
				"channel":         *channel,
				"conversation_id": "smoke-conv",
				"text":            *text,
				"sender": map[string]any{
					"platform_id":  "smoke-sender",
					"display_name": "Smoke Tester",
				},
			},
		}
		if err := js.Publish(context.Background(), subj, body); err != nil {
			fail("publish %d: %v", i, err)
		}
		fmt.Printf("✓ published #%d → %s\n", i+1, subj)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "smoke: "+format+"\n", a...)
	os.Exit(1)
}
