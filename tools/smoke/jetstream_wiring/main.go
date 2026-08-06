// jetstream_wiring — manual smoke harness that mimics what channels +
// runtime do at boot: ensure the BIUMIND_CHANNELS stream and bind the
// runtime-channels-inbound durable consumer. After running this once,
// `deploy/scripts/smoke-jetstream.sh` should report green.
//
// Use case:
//   * You haven't yet deployed channels + runtime but want to verify
//     the broker side independently.
//   * You want to publish a message and watch it land in the consumer.
//
// Usage:
//
//	go run ./tools/smoke/jetstream_wiring -hold 30s
//
// The `-hold` flag keeps a consume loop running for that long, so you
// can publish into the subject from another terminal and see it ACKed.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

func main() {
	natsURL := flag.String("nats", envOr("NATS_URL", "nats://localhost:4222"), "NATS URL")
	env := flag.String("env", envOr("BIUMIND_ENV", "dev"), "deployment env tag")
	hold := flag.Duration("hold", 0, "keep consumer alive for this long; 0 = exit after wiring")
	target := flag.String("target", "channels", "which wiring to ensure: channels | brain")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	b, err := bus.Connect(*natsURL, "smoke-jetstream-wiring", *env)
	if err != nil {
		fail("connect: %v", err)
	}
	defer b.Close()
	js, err := b.JetStream()
	if err != nil {
		fail("jetstream init: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var streamName, streamSubj, filterSubj, durable string
	switch *target {
	case "channels":
		streamName = "BIUMIND_CHANNELS"
		streamSubj = bus.Subject(*env, "channels") + ".>"
		filterSubj = bus.Subject(*env, "channels", "inbound", ">")
		durable = "runtime-channels-inbound"
	case "brain":
		streamName = "BIUMIND_BRAIN"
		streamSubj = bus.Subject(*env, "brain") + ".>"
		filterSubj = bus.Subject(*env, "brain", ">")
		durable = "brain-graph-extractor"
	default:
		fail("unknown -target=%q (channels|brain)", *target)
	}

	if err := js.EnsureStream(ctx, bus.StreamSpec{
		Name:     streamName,
		Subjects: []string{streamSubj},
		MaxAge:   7 * 24 * time.Hour,
	}); err != nil {
		fail("ensure stream: %v", err)
	}
	logger.Info("stream ensured", "name", streamName, "subjects", streamSubj)

	sub, err := js.Subscribe(ctx, bus.ConsumerSpec{
		Stream:        streamName,
		Durable:       durable,
		FilterSubject: filterSubj,
	}, func(_ context.Context, m *bus.Message) error {
		logger.Info("got message", "subject", m.Subject, "len", len(m.Body))
		return nil
	})
	if err != nil {
		fail("subscribe: %v", err)
	}
	logger.Info("consumer bound; waiting", "filter", filterSubj, "durable", durable, "hold", *hold)

	if *hold == 0 {
		// Brief sleep so the broker has a moment to see the consumer
		// before we tear down — otherwise the script might exit before
		// jsz reflects the binding.
		time.Sleep(500 * time.Millisecond)
		_ = sub.Drain()
		fmt.Println("WIRING OK — stream + consumer ready for production traffic.")
		return
	}

	timer := time.NewTimer(*hold)
	defer timer.Stop()
	<-timer.C
	_ = sub.Drain()
	fmt.Println("hold elapsed; bye.")
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
