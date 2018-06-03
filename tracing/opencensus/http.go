package opencensus

import (
	"context"
	"net/http"

	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/plugin/ochttp/propagation/b3"
	"go.opencensus.io/trace"

	kithttp "github.com/go-kit/kit/transport/http"
)

// HTTPClientTrace enables OpenCensus tracing of a Go kit HTTP transport client.
func HTTPClientTrace(options ...TracerOption) kithttp.ClientOption {
	cfg := TracerOptions{
		sampler:       trace.AlwaysSample(),
		httpPropagate: &b3.HTTPFormat{},
	}

	for _, option := range options {
		option(&cfg)
	}

	clientBefore := kithttp.ClientBefore(
		func(ctx context.Context, req *http.Request) context.Context {
			var name string

			if cfg.name != "" {
				name = cfg.name
			} else {
				// OpenCensus states Path being default naming for a client span
				name = req.Method + " " + req.URL.Path
			}

			span := trace.NewSpan(
				name,
				trace.FromContext(ctx),
				trace.StartOptions{
					Sampler:  cfg.sampler,
					SpanKind: trace.SpanKindClient,
				},
			)

			span.AddAttributes(
				trace.StringAttribute(ochttp.HostAttribute, req.URL.Host),
				trace.StringAttribute(ochttp.MethodAttribute, req.Method),
				trace.StringAttribute(ochttp.PathAttribute, req.URL.Path),
				trace.StringAttribute(ochttp.UserAgentAttribute, req.UserAgent()),
			)

			if !cfg.public {
				cfg.httpPropagate.SpanContextToRequest(span.SpanContext(), req)
			}

			return trace.NewContext(ctx, span)
		},
	)

	clientAfter := kithttp.ClientAfter(
		func(ctx context.Context, res *http.Response) context.Context {
			if span := trace.FromContext(ctx); span != nil {
				span.SetStatus(ochttp.TraceStatus(res.StatusCode, http.StatusText(res.StatusCode)))
				span.AddAttributes(
					trace.Int64Attribute(ochttp.StatusCodeAttribute, int64(res.StatusCode)),
				)
			}
			return ctx
		},
	)

	clientFinalizer := kithttp.ClientFinalizer(
		func(ctx context.Context, err error) {
			if span := trace.FromContext(ctx); span != nil {
				if err != nil {
					span.SetStatus(trace.Status{
						Code:    trace.StatusCodeUnknown,
						Message: err.Error(),
					})
				}
				span.End()
			}
		},
	)

	return func(c *kithttp.Client) {
		clientBefore(c)
		clientAfter(c)
		clientFinalizer(c)
	}
}

// HTTPServerTrace enables OpenCensus tracing of a Go kit HTTP transport server.
func HTTPServerTrace(options ...TracerOption) kithttp.ServerOption {
	cfg := TracerOptions{
		sampler:       trace.AlwaysSample(),
		httpPropagate: &b3.HTTPFormat{},
	}

	for _, option := range options {
		option(&cfg)
	}

	serverBefore := kithttp.ServerBefore(
		func(ctx context.Context, req *http.Request) context.Context {
			var (
				spanContext trace.SpanContext
				span        *trace.Span
				name        string
				ok          bool
			)

			if cfg.name != "" {
				name = cfg.name
			} else {
				name = req.Method + " " + req.URL.Path
			}

			spanContext, ok = cfg.httpPropagate.SpanContextFromRequest(req)
			if ok && !cfg.public {
				ctx, span = trace.StartSpanWithRemoteParent(
					ctx,
					name,
					spanContext,
					trace.WithSpanKind(trace.SpanKindServer),
					trace.WithSampler(cfg.sampler),
				)
			} else {
				ctx, span = trace.StartSpan(
					ctx,
					name,
					trace.WithSpanKind(trace.SpanKindServer),
					trace.WithSampler(cfg.sampler),
				)
				if ok {
					span.AddLink(trace.Link{
						TraceID:    spanContext.TraceID,
						SpanID:     spanContext.SpanID,
						Type:       trace.LinkTypeChild,
						Attributes: nil,
					})
				}
			}

			span.AddAttributes(
				trace.StringAttribute(ochttp.MethodAttribute, req.Method),
				trace.StringAttribute(ochttp.PathAttribute, req.URL.Path),
			)

			return ctx
		},
	)

	serverFinalizer := kithttp.ServerFinalizer(
		func(ctx context.Context, code int, r *http.Request) {
			if span := trace.FromContext(ctx); span != nil {
				span.SetStatus(ochttp.TraceStatus(code, http.StatusText(code)))

				if rs, ok := ctx.Value(kithttp.ContextKeyResponseSize).(int64); ok {
					span.AddAttributes(
						trace.Int64Attribute("http.response_size", rs),
					)
				}

				span.End()
			}
		},
	)

	return func(s *kithttp.Server) {
		serverBefore(s)
		serverFinalizer(s)
	}
}
