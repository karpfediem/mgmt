// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors

package tracing

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	envEndpoint    = "MGMT_OTLP_TRACES_ENDPOINT"
	envProtocol    = "MGMT_OTLP_TRACES_PROTOCOL"
	envInsecure    = "MGMT_OTLP_TRACES_INSECURE"
	envServiceName = "MGMT_OTLP_SERVICE_NAME"
	envSampleRatio = "MGMT_OTLP_TRACE_SAMPLE_RATIO"
)

var (
	mu      sync.RWMutex
	enabled bool
	tracer  = otel.Tracer("github.com/purpleidea/mgmt")
)

func Run(ctx context.Context, program, version string, logf func(format string, v ...interface{})) (func(context.Context) error, error) {
	endpoint := strings.TrimSpace(os.Getenv(envEndpoint))
	if endpoint == "" {
		setEnabled(false)
		return func(context.Context) error { return nil }, nil
	}

	protocol := strings.ToLower(strings.TrimSpace(os.Getenv(envProtocol)))
	if protocol == "" {
		protocol = "grpc"
	}
	insecure := parseBoolEnv(os.Getenv(envInsecure))
	sampleRatio, err := parseSampleRatio(os.Getenv(envSampleRatio))
	if err != nil {
		return nil, err
	}

	serviceName := strings.TrimSpace(os.Getenv(envServiceName))
	if serviceName == "" {
		serviceName = program
	}
	if serviceName == "" {
		serviceName = "mgmt"
	}

	var exporter sdktrace.SpanExporter
	switch protocol {
	case "grpc":
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
		if insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exporter, err = otlptracegrpc.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unknown OTLP traces protocol: %s (supported: grpc)", protocol)
	}
	if err != nil {
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio))),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(sdkresource.NewWithAttributes("",
			attribute.String("service.name", serviceName),
			attribute.String("service.version", version),
		)),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	mu.Lock()
	tracer = provider.Tracer("github.com/purpleidea/mgmt")
	enabled = true
	mu.Unlock()

	if logf != nil {
		logf("tracing: OTLP enabled endpoint=%s protocol=%s sample_ratio=%g", endpoint, protocol, sampleRatio)
	}

	return provider.Shutdown, nil
}

func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if !Enabled() {
		return ctx, trace.SpanFromContext(ctx)
	}

	mu.RLock()
	t := tracer
	mu.RUnlock()

	return t.Start(ctx, name, trace.WithAttributes(attrs...))
}

func setEnabled(value bool) {
	mu.Lock()
	defer mu.Unlock()
	enabled = value
	if !value {
		tracer = otel.Tracer("github.com/purpleidea/mgmt")
	}
}

func parseBoolEnv(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseSampleRatio(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1.0, nil
	}

	ratio, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", envSampleRatio, err)
	}
	if ratio < 0 || ratio > 1 {
		return 0, fmt.Errorf("invalid %s: must be between 0 and 1", envSampleRatio)
	}
	return ratio, nil
}
