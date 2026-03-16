package main

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/resource"

)

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func setupOTelSDK(ctx context.Context) (func(context.Context) error, error) {
	var shutdownFuncs []func(context.Context) error // set of terminating logics
	var err error

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown := func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx)) // union of all errors
		}
		shutdownFuncs = nil // clear the terminating logics
		return err
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx)) // get the error and see if another errors are triggered from the cntext
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop) // register propagator

	// Set up trace provider.
	tracerProvider, err := newTracerProvider()
	if err != nil {
		handleErr(err)
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown) // register the terminating logic for tracerProvider
	otel.SetTracerProvider(tracerProvider) // register traceProvider

	// Set up meter provider - metrics
	meterProvider, err := newMeterProvider()
	if err != nil {
		handleErr(err)
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown) // register the terminatng logic for meterProvider
	otel.SetMeterProvider(meterProvider) // register the metrics

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider()
	if err != nil {
		handleErr(err)
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown) // register the terminating logic for loggerProvider
	global.SetLoggerProvider(loggerProvider) // register the logging

	return shutdown, nil
}

// acts for propagating context between spans
// The receiving service extracts the trace context from the request, creates a new span, and links it as a child of the previous span.

// 1️⃣ Service A creates a span
// 
// Suppose Service A receives a request and creates a span:
// 
// ctx, span := tracer.Start(ctx, "checkout")
// 
// Internally the context now contains something like:
// 
// trace_id = abc123
// span_id  = spanA
// 2️⃣ Service A calls Service B
// 
// Before sending the HTTP request, OpenTelemetry injects the trace context into headers:
// 
// otel.GetTextMapPropagator().Inject(ctx, req.Header)
// 
// Headers now contain something like:
// 
// traceparent: 00-abc123-spanA-01
// 
// Meaning:
// 
// trace_id = abc123
// parent_span_id = spanA
// 
// So the request carries the trace lineage.
// 
// 3️⃣ Service B receives the request
// 
// Service B extracts the trace context:
// 
// ctx := otel.GetTextMapPropagator().Extract(context.Background(), req.Header)
// 
// Now the context contains:
// 
// trace_id = abc123
// parent_span_id = spanA
// 
// This reconstructs the upstream trace state
// 
// parent_span_id = spanA

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// creating span context:
// - trace_id
// - span_id
// - parent_span_id
// - name
// - start_time
// - end_time
// - attributes
func newTracerProvider() (*trace.TracerProvider, error) {
    // traceExporter is responsible to send finished spans somewhere
	traceExporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, err
	}

    // metadata about the service
    res, _ := resource.Merge(
        resource.Default(), // detected atts, hostname, process.pid, os.type, telemetry sdk name
        resource.NewWithAttributes( // custom attrs
            semconv.SchemaURL,
            semconv.ServiceName("dice-service"),
        ),
    )

    // goroutine A → span.End()
    // goroutine B → span.End()
    // goroutine C → span.End()
    // 
    // All push spans into the same queue.

    // tracerProvider.Shutdown(ctx)
    // 
    // The SDK does:
    // 
    // flush remaining spans
    // stop exporter goroutine

	tracerProvider := trace.NewTracerProvider(
        trace.WithResource(res), // register metadata
		trace.WithBatcher(traceExporter, // store it as batches and flushes every second, a background goroutine is charged to flush the batch - batch Queue
			// Default is 5s. Set to 1s for demonstrative purposes.
			trace.WithBatchTimeout(time.Second)),
	)
	return tracerProvider, nil
}

// Metrics creation
// Example:
// {
//   "name": "http.server.duration",
//   "unit": "ms",
//   "attributes": {
//     "http.method": "GET",
//     "http.route": "/rolldice"
//   },
//   "timestamp": 1710600000,
//   "value": 23
// }
// Example usage:
// meter := otel.Meter("dice-service")
// 
// counter, _ := meter.Int64Counter("dice.rolls")
// 
// counter.Add(ctx, 1) // recording here
func newMeterProvider() (*metric.MeterProvider, error) {
	metricExporter, err := stdoutmetric.New(stdoutmetric.WithPrettyPrint())
	if err != nil {
		return nil, err
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter,
			// Default is 1m. Set to 3s for demonstrative purposes.
            // creates a background goroutine that its only work is to flush aggregated data
			metric.WithInterval(3*time.Second))), // every 3 seconds it will flush
	)
	return meterProvider, nil
}

// Experimental - loggin span
func newLoggerProvider() (*log.LoggerProvider, error) {
	logExporter, err := stdoutlog.New(stdoutlog.WithPrettyPrint())
	if err != nil {
		return nil, err
	}

	loggerProvider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
	)
	return loggerProvider, nil
}


