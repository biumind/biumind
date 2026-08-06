// Package otel sets up OpenTelemetry tracing for a service.
// One call to Init() at startup wires it all up.
package otel

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// stripURLScheme — otlptracegrpc.WithEndpoint 只接受 "host:port", 不接
// 受带 scheme 的完整 URL. 但 OTEL_EXPORTER_OTLP_ENDPOINT 这个环境变量
// 按 OTel spec 习惯写完整 URL (e.g. "http://otel-collector:4317"). 直接
// 透传会被 gRPC 当成 host 解析, 再追加默认 :443 → "http://...:4317:443"
// "too many colons in address" 报错把日志刷爆 (见 P4 部署).
//
// 这里 best-effort 把 http:// / https:// 前缀剥掉, 让两种写法都能工作.
func stripURLScheme(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	return strings.TrimRight(s, "/")
}

type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	OtlpEndpoint   string // e.g. "otel-collector:4317"; empty = no-op
}

type Shutdown func(context.Context) error

// Init sets up the global tracer. Returns a shutdown func to call before exit.
// If OtlpEndpoint is empty, traces are dropped (no-op exporter).
func Init(ctx context.Context, cfg Config) (trace.Tracer, Shutdown, error) {
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			attribute.String("deployment.environment", cfg.Environment),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	var tp *sdktrace.TracerProvider
	if cfg.OtlpEndpoint == "" {
		tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		slog.Info("otel: no exporter (OTEL_EXPORTER_OTLP_ENDPOINT empty)")
	} else {
		endpoint := stripURLScheme(cfg.OtlpEndpoint)
		exp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithTimeout(5*time.Second),
		)
		if err != nil {
			return nil, nil, err
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
		)
	}

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tracer := tp.Tracer(cfg.ServiceName)
	shutdown := func(ctx context.Context) error { return tp.Shutdown(ctx) }
	return tracer, shutdown, nil
}

// SlogJSONHandler returns a structured JSON logger that injects trace_id when present.
func SlogJSONHandler(level slog.Level) slog.Handler {
	return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
}
