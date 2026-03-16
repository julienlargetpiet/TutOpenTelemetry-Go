package main

import (
	"io"
	"math/rand"
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// instrumentation lib name
const name = "go.opentelemetry.io/contrib/examples/dice"

var (
	tracer  = otel.Tracer(name)
	meter   = otel.Meter(name)
	logger  = otelslog.NewLogger(name) // login
	rollCnt metric.Int64Counter
)

func init() { // initialization of rollCnt
	var err error
	rollCnt, err = meter.Int64Counter("dice.rolls",
		metric.WithDescription("The number of rolls by roll value"),
		metric.WithUnit("{roll}"))
	if err != nil {
		panic(err)
	}
}

func rolldice(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "roll")
	defer span.End()

	roll := 1 + rand.Intn(6)

	var msg string
	if player := r.PathValue("player"); player != "" {
		msg = player + " is rolling the dice"
	} else {
		msg = "Anonymous player is rolling the dice"
	}
	logger.InfoContext(ctx, msg, "result", roll) // records result in the loggin

	rollValueAttr := attribute.Int("roll.value", roll)
	span.SetAttributes(rollValueAttr) // records a span

	rollCnt.Add(ctx, 1, metric.WithAttributes(rollValueAttr)) // records a measurment

	resp := strconv.Itoa(roll) + "\n"
	if _, err := io.WriteString(w, resp); err != nil {
		logger.ErrorContext(ctx, "Write failed", "error", err)
	}

    client := http.Client{ // manual injection, not handled by otelhttp midleware
        Transport: otelhttp.NewTransport(http.DefaultTransport), // inject trace headers, like: propagator.Inject(ctx, headers)
    }
    req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080/checkluck", nil)
    resp2, err := client.Do(req)
    if err != nil {
    	logger.ErrorContext(ctx, "downstream request failed", "error", err)
    } else {
    	resp2.Body.Close()
    }

    // if no otelhttp midleware
    //prop := otel.GetTextMapPropagator()
    //
    //req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080/checkluck", nil)
    //
    //// inject trace context into HTTP headers
    //prop.Inject(ctx, propagation.HeaderCarrier(req.Header))
    //
    //client := http.Client{}
    //resp2, err := client.Do(req)
    //if err != nil {
    //	logger.ErrorContext(ctx, "downstream request failed", "error", err)
    //} else {
    //	resp2.Body.Close()
    //}

}

func checkluck(w http.ResponseWriter, r *http.Request) {

	ctx, span := tracer.Start(r.Context(), "checkluck")
	defer span.End()

	logger.InfoContext(ctx, "checking luck")

	io.WriteString(w, "ok\n")
}

// if no otelhttp midleware
//func checkluck(w http.ResponseWriter, r *http.Request) {
//
//	prop := otel.GetTextMapPropagator()
//
//	// extract context from incoming headers
//	ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
//
//	ctx, span := tracer.Start(ctx, "checkluck")
//	defer span.End()
//
//	logger.InfoContext(ctx, "checking luck")
//
//	io.WriteString(w, "ok\n")
//}


