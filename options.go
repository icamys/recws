package recws

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"time"
)

type connOptions struct {
	// ReconnectIntervalMin specifies the initial reconnecting interval,
	// default to 2 seconds
	ReconnectIntervalMin time.Duration
	// ReconnectIntervalMax specifies the maximum reconnecting interval,
	// default to 30 seconds
	ReconnectIntervalMax time.Duration
	// ReconnectIntervalFactor specifies the rate of increase of the reconnection
	// interval, default to 1.5
	ReconnectIntervalFactor float64
	// HandshakeTimeout specifies the duration for the handshake to complete,
	// default to 2 seconds
	HandshakeTimeout time.Duration
	// Proxy specifies the proxy function for the dialer
	// defaults to ProxyFromEnvironment
	Proxy func(*http.Request) (*url.URL, error)
	// Client TLS config to use on reconnect
	TLSClientConfig *tls.Config
	// OnConnectCallback fires after the connection successfully establish.
	OnConnectCallback func()
	// KeepAliveTimeout is an interval for sending ping/pong messages
	// disabled if 0
	KeepAliveTimeout time.Duration
	// LogFn is a set of functions to run for different logging levels
	LogFn logFnOptions
}

type logFnOptions struct {
	Debug func(string)
	Error func(error, string)
}

type Option func(c *connOptions)

func WithReconnectIntervalMin(interval time.Duration) Option {
	return func(o *connOptions) {
		o.ReconnectIntervalMin = interval
	}
}

func WithReconnectIntervalMax(interval time.Duration) Option {
	return func(o *connOptions) {
		o.ReconnectIntervalMax = interval
	}
}

func WithReconnectIntervalFactor(factor float64) Option {
	return func(o *connOptions) {
		o.ReconnectIntervalFactor = factor
	}
}

func WithHandshakeTimeout(to time.Duration) Option {
	return func(o *connOptions) {
		o.HandshakeTimeout = to
	}
}

func WithProxy(p func(*http.Request) (*url.URL, error)) Option {
	return func(o *connOptions) {
		o.Proxy = p
	}
}

func WithTLSClientConfig(c *tls.Config) Option {
	return func(o *connOptions) {
		o.TLSClientConfig = c
	}
}

func WithOnConnectCallback(cb func()) Option {
	return func(o *connOptions) {
		o.OnConnectCallback = cb
	}
}

func WithKeepAliveTimeout(kat time.Duration) Option {
	return func(o *connOptions) {
		o.KeepAliveTimeout = kat
	}
}

func WithDebugLogFn(fn func(string)) Option {
	return func(o *connOptions) {
		o.LogFn.Debug = fn
	}
}

func WithErrorLogFn(fn func(error, string)) Option {
	return func(o *connOptions) {
		o.LogFn.Error = fn
	}
}
